"""Fetch the public, SHA256-pinned binary bundle for the development image."""
import hashlib
import json
from pathlib import Path
import re
import subprocess
import sys
import tarfile
import tempfile

lock = json.loads(Path('deploy/node-release.json').read_text())
version, digest, url = (lock[k] for k in ('version', 'sha256', 'url'))
assert re.fullmatch(r'v[0-9]+\.[0-9]+\.[0-9]+', version)
assert re.fullmatch(r'[a-f0-9]{64}', digest)
assert url == f'https://github.com/Hao-Monster/Xboard-Go/releases/download/node-{version}/node-release.tar.gz'
destination = Path(sys.argv[1])
destination.mkdir(parents=True, exist_ok=False)
with tempfile.TemporaryDirectory() as work:
    # Use the same curl transport as the runner's source download. urllib's
    # connection path timed out on this runner while curl reached the asset.
    # Bound network retries and retain checksum verification before extraction.
    subprocess.run(['curl', '--fail', '--location', '--silent', '--show-error',
                    '--proto', '=https', '--proto-redir', '=https',
                    # The development runner transferred 28 MB of this 49 MB
                    # bundle in 180 seconds. Allow a complete slow transfer.
                    '--connect-timeout', '15', '--max-time', '600',
                    '--retry', '2', '--retry-max-time', '900',
                    '--max-filesize', str(512*1024*1024),
                    '--output', str(Path(work)/'bundle.tar.gz'), url],
                   check=True, timeout=1500)
    # curl owns the output path so a retry truncates a partial previous attempt.
    temporary = Path(work)/'bundle.tar.gz'
    calculated = hashlib.sha256()
    total = 0
    with temporary.open('rb') as downloaded:
        while chunk := downloaded.read(1024*1024):
            total += len(chunk)
            if total > 512*1024*1024:
                raise ValueError('Bundle exceeds size limit')
            calculated.update(chunk)
    assert calculated.hexdigest() == digest, 'Bundle SHA256 mismatch'
    with tarfile.open(temporary, mode='r:gz') as archive:
        expected = {version+'/'+name for name in ('manifest.json','SHA256SUMS','install.sh','xboard-node-linux-amd64','xboard-node-linux-arm64','xbctl-linux-amd64','xbctl-linux-arm64')}
        members = archive.getmembers()
        assert len(members) == len(expected) and {m.name for m in members} == expected
        assert all(m.isfile() and 0 < m.size <= 512*1024*1024 for m in members)
        archive.extractall(destination, filter='data')
print('Verified Node bundle: '+version)
