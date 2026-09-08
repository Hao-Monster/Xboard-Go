#!/usr/bin/env bash
set -euo pipefail

# Transcript-reconstructed local runner. Run `prepare`, inspect the generated
# Compose configuration, then let the assigned executor run start/init/verify.
# A completed run must be marked PENDING REVALIDATION until its evidence files
# are retained under output/local-parity-<run-id>/.

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$root_dir"

action="${1:-}"
run_id="${LOCAL_PARITY_RUN_ID:-}"
if [[ -z "$run_id" && "$action" == "prepare" ]]; then
  run_id="$(date -u +%Y%m%d%H%M%S)-$(openssl rand -hex 6)"
fi
if [[ ! "$run_id" =~ ^[a-z0-9-]+$ ]]; then
  echo 'LOCAL_PARITY_RUN_ID must contain only lowercase letters, digits, and hyphens' >&2
  exit 2
fi

run_dir=".local/local-parity-${run_id}"
evidence_dir="output/local-parity-${run_id}"
runtime_env="${run_dir}/runtime.env"
project="xboard-user-parity-${run_id}"
candidate_image="xboard-go:user-parity-${run_id}"
compose=(docker compose -p "$project" -f compose.local.yaml -f tools/local-parity/compose.user-parity.yaml --profile e2e)

load_runtime() {
  if [[ ! -f "$runtime_env" ]]; then
    echo "missing $runtime_env; run prepare first with LOCAL_PARITY_RUN_ID=$run_id" >&2
    exit 2
  fi
  # This file contains only paths, image tags, generated identifiers, and ports.
  # It never stores a secret value.
  # shellcheck disable=SC1090
  source "$runtime_env"
  export LOCAL_PARITY_RUN_DIR="$run_dir"
  export LOCAL_LEGACY_APP_KEY="$(<"$run_dir/local-legacy-app-key.txt")"
}

require_clean_head() {
  if ! git diff --quiet || ! git diff --cached --quiet; then
    echo 'refusing to build from a dirty tracked source tree; commit or discard tracked changes first' >&2
    exit 2
  fi
  local head
  head="$(git rev-parse HEAD)"
  if [[ -n "${XBOARD_GO_REVISION:-}" && "$XBOARD_GO_REVISION" != "$head" ]]; then
    echo 'XBOARD_GO_REVISION must equal the current clean HEAD; building arbitrary source under another revision label is forbidden' >&2
    exit 2
  fi
  printf '%s' "$head"
}

