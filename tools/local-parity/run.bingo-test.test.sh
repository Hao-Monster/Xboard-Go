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
  cleanup-safe) rm -rf --one-file-system -- "$run_dir" ;;
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
[[ "$(sha256sum "$run_dir/compose.bingo-test.prepared.yaml" | awk '{print $1}')" == c58e881b725f4bb1db8756681b446e7428a77a59300cf476e5e59f9365c05f69 ]]
grep -Fx 'captcha_image=xboard-node-parity:24.18.0-ae91dcc111a6' "$run_dir/bingo-run-identity.txt" >/dev/null
grep -Fx 'captcha_image_source_index_digest=sha256:ae91dcc111a68c9d2d81ff2a17bda61be126426176fde6fe7d08ab13b7f50573' "$run_dir/bingo-run-identity.txt" >/dev/null
grep -Fx 'captcha_image_platform_manifest_digest=sha256:5301bbf5e8046148348b1dea15436326f43c579031f8d76654a631225bdfe467' "$run_dir/bingo-run-identity.txt" >/dev/null
grep -Fx 'captcha_image_id=sha256:77343b9c9fae9d567dbbb308eed6ac08564ef29663b6f09e7183b0a7c94f0c73' "$run_dir/bingo-run-identity.txt" >/dev/null
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
[[ "$(cut -d'|' -f1 "$call_log" | paste -sd, -)" == 'prepare,start,verify,cleanup-safe' ]]
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
grep -E 'unsafe (local runtime root|run directory)' "$test_root/accepted-overlay.log" >/dev/null
echo 'PASS stock runner accepts the reviewed source overlay and reaches the runtime storage boundary'

set +e
env -u LOCAL_PARITY_EXPECTED_PROJECT -u LOCAL_PARITY_COMPOSE_OVERLAY   LOCAL_PARITY_RUN_ID=bingo-preflight   bash "$repo/tools/local-parity/run.sh" verify >"$test_root/default-path.log" 2>&1
rc=$?
set -e
[[ "$rc" -eq 2 ]]
grep -E 'unsafe (local runtime root|run directory)' "$test_root/default-path.log" >/dev/null
echo 'PASS stock runner default overlay path remains available and reaches the runtime storage boundary'

make_stock_fixture() {
  local name="$1"
  local stock="$test_root/$name"
  mkdir -p "$stock/tools/local-parity" "$stock/fake-bin"
  cp "$repo/tools/local-parity/run.sh" "$stock/tools/local-parity/run.sh"
  cat >"$stock/fake-bin/docker" <<'DOCKER'
#!/usr/bin/env bash
printf '%q ' "$@" >>"$FAKE_DOCKER_LOG"
printf '\n' >>"$FAKE_DOCKER_LOG"
case "${1:-}" in
  compose) printf '%s\n' '{}' ;;
  image) printf '%s\n' 'id=fake' ;;
esac
exit 0
DOCKER
  cat >"$stock/fake-bin/curl" <<'CURL'
#!/usr/bin/env bash
printf '%s' '200'
exit 0
CURL
  chmod 755 "$stock/fake-bin/docker" "$stock/fake-bin/curl"
  printf '%s\n' "$stock"
}

write_valid_runtime() {
  local stock="$1" id="$2"
  local stock_run="$stock/.local/local-parity-$id"
  mkdir -p "$stock_run/image-context" "$stock_run/legacy-data" "$stock/output/local-parity-$id"
  cat >"$stock_run/runtime.env" <<RUNTIME
export COMPOSE_PROJECT_NAME='xboard-user-parity-$id'
export XBOARD_GO_IMAGE='xboard-go:user-parity-$id'
export XBOARD_GO_REVISION='b4b646715b64acffd0a126c481bf2181d4957f12'
export XBOARD_GO_PORT='18780'
export XBOARD_LEGACY_PORT='18781'
export XBOARD_MAILPIT_PORT='18782'
export XBOARD_GO_BOOTSTRAP_ADMIN_EMAIL='admin@legacy-parity.test'
export XBOARD_GO_BOOTSTRAP_ADMIN_PASSWORD_FILE='$stock_run/bootstrap-password.txt'
export XBOARD_GO_SETTINGS_ENCRYPTION_KEY_FILE='$stock_run/settings-encryption-key.txt'
export XBOARD_E2E_ADMIN_PATH='e2e-admin-secure'
export XBOARD_LEGACY_ADMIN_PATH='e2e-admin-secure'
export XBOARD_CAPTCHA_ALLOW_INSECURE='true'
export XBOARD_CAPTCHA_RECAPTCHA_VERIFY_URL='http://captcha-stub:4199/recaptcha'
export XBOARD_CAPTCHA_RECAPTCHA_V3_VERIFY_URL='http://captcha-stub:4199/recaptcha-v3'
export XBOARD_CAPTCHA_TURNSTILE_VERIFY_URL='http://captcha-stub:4199/turnstile'
export XBOARD_LEGACY_IMAGE='xboard-legacy-parity:8065164'
RUNTIME
  printf '%s' synthetic >"$stock_run/bootstrap-password.txt"
  printf '%s' synthetic >"$stock_run/settings-encryption-key.txt"
  printf '%s' base64:synthetic >"$stock_run/local-legacy-app-key.txt"
  printf '%s' admin@legacy-parity.test >"$stock_run/legacy-admin-email.txt"
  printf '%s' synthetic >"$stock_run/legacy-admin-password.txt"
  printf '%s' e2e-admin-secure >"$stock_run/legacy-admin-path.txt"
}

