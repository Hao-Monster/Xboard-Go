#!/usr/bin/env bash
set -Eeuo pipefail

echo 'FAKE CAPTCHA IMAGE VALIDATION TESTS ONLY: no runner service action is executed.'
repo="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"
test_root="$(mktemp -d /tmp/run-bingo-captcha-image-test.XXXXXX)"

cleanup() {
  [[ "$test_root" == /tmp/run-bingo-captcha-image-test.* && -d "$test_root" && ! -L "$test_root" ]] || return 1
  rm -rf --one-file-system -- "$test_root"
}
trap cleanup EXIT

make_fixture() {
  local name="$1"
  local fixture="$test_root/$name"
  mkdir -p "$fixture/tools/local-parity"
  cp "$repo/tools/local-parity/run.bingo-test.sh" "$fixture/tools/local-parity/"
  cp "$repo/tools/local-parity/compose.bingo-test.yaml" "$fixture/tools/local-parity/"
  python3 - "$fixture/tools/local-parity/run.bingo-test.sh" <<'PY'
from pathlib import Path
import sys

path = Path(sys.argv[1])
text = path.read_text()
needle = "/usr/bin/docker image inspect"
assert text.count(needle) == 3
path.write_text(text.replace(needle, '"$BINGO_TEST_DOCKER" image inspect'))
PY
  cat >"$fixture/tools/local-parity/run.sh" <<'RUNNER'
#!/usr/bin/env bash
set -Eeuo pipefail
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"
cd "$root"
run_dir=".local/local-parity-$LOCAL_PARITY_RUN_ID"
evidence_dir="output/local-parity-$LOCAL_PARITY_RUN_ID"
if [[ "$1" == prepare ]]; then
  mkdir -p "$run_dir" "$evidence_dir"
fi
printf '%s\n' "$1" >>"$BINGO_FAKE_DISPATCH_LOG"
RUNNER
  cat >"$fixture/fake-docker" <<'DOCKER'
#!/usr/bin/env bash
set -Eeuo pipefail
ref="${*: -1}"
case "$ref" in
  xboard-go:gpt55-user-2e1713c69dfc-offline)
    printf '%s\n' 'sha256:0e19640a157c07392250caadd0321cceced1f8c214a1cdf5156536058bfddfb5'
    ;;
  xboard-legacy-parity:8065164)
    printf '%s\n' 'sha256:6bb8ce9dd1a06ce33400588dd9a5002dccb7e73113ed4d88a7ce065f3439bd94'
    ;;
  xboard-node-parity:24.18.0-ae91dcc111a6)
    case "${BINGO_CAPTCHA_FAKE_MODE:-correct}" in
      correct) printf '%s\n' 'sha256:77343b9c9fae9d567dbbb308eed6ac08564ef29663b6f09e7183b0a7c94f0c73' ;;
      missing) exit 1 ;;
      mismatch) printf '%s\n' 'sha256:0000000000000000000000000000000000000000000000000000000000000000' ;;
      *) exit 2 ;;
    esac
    ;;
  *) exit 2 ;;
esac
DOCKER
  chmod 755 "$fixture/tools/local-parity/run.bingo-test.sh" "$fixture/tools/local-parity/run.sh" "$fixture/fake-docker"
  git -C "$fixture" init -q
  printf '%s\n' "$fixture"
}

invoke() {
  local fixture="$1" action="$2" mode="$3"
  local -a test_env=(
    "BINGO_PARITY_EXPECTED_ROOT=$fixture"
    "BINGO_TEST_DOCKER=$fixture/fake-docker"
    "BINGO_CAPTCHA_FAKE_MODE=$mode"
    "BINGO_FAKE_DISPATCH_LOG=$fixture/dispatch.log"
  )
  env "${test_env[@]}" "$fixture/tools/local-parity/run.bingo-test.sh" "$action" bingo-captcha-validation
}

fixture="$(make_fixture control)"
invoke "$fixture" prepare correct
invoke "$fixture" start correct
[[ "$(paste -sd, "$fixture/dispatch.log")" == prepare,start ]]
echo 'PASS valid pinned captcha image reaches prepare and start runner dispatch'

for mode in missing mismatch; do
  fixture="$(make_fixture "prepare-$mode")"
  set +e
  invoke "$fixture" prepare "$mode" >"$fixture/prepare.log" 2>&1
  rc=$?
  set -e
  dispatch_count=0
  [[ ! -f "$fixture/dispatch.log" ]] || dispatch_count="$(wc -l <"$fixture/dispatch.log")"
  [[ "$rc" -eq 2 && "$dispatch_count" -eq 0 ]]
  if [[ "$mode" == missing ]]; then
    grep -F 'reviewed local captcha image is unavailable' "$fixture/prepare.log" >/dev/null
  else
    grep -F 'reviewed local captcha image ID mismatch' "$fixture/prepare.log" >/dev/null
  fi
  echo "PASS captcha image $mode is rejected before prepare runner dispatch"
done

for mode in missing mismatch; do
  fixture="$(make_fixture "start-$mode")"
  invoke "$fixture" prepare correct
  [[ "$(wc -l <"$fixture/dispatch.log")" -eq 1 ]]
  set +e
  invoke "$fixture" start "$mode" >"$fixture/start.log" 2>&1
  rc=$?
  set -e
  [[ "$rc" -eq 2 && "$(wc -l <"$fixture/dispatch.log")" -eq 1 ]]
  if [[ "$mode" == missing ]]; then
    grep -F 'reviewed local captcha image is unavailable' "$fixture/start.log" >/dev/null
  else
    grep -F 'reviewed local captcha image ID mismatch' "$fixture/start.log" >/dev/null
  fi
  echo "PASS captcha image $mode is rejected before start runner dispatch"
done

echo 'ALL FAKE CAPTCHA IMAGE VALIDATION TESTS PASSED; no runner service action was executed.'
