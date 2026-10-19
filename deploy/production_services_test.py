"""Execute production preflight and completion shell with isolated command mocks."""
import os
from pathlib import Path
import re
import shutil
import subprocess
import tempfile
import unittest


DEPLOY = Path(__file__).parent
BASH = shutil.which('bash') if os.name != 'nt' else str(Path(shutil.which('git')).parent.parent / 'bin/bash.exe')
SCRIPTS = ('production-server.sh', 'production-update.sh')
MOCKS = r'''
set -Eeuo pipefail
fail() { printf 'ERROR: %s\n' "$*" >&2; return 1; }
hostname() { printf 'vmi3574179\n'; }
uname() { printf 'x86_64\n'; }
curl() { printf 'curl %s\n' "$*" >> "$TEST_ROOT/events"; }
python3() { return 0; }
openssl() { return 0; }
sha256sum() { return 0; }
ss() { return 0; }
docker() {
  printf 'docker %s\n' "$*" >> "$TEST_ROOT/events"
  if [[ "$1 $2" == 'compose version' ]]; then return 0; fi
  if [[ "$1 $2" == 'container ls' ]]; then
    [[ "$INVENTORY_ERROR" != true ]] || return 7
    [[ "$*" == *'name=^/remnanode$'* ]] || return 8
    if [[ "$NODE" != absent ]]; then printf 'node-id\n'; fi
    return 0
  fi
  [[ "$1" == inspect ]] || return 9
  shift
  local names=() name running identity started
  while [[ "$1" != --format ]]; do names+=("$1"); shift; done
  for name in "${names[@]}"; do
    if [[ "$name" == xboard-production-internal-app-1 ]]; then
      printf 'xboard-production-internal healthy\n'; continue
    fi
    [[ "$name" != "$MISSING" && !( "$name" == remnanode && "$NODE" == absent ) ]] || return 1
    running=true; identity="$name-id"; started=original-start
    if [[ "$name" == remnanode && "$NODE" == stopped ]]; then running=false; fi
    if [[ "$name" == "$CHANGED" ]]; then
      if [[ "$CHANGE_FIELD" == id ]]; then identity=replaced-id; else started=restarted; fi
    fi
    if [[ "$2" == '{{.State.Running}}' ]]; then printf '%s\n' "$running"
    else printf '/%s %s %s %s\n' "$name" "$identity" "$started" "$running"; fi
  done
}
'''


def snapshot_function(script):
    return re.search(r'(?m)^snapshot\(\) \{(?:[^\n]*\}\n|[\s\S]*?^\}\n)', script).group()


class ProtectedServicesTests(unittest.TestCase):
    def run_shell(self, body, **values):
        with tempfile.TemporaryDirectory() as temporary:
            env = {**os.environ, 'TEST_ROOT': temporary.replace('\\', '/'),
                   'NODE': 'present', 'MISSING': '', 'CHANGED': '', 'CHANGE_FIELD': 'id',
                   'INVENTORY_ERROR': 'false', **values}
            result = subprocess.run([BASH, '-s'], input=MOCKS + body, text=True, capture_output=True, env=env)
            events = (Path(temporary) / 'events').read_text().splitlines()
            self.assertTrue(all(event.startswith(('docker inspect ', 'docker container ls ',
                                                  'docker compose version', 'curl ')) for event in events), events)
            return result, events

    def baseline(self, script):
        lines = script.splitlines()
        index = lines.index('baseline="$(snapshot)"')
        return snapshot_function(script) + '\n'.join(lines[index:index + 2]) + '\n'

    def test_required_services_and_optional_node_preflight(self):
        cases = [('present', '', 'false', True), ('absent', '', 'false', True),
                 ('stopped', '', 'false', False), ('absent', '', 'true', False)]
        cases += [('absent', name, 'false', False) for name in ('remnawave', 'remnawave-db', 'remnawave-redis', 'caddy')]
        for filename in SCRIPTS:
            script = (DEPLOY / filename).read_text()
            for node, missing, query_error, allowed in cases:
                with self.subTest(script=filename, node=node, missing=missing, query_error=query_error):
                    result, events = self.run_shell(self.baseline(script), NODE=node, MISSING=missing, INVENTORY_ERROR=query_error)
                    self.assertEqual(result.returncode == 0, allowed, result.stderr)
                    if allowed:
                        inspect = [event for event in events if event.startswith('docker inspect ')][-1]
                        self.assertEqual(' remnanode ' in inspect, node == 'present')

    def test_existing_install_inspect_uses_same_protection_rules(self):
        script = (DEPLOY / 'production-server.sh').read_text()
        prefix = script[:script.index('if [[ "$mode" == resume')]
        prefix = prefix.replace('directory=/opt/xboard-go', 'directory="$TEST_ROOT"')
        args = 'set -- inspect ' + 'a' * 40 + ' https://fast.hjy.ca:8443 admin@example.test ' + 'b' * 64 + ' ' + 'c' * 64 + '\n'
        for node, missing, allowed in [('present', '', True), ('absent', '', True),
                                       ('stopped', '', False), ('absent', 'remnawave-db', False)]:
            with self.subTest(node=node, missing=missing):
                result, _ = self.run_shell(snapshot_function(script) + args + prefix, NODE=node, MISSING=missing)
                self.assertEqual(result.returncode == 0, allowed, result.stderr)
                self.assertEqual('Read-only preflight passed' in result.stdout, allowed)

    def test_protected_identity_start_time_and_presence_changes_are_rejected(self):
        for filename in SCRIPTS:
            script = (DEPLOY / filename).read_text()
            compare = next(line for line in script.splitlines() if line.startswith('[[ "$(snapshot)" == "$baseline" ]]'))
            for node, change in [('present', ''), ('absent', ''),
                                 ('present', 'CHANGED=remnawave'), ('present', 'CHANGED=remnanode'),
                                 ('present', 'CHANGED=caddy; CHANGE_FIELD=start'),
                                 ('present', 'NODE=absent'), ('absent', 'NODE=present')]:
                with self.subTest(script=filename, node=node, change=change):
                    result, _ = self.run_shell(self.baseline(script) + change + '\n' + compare + '\n', NODE=node)
                    self.assertEqual(result.returncode == 0, not change, result.stderr)


