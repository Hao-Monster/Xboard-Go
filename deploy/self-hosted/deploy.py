#!/usr/bin/env python3
"""Run only through the owner's deployment workflow. No automatic database rollback."""
import base64
import contextlib
import datetime
import json
import os
from pathlib import Path
import re
import secrets
import shutil
import subprocess
import sys
import urllib.request

IMAGE = 'ghcr.io/hao-monster/xboard-go'


def run(*args, capture=False):
    return subprocess.run(args, check=True, text=True,
                          stdout=subprocess.PIPE if capture else None).stdout


def version(value):
    if not isinstance(value, str) or not re.fullmatch(r'v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)', value):
        raise ValueError('Invalid stable version')
    return tuple(map(int, value[1:].split('.')))


def validate_release(metadata, reference, labels):
    version(metadata['version'])
    if (metadata.get('schema_version') != 1 or metadata.get('image') != reference
            or not re.fullmatch(re.escape(IMAGE) + r'@sha256:[0-9a-f]{64}', reference)
            or not re.fullmatch(r'[0-9a-f]{40}', metadata.get('revision', ''))
            or metadata['revision'] != labels.get('org.opencontainers.image.revision')
            or metadata['version'] != labels.get('org.opencontainers.image.version')
            or 'linux/amd64' not in metadata.get('platforms', [])):
        raise ValueError('Release metadata and immutable image do not match')


def upgrade_allowed(current, target):
    old, new = version(current['version']), version(target['version'])
    lower = target.get('compatible_from')
    return (target.get('automatic_upgrade') is True and bool(lower)
            and version(lower)[0] == old[0] == new[0]
            and version(lower) <= old < new)


def release():
    run('docker', 'pull', IMAGE + ':stable')
    image = json.loads(run('docker', 'image', 'inspect', IMAGE + ':stable', capture=True))[0]
    reference = next(x for x in image['RepoDigests'] if x.startswith(IMAGE + '@sha256:'))
    labels = image['Config']['Labels']
    tag = labels['org.opencontainers.image.version']
    version(tag)
    url = f'https://github.com/Hao-Monster/Xboard-Go/releases/download/{tag}/release.json'
    with urllib.request.urlopen(url, timeout=30) as response:
        metadata = json.load(response)
    validate_release(metadata, reference, labels)
    return metadata


def validate_config(config):
    directory = config['directory']
    if not re.fullmatch(r'/opt/[A-Za-z0-9_-]+', directory):
        raise ValueError('Install directory must be a direct child of /opt')
    if not re.fullmatch(r'[a-z][a-z0-9-]{1,40}', config['project']):
        raise ValueError('Invalid project')
    if not re.fullmatch(r'https?://[A-Za-z0-9.-]+(?::[0-9]{1,5})?', config['url']):
        raise ValueError('Panel URL must be an HTTP(S) origin')
    if not re.fullmatch(r'[A-Za-z0-9._+%-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}', config['email']):
        raise ValueError('Invalid administrator email')
    if not 1 <= int(config['port']) <= 65535 or config['bind'] not in ('127.0.0.1', '0.0.0.0'):
        raise ValueError('Invalid port or bind address')


