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
if [[ -z "$run_id" && "$action" == "validate-prebuilt" ]]; then
  run_id='validation'
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
if [[ -n "${LOCAL_PARITY_EXPECTED_PROJECT:-}" && "$LOCAL_PARITY_EXPECTED_PROJECT" != "$project" ]]; then
  echo 'LOCAL_PARITY_EXPECTED_PROJECT does not match the run ID-derived project' >&2
  exit 2
fi

compose_files=(-f compose.local.yaml -f tools/local-parity/compose.user-parity.yaml)
if [[ -n "${LOCAL_PARITY_COMPOSE_OVERLAY:-}" ]]; then
  overlay_rel="$LOCAL_PARITY_COMPOSE_OVERLAY"
  if [[ "$overlay_rel" = /* || ! "$overlay_rel" =~ ^tools/local-parity/[A-Za-z0-9._-]+\.ya?ml$ ]]; then
    echo 'LOCAL_PARITY_COMPOSE_OVERLAY must name a YAML file directly under tools/local-parity' >&2
    exit 2
  fi
  overlay_path="$root_dir/$overlay_rel"
  if [[ ! -f "$overlay_path" || -L "$overlay_path" ]]; then
    echo 'LOCAL_PARITY_COMPOSE_OVERLAY must be an existing non-symlink file' >&2
    exit 2
  fi
  overlay_real="$(realpath -e -- "$overlay_path")"
  if [[ "$(dirname -- "$overlay_real")" != "$root_dir/tools/local-parity" ]]; then
    echo 'LOCAL_PARITY_COMPOSE_OVERLAY resolved outside tools/local-parity' >&2
    exit 2
  fi
  compose_files+=(-f "$overlay_real")
fi
compose=(docker compose -p "$project" "${compose_files[@]}" --profile e2e)

git_in_tree() {
  if ! git -C "$root_dir" rev-parse --is-inside-work-tree >/dev/null 2>&1; then
    echo 'the source must be a WSL-native clone; create an exact-SHA bundle on Windows and clone it in WSL instead of rewriting .git metadata' >&2
    return 2
  fi
  git -C "$root_dir" "$@"
}

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
  if ! git_in_tree diff --quiet || ! git_in_tree diff --cached --quiet; then
    echo 'refusing to build from a dirty tracked source tree; commit or discard tracked changes first' >&2
    exit 2
  fi
  local head
  head="$(git_in_tree rev-parse HEAD)"
  if [[ -n "${XBOARD_GO_REVISION:-}" && "$XBOARD_GO_REVISION" != "$head" ]]; then
    echo 'XBOARD_GO_REVISION must equal the current clean HEAD; building arbitrary source under another revision label is forbidden' >&2
    exit 2
  fi
  printf '%s' "$head"
}

validate_prebuilt_manifest() {
  local expected_revision="$1"
  if [[ ! "$expected_revision" =~ ^[0-9a-f]{40}$ ]]; then
    echo 'expected prebuilt source revision must be an exact lowercase Git SHA' >&2
    exit 2
  fi
  if [[ ! -f "${LOCAL_PARITY_PREBUILT_XBOARD:-}" || ! -f "${LOCAL_PARITY_PREBUILT_MANIFEST:-}" ]]; then
    echo 'prebuilt candidate requires LOCAL_PARITY_PREBUILT_XBOARD and LOCAL_PARITY_PREBUILT_MANIFEST files' >&2
    exit 2
  fi

  declare -A manifest=()
  local line key value line_number=0
  while IFS= read -r line || [[ -n "$line" ]]; do
    line_number=$((line_number + 1))
    if [[ "$line" != *=* || "$line" == *=*=* ]]; then
      echo "invalid prebuilt manifest line $line_number" >&2
      exit 2
    fi
    key="${line%%=*}"
    value="${line#*=}"
    case "$key" in
      source_commit|binary_sha256|goos|goarch|cgo_enabled|go_version) ;;
      *)
        echo "unexpected prebuilt manifest key $key" >&2
        exit 2
        ;;
    esac
    if [[ -z "$value" || -n "${manifest[$key]+present}" ]]; then
      echo "empty or duplicate prebuilt manifest key $key" >&2
      exit 2
    fi
    manifest[$key]="$value"
  done <"$LOCAL_PARITY_PREBUILT_MANIFEST"

  for key in source_commit binary_sha256 goos goarch cgo_enabled go_version; do
    if [[ -z "${manifest[$key]+present}" ]]; then
      echo "missing prebuilt manifest key $key" >&2
      exit 2
    fi
  done
  if [[ ! "${manifest[source_commit]}" =~ ^[0-9a-f]{40}$ || ! "${manifest[binary_sha256]}" =~ ^[0-9a-f]{64}$ ]]; then
    echo 'prebuilt manifest source_commit or binary_sha256 has an invalid format' >&2
    exit 2
  fi
  if [[ "${manifest[source_commit]}" != "$expected_revision" || "${manifest[goos]}" != linux || "${manifest[goarch]}" != amd64 || "${manifest[cgo_enabled]}" != 0 || "${manifest[go_version]}" != go1.26.8 ]]; then
    echo 'prebuilt manifest does not describe the required clean revision, linux/amd64, CGO_ENABLED=0, Go 1.26.8 artifact' >&2
    exit 2
  fi

  PREBUILT_BINARY_SHA256="$(sha256sum "$LOCAL_PARITY_PREBUILT_XBOARD" | awk '{print $1}')"
  if [[ "$PREBUILT_BINARY_SHA256" != "${manifest[binary_sha256]}" ]]; then
    echo 'prebuilt candidate SHA-256 did not match the manifest' >&2
    exit 2
  fi
  PREBUILT_MANIFEST_SHA256="$(sha256sum "$LOCAL_PARITY_PREBUILT_MANIFEST" | awk '{print $1}')"
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
    printf 'tracked_status=%s\n' "$(git_in_tree status --porcelain --untracked-files=no)"
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
    validate_prebuilt_manifest "$revision"
    cp "$LOCAL_PARITY_PREBUILT_XBOARD" "$run_dir/image-context/xboard"
    cp "$LOCAL_PARITY_PREBUILT_MANIFEST" "$evidence_dir/prebuilt-manifest.env"
    printf 'prebuilt_xboard_sha256=%s\nprebuilt_manifest=%s\nprebuilt_manifest_sha256=%s\n' "$PREBUILT_BINARY_SHA256" "$LOCAL_PARITY_PREBUILT_MANIFEST" "$PREBUILT_MANIFEST_SHA256" >>"$evidence_dir/source-identity.txt"
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
  local -a candidate_build_args=(--build-arg "APP_REVISION=$revision")
  if [[ -n "${LOCAL_PARITY_CANDIDATE_BASE_IMAGE:-}" ]]; then
    if [[ ! "${LOCAL_PARITY_CANDIDATE_BASE_IMAGE_ID:-}" =~ ^sha256:[0-9a-f]{64}$ ]]; then
      echo 'an explicit candidate base image requires its exact sha256 image ID' >&2
      exit 2
    fi
    local actual_base_id
    actual_base_id="$(docker image inspect --format '{{.Id}}' "$LOCAL_PARITY_CANDIDATE_BASE_IMAGE")" || {
      echo 'unable to inspect the explicit candidate base image' >&2
      exit 2
    }
    if [[ "$actual_base_id" != "$LOCAL_PARITY_CANDIDATE_BASE_IMAGE_ID" ]]; then
      echo 'explicit candidate base image ID mismatch' >&2
      exit 2
    fi
    candidate_build_args+=(--build-arg "BASE_IMAGE=$LOCAL_PARITY_CANDIDATE_BASE_IMAGE")
    {
      printf 'candidate_base_image=%s\n' "$LOCAL_PARITY_CANDIDATE_BASE_IMAGE"
      printf 'candidate_base_image_id=%s\n' "$actual_base_id"
    } >>"$evidence_dir/source-identity.txt"
  elif [[ -n "${LOCAL_PARITY_CANDIDATE_BASE_IMAGE_ID:-}" ]]; then
    echo 'candidate base image ID was set without a base image reference' >&2
    exit 2
  fi
  docker build --file tools/local-parity/Dockerfile.candidate "${candidate_build_args[@]}" --tag "$XBOARD_GO_IMAGE" "$run_dir/image-context"
  test "$(docker image inspect --format '{{index .Config.Labels "org.opencontainers.image.revision"}}' "$XBOARD_GO_IMAGE")" = "$revision"
}

start() {
  load_runtime
  build_candidate
  # Bring up only Redis first. `initialize` must complete before the Oracle's
  # Caddy/Octane entrypoint can populate the forever settings cache.
  "${compose[@]}" up -d --no-build --wait legacy-redis | tee "$evidence_dir/compose-up.log"
  initialize_before_oracle
  printf '%s\n' 'legacy migration and settings initialization completed before legacy-oracle startup' >"$evidence_dir/legacy-initialization-order.txt"
  "${compose[@]}" up -d --no-build --wait | tee -a "$evidence_dir/compose-up.log"
}

initialize_before_oracle() {
  load_runtime
  # `admin_setting` caches all settings in Redis forever.  Do this while only
  # Redis is running: starting Octane first can cache an empty database and
  # permanently register its fallback (APP_KEY-derived) administrator route.
  "${compose[@]}" run --rm --no-deps --entrypoint php legacy-oracle /www/artisan migrate --force --no-interaction | tee "$evidence_dir/legacy-migrate.log"
  "${compose[@]}" run --rm --no-deps \
    -e "LOCAL_PARITY_ADMIN_EMAIL=$(<"$run_dir/legacy-admin-email.txt")" \
    -e "LOCAL_PARITY_ADMIN_PASSWORD=$(<"$run_dir/legacy-admin-password.txt")" \
    -e "LOCAL_PARITY_ADMIN_PATH=$(<"$run_dir/legacy-admin-path.txt")" \
    --entrypoint php legacy-oracle /opt/local-parity/init-legacy-oracle.php | tee "$evidence_dir/legacy-init.json"
}

initialize() {
  load_runtime
  if [[ -s "$evidence_dir/legacy-init.json" && -s "$evidence_dir/legacy-migrate.log" ]]; then
    echo 'legacy initialization was already performed by start before legacy-oracle startup'
    return
  fi
  echo 'run start first; it performs legacy initialization before legacy-oracle starts' >&2
  exit 2
}

verify() {
  load_runtime
  local legacy_path root_status
  legacy_path="$(<"$run_dir/legacy-admin-path.txt")"
  test "$legacy_path" = "$XBOARD_LEGACY_ADMIN_PATH"
  printf 'http://127.0.0.1:%s/%s\n' "$XBOARD_LEGACY_PORT" "$legacy_path" >"$evidence_dir/legacy-admin-url.txt"
  "${compose[@]}" exec -T -e "LOCAL_PARITY_ADMIN_PATH=$legacy_path" legacy-oracle php /opt/local-parity/legacy-state.php | tee "$evidence_dir/legacy-state.json"
  root_status="$(curl --silent --show-error --max-time 10 --output /dev/null --write-out '%{http_code}' "http://127.0.0.1:${XBOARD_LEGACY_PORT}/" || true)"
  printf 'legacy-root status=%s\n' "$root_status" | tee "$evidence_dir/legacy-root-http.txt"
  if [[ "$root_status" =~ ^5[0-9]{2}$ ]]; then
    # Emit only a deidentified exception category and first app/vendor frame;
    # never copy requests, cookies, credentials, or a full Laravel log.
    "${compose[@]}" exec -T legacy-oracle php /opt/local-parity/legacy-diagnostics.php | tee "$evidence_dir/legacy-root-diagnostics.json"
  fi
  curl --fail --silent --show-error --max-time 10 --output /dev/null --write-out 'legacy-admin status=%{http_code} bytes=%{size_download}\n' "http://127.0.0.1:${XBOARD_LEGACY_PORT}/${legacy_path}" | tee "$evidence_dir/legacy-admin-http.txt"
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
  validate-prebuilt)
    validate_prebuilt_manifest "${LOCAL_PARITY_EXPECTED_REVISION:-$(git_in_tree rev-parse HEAD)}"
    printf 'validated prebuilt binary SHA-256 %s with manifest SHA-256 %s\n' "$PREBUILT_BINARY_SHA256" "$PREBUILT_MANIFEST_SHA256"
    ;;
  *)
    echo 'usage: tools/local-parity/run.sh {prepare|start|init|verify|cleanup}' >&2
    exit 2
    ;;
esac
