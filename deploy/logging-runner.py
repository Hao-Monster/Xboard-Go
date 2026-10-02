"""GitHub Actions deployment of the private log center and panel collectors."""
import io
import hashlib
import json
import os
from pathlib import Path
import re
import shlex
import subprocess
import tarfile
import tempfile
import time


def execute(command, data=None):
    result = subprocess.run(command, input=data, capture_output=True)
    if result.returncode:
        # Remote stderr/configuration may contain credentials. Diagnose on host.
        raise RuntimeError('Scoped logging deployment command failed (exit %d); inspect private host diagnostics' % result.returncode)
    return result.stdout


def transfer_image(arguments, image_file, kind):
    # A broken stream can leave incomplete layers, but Docker only publishes the
    # image after complete import; the caller checks its exact identity as well.
    for attempt in range(3):
        try:
            with image_file.open('rb') as stream:
                result = subprocess.run(arguments, stdin=stream, capture_output=True, timeout=600)
            if result.returncode == 0:
                return result.stdout
            diagnostic = result.stderr.decode(errors='replace').lower()
            reason = next((name for name in ('connection reset', 'broken pipe', 'unexpected eof',
                           'no space left', 'permission denied', 'connection closed', 'timed out')
                           if name in diagnostic), 'unclassified')
            print(f'Collector transfer {kind}: exit={result.returncode} reason={reason} attempt={attempt + 1}', flush=True)
        except subprocess.TimeoutExpired:
            print(f'Collector transfer {kind}: timeout attempt={attempt + 1}', flush=True)
        if attempt < 2:
            time.sleep(5)
    raise RuntimeError('Collector image transfer exhausted bounded retries')


def validate(environment, event):
    if (environment.get('GITHUB_REPOSITORY') != 'Hao-Monster/Xboard-Go' or
            environment.get('GITHUB_REF') != 'refs/heads/main' or
            environment.get('GITHUB_EVENT_NAME') != 'workflow_run' or
            environment.get('GITHUB_WORKFLOW') != 'Legacy parity' or
            environment.get('GITHUB_WORKFLOW_REF') != 'Hao-Monster/Xboard-Go/.github/workflows/legacy-parity.yml@refs/heads/main'):
        raise ValueError('Deployment requires the trusted main CI completion workflow')
    source = event.get('workflow_run', {})
    if (event.get('action') != 'completed' or source.get('name') != 'CI' or
            source.get('event') != 'push' or source.get('head_branch') != 'main' or
            source.get('head_repository', {}).get('full_name') != 'Hao-Monster/Xboard-Go' or
            source.get('conclusion') != 'success' or source.get('head_sha') != environment.get('LOGGING_SOURCE_SHA')):
        raise ValueError('Untrusted or mismatched source workflow')
    if not re.fullmatch('[0-9a-f]{40}', environment.get('LOGGING_SOURCE_SHA', '')):
        raise ValueError('An exact source SHA is required')
    if (environment.get('DEPLOY_HOST') != '109.205.178.211' or
            environment.get('DEPLOY_USER') != 'root' or
            environment.get('DEPLOY_PORT', '22') not in ('', '22')):
        raise ValueError('Unexpected production internal-test SSH target')
    for name in ('BINGO_DEV_SSH_KEY', 'DEPLOY_SSH_KEY', 'DEPLOY_KNOWN_HOSTS'):
        if not environment.get(name):
            raise ValueError('Required SSH secret is unavailable: ' + name)


def archive():
    buffer = io.BytesIO()
    with tarfile.open(fileobj=buffer, mode='w:gz') as output:
        files = [Path('deploy/logging-remote.py'), Path('deploy/logging-probe.py'), *Path('deploy/observability').glob('*')]
        for path in files:
            if path.is_file() and (path.suffix in ('.py', '.yaml', '.json', '.sh') or path.name == 'Dockerfile.collector'):
                output.add(path, arcname=str(path).replace('\\', '/'), recursive=False)
    return buffer.getvalue()


def image_manifest(directory, sha):
    manifest = json.loads((directory / 'manifest.json').read_text())
    if manifest.get('revision') != sha or not re.fullmatch('sha256:[a-f0-9]{64}', manifest.get('image_id', '')):
        raise ValueError('Collector artifact identity mismatch')
    with (directory / 'collector.tar.gz').open('rb') as source:
        digest = hashlib.file_digest(source, 'sha256').hexdigest()
    if digest != manifest.get('sha256'):
        raise ValueError('Collector artifact checksum mismatch')
    return manifest