def deploy_inner(config, mode, target):
    validate_config(config)
    directory = Path(config['directory'])
    state = directory / 'installed.json'
    failed = directory / 'deployment-failed'
    compose = ['docker', 'compose', '--env-file', str(directory / '.env'),
               '--project-directory', str(directory), '-f', str(directory / 'compose.yaml')]
    exists = directory.exists() or directory.is_symlink()
    if mode == 'install' and exists:
        raise ValueError('Install refuses existing directory; use update')
    if mode == 'update' and not exists:
        print('Not installed; scheduled updates never perform first installation')
        return
    if directory.is_symlink():
        raise ValueError('Symlink installation refused')
    if failed.exists():
        raise ValueError('Previous deployment failed; operator recovery is required')
    if exists:
        current = json.loads(state.read_text())
        settings = dict(line.split('=', 1) for line in (directory / '.env').read_text().splitlines() if '=' in line)
        if settings.get('COMPOSE_PROJECT_NAME') != config['project'] or settings.get('XBOARD_IMAGE') != current['image']:
            raise ValueError('Installed project/image differs from recorded state')
        containers = run(*compose, 'ps', '-aq', 'app', capture=True).strip().splitlines()
        if len(containers) != 1:
            raise ValueError('Expected exactly one existing application container')
        inspected = json.loads(run('docker', 'inspect', containers[0], capture=True))[0]
        if inspected['Config']['Image'] != current['image']:
            raise ValueError('Running image differs from recorded state')
        if current['image'] == target['image']:
            if inspected['State'].get('Health', {}).get('Status') != 'healthy' or not inspected['State'].get('Running'):
                raise ValueError('Current installation is not healthy; operator recovery required')
            print('Already running the current stable digest')
            return
        if not upgrade_allowed(current, target):
            raise ValueError('Automatic upgrade incompatible or requires operator approval; service unchanged')
    else:
        for resource in ('ps', 'volume'):
            args = ['docker', 'ps', '-aq'] if resource == 'ps' else ['docker', 'volume', 'ls', '-q']
            if run(*args, '--filter', 'label=com.docker.compose.project=' + config['project'], capture=True).strip():
                raise ValueError('Existing project resources refused')
    run('docker', 'pull', target['image'])
    if exists:
        stamp = datetime.datetime.now(datetime.timezone.utc).strftime('%Y%m%dT%H%M%SZ')
        backup = directory.parent / (directory.name + '-backup-' + stamp + '-' + secrets.token_hex(4))
        backup.mkdir(mode=0o700)
        # A durable marker blocks all later scheduled runs after interruption or failure.
        failed.write_text('Deployment in progress or failed. Recovery set: ' + str(backup) + '\n')
        # Stop before copying SQLite/WAL, attachments and encryption keys as one recovery set.
        run(*compose, 'stop', 'app')
        shutil.copytree(directory, backup / 'configuration')
        run('docker', 'cp', containers[0] + ':/var/lib/xboard/.', str(backup / 'data'))
        run('docker', 'cp', containers[0] + ':/var/lib/xboard-backups/.', str(backup / 'application-backups'))
        print('Pre-upgrade recovery set:', backup, flush=True)
        env = (directory / '.env').read_text()
        env, count = re.subn(r'^XBOARD_IMAGE=.*$', 'XBOARD_IMAGE=' + target['image'], env, flags=re.M)
        if count != 1:
            raise ValueError('Invalid installed image configuration; service remains stopped')
    else:
        directory.mkdir(mode=0o700)
        failed.write_text('First installation in progress or failed; inspect before recovering.\n')
        shutil.copyfile(Path(__file__).with_name('compose.yaml'), directory / 'compose.yaml')
        (directory / 'secrets').mkdir(mode=0o700)
        for name, size in [('admin-password', 24), ('settings-key', 32)]:
            path = directory / 'secrets' / name
            path.write_text(base64.b64encode(secrets.token_bytes(size)).decode())
            path.chmod(0o444)
        values = {'COMPOSE_PROJECT_NAME': config['project'], 'XBOARD_IMAGE': target['image'],
                  'XBOARD_PANEL_URL': config['url'], 'XBOARD_ADMIN_EMAIL': config['email'],
                  'XBOARD_ADMIN_PATH': secrets.token_hex(24), 'XBOARD_PORT': str(config['port']),
                  'XBOARD_BIND_ADDRESS': config['bind'],
                  'XBOARD_COOKIE_SECURE': str(config['url'].startswith('https:')).lower()}
        env = ''.join(k + '=' + v + '\n' for k, v in values.items())
    (directory / '.env').write_text(env)
    try:
        run(*compose, 'up', '-d', '--no-build', '--wait', '--wait-timeout', '180')
        container = run(*compose, 'ps', '-q', 'app', capture=True).strip()
        inspected = json.loads(run('docker', 'inspect', container, capture=True))[0]
        if (inspected['Config']['Image'] != target['image']
                or inspected['State']['Health']['Status'] != 'healthy'):
            raise ValueError('Running digest/health verification failed')
    except Exception:
        with contextlib.suppress(subprocess.CalledProcessError):
            run(*compose, 'stop', 'app')
        raise RuntimeError('Deployment failed; application stopped, data and recovery set preserved. No database rollback attempted.') from None
    temporary = state.with_suffix('.tmp')
    temporary.write_text(json.dumps(target, indent=2) + '\n')
    temporary.replace(state)
    failed.unlink()
    print('Deployed', target['version'], target['revision'], target['image'])
    if not exists:
        print('Retrieve administrator password from', directory / 'secrets/admin-password', 'and admin path from .env over your secure server session.')


def deploy(config, mode, target):
    validate_config(config)
    directory = Path(config['directory'])
    already_failed = (directory / 'deployment-failed').exists()
    try:
        deploy_inner(config, mode, target)
    except Exception:
        if not already_failed and (directory / 'deployment-failed').exists() and (directory / '.env').exists():
            with contextlib.suppress(subprocess.CalledProcessError):
                run('docker', 'compose', '--env-file', str(directory / '.env'),
                    '--project-directory', str(directory), '-f', str(directory / 'compose.yaml'), 'stop', 'app')
        raise


def main():
    import fcntl
    import platform
    os.umask(0o077)
    if platform.system() != 'Linux' or platform.machine() != 'x86_64':
        raise ValueError('Only Linux amd64 is supported')
    config = json.loads(Path(sys.argv[1]).read_text())
    validate_config(config)
    mode = sys.argv[2]
    if mode not in ('install', 'update'):
        raise ValueError('Invalid mode')
    lock = Path(config['directory'] + '.deploy.lock')
    with lock.open('a') as handle:
        fcntl.flock(handle, fcntl.LOCK_EX | fcntl.LOCK_NB)
        if mode == 'update' and not Path(config['directory']).exists():
            print('Not installed; use manual install first')
            return
        run('docker', 'info', capture=True)
        run('docker', 'compose', 'version')
        deploy(config, mode, release())


if __name__ == '__main__':
    main()
