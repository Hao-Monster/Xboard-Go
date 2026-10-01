"""Emit only scoped runtime counters, never Docker environment or health output."""
import argparse
import json
import os
import re
import socket
import subprocess
import urllib.request


def runtime_record(state):
    return {'event': 'panel.runtime', 'service': 'xboard-observer',
            'container_state': state.get('Status', 'unknown') if state.get('Status') in ('running', 'exited', 'restarting', 'dead', 'created', 'paused') else 'unknown',
            'oom_killed': bool(state.get('OOMKilled')), 'exit_code': int(state.get('ExitCode', 0)),
            'healthy': state.get('Health', {}).get('Status') == 'healthy'}


def counters(text):
    result = {'buffer_bytes': 0, 'discarded_events': 0, 'collector_errors': 0}
    names = {'buffer_byte_size': 'buffer_bytes', 'component_discarded_events_total': 'discarded_events',
             'component_errors_total': 'collector_errors'}
    for line in text.splitlines():
        match = re.fullmatch(r'(?:vector_)?([a-z_]+)(?:\{[^\r\n]*\})? ([0-9]+(?:\.[0-9]+)?(?:[eE][+-]?[0-9]+)?)', line)
        if match and match[1] in names:
            result[names[match[1]]] += float(match[2])
    return result


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--source', choices=['development', 'production'], required=True)
    source = parser.parse_args().source
    expected = 'bingo' if source == 'development' else 'vmi3574179'
    if socket.gethostname() != expected:
        raise ValueError('Unexpected probe host')
    container = 'xboard-go-freedom-app-1' if source == 'development' else 'xboard-production-internal-app-1'
    record = {'event': 'panel.runtime', 'service': 'xboard-observer', 'healthy': False}
    try:
        result = subprocess.run(['docker', 'inspect', container, '--format', '{{json .State}}'], capture_output=True, text=True, timeout=10, check=True)
        record = runtime_record(json.loads(result.stdout))
        count = subprocess.run(['docker', 'inspect', container, '--format', '{{.RestartCount}}'], capture_output=True, text=True, timeout=10, check=True)
        record['restart_count'] = int(count.stdout.strip())
        record['disk_free_bytes'] = os.statvfs('/').f_bavail * os.statvfs('/').f_frsize
        with urllib.request.urlopen('http://127.0.0.1:19429/metrics', timeout=5) as response:
            record.update(counters(response.read(2 * 1024 * 1024).decode()))
    except Exception as error:
        record['error_type'] = type(error).__name__
    # A fixed journal tag enters the same persisted source/cursor pipeline. No
    # user-controlled journal field, raw Docker data or health-check body is sent.
    entry = 'MESSAGE=' + json.dumps(record, separators=(',', ':')) + '\nCONTAINER_TAG=xboard-panel-' + source + '\nSYSLOG_IDENTIFIER=xboard-logs-probe\n'
    subprocess.run(['logger', '--journald'], input=entry, text=True, check=True, timeout=5)


if __name__ == '__main__':
    main()
