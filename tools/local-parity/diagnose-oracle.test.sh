#!/usr/bin/env bash
set -Eeuo pipefail

echo 'MOCK CONTROL-FLOW TESTS ONLY: no Oracle container, image, network, volume, or real HTTP service is used.'
repo="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"
source_script="$repo/tools/local-parity/diagnose-oracle.sh"
test_root="$(mktemp -d /tmp/diagnose-oracle-test.XXXXXX)"
cleanup_test_root() {
  [[ "$test_root" == /tmp/diagnose-oracle-test.* && -d "$test_root" && ! -L "$test_root" ]] || return 1
  rm -rf --one-file-system -- "$test_root"
}
trap cleanup_test_root EXIT

make_fixture() {
  local name="$1"
  local fixture="$test_root/$name/repo"
  mkdir -p "$fixture/tools/local-parity" "$fixture/output" "$fixture/.local" "$fixture/mock-bin"
  cp "$source_script" "$fixture/tools/local-parity/diagnose-oracle.sh"
  chmod 755 "$fixture/tools/local-parity/diagnose-oracle.sh"
  : >"$fixture/compose.local.yaml"
  : >"$fixture/tools/local-parity/compose.user-parity.yaml"
  cat >"$fixture/tools/local-parity/run.sh" <<'STUB'
#!/usr/bin/env bash
set -Eeuo pipefail
[[ "${1:-}" == prepare ]]
run=".local/local-parity-${LOCAL_PARITY_RUN_ID:?}"
evidence="output/local-parity-${LOCAL_PARITY_RUN_ID}"
mkdir -p "$run" "$evidence"
printf '%s\n' "export XBOARD_LEGACY_IMAGE='mock-oracle'" >"$run/runtime.env"
printf '%s' 'base64:mock-app-key' >"$run/local-legacy-app-key.txt"
printf '%s' 'mock-admin@parity.test' >"$run/legacy-admin-email.txt"
printf '%s' 'mock-password' >"$run/legacy-admin-password.txt"
printf '%s' 'mock-admin-path' >"$run/legacy-admin-path.txt"
STUB
  chmod 755 "$fixture/tools/local-parity/run.sh"
  cat >"$fixture/mock-bin/docker" <<'MOCK_DOCKER'
#!/usr/bin/env bash
set -Eeuo pipefail
printf '%q ' "$@" >>"${MOCK_DOCKER_LOG:?}"
printf '\n' >>"$MOCK_DOCKER_LOG"
case "${1:-}" in
  ps|volume|network) exit 0 ;;
  compose)
    args=" $* "
    if [[ "$args" == *' logs '* ]]; then
      printf '%s\n' 'App\Domain\QuotedException at /www/app/Http/Test.php:42'
    elif [[ "$args" == *' exec '* && "$args" == *'curl -sS'* ]]; then
      printf '%s' '200,21'
    elif [[ "$args" == *' run '* ]]; then
      printf '%s\n' '{"mock":true}'
    elif [[ "$args" == *' down '* ]]; then
      printf '%s\n' 'mock compose down'
    else
      printf '%s\n' 'mock docker command'
    fi
    ;;
  *) exit 64 ;;
esac
MOCK_DOCKER
  chmod 755 "$fixture/mock-bin/docker"
  cat >"$fixture/mock-bin/curl" <<'MOCK_CURL'
#!/usr/bin/env bash
set -u
printf '%q ' "$@" >>"${MOCK_CURL_LOG:?}"
printf '\n' >>"$MOCK_CURL_LOG"
case "${MOCK_CURL_SCENARIO:?}" in
  success) printf '%s' '200,17'; exit 0 ;;
  http500) printf '%s' '500,17'; exit 0 ;;
  transport) printf '%s' '000,0'; exit 7 ;;
  *) exit 64 ;;
esac
MOCK_CURL
  chmod 755 "$fixture/mock-bin/curl"
  printf '%s\n' "$fixture"
}

run_diag() {
  local fixture="$1" id="$2" scenario="$3" out="$4" stdout_file="$5"
  (
    cd "$fixture"
    PATH="$fixture/mock-bin:/usr/bin:/bin" \
    MOCK_DOCKER_LOG="$fixture/mock-docker.log" \
    MOCK_CURL_LOG="$fixture/mock-curl.log" \
    MOCK_CURL_SCENARIO="$scenario" \
    LOCAL_PARITY_RUN_ID="$id" \
    XBOARD_LEGACY_PORT=18781 \
    LOCAL_PARITY_DIAGNOSTIC_OUTPUT="$out" \
    tools/local-parity/diagnose-oracle.sh
  ) >"$stdout_file" 2>&1
}

