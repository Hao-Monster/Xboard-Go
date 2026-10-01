import importlib.util
from pathlib import Path
import unittest

spec = importlib.util.spec_from_file_location('runner', Path(__file__).with_name('production-runner.py'))
runner = importlib.util.module_from_spec(spec)
spec.loader.exec_module(runner)


class ValidationTests(unittest.TestCase):
    def setUp(self):
        self.config = dict(DEPLOY_HOST='203.0.113.1', DEPLOY_USER='deploy', DEPLOY_PORT='22',
                           DEPLOY_DIR='/opt/xboard-go', PANEL_URL='panel.example.test')

    def test_bare_domain_normalizes(self):
        self.assertEqual(runner.validate(self.config, 'inspect', 'a'*40, 'admin@example.test'), 'https://panel.example.test')

    def test_rejects_takeover_paths_and_shell_input(self):
        for key, value in [('DEPLOY_DIR', '/opt/remnawave'), ('PANEL_URL', 'https://example.test/xboard/'),
                           ('PANEL_URL', 'example.test;id'), ('DEPLOY_HOST', '-oProxyCommand=id'),
                           ('DEPLOY_USER', 'root;id'), ('DEPLOY_PORT', '65536')]:
            with self.subTest(key=key), self.assertRaises(ValueError):
                runner.validate({**self.config,key:value}, 'install', 'a'*40, 'admin@example.test')

    def test_exact_release_and_valid_mode(self):
        for mode, revision in [('upgrade','a'*40), ('install','main')]:
            with self.subTest(mode=mode), self.assertRaises(ValueError):
                runner.validate(self.config, mode, revision, 'admin@example.test')


if __name__ == '__main__':
    unittest.main()
