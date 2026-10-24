#!/usr/bin/env python3
import importlib.util
import json
from pathlib import Path
import tempfile
import unittest

spec = importlib.util.spec_from_file_location('configure', Path(__file__).with_name('configure.py'))
configure = importlib.util.module_from_spec(spec)
spec.loader.exec_module(configure)


class DiagnosticsConfigurationTests(unittest.TestCase):
    def test_central_configuration_and_role_separation(self):
        with tempfile.TemporaryDirectory() as d:
            configure.central(d, 'xboard-scope-1', ['node-701'])
            first_secrets = json.loads((Path(d) / 'secrets.json').read_text())
            self.assertEqual('xboard-scope-1', first_secrets['panel_scope'])
            self.assertIn('node-701', first_secrets['writers'])
            token1 = first_secrets['writers']['node-701']

            # Idempotence: re-running preserves credentials
            configure.central(d, 'xboard-scope-1', ['node-701', 'node-702'])
            second_secrets = json.loads((Path(d) / 'secrets.json').read_text())
            self.assertEqual(token1, second_secrets['writers']['node-701'])
            self.assertIn('node-702', second_secrets['writers'])
            self.assertNotEqual(second_secrets['writers']['node-701'], second_secrets['writers']['node-702'])

            # Verify vmauth auth.json roles and permissions
            auth = json.loads((Path(d) / 'auth.json').read_text())
            users = auth['users']
            self.assertEqual(3, len(users))  # 1 viewer + 2 node writers

            viewer = users[0]
            self.assertEqual(second_secrets['viewer']['username'], viewer['username'])
            self.assertEqual(2, len(viewer['url_map']))
            self.assertEqual(['/select/.*'], viewer['url_map'][0]['src_paths'])
            self.assertEqual('http://victoria-logs:9428/', viewer['url_map'][0]['url_prefix'])
            self.assertEqual(['/diagnostics/health'], viewer['url_map'][1]['src_paths'])
            self.assertEqual('http://guard:8429/diagnostics/health', viewer['url_map'][1]['url_prefix'])

            # Verify writer extra_fields and fixed stream fields
            writer1 = users[1]
            self.assertEqual(token1, writer1['bearer_token'])
            self.assertEqual(['/insert/jsonline'], writer1['url_map'][0]['src_paths'])
            prefix = writer1['url_map'][0]['url_prefix']
            self.assertIn('extra_fields=panel_scope:xboard-scope-1,node_id:701,collector_id:node-701', prefix)
            self.assertIn('_stream_fields=panel_scope,node_id,service,event_family', prefix)
            self.assertIn('_time_field=event_time', prefix)
            self.assertIn('_msg_field=event', prefix)

            # Verify compose.yaml services: victoria-logs, guard, auth
            compose = json.loads((Path(d) / 'compose.yaml').read_text())
            services = compose['services']
            self.assertIn('victoria-logs', services)
            self.assertIn('guard', services)
            self.assertIn('auth', services)

            vl = services['victoria-logs']
            self.assertNotIn('ports', vl)  # not exposed to host
            self.assertIn('-retentionPeriod=3d', vl['command'])
            self.assertIn('-retention.maxDiskSpaceUsageBytes=21474836480', vl['command'])
            self.assertIn('-storage.minFreeDiskSpaceBytes=5368709120', vl['command'])
            self.assertEqual('1536m', vl['mem_limit'])
            self.assertEqual('1.0', vl['cpus'])

            guard = services['guard']
            self.assertIn('--port=8429', guard['command'])
            self.assertIn('--scope=xboard-scope-1', guard['command'])
            self.assertEqual('64m', guard['mem_limit'])
            self.assertEqual('0.25', guard['cpus'])

            auth_svc = services['auth']
            self.assertEqual(['127.0.0.1:19429:8427'], auth_svc['ports'])
            self.assertEqual('128m', auth_svc['mem_limit'])
            self.assertEqual('0.25', auth_svc['cpus'])

    def test_collector_configuration_and_vrl_remap(self):
        with tempfile.TemporaryDirectory() as d:
            configure.collector(
                d,
                source='node-701',
                token='x' * 48,
                tag='xboard-node-701',
                endpoint='http://127.0.0.1:19429',
                scope='scope-test',
                node_id=701,
            )
            config = json.loads((Path(d) / 'collector.yaml').read_text())

            # Data directory separate from legacy operational logging
            self.assertEqual('/var/lib/vector-diagnostics', config['data_dir'])

            # 2 GiB dedicated disk buffer
            sink = config['sinks']['diagnostics_sink']
            self.assertEqual('disk', sink['buffer']['type'])
            self.assertEqual(2147483648, sink['buffer']['max_size'])
            self.assertEqual('block', sink['buffer']['when_full'])
            self.assertTrue(sink['acknowledgements']['enabled'])

            # VRL transform inspect
            vrl = config['transforms']['diag_filter']['source']
            self.assertIn('family == "connection"', vrl)
            self.assertIn('family == "resolver_operation"', vrl)
            self.assertIn('family == "node"', vrl)
            self.assertIn('parse_timestamp(raw_time, "%+")', vrl)
            self.assertIn('del(.message)', vrl)
            self.assertIn('del(.raw)', vrl)
            self.assertIn('.schema_version = 1', vrl)
            self.assertIn('.collected_at = now()', vrl)

            # Compose mounts
            compose = json.loads((Path(d) / 'collector-compose.yaml').read_text())
            collector_svc = compose['services']['collector']
            self.assertNotIn('docker.sock', json.dumps(collector_svc))

    def test_rejects_invalid_identifiers_and_endpoints(self):
        with tempfile.TemporaryDirectory() as d:
            # Bad scope
            with self.assertRaises(ValueError):
                configure.central(d, '../escape', ['node-1'])
            # Bad endpoint
            with self.assertRaises(ValueError):
                configure.collector(
                    d, 'node-1', 'x' * 48, 'tag1', 'http://remote.host:19429', 'scope1', 1
                )
            # Bad token
            with self.assertRaises(ValueError):
                configure.collector(
                    d, 'node-1', 'short', 'tag1', 'http://127.0.0.1:19429', 'scope1', 1
                )


if __name__ == '__main__':
    unittest.main()
