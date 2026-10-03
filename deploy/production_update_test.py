"""Isolated failure injection: no Docker daemon or network is contacted."""
import os
import json
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest

SCRIPT = Path(__file__).with_name('production-update.sh').read_text()
BASH = shutil.which('bash') if os.name != 'nt' else str(Path(shutil.which('git')).parent.parent / 'bin/bash.exe')


def heredoc(name):
    return SCRIPT.split("<<'" + name + "'\n", 1)[1].split('\n' + name, 1)[0]


class RecoveryFilesTests(unittest.TestCase):
    def promote(self, root, stage):
        code = heredoc('PYPROMOTE')
        # Directory fsync is Linux-only; execute real filesystem renames on both OSes.
        if os.name == 'nt':
            code = 'import os\nos.O_DIRECTORY=0\nos.open=lambda *args: 99\nos.fsync=lambda fd: None\nos.close=lambda fd: None\n' + code
        return subprocess.run([sys.executable, '-', str(root), stage], input=code, text=True, capture_output=True)

    def test_preserves_failed_database_sidecars_and_attachments(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary).resolve(); stage = root / 'recover-test'; stage.mkdir()
            for directory, value in [(root, 'new'), (stage, 'old')]:
                (directory / 'xboard.db').write_text(value)
                (directory / 'knowledge-attachments').mkdir()
                (directory / 'knowledge-attachments/file').write_text(value)
            (root / 'xboard.db-wal').write_text('pending-new-writes')
            (root / 'xboard.db-shm').write_text('new-shm')
            result = self.promote(root, stage.name)
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual((root / 'xboard.db').read_text(), 'old')
            self.assertEqual((root / 'knowledge-attachments/file').read_text(), 'old')
            failed = root / 'recover-test-failed'
            self.assertEqual((failed / 'xboard.db').read_text(), 'new')
            self.assertEqual((failed / 'xboard.db-wal').read_text(), 'pending-new-writes')
            self.assertEqual((failed / 'xboard.db-shm').read_text(), 'new-shm')
            self.assertEqual((failed / 'knowledge-attachments/file').read_text(), 'new')
            self.assertFalse((root / 'xboard.db-wal').exists())

    def test_incomplete_stage_does_not_touch_live_database(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary).resolve(); stage = root / 'recover-test'; stage.mkdir()
            (root / 'xboard.db').write_text('new'); (stage / 'xboard.db').write_text('old')
            self.assertNotEqual(self.promote(root, stage.name).returncode, 0)
            self.assertEqual((root / 'xboard.db').read_text(), 'new')
            self.assertFalse((root / 'recover-test-failed').exists())

    def test_existing_failure_evidence_is_never_overwritten(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary).resolve(); stage = root / 'recover-test'; stage.mkdir()
            (root / 'xboard.db').write_text('new')
            failed = root / 'recover-test-failed'; failed.mkdir(); (failed / 'xboard.db').write_text('evidence')
            self.assertNotEqual(self.promote(root, stage.name).returncode, 0)
            self.assertEqual((root / 'xboard.db').read_text(), 'new')
            self.assertEqual((failed / 'xboard.db').read_text(), 'evidence')


class ResourceBoundaryTests(unittest.TestCase):
    def test_shared_network_wrong_volume_or_public_port_are_rejected(self):
        for invalid in (None, "network", "volume", "public"):
            with self.subTest(invalid=invalid), tempfile.TemporaryDirectory() as temporary:
                root = Path(temporary).resolve(); (root / "xboard.db").write_text("db"); (root / "knowledge-attachments").mkdir()
                project = "xboard-production-internal"; network_name = project + "_default"
                app = {"Id": "app", "NetworkSettings": {"Networks": {network_name: {}}, "Ports": {"8080/tcp": [{"HostIp": "127.0.0.1"}]}}, "Config": {"Env": ["XBOARD_DATABASE_DSN=file:/var/lib/xboard/xboard.db", "XBOARD_ATTACHMENT_ROOT=/var/lib/xboard/knowledge-attachments"]}, "Mounts": [{"Destination": "/var/lib/xboard", "Type": "volume", "Name": project + "_data", "RW": True, "Source": str(root)}]}
                proxy = {"Id": "caddy", "NetworkSettings": {"Networks": {network_name: {"Aliases": ["caddy"]}, "unrelated": {}}}}
                network = {"Name": network_name, "Driver": "bridge", "Scope": "local", "Labels": {"com.docker.compose.project": project, "com.docker.compose.network": "default"}, "Containers": {"app": {}, "caddy": {}}}
                volume = {"Name": project + "_data", "Driver": "local", "Scope": "local", "Mountpoint": str(root), "Labels": {"com.docker.compose.project": project, "com.docker.compose.volume": "data"}}
                if invalid == "network": network["Containers"]["foreign-service"] = {}
                if invalid == "volume": volume["Labels"]["com.docker.compose.project"] = "foreign"
                if invalid == "public": app["NetworkSettings"]["Ports"]["8080/tcp"][0]["HostIp"] = "0.0.0.0"
                paths = [root / name for name in ("containers.json", "network.json", "volume.json")]
                for path, value in zip(paths, ([app, proxy], [network], [volume])): path.write_text(json.dumps(value))
                code = heredoc("PYBOUNDARY")
                if os.name == "nt": code = "import os,stat\nos.geteuid=lambda: 0\nstat.S_IWOTH=0\n" + code
                result = subprocess.run([sys.executable, "-", *(str(path) for path in paths), project, str(root / "data-root"), str(root / "aliases")], input=code, text=True, capture_output=True)
                self.assertEqual(result.returncode == 0, invalid is None, result.stderr)
                self.assertEqual((root / "xboard.db").read_text(), "db")


