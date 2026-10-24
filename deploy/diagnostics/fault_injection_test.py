#!/usr/bin/env python3
"""Fault injection and recovery tests for Customer Connection Diagnostics (T11, T17, T18).
Simulates storage exhaustion, process crashes, latch survival, and recovery."""
import importlib.util
import json
from pathlib import Path
import tempfile
import threading
import time
import unittest
import urllib.error
import urllib.request

spec = importlib.util.spec_from_file_location('guard', Path(__file__).with_name('guard.py'))
guard_mod = importlib.util.module_from_spec(spec)
spec.loader.exec_module(guard_mod)


class FaultInjectionTests(unittest.TestCase):
    def test_storage_exhaustion_and_persistent_latching(self):
        """T17/T18: Storage limits (>= 24 GiB or <= 6 GiB free) trigger 503 and persistent latch."""
        with tempfile.TemporaryDirectory() as d:
            data_dir = Path(d) / 'data'
            state_dir = Path(d) / 'state'
            state = guard_mod.GuardState(data_dir, state_dir, 'scope-fault')

            class MockDataUsage:
                used = 25 * guard_mod.GIB
                free = 4 * guard_mod.GIB
                total = 30 * guard_mod.GIB

            class MockRootUsage:
                used = 50 * guard_mod.GIB
                free = 150 * guard_mod.GIB
                total = 200 * guard_mod.GIB

            orig_usage = guard_mod.shutil.disk_usage
            try:
                def fake_usage(p):
                    if str(p) == str(data_dir):
                        return MockDataUsage()
                    return MockRootUsage()

                guard_mod.shutil.disk_usage = fake_usage

                state.evaluate()
                health = state.get_health_dict()
                self.assertEqual('blocked', health['state'])
                self.assertIn('storage_data_limit_24_gib', health['reason_codes'])
                self.assertIn('storage_data_free_below_6_gib', health['reason_codes'])
                self.assertTrue(health['latched'])
                self.assertTrue(state.latch_file.exists())
                self.assertFalse(state.can_ingest())

                # Simulate restart: new GuardState instance
                restarted_state = guard_mod.GuardState(data_dir, state_dir, 'scope-fault')

                class RestoredDataUsage:
                    used = 5 * guard_mod.GIB
                    free = 25 * guard_mod.GIB
                    total = 30 * guard_mod.GIB

                def fake_restored_usage(p):
                    if str(p) == str(data_dir):
                        return RestoredDataUsage()
                    return MockRootUsage()

                guard_mod.shutil.disk_usage = fake_restored_usage
                restarted_state.evaluate()

                restarted_health = restarted_state.get_health_dict()
                # Must stay blocked because latch file is present!
                self.assertEqual('blocked', restarted_health['state'])
                self.assertTrue(restarted_health['latched'])
                self.assertIn('latched', restarted_health['reason_codes'])
                self.assertFalse(restarted_state.can_ingest())

                # Explicit resume clears latch
                resumed_health = restarted_state.resume()
                self.assertEqual('ready', resumed_health['state'])
                self.assertFalse(resumed_health['latched'])
                self.assertTrue(restarted_state.can_ingest())

            finally:
                guard_mod.shutil.disk_usage = orig_usage

    def test_guard_crash_and_upstream_error_handling(self):
        """T18: When upstream VictoriaLogs is down, guard returns 502 without crashing."""
        with tempfile.TemporaryDirectory() as d:
            data_dir = Path(d) / 'data'
            state_dir = Path(d) / 'state'
            state = guard_mod.GuardState(data_dir, state_dir, 'scope-crash')
            state.state = 'ready'
            state.latched = False
            state.last_check_monotonic = time.monotonic()

            # Point guard to an unreachable port
            server = guard_mod.GuardServer(('127.0.0.1', 0), state, 'http://127.0.0.1:59999')
            port = server.server_address[1]
            t = threading.Thread(target=server.serve_forever, daemon=True)
            t.start()

            try:
                req = urllib.request.Request(
                    f'http://127.0.0.1:{port}/insert/jsonline',
                    data=b'{"event":"test"}\n',
                    method='POST',
                )
                with self.assertRaises(urllib.error.HTTPError) as ctx:
                    urllib.request.urlopen(req, timeout=3)
                self.assertEqual(502, ctx.exception.code)
            finally:
                server.shutdown()

    def test_payload_too_large_rejection(self):
        """T12/T13: Ingestion guards reject oversized payloads (exceeding MAX_BODY_BYTES)."""
        with tempfile.TemporaryDirectory() as d:
            data_dir = Path(d) / 'data'
            state_dir = Path(d) / 'state'
            state = guard_mod.GuardState(data_dir, state_dir, 'scope-size')
            state.state = 'ready'
            state.latched = False
            state.last_check_monotonic = time.monotonic()

            server = guard_mod.GuardServer(('127.0.0.1', 0), state, 'http://127.0.0.1:59999')
            port = server.server_address[1]
            t = threading.Thread(target=server.serve_forever, daemon=True)
            t.start()

            try:
                # Send headers declaring oversized content-length
                req = urllib.request.Request(
                    f'http://127.0.0.1:{port}/insert/jsonline',
                    data=b'small',
                    headers={'Content-Length': str(guard_mod.MAX_BODY_BYTES + 100)},
                    method='POST',
                )
                with self.assertRaises(urllib.error.HTTPError) as ctx:
                    urllib.request.urlopen(req, timeout=3)
                self.assertEqual(413, ctx.exception.code)
            finally:
                server.shutdown()


if __name__ == '__main__':
    unittest.main()
