#!/usr/bin/env bash

set -euo pipefail

remote_command="${*: -1}"
case "$remote_command" in
  *"telemetry manifest")
    cp "$FAKE_TELEMETRY_MANIFEST" /dev/stdout
    ;;
  *"telemetry export --payload-id "*)
    cp "$FAKE_TELEMETRY_EXPORT" /dev/stdout
    ;;
  *"telemetry confirm --file -")
    request="$(mktemp "${TMPDIR:-/tmp}/fake-confirm.XXXXXX")"
    trap 'rm -f "$request"' EXIT
    cp /dev/stdin "$request"
    cp "$request" "$FAKE_TELEMETRY_CONFIRM"
    jq -c '{
      confirmed_payload_ids: .payload_ids,
      missing_payload_ids: []
    }' "$request"
    ;;
  *)
    printf '测试 SSH 收到未知命令: %s\n' "$remote_command" >&2
    exit 64
    ;;
esac
