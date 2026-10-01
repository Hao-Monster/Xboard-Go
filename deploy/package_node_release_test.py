import hashlib
import importlib.util
import json
from pathlib import Path
import tarfile
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('bundle', Path(__file__).with_name('package-node-release.py'))
bundle = importlib.util.module_from_spec(spec)
spec.loader.exec_module(bundle)


class BundleTests(unittest.TestCase):
    def run_package(self, corrupt=False):
        with tempfile.TemporaryDirectory() as temporary:
            output = Path(temporary) / 'release'
            files = {name: ('fixture:' + name).encode() for name in bundle.FILES if name != 'SHA256SUMS'}
            files['SHA256SUMS'] = ''.join(hashlib.sha256(data).hexdigest()+'  '+name+'\n' for name, data in files.items()).encode()
            metadata = {'draft': False, 'tag_name': 'v1.14.4', 'html_url': 'https://example.test/release',
                        'assets': [{'name': name, 'digest': 'sha256:'+hashlib.sha256(data).hexdigest()} for name, data in files.items()]}

            def fake_gh(*args):
                if args[0] == 'api':
                    return json.dumps(metadata).encode()
                if args[:2] == ('release', 'download'):
                    for name, data in files.items():
                        (output / name).write_bytes(data)
                    if corrupt:
                        (output / 'install.sh').write_bytes(b'changed after publication')
                return b''

            def fake_download(metadata, output):
                fake_gh('release', 'download')

            with patch.object(bundle, 'gh', side_effect=fake_gh), patch.object(bundle, 'download_assets', side_effect=fake_download):
                if corrupt:
                    with self.assertRaisesRegex(ValueError, 'digest mismatch'):
                        bundle.package('v1.14.4', output)
                    self.assertFalse((output / 'node-release.tar.gz').exists())
                else:
                    bundle.package('v1.14.4', output)
                    public = json.loads((output / 'release.json').read_text())
                    self.assertEqual(public['tag_name'], 'v1.14.4')
                    self.assertEqual({a['name'] for a in public['assets']}, set(bundle.FILES))
                    for asset in public['assets']:
                        self.assertEqual(asset['url'], 'https://github.com/Hao-Monster/Xboard-Go/releases/download/node-v1.14.4/'+asset['name'])
                        self.assertEqual(asset['size'], len(files[asset['name']]))
                    with tarfile.open(output / 'node-release.tar.gz') as archive:
                        self.assertEqual(set(archive.getnames()), {'v1.14.4/'+name for name in (*bundle.FILES, 'manifest.json')})
                        manifest = json.load(archive.extractfile('v1.14.4/manifest.json'))
                        self.assertEqual(manifest['version'], 'v1.14.4')

    def test_verified_assets_only(self):
        self.run_package()

    def test_tampered_asset_rejected(self):
        self.run_package(corrupt=True)


if __name__ == '__main__':
    unittest.main()
