#!/usr/bin/env bash
# Invoked only by the reviewed GitHub workflow. No updates or takeover of an existing installation.
set -Eeuo pipefail
umask 077
mode="$1"; revision="$2"; origin="$3"; email="$4"; installer_digest="$5"; compose_digest="$6"
directory=/opt/xboard-go
project=xboard-production-internal
caddy_file=/opt/remnawave/caddy/Caddyfile
fail() { printf 'ERROR: %s\n' "$*" >&2; return 1; }
[[ "$mode" == inspect || "$mode" == install || "$mode" == resume ]] || fail 'Invalid mode'
[[ "$revision" =~ ^[0-9a-f]{40}$ && "$installer_digest" =~ ^[0-9a-f]{64}$ && "$compose_digest" =~ ^[0-9a-f]{64}$ ]] || fail 'Invalid release identity'
[[ "$origin" =~ ^https://[a-zA-Z0-9.-]+(:8443)?$ ]] || fail 'Invalid HTTPS origin'
[[ "$email" =~ ^[a-zA-Z0-9._+%-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}$ ]] || fail 'Invalid administrator email'
[[ "$(hostname)" == vmi3574179 ]] || fail 'Unexpected server identity'
[[ "$(uname -m)" == x86_64 ]] || fail 'Unsupported architecture'
for tool in docker curl python3 openssl sha256sum ss; do command -v "$tool" >/dev/null || fail "Missing prerequisite: $tool"; done
docker compose version >/dev/null
if [[ "$mode" == resume || ( "$mode" == inspect && -d "$directory" ) ]]; then
  [[ -d "$directory" && ! -L "$directory" && -f "$directory/.env" && ! -L "$directory/.env" ]] || fail 'Expected retained Xboard installation missing'
  [[ "$(docker inspect "${project}-app-1" --format '{{index .Config.Labels "com.docker.compose.project"}} {{index .Config.Labels "org.opencontainers.image.revision"}} {{.State.Running}}')" == "$project $revision false" ]] || fail 'Resume requires the exact stopped Xboard container from the failed installation'
  [[ "$(docker inspect caddy --format '{{json .NetworkSettings.Networks}}')" != *"${project}_default"* ]] || fail 'Xboard route/network rollback is incomplete'
else
  [[ ! -e "$directory" && ! -L "$directory" ]] || fail 'Existing Xboard path preserved; use reviewed resume mode only for a failed installation'
  [[ -z "$(docker ps -aq --filter "label=com.docker.compose.project=$project")" ]] || fail 'Compose project already exists'
  [[ -z "$(docker volume ls -q --filter "label=com.docker.compose.project=$project")" ]] || fail 'Existing project data preserved'
  [[ -z "$(docker network ls -q --filter "name=^${project}_default$")" ]] || fail 'Project network already exists'
fi
[[ -z "$(ss -H -ltn 'sport = :7080')" ]] || fail 'Port 7080 is already occupied'
[[ -f "$caddy_file" && ! -L "$caddy_file" ]] || fail 'Expected Caddy configuration not found'
host="${origin#https://}"
host="${host%:8443}"
python3 - "$caddy_file" "$host" <<'PY'
import pathlib,re,sys
config=pathlib.Path(sys.argv[1]).read_text()
if re.search(r'(?<![A-Za-z0-9.-])'+re.escape(sys.argv[2])+r'(?![A-Za-z0-9.-])',config):
 raise SystemExit('Domain already appears in Caddy configuration; refusing route takeover')
PY
snapshot() {
  docker inspect remnawave remnanode remnawave-db remnawave-redis caddy --format '{{.Name}} {{.Id}} {{.State.StartedAt}} {{.State.Running}}'
}
baseline="$(snapshot)"
[[ "$baseline" != *' false'* ]] || fail 'An existing protected service is not running'
[[ "$(docker inspect caddy --format '{{range .Mounts}}{{if eq .Destination "/etc/caddy/Caddyfile"}}{{.Source}}{{end}}{{end}}')" == "$caddy_file" ]] || fail 'Unexpected Caddy mount'
printf 'Preflight passed: server identity, dedicated path/project/port, and unused domain; protected services running.\n'
[[ "$mode" != inspect ]] || exit 0

work="$(mktemp -d)"
trap 'rm -rf -- "$work"' EXIT
cp -p "$caddy_file" "$work/Caddyfile.before"
network_added=false
route_added=false
restore_route() {
  # Keep the mounted file inode. Never roll back unrelated later edits.
  if [[ "$route_added" == true ]]; then
    python3 - "$caddy_file" <<'PY'
import pathlib,sys
p=pathlib.Path(sys.argv[1]);s=p.read_text()
begin='# BEGIN XBOARD PRODUCTION INTERNAL\n';end='# END XBOARD PRODUCTION INTERNAL\n'
if s.count(begin)!=1 or s.count(end)!=1: raise SystemExit('Route markers changed; manual route recovery required')
a=s.index(begin);b=s.index(end,a)+len(end)
p.write_text(s[:a]+s[b:])
PY
    docker exec caddy caddy reload --config /etc/caddy/Caddyfile > "$work/caddy-rollback.log" 2>&1 || true
  fi
}
rollback() {
  trap - ERR
  if [[ -d "$directory" && ! -L "$directory" ]]; then
    for log in install caddy-validate caddy-reload; do
      if [[ -f "$work/$log.log" ]]; then install -m 600 "$work/$log.log" "$directory/$log.failure.log"; fi
    done
  fi
  restore_route || true
  if [[ "$network_added" == true ]]; then docker network disconnect "${project}_default" caddy >/dev/null || true; fi
  if [[ -f "$directory/.env" ]]; then
    docker compose --env-file "$directory/.env" --project-directory "$directory" -f "$directory/compose.yaml" stop app >/dev/null || true
  fi
  printf 'Deployment failed. Xboard was stopped; its data and credentials were retained. Existing services were not restarted.\n' >&2
  exit 1
}
trap rollback ERR
base="https://github.com/Hao-Monster/Xboard-Go/releases/download/internal-$revision"
if [[ "$mode" == install ]]; then
curl --fail --location --silent --show-error --retry 3 "$base/install.sh" -o "$work/install.sh"
printf '%s  %s\n' "$installer_digest" "$work/install.sh" | sha256sum --check --status
XBOARD_INSTALL_DIR="$directory" XBOARD_PROJECT="$project" XBOARD_PORT=7080 \
  XBOARD_BIND_ADDRESS=127.0.0.1 XBOARD_PANEL_URL="$origin" XBOARD_ADMIN_EMAIL="$email" \
  XBOARD_RELEASE_TAG="internal-$revision" bash "$work/install.sh" > "$work/install.log" 2>&1
install -m 600 "$work/install.log" "$directory/installation.log"
else
  # Adopt only the exact failed CI installation. Preserve its database and generated secrets.
  curl --fail --location --silent --show-error --retry 3 "$base/compose.yaml" -o "$work/compose.yaml"
  printf '%s  %s\n' "$compose_digest" "$work/compose.yaml" | sha256sum --check --status
  cmp -s "$directory/compose.yaml" "$work/compose.yaml" || fail 'Retained Compose file differs from the verified release'
  python3 - "$directory/.env" "$project" "$revision" "$origin" "$email" <<'PYENV'
from pathlib import Path
import re,sys
p=Path(sys.argv[1]); project,revision,origin,email=sys.argv[2:]
s=p.read_text(); lines=s.splitlines()
values=dict(line.split('=',1) for line in lines)
expected={'COMPOSE_PROJECT_NAME':project,'XBOARD_IMAGE':'xboard-go:'+revision,'XBOARD_ADMIN_EMAIL':email,
          'XBOARD_PORT':'7080','XBOARD_BIND_ADDRESS':'127.0.0.1','XBOARD_COOKIE_SECURE':'true'}
assert len(lines)==len(values)==8 and set(values)==set(expected)|{'XBOARD_ADMIN_PATH','XBOARD_PANEL_URL'}, 'Unexpected retained configuration keys'
assert all(values[k]==v for k,v in expected.items()), 'Retained installation identity mismatch'
assert re.fullmatch('[a-f0-9]{48}',values['XBOARD_ADMIN_PATH']), 'Unexpected management path'
assert values['XBOARD_PANEL_URL'] in ('https://fast.hjy.ca','https://fast.hjy.ca:8443'), 'Unexpected previous origin'
p.with_name('.env.before-8443').write_text(s)
p.write_text('\n'.join('XBOARD_PANEL_URL='+origin if line.startswith('XBOARD_PANEL_URL=') else line for line in lines)+'\n')
PYENV
  docker compose --env-file "$directory/.env" --project-directory "$directory" -f "$directory/compose.yaml" up -d --no-build --wait --wait-timeout 180 > "$work/install.log" 2>&1
fi
install -m 600 "$work/Caddyfile.before" "$directory/Caddyfile.before-xboard"
curl --fail --silent --show-error http://127.0.0.1:7080/healthz >/dev/null
cmp -s "$caddy_file" "$work/Caddyfile.before" || fail 'Caddy configuration changed concurrently; preserving it'
docker network connect "${project}_default" caddy
network_added=true
route_added=true
printf '\n# BEGIN XBOARD PRODUCTION INTERNAL\n%s {\n    reverse_proxy %s-app-1:8080\n}\n# END XBOARD PRODUCTION INTERNAL\n' "https://$host" "$project" >> "$caddy_file"
docker exec caddy caddy validate --config /etc/caddy/Caddyfile > "$work/caddy-validate.log" 2>&1
docker exec caddy caddy reload --config /etc/caddy/Caddyfile > "$work/caddy-reload.log" 2>&1
# Caddy obtains a certificate through the existing port-80 listener. No process takes over port 443.
curl --fail --silent --show-error --retry 18 --retry-all-errors --retry-delay 5 --max-time 12 "$origin/healthz" > "$work/health.json"
python3 - "$work/health.json" <<'PY'
import json,sys
assert json.load(open(sys.argv[1]))['data']['status']=='ok', 'Unexpected public health response'
PY
curl --fail --silent --show-error --max-time 20 "$origin/" >/dev/null
[[ "$(docker inspect "${project}-app-1" --format '{{index .Config.Labels "org.opencontainers.image.revision"}}')" == "$revision" ]] || fail 'Image revision mismatch'
[[ "$(snapshot)" == "$baseline" ]] || fail 'Protected service identity or start time changed'
trap - ERR
printf 'Installed exact revision %s at %s. Public health passed. Remnawave container identities/start times unchanged.\n' "$revision" "$origin"
printf 'Administrator credentials and generated management URL remain only in /opt/xboard-go/.env, secrets/admin-password and installation.log.\n'