prepare() {
  if [[ -e "$run_dir" || -e "$evidence_dir" ]]; then
    echo "refusing to reuse existing run path for $run_id" >&2
    exit 2
  fi
  install -d -m 700 "$run_dir/image-context" "$run_dir/legacy-data" "$evidence_dir"
  umask 077
  printf '%s' "$(openssl rand -hex 24)" >"$run_dir/bootstrap-password.txt"
  openssl rand -base64 32 | tr -d '\n' >"$run_dir/settings-encryption-key.txt"
  printf 'base64:%s' "$(openssl rand -base64 32 | tr -d '\n')" >"$run_dir/local-legacy-app-key.txt"
  printf '%s@parity.test' "legacy-admin-$(openssl rand -hex 8)" >"$run_dir/legacy-admin-email.txt"
  printf '%s' "$(openssl rand -hex 24)" >"$run_dir/legacy-admin-password.txt"
  printf '%s' 'e2e-admin-secure' >"$run_dir/legacy-admin-path.txt"
  # Docker Compose exposes these two file secrets as root-owned read-only files
  # in a process running as UID 65532. Keep the parent private and make only
  # these required files readable; do not relax the other generated secrets.
  chmod 444 "$run_dir/bootstrap-password.txt" "$run_dir/settings-encryption-key.txt"
  chmod 600 "$run_dir/local-legacy-app-key.txt" "$run_dir/legacy-admin-email.txt" "$run_dir/legacy-admin-password.txt" "$run_dir/legacy-admin-path.txt"

  local revision
  revision="$(require_clean_head)"
  cat >"$runtime_env" <<EOF
export COMPOSE_PROJECT_NAME='$project'
export XBOARD_GO_IMAGE='$candidate_image'
export XBOARD_GO_REVISION='$revision'
export XBOARD_GO_PORT='${XBOARD_GO_PORT:-18780}'
export XBOARD_LEGACY_PORT='${XBOARD_LEGACY_PORT:-18781}'
export XBOARD_MAILPIT_PORT='${XBOARD_MAILPIT_PORT:-18782}'
export XBOARD_GO_BOOTSTRAP_ADMIN_EMAIL='admin@legacy-parity.test'
export XBOARD_GO_BOOTSTRAP_ADMIN_PASSWORD_FILE='$root_dir/$run_dir/bootstrap-password.txt'
export XBOARD_GO_SETTINGS_ENCRYPTION_KEY_FILE='$root_dir/$run_dir/settings-encryption-key.txt'
export XBOARD_E2E_ADMIN_PATH='e2e-admin-secure'
export XBOARD_LEGACY_ADMIN_PATH='e2e-admin-secure'
export XBOARD_CAPTCHA_ALLOW_INSECURE='true'
export XBOARD_CAPTCHA_RECAPTCHA_VERIFY_URL='http://captcha-stub:4199/recaptcha'
export XBOARD_CAPTCHA_RECAPTCHA_V3_VERIFY_URL='http://captcha-stub:4199/recaptcha-v3'
export XBOARD_CAPTCHA_TURNSTILE_VERIFY_URL='http://captcha-stub:4199/turnstile'
export XBOARD_LEGACY_IMAGE='${XBOARD_LEGACY_IMAGE:-xboard-legacy-parity:8065164}'
EOF
  chmod 600 "$runtime_env"
  {
    printf 'runtime_head=%s\n' "$revision"
    printf 'tracked_status=%s\n' "$(git status --porcelain --untracked-files=no)"
    if [[ -f web/parity/user-generation-persistence.spec.ts ]]; then
      printf 'user_parity_source_sha256=%s\n' "$(sha256sum web/parity/user-generation-persistence.spec.ts | awk '{print $1}')"
    else
      printf 'user_parity_source_sha256=MISSING\n'
    fi
  } >"$evidence_dir/source-identity.txt"
  load_runtime
  "${compose[@]}" config --quiet
  echo "prepared $run_id; Compose is syntactically valid and no containers were started"
}

build_candidate() {
  local revision
  revision="$(require_clean_head)"
  if [[ "$revision" != "$XBOARD_GO_REVISION" ]]; then
    echo 'runtime source HEAD changed after prepare; start a new run' >&2
    exit 2
  fi
  if [[ -n "${LOCAL_PARITY_PREBUILT_XBOARD:-}" ]]; then
    if [[ ! -f "$LOCAL_PARITY_PREBUILT_XBOARD" || -z "${LOCAL_PARITY_PREBUILT_XBOARD_SHA256:-}" || -z "${LOCAL_PARITY_PREBUILT_XBOARD_SOURCE:-}" ]]; then
      echo 'prebuilt candidate requires LOCAL_PARITY_PREBUILT_XBOARD, its SHA-256, and a source-manifest reference' >&2
      exit 2
    fi
    local actual_sha
    actual_sha="$(sha256sum "$LOCAL_PARITY_PREBUILT_XBOARD" | awk '{print $1}')"
    if [[ "$actual_sha" != "$LOCAL_PARITY_PREBUILT_XBOARD_SHA256" ]]; then
      echo 'prebuilt candidate SHA-256 did not match LOCAL_PARITY_PREBUILT_XBOARD_SHA256' >&2
      exit 2
    fi
    cp "$LOCAL_PARITY_PREBUILT_XBOARD" "$run_dir/image-context/xboard"
    printf 'prebuilt_xboard_sha256=%s\nprebuilt_xboard_source=%s\n' "$actual_sha" "$LOCAL_PARITY_PREBUILT_XBOARD_SOURCE" >>"$evidence_dir/source-identity.txt"
  else
    test "$(go env GOVERSION)" = 'go1.26.8'
    {
      go version
      CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -buildvcs=false -trimpath -ldflags="-s -w -buildid= -X main.buildRevision=${revision}" -o "$run_dir/image-context/xboard" ./cmd/xboard
      go version -m "$run_dir/image-context/xboard"
      sha256sum "$run_dir/image-context/xboard"
    } >>"$evidence_dir/source-identity.txt"
  fi
  pnpm --dir web build
  cp -a web/dist "$run_dir/image-context/web-dist"
  docker build --file tools/local-parity/Dockerfile.candidate --build-arg "APP_REVISION=$revision" --tag "$XBOARD_GO_IMAGE" "$run_dir/image-context"
  test "$(docker image inspect --format '{{index .Config.Labels "org.opencontainers.image.revision"}}' "$XBOARD_GO_IMAGE")" = "$revision"
}

