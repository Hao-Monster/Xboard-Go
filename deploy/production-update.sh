#!/usr/bin/env bash
# CI-only image update of the known Xboard instance; existing proxy and other services stay intact.
set -Eeuo pipefail
umask 077
mode="$1"; revision="$2"; origin="$3"; archive_digest="$7"; compose_digest="$6"
node_version="${8:-}"; node_source="${9:-preserve}"
directory=/opt/xboard-go
project=xboard-production-internal
container="${project}-app-1"
fail() { printf 'ERROR: %s\n' "$*" >&2; return 1; }
[[ ( "$node_source" == preserve && -z "$node_version" ) || ( "$node_source" =~ ^(github|panel)$ && "$node_version" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ) ]] || fail 'Invalid Node selection'
[[ "$mode" == update && "$revision" =~ ^[a-f0-9]{40}$ && "$archive_digest" =~ ^[a-f0-9]{64}$ ]] || fail 'Invalid update identity'
[[ "$(hostname)" == vmi3574179 && "$origin" == https://fast.hjy.ca:8443 ]] || fail 'Unexpected target'
[[ "$compose_digest" =~ ^[a-f0-9]{64}$ ]] || fail 'Invalid Compose digest'
[[ -f "$directory/compose.yaml" && ! -L "$directory/compose.yaml" ]] || fail 'Unexpected Compose path'
[[ -d "$directory" && ! -L "$directory" && -f "$directory/.env" && ! -L "$directory/.env" ]] || fail 'Expected installation missing'
exec 9>"$directory/.deployment.lock"
flock -x 9
[[ "$(docker inspect "$container" --format '{{index .Config.Labels "com.docker.compose.project"}} {{.State.Health.Status}}')" == "$project healthy" ]] || fail 'Expected healthy Xboard instance missing'
old_revision="$(docker inspect "$container" --format '{{index .Config.Labels "org.opencontainers.image.revision"}}')"
[[ "$old_revision" =~ ^[a-f0-9]{40}$ && "$old_revision" != "$revision" ]] || fail 'Invalid or already installed revision'
snapshot() { docker inspect remnawave remnanode remnawave-db remnawave-redis caddy --format '{{.Name}} {{.Id}} {{.State.StartedAt}} {{.State.Running}}'; }
baseline="$(snapshot)"
[[ "$baseline" != *' false'* ]] || fail 'Protected service is not running'
compose=(docker compose -p "$project" --env-file "$directory/.env" --project-directory "$directory" -f "$directory/compose.yaml")
if [[ -f "$directory/compose.observability.yaml" ]]; then
  [[ ! -L "$directory/compose.observability.yaml" ]] || fail 'Unexpected logging overlay path'
  compose+=(-f "$directory/compose.observability.yaml")
fi
work="$(mktemp -d)"
trap 'rm -rf -- "$work"' EXIT
if [[ "$node_source" != preserve ]]; then
  selected_base="$origin/api/v2/node/releases/$node_version"
  selected_metadata="$selected_base"
  if [[ "$node_source" == github ]]; then
    selected_base="https://github.com/Hao-Monster/Xboard-Go/releases/download/node-$node_version"
    selected_metadata="$selected_base/release.json"
  fi
  mkdir "$work/selected-node"
  curl --http1.1 -fsSL --proto '=https' --proto-redir '=https' --connect-timeout 10 --max-time 60 --retry 2 "$selected_metadata" -o "$work/selected-node/release.json"
  python3 - "$work/selected-node/release.json" "$node_version" "$selected_base" <<'PYNODE'
import json,sys
r=json.load(open(sys.argv[1])); assert r['tag_name']==sys.argv[2] and not r.get('draft',False)
required={'install.sh','SHA256SUMS','xboard-node-linux-amd64','xboard-node-linux-arm64','xbctl-linux-amd64','xbctl-linux-arm64'}
assets={a['name']:a for a in r['assets']}
assert required <= assets.keys()
assert all(assets[n]['url']==sys.argv[3]+'/'+n and assets[n]['size']>0 and assets[n]['state']=='uploaded' for n in required)
PYNODE
  for file in install.sh SHA256SUMS; do
    curl --http1.1 -fsSL --proto '=https' --proto-redir '=https' --connect-timeout 10 --max-time 60 --retry 2 "$selected_base/$file" -o "$work/selected-node/$file"
  done
  (cd "$work/selected-node" && grep ' install.sh$' SHA256SUMS | sha256sum -c -)
fi
base="https://github.com/Hao-Monster/Xboard-Go/releases/download/internal-$revision"
curl -fLsS --proto '=https' --proto-redir '=https' --max-time 60 "https://github.com/Hao-Monster/Xboard-Go/releases/download/internal-$old_revision/compose.yaml" -o "$work/compose.previous"
cmp "$work/compose.previous" "$directory/compose.yaml" || fail 'Existing Compose differs from its published release'
curl -fLsS --proto '=https' --proto-redir '=https' --max-time 60 "$base/compose.yaml" -o "$work/compose.next"
printf '%s  %s\n' "$compose_digest" "$work/compose.next" | sha256sum --check --status
curl -fLsS --retry 3 "$base/xboard-go-linux-amd64.tar.gz" -o "$work/image.tar.gz"
printf '%s  %s\n' "$archive_digest" "$work/image.tar.gz" | sha256sum --check --status
docker load -i "$work/image.tar.gz" > "$work/load.log"
[[ "$(docker image inspect "xboard-go:$revision" --format '{{index .Config.Labels "org.opencontainers.image.revision"}}')" == "$revision" ]] || fail 'Image revision mismatch'
backup="/var/lib/xboard-backups/pre-update-$(date -u +%Y%m%dT%H%M%SZ).xbbackup"
docker exec "$container" /xboard backup create --output "$backup" > "$work/backup.json"
docker exec "$container" /xboard backup verify --input "$backup" > /dev/null
cp -p "$directory/.env" "$work/env.before"
rollback() {
  trap - ERR
  install -m 600 "$work/env.before" "$directory/.env"
  install -m 600 "$work/compose.previous" "$directory/compose.yaml"
  if "${compose[@]}" up -d --no-build --no-deps --wait --wait-timeout 180 app > "$work/rollback.log" 2>&1; then
    printf 'Update failed; previous Xboard image restored. Backup: %s\n' "$backup" >&2
  else
    install -m 600 "$work/rollback.log" "$directory/update-rollback.failure.log"
    printf 'Update and rollback failed; inspect the private rollback log.\n' >&2
  fi
  install -m 600 "$work/update.log" "$directory/update.failure.log" 2>/dev/null || true
  exit 1
}
python3 - "$directory/.env" "$old_revision" "$revision" "$origin" "$work/env.next" "$node_version" "$node_source" <<'PY'
from pathlib import Path
import re,sys
p=Path(sys.argv[1]); old,new,origin,output=sys.argv[2:6]; s=p.read_text(); lines=s.splitlines()
version,source = sys.argv[6:8] if len(sys.argv) > 6 else ('','preserve')
assert lines.count('XBOARD_IMAGE=xboard-go:'+old)==1, 'Unexpected previous image configuration'
assert lines.count('XBOARD_PANEL_URL='+origin)==1, 'Unexpected origin configuration'
for expected in ('COMPOSE_PROJECT_NAME=xboard-production-internal','XBOARD_PORT=7080','XBOARD_BIND_ADDRESS=127.0.0.1'):
    assert lines.count(expected)==1, 'Unexpected project or port configuration'
keys=[line.split('=',1)[0] for line in lines]
assert len(keys)==len(set(keys)), 'Duplicate configuration key'
lines=['XBOARD_IMAGE=xboard-go:'+new if line=='XBOARD_IMAGE=xboard-go:'+old else line for line in lines]
if source != 'preserve' or version:
    assert source in ('github','panel') and re.fullmatch(r'v[0-9]+\.[0-9]+\.[0-9]+',version), 'Invalid Node selection'
    lines=[line for line in lines if line.split('=',1)[0] not in ('XBOARD_NODE_RELEASE','XBOARD_NODE_RELEASE_SOURCE')]
    lines += ['XBOARD_NODE_RELEASE='+version,'XBOARD_NODE_RELEASE_SOURCE='+source]
Path(output).write_text('\n'.join(lines)+'\n')
PY
trap rollback ERR
install -m 600 "$work/env.next" "$directory/.env"
install -m 600 "$work/compose.next" "$directory/compose.yaml"
"${compose[@]}" up -d --no-build --no-deps --wait --wait-timeout 180 app > "$work/update.log" 2>&1
curl -fsS --retry 6 --retry-all-errors --retry-delay 3 --max-time 15 "$origin/healthz" > "$work/health.json"
curl -fsS --max-time 30 "$origin/api/v2/node/releases/v1.14.4" > "$work/release.json"
python3 - "$work/health.json" "$work/release.json" "$origin" <<'PY'
import json,sys
assert json.load(open(sys.argv[1]))['data']['status']=='ok'
r=json.load(open(sys.argv[2])); assert r['tag_name']=='v1.14.4'
assert {'install.sh','SHA256SUMS','xboard-node-linux-amd64','xboard-node-linux-arm64','xbctl-linux-amd64','xbctl-linux-arm64'} <= {a['name'] for a in r['assets']}
assert all(a['url'].startswith(sys.argv[3]+'/api/v2/node/releases/v1.14.4/') for a in r['assets'])
PY
for file in install.sh SHA256SUMS; do curl -fsS --max-time 30 "$origin/api/v2/node/releases/v1.14.4/$file" -o "$work/$file"; done
(cd "$work" && grep ' install.sh$' SHA256SUMS | sha256sum -c -)
[[ "$(docker inspect "$container" --format '{{index .Config.Labels "org.opencontainers.image.revision"}}')" == "$revision" ]] || fail 'Running image mismatch'
if [[ "$node_source" != preserve ]]; then
  docker inspect "$container" --format '{{json .Config.Env}}' | python3 -c 'import json,sys; e=json.load(sys.stdin); assert "XBOARD_NODE_RELEASE="+sys.argv[1] in e; assert "XBOARD_NODE_RELEASE_SOURCE="+sys.argv[2] in e' "$node_version" "$node_source"
fi
[[ "$(snapshot)" == "$baseline" ]] || fail 'Protected service changed'
trap - ERR
install -m 600 "$work/env.before" "$directory/.env.before-$revision"
install -m 600 "$work/compose.previous" "$directory/compose.before-$revision.yaml"
printf 'Updated Xboard to %s. HTTPS and node release v1.14.4 verified; protected containers unchanged. Previous revision: %s; backup: %s\n' "$revision" "$old_revision" "$backup"
printf 'Node source selection: %s; version: %s (empty means preserved).\n' "$node_source" "$node_version"
