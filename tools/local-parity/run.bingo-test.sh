#!/usr/bin/env bash
set -Eeuo pipefail

# Restricted bingo-dev entry point. The stock runner remains unchanged unless
# this entry point supplies its reviewed overlay, project, and base identity.
readonly expected_host='bingo'
readonly source_overlay_rel='tools/local-parity/compose.bingo-test.yaml'
readonly source_overlay_sha256='98c20be611d5989617e0a2ece97bceeceaae3e8635ee611342eb931569e25403'
readonly candidate_base_image='xboard-go:gpt55-user-2e1713c69dfc-offline'
readonly candidate_base_image_id='sha256:0e19640a157c07392250caadd0321cceced1f8c214a1cdf5156536058bfddfb5'
readonly legacy_image='xboard-legacy-parity:8065164'
readonly legacy_image_id='sha256:6bb8ce9dd1a06ce33400588dd9a5002dccb7e73113ed4d88a7ce065f3439bd94'
readonly go_bin='/home/bingo/.local/toolchains/go1.26.8/bin'
readonly node_bin='/home/bingo/apps/xboard-go-tools/node-v24.20.0-linux-x64/bin'
export PATH="$node_bin:$go_bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
export GOTOOLCHAIN=local
export GOPROXY=off
export GOMODCACHE=/home/bingo/.cache/xboard-go/gopath/pkg/mod
export COREPACK_HOME=/home/bingo/.cache/node/corepack

usage() {
  echo 'usage: BINGO_PARITY_EXPECTED_ROOT=/absolute/repo/path tools/local-parity/run.bingo-test.sh {prepare|start|verify|stop} bingo-<run-id>' >&2
  exit 2
}

action="${1:-}"
run_id="${2:-}"
case "$action" in
  prepare|start|verify|stop) ;;
  *) usage ;;
esac
[[ "$run_id" =~ ^bingo-[a-z0-9-]+$ ]] || usage

