#!/usr/bin/env bash

set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "$script_dir/.." && pwd)"
invocation_dir="$PWD"

if [[ "${1:-}" == "--help" || "${1:-}" == "-h" ]]; then
  (
    cd "$repo_root"
    go run ./cmd/telemetry-analyzer --help
  )
  exit 0
fi

cd "$repo_root"
exec go run ./cmd/telemetry-analyzer --archive-root "$invocation_dir" "$@"
