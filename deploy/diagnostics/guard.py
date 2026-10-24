#!/usr/bin/env python3
"""Lightweight storage guard for customer connection diagnostics.
Monitors dedicated storage volume and root disk, controls ingestion gate,
manages persistent latching, and provides GET /diagnostics/health."""
import argparse
import datetime
from http.server import HTTPServer, BaseHTTPRequestHandler
import json
import os
from pathlib import Path
import shutil
import threading
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid

# Thresholds (in bytes)
GIB = 1024 ** 3
WARN_USED_BYTES = 16 * GIB
CRIT_USED_BYTES = 20 * GIB
STOP_USED_BYTES = 24 * GIB
STOP_FREE_BYTES = 6 * GIB
ROOT_MIN_FREE_FLOOR = 50 * GIB
ROOT_MIN_FREE_RATIO = 0.20
MAX_CHECK_AGE_SECONDS = 60
MAX_BODY_BYTES = 16 * 1024 * 1024


class GuardState:
    def __init__(self, data_dir, state_dir, panel_scope, storage_id=None):
        self.data_dir = Path(data_dir).resolve()
        self.state_dir = Path(state_dir).resolve()
        self.panel_scope = panel_scope
        self.state_dir.mkdir(parents=True, exist_ok=True, mode=0o700)
        self.data_dir.mkdir(parents=True, exist_ok=True, mode=0o700)

        # Persistent storage ID
        id_file = self.state_dir / 'storage_id'
        if id_file.exists():
            self.storage_id = id_file.read_text().strip()
        else:
            self.storage_id = storage_id or f'store-{uuid.uuid4().hex[:12]}'
            id_file.write_text(self.storage_id)

        self.latch_file = self.state_dir / 'latched'
        self.lock = threading.RLock()

        # In-memory check snapshot
        self.state = 'unknown'
        self.reason_codes = ['initial_check_pending']
        self.checked_at_utc = None
        self.last_check_monotonic = 0
        self.latched = self.latch_file.exists()
        self.data_used_bytes = 0
        self.data_available_bytes = 0
        self.root_available_bytes = 0

    def evaluate(self, root_dir='/'):
        now_mono = time.monotonic()
        now_utc = datetime.datetime.now(datetime.timezone.utc).isoformat()
        reason_codes = []
        state = 'ready'

        # Check persistent latch first
        latched = self.latch_file.exists()

        try:
            # Measure dedicated volume
            usage = shutil.disk_usage(self.data_dir)
            used = usage.used
            free = usage.free
            total = usage.total

            # Root volume check
            root_usage = shutil.disk_usage(root_dir)
            root_free = root_usage.free
            root_total = root_usage.total
            root_min_required = max(ROOT_MIN_FREE_FLOOR, int(root_total * ROOT_MIN_FREE_RATIO))

            # Capacity warnings and critical thresholds
            if used >= WARN_USED_BYTES:
                reason_codes.append('capacity_warning')
            if used >= CRIT_USED_BYTES:
                reason_codes.append('capacity_critical')

            # Hard stop conditions
            if used >= STOP_USED_BYTES:
                reason_codes.append('storage_data_limit_24_gib')
                state = 'blocked'
            if free <= STOP_FREE_BYTES:
                reason_codes.append('storage_data_free_below_6_gib')
                state = 'blocked'
            if root_free < root_min_required:
                reason_codes.append('root_free_below_threshold')
                state = 'blocked'

        except Exception as e:
            state = 'blocked'
            reason_codes.append(f'disk_check_error:{type(e).__name__}')
            used, free, root_free = 0, 0, 0

        # If any stop condition is triggered, latch permanently
        if state == 'blocked':
            latched = True
            try:
                self.latch_file.write_text(f'{now_utc}\n' + '\n'.join(reason_codes) + '\n')
            except Exception:
                pass

        if latched:
            state = 'blocked'
            if 'latched' not in reason_codes:
                reason_codes.append('latched')

        with self.lock:
            self.state = state
            self.reason_codes = sorted(set(reason_codes))
            self.checked_at_utc = now_utc
            self.last_check_monotonic = now_mono
            self.latched = latched
            self.data_used_bytes = used
            self.data_available_bytes = free
            self.root_available_bytes = root_free

            # Save snapshot to health.json
            self._save_health_file()

    def _save_health_file(self):
        health_data = self.get_health_dict()
        tmp_file = self.state_dir / 'health.json.tmp'
        target_file = self.state_dir / 'health.json'
        try:
            with open(tmp_file, 'w', encoding='utf-8') as f:
                json.dump(health_data, f, indent=2)
            os.replace(tmp_file, target_file)
        except Exception:
            if tmp_file.exists():
                tmp_file.unlink(missing_ok=True)

    def resume(self):
        """Explicit admin resume: remove latch file and re-evaluate immediately."""
        try:
            self.latch_file.unlink(missing_ok=True)
        except Exception:
            pass
        self.evaluate()
        return self.get_health_dict()

    def get_health_dict(self):
        with self.lock:
            age = int(time.monotonic() - self.last_check_monotonic) if self.last_check_monotonic > 0 else 999
            current_state = self.state
            current_reasons = list(self.reason_codes)

            # Monotonic freshness check: if older than 60s, state becomes blocked/unknown
            if age > MAX_CHECK_AGE_SECONDS:
                current_state = 'blocked'
                if 'check_stale' not in current_reasons:
                    current_reasons.append('check_stale')

            return {
                'schema_version': 1,
                'panel_scope': self.panel_scope,
                'storage_id': self.storage_id,
                'state': current_state,
                'reason_codes': sorted(set(current_reasons)),
                'checked_at_utc': self.checked_at_utc or datetime.datetime.now(datetime.timezone.utc).isoformat(),
                'check_age_seconds': age,
                'latched': self.latched,
                'data_used_bytes': self.data_used_bytes,
                'data_available_bytes': self.data_available_bytes,
                'root_available_bytes': self.root_available_bytes,
            }

    def can_ingest(self):
        health = self.get_health_dict()
        return health['state'] == 'ready' and not health['latched'] and health['check_age_seconds'] <= MAX_CHECK_AGE_SECONDS