assert_mock_cleanup() {
  local fixture="$1" id="$2" out="$3"
  [[ ! -e "$fixture/.local/local-parity-$id" && ! -L "$fixture/.local/local-parity-$id" ]]
  grep -F ' down ' "$fixture/mock-docker.log" >/dev/null
  grep -Fx 'cleanup_exit=0' "$out/exit.txt" >/dev/null
}

fixture="$(make_fixture success-json)"
out="$fixture/diagnostic-success"
run_diag "$fixture" success-json success "$out" "$fixture/stdout.log"
jq -e --arg expected 'App\Domain\QuotedException' '.exception_category == $expected and .stack_location == "/www/app/Http/Test.php:42"' "$out/redacted.json" >/dev/null
grep -F 'outer=0,200,17' "$out/probe.txt" >/dev/null
grep -Fx 'driver_exit=0' "$out/exit.txt" >/dev/null
assert_mock_cleanup "$fixture" success-json "$out"
echo 'PASS success requires transport exit 0 plus HTTP 200; backslash JSON is parseable'

run_failure_case() {
  local scenario="$1" expected_probe="$2"
  local fixture out started elapsed rc
  fixture="$(make_fixture "$scenario")"
  out="$fixture/diagnostic-$scenario"
  started=$SECONDS
  set +e
  run_diag "$fixture" "$scenario" "$scenario" "$out" "$fixture/stdout.log"
  rc=$?
  set -e
  elapsed=$((SECONDS - started))
  [[ "$rc" -ne 0 ]]
  (( elapsed >= 58 && elapsed <= 75 ))
  jq -e 'type == "object" and .source == "container-stdout"' "$out/redacted.json" >/dev/null
  grep -F "$expected_probe" "$out/probe.txt" >/dev/null
  grep -Fx 'driver_exit=1' "$out/exit.txt" >/dev/null
  assert_mock_cleanup "$fixture" "$scenario" "$out"
  printf 'PASS %s remains nonzero after bounded wait elapsed=%ss and retains valid JSON/cleanup\n' "$scenario" "$elapsed"
}
run_failure_case http500 'outer=0,500,17'
run_failure_case transport 'outer=7,000,0'

fixture="$(make_fixture existing-run)"
mkdir -p "$fixture/.local/local-parity-existing-run"
printf '%s' keep >"$fixture/.local/local-parity-existing-run/sentinel"
set +e
run_diag "$fixture" existing-run success "$fixture/diagnostic-existing" "$fixture/stdout.log"
rc=$?
set -e
[[ "$rc" -eq 2 ]]
[[ "$(cat "$fixture/.local/local-parity-existing-run/sentinel")" == keep ]]
[[ ! -e "$fixture/diagnostic-existing" ]]
[[ ! -s "$fixture/mock-docker.log" ]]
echo 'PASS existing run is refused before trap/docker and sentinel is retained'

fixture="$(make_fixture symlink-run)"
external="$test_root/symlink-external"
mkdir -p "$external"
printf '%s' keep >"$external/sentinel"
ln -s "$external" "$fixture/.local/local-parity-symlink-run"
set +e
run_diag "$fixture" symlink-run success "$fixture/diagnostic-symlink" "$fixture/stdout.log"
rc=$?
set -e
[[ "$rc" -eq 2 ]]
[[ "$(cat "$external/sentinel")" == keep ]]
[[ -L "$fixture/.local/local-parity-symlink-run" ]]
[[ ! -e "$fixture/diagnostic-symlink" ]]
[[ ! -s "$fixture/mock-docker.log" ]]
echo 'PASS symlinked run is refused before trap/docker and external sentinel is retained'

fixture="$(make_fixture existing-output)"
mkdir -p "$fixture/diagnostic-existing-output"
printf '%s' keep >"$fixture/diagnostic-existing-output/sentinel"
set +e
run_diag "$fixture" existing-output success "$fixture/diagnostic-existing-output" "$fixture/stdout.log"
rc=$?
set -e
[[ "$rc" -eq 2 ]]
[[ "$(cat "$fixture/diagnostic-existing-output/sentinel")" == keep ]]
[[ ! -s "$fixture/mock-docker.log" ]]
echo 'PASS existing output is refused and old log sentinel is retained'

echo 'ALL MOCK CONTROL-FLOW TESTS PASSED; this is not an Oracle test.'
