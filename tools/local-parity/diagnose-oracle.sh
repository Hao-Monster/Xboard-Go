#!/usr/bin/env bash
set -u -o pipefail
# Bounded, no-Go Oracle diagnostic; run only from a WSL-native clean clone.
# It retains redacted process/listener/HTTP state before cleanup.
set -e
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"; cd "$root"
id="${LOCAL_PARITY_RUN_ID:?set run id}"; port="${XBOARD_LEGACY_PORT:?set legacy port}"
run=".local/local-parity-$id"; out="${LOCAL_PARITY_DIAGNOSTIC_OUTPUT:?set stable output dir}"; project="xboard-oracle-$id"
mkdir -p "$out"; exec > >(tee "$out/driver.log") 2>&1
cleanup() { rc=$?; printf 'driver_exit=%s\n' "$rc" >"$out/exit.txt"; docker compose -p "$project" -f compose.local.yaml -f tools/local-parity/compose.user-parity.yaml --profile e2e down --volumes --remove-orphans >>"$out/cleanup.log" 2>&1 || true; rm -rf -- "$run"; exit "$rc"; }; trap cleanup EXIT
bash tools/local-parity/run.sh prepare
source "$run/runtime.env"; export LOCAL_PARITY_RUN_DIR="$run" LOCAL_LEGACY_APP_KEY="$(<"$run/local-legacy-app-key.txt")"
c=(docker compose -p "$project" -f compose.local.yaml -f tools/local-parity/compose.user-parity.yaml --profile e2e)
"${c[@]}" up -d --wait legacy-redis
"${c[@]}" run --rm --no-deps --entrypoint php legacy-oracle /www/artisan migrate --force --no-interaction >"$out/migrate.log"
"${c[@]}" run --rm --no-deps -e "LOCAL_PARITY_ADMIN_EMAIL=$(<"$run/legacy-admin-email.txt")" -e "LOCAL_PARITY_ADMIN_PASSWORD=$(<"$run/legacy-admin-password.txt")" -e "LOCAL_PARITY_ADMIN_PATH=$(<"$run/legacy-admin-path.txt")" --entrypoint php legacy-oracle /opt/local-parity/init-legacy-oracle.php >"$out/init.json"
"${c[@]}" up -d --wait legacy-oracle
for n in 1 2 3; do
  { printf 'attempt=%s time=%s\n' "$n" "$(date -u +%FT%TZ)"; "${c[@]}" exec -T legacy-oracle supervisorctl status || true; "${c[@]}" exec -T legacy-oracle sh -c 'ps -o pid,stat,args; ss -ltn 2>/dev/null || netstat -ltn 2>/dev/null || true'; printf 'outer='; curl -sS -m 5 -o /dev/null -w '%{exitcode},%{http_code},%{size_download}\n' "http://127.0.0.1:$port/" || true; printf 'inner='; "${c[@]}" exec -T legacy-oracle sh -c "curl -sS -m 5 -o /dev/null -w '%{exitcode},%{http_code},%{size_download}\\n' http://127.0.0.1:7002/" || true; } >>"$out/probe.txt" 2>&1
  grep -q 'outer=0,200' "$out/probe.txt" && break; [ "$n" = 3 ] || sleep 5
done
logs="$("${c[@]}" logs --no-color --tail=250 legacy-oracle || true)"; cat >"$out/redacted.json" <<EOF
{"source":"container-stdout","exception_category":"$(printf '%s\n' "$logs" | grep -Eo '[A-Za-z_\\][A-Za-z0-9_\\]*(Exception|Error)|Fatal error' | tail -1 || echo unavailable)","stack_location":"$(printf '%s\n' "$logs" | grep -Eo '/www/(app|vendor)/[^[:space:]:]+:[0-9]+' | head -1 || echo unavailable)"}
EOF
