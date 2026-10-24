#!/usr/bin/env python3
"""Generate customer connection diagnostics configuration. Never print credentials."""
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
VECTOR = os.environ.get('XBOARD_DIAGNOSTICS_COLLECTOR_IMAGE', 'xboard-log-collector:local')

# Pinned and allowed event families and events
ALLOWED_FAMILIES = {'connection', 'resolver_operation', 'node'}

ALLOWED_EVENTS = {
    'connection': [
        'diag.connection.open',
        'diag.admission.rejected',
        'diag.route.result',
        'diag.route.step',
        'diag.dns.result',
        'diag.dial.result',
        'diag.dial.attempt',
        'diag.relay.result',
        'diag.target.summary',
        'diag.connection.close',
    ],
    'resolver_operation': [
        'diag.dns.exchange.result',
        'diag.dns.attempt',
    ],
    'node': [
        'diag.observer.health',
        'collector.canary',
    ],
}

FORBIDDEN_FIELDS = [
    'user_id', 'uuid', 'token', 'subscribe_url', 'email', 'trade_no', 'order_id',
    'client_ip', 'source_ip', 'dest_ip', 'resolved_ip', 'ecs_subnet',
    'raw_url', 'sni', 'ech', 'alpn', 'headers', 'stack_raw',
    'waiting_customers', 'customer_ids',
]


def identifier(value):
    if not re.fullmatch(r'[a-z][a-z0-9_-]{0,63}', value):
        raise ValueError(f'identifier is invalid: {value}')
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


def service(image, memory, cpus='1.0'):
    return dict(
        image=image,
        user=runtime_user(),
        restart='unless-stopped',
        mem_limit=memory,
        cpus=cpus,
        security_opt=['no-new-privileges:true'],
        cap_drop=['ALL'],
        logging={'driver': 'local', 'options': {'max-size': '10m', 'max-file': '3'}},
    )


def runtime_user():
    return str(os.getuid() if hasattr(os, 'getuid') else 0) + ':' + str(os.getgid() if hasattr(os, 'getgid') else 0)


def journal_groups():
    if os.name != 'posix':
        return []
    try:
        import grp
        return [str(grp.getgrnam('systemd-journal').gr_gid)]
    except (KeyError, ImportError):
        return []


