#!/usr/bin/env bash
set -Eeuo pipefail

repository_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
matrix="$repository_root/.github/client-compatibility.json"
artifact_directory="${1:-$repository_root/artifacts/client-compat/xboard-node}"
temporary_root="$(mktemp -d)"
app_port_base="${XBOARD_CLIENT_COMPAT_APP_PORT_BASE:-19087}"
node_port_base="${XBOARD_CLIENT_COMPAT_NODE_PORT_BASE:-19443}"
app_pid=
node_pid=

cleanup_processes() {
  if [ -n "$node_pid" ]; then
    kill "$node_pid" 2>/dev/null || true
    wait "$node_pid" 2>/dev/null || true
    node_pid=
  fi
  if [ -n "$app_pid" ]; then
    kill "$app_pid" 2>/dev/null || true
    wait "$app_pid" 2>/dev/null || true
    app_pid=
  fi
}

cleanup() {
  cleanup_processes
  rm -rf "$temporary_root"
}
trap cleanup EXIT

mkdir -p "$artifact_directory"
umask 077
go build -trimpath -o "$temporary_root/xboard" ./cmd/xboard
printf 'client\tversion\tchannel\tenrollment\tnode-list\tlistener\tsecret-scan\tresult\n' > "$artifact_directory/results.tsv"
printf 'client\tversion\tsha256\n' > "$artifact_directory/asset-checksums.tsv"

