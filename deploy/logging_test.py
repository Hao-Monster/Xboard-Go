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


class BoundaryTests(unittest.TestCase):
    def setUp(self):
        self.environment = dict(GITHUB_REPOSITORY='Hao-Monster/Xboard-Go', GITHUB_REF='refs/heads/main',
                                GITHUB_EVENT_NAME='workflow_dispatch', GITHUB_SHA='a' * 40,
                                DEPLOY_HOST='109.205.178.211', DEPLOY_USER='root', DEPLOY_PORT='22',
                                BINGO_DEV_SSH_KEY='synthetic-key', DEPLOY_SSH_KEY='synthetic-key',
                                DEPLOY_KNOWN_HOSTS='synthetic-host-key')

    def test_manual_protected_main_only(self):
        runner.validate(self.environment)
        for name, value in [('GITHUB_REF', 'refs/heads/codex/ci-001-tiered-gates'), ('GITHUB_EVENT_NAME', 'push'),
                            ('GITHUB_REPOSITORY', 'other/repo'), ('GITHUB_SHA', 'main'),
                            ('DEPLOY_HOST', '127.0.0.1'), ('DEPLOY_PORT', '22;id'), ('DEPLOY_USER', 'other'),
                            ('DEPLOY_SSH_KEY', '')]:
            with self.subTest(name=name), self.assertRaises(ValueError):
                runner.validate({**self.environment, name: value})

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


if __name__ == '__main__':
    unittest.main()
