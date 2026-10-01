"""Read configured Variables through the API, never interpolate a key into a logged step."""
import json
import os
from pathlib import Path
import re
import shlex
import subprocess
import tempfile
import urllib.error
import urllib.parse
import urllib.request


def api(path):
    request = urllib.request.Request('https://api.github.com/' + path, headers={
        'Authorization': 'Bearer ' + os.environ['GH_TOKEN'],
        'Accept': 'application/vnd.github+json',
        'X-GitHub-Api-Version': '2022-11-28',
    })
    try:
        with urllib.request.urlopen(request, timeout=30) as response:
            return json.load(response)
    except urllib.error.HTTPError as error:
        raise RuntimeError(f'GitHub API rejected configuration/release read: HTTP {error.code}; no credentials were printed') from None


def validate(config, mode, revision, email):
    if mode not in ('inspect', 'install') or not re.fullmatch(r'[0-9a-f]{40}', revision):
        raise ValueError('Invalid deployment mode or release SHA')
    if not re.fullmatch(r'[a-zA-Z0-9.-]+', config['DEPLOY_HOST']):
        raise ValueError('Invalid SSH host')
    if not re.fullmatch(r'[a-z_][a-z0-9_-]*', config['DEPLOY_USER']):
        raise ValueError('Invalid SSH user')
    if not config['DEPLOY_PORT'].isdigit() or not 1 <= int(config['DEPLOY_PORT']) <= 65535:
        raise ValueError('Invalid SSH port')
    if config['DEPLOY_DIR'] != '/opt/xboard-go':
        raise ValueError('This reviewed first-install workflow is scoped to /opt/xboard-go')
    origin = config['PANEL_URL'].strip().rstrip('/')
    if '://' not in origin:
        origin = 'https://' + origin
    parsed = urllib.parse.urlsplit(origin)
    if not re.fullmatch(r'https://[a-zA-Z0-9.-]+', origin) or not parsed.hostname:
        raise ValueError('Expected an HTTPS origin without a subpath or custom port')
    if not re.fullmatch(r'[a-zA-Z0-9._+%-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}', email):
        raise ValueError('Invalid administrator email')
    return origin


def main():
    repository = os.environ['GITHUB_REPOSITORY']
    if repository != 'Hao-Monster/Xboard-Go':
        raise ValueError('Unexpected repository')
    mode, revision, email = (os.environ[name] for name in ('DEPLOY_MODE', 'RELEASE_SHA', 'ADMINISTRATOR_EMAIL'))
    if mode == 'install' and (os.environ['GITHUB_REF'] != 'refs/heads/main' or os.environ['GITHUB_EVENT_NAME'] != 'workflow_dispatch'):
        raise ValueError('Installation requires a manual run on protected main')
    config = {row['name']: row['value'] for row in api(f'repos/{repository}/environments/production-internal-test/variables')['variables']}
    origin = validate(config, mode, revision, email)
    # API failures are deliberately fatal; do not fall back to env/with interpolation of private Variables.
    private_key = config['DEPLOY_SSH_KEY'].replace('\r\n', '\n').strip() + '\n'
    for line in private_key.splitlines():
        print('::add-mask::' + line.replace('%', '%25').replace('\r', '%0D'), flush=True)
    release = api(f'repos/{repository}/releases/tags/internal-{revision}')
    if release['draft']:
        raise ValueError('Unpublished release')
    comparison = api(f'repos/{repository}/compare/{revision}...main')
    if comparison['status'] not in ('ahead', 'identical'):
        raise ValueError('Release must belong to protected main history')
    installer = next(item for item in release['assets'] if item['name'] == 'install.sh')
    digest = installer.get('digest', '')
    if not re.fullmatch(r'sha256:[a-f0-9]{64}', digest):
        raise ValueError('Release installer has no immutable digest')
    with tempfile.TemporaryDirectory(prefix='xboard-production-') as temporary:
        directory = Path(temporary)
        os.chmod(directory, 0o700)
        key, hosts = directory / 'key', directory / 'known_hosts'
        key.write_text(private_key)
        hosts.write_text(config['DEPLOY_KNOWN_HOSTS'].strip() + '\n')
        os.chmod(key, 0o600)
        os.chmod(hosts, 0o600)
        command = ['ssh', '-i', str(key), '-p', config['DEPLOY_PORT'], '-o', 'BatchMode=yes',
                   '-o', 'IdentitiesOnly=yes', '-o', 'StrictHostKeyChecking=yes',
                   '-o', 'UserKnownHostsFile=' + str(hosts), '-o', 'ConnectTimeout=15',
                   '-o', 'ServerAliveInterval=15', '-o', 'ServerAliveCountMax=4',
                   config['DEPLOY_USER'] + '@' + config['DEPLOY_HOST'],
                   'bash -s -- ' + ' '.join(shlex.quote(value) for value in (mode, revision, origin, email, digest[7:]))]
        subprocess.run(command, input=Path('deploy/production-server.sh').read_text(), text=True, check=True)


if __name__ == '__main__':
    try:
        main()
    except (RuntimeError, ValueError, KeyError, StopIteration, subprocess.CalledProcessError) as error:
        # Never render config dictionaries or HTTP response bodies on failure.
        raise SystemExit(str(error) if isinstance(error, (RuntimeError, ValueError)) else 'Deployment failed; configuration values remain redacted')
