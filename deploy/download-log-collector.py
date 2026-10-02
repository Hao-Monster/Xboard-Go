"""Download only this workflow's collector artifact with verified HTTPS/curl."""
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import tempfile
import time
import zipfile


def extract_verified(archive, digest, destination):
    if not re.fullmatch('sha256:[a-f0-9]{64}', digest):
        raise ValueError('Artifact digest missing')
    with archive.open('rb') as stream:
        actual = 'sha256:' + hashlib.file_digest(stream, 'sha256').hexdigest()
    if actual != digest:
        raise ValueError('Artifact digest mismatch')
    with zipfile.ZipFile(archive) as package:
        members = package.infolist()
        expected = {'collector.tar.gz', 'manifest.json'}
        if len(members) != 2 or {member.filename for member in members} != expected:
            raise ValueError('Unexpected artifact members')
        if any(member.file_size <= 0 or member.file_size > 512 * 1024 * 1024 for member in members):
            raise ValueError('Invalid artifact member size')
        destination.mkdir(parents=True, mode=0o700, exist_ok=True)
        for member in members:
            target = destination / member.filename
            if target.is_symlink():
                raise ValueError('Artifact target cannot be a symlink')
            with package.open(member) as source, target.open('wb') as output:
                shutil.copyfileobj(source, output)
            target.chmod(0o600)


def download_resumable(url, target, headers, expected_size, digest):
    if not isinstance(expected_size, int) or not 0 < expected_size <= 512 * 1024 * 1024:
        raise ValueError('Invalid artifact size')
    if not re.fullmatch('sha256:[a-f0-9]{64}', digest):
        raise ValueError('Artifact digest missing')

    def complete():
        size = target.stat().st_size if target.exists() else 0
        if size > expected_size:
            raise ValueError('Artifact exceeds expected size')
        if size != expected_size:
            return False
        with target.open('rb') as stream:
            actual = 'sha256:' + hashlib.file_digest(stream, 'sha256').hexdigest()
        if actual != digest:
            raise ValueError('Resumed artifact digest mismatch')
        return True

    for attempt in range(6):
        if complete():
            return
        result = subprocess.run(['curl', '--fail', '--silent', '--show-error', '--location', '--http1.1',
                                 '--proto', '=https', '--proto-redir', '=https', '--tlsv1.2',
                                 '--connect-timeout', '15', '--max-time', '300', '--continue-at', '-',
                                 '--max-filesize', str(512 * 1024 * 1024), '--header', '@' + str(headers),
                                 '--output', str(target), url], capture_output=True, timeout=320)
        size = target.stat().st_size if target.exists() else 0
        print(f'Collector artifact transfer attempt={attempt + 1} bytes={size} curl_exit={result.returncode}', flush=True)
        # Even a transport close error cannot bypass size and authenticated
        # GitHub digest verification. Partial bytes resume on the next attempt.
        if complete():
            return
        if attempt < 5:
            time.sleep(3)
    raise RuntimeError('Collector artifact transfer exhausted bounded resume attempts')


def main():
    if os.environ.get('GITHUB_REPOSITORY') != 'Hao-Monster/Xboard-Go':
        raise ValueError('Unexpected repository')
    run_id = os.environ['GITHUB_RUN_ID']
    if not run_id.isdigit():
        raise ValueError('Invalid run identity')
    token = os.environ['GH_TOKEN']
    if not token or '\n' in token or '\r' in token:
        raise ValueError('Invalid artifact credential')
    with tempfile.TemporaryDirectory(prefix='collector-download-') as work:
        directory = Path(work)
        headers = directory / 'headers'
        headers.write_text('Authorization: Bearer ' + token + '\nAccept: application/vnd.github+json\n')
        headers.chmod(0o600)

        def download(url, target):
            result = subprocess.run(['curl', '--fail', '--silent', '--show-error', '--location', '--http1.1',
                                     '--proto', '=https', '--proto-redir', '=https', '--tlsv1.2',
                                     '--connect-timeout', '15', '--max-time', '900', '--retry', '2',
                                     '--retry-all-errors', '--retry-delay', '3', '--retry-max-time', '1800',
                                     '--max-filesize', str(512 * 1024 * 1024), '--header', '@' + str(headers),
                                     '--output', str(target), url], capture_output=True, timeout=1850)
            if result.returncode:
                raise RuntimeError('Artifact HTTPS transfer failed (curl exit %d)' % result.returncode)

        base = 'https://api.github.com/repos/Hao-Monster/Xboard-Go/actions'
        metadata = directory / 'metadata.json'
        download(base + '/runs/' + run_id + '/artifacts?per_page=100', metadata)
        matches = [item for item in json.loads(metadata.read_text())['artifacts']
                   if item['name'] == 'tested-log-collector' and not item['expired']]
        if len(matches) != 1 or not isinstance(matches[0]['id'], int):
            raise ValueError('Expected one current-run collector artifact')
        item = matches[0]
        archive = directory / 'artifact.zip'
        download_resumable(base + '/artifacts/' + str(item['id']) + '/zip', archive, headers,
                           item.get('size_in_bytes'), item.get('digest', ''))
        extract_verified(archive, item.get('digest', ''), Path(os.environ['LOGGING_IMAGE_DIRECTORY']))
    print('Current-run collector artifact digest and archive contents verified')


if __name__ == '__main__':
    main()
