#!/usr/bin/env python3
"""Real isolated Docker smoke. Requires Linux Docker; never targets existing deployment."""
import base64
import json
from pathlib import Path
import subprocess
import socket
import tempfile
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid
import configure

MASKS = []

def run(*args):
    result = subprocess.run(args, capture_output=True, text=True)
    if result.returncode:
        output = result.stdout + result.stderr
        for value in MASKS:
            output = output.replace(value, '[REDACTED]')
        print(output)
        raise RuntimeError('Isolated container command failed with exit ' + str(result.returncode))


def vector_options():
    return ['--user', configure.runtime_user(), *[arg for group in configure.journal_groups() for arg in ['--group-add', group]]]


def request(url, authorization=None, body=None):
    headers = {'Authorization': authorization} if authorization else {}
    if body is not None:
        headers['Content-Type'] = 'application/x-ndjson'
    req = urllib.request.Request(url, body, headers)
    try:
        with urllib.request.urlopen(req, timeout=5) as response:
            return response.status, response.read().decode()
    except urllib.error.HTTPError as error:
        return error.code, ''


def eventually(check, seconds=45):
    until = time.monotonic() + seconds
    while time.monotonic() < until:
        try:
            if check():
                return
        except (OSError, urllib.error.URLError):
            pass
        time.sleep(0.5)
    raise AssertionError('condition timed out')