def main():
    validate(os.environ, json.loads(Path(os.environ['GITHUB_EVENT_PATH']).read_text()))
    sha = os.environ['LOGGING_SOURCE_SHA']
    image_directory = Path(os.environ['LOGGING_IMAGE_DIRECTORY'])
    manifest = image_manifest(image_directory, sha)
    with tempfile.TemporaryDirectory(prefix='xboard-logging-') as work:
        directory = Path(work)
        keys = {}
        for kind, variable in [('central', 'BINGO_DEV_SSH_KEY'), ('production', 'DEPLOY_SSH_KEY')]:
            keys[kind] = directory / (kind + '.key')
            keys[kind].write_text(os.environ[variable].strip() + '\n')
            keys[kind].chmod(0o600)
        hosts = directory / 'known_hosts'
        # The central SSH hop is loopback inside the existing self-hosted runner
        # network, not an Internet key discovery. Production is strictly pinned.
        loopback = execute(['ssh-keyscan', '-T', '5', '127.0.0.1'])
        hosts.write_bytes(loopback + os.environ['DEPLOY_KNOWN_HOSTS'].encode() + b'\n')
        hosts.chmod(0o600)

        def ssh(kind, command, data=None, image_file=None):
            target = 'bingo@127.0.0.1' if kind == 'central' else 'root@109.205.178.211'
            arguments = ['ssh', '-i', str(keys[kind]), '-o', 'IdentitiesOnly=yes', '-o', 'BatchMode=yes',
                            '-o', 'StrictHostKeyChecking=yes', '-o', 'UserKnownHostsFile=' + str(hosts),
                            '-o', 'ConnectTimeout=15', '-o', 'ServerAliveInterval=15', '-o', 'ServerAliveCountMax=4',
                            target, command]
            if image_file is not None:
                return transfer_image(arguments, image_file, kind)
            return execute(arguments, data)

        directories = {'central': '/home/bingo/apps/xboard-logs/releases/' + sha,
                       'production': '/opt/xboard-observability/releases/' + sha}
        payload = archive()
        for kind, path in directories.items():
            expected = 'bingo' if kind == 'central' else 'vmi3574179'
            # Only versioned task files are transferred by CI. No application,
            # Remnawave, global SSH daemon or Docker daemon configuration changes.
            ssh(kind, 'set -eu; test "$(hostname)" = ' + expected + '; umask 077; mkdir -p ' + shlex.quote(path) +
                '; test ! -L ' + shlex.quote(path) + '; tar -xzf - --no-same-owner -C ' + shlex.quote(path), payload)
            ssh(kind, 'docker load', image_file=image_directory / 'collector.tar.gz')
            identity = ssh(kind, "docker image inspect --format '{{.Id}}' xboard-log-collector:" + sha).decode().strip()
            if identity != manifest['image_id']:
                raise ValueError('Loaded collector image identity mismatch')

        def action(kind, value, data=None):
            return ssh(kind, 'python3 ' + shlex.quote(directories[kind] + '/deploy/logging-remote.py') + ' ' + value, data)

        public_key = action('central', 'central-init')
        action('central', 'central-start')
        action('production', 'authorize', public_key)
        action('central', 'tunnel', json.dumps({'host': '109.205.178.211', 'port': '22',
                                              'known_hosts': os.environ['DEPLOY_KNOWN_HOSTS']}).encode())
        for source in ('development', 'production'):
            credential = action('central', 'token --source ' + source)
            action('central' if source == 'development' else 'production', 'panel --source ' + source, credential)
            del credential
            print('Scoped panel collector configured: ' + source, flush=True)
        probes = [json.loads(action('central' if source == 'development' else 'production', 'probe --source ' + source))
                  for source in ('development', 'production')]
        action('central', 'verify', json.dumps(probes).encode())
        # Read-only authenticated health/canary checks are implemented by the
        # asset helper. No credential appears in argv or stdout.
        for attempt in range(19):
            try:
                ssh('central', 'python3 ' + shlex.quote(directories['central'] + '/deploy/observability/health.py') +
                    ' --directory /home/bingo/apps/xboard-logs')
                break
            except RuntimeError:
                if attempt == 18:
                    raise
                time.sleep(5)
        print('Private logging deployment complete: source=' + sha)


if __name__ == '__main__':
    try:
        main()
    except (ValueError, RuntimeError, OSError) as error:
        raise SystemExit(str(error) if isinstance(error, (ValueError, RuntimeError)) else 'Deployment I/O failed; no secret output was printed')
