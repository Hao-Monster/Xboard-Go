#!/usr/bin/env python3
"""Generate private logging configuration. Never print credentials."""
import argparse
import json
import os
from pathlib import Path
import re
import secrets
import tempfile
from urllib.parse import urlsplit

VL = 'victoriametrics/victoria-logs:v1.53.0@sha256:251121fa882af99b95ba0c230a4a2f412ea602d2698c64a96c58dc9842bb755d'
AUTH = 'victoriametrics/vmauth:v1.153.0@sha256:4ebf2f21490df3e8837302b85d9db6ac45765442dfa28db77ffacd3577035a74'
VECTOR = os.environ.get('XBOARD_LOG_COLLECTOR_IMAGE', 'xboard-log-collector:local')


def identifier(value):
    if not re.fullmatch(r'[a-z][a-z0-9-]{0,47}', value):
        raise ValueError('source/tag identifier is invalid')
    return value


def write(path, value):
    path = Path(path)
    path.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
    if path.is_symlink():
        raise ValueError('refusing symlink output')
    fd, name = tempfile.mkstemp(dir=path.parent)
    try:
        with os.fdopen(fd, 'w', encoding='utf-8', newline='\n') as stream:
            stream.write(json.dumps(value, indent=2) + '\n')
        os.chmod(name, 0o600)
        os.replace(name, path)
    finally:
        if os.path.exists(name):
            os.unlink(name)


def service(image, memory):
    return dict(image=image, user=runtime_user(), restart='unless-stopped', mem_limit=memory,
                cpus='1.0', security_opt=['no-new-privileges:true'],
                cap_drop=['ALL'], logging={'driver': 'local', 'options': {'max-size': '10m', 'max-file': '3'}})


def runtime_user():
    return str(os.getuid() if hasattr(os, 'getuid') else 0) + ':' + str(os.getgid() if hasattr(os, 'getgid') else 0)


def journal_groups():
    if os.name != 'posix':
        return []
    import grp
    return [str(grp.getgrnam('systemd-journal').gr_gid)]


def central(directory, sources):
    directory = Path(directory).resolve()
    directory.mkdir(parents=True, exist_ok=True, mode=0o700)
    secret_path = directory / 'secrets.json'
    if secret_path.is_symlink():
        raise ValueError('refusing symlink secrets')
    state = json.loads(secret_path.read_text()) if secret_path.exists() else {
        'viewer': {'username': 'viewer', 'password': secrets.token_urlsafe(36)}, 'writers': {}}
    for source in sources:
        state['writers'].setdefault(identifier(source), secrets.token_urlsafe(36))
    write(secret_path, state)
    users = [{'username': state['viewer']['username'], 'password': state['viewer']['password'],
              'url_map': [{'src_paths': ['/select/.*'], 'url_prefix': 'http://victoria-logs:9428/'}]}]
    for source, token in state['writers'].items():
        users.append({'bearer_token': token, 'url_map': [{
            'src_paths': ['/insert/jsonline'],
            'url_prefix': 'http://victoria-logs:9428/'}]})
    write(directory / 'auth.json', {'users': users})
    vl = service(VL, '1536m')
    vl.update(command=['-storageDataPath=/storage', '-retentionPeriod=30d',
                       '-storage.minFreeDiskSpaceBytes=5368709120', '-memory.allowedPercent=70'],
              volumes=['./data:/storage'])
    auth = service(AUTH, '128m')
    auth.update(command=['-auth.config=/config/auth.json', '-httpListenAddr=:8427',
                         '-httpInternalListenAddr=127.0.0.1:8426'],
                volumes=['./auth.json:/config/auth.json:ro'], ports=['127.0.0.1:19428:8427'],
                depends_on=['victoria-logs'])
    (directory / 'data').mkdir(exist_ok=True, mode=0o700)
    write(directory / 'compose.yaml', {'name': 'xboard-logs', 'services': {'victoria-logs': vl, 'auth': auth}})
    write(directory / 'sources.json', sorted(state['writers']))