index=0
while IFS= read -r entry; do
  version="$(jq -r '.version' <<<"$entry")"
  channel="$(jq -r '.channel' <<<"$entry")"
  binary_env="$(jq -r '.binary_env' <<<"$entry")"
  asset_url="$(jq -r '.asset_url' <<<"$entry")"
  asset_sha256="$(jq -r '.asset_sha256' <<<"$entry")"
  binary="$temporary_root/xboard-node-$version"
  run_directory="$temporary_root/run-$version"
  app_port=$((app_port_base + index))
  node_port=$((node_port_base + index))
  base="http://127.0.0.1:$app_port"
  mkdir -p "$run_directory/kernel"

  if ss -ltn | grep -q ":${app_port} " || ss -ltn | grep -q ":${node_port} "; then
    printf 'required compatibility test port is already in use: app=%s node=%s\n' "$app_port" "$node_port" >&2
    exit 1
  fi
  source_binary="${!binary_env:-}"
  if [ -n "$source_binary" ]; then
    test -f "$source_binary"
    cp "$source_binary" "$binary"
  elif [ -n "${XBOARD_NODE_RELEASE_TOKEN:-}" ]; then
    curl --fail --location --proto '=https' --tlsv1.2 --retry 3 --max-time 180 \
      -H 'Accept: application/octet-stream' -H "Authorization: Bearer $XBOARD_NODE_RELEASE_TOKEN" \
      --output "$binary" "$asset_url"
  else
    printf 'Xboard-Node %s requires %s or XBOARD_NODE_RELEASE_TOKEN; refusing an unauthenticated private asset download\n' \
      "$version" "$binary_env" >&2
    exit 1
  fi
  printf '%s  %s\n' "$asset_sha256" "$binary" | sha256sum --check --strict
  chmod 0755 "$binary"
  "$binary" -v 2>&1 | tee "$artifact_directory/xboard-node-$version.version.log"
  grep -Fq "xboard-node v$version " "$artifact_directory/xboard-node-$version.version.log"
  printf 'Xboard-Node\t%s\t%s\n' "$version" "$asset_sha256" >> "$artifact_directory/asset-checksums.tsv"

  admin_email="real-node-$version@example.test"
  admin_password="$(openssl rand -base64 36 | tr -d '\r\n')"
  settings_key="$(openssl rand -base64 32 | tr -d '\r\n')"
  XBOARD_ADDRESS="127.0.0.1:$app_port" \
  XBOARD_DATABASE_DSN="file:$run_directory/xboard.db" \
  XBOARD_PANEL_URL="$base" \
  XBOARD_ALLOWED_ORIGINS="$base" \
  XBOARD_COOKIE_SECURE=false \
  XBOARD_BOOTSTRAP_ADMIN_EMAIL="$admin_email" \
  XBOARD_BOOTSTRAP_ADMIN_PASSWORD="$admin_password" \
  XBOARD_SETTINGS_ENCRYPTION_KEY="$settings_key" \
  XBOARD_LEGACY_ADMIN_PATH=e2e-admin-secure \
  XBOARD_WEB_ROOT="$repository_root/web/dist" \
    "$temporary_root/xboard" >"$run_directory/app.log" 2>&1 &
  app_pid=$!

  ready=0
  for _ in $(seq 1 300); do
    if ! kill -0 "$app_pid" 2>/dev/null; then
      printf 'Xboard-Go exited before readiness for Xboard-Node %s\n' "$version" >&2
      exit 1
    fi
    if curl --fail --silent --max-time 1 "$base/healthz" >/dev/null; then
      ready=1
      break
    fi
    sleep 0.1
  done
  if [ "$ready" != 1 ]; then
    printf 'Xboard-Go did not become ready for Xboard-Node %s\n' "$version" >&2
    exit 1
  fi

  login_body="$(jq -cn --arg email "$admin_email" --arg password "$admin_password" '{email:$email,password:$password}')"
  status="$(curl --silent --show-error --output "$run_directory/response.json" --write-out '%{http_code}' \
    --cookie-jar "$run_directory/cookies" -H 'Content-Type: application/json' -d "$login_body" "$base/api/v1/auth/login")"
  test "$status" = 200
  csrf="$(awk '$6=="xboard_csrf" {print $7}' "$run_directory/cookies" | tail -1)"
  test -n "$csrf"

  status="$(curl --silent --show-error --output "$run_directory/response.json" --write-out '%{http_code}' \
    --cookie "$run_directory/cookies" -H 'Content-Type: application/json' -H "X-CSRF-Token: $csrf" \
    -d '{"name":"real-node-compat-group"}' "$base/api/v1/admin/e2e-admin-secure/server-groups")"
  test "$status" = 201
  group_id="$(jq -er '.data.id' "$run_directory/response.json")"

  node_body="$(jq -cn --arg port "$node_port" --argjson group_id "$group_id" \
    '{name:"real-node-compat-socks",type:"socks",external_code:null,parent_id:null,rate:1,tags:[],host:"127.0.0.1",port:$port,server_port:($port|tonumber),listen_address:"0.0.0.0",protocol_settings:{tls:0,tls_settings:{server_name:"",allow_insecure:false,ech:{enabled:false,config:"",query_server_name:"",key:""}}},show:true,enabled:true,sort:0,machine_id:null,group_ids:[$group_id],route_ids:[],rate_time_enabled:false,rate_time_ranges:[],custom_outbounds:[],custom_routes:[],certificate_config:{cert_mode:"none"},transfer_enable:0}')"
  status="$(curl --silent --show-error --output "$run_directory/response.json" --write-out '%{http_code}' \
    --cookie "$run_directory/cookies" -H 'Content-Type: application/json' -H "X-CSRF-Token: $csrf" \
    -d "$node_body" "$base/api/v1/admin/e2e-admin-secure/nodes")"
  test "$status" = 201
  node_id="$(jq -er '.data.id' "$run_directory/response.json")"
  node_revision="$(jq -er '.data.revision' "$run_directory/response.json")"

  status="$(curl --silent --show-error --output "$run_directory/response.json" --write-out '%{http_code}' \
    --cookie "$run_directory/cookies" -H 'Content-Type: application/json' -H "X-CSRF-Token: $csrf" \
    -d '{"name":"real-node-compat-machine","is_active":true}' "$base/api/v1/admin/e2e-admin-secure/machines")"
  test "$status" = 201
  machine_id="$(jq -er '.data.id' "$run_directory/response.json")"
  enrollment="$(jq -er '.data.token' "$run_directory/response.json")"

  enroll_body="$(jq -cn --argjson machine_id "$machine_id" --arg enrollment "$enrollment" '{machine_id:$machine_id,enrollment_code:$enrollment}')"
  status="$(curl --silent --show-error --output "$run_directory/response.json" --write-out '%{http_code}' \
    -H 'Content-Type: application/json' -d "$enroll_body" "$base/api/v2/server/machine/enroll")"
  test "$status" = 200
  machine_token="$(jq -er '.data.token' "$run_directory/response.json")"

  status="$(curl --silent --show-error --output "$run_directory/response.json" --write-out '%{http_code}' \
    --cookie "$run_directory/cookies" -X PUT -H 'Content-Type: application/json' -H "X-CSRF-Token: $csrf" \
    -d "{\"revision\":$node_revision}" "$base/api/v1/admin/e2e-admin-secure/machines/$machine_id/nodes/$node_id")"
  test "$status" = 204

  user_body="$(jq -cn --argjson group_id "$group_id" \
    '{email:"real-node-compat-user@example.test",password:"real-node-user-password-123",group_id:$group_id,transfer_enable:1073741824,speed_limit:0,device_limit:0,banned:false}')"
  status="$(curl --silent --show-error --output "$run_directory/response.json" --write-out '%{http_code}' \
    --cookie "$run_directory/cookies" -H 'Content-Type: application/json' -H "X-CSRF-Token: $csrf" \
    -d "$user_body" "$base/api/v1/admin/e2e-admin-secure/users")"
  test "$status" = 201

  cat > "$run_directory/xboard-node.yml" <<EOF
