#!/usr/bin/env bash

set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "$script_dir/../.." && pwd)"
test_root="$(mktemp -d "${TMPDIR:-/tmp}/etos-telemetry-pull-test.XXXXXX")"
trap 'rm -rf "$test_root"' EXIT

fake_bin="$test_root/bin"
archive_root="$test_root/archive"
default_archive_root="$test_root/default-archive"
fixture="$test_root/envelope.json"
manifest="$test_root/manifest.json"
confirm="$test_root/confirm.json"
mkdir -p "$fake_bin"
ln -s "$repo_root/scripts/testdata/fake-telemetry-ssh.sh" "$fake_bin/ssh"

payload='{"hangDiagnostics":[],"signpostMetrics":[]}'
payload_id="$(printf '%s' "$payload" | shasum -a 256 | awk '{print $1}')"
jq -cn \
  --arg payload_id "$payload_id" \
  --argjson payload "$payload" \
  '{
    schema_version: 1,
    payload_id: $payload_id,
    kind: "diagnostic",
    captured_at: "2026-07-26T12:00:00Z",
    app: {
      version: "2.7.0",
      build: "270",
      distribution: "testflight"
    },
    platform: {
      name: "ios",
      os_version: "26.0",
      device_class: "iPhone17,2",
      architecture: "arm64"
    },
    privacy: {
      contains_chat_content: false,
      contains_request_body: false,
      contains_response_body: false,
      contains_credentials: false,
      contains_user_identifier: false
    },
    payload: $payload
  }' > "$fixture"

fixture_size="$(stat -f '%z' "$fixture" 2>/dev/null || stat -c '%s' "$fixture")"
fixture_sha="$(shasum -a 256 "$fixture" | awk '{print $1}')"
jq -cn \
  --arg payload_id "$payload_id" \
  --argjson size "$fixture_size" \
  --arg sha "$fixture_sha" \
  '{
    schema_version: 1,
    generated_at: "2026-07-26T12:01:00Z",
    entries: [{
      payload_id: $payload_id,
      kind: "diagnostic",
      captured_at: "2026-07-26T12:00:00Z",
      received_at: "2026-07-26T12:01:00Z",
      relative_path: ("records/2026-07-26/diagnostic_" + $payload_id + ".json"),
      size_bytes: $size,
      file_sha256: $sha
    }]
  }' > "$manifest"

export PATH="$fake_bin:$PATH"
export FAKE_TELEMETRY_MANIFEST="$manifest"
export FAKE_TELEMETRY_EXPORT="$fixture"
export FAKE_TELEMETRY_CONFIRM="$confirm"

mkdir -p "$default_archive_root"
(
  cd "$default_archive_root"
  "$repo_root/scripts/telemetry-pull.sh" \
    --host test@example \
    --once
)
default_target="$default_archive_root/raw/2026-07-26/diagnostic_${payload_id}.json"
[[ -f "$default_target" ]] ||
  { printf '默认归档没有写入当前工作目录\n' >&2; exit 1; }

"$repo_root/scripts/telemetry-pull.sh" \
  --host test@example \
  --archive "$archive_root" \
  --once

target="$archive_root/raw/2026-07-26/diagnostic_${payload_id}.json"
[[ -f "$target" ]] || { printf '未生成长期归档文件\n' >&2; exit 1; }
[[ "$(shasum -a 256 "$target" | awk '{print $1}')" == "$fixture_sha" ]] ||
  { printf '长期归档哈希不一致\n' >&2; exit 1; }
jq -e --arg payload_id "$payload_id" \
  '.payload_ids == [$payload_id]' "$confirm" >/dev/null ||
  { printf '服务端确认 ID 不精确\n' >&2; exit 1; }

# 模拟“本地落盘后、确认响应前中断”：再次拉取必须复用并校验本地文件。
"$repo_root/scripts/telemetry-pull.sh" \
  --host test@example \
  --archive "$archive_root" \
  --once

if find "$archive_root" -name '*.partial' -o -name '.pull.lock' | grep -q .; then
  printf '拉取完成后遗留临时文件或锁\n' >&2
  exit 1
fi

# 传输内容损坏时必须在确认前失败，且不能把 .partial 原子替换成正式文件。
corrupt_root="$test_root/corrupt-archive"
corrupt_fixture="$test_root/corrupt.json"
printf '%s\n' '{"corrupt":true}' > "$corrupt_fixture"
rm -f "$confirm"
export FAKE_TELEMETRY_EXPORT="$corrupt_fixture"
if "$repo_root/scripts/telemetry-pull.sh" \
  --host test@example \
  --archive "$corrupt_root" \
  --once; then
  printf '损坏传输不应通过校验\n' >&2
  exit 1
fi
[[ ! -e "$confirm" ]] || { printf '损坏文件不应发送确认\n' >&2; exit 1; }
[[ ! -f "$corrupt_root/raw/2026-07-26/diagnostic_${payload_id}.json" ]] ||
  { printf '损坏文件不应成为正式归档\n' >&2; exit 1; }

printf 'telemetry-pull-test: OK\n'