def remap(source):
    # Free-form log messages, SQL, bodies, URLs and journal metadata are deliberately omitted.
    fields = ['level', 'service', 'environment', 'revision', 'request_id', 'method', 'route',
              'status', 'duration_ms', 'bytes', 'error_type', 'stack', 'machine_id', 'node_id',
              'administrator_id', 'user_id', 'job_id', 'route_id', 'knowledge_id', 'payment_id',
              'revision_number', 'count', 'outcome', 'config_revision', 'users_revision',
              'limit', 'expire', 'traffic', 'checked', 'paid', 'remaining', 'cancelled', 'completed', 'processed',
              'runtime_id', 'component', 'instance', 'report_correlation', 'retry', 'users_count',
              'added_count', 'removed_count', 'traffic_users_count', 'instances', 'version',
              'error_class', 'error_code', 'network_op', 'container_state', 'oom_killed', 'exit_code',
              'healthy', 'restart_count', 'disk_free_bytes', 'buffer_bytes', 'discarded_events', 'collector_errors']
    lines = ['record, err = parse_json(.message)', 'record = object(record) ?? {}',
             'attributes = object(record.attributes) ?? {}',
             'cursor = string(.__CURSOR) ?? ""', 'stamp = now()',
             'if exists(.timestamp) { stamp = .timestamp }',
             '. = {"source": ' + json.dumps(source) + ', "timestamp": stamp, "event": "unstructured_log_suppressed"}',
             'if cursor != "" { .event_id = sha2(cursor, variant: "SHA-256") }',
             'event = string(record.event) ?? string(attributes.event) ?? string(record.msg) ?? ""',
             'if match(event, r\'^[a-z][a-z0-9_]*(\\.[a-z0-9_]+)+$\') { .event = event }',
             'literal = string(record.msg) ?? string(record.message) ?? ""',
             'catalog = ' + json.dumps(json.loads(Path(__file__).with_name('message-catalog.json').read_text(encoding='utf-8')), ensure_ascii=False),
             'classified = get(catalog, [literal]) ?? null',
             'if .event == "unstructured_log_suppressed" && is_string(classified) { .event = classified }']
    for field in fields:
        lines.append(f'if !exists(record.{field}) && exists(attributes.{field}) {{ record.{field} = attributes.{field} }}')
        if field in ['stack', 'route']:
            # App logger is responsible for trusted route templates and argument-free stacks.
            lines.append(f'if is_string(record.{field}) {{ .{field} = truncate(string!(record.{field}), 4096) }}')
        else:
            lines.append(f'if is_string(record.{field}) || is_integer(record.{field}) || is_float(record.{field}) || is_boolean(record.{field}) {{ .{field} = record.{field} }}')
    lines.extend(['if !exists(.error_type) && is_string(attributes.error) { .error_type = attributes.error }',
                  'if is_string(record.source) { .code_source = truncate(string!(record.source), 4096) }',
                  '.message = .event', 'if is_string(classified) { .message = literal }'])
    return '\n'.join(lines)


def collector(directory, source, token, tag, endpoint, unit=None):
    source = identifier(source)
    identifier(tag)
    url = urlsplit(endpoint)
    if url.scheme != 'http' or url.hostname not in ('127.0.0.1', 'localhost') or url.username or url.password or url.query or url.fragment or url.path not in ('', '/'):
        raise ValueError('collector endpoint must be a loopback HTTP SSH tunnel')
    if not re.fullmatch(r'[A-Za-z0-9_-]{32,128}', token):
        raise ValueError('invalid writer token')
    sources = {'journal': {'type': 'journald', 'current_boot_only': False, 'emit_cursor': True,
                          'include_matches': {'CONTAINER_TAG': [tag]},
                          'extra_args': ['CONTAINER_TAG=' + tag]}}
    if unit:
        if not re.fullmatch(r'xboard[-a-zA-Z0-9@_.]*\.service', unit):
            raise ValueError('only exact Xboard service units are supported')
        sources['node_journal'] = {'type': 'journald', 'current_boot_only': False, 'emit_cursor': True,
                                   'include_units': [unit], 'extra_args': ['_SYSTEMD_UNIT=' + unit]}
    inputs = list(sources)
    sources['canary'] = {'type': 'demo_logs', 'format': 'shuffle', 'interval': 60,
                         'lines': ['{"event":"collector.canary"}']}
    sources['internal_metrics'] = {'type': 'internal_metrics', 'scrape_interval_secs': 30}
    config = {'data_dir': '/var/lib/vector', 'sources': sources,
              'transforms': {'safe': {'type': 'remap', 'inputs': inputs + ['canary'], 'source': remap(source)}},
              'sinks': {'logs': {'type': 'http', 'inputs': ['safe'],
                        'uri': endpoint.rstrip('/') + '/insert/jsonline?_stream_fields=source,service&_msg_field=message&_time_field=timestamp',
                        'auth': {'strategy': 'bearer', 'token': token},
                        'compression': 'gzip', 'encoding': {'codec': 'json'},
                        'framing': {'method': 'newline_delimited'},
                        'buffer': {'type': 'disk', 'max_size': 2147483648, 'when_full': 'block'},
                        'acknowledgements': {'enabled': True}, 'healthcheck': {'enabled': False},
                        'batch': {'timeout_secs': 2}},
                        'metrics': {'type': 'prometheus_exporter', 'inputs': ['internal_metrics'], 'address': '127.0.0.1:19429'}}}
    directory = Path(directory).resolve()
    write(directory / 'collector.yaml', config)
    (directory / 'vector-data').mkdir(exist_ok=True, mode=0o700)
    vector = service(VECTOR, '384m')
    vector.update(command=['--config', '/etc/vector/collector.yaml'], network_mode='host', group_add=journal_groups(),
                  volumes=['./collector.yaml:/etc/vector/collector.yaml:ro', './vector-data:/var/lib/vector',
                           '/var/log/journal:/var/log/journal:ro', '/run/log/journal:/run/log/journal:ro',
                           '/etc/machine-id:/etc/machine-id:ro'])
    write(directory / 'collector-compose.yaml', {'name': 'xboard-log-collector-' + source, 'services': {'collector': vector}})


def main():
    parser = argparse.ArgumentParser()
    sub = parser.add_subparsers(dest='command', required=True)
    c = sub.add_parser('central')
    c.add_argument('--directory', required=True)
    c.add_argument('--source', action='append', required=True)
    c = sub.add_parser('collector')
    for key in ['directory', 'source', 'token-file', 'tag', 'endpoint']:
        c.add_argument('--' + key, required=True)
    c.add_argument('--unit')
    args = parser.parse_args()
    if args.command == 'central':
        central(args.directory, args.source)
    else:
        collector(args.directory, args.source, Path(args.token_file).read_text().strip(), args.tag, args.endpoint, args.unit)


if __name__ == '__main__':
    main()
