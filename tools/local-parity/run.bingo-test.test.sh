#!/usr/bin/env bash
set -Eeuo pipefail

echo 'FAKE RUNNER TESTS ONLY: no service lifecycle action is executed.'
repo="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"
test_root="$(mktemp -d /tmp/run-bingo-test.XXXXXX)"
cleanup_test_root() {
  [[ "$test_root" == /tmp/run-bingo-test.* && -d "$test_root" && ! -L "$test_root" ]] || return 1
  rm -rf --one-file-system -- "$test_root"
}
trap cleanup_test_root EXIT

fixture="$test_root/repo"
mkdir -p "$fixture/tools/local-parity"
cp "$repo/tools/local-parity/run.bingo-test.sh" "$fixture/tools/local-parity/"
cp "$repo/tools/local-parity/compose.bingo-test.yaml" "$fixture/tools/local-parity/"
chmod 755 "$fixture/tools/local-parity/run.bingo-test.sh"
git -C "$fixture" init -q
call_log="$fixture/calls.log"
cat >"$fixture/tools/local-parity/run.sh" <<'STUB'
#!/usr/bin/env bash
set -Eeuo pipefail
fields=(
  "$1"
  "$LOCAL_PARITY_RUN_ID"
  "$LOCAL_PARITY_EXPECTED_PROJECT"
  "$LOCAL_PARITY_COMPOSE_OVERLAY"
  "$LOCAL_PARITY_CANDIDATE_BASE_IMAGE"
  "$LOCAL_PARITY_CANDIDATE_BASE_IMAGE_ID"
  "$XBOARD_LEGACY_IMAGE"
  "$(command -v go)"
  "$(command -v node)"
)
(IFS='|'; printf '%s\n' "${fields[*]}") >>"$BINGO_FAKE_CALL_LOG"
STUB
chmod 755 "$fixture/tools/local-parity/run.sh"

invoke() {
  BINGO_PARITY_EXPECTED_ROOT="$fixture"   BINGO_FAKE_CALL_LOG="$call_log"     "$fixture/tools/local-parity/run.bingo-test.sh" "$1" bingo-repeatable
}
invoke prepare
invoke start
invoke verify
invoke stop

[[ "$(wc -l <"$call_log")" -eq 4 ]]
[[ "$(cut -d'|' -f1 "$call_log" | paste -sd, -)" == 'prepare,start,verify,cleanup' ]]
while IFS='|' read -r action run_id project overlay base base_id legacy go_path node_path; do
  [[ "$run_id" == bingo-repeatable ]]
  [[ "$project" == xboard-user-parity-bingo-repeatable ]]
  [[ "$overlay" == tools/local-parity/compose.bingo-test.yaml ]]
  [[ "$base" == xboard-go:gpt55-user-2e1713c69dfc-offline ]]
  [[ "$base_id" == sha256:0e19640a157c07392250caadd0321cceced1f8c214a1cdf5156536058bfddfb5 ]]
  [[ "$legacy" == xboard-legacy-parity:8065164 ]]
  [[ "$go_path" == /home/bingo/.local/toolchains/go1.26.8/bin/go ]]
  [[ "$node_path" == /home/bingo/apps/xboard-go-tools/node-v24.20.0-linux-x64/bin/node ]]
done <"$call_log"
echo 'PASS prepare/start/verify/stop use one run-derived project, reviewed overlay, image identities, and process PATH'

before="$(wc -l <"$call_log")"
set +e
BINGO_PARITY_EXPECTED_ROOT=/tmp BINGO_FAKE_CALL_LOG="$call_log"   "$fixture/tools/local-parity/run.bingo-test.sh" prepare bingo-repeatable >"$fixture/wrong-root.log" 2>&1
rc=$?
set -e
[[ "$rc" -eq 2 && "$(wc -l <"$call_log")" -eq "$before" ]]
echo 'PASS mismatched expected root is rejected before runner dispatch'

printf '%s\n' '# changed' >>"$fixture/tools/local-parity/compose.bingo-test.yaml"
set +e
BINGO_PARITY_EXPECTED_ROOT="$fixture" BINGO_FAKE_CALL_LOG="$call_log"   "$fixture/tools/local-parity/run.bingo-test.sh" prepare bingo-repeatable >"$fixture/changed-overlay.log" 2>&1
rc=$?
set -e
[[ "$rc" -eq 2 && "$(wc -l <"$call_log")" -eq "$before" ]]
echo 'PASS changed overlay is rejected before runner dispatch'

set +e
BINGO_PARITY_EXPECTED_ROOT="$fixture" BINGO_FAKE_CALL_LOG="$call_log"   "$fixture/tools/local-parity/run.bingo-test.sh" prepare wrong-id >"$fixture/invalid-id.log" 2>&1
rc=$?
set -e
[[ "$rc" -eq 2 && "$(wc -l <"$call_log")" -eq "$before" ]]
echo 'PASS non-bingo run ID is rejected before runner dispatch'

set +e
LOCAL_PARITY_RUN_ID=bingo-preflight LOCAL_PARITY_EXPECTED_PROJECT=wrong-project   bash "$repo/tools/local-parity/run.sh" verify >"$test_root/project-mismatch.log" 2>&1
rc=$?
set -e
[[ "$rc" -eq 2 ]]
grep -F 'LOCAL_PARITY_EXPECTED_PROJECT does not match' "$test_root/project-mismatch.log" >/dev/null
echo 'PASS stock runner rejects a project inconsistent with the run ID'

set +e
LOCAL_PARITY_RUN_ID=bingo-preflight LOCAL_PARITY_EXPECTED_PROJECT=xboard-user-parity-bingo-preflight LOCAL_PARITY_COMPOSE_OVERLAY=../outside.yaml   bash "$repo/tools/local-parity/run.sh" verify >"$test_root/unsafe-overlay.log" 2>&1
rc=$?
set -e
[[ "$rc" -eq 2 ]]
grep -F 'must name a YAML file directly under tools/local-parity' "$test_root/unsafe-overlay.log" >/dev/null
echo 'PASS stock runner rejects an overlay outside the reviewed directory'

set +e
LOCAL_PARITY_RUN_ID=bingo-preflight LOCAL_PARITY_EXPECTED_PROJECT=xboard-user-parity-bingo-preflight LOCAL_PARITY_COMPOSE_OVERLAY=tools/local-parity/compose.bingo-test.yaml   bash "$repo/tools/local-parity/run.sh" verify >"$test_root/accepted-overlay.log" 2>&1
rc=$?
set -e
[[ "$rc" -eq 2 ]]
grep -F 'missing .local/local-parity-bingo-preflight/runtime.env' "$test_root/accepted-overlay.log" >/dev/null
echo 'PASS stock runner accepts the reviewed overlay/project and stops at the expected missing-runtime boundary'

set +e
env -u LOCAL_PARITY_EXPECTED_PROJECT -u LOCAL_PARITY_COMPOSE_OVERLAY   LOCAL_PARITY_RUN_ID=bingo-preflight   bash "$repo/tools/local-parity/run.sh" verify >"$test_root/default-path.log" 2>&1
rc=$?
set -e
[[ "$rc" -eq 2 ]]
grep -F 'missing .local/local-parity-bingo-preflight/runtime.env' "$test_root/default-path.log" >/dev/null
echo 'PASS stock runner default path remains available without opt-in variables'

echo 'ALL FAKE RESTRICTED-RUNNER TESTS PASSED; no real service was started.'
