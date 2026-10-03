#!/usr/bin/env python3
"""Ship this reviewed template to the owner's SSH server from GitHub Actions."""
import json
import os
from pathlib import Path
import re
import shlex
import subprocess
import tempfile


def main():
    host, user, port = (os.environ[k] for k in ('DEPLOY_HOST', 'DEPLOY_USER', 'DEPLOY_PORT'))
    if not re.fullmatch(r'[A-Za-z0-9][A-Za-z0-9.-]*', host) or not re.fullmatch(r'[a-z_][a-z0-9_-]*', user):
        raise ValueError('Invalid SSH host/user')
    if not port.isdigit() or not 1 <= int(port) <= 65535:
        raise ValueError('Invalid SSH port')
    mode = os.environ.get('DEPLOY_MODE') or 'update'
    if mode not in ('install', 'update'):
        raise ValueError('Invalid deployment mode')
    config = dict(directory=os.environ.get('INSTALL_DIR') or '/opt/xboard-go',
                  project='xboard-go', url=os.environ['PANEL_URL'], email=os.environ['ADMIN_EMAIL'],
                  port=os.environ.get('APP_PORT') or '7080', bind=os.environ.get('BIND_ADDRESS') or '127.0.0.1')
    with tempfile.TemporaryDirectory() as temporary:
        temp = Path(temporary)
        key, known = temp / 'key', temp / 'known_hosts'
        key.write_text(os.environ['SSH_PRIVATE_KEY'].strip() + '\n')
        key.chmod(0o600)
        known.write_text(os.environ['SSH_KNOWN_HOSTS'].strip() + '\n')
        (temp / 'config.json').write_text(json.dumps(config))
        options = ['-i', str(key), '-o', 'IdentitiesOnly=yes', '-o', 'BatchMode=yes',
                   '-o', 'StrictHostKeyChecking=yes', '-o', 'UserKnownHostsFile=' + str(known),
                   '-o', 'ConnectTimeout=20', '-o', 'ServerAliveInterval=30']
        ssh = ['ssh', *options, '-p', port, user + '@' + host]
        remote = subprocess.check_output([*ssh, 'mktemp -d /tmp/xboard-deploy.XXXXXXXXXX'], text=True).strip()
        if not re.fullmatch(r'/tmp/xboard-deploy\.[A-Za-z0-9]{10}', remote):
            raise ValueError('Invalid remote temporary directory')
        try:
            root = Path(__file__).parent
            subprocess.run(['scp', *options, '-P', port, str(root / 'deploy.py'),
                            str(root / 'compose.yaml'), str(temp / 'config.json'),
                            user + '@' + host + ':' + remote + '/'], check=True)
            # Passwordless sudo is required only for this owner-controlled deployment account.
            command = ['sudo', '-n', 'python3', remote + '/deploy.py', remote + '/config.json', mode]
            subprocess.run([*ssh, shlex.join(command)], check=True)
        finally:
            subprocess.run([*ssh, shlex.join(['rm', '-rf', '--', remote])], check=True)


if __name__ == '__main__':
    main()