def main():
    name = 'xboard-logs-smoke-' + uuid.uuid4().hex[:8]
    run('docker', 'build', '-t', configure.VECTOR, '-f', str(Path(__file__).with_name('Dockerfile.collector')), str(Path(__file__).parent))
    with tempfile.TemporaryDirectory(prefix=name) as temp:
        root = Path(temp)
        configure.central(root, ['smoke'])
        state = json.loads((root / 'secrets.json').read_text())
        MASKS.extend([state['viewer']['password'], *state['writers'].values()])
        writer = 'Bearer ' + state['writers']['smoke']
        viewer = 'Basic ' + base64.b64encode(('viewer:' + state['viewer']['password']).encode()).decode()
        compose = json.loads((root / 'compose.yaml').read_text())
        compose['name'] = name
        # Docker can reassign an automatically published port after stop/start.
        # Keep the fixture endpoint stable during the deliberate outage.
        with socket.socket() as listener:
            listener.bind(('127.0.0.1', 0))
            fixture_port = listener.getsockname()[1]
        compose['services']['auth']['ports'] = [f'127.0.0.1:{fixture_port}:8427']
        configure.write(root / 'compose.yaml', compose)
        cmd = ['docker', 'compose', '-f', str(root / 'compose.yaml')]
        try:
            run(*cmd, 'up', '-d')
            port = subprocess.check_output([*cmd, 'port', 'auth', '8427'], text=True).strip().split(':')[-1]
            endpoint = 'http://127.0.0.1:' + port
            query = endpoint + '/select/logsql/query?' + urllib.parse.urlencode({'query': '*', 'limit': 100})
            eventually(lambda: request(query, viewer)[0] == 200)
            assert request(query)[0] == 401
            assert request(query, writer)[0] >= 400
            assert request(endpoint + '/insert/jsonline', viewer, b'{}\n')[0] >= 400
            for internal in ['/debug/pprof/', '/metrics', '/flags', '/-/reload']:
                assert request(endpoint + internal, viewer)[0] >= 400
                assert request(endpoint + internal, writer)[0] >= 400
                assert request(endpoint + internal)[0] >= 400
            assert request(endpoint + '/insert/jsonline', writer, b'{"message":"smoke.persisted","source":"smoke"}\n')[0] == 200
            eventually(lambda: 'smoke.persisted' in request(query, viewer)[1])
            run(*cmd, 'restart', 'victoria-logs')
            eventually(lambda: 'smoke.persisted' in request(query, viewer)[1])

            collector = root / 'collector'
            configure.collector(collector, 'smoke', state['writers']['smoke'], 'xboard-panel-smoke', endpoint)
            journal_mounts = [*vector_options(), '--mount', f'type=bind,src={collector},dst=/config',
                              '--mount', f'type=bind,src={collector / "vector-data"},dst=/var/lib/vector',
                              '-v', '/var/log/journal:/var/log/journal:ro',
                              '-v', '/run/log/journal:/run/log/journal:ro',
                              '-v', '/etc/machine-id:/etc/machine-id:ro']
            run('docker', 'run', '--rm', '--network', 'host', *journal_mounts,
                configure.VECTOR, 'validate', '--skip-healthchecks', '/config/collector.yaml')
            journal_name = name + '-journal'
            try:
                run('docker', 'run', '-d', '--name', journal_name, '--network', 'host',
                    *journal_mounts, configure.VECTOR, '--config', '/config/collector.yaml')
                for event in ['smoke.journal_before_restart', 'smoke.journal_after_restart']:
                    run('docker', 'run', '--rm', '--log-driver', 'journald', '--log-opt', 'tag=xboard-panel-smoke',
                        '--entrypoint', '/bin/sh', configure.VECTOR, '-c',
                        'printf \'%s\\n\' \'{"msg":"' + event + '","status":200}\'')
                    eventually(lambda: event in request(query, viewer)[1])
                    run('docker', 'restart', journal_name)
                assert any(p.is_file() for p in (collector / 'vector-data').rglob('*'))
            finally:
                subprocess.run(['docker', 'rm', '-f', journal_name], check=False, stdout=subprocess.DEVNULL)
            collector = root / 'collector-file'
            configure.collector(collector, 'smoke', state['writers']['smoke'], 'xboard-panel-smoke', endpoint)
            config = json.loads((collector / 'collector.yaml').read_text())
            config['sources']['journal'] = {'type': 'file', 'include': ['/fixtures/events.jsonl'], 'read_from': 'beginning'}
            config['sources'].pop('internal_metrics')
            config['sinks'].pop('metrics')
            config['tests'] = [{
                'name': 'drops payload secrets while retaining event identity',
                'inputs': [{'insert_at': 'safe', 'type': 'log', 'log_fields': {
                    'message': '{"msg":"http.error","status":500,"password":"fixture-secret","body":"fixture-body","error":"fixture-error"}'}}],
                'outputs': [{'extract_from': 'safe', 'conditions': [{'type': 'vrl', 'source':
                    'assert_eq!(.event, "http.error")\nassert_eq!(.status, 500)\nassert!(!exists(.password))\nassert!(!exists(.body))\nassert!(!exists(.error))'}]}]}, {
                'name': 'retains nested node report correlation without payload',
                'inputs': [{'insert_at': 'safe', 'type': 'log', 'log_fields': {'message': json.dumps({
                    'service': 'xboard-node', 'runtime_id': 'fixture-runtime', 'component': 'core', 'message': 'control plane event',
                    'attributes': {'event': 'report.failed', 'node_id': 5, 'machine_id': 6,
                                   'report_correlation': 'fixture-correlation', 'retry': True, 'error': '*url.Error', 'payload': 'never-forward'}})}}],
                'outputs': [{'extract_from': 'safe', 'conditions': [{'type': 'vrl', 'source':
                    'assert_eq!(.event, "report.failed")\nassert_eq!(.node_id, 5)\nassert_eq!(.machine_id, 6)\nassert_eq!(.runtime_id, "fixture-runtime")\nassert_eq!(.report_correlation, "fixture-correlation")\nassert_eq!(.error_type, "*url.Error")\nassert_eq!(.retry, true)\nassert!(!exists(.payload))'}]}]}, {
                'name': 'preserves approved worker failure message and callsite',
                'inputs': [{'insert_at': 'safe', 'type': 'log', 'log_fields': {'message': json.dumps({
                    'msg': 'deliver queued email', 'source': 'mailer.worker.Run worker.go:80',
                    'error_type': '*net.OpError', 'error_class': 'network', 'error_code': 'operation_failed', 'network_op': 'dial'})}}],
                'outputs': [{'extract_from': 'safe', 'conditions': [{'type': 'vrl', 'source':
                    'assert_eq!(.message, "deliver queued email")\nassert_eq!(.event, "panel.deliver_queued_email")\nassert_eq!(.code_source, "mailer.worker.Run worker.go:80")\nassert_eq!(.error_class, "network")\nassert_eq!(.network_op, "dial")'}]}]}]
            configure.write(collector / 'collector.yaml', config)
            fixtures = root / 'fixtures'
            fixtures.mkdir()
            events = fixtures / 'events.jsonl'
            events.write_text('', encoding='utf-8')
            mount = [*vector_options(), '--mount', f'type=bind,src={collector},dst=/config', '--mount', f'type=bind,src={fixtures},dst=/fixtures',
                     '--mount', f'type=bind,src={collector / "vector-data"},dst=/var/lib/vector']
            run('docker', 'run', '--rm', *mount, configure.VECTOR, 'test', '/config/collector.yaml')
            run('docker', 'run', '--rm', '--entrypoint', '/bin/sh', configure.VECTOR, '-c', 'command -v journalctl')
            cname = name + '-collector'
            try:
                run('docker', 'run', '-d', '--name', cname, '--network', 'host', *mount, configure.VECTOR, '--config', '/config/collector.yaml')
                eventually(lambda: 'collector.canary' in request(query, viewer)[1])
                run(*cmd, 'stop', 'auth')
                with events.open('a', encoding='utf-8') as stream:
                    stream.write('{"msg":"smoke.buffered","password":"never-forward-fixture"}\n')
                # Wait for durable buffer occupancy, not an arbitrary delivery sleep.
                def buffered_event():
                    for path in (collector / 'vector-data').rglob('*.dat'):
                        with path.open('rb') as stream:
                            if b'smoke.buffered' in stream.read(1048576):
                                return True
                    return False
                eventually(buffered_event)
                run('docker', 'restart', cname)
                run(*cmd, 'start', 'auth')
                eventually(lambda: 'smoke.buffered' in request(query, viewer)[1], 90)
                assert 'never-forward-fixture' not in request(query, viewer)[1]
            except Exception:
                output = subprocess.run(['docker', 'logs', '--tail', '30', cname], capture_output=True, text=True)
                diagnostic = output.stdout + output.stderr
                for value in MASKS:
                    diagnostic = diagnostic.replace(value, '[REDACTED]')
                print(diagnostic)
                raise
            finally:
                subprocess.run(['docker', 'rm', '-f', cname], stdout=subprocess.DEVNULL, check=False)
            print('PASS auth roles, persistence, real journald cursor/restart, panel/node diagnostics, Vector redaction, buffered outage recovery')
        except Exception:
            # Only synthetic credentials/events exist in this isolated job.
            # Still mask generated secrets before showing startup diagnostics.
            logs = subprocess.run([*cmd, 'logs', '--no-color', '--tail', '30'], capture_output=True, text=True)
            output = logs.stdout + logs.stderr
            for value in [state['viewer']['password'], *state['writers'].values()]:
                output = output.replace(value, '[REDACTED]')
            print(output)
            raise
        finally:
            subprocess.run([*cmd, 'down', '--volumes'], check=False)


if __name__ == '__main__':
    main()