panel:
  url: "$base"
machine:
  machine_id: $machine_id
  token_env: "XBOARD_NODE_COMPAT_MACHINE_TOKEN"
kernel:
  type: "singbox"
  config_dir: "$run_directory/kernel"
log:
  level: "info"
node:
  push_interval: 5
  pull_interval: 5
  track_interval: 5
  device_report_interval: 5
ws:
  status_interval: 2
  handshake_timeout: 5
  backoff_initial: 1
  backoff_max: 2
  discovery_interval: 5
EOF

  export XBOARD_NODE_COMPAT_MACHINE_TOKEN="$machine_token"
  "$binary" -c "$run_directory/xboard-node.yml" >"$run_directory/node.log" 2>&1 &
  node_pid=$!
  online=0
  for _ in $(seq 1 150); do
    if ! kill -0 "$node_pid" 2>/dev/null; then
      break
    fi
    status="$(curl --silent --show-error --output "$run_directory/response.json" --write-out '%{http_code}' \
      --cookie "$run_directory/cookies" "$base/api/v1/admin/e2e-admin-secure/machines/$machine_id")"
    if [ "$status" = 200 ] && jq -e '.data.last_seen_at != null' "$run_directory/response.json" >/dev/null \
      && ss -ltn | grep -q ":${node_port} "; then
      online=1
      break
    fi
    sleep 0.2
  done
  if [ "$online" != 1 ]; then
    printf 'Xboard-Node %s did not become online with its SOCKS listener\n' "$version" >&2
    exit 1
  fi

  status="$(curl --silent --show-error --output "$run_directory/response.json" --write-out '%{http_code}' \
    -X POST -H 'Content-Type: application/json' -H "Authorization: Bearer $machine_token" \
    -d "{\"machine_id\":$machine_id}" "$base/api/v2/server/machine/nodes")"
  test "$status" = 200
  jq -e --argjson node_id "$node_id" '.nodes|length==1 and .[0].id==$node_id and .[0].type=="socks"' \
    "$run_directory/response.json" >/dev/null
  sleep 2
  kill -0 "$node_pid"

  if grep -Fq -- "$machine_token" "$run_directory/node.log" \
    || grep -Fq -- "$admin_password" "$run_directory/node.log" \
    || grep -Fq -- "$settings_key" "$run_directory/node.log" \
    || grep -Fq -- "$machine_token" "$run_directory/app.log"; then
    printf 'secret material appeared in Xboard-Node %s compatibility logs\n' "$version" >&2
    exit 1
  fi

  printf 'Xboard-Node\t%s\t%s\tPASS\tPASS\tPASS\tPASS\tPASS\n' "$version" "$channel" \
    >> "$artifact_directory/results.tsv"
  sed -e "s#$run_directory#<ephemeral-run-directory>#g" \
    -e "s#$base#<loopback-panel>#g" "$run_directory/xboard-node.yml" \
    > "$artifact_directory/xboard-node-$version.config.yml"
  cleanup_processes
  unset XBOARD_NODE_COMPAT_MACHINE_TOKEN
  index=$((index + 1))
done < <(jq -c '.node_agents[]' "$matrix")

cat "$artifact_directory/results.tsv"
