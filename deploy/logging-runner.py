"""GitHub Actions deployment of the private log center and panel collectors."""
import io
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


def validate(environment):
    if (environment.get('GITHUB_REPOSITORY') != 'Hao-Monster/Xboard-Go' or
            environment.get('GITHUB_REF') != 'refs/heads/main' or
            environment.get('GITHUB_EVENT_NAME') != 'workflow_dispatch'):
        raise ValueError('Deployment requires a manual workflow on protected main')
    if not re.fullmatch('[0-9a-f]{40}', environment.get('GITHUB_SHA', '')):
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
        files = [Path('deploy/logging-remote.py'), *Path('deploy/observability').glob('*')]
        for path in files:
            if path.is_file() and path.suffix in ('.py', '.yaml', '.json', '.sh'):
                output.add(path, arcname=str(path).replace('\\', '/'), recursive=False)
    return buffer.getvalue()


def main():
    validate(os.environ)
    sha = os.environ['GITHUB_SHA']
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

        def ssh(kind, command, data=None):
            target = 'bingo@127.0.0.1' if kind == 'central' else 'root@109.205.178.211'
            return execute(['ssh', '-i', str(keys[kind]), '-o', 'IdentitiesOnly=yes', '-o', 'BatchMode=yes',
                            '-o', 'StrictHostKeyChecking=yes', '-o', 'UserKnownHostsFile=' + str(hosts),
                            '-o', 'ConnectTimeout=15', '-o', 'ServerAliveInterval=15', '-o', 'ServerAliveCountMax=4',
                            target, command], data)

        directories = {'central': '/home/bingo/apps/xboard-logs/releases/' + sha,
                       'production': '/opt/xboard-observability/releases/' + sha}
        payload = archive()
        for kind, path in directories.items():
            expected = 'bingo' if kind == 'central' else 'vmi3574179'
            # Only versioned task files are transferred by CI. No application,
            # Remnawave, global SSH daemon or Docker daemon configuration changes.
            ssh(kind, 'set -eu; test "$(hostname)" = ' + expected + '; umask 077; mkdir -p ' + shlex.quote(path) +
                '; test ! -L ' + shlex.quote(path) + '; tar -xzf - --no-same-owner -C ' + shlex.quote(path), payload)

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