start() {
  load_runtime
  build_candidate
  "${compose[@]}" up -d --no-build --wait | tee "$evidence_dir/compose-up.log"
}

initialize() {
  load_runtime
  "${compose[@]}" exec -T legacy-oracle php /www/artisan migrate --force --no-interaction | tee "$evidence_dir/legacy-migrate.log"
  "${compose[@]}" exec -T \
    -e "LOCAL_PARITY_ADMIN_EMAIL=$(<"$run_dir/legacy-admin-email.txt")" \
    -e "LOCAL_PARITY_ADMIN_PASSWORD=$(<"$run_dir/legacy-admin-password.txt")" \
    -e "LOCAL_PARITY_ADMIN_PATH=$(<"$run_dir/legacy-admin-path.txt")" \
    legacy-oracle php /opt/local-parity/init-legacy-oracle.php | tee "$evidence_dir/legacy-init.json"
}

verify() {
  load_runtime
  curl --fail --silent --show-error --max-time 10 --output /dev/null --write-out 'legacy status=%{http_code} bytes=%{size_download}\n' "http://127.0.0.1:${XBOARD_LEGACY_PORT}/" | tee "$evidence_dir/legacy-http.txt"
  curl --fail --silent --show-error --max-time 10 --output /dev/null --write-out 'go status=%{http_code} bytes=%{size_download}\n' "http://127.0.0.1:${XBOARD_GO_PORT}/${XBOARD_E2E_ADMIN_PATH}" | tee "$evidence_dir/go-admin-http.txt"
  docker image inspect "$XBOARD_LEGACY_IMAGE" --format 'id={{.Id}} repoDigests={{json .RepoDigests}}' | tee "$evidence_dir/legacy-image.txt"
  docker image inspect "$XBOARD_GO_IMAGE" --format 'id={{.Id}} repoDigests={{json .RepoDigests}} revision={{index .Config.Labels "org.opencontainers.image.revision"}}' | tee "$evidence_dir/candidate-image.txt"
}

cleanup() {
  load_runtime
  "${compose[@]}" down --volumes --remove-orphans | tee "$evidence_dir/compose-down.log"
  docker image rm "$XBOARD_GO_IMAGE" >>"$evidence_dir/compose-down.log" 2>&1 || true
  rm -rf -- "$run_dir"
  unset LOCAL_LEGACY_APP_KEY
  echo "removed generated runtime resources for $run_id; retained $evidence_dir"
}

case "$action" in
  prepare) prepare ;;
  start) start ;;
  init) initialize ;;
  verify) verify ;;
  cleanup) cleanup ;;
  *)
    echo 'usage: tools/local-parity/run.sh {prepare|start|init|verify|cleanup}' >&2
    exit 2
    ;;
esac
