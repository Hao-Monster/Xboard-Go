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
# Narrow recovery of the interrupted, pre-migration schema-68 rollout. The
# previous image/configuration and schema must all still match; no data restore.
if [[ "$(docker inspect "$container" --format '{{.State.Status}}')" == exited ]]; then
  [[ "$old_revision" == 646878f202eb5e66bffdbde35f55771aca149d2f && "$revision" == bf4ef28e19d139904c2b0c3bd59a17b61dc8b895 ]] || fail 'Stopped update requires explicit recovery review'
  docker inspect "$container" caddy > "$work/interrupted-containers.json"
  docker network inspect "${project}_default" > "$work/interrupted-network.json"
  docker volume inspect "${project}_data" > "$work/interrupted-volume.json"
  python3 - "$work/interrupted-containers.json" "$work/interrupted-network.json" "$work/interrupted-volume.json" "$directory/.env" "$old_revision" "$project" <<'PYINTERRUPTED'
import json,sqlite3,sys
from pathlib import Path
app,proxy=json.load(open(sys.argv[1])); network,=json.load(open(sys.argv[2])); volume,=json.load(open(sys.argv[3]))
revision,project=sys.argv[5:7]; name=project+'_default'
assert app['State']['Status']=='exited' and not app['State']['Running'] and proxy['State']['Running']
assert app['Config']['Labels']['com.docker.compose.project']==project
assert app['Config']['Labels']['org.opencontainers.image.revision']==revision
assert name not in proxy['NetworkSettings']['Networks'] and set(app['NetworkSettings']['Networks'])=={name}
assert network['Name']==name and network['Labels']['com.docker.compose.project']==project
assert set(network['Containers']) <= {app['Id']}, 'Unexpected network member'
assert volume['Name']==project+'_data' and volume['Driver']=='local' and not volume.get('Options')
assert volume['Labels']['com.docker.compose.project']==project
mount,=[m for m in app['Mounts'] if m['Destination']=='/var/lib/xboard']
assert mount['Type']=='volume' and mount['Name']==volume['Name'] and mount['Source']==volume['Mountpoint']
root=Path(volume['Mountpoint']); database=root/'xboard.db'
assert root.is_absolute() and root.resolve()==root and not database.is_symlink() and database.is_file()
lines=Path(sys.argv[4]).read_text().splitlines()
keys=[line.split('=',1)[0] for line in lines]
assert len(keys)==len(set(keys)), 'Duplicate interrupted configuration key'
for expected in ('XBOARD_IMAGE=xboard-go:'+revision,'COMPOSE_PROJECT_NAME='+project,'XBOARD_PORT=7080','XBOARD_BIND_ADDRESS=127.0.0.1','XBOARD_PANEL_URL=https://fast.hjy.ca:8443'):
    assert lines.count(expected)==1, 'Interrupted configuration changed'
with sqlite3.connect(database.as_uri()+'?mode=ro',uri=True) as db:
    assert db.execute('PRAGMA user_version').fetchone()[0]==67, 'Database already migrated; refuse automatic recovery'
PYINTERRUPTED
  curl -fLsS --proto '=https' --proto-redir '=https' --max-time 60 "https://github.com/Hao-Monster/Xboard-Go/releases/download/internal-$old_revision/compose.yaml" -o "$work/interrupted-compose.yaml"
  cmp "$work/interrupted-compose.yaml" "$directory/compose.yaml" || fail 'Interrupted Compose differs from published previous release'
  "${compose[@]}" up -d --no-build --no-deps --wait --wait-timeout 180 app </dev/null
  curl -fsS --max-time 15 http://127.0.0.1:7080/healthz > "$work/recovered-health.json"
  python3 -c 'import json,sys; assert json.load(open(sys.argv[1]))["data"]["status"]=="ok"' "$work/recovered-health.json"
  [[ "$(docker inspect "$container" --format '{{index .Config.Labels "org.opencontainers.image.revision"}}')" == "$old_revision" ]] || fail 'Recovery image revision mismatch'
  python3 - "$work/interrupted-volume.json" <<'PYRECOVERYSCHEMA'
import json,sqlite3,sys
from pathlib import Path
volume,=json.load(open(sys.argv[1]))
with sqlite3.connect((Path(volume['Mountpoint'])/'xboard.db').as_uri()+'?mode=ro',uri=True) as db:
    assert db.execute('PRAGMA user_version').fetchone()[0]==67, 'Unexpected recovered schema'
PYRECOVERYSCHEMA
  docker network connect "${project}_default" caddy </dev/null
  curl -fsS --retry 3 --max-time 15 "$origin/healthz" > /dev/null
  printf 'Recovered interrupted pre-migration update to verified previous image. Continuing requested update.\n'
