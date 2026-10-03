#!/usr/bin/env python3
"""Isolated CI only: sudo python3 smoke.py LOCAL_IMAGE. Never target a real server."""
import json
import os
from pathlib import Path
import secrets
import shutil
import socket
import subprocess
import sys
from unittest.mock import patch
import deploy


def main():
    os.umask(0o077)
    image = sys.argv[1]
    name = 'xboard-smoke-' + secrets.token_hex(6)
    directory = Path('/opt') / name
    with socket.socket() as sock:
        sock.bind(('127.0.0.1', 0))
        port = sock.getsockname()[1]
    config = dict(directory=str(directory), project=name, url=f'http://127.0.0.1:{port}', email='smoke@example.invalid', port=str(port), bind='127.0.0.1')
    target = dict(version='v0.1.0', image=image, revision='a' * 40, compatible_from=None, automatic_upgrade=False)
    real_run = deploy.run

    def local_run(*args, capture=False):
        if args[:2] == ('docker', 'pull'):
            return ''  # Image already built by this CI job; no network publication required.
        return real_run(*args, capture=capture)

    compose = ['docker', 'compose', '--env-file', str(directory / '.env'), '--project-directory', str(directory), '-f', str(directory / 'compose.yaml')]
    try:
        with patch.object(deploy, 'run', side_effect=local_run):
            deploy.deploy(config, 'install', target)
            state = json.loads((directory / 'installed.json').read_text())
            assert state == target
            deploy.deploy(config, 'update', target)
            upgraded = dict(target, version='v0.1.1', image=name + ':upgrade', compatible_from='v0.1.0', automatic_upgrade=True)
            real_run('docker', 'tag', image, upgraded['image'])
            deploy.deploy(config, 'update', upgraded)
            assert json.loads((directory / 'installed.json').read_text()) == upgraded
            assert len(list(Path('/opt').glob(name + '-backup-*'))) == 1
            failed = dict(upgraded, version='v0.1.2', image=name + ':missing', compatible_from='v0.1.1')
            try:
                deploy.deploy(config, 'update', failed)
            except (RuntimeError, subprocess.CalledProcessError):
                pass
            else:
                raise AssertionError('Missing image must fail startup')
            assert (directory / 'deployment-failed').exists()
            assert not real_run(*compose, 'ps', '-q', 'app', capture=True).strip()
            assert json.loads((directory / 'installed.json').read_text()) == upgraded
        print('PASS real Docker install, no-op, compatible upgrade, stopped failure and preserved recovery state')
    finally:
        if (directory / '.env').exists():
            subprocess.run([*compose, 'down', '-v'], check=True)
        for path in [directory, *Path('/opt').glob(name + '-backup-*')]:
            if path.exists() and path.parent == Path('/opt') and path.name.startswith(name):
                shutil.rmtree(path)


if __name__ == '__main__':
    main()
