#!/usr/bin/env bash
set -Eeuo pipefail

# Restricted bingo-dev entry point. It keeps the stock runner defaults unchanged
# and opts into one reviewed overlay/base identity for every lifecycle action.
readonly expected_host='bingo'
readonly overlay_rel='tools/local-parity/compose.bingo-test.yaml'
readonly overlay_sha256='98c20be611d5989617e0a2ece97bceeceaae3e8635ee611342eb931569e25403'
readonly candidate_base_image='xboard-go:gpt55-user-2e1713c69dfc-offline'
readonly candidate_base_image_id='sha256:0e19640a157c07392250caadd0321cceced1f8c214a1cdf5156536058bfddfb5'
readonly legacy_image='xboard-legacy-parity:8065164'
readonly legacy_image_id='sha256:6bb8ce9dd1a06ce33400588dd9a5002dccb7e73113ed4d88a7ce065f3439bd94'
readonly go_bin='/home/bingo/.local/toolchains/go1.26.8/bin'
readonly node_bin='/home/bingo/apps/xboard-go-tools/node-v24.20.0-linux-x64/bin'
export PATH="$go_bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin:$node_bin"

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

actual_host="$(hostname -s)"
[[ "$actual_host" == "$expected_host" ]] || {
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
expected_root="$(realpath -e -- "$expected_root_input")"
[[ "$root_dir" == "$expected_root" ]] || {
  echo 'entry point repository root does not match BINGO_PARITY_EXPECTED_ROOT' >&2
  exit 2
}
[[ "$(git -C "$root_dir" rev-parse --show-toplevel)" == "$root_dir" ]] || {
  echo 'entry point root must be the Git worktree top level' >&2
  exit 2
}

overlay_path="$root_dir/$overlay_rel"
[[ -f "$overlay_path" && ! -L "$overlay_path" ]] || {
  echo 'reviewed bingo test overlay is missing or symlinked' >&2
  exit 2
}
[[ "$(sha256sum "$overlay_path" | awk '{print $1}')" == "$overlay_sha256" ]] || {
  echo 'reviewed bingo test overlay SHA-256 mismatch' >&2
  exit 2
}

for system_tool in docker git realpath sha256sum hostname; do
  expected_system_path="/usr/bin/$system_tool"
  [[ "$(command -v "$system_tool")" == "$expected_system_path" ]] || {
    echo "unexpected bingo-dev system tool path: $system_tool" >&2
    exit 2
  }
done

# Build inputs are fail-closed for prepare/start, but cleanup must remain
# available if a build tool or source image is later removed.
if [[ "$action" == prepare || "$action" == start ]]; then
  for required in go node pnpm; do
    command -v "$required" >/dev/null || {
      echo "missing required bingo-dev build tool: $required" >&2
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

  actual_candidate_base_id="$(docker image inspect --format '{{.Id}}' "$candidate_base_image")" || {
    echo 'reviewed candidate base image is unavailable' >&2
    exit 2
  }
  [[ "$actual_candidate_base_id" == "$candidate_base_image_id" ]] || {
    echo 'reviewed candidate base image ID mismatch' >&2
    exit 2
  }
  actual_legacy_image_id="$(docker image inspect --format '{{.Id}}' "$legacy_image")" || {
    echo 'reviewed legacy image is unavailable' >&2
    exit 2
  }
  [[ "$actual_legacy_image_id" == "$legacy_image_id" ]] || {
    echo 'reviewed legacy image ID mismatch' >&2
    exit 2
  }
fi

export LOCAL_PARITY_RUN_ID="$run_id"
export LOCAL_PARITY_EXPECTED_PROJECT="xboard-user-parity-$run_id"
export LOCAL_PARITY_COMPOSE_OVERLAY="$overlay_rel"
export LOCAL_PARITY_CANDIDATE_BASE_IMAGE="$candidate_base_image"
export LOCAL_PARITY_CANDIDATE_BASE_IMAGE_ID="$candidate_base_image_id"
export XBOARD_LEGACY_IMAGE="$legacy_image"

runner_action="$action"
[[ "$action" == stop ]] && runner_action=cleanup
exec bash "$root_dir/tools/local-parity/run.sh" "$runner_action"
