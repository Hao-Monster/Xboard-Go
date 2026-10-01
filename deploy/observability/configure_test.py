import importlib.util
import json
from pathlib import Path
import tempfile
import unittest

spec = importlib.util.spec_from_file_location('configure', Path(__file__).with_name('configure.py'))
configure = importlib.util.module_from_spec(spec)
spec.loader.exec_module(configure)


class ConfigurationTests(unittest.TestCase):
    def test_central_idempotence_and_role_separation(self):
        with tempfile.TemporaryDirectory() as d:
            configure.central(d, ['development'])
            first = json.loads((Path(d) / 'secrets.json').read_text())
            configure.central(d, ['development', 'production'])
            second = json.loads((Path(d) / 'secrets.json').read_text())
            self.assertEqual(first['viewer'], second['viewer'])
            self.assertEqual(first['writers']['development'], second['writers']['development'])
            self.assertNotEqual(second['writers']['production'], second['writers']['development'])
            compose = json.loads((Path(d) / 'compose.yaml').read_text())
            self.assertNotIn('ports', compose['services']['victoria-logs'])
            self.assertEqual(['127.0.0.1:19428:8427'], compose['services']['auth']['ports'])
            self.assertIn('-retentionPeriod=30d', compose['services']['victoria-logs']['command'])
            self.assertFalse(any('maxDiskSpaceUsage' in v for v in compose['services']['victoria-logs']['command']))
            users = json.loads((Path(d) / 'auth.json').read_text())['users']
            self.assertEqual(['/select/.*'], users[0]['url_map'][0]['src_paths'])
            self.assertEqual(['/insert/jsonline'], users[1]['url_map'][0]['src_paths'])

    def test_collector_buffer_cursor_and_scope(self):
        with tempfile.TemporaryDirectory() as d:
            configure.collector(d, 'production', 'x' * 48, 'xboard-panel-production', 'http://127.0.0.1:19428')
            config = json.loads((Path(d) / 'collector.yaml').read_text())
            self.assertFalse(config['sources']['journal']['current_boot_only'])
            self.assertTrue(config['sources']['journal']['emit_cursor'])
            self.assertEqual({'CONTAINER_TAG': ['xboard-panel-production']}, config['sources']['journal']['include_matches'])
            self.assertEqual('block', config['sinks']['logs']['buffer']['when_full'])
            self.assertEqual(2147483648, config['sinks']['logs']['buffer']['max_size'])
            self.assertTrue(config['sinks']['logs']['acknowledgements']['enabled'])
            compose = json.loads((Path(d) / 'collector-compose.yaml').read_text())
            self.assertNotIn('docker.sock', json.dumps(compose))

    def test_rejects_public_endpoint_invalid_names_and_other_units(self):
        with tempfile.TemporaryDirectory() as d:
            for endpoint in ['http://example.com', 'https://127.0.0.1', 'http://127.0.0.1/x', 'http://u:p@127.0.0.1']:
                with self.assertRaises(ValueError):
                    configure.collector(d, 'production', 'x' * 48, 'xboard-panel-production', endpoint)
            with self.assertRaises(ValueError):
                configure.central(d, ['../escape'])
            with self.assertRaises(ValueError):
                configure.collector(d, 'production', 'x' * 48, 'xboard-panel-production', 'http://127.0.0.1', 'remnawave.service')


if __name__ == '__main__':
    unittest.main()