[[ "$(/usr/bin/hostname -s)" == "$expected_host" ]] || {
  echo "this entry point is restricted to host $expected_host" >&2
  exit 2
}

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
root_dir="$(cd "$script_dir/../.." && pwd -P)"
expected_root_input="${BINGO_PARITY_EXPECTED_ROOT:-}"
[[ "$expected_root_input" = /* && -d "$expected_root_input" && ! -L "$expected_root_input" ]] || {
  echo 'BINGO_PARITY_EXPECTED_ROOT must be an existing absolute non-symlink directory' >&2
  exit 2
}
expected_root="$(/usr/bin/realpath -e -- "$expected_root_input")"
[[ "$root_dir" == "$expected_root" ]] || {
  echo 'entry point repository root does not match BINGO_PARITY_EXPECTED_ROOT' >&2
  exit 2
}
[[ "$(/usr/bin/git -C "$root_dir" rev-parse --show-toplevel)" == "$root_dir" ]] || {
  echo 'entry point root must be the Git worktree top level' >&2
  exit 2
}

for system_tool in docker git realpath sha256sum hostname cmp install; do
  [[ "$(command -v "$system_tool")" == "/usr/bin/$system_tool" ]] || {
    echo "unexpected bingo-dev system tool path: $system_tool" >&2
    exit 2
  }
done
for cache_dir in "$GOMODCACHE" "$COREPACK_HOME"; do
  [[ -d "$cache_dir" && ! -L "$cache_dir" ]] || {
    echo "reviewed bingo-dev cache is unavailable: $cache_dir" >&2
    exit 2
  }
done

project="xboard-user-parity-$run_id"
run_dir=".local/local-parity-$run_id"
evidence_dir="output/local-parity-$run_id"
source_overlay_path="$root_dir/$source_overlay_rel"
prepared_overlay_rel="$run_dir/compose.bingo-test.prepared.yaml"
prepared_overlay_path="$root_dir/$prepared_overlay_rel"
identity_path="$root_dir/$run_dir/bingo-run-identity.txt"
evidence_identity_path="$root_dir/$evidence_dir/bingo-run-identity.txt"

render_identity() {
  printf 'schema=1\n'
  printf 'run_id=%s\n' "$run_id"
  printf 'project=%s\n' "$project"
  printf 'root=%s\n' "$root_dir"
  printf 'source_overlay=%s\n' "$source_overlay_rel"
  printf 'source_overlay_sha256=%s\n' "$source_overlay_sha256"
  printf 'prepared_overlay=%s\n' "$prepared_overlay_rel"
  printf 'candidate_base_image=%s\n' "$candidate_base_image"
  printf 'candidate_base_image_id=%s\n' "$candidate_base_image_id"
  printf 'legacy_image=%s\n' "$legacy_image"
  printf 'legacy_image_id=%s\n' "$legacy_image_id"
  printf 'go_bin=%s\n' "$go_bin"
  printf 'node_bin=%s\n' "$node_bin"
  printf 'GOTOOLCHAIN=%s\n' "$GOTOOLCHAIN"
  printf 'GOPROXY=%s\n' "$GOPROXY"
  printf 'GOMODCACHE=%s\n' "$GOMODCACHE"
  printf 'COREPACK_HOME=%s\n' "$COREPACK_HOME"
}

validate_source_overlay() {
  [[ -f "$source_overlay_path" && ! -L "$source_overlay_path" ]] || {
    echo 'reviewed bingo test source overlay is missing or symlinked' >&2
    exit 2
  }
  [[ "$(/usr/bin/sha256sum "$source_overlay_path" | awk '{print $1}')" == "$source_overlay_sha256" ]] || {
    echo 'reviewed bingo test source overlay SHA-256 mismatch' >&2
    exit 2
  }
}

validate_prepared_identity() {
  [[ -f "$identity_path" && ! -L "$identity_path" ]] || {
    echo 'missing or symlinked prepared bingo run identity' >&2
    exit 2
  }
  [[ -f "$prepared_overlay_path" && ! -L "$prepared_overlay_path" ]] || {
    echo 'missing or symlinked prepared bingo overlay snapshot' >&2
    exit 2
  }
  /usr/bin/cmp -s <(render_identity) "$identity_path" || {
    echo 'prepared bingo run identity does not match this root/run configuration' >&2
    exit 2
  }
  [[ "$(/usr/bin/sha256sum "$prepared_overlay_path" | awk '{print $1}')" == "$source_overlay_sha256" ]] || {
    echo 'prepared bingo overlay snapshot SHA-256 mismatch' >&2
    exit 2
  }
}

validate_build_inputs() {
  for build_tool in go node pnpm; do
    command -v "$build_tool" >/dev/null || {
      echo "missing required bingo-dev build tool: $build_tool" >&2
      exit 2
    }
  done
  [[ "$(command -v go)" == "$go_bin/go" && "$(go env GOVERSION)" == go1.26.8 ]] || {
    echo 'the reviewed bingo-dev Go 1.26.8 toolchain is unavailable' >&2
    exit 2
  }
  [[ "$(command -v node)" == "$node_bin/node" && "$(node --version)" == v24.20.0 ]] || {
    echo 'the reviewed bingo-dev Node 24.20.0 toolchain is unavailable' >&2
    exit 2
  }
  [[ "$(command -v pnpm)" == "$node_bin/pnpm" && "$(pnpm --version)" == 11.10.0 ]] || {
    echo 'the reviewed bingo-dev pnpm 11.10.0 toolchain is unavailable' >&2
    exit 2
  }

  actual_candidate_base_id="$(/usr/bin/docker image inspect --format '{{.Id}}' "$candidate_base_image")" || {
    echo 'reviewed candidate base image is unavailable' >&2
    exit 2
  }
  [[ "$actual_candidate_base_id" == "$candidate_base_image_id" ]] || {
    echo 'reviewed candidate base image ID mismatch' >&2
    exit 2
  }
  actual_legacy_image_id="$(/usr/bin/docker image inspect --format '{{.Id}}' "$legacy_image")" || {
    echo 'reviewed legacy image is unavailable' >&2
    exit 2
  }
  [[ "$actual_legacy_image_id" == "$legacy_image_id" ]] || {
    echo 'reviewed legacy image ID mismatch' >&2
    exit 2
  }
}

export LOCAL_PARITY_RUN_ID="$run_id"
export LOCAL_PARITY_EXPECTED_PROJECT="$project"
export LOCAL_PARITY_CANDIDATE_BASE_IMAGE="$candidate_base_image"
export LOCAL_PARITY_CANDIDATE_BASE_IMAGE_ID="$candidate_base_image_id"
export XBOARD_LEGACY_IMAGE="$legacy_image"

case "$action" in
  prepare)
    validate_source_overlay
    validate_build_inputs
    export LOCAL_PARITY_COMPOSE_OVERLAY="$source_overlay_rel"
    bash "$root_dir/tools/local-parity/run.sh" prepare
    [[ -d "$root_dir/$run_dir" && ! -L "$root_dir/$run_dir" ]]
    [[ -d "$root_dir/$evidence_dir" && ! -L "$root_dir/$evidence_dir" ]]
    umask 077
    /usr/bin/install -m 600 "$source_overlay_path" "$prepared_overlay_path"
    identity_tmp="$identity_path.tmp.$$"
    render_identity >"$identity_tmp"
    chmod 600 "$identity_tmp"
    mv -T -- "$identity_tmp" "$identity_path"
    /usr/bin/install -m 600 "$identity_path" "$evidence_identity_path"
    ;;
  start|verify)
    validate_prepared_identity
    validate_source_overlay
    [[ "$action" != start ]] || validate_build_inputs
    export LOCAL_PARITY_COMPOSE_OVERLAY="$prepared_overlay_rel"
    exec bash "$root_dir/tools/local-parity/run.sh" "$action"
    ;;
  stop)
    validate_prepared_identity
    export LOCAL_PARITY_COMPOSE_OVERLAY="$prepared_overlay_rel"
    exec bash "$root_dir/tools/local-parity/run.sh" cleanup
    ;;
esac
