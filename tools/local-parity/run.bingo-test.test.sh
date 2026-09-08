#!/usr/bin/env bash
set -Eeuo pipefail

echo 'FAKE RUNNER TESTS ONLY: no real service lifecycle action is executed.'
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
stub_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"
cd "$stub_root"
run_dir=".local/local-parity-$LOCAL_PARITY_RUN_ID"
evidence_dir="output/local-parity-$LOCAL_PARITY_RUN_ID"
case "$1" in
  prepare) mkdir -p "$run_dir" "$evidence_dir" ;;
  cleanup) rm -rf --one-file-system -- "$run_dir" ;;
esac
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
  "$GOTOOLCHAIN"
  "$GOPROXY"
  "$GOMODCACHE"
  "$COREPACK_HOME"
)
(IFS='|'; printf '%s\n' "${fields[*]}") >>"$BINGO_FAKE_CALL_LOG"
STUB
chmod 755 "$fixture/tools/local-parity/run.sh"

invoke() {
  env BINGO_PARITY_EXPECTED_ROOT="$fixture" BINGO_FAKE_CALL_LOG="$call_log"     "$fixture/tools/local-parity/run.bingo-test.sh" "$1" bingo-repeatable
}

invoke prepare
run_dir="$fixture/.local/local-parity-bingo-repeatable"
evidence_dir="$fixture/output/local-parity-bingo-repeatable"
[[ -f "$run_dir/bingo-run-identity.txt" && ! -L "$run_dir/bingo-run-identity.txt" ]]
[[ -f "$run_dir/compose.bingo-test.prepared.yaml" && ! -L "$run_dir/compose.bingo-test.prepared.yaml" ]]
cmp -s "$run_dir/bingo-run-identity.txt" "$evidence_dir/bingo-run-identity.txt"
[[ "$(sha256sum "$run_dir/compose.bingo-test.prepared.yaml" | awk '{print $1}')" == 98c20be611d5989617e0a2ece97bceeceaae3e8635ee611342eb931569e25403 ]]
echo 'PASS prepare retains a strict non-secret run identity and reviewed overlay snapshot'

invoke start
invoke verify
before="$(wc -l <"$call_log")"
printf '%s\n' '# drift after prepare' >>"$fixture/tools/local-parity/compose.bingo-test.yaml"
set +e
invoke start >"$fixture/drifted-start.log" 2>&1
rc=$?
set -e
[[ "$rc" -eq 2 && "$(wc -l <"$call_log")" -eq "$before" ]]
grep -F 'source overlay SHA-256 mismatch' "$fixture/drifted-start.log" >/dev/null
echo 'PASS start rejects source-overlay drift after prepare'

invoke stop
[[ ! -e "$run_dir" && ! -L "$run_dir" ]]
[[ -f "$evidence_dir/bingo-run-identity.txt" ]]
echo 'PASS stop uses prepared identity/snapshot and cleans up despite source-overlay drift'

[[ "$(wc -l <"$call_log")" -eq 4 ]]
[[ "$(cut -d'|' -f1 "$call_log" | paste -sd, -)" == 'prepare,start,verify,cleanup' ]]
line_number=0
while IFS='|' read -r action run_id project overlay base base_id legacy go_path node_path gotoolchain goproxy gomodcache corepack_home; do
  line_number=$((line_number + 1))
  [[ "$run_id" == bingo-repeatable ]]
  [[ "$project" == xboard-user-parity-bingo-repeatable ]]
  if [[ "$line_number" -eq 1 ]]; then
    [[ "$overlay" == tools/local-parity/compose.bingo-test.yaml ]]
  else
    [[ "$overlay" == .local/local-parity-bingo-repeatable/compose.bingo-test.prepared.yaml ]]
  fi
  [[ "$base" == xboard-go:gpt55-user-2e1713c69dfc-offline ]]
  [[ "$base_id" == sha256:0e19640a157c07392250caadd0321cceced1f8c214a1cdf5156536058bfddfb5 ]]
  [[ "$legacy" == xboard-legacy-parity:8065164 ]]
  [[ "$go_path" == /home/bingo/.local/toolchains/go1.26.8/bin/go ]]
  [[ "$node_path" == /home/bingo/apps/xboard-go-tools/node-v24.20.0-linux-x64/bin/node ]]
  [[ "$gotoolchain" == local ]]
  [[ "$goproxy" == off ]]
  [[ "$gomodcache" == /home/bingo/.cache/xboard-go/gopath/pkg/mod ]]
  [[ "$corepack_home" == /home/bingo/.cache/node/corepack ]]
