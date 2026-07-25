#!/usr/bin/env bash

set -euo pipefail

remote_command="${*: -1}"
case "$remote_command" in
  *"telemetry manifest")
    [[ " $* " == *" -n "* ]] || {
      printf '清单 SSH 必须禁用标准输入\n' >&2
      exit 65
    }
    cp "$FAKE_TELEMETRY_MANIFEST" /dev/stdout
    ;;
  *"telemetry export --payload-id "*)
    [[ " $* " == *" -n "* ]] || {
      printf '导出 SSH 必须禁用标准输入，避免吞掉下一条清单\n' >&2
      exit 65
    }
    cp "$FAKE_TELEMETRY_EXPORT" /dev/stdout
    ;;
  *"telemetry confirm --file -")
    [[ " $* " != *" -n "* ]] || {
      printf '确认 SSH 必须保留标准输入\n' >&2
      exit 65
    }
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