def central(directory, scope, sources, port=19429):
    scope = identifier(scope)
    directory = Path(directory).resolve()
    directory.mkdir(parents=True, exist_ok=True, mode=0o700)
    secret_path = directory / 'secrets.json'
    if secret_path.is_symlink():
        raise ValueError('refusing symlink secrets')

    state = json.loads(secret_path.read_text()) if secret_path.exists() else {
        'viewer': {'username': 'viewer', 'password': secrets.token_urlsafe(36)},
        'writers': {},
        'panel_scope': scope,
    }
    state['panel_scope'] = scope
    for source in sources:
        src_id = identifier(source)
        state['writers'].setdefault(src_id, secrets.token_urlsafe(36))
    write(secret_path, state)

    # vmauth configuration:
    # 1. Viewer can only access select endpoints and health, cannot insert
    # 2. Writers can only access /insert/jsonline routed through guard with fixed extra_fields
    users = [
        {
            'username': state['viewer']['username'],
            'password': state['viewer']['password'],
            'url_map': [
                {'src_paths': ['/select/.*'], 'url_prefix': 'http://victoria-logs:9428/'},
                {'src_paths': ['/diagnostics/health'], 'url_prefix': 'http://guard:8429/diagnostics/health'},
            ],
        }
    ]

    for source, token in state['writers'].items():
        # Match node ID from source if pattern node-(\d+), else default to source
        m = re.match(r'node-(\d+)', source)
        node_id_str = m.group(1) if m else '0'
        extra_fields = f'panel_scope:{scope},node_id:{node_id_str},collector_id:{source}'
        stream_fields = 'panel_scope,node_id,service,event_family'
        prefix = (
            f'http://guard:8429/insert/jsonline?extra_fields={extra_fields}'
            f'&_stream_fields={stream_fields}&_time_field=event_time&_msg_field=event'
        )
        users.append({
            'bearer_token': token,
            'url_map': [{
                'src_paths': ['/insert/jsonline'],
                'url_prefix': prefix,
            }],
        })

    write(directory / 'auth.json', {'users': users})

    # VictoriaLogs service
    # 30 GiB volume, 3-day retention, 20 GiB maxDiskSpaceUsageBytes soft target, 5 GiB minFreeDiskSpaceBytes
    vl = service(VL, '1536m', cpus='1.0')
    vl.update(
        command=[
            '-storageDataPath=/storage',
            '-retentionPeriod=3d',
            '-retention.maxDiskSpaceUsageBytes=21474836480',
            '-storage.minFreeDiskSpaceBytes=5368709120',
            '-memory.allowedPercent=70',
        ],
        volumes=['./data:/storage'],
    )

    # Lightweight Guard service
    guard = service('python:3.11-alpine', '64m', cpus='0.25')
    guard.update(
        command=[
            'python3', '/app/guard.py',
            '--port=8429',
            '--data-dir=/storage',
            '--state-dir=/state',
            '--upstream=http://victoria-logs:9428',
            f'--scope={scope}',
        ],
        volumes=[
            './data:/storage:ro',
            './state:/state',
            './guard.py:/app/guard.py:ro',
        ],
        depends_on=['victoria-logs'],
    )

    # vmauth service (port on loopback only)
    auth = service(AUTH, '128m', cpus='0.25')
    auth.update(
        command=[
            '-auth.config=/config/auth.json',
            '-httpListenAddr=:8427',
            '-httpInternalListenAddr=127.0.0.1:8426',
        ],
        volumes=['./auth.json:/config/auth.json:ro'],
        ports=[f'127.0.0.1:{port}:8427'],
        depends_on=['victoria-logs', 'guard'],
    )

    (directory / 'data').mkdir(exist_ok=True, mode=0o700)
    (directory / 'state').mkdir(exist_ok=True, mode=0o700)

    # Copy guard.py next to compose.yaml for container mounting
    guard_src = Path(__file__).with_name('guard.py')
    if guard_src.exists():
        (directory / 'guard.py').write_text(guard_src.read_text(encoding='utf-8'), encoding='utf-8')

    compose = {
        'name': 'xboard-customer-diagnostics-pilot',
        'services': {
            'victoria-logs': vl,
            'guard': guard,
            'auth': auth,
        },
    }
    write(directory / 'compose.yaml', compose)
    write(directory / 'sources.json', sorted(state['writers']))


