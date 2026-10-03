"""Use protected Environment Secrets for SSH and public Variables for routing."""
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
    if mode not in ('inspect', 'install', 'resume', 'update') or not re.fullmatch(r'[0-9a-f]{40}', revision):
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
    if not re.fullmatch(r'https://[a-zA-Z0-9.-]+(?::8443)?', origin) or not parsed.hostname:
        raise ValueError('Expected an HTTPS origin without a subpath (only port 8443 is supported)')
    if not re.fullmatch(r'[a-zA-Z0-9._+%-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}', email):
        raise ValueError('Invalid administrator email')
    return origin


def main():
    repository = os.environ['GITHUB_REPOSITORY']
    if repository != 'Hao-Monster/Xboard-Go':
        raise ValueError('Unexpected repository')
    mode, revision, email = (os.environ[name] for name in ('DEPLOY_MODE', 'RELEASE_SHA', 'ADMINISTRATOR_EMAIL'))
    node_version, node_source = os.environ.get('NODE_VERSION', ''), os.environ.get('NODE_SOURCE', 'preserve')
    validate_node_selection(mode, node_version, node_source)
    if mode in ('install', 'resume', 'update') and (os.environ['GITHUB_REF'] != 'refs/heads/main' or os.environ['GITHUB_EVENT_NAME'] != 'workflow_dispatch'):
        raise ValueError('Installation requires a manual run on protected main')
    config = {name: os.environ.get(name, '') for name in ('DEPLOY_SSH_KEY', 'DEPLOY_KNOWN_HOSTS', 'DEPLOY_HOST', 'DEPLOY_PORT', 'DEPLOY_USER', 'DEPLOY_DIR', 'PANEL_URL')}
    if not config['DEPLOY_SSH_KEY'] or not config['DEPLOY_KNOWN_HOSTS']:
        raise ValueError('SSH Environment Secrets are required; no unsafe Variables fallback is allowed')
    origin = validate(config, mode, revision, email)
    # Never fall back to env/with interpolation of private Variables.
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
    compose_asset = next(item for item in release['assets'] if item['name'] == 'compose.yaml')
    compose_digest = compose_asset.get('digest', '')
    if not re.fullmatch(r'sha256:[a-f0-9]{64}', compose_digest):
        raise ValueError('Release Compose file has no immutable digest')
    archive = next(item for item in release['assets'] if item['name'] == 'xboard-go-linux-amd64.tar.gz')
    archive_digest = archive.get('digest', '')
    if not re.fullmatch(r'sha256:[a-f0-9]{64}', archive_digest):
        raise ValueError('Release image archive has no immutable digest')
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
                   'bash -s -- ' + ' '.join(shlex.quote(value) for value in (mode, revision, origin, email, digest[7:], compose_digest[7:], archive_digest[7:], node_version, node_source))]
        execute_remote(command, Path('deploy/production-update.sh' if mode == 'update' else 'deploy/production-server.sh').read_text(), mode, revision)


def execute_remote(command, script, mode, revision):
    result = subprocess.run(command, input=script, text=True, check=True, stdout=subprocess.PIPE)
    # An interactive child can consume bash -s input and still produce exit code zero.
    # Only the final marker emitted after all update probes proves script completion.
    if result.stdout:
        print(result.stdout, end='', flush=True)
    if mode == 'update' and f'XBOARD_DEPLOYMENT_COMPLETE:{revision}' not in result.stdout.splitlines():
        raise RuntimeError('Remote update exited without its exact completion marker; inspect application and proxy state before recovery')


def validate_node_selection(mode, version, source):
    if source == 'preserve' and version == '':
        return
    if mode != 'update' or source not in ('github', 'panel') or not re.fullmatch(r'v[0-9]+\.[0-9]+\.[0-9]+', version):
        raise ValueError('Node selection requires update, an exact version and github/panel source')


if __name__ == '__main__':
    try:
        main()
    except (RuntimeError, ValueError, KeyError, StopIteration, subprocess.CalledProcessError) as error:
        # Never render config dictionaries or HTTP response bodies on failure.
        raise SystemExit(str(error) if isinstance(error, (RuntimeError, ValueError)) else 'Deployment failed; configuration values remain redacted')
