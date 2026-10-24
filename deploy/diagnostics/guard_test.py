#!/usr/bin/env python3
import importlib.util
import json
from pathlib import Path
import tempfile
import time
import unittest
import urllib.request
import urllib.error

spec = importlib.util.spec_from_file_location('guard', Path(__file__).with_name('guard.py'))
guard_mod = importlib.util.module_from_spec(spec)
spec.loader.exec_module(guard_mod)


class HealthyUsage:
    def __init__(self, used, free, total):
        self.used = used
        self.free = free
        self.total = total


def mock_healthy_disk(p):
    # Simulate healthy 30 GiB data partition (5 GiB used, 25 GiB free)
    # and healthy 200 GiB root (50 GiB used, 150 GiB free)
    if 'data' in str(p):
        return HealthyUsage(5 * guard_mod.GIB, 25 * guard_mod.GIB, 30 * guard_mod.GIB)
    return HealthyUsage(50 * guard_mod.GIB, 150 * guard_mod.GIB, 200 * guard_mod.GIB)


class GuardTests(unittest.TestCase):
    def test_guard_state_evaluation_and_latch(self):
        orig_usage = guard_mod.shutil.disk_usage
        guard_mod.shutil.disk_usage = mock_healthy_disk
        try:
            with tempfile.TemporaryDirectory() as d:
                data_dir = Path(d) / 'data'
                state_dir = Path(d) / 'state'
                state = guard_mod.GuardState(data_dir, state_dir, 'scope-test-1')

                # Initial evaluation under normal conditions
                state.evaluate()
                health = state.get_health_dict()
                self.assertEqual(1, health['schema_version'])
                self.assertEqual('scope-test-1', health['panel_scope'])
                self.assertTrue(health['storage_id'].startswith('store-'))
                self.assertEqual('ready', health['state'])
                self.assertFalse(health['check_age_seconds'] > 5)

                # Test persistent latch survival
                latch_file = state_dir / 'latched'
                latch_file.write_text('manual_test_latch\n')
                state.evaluate()
                self.assertTrue(state.latched)
                self.assertEqual('blocked', state.state)
                self.assertIn('latched', state.reason_codes)

                # Test explicit resume clears latch
                state.resume()
                self.assertFalse(latch_file.exists())
                self.assertFalse(state.latched)
                self.assertEqual('ready', state.state)
        finally:
            guard_mod.shutil.disk_usage = orig_usage

    def test_check_stale_monotonic_clock(self):
        orig_usage = guard_mod.shutil.disk_usage
        guard_mod.shutil.disk_usage = mock_healthy_disk
        try:
            with tempfile.TemporaryDirectory() as d:
                data_dir = Path(d) / 'data'
                state_dir = Path(d) / 'state'
                state = guard_mod.GuardState(data_dir, state_dir, 'scope-test-2')
                state.evaluate()

                # Artificially age the monotonic check timestamp
                state.last_check_monotonic = time.monotonic() - 65
                health = state.get_health_dict()
                self.assertEqual('blocked', health['state'])
                self.assertIn('check_stale', health['reason_codes'])
                self.assertFalse(state.can_ingest())
        finally:
            guard_mod.shutil.disk_usage = orig_usage

    def test_server_http_endpoints_and_gating(self):
        orig_usage = guard_mod.shutil.disk_usage
        guard_mod.shutil.disk_usage = mock_healthy_disk
        try:
            with tempfile.TemporaryDirectory() as d:
                data_dir = Path(d) / 'data'
                state_dir = Path(d) / 'state'
                state = guard_mod.GuardState(data_dir, state_dir, 'scope-http')
                state.evaluate()

                # Start dummy upstream server
                dummy_upstream_hits = []

                class DummyUpstreamHandler(guard_mod.BaseHTTPRequestHandler):
                    def log_message(self, format, *args):
                        pass

                    def do_POST(self):
                        dummy_upstream_hits.append(self.path)
                        body = self.rfile.read(int(self.headers.get('Content-Length', 0)))
                        self.send_response(200)
                        self.end_headers()
                        self.wfile.write(b'{"status":"ok"}')

                upstream_server = guard_mod.HTTPServer(('127.0.0.1', 0), DummyUpstreamHandler)
                up_port = upstream_server.server_address[1]

                import threading
                up_thread = threading.Thread(target=upstream_server.serve_forever, daemon=True)
                up_thread.start()

                # Start Guard server
                guard_server = guard_mod.GuardServer(('127.0.0.1', 0), state, f'http://127.0.0.1:{up_port}')
                guard_port = guard_server.server_address[1]
                guard_thread = threading.Thread(target=guard_server.serve_forever, daemon=True)
                guard_thread.start()

                base_url = f'http://127.0.0.1:{guard_port}'

                try:
                    # 1. Test GET /diagnostics/health
                    req = urllib.request.Request(f'{base_url}/diagnostics/health')
                    try:
                        with urllib.request.urlopen(req, timeout=5) as resp:
                            self.assertIn(resp.status, (200, 503))
                            data = json.loads(resp.read().decode('utf-8'))
                            self.assertEqual('scope-http', data['panel_scope'])
                    except urllib.error.HTTPError as e:
                        self.assertEqual(503, e.code)

                    # 2. Test Ingestion Gating when state is blocked
                    state.latch_file.write_text('blocked_test')
                    state.evaluate()

                    req_post = urllib.request.Request(
                        f'{base_url}/insert/jsonline',
                        data=b'{"test":1}\n',
                        headers={'Content-Type': 'application/json'},
                        method='POST',
                    )
                    with self.assertRaises(urllib.error.HTTPError) as ctx:
                        urllib.request.urlopen(req_post, timeout=5)
                    self.assertEqual(503, ctx.exception.code)
                    self.assertEqual(0, len(dummy_upstream_hits))

                    # 3. Test POST /diagnostics/resume
                    req_resume = urllib.request.Request(
                        f'{base_url}/diagnostics/resume',
                        data=b'',
                        method='POST',
                    )
                    with urllib.request.urlopen(req_resume, timeout=5) as resp:
                        self.assertEqual(200, resp.status)
                        res_data = json.loads(resp.read().decode('utf-8'))
                        self.assertFalse(res_data['latched'])
                        self.assertEqual('ready', res_data['state'])

                finally:
                    guard_server.shutdown()
                    upstream_server.shutdown()
        finally:
            guard_mod.shutil.disk_usage = orig_usage


if __name__ == '__main__':
    unittest.main()
