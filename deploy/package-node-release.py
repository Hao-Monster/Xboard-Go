"""Mirror verified Node CI release binaries for token-free panel image builds.

Maintainer-only: gh authenticates to the private source repository. No token or
private source code is included in the bundle, and nodes never run this tool.
"""
import argparse
import gzip
import hashlib
import io
import json
from pathlib import Path
import re
import subprocess
import tarfile


FILES = ('install.sh', 'xboard-node-linux-amd64', 'xboard-node-linux-arm64',
         'xbctl-linux-amd64', 'xbctl-linux-arm64', 'SHA256SUMS')
SOURCE = 'Hao-Monster/Xboard-Node'


def gh(*args):
    return subprocess.check_output(['gh', *args])


def package(version, output):
    if not re.fullmatch(r'v[0-9]+\.[0-9]+\.[0-9]+', version):
        raise ValueError('An exact stable version is required')
    output.mkdir(parents=True, exist_ok=False)
    metadata = json.loads(gh('api', f'repos/{SOURCE}/releases/tags/{version}'))
    if metadata['draft'] or metadata['tag_name'] != version:
        raise ValueError('Expected a published release')
    # GitHub's immutable-release attestation binds the published assets.
    gh('release', 'verify', version, '--repo', SOURCE)
    gh('release', 'download', version, '--repo', SOURCE, '--dir', str(output),
       *[arg for name in FILES for arg in ('--pattern', name)])
    assets = {item['name']: item for item in metadata['assets']}
    for name in FILES:
        digest = hashlib.sha256((output / name).read_bytes()).hexdigest()
        if assets[name].get('digest') != 'sha256:' + digest:
            raise ValueError('GitHub asset digest mismatch: ' + name)
    checksums = {}
    for line in (output / 'SHA256SUMS').read_text().splitlines():
        match = re.fullmatch(r'([a-f0-9]{64}) [ *]([^/\\]+)', line)
        if not match or match[2] in checksums:
            raise ValueError('Invalid or duplicate checksum entry')
        checksums[match[2]] = match[1]
    if set(checksums) != set(FILES) - {'SHA256SUMS'}:
        raise ValueError('Unexpected release file list')
    for name, digest in checksums.items():
        if hashlib.sha256((output / name).read_bytes()).hexdigest() != digest:
            raise ValueError('Release checksum mismatch: ' + name)
    manifest = {'version': version, 'source': metadata['html_url'],
                'artifacts': [{'name': name} for name in FILES if name != 'SHA256SUMS']}
    (output / 'manifest.json').write_text(json.dumps(manifest, indent=2)+'\n', encoding='utf-8')
    archive = output / 'node-release.tar.gz'
    with archive.open('wb') as raw, gzip.GzipFile(filename='', fileobj=raw, mode='wb', mtime=0) as compressed:
        with tarfile.open(fileobj=compressed, mode='w') as bundle:
            for name in (*FILES, 'manifest.json'):
                data = (output / name).read_bytes()
                info = tarfile.TarInfo(version + '/' + name)
                info.size = len(data)
                info.mode = 0o644
                bundle.addfile(info, io.BytesIO(data))
    print(json.dumps({'archive': str(archive.resolve()),
                      'sha256': hashlib.sha256(archive.read_bytes()).hexdigest(),
                      'source_release': metadata['html_url']}))


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--version', required=True)
    parser.add_argument('--output', type=Path, required=True)
    args = parser.parse_args()
    package(args.version, args.output)
