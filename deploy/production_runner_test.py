import importlib.util
from pathlib import Path
import unittest
import subprocess
import sys
import tempfile
import json

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
                           ('PANEL_URL', 'https://example.test:9443'), ('PANEL_URL', 'example.test;id'), ('DEPLOY_HOST', '-oProxyCommand=id'),
                           ('DEPLOY_USER', 'root;id'), ('DEPLOY_PORT', '65536')]:
            with self.subTest(key=key), self.assertRaises(ValueError):
                runner.validate({**self.config,key:value}, 'install', 'a'*40, 'admin@example.test')

    def test_explicit_caddy_port_and_resume(self):
        self.assertEqual(runner.validate({**self.config, 'PANEL_URL':'https://fast.hjy.ca:8443'}, 'resume', 'a'*40, 'admin@example.test'), 'https://fast.hjy.ca:8443')

    def test_update_accepts_exact_release(self):
        self.assertEqual(runner.validate(self.config, 'update', 'a'*40, 'admin@example.test'), 'https://panel.example.test')

    def test_exact_release_and_valid_mode(self):
        for mode, revision in [('unknown','a'*40), ('install','main')]:
            with self.subTest(mode=mode), self.assertRaises(ValueError):
                runner.validate(self.config, mode, revision, 'admin@example.test')



class RetainedConfigurationTests(unittest.TestCase):
    def test_resume_preserves_credentials_and_rejects_changed_installation(self):
        script=Path(__file__).with_name('production-server.sh').read_text().split("<<'PYENV'\n",1)[1].split('\nPYENV',1)[0]
        original=('COMPOSE_PROJECT_NAME=xboard-production-internal\nXBOARD_IMAGE=xboard-go:'+'a'*40+
                  '\nXBOARD_ADMIN_EMAIL=admin@fast.hjy.ca\nXBOARD_PORT=7080\nXBOARD_BIND_ADDRESS=127.0.0.1\nXBOARD_COOKIE_SECURE=true\nXBOARD_ADMIN_PATH='+'b'*48+'\nXBOARD_PANEL_URL=https://fast.hjy.ca\n')
        for content,success in [(original,True),(original.replace('7080','3000'),False),(original+'EVIL=value\n',False),
                                (original.replace('xboard-production-internal','remnawave'),False)]:
            with self.subTest(success=success), tempfile.TemporaryDirectory() as directory:
                path=Path(directory)/'.env';path.write_text(content)
                result=subprocess.run([sys.executable,'-',str(path),'xboard-production-internal','a'*40,'https://fast.hjy.ca:8443','admin@fast.hjy.ca'],input=script,text=True,capture_output=True)
                self.assertEqual(result.returncode==0,success)
                self.assertEqual(path.read_text(),content.replace('XBOARD_PANEL_URL=https://fast.hjy.ca\n','XBOARD_PANEL_URL=https://fast.hjy.ca:8443\n') if success else content)
                if success: self.assertEqual(path.with_name('.env.before-8443').read_text(),original)

class UpdateConfigurationTests(unittest.TestCase):
    def test_selected_release_rejects_wrong_version_missing_and_foreign_assets(self):
        script=Path(__file__).with_name('production-update.sh').read_text().split("<<'PYNODE'\n",1)[1].split('\nPYNODE',1)[0]
        base='https://github.com/Hao-Monster/Xboard-Go/releases/download/node-v1.14.5'
        names=['install.sh','SHA256SUMS','xboard-node-linux-amd64','xboard-node-linux-arm64','xbctl-linux-amd64','xbctl-linux-arm64']
        valid={'tag_name':'v1.14.5','assets':[{'name':n,'url':base+'/'+n,'size':1,'state':'uploaded'} for n in names]}
        wrong_host=json.loads(json.dumps(valid));wrong_host['assets'][0]['url']='https://example.test/install.sh'
        for release,ok in [(valid,True),({**valid,'tag_name':'v99.99.99'},False),({**valid,'assets':valid['assets'][1:]},False),(wrong_host,False),({**valid,'draft':True},False)]:
            with self.subTest(ok=ok), tempfile.TemporaryDirectory() as directory:
                path=Path(directory)/'release.json';path.write_text(json.dumps(release))
                result=subprocess.run([sys.executable,'-',str(path),'v1.14.5',base],input=script,text=True,capture_output=True)
                self.assertEqual(result.returncode==0,ok)

    def test_node_selection_is_explicit_and_validated(self):
        runner.validate_node_selection('update', 'v1.14.5', 'github')
        runner.validate_node_selection('update', '', 'preserve')
        for mode, version, source in [('install','v1.14.5','github'), ('update','','github'), ('update','v1.14.5','preserve'), ('update','v1.14.5;id','github')]:
            with self.subTest(version=version, source=source), self.assertRaises(ValueError):
                runner.validate_node_selection(mode,version,source)

    def test_node_update_preserves_secrets_and_replaces_existing_selection(self):
        script=Path(__file__).with_name('production-update.sh').read_text().split("<<'PY'\n",1)[1].split('\nPY',1)[0]
        original='XBOARD_IMAGE=xboard-go:'+'a'*40+'\nXBOARD_PANEL_URL=https://fast.hjy.ca:8443\nXBOARD_ADMIN_PATH=preserved\nCOMPOSE_PROJECT_NAME=xboard-production-internal\nXBOARD_PORT=7080\nXBOARD_BIND_ADDRESS=127.0.0.1\nXBOARD_NODE_RELEASE=v1.14.4\nXBOARD_NODE_RELEASE_SOURCE=panel\n'
        with tempfile.TemporaryDirectory() as directory:
            path=Path(directory)/'.env';path.write_text(original)
            output=path.with_name('next.env')
            result=subprocess.run([sys.executable,'-',str(path),'a'*40,'b'*40,'https://fast.hjy.ca:8443',str(output),'v1.14.5','github'],input=script,text=True,capture_output=True)
            self.assertEqual(result.returncode,0,result.stderr)
            self.assertEqual(path.read_text(),original)
            self.assertEqual(output.read_text(),original.replace('a'*40,'b'*40).replace('v1.14.4','v1.14.5').replace('SOURCE=panel','SOURCE=github'))

    def test_image_update_preserves_other_configuration_and_rejects_mismatch(self):
        script=Path(__file__).with_name('production-update.sh').read_text().split("<<'PY'\n",1)[1].split('\nPY',1)[0]
        original='XBOARD_IMAGE=xboard-go:'+'a'*40+'\nXBOARD_PANEL_URL=https://fast.hjy.ca:8443\nXBOARD_ADMIN_PATH=preserved\nCOMPOSE_PROJECT_NAME=xboard-production-internal\nXBOARD_PORT=7080\nXBOARD_BIND_ADDRESS=127.0.0.1\n'
        for content,ok in [(original,True),(original.replace('xboard-go:','other:'),False),(original+original,False)]:
            with self.subTest(ok=ok), tempfile.TemporaryDirectory() as directory:
                path=Path(directory)/'.env';path.write_text(content)
                result=subprocess.run([sys.executable,'-',str(path),'a'*40,'b'*40,'https://fast.hjy.ca:8443',str(path.with_name('next.env'))],input=script,text=True,capture_output=True)
                self.assertEqual(result.returncode==0,ok)
                self.assertEqual(path.read_text(),content)
                if ok: self.assertEqual(path.with_name('next.env').read_text(),content.replace('xboard-go:'+'a'*40,'xboard-go:'+'b'*40))
                else: self.assertFalse(path.with_name('next.env').exists())

if __name__ == '__main__':
    unittest.main()
