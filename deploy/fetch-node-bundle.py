"""Fetch the public, SHA256-pinned binary bundle for the development image."""
import hashlib
import json
from pathlib import Path
import re
import sys
import tarfile
import tempfile
import urllib.request

lock = json.loads(Path('deploy/node-release.json').read_text())
version, digest, url = (lock[k] for k in ('version', 'sha256', 'url'))
assert re.fullmatch(r'v[0-9]+\.[0-9]+\.[0-9]+', version)
assert re.fullmatch(r'[a-f0-9]{64}', digest)
assert url == f'https://github.com/Hao-Monster/Xboard-Go/releases/download/node-{version}/node-release.tar.gz'
destination = Path(sys.argv[1])
destination.mkdir(parents=True, exist_ok=False)
with tempfile.TemporaryFile() as temporary:
    calculated = hashlib.sha256()
    total = 0
    with urllib.request.urlopen(url, timeout=180) as response:
        while chunk := response.read(1024*1024):
            total += len(chunk)
            if total > 512*1024*1024:
                raise ValueError('Bundle exceeds size limit')
            calculated.update(chunk)
            temporary.write(chunk)
    assert calculated.hexdigest() == digest, 'Bundle SHA256 mismatch'
    temporary.seek(0)
    with tarfile.open(fileobj=temporary, mode='r:gz') as archive:
        expected = {version+'/'+name for name in ('manifest.json','SHA256SUMS','install.sh','xboard-node-linux-amd64','xboard-node-linux-arm64','xbctl-linux-amd64','xbctl-linux-arm64')}
        members = archive.getmembers()
        assert len(members) == len(expected) and {m.name for m in members} == expected
        assert all(m.isfile() and 0 < m.size <= 512*1024*1024 for m in members)
        archive.extractall(destination, filter='data')
print('Verified Node bundle: '+version)
