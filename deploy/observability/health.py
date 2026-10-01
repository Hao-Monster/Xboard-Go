#!/usr/bin/env python3
"""Local operator health check; output contains no credentials or raw log messages."""
import argparse
import base64
import datetime
import json
from pathlib import Path
import shutil
import urllib.parse
import urllib.request
from configure import write


def query(endpoint, credentials, expression):
    value = base64.b64encode((credentials['username'] + ':' + credentials['password']).encode()).decode()
    request = urllib.request.Request(endpoint.rstrip('/') + '/select/logsql/query',
                                     urllib.parse.urlencode({'query': expression, 'limit': 1}).encode(),
                                     headers={'Authorization': 'Basic ' + value})
    with urllib.request.urlopen(request, timeout=15) as response:
        return [json.loads(line) for line in response.read(65536).decode().splitlines() if line]


def inspect(directory, endpoint):
    directory = Path(directory)
    secrets = json.loads((directory / 'secrets.json').read_text())
    usage = shutil.disk_usage(directory)
    size = sum(p.stat().st_size for p in (directory / 'data').rglob('*') if p.is_file() and not p.is_symlink())
    warnings = []
    if size >= 30 * 1024**3:
        warnings.append('storage_budget_30_gib_exceeded')
    if usage.free < 5 * 1024**3:
        warnings.append('disk_free_below_5_gib')
    canaries = {}
    application_events = {}
    for source in sorted(secrets['writers']):
        try:
            rows = query(endpoint, secrets['viewer'], '_time:3m source:' + json.dumps(source) + ' event:collector.canary | sort by (_time) desc')
            canaries[source] = rows[0].get('_time') if rows else None
            if not rows:
                warnings.append('canary_missing:' + source)
            if source in ('development', 'production'):
                rows = query(endpoint, secrets['viewer'], '_time:3m source:' + json.dumps(source) + ' event:http.request | sort by (_time) desc')
                application_events[source] = rows[0].get('_time') if rows else None
                if not rows:
                    warnings.append('panel_journal_events_missing:' + source)
        except Exception as error:
            canaries[source] = None
            warnings.append('query_failed:' + source + ':' + type(error).__name__)
    return {'event': 'logging.health', 'time': datetime.datetime.now(datetime.timezone.utc).isoformat(),
            'healthy': not warnings, 'storage_bytes': size, 'disk_free_bytes': usage.free,
            'last_canary': canaries, 'last_panel_event': application_events, 'warnings': warnings}


if __name__ == '__main__':
    parser = argparse.ArgumentParser()
    parser.add_argument('--directory', required=True)
    parser.add_argument('--endpoint', default='http://127.0.0.1:19428')
    args = parser.parse_args()
    result = inspect(args.directory, args.endpoint)
    write(Path(args.directory) / 'health.json', result)
    print(json.dumps(result))
    raise SystemExit(0 if result['healthy'] else 1)
