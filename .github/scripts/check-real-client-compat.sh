#!/usr/bin/env bash
set -euo pipefail

repository_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
matrix="$repository_root/.github/client-compatibility.json"
artifact_directory="${1:-$repository_root/artifacts/client-compat}"
temporary_root="$(mktemp -d)"
trap 'rm -rf "$temporary_root"' EXIT

mkdir -p "$artifact_directory/fixtures" "$artifact_directory/logs"

jq -e '
  .schema_version == 1 and
  ([.automated_clients[].channel] | sort == ["current-stable", "current-stable", "previous-stable", "previous-stable"]) and
  ([.automated_clients[].client] | sort == ["mihomo", "mihomo", "sing-box", "sing-box"]) and
  ([.node_agents[].channel] | sort == ["current-stable", "previous-stable"]) and
  ([.node_agents[].client] | unique == ["Xboard-Node"]) and
  ([.not_run_clients[].client] | unique | length == 6) and
  all(.automated_clients[];
    (.version | test("^[0-9]+\\.[0-9]+\\.[0-9]+$")) and
    (.asset_sha256 | test("^[0-9a-f]{64}$")) and
    (.asset_url | startswith("https://github.com/")) and
    (.fixtures | length > 0))
' "$matrix" >/dev/null

XBOARD_CLIENT_COMPAT_OUTPUT_DIR="$artifact_directory/fixtures" \
  go test ./internal/subscription \
    -run '^TestGenerateRealClientCompatibilityFixtures$' -count=1

printf 'client\tversion\tchannel\tfixture\tresult\n' > "$artifact_directory/results.tsv"
printf 'client\tversion\tsha256\n' > "$artifact_directory/asset-checksums.tsv"

while IFS= read -r entry; do
  client="$(jq -r '.client' <<<"$entry")"
  version="$(jq -r '.version' <<<"$entry")"
  channel="$(jq -r '.channel' <<<"$entry")"
  asset_url="$(jq -r '.asset_url' <<<"$entry")"
  asset_sha256="$(jq -r '.asset_sha256' <<<"$entry")"
  archive="$temporary_root/$client-$version.download"
  tool_directory="$temporary_root/$client-$version"
  mkdir -p "$tool_directory"

  curl --fail --location --proto '=https' --tlsv1.2 --retry 3 --max-time 180 \
    --output "$archive" "$asset_url"
  printf '%s  %s\n' "$asset_sha256" "$archive" | sha256sum --check --strict
  printf '%s\t%s\t%s\n' "$client" "$version" "$asset_sha256" >> "$artifact_directory/asset-checksums.tsv"

  case "$client" in
    sing-box)
      tar -xzf "$archive" -C "$tool_directory" --strip-components=1
      binary="$tool_directory/sing-box"
      "$binary" version | tee "$artifact_directory/logs/$client-$version.version.log"
      grep -Fq "sing-box version $version" "$artifact_directory/logs/$client-$version.version.log"
      ;;
    mihomo)
      binary="$tool_directory/mihomo"
      gzip -dc "$archive" > "$binary"
      chmod 0755 "$binary"
      "$binary" -v | tee "$artifact_directory/logs/$client-$version.version.log"
      grep -Fq "Mihomo Meta v$version " "$artifact_directory/logs/$client-$version.version.log"
      ;;
    *)
      printf 'unsupported automated client: %s\n' "$client" >&2
      exit 1
      ;;
  esac

  while IFS= read -r fixture; do
    fixture_path="$artifact_directory/fixtures/$fixture"
    test -s "$fixture_path"
    runtime_directory="$temporary_root/runtime-$client-$version-${fixture//[^A-Za-z0-9]/-}"
    mkdir -p "$runtime_directory"
    log="$artifact_directory/logs/$client-$version-${fixture//[^A-Za-z0-9]/-}.log"
    if [ "$client" = "sing-box" ]; then
      "$binary" check --disable-color -D "$runtime_directory" -c "$fixture_path" >"$log" 2>&1
    else
      "$binary" -t -d "$runtime_directory" -f "$fixture_path" >"$log" 2>&1
    fi
    printf '%s\t%s\t%s\t%s\tPASS\n' "$client" "$version" "$channel" "$fixture" >> "$artifact_directory/results.tsv"
  done < <(jq -r '.fixtures[]' <<<"$entry")
done < <(jq -c '.automated_clients[]' "$matrix")

"$repository_root/.github/scripts/check-real-node-compat.sh" "$artifact_directory/xboard-node"
cp "$matrix" "$artifact_directory/client-compatibility.json"
find "$artifact_directory" -type f ! -name manifest.sha256 -print0 | sort -z | xargs -0 sha256sum > "$artifact_directory/manifest.sha256"
cat "$artifact_directory/results.tsv"
