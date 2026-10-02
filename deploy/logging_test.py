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
downloader = module('download-log-collector')


class BoundaryTests(unittest.TestCase):
    def test_artifact_resume_preserves_partial_bytes_and_checks_final_digest(self):
        import hashlib
        import subprocess
        import tempfile
        from unittest.mock import patch
        with tempfile.TemporaryDirectory() as work:
            target = Path(work) / 'artifact.zip'
            observations = []
            def download(arguments, **options):
                self.assertIn('--continue-at', arguments)
                observations.append(target.read_bytes() if target.exists() else b'')
                with target.open('ab') as stream:
                    stream.write(b'abc' if len(observations) == 1 else b'def')
                return subprocess.CompletedProcess(arguments, 28 if len(observations) == 1 else 0)
            digest = 'sha256:' + hashlib.sha256(b'abcdef').hexdigest()
            with patch.object(downloader.subprocess, 'run', side_effect=download), patch.object(downloader.time, 'sleep'):
                downloader.download_resumable('https://fixture', target, Path(work) / 'headers', 6, digest)
            self.assertEqual(observations, [b'', b'abc'])
            self.assertEqual(target.read_bytes(), b'abcdef')
            target.write_bytes(b'badbad')
            with self.assertRaises(ValueError):
                downloader.download_resumable('https://fixture', target, Path(work) / 'headers', 6, digest)

    def test_artifact_digest_and_member_boundaries(self):
        import hashlib
        import tempfile
        import zipfile
        with tempfile.TemporaryDirectory() as work:
            directory = Path(work)
            archive = directory / 'artifact.zip'
            with zipfile.ZipFile(archive, 'w') as package:
                package.writestr('collector.tar.gz', b'image')
                package.writestr('manifest.json', b'{}')
            digest = 'sha256:' + hashlib.sha256(archive.read_bytes()).hexdigest()
            downloader.extract_verified(archive, digest, directory / 'out')
            self.assertEqual((directory / 'out/collector.tar.gz').read_bytes(), b'image')
            with self.assertRaises(ValueError):
                downloader.extract_verified(archive, 'sha256:' + '0' * 64, directory / 'bad')
            with zipfile.ZipFile(archive, 'w') as package:
                package.writestr('../escape', b'bad')
                package.writestr('manifest.json', b'{}')
            digest = 'sha256:' + hashlib.sha256(archive.read_bytes()).hexdigest()
            with self.assertRaises(ValueError):
                downloader.extract_verified(archive, digest, directory / 'bad')

    def test_portable_image_identity_rejects_content_changes(self):
        import copy
        image = dict(Id='classic-id', Architecture='amd64', Os='linux', Created='fixture-time',
                     RootFS=dict(Type='layers', Layers=['sha256:' + 'a' * 64]),
                     Config=dict(Entrypoint=['vector'], Env=['fixture=value']))
        expected = runner.fingerprint(image)
        self.assertEqual(expected, runner.fingerprint({**image, 'Id': 'containerd-id'}))
        defaults = dict(User='', WorkingDir='', Hostname='', Domainname='', Image='', Cmd=None,
                        Volumes=None, OnBuild=None, AttachStdin=False, AttachStdout=False,
                        AttachStderr=False, Tty=False, OpenStdin=False, StdinOnce=False)
        self.assertEqual(expected, runner.fingerprint({**image, 'Config': {**image['Config'], **defaults}}))
        self.assertNotEqual(expected, runner.fingerprint({**image, 'Config': {**image['Config'], 'User': '1001'}}))
        self.assertNotEqual(expected, runner.fingerprint({**image, 'Config': {**image['Config'], 'Tty': True}}))
        changed = copy.deepcopy(image)
        changed['Config']['Entrypoint'] = ['other']
        self.assertNotEqual(expected, runner.fingerprint(changed))
        changed = copy.deepcopy(image)
        changed['RootFS']['Layers'] = ['sha256:' + 'b' * 64]
        self.assertNotEqual(expected, runner.fingerprint(changed))
        with self.assertRaises(ValueError):
            runner.fingerprint({**image, 'Config': {}})

    def test_image_transfer_reopens_stream_and_does_not_print_stderr(self):
        import contextlib
        import io
        import subprocess
        import tempfile
        from unittest.mock import patch
        with tempfile.TemporaryDirectory() as work:
            path = Path(work) / 'image'
            path.write_bytes(b'image fixture')
            attempts = []
            def send(arguments, **options):
                attempts.append(options['stdin'].read())
                if len(attempts) == 1:
                    return subprocess.CompletedProcess(arguments, 255, b'', b'connection reset SECRET')
                return subprocess.CompletedProcess(arguments, 0, b'loaded', b'')
            output = io.StringIO()
            with patch.object(runner.subprocess, 'run', side_effect=send), patch.object(runner.time, 'sleep'), contextlib.redirect_stdout(output):
                self.assertEqual(runner.transfer_image(['fixture'], path, 'production'), b'loaded')
            self.assertEqual(attempts, [b'image fixture', b'image fixture'])
            self.assertNotIn('SECRET', output.getvalue())

    def test_collector_artifact_rejects_wrong_revision_and_corruption(self):
        import hashlib
        import json
        import tempfile
        with tempfile.TemporaryDirectory() as work:
            directory = Path(work)
            archive = directory / 'collector.tar.gz'
            archive.write_bytes(b'fixture-image')
            manifest = dict(revision='a' * 40, image_id='sha256:' + 'b' * 64, content_sha256='c' * 64,
                            sha256=hashlib.sha256(archive.read_bytes()).hexdigest())
            (directory / 'manifest.json').write_text(json.dumps(manifest))
            self.assertEqual(runner.image_manifest(directory, 'a' * 40), manifest)
            with self.assertRaises(ValueError):
                runner.image_manifest(directory, 'c' * 40)
            archive.write_bytes(b'corrupt-image')
            with self.assertRaises(ValueError):
                runner.image_manifest(directory, 'a' * 40)

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
