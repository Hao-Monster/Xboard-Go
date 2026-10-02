import importlib.util
from pathlib import Path
import unittest


def module(name):
    spec = importlib.util.spec_from_file_location(name, Path(__file__).with_name(name + '.py'))
    result = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(result)
    return result


runner = module('logging-runner')
remote = module('logging-remote')
probe = module('logging-probe')


class BoundaryTests(unittest.TestCase):
    def setUp(self):
        self.environment = dict(GITHUB_REPOSITORY='Hao-Monster/Xboard-Go', GITHUB_REF='refs/heads/main',
                                GITHUB_EVENT_NAME='workflow_run', LOGGING_SOURCE_SHA='a' * 40,
                                GITHUB_WORKFLOW='Legacy parity',
                                GITHUB_WORKFLOW_REF='Hao-Monster/Xboard-Go/.github/workflows/legacy-parity.yml@refs/heads/main',
                                DEPLOY_HOST='109.205.178.211', DEPLOY_USER='root', DEPLOY_PORT='22',
                                BINGO_DEV_SSH_KEY='synthetic-key', DEPLOY_SSH_KEY='synthetic-key',
                                DEPLOY_KNOWN_HOSTS='synthetic-host-key')

        self.event = dict(action='completed', workflow_run=dict(name='CI', event='push', head_branch='main',
                          head_repository=dict(full_name='Hao-Monster/Xboard-Go'), conclusion='success', head_sha='a' * 40))

    def test_trusted_main_ci_only(self):
        runner.validate(self.environment, self.event)
        for name, value in [('GITHUB_REF', 'refs/heads/codex/ci-001-tiered-gates'), ('GITHUB_EVENT_NAME', 'push'),
                            ('GITHUB_REPOSITORY', 'other/repo'), ('GITHUB_WORKFLOW', 'Private observability'),
                            ('GITHUB_WORKFLOW_REF', 'other'), ('LOGGING_SOURCE_SHA', 'main'),
                            ('DEPLOY_HOST', '127.0.0.1'), ('DEPLOY_PORT', '22;id'), ('DEPLOY_USER', 'other'),
                            ('DEPLOY_SSH_KEY', '')]:
            with self.subTest(name=name), self.assertRaises(ValueError):
                runner.validate({**self.environment, name: value}, self.event)

    def test_rejects_untrusted_source_completion(self):
        for field, value in [('event', 'pull_request'), ('head_branch', 'feature'), ('conclusion', 'failure'),
                             ('head_sha', 'b' * 40), ('name', 'Other'), ('head_repository', {'full_name': 'other/repo'})]:
            with self.subTest(field=field), self.assertRaises(ValueError):
                runner.validate(self.environment, {**self.event, 'workflow_run': {**self.event['workflow_run'], field: value}})
        with self.assertRaises(ValueError):
            runner.validate(self.environment, {**self.event, 'action': 'requested'})

    def test_restricted_tunnel_public_key(self):
        key = 'ssh-ed25519 AAAAsynthetic xboard-logs-production-tunnel'
        entry = remote.authorized_entry(key)
        for constraint in ['restrict,port-forwarding,', 'permitlisten="127.0.0.1:19428"',
                           'permitopen="127.0.0.1:19428"', 'command="/bin/false"']:
            self.assertIn(constraint, entry)
        self.assertTrue(entry.endswith(key))
        for invalid in [key + '\nssh-ed25519 other attacker', 'ssh-rsa AAAA comment', key.replace('AAAAsynthetic', 'AAAA,command=evil')]:
            with self.assertRaises(ValueError):
                remote.authorized_entry(invalid)

    def test_ssh_failure_cannot_print_secret_output(self):
        import sys
        with self.assertRaises(RuntimeError) as context:
            runner.execute([sys.executable, '-c', 'import sys; print("synthetic-sensitive-output"); sys.exit(2)'])
        self.assertNotIn('synthetic-sensitive-output', str(context.exception))

    def test_runtime_probe_drops_raw_health_and_error_payload(self):
        record = probe.runtime_record({'Status': 'exited', 'OOMKilled': True, 'ExitCode': 137,
                                       'Error': 'private-error', 'Health': {'Status': 'unhealthy', 'Log': ['private-health']}})
        self.assertEqual(record['exit_code'], 137)
        self.assertTrue(record['oom_killed'])
        self.assertNotIn('private', str(record))
        self.assertFalse(record['healthy'])

    def test_probe_metrics_aggregate_only_numeric_counters(self):
        record = probe.counters('vector_component_errors_total{component_id="private-fixture"} 4\n'
                                'vector_component_errors_total{component_id="another"} 2\n'
                                'vector_buffer_byte_size{component_id="logs"} 12345\n')
        self.assertEqual(record['collector_errors'], 6)
        self.assertEqual(record['buffer_bytes'], 12345)
        self.assertNotIn('private', str(record))


if __name__ == '__main__':
    unittest.main()