def remap():
    """Build VRL (Vector Remap Language) for strict diagnostic family filtering,
    RFC3339 event_time validation, removal of raw message & forbidden fields."""
    lines = [
        '# Parse JSON payload from .message or object',
        'raw_record = parse_json(.message) ?? {}',
        'record = object(raw_record) ?? {}',
        '',
        '# Enforce schema_version == 1',
        'schema_ver = int(record.schema_version) ?? 0',
        'if schema_ver != 1 {',
        '    abort',
        '}',
        '',
        '# Event and family validation against whitelist',
        'family = string(record.event_family) ?? ""',
        'event_name = string(record.event) ?? ""',
        '',
        '# Strict family and event whitelist checking',
        'is_allowed = false',
        'if family == "connection" {',
        '    if match(event_name, r\'^diag\\.(connection\\.(open|close)|admission\\.rejected|route\\.(result|step)|dns\\.result|dial\\.(result|attempt)|relay\\.result|target\\.summary)$\') {',
        '        is_allowed = true',
        '    }',
        '} else if family == "resolver_operation" {',
        '    if match(event_name, r\'^diag\\.dns\\.(exchange\\.result|attempt)$\') {',
        '        is_allowed = true',
        '    }',
        '} else if family == "node" {',
        '    if match(event_name, r\'^(diag\\.observer\\.health|collector\\.canary)$\') {',
        '        is_allowed = true',
        '    }',
        '}',
        '',
        'if !is_allowed {',
        '    abort',
        '}',
        '',
        '# Validate event_time strictly with RFC 3339. Do NOT fallback to ingress time.',
        'raw_time = string(record.event_time) ?? ""',
        'parsed_time, err = parse_timestamp(raw_time, "%+")',
        'if err != null {',
        '    abort',
        '}',
        '',
        '# Redact/suppress raw messages, credentials, SQL, URLs, and forbidden fields',
        'cursor = string(.__CURSOR) ?? ""',
        'event_id = string(record.event_id) ?? ""',
        'if event_id == "" && cursor != "" {',
        '    event_id = sha2(cursor, variant: "SHA-256")',
        '}',
        '',
        '# Initialize sanitized diagnostic object',
        '. = {}',
        '.schema_version = 1',
        '.event_family = family',
        '.event = event_name',
        '.event_time = raw_time',
        '.event_id = event_id',
        '.collected_at = now()',
    ]

    # Explicit allowed structured fields
    allowed_fields = [
        'panel_scope', 'node_id', 'runtime_id', 'diagnostic_id', 'connection_id',
        'target_id', 'seq', 'stage', 'observation_mode', 'session_id', 'build_revision',
        'payload_truncated', 'trace_id', 'span_id', 'parent_span_id', 'span_name',
        'span_start_time', 'span_duration_ms', 'span_status', 'span_links',
        'network', 'protocol', 'requested_host', 'requested_port', 'observed_domain',
        'domain_source', 'outbound_tag', 'rule_id', 'selected_target_address',
        'socket_peer', 'has_error', 'last_error_stage', 'last_reason_code',
        'error_stages', 'reason_codes', 'transport_state', 'summary_reason',
        'targets_evicted_total', 'resolver_operation_id', 'node_resolution',
        'dns_rcode', 'dns_answers', 'dns_answers_total', 'dns_answers_truncated',
        'dns_cache_hit', 'dns_query_type', 'device_limit', 'active_ip_count',
        'attempt_id', 'duration_ms', 'bytes_up', 'bytes_down', 'close_reason',
        'reason_code', 'error_type', 'timeout', 'errno', 'safe_message', 'exception',
        'operation_seq', 'dns_server', 'dns_transport', 'detail_started_mid_operation',
        'runtime_seq', 'observer_state', 'control_epoch', 'control_version',
        'active_leases', 'queue_depth', 'queue_bytes', 'dropped_total',
        'dropped_detail', 'dropped_baseline', 'evicted_targets_total',
    ]

    for f in allowed_fields:
        lines.append(
            f'if exists(record.{f}) {{ .{f} = record.{f} }}'
        )

    # Clean message to prevent raw log propagation
    lines.extend([
        '# Never emit raw or freeform message',
        'del(.message)',
        'del(.raw)',
    ])

    return '\n'.join(lines)


