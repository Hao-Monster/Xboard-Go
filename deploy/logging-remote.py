"""CI-only, scoped host operations for the private Xboard log service.

Never run this from a developer SSH session. All secret material stays in private
host files or SSH stdin; command output contains only operation status.
"""
import argparse
import json
import os
from pathlib import Path
import re
import socket
import subprocess
import sys
import time
import urllib.request

CENTRAL = Path('/home/bingo/apps/xboard-logs')
PRODUCTION = Path('/opt/xboard-observability')
ASSETS = Path(__file__).parent / 'observability'


def run(args, **kwargs):
    result = subprocess.run(args, capture_output=True, text=True, **kwargs)
    if result.returncode:
        # Docker output can contain expanded environment/configuration. Keep it
        # private on the host, never in GitHub logs.
        path = (CENTRAL if socket.gethostname() == 'bingo' else PRODUCTION) / 'last-failure.log'
        path.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
        private_write(path, result.stdout + result.stderr)
        raise RuntimeError('Command failed; details are in the private host last-failure.log')
    return result.stdout


def private_write(path, content):
    if path.is_symlink():
        raise ValueError('Refusing a symlink')
    temporary = path.with_name(path.name + '.next')
    fd = os.open(temporary, os.O_WRONLY | os.O_CREAT | os.O_TRUNC | os.O_NOFOLLOW, 0o600)
    with os.fdopen(fd, 'w') as stream:
        stream.write(content)
    os.chmod(temporary, 0o600)
    os.replace(temporary, path)


def host(expected):
    if socket.gethostname() != expected:
        raise ValueError('Unexpected host identity')


def compose(directory, filename, *args):
    return run(['docker', 'compose', '--project-directory', str(directory), '-f', str(directory / filename), *args])


def configure(*args):
    return run(['python3', str(ASSETS / 'configure.py'), *map(str, args)], env={**os.environ, 'XBOARD_LOG_COLLECTOR_IMAGE': collector_image()})


def collector_image():
    revision = ASSETS.parents[1].name
    if not re.fullmatch('[a-f0-9]{40}', revision):
        raise ValueError('Collector build must use an exact-SHA release directory')
    return 'xboard-log-collector:' + revision


def central_init():
    host('bingo')
    if CENTRAL.is_symlink():
        raise ValueError('Unexpected central directory')
    CENTRAL.mkdir(mode=0o700, parents=True, exist_ok=True)
    configure('central', '--directory', CENTRAL, '--source', 'development', '--source', 'production')
    keys = CENTRAL / 'tunnel'
    keys.mkdir(mode=0o700, exist_ok=True)
    if not (keys / 'id_ed25519').exists():
        run(['ssh-keygen', '-q', '-t', 'ed25519', '-N', '', '-C', 'xboard-logs-production-tunnel', '-f', str(keys / 'id_ed25519')])
    # Public key only. Private key never leaves bingo-dev.
    print((keys / 'id_ed25519.pub').read_text().strip())


def token(source):
    host('bingo')
    if source not in ('development', 'production'):
        raise ValueError('Unexpected source')
    # This action is consumed via a captured SSH pipe, never printed by runner.
    print(json.loads((CENTRAL / 'secrets.json').read_text())['writers'][source])


def authorize():
    host('vmi3574179')
    key = sys.stdin.read().strip()
    line = authorized_entry(key)
    directory = Path('/root/.ssh')
    directory.mkdir(mode=0o700, exist_ok=True)
    path = directory / 'authorized_keys'
    original = path.read_text() if path.exists() else ''
    matching = [value for value in original.splitlines() if 'xboard-logs-production-tunnel' in value]
    if matching and matching != [line]:
        raise ValueError('Existing tunnel key differs; no implicit rotation allowed')
    if not matching:
        private_write(path, original.rstrip('\n') + '\n' + line + '\n')


def authorized_entry(key):
    if not re.fullmatch(r'ssh-ed25519 [A-Za-z0-9+/=]+ xboard-logs-production-tunnel', key):
        raise ValueError('Unexpected tunnel public key')
    return 'restrict,port-forwarding,permitlisten="127.0.0.1:19428",permitopen="127.0.0.1:19428",command="/bin/false" ' + key


