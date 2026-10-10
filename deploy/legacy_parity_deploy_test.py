"""Exercise the actual workflow's remote shell with local Docker/HTTP boundaries.

No Docker daemon, SSH connection or HTTP request is used. Real shell file updates
are confined to TemporaryDirectory, and the production cd is intercepted.
"""

import os
from pathlib import Path
import re
import shutil
import subprocess
import tempfile
import textwrap
import unittest


ROOT = Path(__file__).resolve().parent.parent
WORKFLOW = ROOT / ".github/workflows/legacy-parity.yml"


def bash_binary():
    if os.environ.get("XBOARD_TEST_BASH"):
        return os.environ["XBOARD_TEST_BASH"]
    if os.name == "nt":
        git = shutil.which("git")
        if git:
            candidate = Path(git).parent.parent / "bin/bash.exe"
            if candidate.is_file():
                return str(candidate)
        raise unittest.SkipTest("Git Bash required on Windows; set XBOARD_TEST_BASH")
    result = shutil.which("bash")
    if not result:
        raise unittest.SkipTest("Bash is unavailable")
    return result


def remote_script():
    source = WORKFLOW.read_text(encoding="utf-8")
    match = re.search(r"<<'REMOTE'\n(.*?)^          REMOTE$", source, re.M | re.S)
    if not match:
        raise AssertionError("Expected the deployment's remote heredoc")
    return textwrap.dedent(match.group(1))


STUBS = r'''
cd() { builtin cd "$TEST_APP_DIR"; }
flock() { :; }
sleep() { :; }
sed() { [[ "$SCENARIO" != configuration_failure ]] || return 1; command sed "$@"; }
rm() { [[ "$SCENARIO" != marker_removal_failure ]] || return 1; command rm "$@"; }
docker() {
  printf '%s\n' "$*" >> "$TEST_EVENTS"
  if [[ "$*" == *"backup create"* && "$SCENARIO" == backup_failure ]]; then return 1; fi
  if [[ "$*" == *"backup restore"* ]]; then printf 'old snapshot\n' > active.db; fi
  if [[ "$*" == "compose up "* ]]; then
    printf 'accepted new write\n' >> active.db
    touch candidate-started
    [[ "$SCENARIO" != interrupt ]] || kill -TERM "$$"
    [[ "$SCENARIO" != up_failure ]]
    return
  fi
  if [[ "$*" == "compose stop app" ]]; then
    [[ "$SCENARIO" != stop_failure ]]
    return
  fi
  if [[ "$*" == *".State.Health.Status"* ]]; then
    if [[ "$SCENARIO" == unhealthy || "$SCENARIO" == stop_failure ]]; then printf 'unhealthy\n'; elif [[ "$SCENARIO" == health_timeout ]]; then printf 'starting\n'; else printf 'healthy\n'; fi
  elif [[ "$*" == *".Config.Image"* ]]; then
    if [[ -f candidate-started ]]; then printf 'xboard-go:%s\n' "$REVISION"; else printf 'xboard-go:previous\n'; fi
  fi
  return 0
}
curl() {
  printf 'curl %s\n' "$*" >> "$TEST_EVENTS"
  if [[ "$SCENARIO" == public_failure && "$*" == *"https://freedom.openal.uk"* ]]; then return 22; fi
  if [[ "$SCENARIO" == loopback_failure && "$*" == *"http://127.0.0.1"* ]]; then return 22; fi
  return 0
}
'''


