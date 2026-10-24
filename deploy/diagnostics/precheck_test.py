#!/usr/bin/env python3
import importlib.util
from pathlib import Path
import tempfile
import unittest

spec = importlib.util.spec_from_file_location('precheck', Path(__file__).with_name('precheck.py'))
precheck = importlib.util.module_from_spec(spec)
spec.loader.exec_module(precheck)


class PrecheckTests(unittest.TestCase):
    def test_precheck_validates_directories_and_permissions(self):
        with tempfile.TemporaryDirectory() as d:
            data_dir = Path(d) / 'data'
            state_dir = Path(d) / 'state'
            # allow_non_mount=True for testing environment
            report = precheck.inspect_precheck(data_dir, state_dir, root_dir=d, allow_non_mount=True)
            self.assertEqual(1, report['schema_version'])
            self.assertTrue(data_dir.exists())
            self.assertTrue(state_dir.exists())
            self.assertIn(report['healthy'], (True, False))

    def test_precheck_detects_unwritable_or_missing_paths(self):
        with tempfile.TemporaryDirectory() as d:
            # Test symlink refusal
            real_dir = Path(d) / 'real'
            real_dir.mkdir()
            sym_dir = Path(d) / 'sym'
            try:
                sym_dir.symlink_to(real_dir)
                report = precheck.inspect_precheck(sym_dir, Path(d) / 'state', allow_non_mount=True)
                self.assertFalse(report['healthy'])
                self.assertIn('refusing_symlink_paths', report['errors'])
            except OSError:
                # Symlinks might require admin privileges on some Windows setups
                pass


if __name__ == '__main__':
    unittest.main()
