#!/usr/bin/env python3
"""Real isolated Docker smoke. Requires Linux Docker; never targets existing deployment."""
import base64
import json
from pathlib import Path
import subprocess
import tempfile
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid
import configure


def run(*args):
    subprocess.run(args, check=True, stdout=subprocess.DEVNULL)


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
    with tempfile.TemporaryDirectory(prefix=name) as temp:
        root = Path(temp)
        configure.central(root, ['smoke'])
        state = json.loads((root / 'secrets.json').read_text())
        writer = 'Bearer ' + state['writers']['smoke']
        viewer = 'Basic ' + base64.b64encode(('viewer:' + state['viewer']['password']).encode()).decode()
        compose = json.loads((root / 'compose.yaml').read_text())
        compose['name'] = name
        compose['services']['auth']['ports'] = ['127.0.0.1::8427']
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
            assert request(endpoint + '/debug/pprof/', viewer)[0] >= 400
            assert request(endpoint + '/insert/jsonline', writer, b'{"message":"smoke.persisted","source":"smoke"}\n')[0] == 200
            eventually(lambda: 'smoke.persisted' in request(query, viewer)[1])
            run(*cmd, 'restart', 'victoria-logs')
            eventually(lambda: 'smoke.persisted' in request(query, viewer)[1])

            collector = root / 'collector'
            configure.collector(collector, 'smoke', state['writers']['smoke'], 'xboard-panel-smoke', endpoint)
            config = json.loads((collector / 'collector.yaml').read_text())
            config['sources']['journal'] = {'type': 'file', 'include': ['/fixtures/events.jsonl'], 'read_from': 'beginning'}
            config['sources'].pop('metrics')
            config['sinks'].pop('metrics')
            config['tests'] = [{
                'name': 'drops payload secrets while retaining event identity',
                'inputs': [{'insert_at': 'safe', 'type': 'log', 'log_fields': {
                    'message': '{"msg":"http.error","status":500,"password":"fixture-secret","body":"fixture-body","error":"fixture-error"}'}}],
                'outputs': [{'extract_from': 'safe', 'conditions': [{'type': 'vrl', 'source':
                    'assert_eq!(.event, "http.error")\nassert_eq!(.status, 500)\nassert!(!exists(.password))\nassert!(!exists(.body))\nassert!(!exists(.error))'}]}]}]
            configure.write(collector / 'collector.yaml', config)
            fixtures = root / 'fixtures'
            fixtures.mkdir()
            events = fixtures / 'events.jsonl'
            events.write_text('', encoding='utf-8')
            mount = ['--mount', f'type=bind,src={collector},dst=/config', '--mount', f'type=bind,src={fixtures},dst=/fixtures',
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
                eventually(lambda: any(p.stat().st_size > 0 for p in (collector / 'vector-data').rglob('*.dat')))
                run('docker', 'restart', cname)
                run(*cmd, 'start', 'auth')
                eventually(lambda: 'smoke.buffered' in request(query, viewer)[1], 90)
                assert 'never-forward-fixture' not in request(query, viewer)[1]
            finally:
                subprocess.run(['docker', 'rm', '-f', cname], stdout=subprocess.DEVNULL, check=False)
            print('PASS auth roles, persistence, Vector redaction, buffered outage recovery')
        finally:
            subprocess.run([*cmd, 'down', '--volumes'], check=False)


if __name__ == '__main__':
    main()