class GuardHandler(BaseHTTPRequestHandler):
    def log_message(self, format, *args):
        # Suppress default stdout log spam
        pass

    @property
    def guard(self):
        return self.server.guard_state

    @property
    def upstream_url(self):
        return self.server.upstream_url

    def do_GET(self):
        path = urllib.parse.urlsplit(self.path).path
        if path == '/diagnostics/health':
            health = self.guard.get_health_dict()
            body = json.dumps(health, indent=2).encode('utf-8')
            status = 200 if health['state'] == 'ready' else 503
            self.send_response(status)
            self.send_header('Content-Type', 'application/json')
            self.send_header('Content-Length', str(len(body)))
            self.end_headers()
            self.wfile.write(body)
            return

        self.send_response(404)
        self.send_header('Content-Type', 'application/json')
        self.end_headers()
        self.wfile.write(b'{"error":"not_found"}')

    def do_POST(self):
        parsed = urllib.parse.urlsplit(self.path)
        path = parsed.path

        if path == '/diagnostics/resume':
            health = self.guard.resume()
            body = json.dumps(health, indent=2).encode('utf-8')
            self.send_response(200)
            self.send_header('Content-Type', 'application/json')
            self.send_header('Content-Length', str(len(body)))
            self.end_headers()
            self.wfile.write(body)
            return

        if path == '/insert/jsonline':
            # Check ingestion gate
            if not self.guard.can_ingest():
                health = self.guard.get_health_dict()
                resp = json.dumps({
                    'error': 'diagnostics_storage_not_ready',
                    'reasons': health['reason_codes'],
                    'latched': health['latched'],
                    'check_age_seconds': health['check_age_seconds'],
                }).encode('utf-8')
                self.send_response(503)
                self.send_header('Content-Type', 'application/json')
                self.send_header('Content-Length', str(len(resp)))
                self.end_headers()
                self.wfile.write(resp)
                return

            # Read body with bounded size
            content_length = int(self.headers.get('Content-Length', 0))
            if content_length > MAX_BODY_BYTES:
                self.send_response(413)
                self.send_header('Content-Type', 'application/json')
                self.end_headers()
                self.wfile.write(b'{"error":"payload_too_large"}')
                return

            body = self.rfile.read(content_length) if content_length > 0 else b''

            # Forward to upstream VictoriaLogs
            target = self.upstream_url.rstrip('/') + path
            if parsed.query:
                target += '?' + parsed.query

            req = urllib.request.Request(target, data=body, method='POST')
            for header, value in self.headers.items():
                if header.lower() not in ('host', 'content-length'):
                    req.add_header(header, value)

            try:
                with urllib.request.urlopen(req, timeout=10) as up_resp:
                    up_status = up_resp.status
                    up_headers = up_resp.headers
                    up_body = up_resp.read()
                    self.send_response(up_status)
                    for k, v in up_headers.items():
                        if k.lower() not in ('transfer-encoding', 'connection'):
                            self.send_header(k, v)
                    self.end_headers()
                    self.wfile.write(up_body)
            except urllib.error.HTTPError as up_err:
                err_body = up_err.read()
                self.send_response(up_err.code)
                for k, v in up_err.headers.items():
                    if k.lower() not in ('transfer-encoding', 'connection'):
                        self.send_header(k, v)
                self.end_headers()
                self.wfile.write(err_body)
            except Exception as e:
                resp = json.dumps({'error': 'upstream_unavailable', 'detail': str(e)}).encode('utf-8')
                self.send_response(502)
                self.send_header('Content-Type', 'application/json')
                self.send_header('Content-Length', str(len(resp)))
                self.end_headers()
                self.wfile.write(resp)
            return

        self.send_response(404)
        self.send_header('Content-Type', 'application/json')
        self.end_headers()
        self.wfile.write(b'{"error":"not_found"}')