def tunnel():
    host('bingo')
    payload = json.load(sys.stdin)
    if payload['host'] != '109.205.178.211' or str(payload['port']) != '22':
        raise ValueError('Unexpected tunnel destination')
    private_write(CENTRAL / 'tunnel/known_hosts', payload['known_hosts'].strip() + '\n')
    units = Path.home() / '.config/systemd/user'
    units.mkdir(parents=True, exist_ok=True)
    private_write(units / 'xboard-logs-tunnel.service', '''[Unit]
Description=Xboard private log transport to production internal test
After=network-online.target
[Service]
ExecStart=/usr/bin/ssh -NT -i /home/bingo/apps/xboard-logs/tunnel/id_ed25519 -o IdentitiesOnly=yes -o BatchMode=yes -o StrictHostKeyChecking=yes -o UserKnownHostsFile=/home/bingo/apps/xboard-logs/tunnel/known_hosts -o ExitOnForwardFailure=yes -o ServerAliveInterval=15 -o ServerAliveCountMax=3 -R 127.0.0.1:19428:127.0.0.1:19428 root@109.205.178.211
Restart=always
RestartSec=10
NoNewPrivileges=yes
[Install]
WantedBy=default.target
''')
    run(['systemctl', '--user', 'daemon-reload'])
    run(['systemctl', '--user', 'enable', '--now', 'xboard-logs-tunnel.service'])
    run(['systemctl', '--user', 'is-active', 'xboard-logs-tunnel.service'])


def central_start():
    host('bingo')
    compose(CENTRAL, 'compose.yaml', 'up', '-d', '--wait', '--wait-timeout', '180')
    units = Path.home() / '.config/systemd/user'
    units.mkdir(parents=True, exist_ok=True)
    private_write(units / 'xboard-logs-health.service', '[Unit]\nDescription=Xboard log capacity and delivery check\n[Service]\nType=oneshot\nExecStart=/usr/bin/python3 ' +
                  str(ASSETS / 'health.py') + ' --directory ' + str(CENTRAL) + '\nNoNewPrivileges=yes\n')
    private_write(units / 'xboard-logs-health.timer', '''[Unit]
Description=Check Xboard log delivery and capacity each minute
[Timer]
OnBootSec=120
OnUnitActiveSec=60
AccuracySec=5
[Install]
WantedBy=timers.target
''')
    run(['systemctl', '--user', 'daemon-reload'])
    run(['systemctl', '--user', 'enable', '--now', 'xboard-logs-health.timer'])


def panel(source):
    production = source == 'production'
    host('vmi3574179' if production else 'bingo')
    directory = PRODUCTION if production else CENTRAL
    directory.mkdir(parents=True, exist_ok=True, mode=0o700)
    collector = directory / ('collector-' + source)
    collector.mkdir(mode=0o700, exist_ok=True)
    # CI loads the exact image which passed the journal and recovery tests.
    run(['docker', 'image', 'inspect', collector_image()])
    credential = sys.stdin.read().strip()
    if not re.fullmatch(r'[A-Za-z0-9_-]{32,}', credential):
        raise ValueError('Invalid collector credential')
    private_write(collector / 'write-token', credential + '\n')
    configure('collector', '--directory', collector, '--source', source, '--token-file', collector / 'write-token',
              '--tag', 'xboard-panel-' + source, '--endpoint', 'http://127.0.0.1:19428')
    compose(collector, 'collector-compose.yaml', 'up', '-d', '--wait', '--wait-timeout', '120')
    application = Path('/opt/xboard-go' if production else '/home/bingo/apps/xboard-go-freedom')
    import fcntl
    lock = open(application / '.deployment.lock', 'a')
    fcntl.flock(lock, fcntl.LOCK_EX)
    project = 'xboard-production-internal' if production else 'xboard-go-freedom'
    container = project + '-app-1'
    inspect = json.loads(run(['docker', 'inspect', container]))[0]
    if inspect['Config']['Labels'].get('com.docker.compose.project') != project or inspect['State'].get('Health', {}).get('Status') != 'healthy':
        raise ValueError('Expected healthy Xboard container missing')
    if not Path('/var/log/journal').is_dir():
        raise ValueError('Persistent journal is required')
    protected = ['remnawave', 'remnanode', 'remnawave-db', 'remnawave-redis', 'caddy'] if production else []
    snapshot_args = ['docker', 'inspect', *protected, '--format', '{{.Id}} {{.State.StartedAt}} {{.State.Running}}']
    baseline = run(snapshot_args) if protected else ''
    override = application / 'compose.observability.yaml'
    content = ('services:\n  app:\n    logging:\n      driver: journald\n      options:\n        tag: xboard-panel-' + source +
               '\n    environment:\n      XBOARD_LOG_ENVIRONMENT: ' + source + '\n      XBOARD_LOG_LEVEL: debug\n')
    existed = override.exists()
    if override.exists() and override.read_text() != content:
        raise ValueError('Unexpected existing observability overlay')
    private_write(override, content)
    # Explicit files in every CI update preserve this overlay; base Compose and
    # secrets are unchanged, so existing installer digest checks remain valid.
    command = ['docker', 'compose', '-p', project, '--project-directory', str(application), '--env-file', str(application / '.env'),
               '-f', str(application / 'compose.yaml'), '-f', str(override)]
    try:
        run([*command, 'up', '-d', '--no-build', '--no-deps', '--wait', '--wait-timeout', '180', 'app'])
        if protected and run(snapshot_args) != baseline:
            raise RuntimeError('Protected services changed')
    except Exception:
        # Roll back only the logging overlay, retaining the existing app image.
        if not existed:
            override.rename(application / 'compose.observability.failed.yaml')
            run(['docker', 'compose', '-p', project, '--project-directory', str(application), '-f', str(application / 'compose.yaml'),
                 'up', '-d', '--no-build', '--no-deps', '--wait', '--wait-timeout', '180', 'app'])
        raise
    print('Collector and scoped Xboard journal logging active: ' + source)
    units = Path('/etc/systemd/system') if production else Path.home() / '.config/systemd/user'
    units.mkdir(parents=True, exist_ok=True)
    private_write(units / 'xboard-logs-probe.service', '[Unit]\nDescription=Scoped Xboard runtime and collector diagnostics\n[Service]\nType=oneshot\nTimeoutStartSec=40\nExecStart=/usr/bin/python3 ' +
                  str(Path(__file__).with_name('logging-probe.py')) + ' --source ' + source + '\nNoNewPrivileges=yes\n')
    private_write(units / 'xboard-logs-probe.timer', '[Unit]\nDescription=Collect Xboard runtime counters each minute\n[Timer]\nOnBootSec=60\nOnUnitActiveSec=60\n[Install]\nWantedBy=' + ('timers.target' if production else 'default.target') + '\n')
    systemctl = ['systemctl'] + ([] if production else ['--user'])
    run([*systemctl, 'daemon-reload'])
    run([*systemctl, 'enable', '--now', 'xboard-logs-probe.timer'])
    run([*systemctl, 'start', 'xboard-logs-probe.service'])