class LegacyParityDeploymentTest(unittest.TestCase):
    def run_deploy(self, scenario, existing_marker=False):
        directory = tempfile.TemporaryDirectory(prefix="xboard-deploy-test-")
        self.addCleanup(directory.cleanup)
        root = Path(directory.name)
        (root / ".env").write_text("XBOARD_GO_REVISION=previous\n", encoding="utf-8")
        (root / "compose.yaml").write_text("services:\n  app:\n    environment:\n      XBOARD_DATABASE_DSN: file:/var/lib/xboard/active.db\n      XBOARD_NODE_RELEASE_ROOT: /old\n", encoding="utf-8")
        (root / "active.db").write_text("existing data\n", encoding="utf-8")
        if existing_marker:
            (root / ".deployment-failed").write_text("operator recovery required\n", encoding="utf-8")
        env = os.environ.copy()
        env.update(TEST_APP_DIR=root.as_posix(), TEST_EVENTS=(root / "events").as_posix(), SCENARIO=scenario, REVISION="b" * 40)
        env.pop("BASH_ENV", None)
        result = subprocess.run([bash_binary(), "--noprofile", "--norc"], input=STUBS + remote_script(), text=True, capture_output=True, env=env, timeout=20)
        events = (root / "events").read_text(encoding="utf-8") if (root / "events").exists() else ""
        return root, result, events

    def assert_preserved_failure(self, scenario):
        root, result, events = self.run_deploy(scenario)
        self.assertNotEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertTrue((root / ".deployment-failed").is_file(), result.stdout + result.stderr)
        self.assertIn("compose stop app", events)
        self.assertNotIn("backup restore", events)
        self.assertIn("accepted new write", (root / "active.db").read_text(encoding="utf-8"))
        self.assertIn("XBOARD_GO_REVISION=" + "b" * 40, (root / ".env").read_text(encoding="utf-8"))
        self.assertIn("file:/var/lib/xboard/active.db", (root / "compose.yaml").read_text(encoding="utf-8"))
        return result

    def test_health_failure_preserves_new_writes(self):
        self.assert_preserved_failure("unhealthy")

    def test_partial_activation_failure_preserves_new_writes(self):
        self.assert_preserved_failure("up_failure")

    def test_health_timeout_preserves_new_writes(self):
        self.assert_preserved_failure("health_timeout")

    def test_loopback_failure_preserves_new_writes(self):
        self.assert_preserved_failure("loopback_failure")

    def test_public_check_failure_is_inside_failure_boundary(self):
        self.assert_preserved_failure("public_failure")

    def test_stop_failure_is_reported_without_restoring(self):
        result = self.assert_preserved_failure("stop_failure")
        self.assertIn("stop", result.stderr.lower())

    def test_interruption_keeps_recovery_marker(self):
        self.assert_preserved_failure("interrupt")

    def test_marker_removal_failure_does_not_report_success(self):
        self.assert_preserved_failure("marker_removal_failure")

    def test_backup_failure_leaves_current_service_running(self):
        root, result, events = self.run_deploy("backup_failure")
        self.assertNotEqual(result.returncode, 0)
        self.assertNotIn("compose stop", events)
        self.assertNotIn("compose up", events)
        self.assertFalse((root / ".deployment-failed").exists())
        self.assertEqual((root / "active.db").read_text(encoding="utf-8"), "existing data\n")

    def test_pre_activation_configuration_failure_does_not_stop_current_service(self):
        root, result, events = self.run_deploy("configuration_failure")
        self.assertNotEqual(result.returncode, 0)
        self.assertNotIn("compose stop", events)
        self.assertNotIn("compose up", events)
        self.assertTrue((root / ".deployment-failed").exists())
        self.assertEqual((root / "active.db").read_text(encoding="utf-8"), "existing data\n")

    def test_failure_marker_blocks_automatic_retry(self):
        root, result, events = self.run_deploy("healthy", existing_marker=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(events, "")
        self.assertEqual((root / "active.db").read_text(encoding="utf-8"), "existing data\n")

    def test_success_checks_public_endpoint_and_clears_marker(self):
        root, result, events = self.run_deploy("healthy")
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertFalse((root / ".deployment-failed").exists())
        self.assertIn("https://freedom.openal.uk/healthz", events)
        self.assertNotIn("compose stop app", events)
        self.assertNotIn("backup restore", events)


if __name__ == "__main__":
    unittest.main()