stock="$(make_stock_fixture normal-runtime)"
write_valid_runtime "$stock" normal
FAKE_DOCKER_LOG="$stock/docker.log" PATH="$stock/fake-bin:/usr/bin:/bin" LOCAL_PARITY_RUN_ID=normal \
  bash "$stock/tools/local-parity/run.sh" verify >"$stock/normal.log" 2>&1
[[ -s "$stock/docker.log" ]]
echo 'PASS normal canonical runtime data reaches the fake Docker verification boundary'

stock="$(make_stock_fixture runtime-project-drift)"
write_valid_runtime "$stock" projectdrift
sed -i "s/export COMPOSE_PROJECT_NAME='xboard-user-parity-projectdrift'/export COMPOSE_PROJECT_NAME='user-supplied-wrong-project'/" \
  "$stock/.local/local-parity-projectdrift/runtime.env"
set +e
FAKE_DOCKER_LOG="$stock/docker.log" PATH="$stock/fake-bin:/usr/bin:/bin" LOCAL_PARITY_RUN_ID=projectdrift \
  bash "$stock/tools/local-parity/run.sh" verify >"$stock/runtime-project-drift.log" 2>&1
rc=$?
set -e
[[ "$rc" -eq 2 && ! -e "$stock/docker.log" ]]
grep -F 'runtime project or candidate image does not match' "$stock/runtime-project-drift.log" >/dev/null
echo 'PASS stock runner rejects a complete runtime file that tries to replace the run-derived project'

stock="$(make_stock_fixture runtime-command)"
write_valid_runtime "$stock" runtimecommand
marker="$stock/runtime-command-marker"
printf '%s\n' ': >"$RUNTIME_COMMAND_MARKER"' >>"$stock/.local/local-parity-runtimecommand/runtime.env"
set +e
RUNTIME_COMMAND_MARKER="$marker" FAKE_DOCKER_LOG="$stock/docker.log" PATH="$stock/fake-bin:/usr/bin:/bin" \
LOCAL_PARITY_RUN_ID=runtimecommand \
  bash "$stock/tools/local-parity/run.sh" verify >"$stock/runtime-command.log" 2>&1
rc=$?
set -e
[[ "$rc" -eq 2 && ! -e "$marker" && ! -e "$stock/docker.log" ]]
grep -F 'invalid runtime line 17' "$stock/runtime-command.log" >/dev/null
echo 'PASS strict runtime parser rejects shell commands in a regular runtime.env without executing them'

stock="$(make_stock_fixture runtime-symlink)"
write_valid_runtime "$stock" runtimesymlink
stock_run="$stock/.local/local-parity-runtimesymlink"
marker="$stock/runtime-marker"
payload="$stock/runtime-payload.env"
cp "$stock_run/runtime.env" "$payload"
printf '%s\n' ': >"$RUNTIME_MARKER"' >>"$payload"
rm "$stock_run/runtime.env"
ln -s "$payload" "$stock_run/runtime.env"
set +e
RUNTIME_MARKER="$marker" FAKE_DOCKER_LOG="$stock/docker.log" PATH="$stock/fake-bin:/usr/bin:/bin" LOCAL_PARITY_RUN_ID=runtimesymlink \
  bash "$stock/tools/local-parity/run.sh" verify >"$stock/runtime-symlink.log" 2>&1
rc=$?
set -e
[[ "$rc" -eq 2 && ! -e "$marker" && ! -e "$stock/docker.log" ]]
grep -F 'unsafe run file' "$stock/runtime-symlink.log" >/dev/null
echo 'PASS runtime.env symlink is rejected before its shell marker can execute'

