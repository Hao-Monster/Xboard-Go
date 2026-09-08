#!/usr/bin/env bash
set -Eeuo pipefail

echo 'ISOLATED CANDIDATE IMAGE TEST ONLY: no container is started.'
repo="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"
tmp="$(mktemp -d /tmp/xboard-candidate-image-test.XXXXXX)"
suffix="$$"
base_tag="xboard-candidate-test-base:$suffix"
candidate_tag="xboard-candidate-test:$suffix"
container="xboard-candidate-test-$suffix"

cleanup() {
  docker rm -f "$container" >/dev/null 2>&1 || true
  docker image rm -f "$candidate_tag" "$base_tag" >/dev/null 2>&1 || true
  [[ "$tmp" == /tmp/xboard-candidate-image-test.* && -d "$tmp" && ! -L "$tmp" ]] || return 1
  rm -rf --one-file-system -- "$tmp"
}
trap cleanup EXIT

mkdir -p "$tmp/base/rootfs/srv/xboard/web/assets" "$tmp/candidate/web-dist/assets" "$tmp/export"
printf '%s\n' synthetic-base-binary >"$tmp/base/rootfs/xboard"
printf '%s\n' stale-base-asset >"$tmp/base/rootfs/srv/xboard/web/assets/stale-from-base.js"
printf '%s\n' candidate-binary >"$tmp/candidate/xboard"
printf '%s\n' '<html>candidate</html>' >"$tmp/candidate/web-dist/index.html"
printf '%s\n' current-candidate-asset >"$tmp/candidate/web-dist/assets/current.js"
cat >"$tmp/base/Dockerfile" <<'DOCKERFILE'
FROM scratch
COPY --chown=65532:65532 rootfs /
USER 65532:65532
ENTRYPOINT ["/xboard"]
DOCKERFILE

cleaner_build=(
  go build -buildvcs=false -trimpath -ldflags='-s -w -buildid='
  -o "$tmp/candidate/clean-web-root"
  "$repo/tools/local-parity/clean-web-root.go"
)
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 "${cleaner_build[@]}"
docker build --network=none --quiet --tag "$base_tag" "$tmp/base" >"$tmp/base-build.log"
base_id="$(docker image inspect --format '{{.Id}}' "$base_tag")"
candidate_build=(
  docker build --network=none --quiet
  --build-arg "BASE_IMAGE=$base_tag"
  --build-arg APP_REVISION=0000000000000000000000000000000000000000
  --file "$repo/tools/local-parity/Dockerfile.candidate"
  --tag "$candidate_tag"
  "$tmp/candidate"
)
"${candidate_build[@]}" >"$tmp/candidate-build.log"
[[ "$(docker image inspect --format '{{.Id}}' "$base_tag")" == "$base_id" ]]
candidate_id="$(docker image inspect --format '{{.Id}}' "$candidate_tag")"

docker image inspect "$base_id" >"$tmp/base.json"
docker image inspect "$candidate_id" >"$tmp/candidate.json"
python3 - "$tmp/base.json" "$tmp/candidate.json" <<'PY'
import json
import sys

base = json.load(open(sys.argv[1], encoding="utf-8"))[0]["RootFS"]["Layers"]
candidate = json.load(open(sys.argv[2], encoding="utf-8"))[0]["RootFS"]["Layers"]
assert candidate[: len(base)] == base
PY

[[ "$(docker image inspect --format '{{.Config.User}}' "$candidate_id")" == 65532:65532 ]]
[[ "$(docker image inspect --format '{{json .Config.Entrypoint}}' "$candidate_id")" == '["/xboard"]' ]]
[[ "$(docker image inspect --format '{{index .Config.Labels "org.opencontainers.image.revision"}}' "$candidate_id")" == 0000000000000000000000000000000000000000 ]]
docker create --name "$container" --entrypoint /xboard "$candidate_id" --help >/dev/null
docker export "$container" | tar -x -C "$tmp/export" srv/xboard/web
docker export "$container" | tar -tf - >"$tmp/candidate-files.txt"
! grep -Fx 'srv/xboard/web/assets/stale-from-base.js' "$tmp/candidate-files.txt"
! grep -Fx 'tmp/clean-web-root' "$tmp/candidate-files.txt"
grep -Fx 'srv/xboard/web/index.html' "$tmp/candidate-files.txt" >/dev/null
grep -Fx 'srv/xboard/web/assets/current.js' "$tmp/candidate-files.txt" >/dev/null

input_hash="$(cd "$tmp/candidate/web-dist" && find . -type f -print0 | sort -z | xargs -0 sha256sum | sha256sum | awk '{print $1}')"
image_hash="$(cd "$tmp/export/srv/xboard/web" && find . -type f -print0 | sort -z | xargs -0 sha256sum | sha256sum | awk '{print $1}')"
[[ "$image_hash" == "$input_hash" ]]
printf 'PASS base_id=%s candidate_id=%s web_tree_sha256=%s stale_asset_present=0 cleanup_helper_present=0 user=65532:65532 services_started=0\n'   "$base_id" "$candidate_id" "$image_hash"