def collector(directory, source, token, tag, endpoint, scope, node_id, unit=None, metrics_port=19430):
    source = identifier(source)
    identifier(tag)
    scope = identifier(scope)
    node_id = int(node_id)
    url = urlsplit(endpoint)
    if url.scheme != 'http' or url.hostname not in ('127.0.0.1', 'localhost') or url.username or url.password or url.query or url.fragment or url.path not in ('', '/'):
        raise ValueError('collector endpoint must be a loopback HTTP SSH tunnel')
    if not re.fullmatch(r'[A-Za-z0-9_-]{32,128}', token):
        raise ValueError('invalid writer token')

    sources = {
        'journal': {
            'type': 'journald',
            'current_boot_only': False,
            'emit_cursor': True,
            'include_matches': {'CONTAINER_TAG': [tag]},
            'extra_args': ['CONTAINER_TAG=' + tag],
        }
    }
    if unit:
        if not re.fullmatch(r'xboard[-a-zA-Z0-9@_.]*\.service', unit):
            raise ValueError('only exact Xboard service units are supported')
        sources['node_journal'] = {
            'type': 'journald',
            'current_boot_only': False,
            'emit_cursor': True,
            'include_units': [unit],
            'extra_args': ['_SYSTEMD_UNIT=' + unit],
        }

    inputs = list(sources)
    sources['canary'] = {
        'type': 'demo_logs',
        'format': 'shuffle',
        'interval': 60,
        'lines': [
            json.dumps({
                'schema_version': 1,
                'event': 'collector.canary',
                'event_family': 'node',
                'event_time': '2026-10-25T00:00:00Z',
                'runtime_id': '0' * 32,
                'runtime_seq': 0,
                'panel_scope': scope,
                'node_id': node_id,
            })
        ],
    }
    sources['internal_metrics'] = {'type': 'internal_metrics', 'scrape_interval_secs': 30}

    config = {
        'data_dir': '/var/lib/vector-diagnostics',
        'sources': sources,
        'transforms': {
            'diag_filter': {
                'type': 'remap',
                'inputs': inputs + ['canary'],
                'source': remap(),
            }
        },
        'sinks': {
            'diagnostics_sink': {
                'type': 'http',
                'inputs': ['diag_filter'],
                'uri': endpoint.rstrip('/') + '/insert/jsonline',
                'auth': {'strategy': 'bearer', 'token': token},
                'compression': 'gzip',
                'encoding': {'codec': 'json'},
                'framing': {'method': 'newline_delimited'},
                'buffer': {
                    'type': 'disk',
                    'max_size': 2147483648,  # 2 GiB dedicated disk buffer
                    'when_full': 'block',
                },
                'acknowledgements': {'enabled': True},
                'healthcheck': {'enabled': False},
                'batch': {'timeout_secs': 2},
            },
            'metrics': {
                'type': 'prometheus_exporter',
                'inputs': ['internal_metrics'],
                'address': f'127.0.0.1:{metrics_port}',
            },
        },
    }

    directory = Path(directory).resolve()
    write(directory / 'collector.yaml', config)
    (directory / 'vector-data').mkdir(exist_ok=True, mode=0o700)

    vector = service(VECTOR, '384m', cpus='1.0')
    vector.update(
        command=['--config', '/etc/vector/collector.yaml'],
        network_mode='host',
        group_add=journal_groups(),
        volumes=[
            './collector.yaml:/etc/vector/collector.yaml:ro',
            './vector-data:/var/lib/vector-diagnostics',
            '/var/log/journal:/var/log/journal:ro',
            '/run/log/journal:/run/log/journal:ro',
            '/etc/machine-id:/etc/machine-id:ro',
        ],
    )
    write(
        directory / 'collector-compose.yaml',
        {'name': f'xboard-diag-collector-{source}', 'services': {'collector': vector}},
    )


def main():
    parser = argparse.ArgumentParser(description='Customer connection diagnostics config generator.')
    sub = parser.add_subparsers(dest='command', required=True)

    c = sub.add_parser('central')
    c.add_argument('--directory', required=True)
    c.add_argument('--scope', required=True, help='Panel scope ID')
    c.add_argument('--source', action='append', required=True, help='Source identifier (e.g. node-701)')
    c.add_argument('--port', type=int, default=19429, help='Port for vmauth loopback listener')

    col = sub.add_parser('collector')
    for key in ['directory', 'source', 'scope', 'node-id', 'token-file', 'tag', 'endpoint']:
        col.add_argument('--' + key, required=True)
    col.add_argument('--unit', help='Optional systemd unit name')
    col.add_argument('--metrics-port', type=int, default=19430)

    args = parser.parse_args()
    if args.command == 'central':
        central(args.directory, args.scope, args.source, args.port)
    elif args.command == 'collector':
        token = Path(args.token_file).read_text().strip()
        collector(
            args.directory,
            args.source,
            token,
            args.tag,
            args.endpoint,
            args.scope,
            int(args.node_id),
            args.unit,
            args.metrics_port,
        )


if __name__ == '__main__':
    main()
