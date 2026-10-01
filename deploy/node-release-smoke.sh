#!/usr/bin/env bash
# Verify the actual published installer contract without enrolling or installing a node.
set -Eeuo pipefail
origin="$1"
version="$2"
work="$(mktemp -d)"
trap 'rm -rf -- "$work"' EXIT
unset XBOARD_NODE_RELEASE_TOKEN GH_TOKEN GITHUB_TOKEN
curl -fsS "$origin/api/v2/node/releases/$version/install.sh" -o "$work/install.sh"
curl -fsS "$origin/api/v2/node/releases/$version/SHA256SUMS" -o "$work/SHA256SUMS"
(cd "$work" && grep ' install.sh$' SHA256SUMS | sha256sum -c -)
# Sourcing defines functions only; the installer's main guard prevents host changes.
source "$work/install.sh"
# This probe owns its cleanup; never invoke installation rollback handlers.
trap - ERR
trap 'rm -rf -- "$work"' EXIT
TMP_DIR="$work"
RELEASE_VERSION="$version"
RELEASE_API_BASE="$origin/api/v2/node/releases"
validate_release_api_base
download_release_metadata "$work/release.json"
for artifact in xboard-node-linux-amd64 xboard-node-linux-arm64 xbctl-linux-amd64 xbctl-linux-arm64; do
    resolve_download_url "$artifact"
    download_file "$DOWNLOAD_URL" "$work/$artifact" "$MAX_BINARY_SIZE"
    verify_download_checksum "$work/$artifact" "$artifact"
done
printf 'Panel installer metadata and all four binary checksums passed without a GitHub token. No node installed or enrolled.\n'
