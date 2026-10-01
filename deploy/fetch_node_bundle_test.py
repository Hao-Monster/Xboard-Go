import hashlib
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import io
import json
import os
from pathlib import Path
import runpy
import subprocess
import sys
import tarfile
import tempfile
import threading
import unittest
from unittest.mock import patch

SCRIPT = Path(__file__).with_name('fetch-node-bundle.py').resolve()


class FetchTests(unittest.TestCase):
    def test_real_curl_retry_replaces_partial_download(self):
        data = io.BytesIO()
        names = ['manifest.json','SHA256SUMS','install.sh','xboard-node-linux-amd64','xboard-node-linux-arm64','xbctl-linux-amd64','xbctl-linux-arm64']
        with tarfile.open(fileobj=data, mode='w:gz') as archive:
            for name in names:
                member = tarfile.TarInfo('v1.14.4/'+name)
                member.size = 1
                archive.addfile(member, io.BytesIO(b'x'))
        raw = data.getvalue()
        release_first = threading.Event()
        attempts = []
        class Handler(BaseHTTPRequestHandler):
            def log_message(self, *args):
                pass
            def do_GET(self):
                attempts.append(self.path)
                self.send_response(200)
                self.send_header('Content-Length', str(len(raw)))
                self.end_headers()
                if len(attempts) == 1:
                    self.wfile.write(raw[:16])
                    self.wfile.flush()
                    release_first.wait(10)
                else:
                    self.wfile.write(raw)
        server = ThreadingHTTPServer(('127.0.0.1', 0), Handler)
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        real_run = subprocess.run
        def local_download(args, **kwargs):
            # Exercise the actual transport/output arguments against an isolated
            # HTTP fixture. Production URL and HTTPS policy stay unchanged.
            args = list(args)
            args[-1] = f'http://127.0.0.1:{server.server_port}/bundle'
            for flag, value in (('--proto','=http'),('--proto-redir','=http'),('--max-time','1'),('--retry-max-time','5')):
                args[args.index(flag)+1] = value
            args[1:1] = ['--noproxy', '*']
            return real_run(args, **kwargs, stderr=subprocess.PIPE)
        previous = Path.cwd()
        try:
            with tempfile.TemporaryDirectory() as directory:
                root = Path(directory)
                (root/'deploy').mkdir()
                (root/'deploy/node-release.json').write_text(json.dumps({
                    'version':'v1.14.4','sha256':hashlib.sha256(raw).hexdigest(),
                    'url':'https://github.com/Hao-Monster/Xboard-Go/releases/download/node-v1.14.4/node-release.tar.gz'}))
                os.chdir(root)
                try:
                    with patch.object(sys, 'argv', [str(SCRIPT), str(root/'output')]), patch('subprocess.run', side_effect=local_download):
                        runpy.run_path(str(SCRIPT), run_name='__main__')
                    self.assertGreaterEqual(len(attempts), 2)
                    self.assertEqual({p.name for p in (root/'output/v1.14.4').iterdir()}, set(names))
                finally:
                    os.chdir(previous)
        finally:
            os.chdir(previous)
            release_first.set()
            server.shutdown()
            server.server_close()
            thread.join(timeout=5)

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
                        self.assertNotIn('stdout', kwargs)
                        Path(args[args.index('--output')+1]).write_bytes(raw)
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