def probe(source):
    host('vmi3574179' if source == 'production' else 'bingo')
    port = 7080 if source == 'production' else 18082
    container = 'xboard-production-internal-app-1' if source == 'production' else 'xboard-go-freedom-app-1'
    revision = run(['docker', 'inspect', container, '--format', '{{index .Config.Labels "org.opencontainers.image.revision"}}']).strip()
    with urllib.request.urlopen('http://127.0.0.1:%d/healthz' % port, timeout=10) as response:
        body = json.load(response)
        identity = response.headers.get('X-Request-ID', '')
    if body.get('data', {}).get('status') != 'ok' or not re.fullmatch('[a-f0-9]{32}', identity) or not re.fullmatch('[a-f0-9]{40}', revision):
        raise ValueError('Panel needs the reviewed diagnostic release before logging verification')
    print(json.dumps({'source': source, 'request_id': identity, 'revision': revision}))


def verify():
    host('bingo')
    sys.path.insert(0, str(ASSETS))
    from health import query
    credentials = json.loads((CENTRAL / 'secrets.json').read_text())['viewer']
    probes = json.load(sys.stdin)
    for item in probes:
        if item['source'] not in ('development', 'production') or not re.fullmatch('[a-f0-9]{32}', item['request_id']) or not re.fullmatch('[a-f0-9]{40}', item['revision']):
            raise ValueError('Invalid probe identity')
        expression = '_time:5m event:http.request source:' + item['source'] + ' request_id:' + item['request_id'] + ' revision:' + item['revision']
        for attempt in range(31):
            if query('http://127.0.0.1:19428', credentials, expression):
                break
            if attempt == 30:
                raise RuntimeError('Panel HTTP request was not observed through the journal collector')
            time.sleep(2)
    private_write(CENTRAL / 'panel-delivery-evidence.json', json.dumps(probes, indent=2) + '\n')
    print('Both panel request IDs observed in centralized logs with exact application revisions')


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('action', choices=['central-init', 'central-start', 'token', 'authorize', 'tunnel', 'panel', 'probe', 'verify'])
    parser.add_argument('--source', choices=['development', 'production'])
    args = parser.parse_args()
    if args.action in ('token', 'panel', 'probe') and not args.source:
        parser.error('--source required')
    {'central-init': central_init, 'central-start': central_start, 'token': lambda: token(args.source),
     'authorize': authorize, 'tunnel': tunnel, 'panel': lambda: panel(args.source), 'probe': lambda: probe(args.source), 'verify': verify}[args.action]()


if __name__ == '__main__':
    try:
        main()
    except (RuntimeError, ValueError, OSError) as error:
        print(type(error).__name__ + ': logging deployment failed; see private host diagnostic', file=sys.stderr)
        sys.exit(1)