class DeploymentFailureTests(unittest.TestCase):
    def run_flow(self, failure=''):
        functions = SCRIPT[SCRIPT.index('reconnect_proxy() {'):SCRIPT.index('python3 - "$directory/.env"')]
        flow = SCRIPT[SCRIPT.index('interrupted() {'):SCRIPT.index('curl -fsS --retry 6')]
        mocks = r'''
set -Eeuo pipefail
work="$TEST_ROOT"; directory="$work"; data_root="$work/data"; recovery=recover-test
mkdir -p "$data_root"
network=dedicated; revision=bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb; old_revision=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
container=app; backup=/backup/safe; compose=(fake_compose); old_compose=(fake_old)
isolated=0; database_changed=0; public_open=0
printf 'caddy\n' > "$work/proxy-aliases"
log() { printf '%s\n' "$*" >> "$work/events"; }
fail() { return 1; }
install() { log "install $*"; }
stat() { printf '65532\n'; }
python3() { if [[ "$1" == - ]]; then cat > /dev/null; fi; log promote-or-health; }
curl() { log "curl $*"; [[ "$FAILURE" != health ]]; }
docker() { log "docker $* published=$public_open"; if [[ "$1" == inspect ]]; then printf '%s\n' "$revision"; fi; }
fake_compose() {
  log "compose $*"
  if [[ "$1" == up && "$FAILURE" == start && ! -f "$work/failed-once" ]]; then touch "$work/failed-once"; return 1; fi
}
fake_old() {
  log "old $*"
  [[ ! ( "$*" == *'backup create'* && "$FAILURE" == backup ) && ! ( "$*" == *'backup verify'* && "$FAILURE" == verify ) && ! ( "$*" == *'backup restore'* && "$FAILURE" == restore ) ]]
}
'''
        if failure == 'restore':
            mocks = mocks.replace('"$FAILURE" == start', '"$FAILURE" == restore')
        if failure == 'published':
            flow += '\nfalse\n'
        if failure == 'signal':
            flow = flow[:flow.index('database_changed=1')] + '\nkill -TERM $$\n'
        with tempfile.TemporaryDirectory() as temporary:
            env = {**os.environ, 'TEST_ROOT': temporary.replace('\\', '/'), 'FAILURE': failure}
            result = subprocess.run([BASH, '-s'], input=mocks + functions + flow, text=True, capture_output=True, env=env)
            events = (Path(temporary) / 'events').read_text().splitlines()
            return result, events

    def test_success_stops_then_backs_up_and_opens_only_after_local_health(self):
        result, events = self.run_flow()
        self.assertEqual(result.returncode, 0, result.stderr)
        def index(value): return next(i for i, line in enumerate(events) if value in line)
        self.assertLess(index('network disconnect'), index('compose stop'))
        self.assertLess(index('compose stop'), index('backup create'))
        self.assertLess(index('backup create'), index('backup verify'))
        self.assertLess(index('backup verify'), index('compose up'))
        self.assertLess(index('http://127.0.0.1'), index('network connect'))
        self.assertIn('published=1', events[index('network connect')])
        self.assertFalse(any('backup restore' in line for line in events))

    def test_pre_migration_backup_failure_restarts_old_without_restoring_data(self):
        for failure in ('backup', 'verify'):
            with self.subTest(failure=failure):
                result, events = self.run_flow(failure)
                self.assertNotEqual(result.returncode, 0)
                self.assertFalse(any('backup restore' in line for line in events))
                self.assertTrue(any('network connect' in line for line in events))
                self.assertNotIn('failed data retained under', result.stderr)

    def test_new_start_or_local_health_failure_restores_before_reopening(self):
        for failure in ('start', 'health'):
            with self.subTest(failure=failure):
                result, events = self.run_flow(failure)
                self.assertNotEqual(result.returncode, 0)
                restore = next(i for i, line in enumerate(events) if 'backup restore' in line)
                reconnect = next(i for i, line in enumerate(events) if 'network connect' in line)
                self.assertLess(restore, reconnect)
                self.assertIn('--entrypoint /xboard app backup restore', events[restore])
                self.assertIn('--attachment-output /var/lib/xboard/recover-test/knowledge-attachments', events[restore])
                self.assertTrue(any('compose stop' in line for line in events[:restore]))

    def test_restore_failure_keeps_proxy_isolated(self):
        result, events = self.run_flow('restore')
        self.assertNotEqual(result.returncode, 0)
        self.assertTrue(any('backup restore' in line for line in events))
        self.assertFalse(any('network connect' in line for line in events))
        self.assertIn('manual recovery required', result.stderr)

    def test_post_publication_failure_never_restores_or_downgrades(self):
        result, events = self.run_flow('published')
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse(any('backup restore' in line for line in events))
        self.assertEqual(sum('compose up' in line for line in events), 1)
        self.assertIn('new image and data retained', result.stderr)

    def test_signal_does_not_assume_rollback_is_safe(self):
        result, events = self.run_flow('signal')
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse(any('backup restore' in line or 'network connect' in line for line in events))
        self.assertIn('no automatic data restoration', result.stderr)


if __name__ == '__main__':
    unittest.main()
