import hashlib
import io
import json
import os
from pathlib import Path
import runpy
import subprocess
import sys
import tarfile
import tempfile
import unittest
from unittest.mock import patch

SCRIPT = Path(__file__).with_name('fetch-node-bundle.py').resolve()


class FetchTests(unittest.TestCase):
    def test_verified_archive_and_rejection_boundaries(self):
        for scenario in ('valid', 'wrong_digest', 'traversal', 'download_failure'):
            with self.subTest(scenario=scenario), tempfile.TemporaryDirectory() as directory:
                data = io.BytesIO()
                names = ['manifest.json','SHA256SUMS','install.sh','xboard-node-linux-amd64','xboard-node-linux-arm64','xbctl-linux-amd64','xbctl-linux-arm64']
                with tarfile.open(fileobj=data, mode='w:gz') as archive:
                    for name in names:
                        member = tarfile.TarInfo('v1.14.4/'+name)
                        member.size = 1
                        archive.addfile(member, io.BytesIO(b'x'))
                    if scenario == 'traversal':
                        member = tarfile.TarInfo('../outside')
                        member.size = 1
                        archive.addfile(member, io.BytesIO(b'x'))
                raw = data.getvalue()
                root = Path(directory)
                (root/'deploy').mkdir()
                lock = {'version':'v1.14.4','sha256':hashlib.sha256(raw).hexdigest() if scenario != 'wrong_digest' else '0'*64,
                        'url':'https://github.com/Hao-Monster/Xboard-Go/releases/download/node-v1.14.4/node-release.tar.gz'}
                (root/'deploy/node-release.json').write_text(json.dumps(lock))
                previous = Path.cwd()
                try:
                    os.chdir(root)
                    def download(args, **kwargs):
                        self.assertEqual(args[0], 'curl')
                        self.assertIn('--fail', args)
                        self.assertIn('--proto-redir', args)
                        self.assertTrue(kwargs['check'])
                        self.assertEqual(kwargs['timeout'], 300)
                        if scenario == 'download_failure':
                            raise subprocess.CalledProcessError(28, args)
                        kwargs['stdout'].write(raw)
                    with patch.object(sys, 'argv', [str(SCRIPT), str(root/'output')]), patch('subprocess.run', side_effect=download):
                        if scenario == 'valid':
                            runpy.run_path(str(SCRIPT), run_name='__main__')
                            self.assertEqual({p.name for p in (root/'output/v1.14.4').iterdir()},set(names))
                        else:
                            with self.assertRaises(subprocess.CalledProcessError if scenario == 'download_failure' else AssertionError):
                                runpy.run_path(str(SCRIPT), run_name='__main__')
                            self.assertEqual(list((root/'output').iterdir()), [])
                finally:
                    os.chdir(previous)


if __name__ == '__main__':
    unittest.main()
