#!/usr/bin/env bash
set -Eeuo pipefail

# Bounded, no-Go Oracle diagnostic for an isolated clean clone.
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"
cd "$root"
id="${LOCAL_PARITY_RUN_ID:?set run id}"
port="${XBOARD_LEGACY_PORT:?set legacy port}"
out_input="${LOCAL_PARITY_DIAGNOSTIC_OUTPUT:?set stable output dir}"
[[ "$id" =~ ^[a-z0-9-]+$ ]] || { echo 'invalid run id' >&2; exit 2; }
[[ "$port" =~ ^[0-9]+$ ]] && (( port >= 1024 && port <= 65535 )) || { echo 'invalid legacy port' >&2; exit 2; }

local_root="$root/.local"
run_abs="$local_root/local-parity-$id"
run_evidence_abs="$root/output/local-parity-$id"
project="xboard-oracle-$id"
[[ "$out_input" = /* ]] && out_candidate="$out_input" || out_candidate="$root/$out_input"
out_basename="$(basename -- "$out_candidate")"
out_parent="$(dirname -- "$out_candidate")"
[[ "$out_basename" =~ ^[A-Za-z0-9._-]+$ && "$out_basename" != "." && "$out_basename" != ".." ]] || { echo 'unsafe diagnostic output name' >&2; exit 2; }
[[ -d "$out_parent" && ! -L "$out_parent" ]] || { echo 'diagnostic output parent must be an existing non-symlink directory' >&2; exit 2; }
out_abs="$(realpath -e -- "$out_parent")/$out_basename"

[[ ! -L "$local_root" ]] || { echo 'refusing symlinked .local directory' >&2; exit 2; }
[[ ! -e "$run_abs" && ! -L "$run_abs" ]] || { echo 'refusing existing or symlinked run directory' >&2; exit 2; }
[[ ! -e "$run_evidence_abs" && ! -L "$run_evidence_abs" ]] || { echo 'refusing existing or symlinked run evidence directory' >&2; exit 2; }
[[ ! -e "$out_abs" && ! -L "$out_abs" ]] || { echo 'refusing existing or symlinked diagnostic output directory' >&2; exit 2; }
for command_name in docker curl jq timeout realpath; do
  command -v "$command_name" >/dev/null || { echo "missing required command: $command_name" >&2; exit 2; }
done

existing_containers="$(docker ps -aq --filter "label=com.docker.compose.project=$project")" || { echo 'unable to inspect existing Docker containers' >&2; exit 2; }
existing_volumes="$(docker volume ls -q --filter "label=com.docker.compose.project=$project")" || { echo 'unable to inspect existing Docker volumes' >&2; exit 2; }
existing_networks="$(docker network ls -q --filter "label=com.docker.compose.project=$project")" || { echo 'unable to inspect existing Docker networks' >&2; exit 2; }
[[ -z "$existing_containers$existing_volumes$existing_networks" ]] || { echo 'refusing existing Compose project resources' >&2; exit 2; }

install -d -m 700 "$out_abs"
run_claimed=0
project_claimed=0
c=(docker compose -p "$project" -f compose.local.yaml -f tools/local-parity/compose.user-parity.yaml --profile e2e)
exec > >(tee "$out_abs/driver.log") 2>&1

safe_remove_run() {
  [[ "$run_claimed" -eq 1 ]] || return 0
  [[ "$run_abs" == "$root/.local/local-parity-$id" ]] || { echo 'cleanup refused unexpected run path' >&2; return 1; }
  [[ ! -L "$run_abs" ]] || { echo 'cleanup refused symlinked run path' >&2; return 1; }
  if [[ -e "$run_abs" ]]; then
    [[ -d "$run_abs" ]] || { echo 'cleanup refused non-directory run path' >&2; return 1; }
    rm -rf --one-file-system -- "$run_abs"
  fi
}

cleanup() {
  original_rc=$?
  trap - EXIT
  set +e
  cleanup_rc=0
  if [[ "$project_claimed" -eq 1 ]]; then
    timeout --signal=TERM --kill-after=5s 30s "${c[@]}" down --volumes --remove-orphans >>"$out_abs/cleanup.log" 2>&1
    compose_rc=$?
    if [[ "$compose_rc" -ne 0 ]]; then
      printf 'compose_cleanup_exit=%s\n' "$compose_rc" >>"$out_abs/cleanup.log"
      cleanup_rc=1
    fi
  else
    printf '%s\n' 'compose_cleanup=not-owned' >>"$out_abs/cleanup.log"
  fi
  safe_remove_run || cleanup_rc=1
  printf 'driver_exit=%s\ncleanup_exit=%s\n' "$original_rc" "$cleanup_rc" >"$out_abs/exit.txt"
  [[ "$original_rc" -ne 0 || "$cleanup_rc" -eq 0 ]] || original_rc=1
  exit "$original_rc"
}
trap cleanup EXIT

run_claimed=1
bash tools/local-parity/run.sh prepare
[[ -d "$run_abs" && ! -L "$run_abs" ]] || { echo 'prepare did not create the claimed run directory' >&2; exit 1; }
for required in runtime.env local-legacy-app-key.txt legacy-admin-email.txt legacy-admin-password.txt legacy-admin-path.txt; do
  [[ -f "$run_abs/$required" && ! -L "$run_abs/$required" ]] || { echo "prepare did not create safe $required" >&2; exit 1; }
done
# shellcheck disable=SC1090
source "$run_abs/runtime.env"
export LOCAL_PARITY_RUN_DIR=".local/local-parity-$id"
export LOCAL_LEGACY_APP_KEY="$(<"$run_abs/local-legacy-app-key.txt")"

project_claimed=1
"${c[@]}" up -d --wait legacy-redis
"${c[@]}" run --rm --no-deps --entrypoint php legacy-oracle /www/artisan migrate --force --no-interaction >"$out_abs/migrate.log"
"${c[@]}" run --rm --no-deps \
  -e "LOCAL_PARITY_ADMIN_EMAIL=$(<"$run_abs/legacy-admin-email.txt")" \
  -e "LOCAL_PARITY_ADMIN_PASSWORD=$(<"$run_abs/legacy-admin-password.txt")" \
  -e "LOCAL_PARITY_ADMIN_PATH=$(<"$run_abs/legacy-admin-path.txt")" \
  --entrypoint php legacy-oracle /opt/local-parity/init-legacy-oracle.php >"$out_abs/init.json"
"${c[@]}" up -d --wait legacy-oracle

readonly readiness_seconds=60
deadline=$((SECONDS + readiness_seconds))
attempt=0
ready=0
: >"$out_abs/probe.txt"
while (( SECONDS < deadline )); do
  attempt=$((attempt + 1))
  remaining=$((deadline - SECONDS))
  request_limit=5
  (( remaining < request_limit )) && request_limit="$remaining"
  (( request_limit >= 1 )) || break
  printf 'attempt=%s time=%s\n' "$attempt" "$(date -u +%FT%TZ)" >>"$out_abs/probe.txt"

  set +e
  outer_metrics="$(curl --silent --show-error --connect-timeout "$request_limit" --max-time "$request_limit" \
    --output /dev/null --write-out '%{http_code},%{size_download}' "http://127.0.0.1:$port/" 2>>"$out_abs/probe.txt")"
  outer_rc=$?
  set -e
  [[ "$outer_metrics" =~ ^[0-9]{3},[0-9]+$ ]] || outer_metrics='000,0'
  outer_http="${outer_metrics%%,*}"
  printf 'outer=%s,%s\n' "$outer_rc" "$outer_metrics" >>"$out_abs/probe.txt"

  if [[ "$outer_rc" -eq 0 && "$outer_http" == 200 ]]; then
    ready=1
    break
  fi
  remaining=$((deadline - SECONDS))
  (( remaining > 0 )) || break
  sleep_seconds=5
  (( remaining < sleep_seconds )) && sleep_seconds="$remaining"
  sleep "$sleep_seconds"
done

{
  printf 'diagnostics time=%s\n' "$(date -u +%FT%TZ)"
  timeout --signal=TERM --kill-after=1s 4s "${c[@]}" exec -T legacy-oracle supervisorctl status || true
  timeout --signal=TERM --kill-after=1s 4s "${c[@]}" exec -T legacy-oracle sh -c 'ps -o pid,stat,comm; ss -ltn 2>/dev/null || netstat -ltn 2>/dev/null || true' || true
} >>"$out_abs/probe.txt" 2>&1
set +e
# This in-container probe is diagnostic only; it never changes the readiness decision.
inner_metrics="$(timeout --signal=TERM --kill-after=1s 7s "${c[@]}" exec -T legacy-oracle sh -c \
  "curl -sS --connect-timeout 5 --max-time 5 -o /dev/null -w '%{http_code},%{size_download}' http://127.0.0.1:7002/" 2>>"$out_abs/probe.txt")"
inner_rc=$?
set -e
[[ "$inner_metrics" =~ ^[0-9]{3},[0-9]+$ ]] || inner_metrics='000,0'
printf 'inner=%s,%s\n' "$inner_rc" "$inner_metrics" >>"$out_abs/probe.txt"

set +e
logs="$(timeout --signal=TERM --kill-after=1s 5s "${c[@]}" logs --no-color --tail=250 legacy-oracle 2>&1)"
logs_rc=$?
set -e
printf 'container_logs_exit=%s\n' "$logs_rc" >>"$out_abs/probe.txt"
if [[ "$logs_rc" -ne 0 ]]; then
  logs=''
fi
exception_category="$(printf '%s\n' "$logs" | grep -Eo '[A-Za-z_\\][A-Za-z0-9_\\]*(Exception|Error)|Fatal error' | tail -1 || true)"
stack_location="$(printf '%s\n' "$logs" | grep -Eo '/www/(app|vendor)/[^[:space:]:]+:[0-9]+' | head -1 || true)"
[[ -n "$exception_category" ]] || exception_category=unavailable
[[ -n "$stack_location" ]] || stack_location=unavailable
jq -n \
  --arg source container-stdout \
  --arg exception_category "$exception_category" \
  --arg stack_location "$stack_location" \
  '{source:$source,exception_category:$exception_category,stack_location:$stack_location}' >"$out_abs/redacted.json"
jq -e '
  type == "object" and
  (.source | type == "string") and
  (.exception_category | type == "string") and
  (.stack_location | type == "string") and
  (keys | sort == ["exception_category","source","stack_location"])
' "$out_abs/redacted.json" >/dev/null

if [[ "$ready" -ne 1 ]]; then
  echo "legacy Oracle root did not return HTTP 200 within ${readiness_seconds}s" >&2
  exit 1
fi
if [[ "$logs_rc" -ne 0 ]]; then
  echo "bounded container log collection failed with exit ${logs_rc}" >&2
  exit 1
fi