done <"$call_log"
echo 'PASS all actions keep one project, prepared overlay identity, trusted images, and pinned process toolchain/cache environment'

before="$(wc -l <"$call_log")"
set +e
env BINGO_PARITY_EXPECTED_ROOT=/tmp BINGO_FAKE_CALL_LOG="$call_log"   "$fixture/tools/local-parity/run.bingo-test.sh" prepare bingo-other >"$fixture/wrong-root.log" 2>&1
rc=$?
set -e
[[ "$rc" -eq 2 && "$(wc -l <"$call_log")" -eq "$before" ]]
echo 'PASS mismatched expected root is rejected before runner dispatch'

set +e
env BINGO_PARITY_EXPECTED_ROOT="$fixture" BINGO_FAKE_CALL_LOG="$call_log"   "$fixture/tools/local-parity/run.bingo-test.sh" prepare wrong-id >"$fixture/invalid-id.log" 2>&1
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
grep -F 'must be a reviewed source overlay or this run ID prepared snapshot' "$test_root/unsafe-overlay.log" >/dev/null
echo 'PASS stock runner rejects an overlay outside reviewed source/snapshot paths'

set +e
LOCAL_PARITY_RUN_ID=bingo-preflight LOCAL_PARITY_EXPECTED_PROJECT=xboard-user-parity-bingo-preflight LOCAL_PARITY_COMPOSE_OVERLAY=tools/local-parity/compose.bingo-test.yaml   bash "$repo/tools/local-parity/run.sh" verify >"$test_root/accepted-overlay.log" 2>&1
rc=$?
set -e
[[ "$rc" -eq 2 ]]
grep -F 'missing .local/local-parity-bingo-preflight/runtime.env' "$test_root/accepted-overlay.log" >/dev/null
echo 'PASS stock runner accepts the reviewed source overlay and expected project'

set +e
env -u LOCAL_PARITY_EXPECTED_PROJECT -u LOCAL_PARITY_COMPOSE_OVERLAY   LOCAL_PARITY_RUN_ID=bingo-preflight   bash "$repo/tools/local-parity/run.sh" verify >"$test_root/default-path.log" 2>&1
rc=$?
set -e
[[ "$rc" -eq 2 ]]
grep -F 'missing .local/local-parity-bingo-preflight/runtime.env' "$test_root/default-path.log" >/dev/null
echo 'PASS stock runner default path remains available without opt-in variables'

stock_fixture="$test_root/stock-repo"
mkdir -p "$stock_fixture/tools/local-parity" "$stock_fixture/.local/local-parity-bingo-runtime-drift"
cp "$repo/tools/local-parity/run.sh" "$stock_fixture/tools/local-parity/run.sh"
cat >"$stock_fixture/.local/local-parity-bingo-runtime-drift/runtime.env" <<'RUNTIME'
export COMPOSE_PROJECT_NAME='user-supplied-wrong-project'
export XBOARD_GO_IMAGE='xboard-go:user-parity-bingo-runtime-drift'
RUNTIME
set +e
LOCAL_PARITY_RUN_ID=bingo-runtime-drift   bash "$stock_fixture/tools/local-parity/run.sh" verify >"$test_root/runtime-drift.log" 2>&1
rc=$?
set -e
[[ "$rc" -eq 2 ]]
grep -F 'runtime project or candidate image does not match' "$test_root/runtime-drift.log" >/dev/null
echo 'PASS stock runner rejects a runtime file that tries to replace the run-derived project'

echo 'ALL FAKE RESTRICTED-RUNNER TESTS PASSED; no real service was started.'
