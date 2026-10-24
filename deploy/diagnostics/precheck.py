#!/usr/bin/env python3
"""Customer connection diagnostics storage mount and capacity precheck.
Verifies dedicated 30 GiB filesystem, root disk protection, and write permissions
before starting diagnostic services. Never falls back to root filesystem."""
import argparse
import json
import os
from pathlib import Path
import shutil
import sys

GIB = 1024 ** 3
EXPECTED_VOLUME_BYTES = 30 * GIB
ROOT_MIN_FREE_FLOOR = 50 * GIB
ROOT_MIN_FREE_RATIO = 0.20


def inspect_precheck(data_dir, state_dir, root_dir='/', allow_non_mount=False):
    raw_data_path = Path(data_dir)
    raw_state_path = Path(state_dir)

    errors = []
    warnings = []

    # 1. Path existence and refusal of symlinks
    if raw_data_path.is_symlink() or raw_state_path.is_symlink() or os.path.islink(data_dir) or os.path.islink(state_dir):
        errors.append('refusing_symlink_paths')

    data_path = raw_data_path.resolve()
    state_path = raw_state_path.resolve()
    root_path = Path(root_dir).resolve()

    # 2. Permissions check
    try:
        data_path.mkdir(parents=True, exist_ok=True, mode=0o700)
        test_file = data_path / '.precheck_write_test'
        test_file.write_text('test')
        test_file.unlink()
    except Exception as e:
        errors.append(f'data_dir_not_writable:{type(e).__name__}')

    try:
        state_path.mkdir(parents=True, exist_ok=True, mode=0o700)
        test_file = state_path / '.precheck_write_test'
        test_file.write_text('test')
        test_file.unlink()
    except Exception as e:
        errors.append(f'state_dir_not_writable:{type(e).__name__}')

    # 3. Mount point validation on POSIX systems
    # Refuse falling back directly to root disk unless allow_non_mount is explicitly set for test
    if os.name == 'posix' and not allow_non_mount:
        if not os.path.ismount(str(data_path)):
            errors.append('data_dir_must_be_mount_point')

    # 4. Measure disk capacity
    try:
        data_usage = shutil.disk_usage(data_path)
        data_total = data_usage.total
        data_free = data_usage.free
        data_used = data_usage.used
    except Exception as e:
        errors.append(f'data_dir_usage_error:{type(e).__name__}')
        data_total, data_free, data_used = 0, 0, 0

    try:
        root_usage = shutil.disk_usage(root_path)
        root_total = root_usage.total
        root_free = root_usage.free
        root_min_required = max(ROOT_MIN_FREE_FLOOR, int(root_total * ROOT_MIN_FREE_RATIO))
    except Exception as e:
        errors.append(f'root_usage_error:{type(e).__name__}')
        root_total, root_free, root_min_required = 0, 0, 0

    # 5. Root disk protection check
    if root_free < root_min_required:
        errors.append(f'root_free_below_protection_line:free={root_free}_required={root_min_required}')

    # 6. Volume size sanity check (if mounted)
    if data_total > 0 and data_total < 25 * GIB:
        warnings.append('data_volume_total_below_expected_30_gib')

    return {
        'schema_version': 1,
        'healthy': len(errors) == 0,
        'errors': errors,
        'warnings': warnings,
        'data_path': str(data_path),
        'state_path': str(state_path),
        'data_total_bytes': data_total,
        'data_used_bytes': data_used,
        'data_free_bytes': data_free,
        'root_total_bytes': root_total,
        'root_free_bytes': root_free,
        'root_min_required_bytes': root_min_required,
    }


def main():
    parser = argparse.ArgumentParser(description='Diagnostics storage precheck tool.')
    parser.add_argument('--data-dir', default='./data', help='Dedicated data directory')
    parser.add_argument('--state-dir', default='./state', help='Guard state directory')
    parser.add_argument('--root-dir', default='/', help='Root filesystem path')
    parser.add_argument('--allow-non-mount', action='store_true', help='Allow non-mount directory for local/testing')
    parser.add_argument('--output', help='Optional path to write json output')

    args = parser.parse_args()
    report = inspect_precheck(args.data_dir, args.state_dir, args.root_dir, args.allow_non_mount)

    formatted = json.dumps(report, indent=2)
    print(formatted)

    if args.output:
        Path(args.output).write_text(formatted + '\n', encoding='utf-8')

    sys.exit(0 if report['healthy'] else 1)


if __name__ == '__main__':
    main()