stock="$(make_stock_fixture fixed-file-symlinks)"
write_valid_runtime "$stock" filesymlink
stock_run="$stock/.local/local-parity-filesymlink"
outside="$stock/outside-marker"
printf '%s' synthetic-outside >"$outside"
for name in bootstrap-password.txt settings-encryption-key.txt local-legacy-app-key.txt legacy-admin-email.txt legacy-admin-password.txt legacy-admin-path.txt; do
  original="$(<"$stock_run/$name")"
  rm "$stock_run/$name"
  ln -s "$outside" "$stock_run/$name"
  rm -f "$stock/docker.log"
  set +e
  FAKE_DOCKER_LOG="$stock/docker.log" PATH="$stock/fake-bin:/usr/bin:/bin" LOCAL_PARITY_RUN_ID=filesymlink \
    bash "$stock/tools/local-parity/run.sh" verify >"$stock/$name.log" 2>&1
  rc=$?
  set -e
  [[ "$rc" -eq 2 && ! -e "$stock/docker.log" ]]
  grep -F "unsafe run file; expected canonical regular non-symlink file $stock_run/$name" "$stock/$name.log" >/dev/null
  rm "$stock_run/$name"
  printf '%s' "$original" >"$stock_run/$name"
done
[[ "$(cat "$outside")" == synthetic-outside ]]
echo 'PASS generated app-key, admin, and Docker secret files reject external symlinks before fake Docker'

stock="$(make_stock_fixture local-root-symlink)"
write_valid_runtime "$stock" rootsymlink
outside_local="$stock/outside-local"
mv "$stock/.local" "$outside_local"
ln -s "$outside_local" "$stock/.local"
marker="$stock/ancestor-marker"
printf '%s\n' ': >"$ANCESTOR_MARKER"' >>"$outside_local/local-parity-rootsymlink/runtime.env"
set +e
ANCESTOR_MARKER="$marker" FAKE_DOCKER_LOG="$stock/docker.log" PATH="$stock/fake-bin:/usr/bin:/bin" LOCAL_PARITY_RUN_ID=rootsymlink \
  bash "$stock/tools/local-parity/run.sh" verify >"$stock/local-root-symlink.log" 2>&1
rc=$?
set -e
[[ "$rc" -eq 2 && ! -e "$marker" && ! -e "$stock/docker.log" ]]
grep -F 'unsafe local runtime root' "$stock/local-root-symlink.log" >/dev/null
echo 'PASS symlinked .local ancestor is rejected before runtime parsing'

stock="$(make_stock_fixture moved-run-cleanup)"
write_valid_runtime "$stock" moved
original_run="$stock/.local/local-parity-moved"
saved_run="$stock/.local/local-parity-moved.saved"
mv "$original_run" "$saved_run"
ln -s "$saved_run" "$original_run"
set +e
FAKE_DOCKER_LOG="$stock/docker.log" PATH="$stock/fake-bin:/usr/bin:/bin" LOCAL_PARITY_RUN_ID=moved \
  bash "$stock/tools/local-parity/run.sh" cleanup >"$stock/moved-cleanup.log" 2>&1
rc=$?
set -e
[[ "$rc" -eq 2 && -L "$original_run" && -d "$saved_run" && ! -e "$stock/docker.log" ]]
grep -F "expected canonical non-symlink directory $original_run" "$stock/moved-cleanup.log" >/dev/null
grep -F 'automatic cleanup will not search for or delete a moved run directory' "$stock/moved-cleanup.log" >/dev/null
grep -F 'use run.bingo-test.sh stop moved' "$stock/moved-cleanup.log" >/dev/null
echo 'PASS cleanup refuses a replaced run symlink, preserves the unknown moved directory, and reports safe scope'

stock="$(make_stock_fixture safe-cleanup)"
write_valid_runtime "$stock" safecleanup
stock_run="$stock/.local/local-parity-safecleanup"
marker="$stock/cleanup-runtime-marker"
payload="$stock/cleanup-runtime-payload.env"
cp "$stock_run/runtime.env" "$payload"
printf '%s\n' ': >"$CLEANUP_RUNTIME_MARKER"' >>"$payload"
rm "$stock_run/runtime.env"
ln -s "$payload" "$stock_run/runtime.env"
printf '%s\n' 'services: {}' >"$stock_run/compose.bingo-test.prepared.yaml"
FAKE_DOCKER_LOG="$stock/docker.log" CLEANUP_RUNTIME_MARKER="$marker" PATH="$stock/fake-bin:/usr/bin:/bin" LOCAL_PARITY_RUN_ID=safecleanup \
LOCAL_PARITY_EXPECTED_PROJECT=xboard-user-parity-safecleanup \
LOCAL_PARITY_COMPOSE_OVERLAY=.local/local-parity-safecleanup/compose.bingo-test.prepared.yaml \
  bash "$stock/tools/local-parity/run.sh" cleanup-safe >"$stock/safe-cleanup.log" 2>&1
[[ ! -e "$marker" && ! -e "$stock_run" && -s "$stock/docker.log" ]]
grep -F 'without reading runtime.env' "$stock/safe-cleanup.log" >/dev/null
echo 'PASS cleanup-safe removes only the canonical run/project without parsing a malicious runtime file'

bash "$repo/tools/local-parity/run.bingo-test.captcha-image.test.sh"

echo 'ALL FAKE RESTRICTED-RUNNER TESTS PASSED; no real service was started.'
