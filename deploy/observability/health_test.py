import datetime
import tempfile
import unittest
from unittest.mock import patch
import configure
import health


class HealthTests(unittest.TestCase):
    def test_all_sources_must_have_recent_canary(self):
        with tempfile.TemporaryDirectory() as d:
            configure.central(d, ['development', 'production'])
            with patch.object(health, 'query', side_effect=[[{'_time': datetime.datetime.now(datetime.timezone.utc).isoformat()}], []]):
                result = health.inspect(d, 'http://127.0.0.1:19428')
            self.assertFalse(result['healthy'])
            self.assertIn('canary_missing:production', result['warnings'])
            self.assertIsNone(result['last_canary']['production'])

    def test_failure_does_not_leak_exception_payload(self):
        with tempfile.TemporaryDirectory() as d:
            configure.central(d, ['development'])
            with patch.object(health, 'query', side_effect=RuntimeError('secret-fixture')):
                result = health.inspect(d, 'http://127.0.0.1:19428')
            self.assertIn('query_failed:development:RuntimeError', result['warnings'])
            self.assertNotIn('secret-fixture', str(result))


if __name__ == '__main__':
    unittest.main()