class PreviousComposeTests(unittest.TestCase):
    def test_previous_compose_uses_exact_source_sha_and_rejects_local_drift(self):
        lines = (DEPLOY / 'production-update.sh').read_text().splitlines()
        downloads = [index for index, line in enumerate(lines) if 'curl ' in line and '$old_revision/' in line]
        self.assertEqual(len(downloads), 2)
        for index in downloads:
            for drift in (False, True):
                with self.subTest(line=index + 1, drift=drift), tempfile.TemporaryDirectory() as temporary:
                    root = Path(temporary)
                    (root / 'compose.yaml').write_bytes(b'changed\n' if drift else b'original\n')
                    shell = r'''
set -Eeuo pipefail
work="$TEST_ROOT"; directory="$TEST_ROOT"; old_revision=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
fail() { return 1; }
curl() {
  local expected="https://raw.githubusercontent.com/Hao-Monster/Xboard-Go/$old_revision/deploy/compose.yaml"
  [[ " $* " == *" $expected "* ]] || return 7
  while [[ "$1" != -o ]]; do shift; done
  printf 'original\n' > "$2"
}
'''
                    env = {**os.environ, 'TEST_ROOT': temporary.replace('\\', '/')}
                    result = subprocess.run([BASH, '-s'], input=shell + '\n'.join(lines[index:index + 2]), text=True, capture_output=True, env=env)
                    self.assertEqual(result.returncode == 0, not drift, result.stdout + result.stderr)
                    self.assertEqual((root / 'compose.yaml').read_text(), 'changed\n' if drift else 'original\n')


class SuccessfulUpdateRetentionTests(unittest.TestCase):
    def test_success_keeps_existing_recovery_points_and_completion_marker(self):
        script = (DEPLOY / 'production-update.sh').read_text()
        completion = script[script.rindex('trap - ERR\n'):]
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            preserved = {'.env.before-old': 'old-env', 'compose.before-old.yaml': 'old-compose',
                         'update.failure.log': 'failure', 'one.xbbackup': 'one',
                         'two.xbbackup': 'two', 'three.xbbackup': 'three'}
            for name, content in {**preserved, 'env.before': 'current-env', 'compose.previous': 'current-compose'}.items():
                (root / name).write_text(content)
            shell = r'''
set -Eeuo pipefail
work="$TEST_ROOT"; directory="$TEST_ROOT"; project=xboard-production-internal
revision=bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb; old_revision=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
backup=verified-backup; node_source=preserve; node_version=
docker() { printf 'docker %s\n' "$*" >> "$TEST_ROOT/cleanup"; }
find() { printf 'find %s\n' "$*" >> "$TEST_ROOT/cleanup"; }
rm() { printf 'rm %s\n' "$*" >> "$TEST_ROOT/cleanup"; }
'''
            env = {**os.environ, 'TEST_ROOT': temporary.replace('\\', '/')}
            result = subprocess.run([BASH, '-s'], input=shell + completion, text=True, capture_output=True, env=env)
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertFalse((root / 'cleanup').exists(), (root / 'cleanup').read_text() if (root / 'cleanup').exists() else '')
            for name, content in preserved.items():
                self.assertEqual((root / name).read_text(), content)
            self.assertEqual((root / ('.env.before-' + 'b' * 40)).read_text(), 'current-env')
            self.assertEqual((root / ('compose.before-' + 'b' * 40 + '.yaml')).read_text(), 'current-compose')
            self.assertIn('XBOARD_DEPLOYMENT_COMPLETE:' + 'b' * 40, result.stdout.splitlines())


if __name__ == '__main__':
    unittest.main()