from socketserver import ThreadingMixIn


class GuardServer(ThreadingMixIn, HTTPServer):
    daemon_threads = True

    def __init__(self, addr, guard_state, upstream_url):
        super().__init__(addr, GuardHandler)
        self.guard_state = guard_state
        self.upstream_url = upstream_url


def run_monitor(guard_state, stop_event, interval=30):
    while not stop_event.is_set():
        guard_state.evaluate()
        stop_event.wait(interval)


def main():
    parser = argparse.ArgumentParser(description='Diagnostics storage guard and health daemon.')
    parser.add_argument('--port', type=int, default=8429, help='Listen port')
    parser.add_argument('--bind', default='0.0.0.0', help='Listen IP address')
    parser.add_argument('--data-dir', default='./data', help='Storage data directory')
    parser.add_argument('--state-dir', default='./state', help='Guard state directory')
    parser.add_argument('--upstream', default='http://victoria-logs:9428', help='Upstream VictoriaLogs endpoint')
    parser.add_argument('--scope', required=True, help='Panel scope')
    parser.add_argument('--storage-id', help='Storage ID override')
    parser.add_argument('--interval', type=int, default=30, help='Check interval in seconds')

    args = parser.parse_args()

    state = GuardState(args.data_dir, args.state_dir, args.scope, args.storage_id)
    # Run initial check
    state.evaluate()

    stop_event = threading.Event()
    monitor_thread = threading.Thread(target=run_monitor, args=(state, stop_event, args.interval), daemon=True)
    monitor_thread.start()

    server = GuardServer((args.bind, args.port), state, args.upstream)
    try:
        server.serve_forever()
    except KeyboardInterrupt:
        pass
    finally:
        stop_event.set()
        server.server_close()


if __name__ == '__main__':
    main()