fi
[[ "$(docker inspect "$container" --format '{{index .Config.Labels "com.docker.compose.project"}} {{.State.Health.Status}} {{.State.Running}}')" == "$project healthy true" ]] || fail 'Expected healthy running Xboard instance missing'
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
# Inspect only the dedicated Xboard resources before interrupting traffic.
network="${project}_default"
docker inspect "$container" caddy > "$work/containers.json"
docker network inspect "$network" > "$work/network.json"
docker volume inspect "${project}_data" > "$work/volume.json"
python3 - "$work/containers.json" "$work/network.json" "$work/volume.json" "$project" "$work/data-root" "$work/proxy-aliases" <<'PYBOUNDARY'
import json,os,stat,sys
from pathlib import Path
app,proxy=json.load(open(sys.argv[1])); network,=json.load(open(sys.argv[2])); volume,=json.load(open(sys.argv[3])); project=sys.argv[4]
name=project+'_default'
assert network['Name']==name and network['Driver']=='bridge' and network['Scope']=='local'
assert network['Labels']['com.docker.compose.project']==project and network['Labels']['com.docker.compose.network']=='default'
assert set(network['Containers'])=={app['Id'],proxy['Id']}, 'Shared Xboard network'
assert set(app['NetworkSettings']['Networks'])=={name}, 'Unexpected application network'
endpoint=proxy['NetworkSettings']['Networks'][name]
assert not endpoint.get('IPAMConfig') and not endpoint.get('DriverOpts'), 'Unsupported proxy endpoint settings'
assert all(p['HostIp']=='127.0.0.1' for ports in app['NetworkSettings']['Ports'].values() for p in (ports or [])), 'Public app port bypasses isolation'
env=dict(value.split('=',1) for value in app['Config']['Env'])
assert env['XBOARD_DATABASE_DSN']=='file:/var/lib/xboard/xboard.db' and env['XBOARD_ATTACHMENT_ROOT']=='/var/lib/xboard/knowledge-attachments'
assert volume['Name']==project+'_data' and volume['Driver']=='local' and volume['Scope']=='local' and not volume.get('Options')
assert volume['Labels']['com.docker.compose.project']==project and volume['Labels']['com.docker.compose.volume']=='data'
mount,=[m for m in app['Mounts'] if m['Destination']=='/var/lib/xboard']
assert mount['Type']=='volume' and mount['Name']==volume['Name'] and mount['RW'] and mount['Source']==volume['Mountpoint']
root=Path(volume['Mountpoint']); assert root.is_absolute() and root.resolve()==root and root.is_dir()
assert os.geteuid()==0 and os.access(root,os.R_OK|os.W_OK|os.X_OK), 'Root data access required'
assert not root.stat().st_mode & stat.S_IWOTH, 'World-writable data root'
for name in ('xboard.db','xboard.db-wal','xboard.db-shm','knowledge-attachments'):
    path=root/name
    assert not path.is_symlink() and path.resolve().parent==root, 'Unsafe data path'
