import json
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch
import deploy


def metadata(version='v0.1.1', digest='b', compatible='v0.1.0'):
    return dict(schema_version=1, version=version, revision='c' * 40,
                image=deploy.IMAGE + '@sha256:' + digest * 64, platforms=['linux/amd64'],
                automatic_upgrade=bool(compatible), compatible_from=compatible)


class PolicyTests(unittest.TestCase):
    def test_metadata_requires_exact_digest_sha_and_version(self):
        release = metadata()
        labels = {'org.opencontainers.image.version': release['version'], 'org.opencontainers.image.revision': release['revision']}
        deploy.validate_release(release, release['image'], labels)
        for field, bad in [('image', deploy.IMAGE + '@sha256:' + 'd' * 64), ('revision', 'd' * 40), ('version', 'v0.1.2')]:
            with self.subTest(field=field), self.assertRaises(ValueError):
                deploy.validate_release(dict(release, **{field: bad}), release['image'], labels)

    def test_compatibility_boundaries(self):
        target = metadata()
        for current, allowed in [('v0.1.0', True), ('v0.0.9', False), ('v0.1.1', False), ('v0.1.2', False), ('v1.0.0', False)]:
            with self.subTest(current=current):
                self.assertEqual(deploy.upgrade_allowed(metadata(current), target), allowed)
        self.assertFalse(deploy.upgrade_allowed(metadata('v0.1.0'), metadata(compatible=None)))

    def test_invalid_versions(self):
        for value in ['stable', 'v01.0.0', 'v0.1.0-alpha', 'v1.2.3\n', None]:
            with self.subTest(value=value), self.assertRaises(ValueError):
                deploy.version(value)

    def test_config_rejects_injection_and_non_owned_directory(self):
        config = dict(directory='/opt/xboard-go', project='xboard-go', url='https://example.com', email='test@example.com', port='7080', bind='127.0.0.1')
        deploy.validate_config(config)
        for key, value in [('directory', '/'), ('directory', '/opt/../etc'), ('url', 'https://example.com\nEVIL=yes'), ('project', '../x'), ('port', '65536')]:
            with self.subTest(key=key, value=value), self.assertRaises(ValueError):
                deploy.validate_config(dict(config, **{key: value}))


class DeploymentTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.directory = Path(self.temp.name) / 'app'
        self.config = dict(directory=str(self.directory), project='xboard-go', url='http://localhost', email='a@example.com', port='7080', bind='127.0.0.1')
        self.validation = patch.object(deploy, 'validate_config')
        self.validation.start()
        self.addCleanup(self.validation.stop)
        self.old = metadata('v0.1.0', 'a', None)
        self.target = metadata()
        self.calls = []
        self.failure = None
        self.running_image = self.old['image']

    def installed(self):
        self.directory.mkdir()
        (self.directory / 'installed.json').write_text(json.dumps(self.old))
        (self.directory / '.env').write_text('COMPOSE_PROJECT_NAME=xboard-go\nXBOARD_IMAGE=' + self.old['image'] + '\n')
        (self.directory / 'compose.yaml').write_text('services: {}\n')

    def fake_run(self, *args, capture=False):
        self.calls.append(args)
        if self.failure and self.failure in args:
            raise subprocess.CalledProcessError(1, args)
        if 'inspect' in args:
            return json.dumps([dict(Config=dict(Image=self.running_image), State=dict(Running=True, Health=dict(Status='healthy')))])
        if 'ps' in args:
            return 'container\n'
        if 'up' in args:
            self.running_image = self.target['image']
        return ''

    def test_schedule_never_installs(self):
        with patch.object(deploy, 'run', side_effect=self.fake_run):
            deploy.deploy(self.config, 'update', self.target)
        self.assertFalse(self.directory.exists())
        self.assertEqual(self.calls, [])

    def test_same_digest_only_inspects(self):
        self.installed()
        with patch.object(deploy, 'run', side_effect=self.fake_run):
            deploy.deploy(self.config, 'update', self.old)
        self.assertFalse(any('up' in call or 'stop' in call or 'pull' in call for call in self.calls))

    def test_drift_stops_before_mutation(self):
        self.installed()
        self.running_image = self.target['image']
        with patch.object(deploy, 'run', side_effect=self.fake_run), self.assertRaises(ValueError):
            deploy.deploy(self.config, 'update', self.target)
        self.assertFalse(any('stop' in call or 'pull' in call for call in self.calls))

    def test_project_drift_rejected_without_docker(self):
        self.installed()
        self.config['project'] = 'different-project'
        with patch.object(deploy, 'run', side_effect=self.fake_run), self.assertRaises(ValueError):
            deploy.deploy(self.config, 'update', self.target)
        self.assertEqual(self.calls, [])

    def test_incompatible_upgrade_preserves_service(self):
        self.installed()
        self.target['automatic_upgrade'] = False
        with patch.object(deploy, 'run', side_effect=self.fake_run), self.assertRaises(ValueError):
            deploy.deploy(self.config, 'update', self.target)
        self.assertFalse(any('stop' in call or 'pull' in call for call in self.calls))

    def test_state_commit_failure_stops_service_and_freezes(self):
        self.installed()
        with patch.object(deploy, 'run', side_effect=self.fake_run), patch.object(Path, 'replace', side_effect=OSError('disk full')), self.assertRaises(OSError):
            deploy.deploy(self.config, 'update', self.target)
        self.assertIn('stop', self.calls[-1])
        self.assertTrue((self.directory / 'deployment-failed').exists())
        self.assertEqual(json.loads((self.directory / 'installed.json').read_text()), self.old)

    def test_success_preserves_recovery_set_and_updates_state(self):
        self.installed()
        with patch.object(deploy, 'run', side_effect=self.fake_run):
            deploy.deploy(self.config, 'update', self.target)
        self.assertEqual(json.loads((self.directory / 'installed.json').read_text()), self.target)
        self.assertFalse((self.directory / 'deployment-failed').exists())
        backups = list(Path(self.temp.name).glob('app-backup-*'))
        self.assertEqual(len(backups), 1)
        self.assertEqual(json.loads((backups[0] / 'configuration/installed.json').read_text()), self.old)

    def test_copy_and_start_failures_stop_and_block_retry(self):
        for failure in ('cp', 'up'):
            with self.subTest(failure=failure):
                if self.directory.exists():
                    import shutil
                    shutil.rmtree(self.directory)
                self.installed()
                self.failure = failure
                self.calls = []
                with patch.object(deploy, 'run', side_effect=self.fake_run), self.assertRaises(Exception):
                    deploy.deploy(self.config, 'update', self.target)
                self.assertTrue((self.directory / 'deployment-failed').exists())
                self.assertEqual(json.loads((self.directory / 'installed.json').read_text()), self.old)
                self.assertIn('stop', self.calls[-1])
                self.calls = []
                with patch.object(deploy, 'run', side_effect=self.fake_run), self.assertRaises(ValueError):
                    deploy.deploy(self.config, 'update', self.target)
                self.assertEqual(self.calls, [])


if __name__ == '__main__':
    unittest.main()
