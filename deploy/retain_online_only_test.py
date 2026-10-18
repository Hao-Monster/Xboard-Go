import importlib.util
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch


def load_module():
    path = Path(__file__).with_name('retain-online-only.py')
    spec = importlib.util.spec_from_file_location('retain_online_only', path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


retain = load_module()


class RetainOnlineOnlyTests(unittest.TestCase):
    def test_apps_entry_regex_preserves_approved_and_matches_stale(self):
        preserved = [
            'xboard-go-freedom',
            'xboard-logs',
            'xboard-legacy-oracle',
            'freedompost-preflight-45dcc39',
            'freedompost-support',
        ]
        for name in preserved:
            with self.subTest(preserved=name):
                self.assertIsNone(retain.STALE_APPS_ENTRY_RE.search(name))

        stale = [
            'xboard-go-team-20260909',
            'xboard-go-tools',
            'xboard-go-workspaces',
            'xboard-go-caches',
            'xboard-go-worktrees',
            'xboard-go-test-results',
            'xboard-go-builds',
            'xboard-go-workspace',
            'xboard-go-releases',
            'xboard-go-test-tools',
            'xboardme-pre-online',
            'xboard-go-build-5b7b56c',
            'rootless-smoke-main-f56e2be.tar',
            'rootless-smoke-main-f56e2be.tar.sha256',
            'xboard-go-test-proxy',
            'xboard-go-test-cache',
            'xboard-node-workspace',
            'xboard-node-workspaces',
            'Xboard-go-team-20260909',
            'xboard-team-20260909',
            'xboard-go-staging-width',
            'xboard-go-test-runtime',
            'run-rootless-smoke.edit.sh',
        ]
        for name in stale:
            with self.subTest(stale=name):
                self.assertIsNotNone(retain.STALE_APPS_ENTRY_RE.search(name))

    def test_docker_resource_regexes_protect_active_services(self):
        active_containers = [
            'xboard-go-freedom-app-1',
            'xboard-log-collector-development-collector-1',
            'xboard-logs-auth-1',
            'xboard-logs-victoria-logs-1',
            'xboard-legacy-oracle',
            'wechatproduct-backend-1',
            'freedompost-preflight-postgres',
            'hysteria-agent',
        ]
        for name in active_containers:
            self.assertIsNone(retain.STALE_DEV_CONTAINER_RE.search(name))

        stale_containers = [
            'xboard-go-closeout-20260909-panel-1',
            'xboard-go-r006-acceptance-app-1',
            'xboard-go-d009-smoke-redis-1',
            'xboardme-preonline-web-1',
            'xboard-xdb-extract',
        ]
        for name in stale_containers:
            self.assertIsNotNone(retain.STALE_DEV_CONTAINER_RE.search(name))

        self.assertIsNotNone(retain.STALE_DEV_VOLUME_RE.search('a' * 64))
        self.assertIsNotNone(retain.STALE_DEV_VOLUME_RE.search('xboard-go-r006_db'))
        self.assertIsNone(retain.STALE_DEV_VOLUME_RE.search('xboard-go-freedom_app_data'))
        self.assertIsNone(retain.STALE_DEV_VOLUME_RE.search('xboard-go-freedom_app_backups'))

    def test_active_systemd_release_shas_extracts_exact_shas(self):
        sha = '9aadd08481c13ae7c63d427515f356bb760dcee2'
        with tempfile.TemporaryDirectory() as tmp:
            unit = Path(tmp) / 'xboard-logs-probe.service'
            unit.write_text(
                f'[Service]\nExecStart=/usr/bin/python3 /opt/xboard-observability/releases/{sha}/deploy/logging-probe.py --source production\n',
                encoding='utf-8',
            )
            self.assertEqual(retain.active_systemd_release_shas([unit]), {sha})

    def test_prune_sorted_files_keeps_newest(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            files = []
            for idx in range(4):
                p = root / f'.env.before-{idx}'
                p.write_text(str(idx), encoding='utf-8')
                files.append(p)
            cand, rem = retain.prune_sorted_files(files, 1, 'audit')
            self.assertEqual((cand, rem), (3, 0))
            self.assertEqual(len(list(root.glob('.env.before-*'))), 4)

            cand, rem = retain.prune_sorted_files(files, 1, 'cleanup')
            self.assertEqual((cand, rem), (3, 3))
            remaining = list(root.glob('.env.before-*'))
            self.assertEqual(len(remaining), 1)

    def test_select_superseded_internal_releases(self):
        s1 = '1' * 40
        s2 = '2' * 40
        s3 = '3' * 40
        s4 = '4' * 40
        s_notes = '5' * 40
        s_tag = '6' * 40
        releases = [
            {
                'id': 10,
                'tag_name': 'v1.0.0',
                'target_commitish': 'main',
                'body': f'Official stable image: ghcr.io/example/xboard-go:v1.0.0\nSource: {s_notes}\n',
                'created_at': '2026-09-01T00:00:00Z',
            },
            {
                'id': 11,
                'tag_name': 'v1.0.1',
                'target_commitish': 'main',
                'created_at': '2026-09-01T06:00:00Z',
            },
            {
                'id': 12,
                'tag_name': 'v2.0.0',
                'draft': True,
                'target_commitish': 'main',
                'body': '',
                'created_at': '2026-09-05T00:00:00Z',
            },
            {'id': 1, 'tag_name': f'internal-{s1}', 'created_at': '2026-09-01T00:00:00Z'},
            {'id': 5, 'tag_name': f'internal-{s_notes}', 'created_at': '2026-09-01T12:00:00Z'},
            {'id': 6, 'tag_name': f'internal-{s_tag}', 'created_at': '2026-09-01T18:00:00Z'},
            {'id': 2, 'tag_name': f'internal-{s2}', 'created_at': '2026-09-02T00:00:00Z'},
            {'id': 3, 'tag_name': f'internal-{s3}', 'created_at': '2026-09-03T00:00:00Z'},
            {'id': 4, 'tag_name': f'internal-{s4}', 'created_at': '2026-09-04T00:00:00Z'},
        ]
        superseded = retain.select_superseded_internal_releases(
            releases,
            {s1},
            keep_latest=2,
            resolve_tag_sha=lambda tag: {'v1.0.1': s_tag}.get(tag, ''),
        )
        self.assertEqual([r['tag_name'] for r in superseded], [f'internal-{s2}'])

    def test_prune_unreferenced_images_untags_aliases_and_removes_unused(self):
        ls_output = (
            'xboard-go:active\tsha256:' + 'a' * 64 + '\n'
            'xboard-go:alias-on-active\tsha256:' + 'a' * 64 + '\n'
            'xboard-go:old\tsha256:' + 'b' * 64 + '\n'
        )
        commands = []

        def fake_docker(args, *, docker_host=None, check=True):
            commands.append(args)
            if args[:2] == ['image', 'ls'] and '--format' in args:
                return subprocess.CompletedProcess(['docker', *args], 0, ls_output, '')
            if args[:2] == ['image', 'ls'] and '-q' in args:
                return subprocess.CompletedProcess(['docker', *args], 0, 'sha256:' + 'a' * 64 + '\n', '')
            return subprocess.CompletedProcess(['docker', *args], 0, '', '')

        with patch.object(retain, 'docker_cmd', side_effect=fake_docker):
            cand, rem = retain.prune_unreferenced_images(
                {'sha256:' + 'a' * 64},
                {'xboard-go:active'},
                'cleanup',
            )
        self.assertEqual((cand, rem), (2, 2))
        self.assertIn(['image', 'rm', 'xboard-go:alias-on-active'], commands)
        self.assertIn(['image', 'rm', '-f', 'xboard-go:old'], commands)
        self.assertNotIn(['image', 'rm', '-f', 'xboard-go:active'], commands)

    def test_protected_app_entries_for_host_paths_protects_bind_mounts_and_compose_dirs(self):
        container = {
            'Mounts': [
                {'Type': 'bind', 'Source': '/home/bingo/apps/xboard-node-live/data'},
                {'Type': 'bind', 'Source': '/var/run/docker.sock'},
            ],
            'HostConfig': {
                'Binds': ['/home/bingo/apps/xboard-team-active:/app:ro'],
            },
            'Config': {
                'Image': 'custom:latest',
                'Labels': {
                    'com.docker.compose.project.working_dir': '/home/bingo/apps/xboard-go-active-worker',
                    'com.docker.compose.project.config_files': '/home/bingo/apps/xboardme-live/compose.yaml',
                },
            },
        }
        host_paths = retain.container_host_paths(container)
        apps_root = Path('/home/bingo/apps')
        entries = [
            apps_root / 'xboard-node-live',
            apps_root / 'xboard-team-active',
            apps_root / 'xboard-go-active-worker',
            apps_root / 'xboardme-live',
            apps_root / 'xboard-node-workspace',
        ]
        protected = retain.protected_app_entries_for_host_paths(host_paths, apps_root, entries)
        self.assertEqual(
            protected,
            {
                'xboard-node-live',
                'xboard-team-active',
                'xboard-go-active-worker',
                'xboardme-live',
            },
        )

    def test_run_development_host_preserves_active_container_bind_mount_dir(self):
        containers = [
            {
                'Id': 'c1',
                'Name': '/xboard-go-freedom-app-1',
                'Image': 'sha256:' + '1' * 64,
                'Config': {
                    'Image': 'xboard-go:active',
                    'Labels': {
                        'com.docker.compose.project.working_dir': '/home/bingo/apps/xboard-go-freedom',
                        'com.docker.compose.project.config_files': '/home/bingo/apps/xboard-go-freedom/compose.yaml,/home/bingo/apps/xboard-go-freedom/compose.extra.yaml',
                    },
                },
                'State': {'Running': True, 'Health': {'Status': 'healthy'}},
                'Mounts': [],
                'NetworkSettings': {'Networks': {}},
            },
            {
                'Id': 'c2',
                'Name': '/xboard-log-collector-development-collector-1',
                'Image': 'sha256:' + '2' * 64,
                'Config': {'Image': 'xboard-log-collector:active'},
                'State': {'Running': True},
                'Mounts': [],
                'NetworkSettings': {'Networks': {}},
            },
            {
                'Id': 'c3',
                'Name': '/xboard-logs-auth-1',
                'Image': 'sha256:' + '3' * 64,
                'Config': {'Image': 'caddy:active'},
                'State': {'Running': True},
                'Mounts': [],
                'NetworkSettings': {'Networks': {}},
            },
            {
                'Id': 'c4',
                'Name': '/xboard-logs-victoria-logs-1',
                'Image': 'sha256:' + '4' * 64,
                'Config': {'Image': 'victoria-logs:active'},
                'State': {'Running': True},
                'Mounts': [],
                'NetworkSettings': {'Networks': {}},
            },
            {
                'Id': 'c5',
                'Name': '/xboard-node-live-1',
                'Image': 'sha256:' + '5' * 64,
                'Config': {'Image': 'xboard-node:live'},
                'State': {'Running': True},
                'Mounts': [
                    {
                        'Type': 'bind',
                        'Source': '/home/bingo/apps/xboard-node-live',
                        'Destination': '/etc/xboard-node',
                    }
                ],
                'NetworkSettings': {'Networks': {}},
            },
        ]
        removed_paths = []
        apps_root = Path('/home/bingo/apps')
        freedom_dir = apps_root / 'xboard-go-freedom'
        fake_entries = [
            freedom_dir,
            apps_root / 'xboard-node-live',
            apps_root / 'xboard-node-workspace',
        ]
        fake_freedom_entries = [
            freedom_dir / 'compose.yaml',
            freedom_dir / 'compose.extra.yaml',
            freedom_dir / 'stale-image.tar',
        ]

        def fake_is_dir(self_path):
            norm = retain._norm_posix(self_path)
            return norm in {retain._norm_posix(apps_root), retain._norm_posix(freedom_dir)}

        def fake_iterdir(self_path):
            norm = retain._norm_posix(self_path)
            if norm == retain._norm_posix(apps_root):
                return iter(fake_entries)
            if norm == retain._norm_posix(freedom_dir):
                return iter(fake_freedom_entries)
            return iter([])

        with (
            patch.object(retain.socket, 'gethostname', return_value='bingo'),
            patch.object(retain, 'disk_available_kib', return_value=1024),
            patch.object(retain, 'inspect_containers', return_value=containers),
            patch.object(retain, 'check_http_health', return_value=None),
            patch.object(
                retain,
                'docker_cmd',
                return_value=subprocess.CompletedProcess(['docker'], 0, '', ''),
            ),
            patch.object(retain, 'prune_unreferenced_images', return_value=(0, 0)),
            patch.object(retain.os, 'getuid', create=True, return_value=1000),
            patch.object(Path, 'is_socket', return_value=False),
            patch.object(Path, 'is_dir', fake_is_dir),
            patch.object(Path, 'is_symlink', return_value=False),
            patch.object(Path, 'iterdir', fake_iterdir),
            patch.object(Path, 'glob', return_value=iter([])),
            patch.object(
                retain,
                'remove_path_robust',
                side_effect=lambda target, parent_mount=None: removed_paths.append(target.name),
            ),
        ):
            retain.run_development_host('cleanup')

        self.assertEqual(removed_paths, ['xboard-node-workspace', 'stale-image.tar'])


if __name__ == '__main__':
    unittest.main()
