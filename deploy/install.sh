#!/usr/bin/env bash
# Fresh installation only. Images are built and smoke-tested by GitHub Actions.
set -Eeuo pipefail
umask 077
fail() { printf 'ERROR: %s\n' "$*" >&2; exit 1; }
for command in docker curl python3 openssl sha256sum; do command -v "$command" >/dev/null || fail "Required command missing: $command"; done
[[ "$(uname -s)" == Linux && "$(uname -m)" == x86_64 ]] || fail 'This release supports Linux amd64.'
docker info >/dev/null 2>&1 || fail 'Docker is not running or the current user cannot access it.'
docker compose version >/dev/null || fail 'Docker Compose v2 is required.'
directory="${XBOARD_INSTALL_DIR:-/opt/xboard-go}"
project="${XBOARD_PROJECT:-xboard-go}"
port="${XBOARD_PORT:-7080}"
bind="${XBOARD_BIND_ADDRESS:-0.0.0.0}"
url="${XBOARD_PANEL_URL:-}"
email="${XBOARD_ADMIN_EMAIL:-}"
[[ "$directory" == /* && "$directory" != / && "$directory" != *$'\n'* ]] || fail 'Installation directory must be an absolute non-root path.'
[[ "$project" =~ ^[a-z][a-z0-9-]{1,40}$ ]] || fail 'Invalid Compose project name.'
[[ "$port" =~ ^[0-9]{1,5}$ ]] && ((10#$port >= 1 && 10#$port <= 65535)) || fail 'Invalid port.'
[[ "$bind" == 0.0.0.0 || "$bind" == 127.0.0.1 ]] || fail 'Bind address must be 0.0.0.0 or 127.0.0.1.'
if [[ -z "$url" ]]; then read -r -p 'Panel URL, e.g. http://SERVER_IP:7080: ' url </dev/tty; fi
if [[ -z "$email" ]]; then read -r -p 'Administrator email: ' email </dev/tty; fi
python3 - "$url" "$email" <<'PY'
import re,sys,urllib.parse
url,email=sys.argv[1:]
u=urllib.parse.urlsplit(url)
if not re.fullmatch(r'https?://[A-Za-z0-9.:-]+',url) or not u.hostname or u.username or u.password or u.path or u.query or u.fragment:
 sys.exit('Panel URL must be an http(s) origin without a path, credentials or query.')
try: u.port
except ValueError: sys.exit('Invalid URL port.')
if not re.fullmatch(r'[A-Za-z0-9._+%-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}',email): sys.exit('Invalid administrator email.')
PY
[[ ! -e "$directory" && ! -L "$directory" ]] || fail "Existing installation path preserved: $directory. This installer never overwrites an installation."
[[ -z "$(docker ps -aq --filter "label=com.docker.compose.project=$project")" ]] || fail 'Compose project already exists; choose another project.'
[[ -z "$(docker volume ls -q --filter "label=com.docker.compose.project=$project")" ]] || fail 'Project data volumes already exist; refusing to adopt existing data.'
temp="$(mktemp -d)"
trap 'rm -rf -- "$temp"' EXIT
revision="${XBOARD_REVISION:-}"
archive="${XBOARD_IMAGE_ARCHIVE:-}"
if [[ -n "$archive" ]]; then
  # Used by isolated CI smoke tests; operators normally download a CI release.
  [[ "$revision" =~ ^[0-9a-f]{40}$ ]] || fail 'Local archive requires an exact revision.'
  cp -- "$(dirname "${BASH_SOURCE[0]}")/compose.yaml" "$temp/compose.yaml"
else
  tag="${XBOARD_RELEASE_TAG:-}"
  if [[ -z "$tag" ]]; then
    curl --fail --silent --show-error --retry 3 https://api.github.com/repos/Hao-Monster/Xboard-Go/releases?per_page=100 > "$temp/releases.json"
    tag="$(python3 - "$temp/releases.json" <<'PY'
import json,re,sys
for release in json.load(open(sys.argv[1])):
 if not release['draft'] and re.fullmatch(r'internal-[0-9a-f]{40}',release['tag_name']):
  print(release['tag_name']); break
else: sys.exit('No published internal-test image is available.')
PY
)"
  fi
  [[ "$tag" =~ ^internal-[0-9a-f]{40}$ ]] || fail 'Invalid release tag.'
  revision="${tag#internal-}"
  base="https://github.com/Hao-Monster/Xboard-Go/releases/download/$tag"
  for file in SHA256SUMS compose.yaml xboard-go-linux-amd64.tar.gz; do
    curl --fail --location --silent --show-error --retry 3 "$base/$file" --output "$temp/$file"
  done
  (cd "$temp" && sha256sum --check --strict SHA256SUMS) || fail 'Release checksum verification failed.'
  archive="$temp/xboard-go-linux-amd64.tar.gz"
fi
image="xboard-go:$revision"
docker load --input "$archive" >/dev/null
[[ "$(docker image inspect "$image" --format '{{index .Config.Labels "org.opencontainers.image.revision"}}')" == "$revision" ]] || fail 'Loaded image revision does not match release.'
mkdir -m 700 -- "$directory"
install -m 600 "$temp/compose.yaml" "$directory/compose.yaml"
mkdir -m 700 "$directory/secrets"
openssl rand -base64 24 | tr -d '\n' > "$directory/secrets/admin-password"
openssl rand -base64 32 | tr -d '\n' > "$directory/secrets/settings-key"
# Compose file secrets retain host modes. The protected parent directory remains 0700.
chmod 444 "$directory/secrets/admin-password" "$directory/secrets/settings-key"
admin_path="$(openssl rand -hex 24)"
secure=false; [[ "$url" != https://* ]] || secure=true
cat > "$directory/.env" <<EOF
COMPOSE_PROJECT_NAME=$project
XBOARD_IMAGE=$image
XBOARD_PANEL_URL=$url
XBOARD_ADMIN_EMAIL=$email
XBOARD_ADMIN_PATH=$admin_path
XBOARD_PORT=$port
XBOARD_BIND_ADDRESS=$bind
XBOARD_COOKIE_SECURE=$secure
EOF
compose=(docker compose --env-file "$directory/.env" --project-directory "$directory" -f "$directory/compose.yaml")
if ! "${compose[@]}" up -d --no-build --wait --wait-timeout 180; then
  "${compose[@]}" stop app || true
  fail "Startup failed. Files and data are preserved at $directory; inspect docker compose logs locally."
fi
container="$("${compose[@]}" ps -q app)"
[[ -n "$container" && "$(docker inspect "$container" --format '{{.Config.Image}}')" == "$image" ]] || fail 'Running image mismatch.'
[[ "$(docker inspect "$container" --format '{{.State.Health.Status}}')" == healthy ]] || fail 'Container is not healthy.'
printf 'Installed revision: %s\nPanel: %s/%s/\nAdministrator: %s\nPassword file: %s/secrets/admin-password\nConfiguration: %s\n' "$revision" "$url" "$admin_path" "$email" "$directory" "$directory"
if [[ "$secure" == true ]]; then printf 'HTTPS origin selected: configure your TLS reverse proxy to forward to port %s.\n' "$port"; fi