assert (root/'xboard.db').is_file() and (root/'knowledge-attachments').is_dir()
Path(sys.argv[5]).write_text(str(root))
aliases=endpoint.get('Aliases') or []
assert all(isinstance(a,str) and a and '\n' not in a for a in aliases)
Path(sys.argv[6]).write_text('\n'.join(aliases))
PYBOUNDARY
data_root="$(cat "$work/data-root")"
[[ "$(docker ps -aq --no-trunc --filter "volume=${project}_data")" == "$(docker inspect "$container" --format '{{.Id}}')" ]] || fail 'Data volume is shared'
backup="/var/lib/xboard-backups/pre-update-${revision}-$(date -u +%Y%m%dT%H%M%SZ).xbbackup"
recovery="recover-${revision}-$(date -u +%Y%m%dT%H%M%SZ)"
cp -p "$directory/.env" "$work/env.before"
# Explicit old-image runner also works after the application container has stopped.
old_compose=(docker compose -p "$project" --env-file "$work/env.before" --project-directory "$directory" -f "$work/compose.previous")
isolated=0; database_changed=0; public_open=0
reconnect_proxy() {
  local aliases=() alias
  while IFS= read -r alias || [[ -n "$alias" ]]; do aliases+=(--alias "$alias"); done < "$work/proxy-aliases"
  docker network connect "${aliases[@]}" "$network" caddy
}
restore_data() {
  "${compose[@]}" stop app || return 1
  [[ ! -e "$data_root/$recovery" && ! -L "$data_root/$recovery" ]] || return 1
  install -d -m 700 -o "$(stat -c %u "$data_root/xboard.db")" -g "$(stat -c %g "$data_root/xboard.db")" "$data_root/$recovery" || return 1
  "${old_compose[@]}" run --interactive=false --rm --no-deps --entrypoint /xboard app backup restore --input "$backup" --output "/var/lib/xboard/$recovery/xboard.db" --attachment-output "/var/lib/xboard/$recovery/knowledge-attachments" < /dev/null || return 1
  python3 - "$data_root" "$recovery" <<'PYPROMOTE'
import os,stat,sys
from pathlib import Path
root=Path(sys.argv[1]); stage=root/sys.argv[2]; failed=root/(sys.argv[2]+'-failed')
assert root.is_absolute() and root.resolve()==root and stage.resolve().parent==root and not stage.is_symlink()
assert not failed.exists() and not failed.is_symlink()
for name in ('xboard.db','xboard.db-wal','xboard.db-shm','knowledge-attachments'):
    for path in (root/name,stage/name):
        assert not path.is_symlink() and path.resolve().parent in (root,stage), 'Unsafe promotion path'
assert (stage/'xboard.db').is_file() and (stage/'knowledge-attachments').is_dir()
failed.mkdir(mode=0o700)
# Preserve the failed migrated database and all SQLite sidecars; never overwrite them.
for name in ('xboard.db','xboard.db-wal','xboard.db-shm','knowledge-attachments'):
    path=root/name
    if path.exists(): path.rename(failed/name)
for name in ('xboard.db','knowledge-attachments'):
    (stage/name).rename(root/name)
fd=os.open(root,os.O_RDONLY|os.O_DIRECTORY)
try: os.fsync(fd)
finally: os.close(fd)
PYPROMOTE
}
rollback() {
  trap - ERR
  install -m 600 "$work/update.log" "$directory/update.failure.log" 2>/dev/null || true
  if (( public_open )); then
    printf 'Post-cutover verification failed; new image and data retained. No backup restoration after public traffic resumed. Manual investigation required.\n' >&2
    exit 1
  fi
  if (( database_changed )) && ! restore_data > "$work/rollback.log" 2>&1; then
    install -m 600 "$work/rollback.log" "$directory/update-rollback.failure.log"
    printf 'Data recovery failed; Xboard remains isolated and stopped. Backup and migrated data retained; manual recovery required.\n' >&2
    exit 1
  fi
  install -m 600 "$work/env.before" "$directory/.env" || exit 1
  install -m 600 "$work/compose.previous" "$directory/compose.yaml" || exit 1
  if ! "${compose[@]}" up -d --no-build --no-deps --wait --wait-timeout 180 app > "$work/rollback-start.log" 2>&1; then
    install -m 600 "$work/rollback-start.log" "$directory/update-rollback.failure.log"
    printf 'Old image restart failed; Xboard remains isolated. Manual recovery required.\n' >&2
    exit 1
  fi
  if (( isolated )); then reconnect_proxy || exit 1; fi
  printf 'Update failed; previous Xboard image restored. Backup: %s\n' "$backup" >&2
  if (( database_changed )); then printf 'Compatible data restored; failed data retained under %s/%s-failed\n' "$data_root" "$recovery" >&2; fi
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
interrupted() {
  trap - ERR INT TERM HUP
  printf 'Deployment interrupted; no automatic data restoration. Inspect Xboard image, data and proxy isolation before recovery.\n' >&2
  exit 1
}
trap interrupted INT TERM HUP
trap rollback ERR
: > "$work/update.log"
docker network disconnect "$network" caddy
isolated=1
"${compose[@]}" stop app >> "$work/update.log" 2>&1
"${old_compose[@]}" run --interactive=false --rm --no-deps --entrypoint /xboard app backup create --output "$backup" < /dev/null > "$work/backup.json"
"${old_compose[@]}" run --interactive=false --rm --no-deps --entrypoint /xboard app backup verify --input "$backup" < /dev/null > /dev/null
install -m 600 "$work/env.next" "$directory/.env"
install -m 600 "$work/compose.next" "$directory/compose.yaml"
database_changed=1
"${compose[@]}" up -d --no-build --no-deps --wait --wait-timeout 180 app >> "$work/update.log" 2>&1
curl -fsS --max-time 15 http://127.0.0.1:7080/healthz > "$work/local-health.json"
python3 -c 'import json,sys; assert json.load(open(sys.argv[1]))["data"]["status"]=="ok"' "$work/local-health.json"
[[ "$(docker inspect "$container" --format '{{index .Config.Labels "org.opencontainers.image.revision"}}')" == "$revision" ]] || fail 'Running image mismatch before cutover'
# Mark before reconnect: even an ambiguous reconnect failure must never discard newly accepted writes.
public_open=1
reconnect_proxy
isolated=0
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

printf "XBOARD_DEPLOYMENT_COMPLETE:%s\n" "$revision"
