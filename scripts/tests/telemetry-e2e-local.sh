#!/usr/bin/env bash

set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "$script_dir/../.." && pwd)"
test_root="$(mktemp -d "${TMPDIR:-/tmp}/etos-telemetry-e2e.XXXXXX")"
server_pid=""

cleanup() {
  if [[ -n "$server_pid" ]] && kill -0 "$server_pid" 2>/dev/null; then
    kill "$server_pid" 2>/dev/null || true
    wait "$server_pid" 2>/dev/null || true
  fi
  rm -rf "$test_root"
}
trap cleanup EXIT INT TERM

public_port=$((30000 + RANDOM % 5000))
admin_port=$((35000 + RANDOM % 5000))
binary="$test_root/els-feedback-proxy"
data_dir="$test_root/data"
archive_root="$test_root/archive"
request_body="$test_root/swift-request.json"
fake_bin="$test_root/bin"
mkdir -p "$fake_bin"
ln -s "$repo_root/scripts/testdata/local-command-ssh.sh" "$fake_bin/ssh"

cd "$repo_root"
go build -o "$binary" ./cmd/server
xcrun swift "$repo_root/scripts/testdata/generate-swift-telemetry-fixture.swift" > "$request_body"
jq -e '
  .schema_version == 1 and
  (.envelopes | length) == 2 and
  all(.envelopes[]; .privacy.contains_chat_content == false)
' "$request_body" >/dev/null

env \
  PORT="$public_port" \
  ADMIN_LISTEN_ADDR="127.0.0.1:$admin_port" \
  ANNOUNCEMENT_ADMIN_TOKEN="synthetic-admin-token" \
  ADMIN_WEB_AUTH_DISABLED="true" \
  GITHUB_TOKEN="synthetic-github-token" \
  GITHUB_TOKEN_LOGIN="synthetic-bot" \
  MODERATION_ENABLED="false" \
  DATA_DIR="$data_dir" \
  "$binary" > "$test_root/server.log" 2>&1 &
server_pid=$!

for _ in $(seq 1 50); do
  if curl -fsS "http://127.0.0.1:$public_port/v1/healthz" >/dev/null; then
    break
  fi
  sleep 0.1
done
curl -fsS "http://127.0.0.1:$public_port/v1/healthz" |
  jq -e '.telemetry_enabled == true' >/dev/null

first_response="$test_root/first-response.json"
second_response="$test_root/second-response.json"
curl -fsS \
  -H 'Content-Type: application/json' \
  -H 'User-Agent: ETOS LLM Studio/2.7.0 (iOS; MetricKit Telemetry)' \
  --data-binary "@$request_body" \
  "http://127.0.0.1:$public_port/v1/telemetry" > "$first_response"
jq -e '
  (.results | length) == 2 and
  all(.results[]; .status == "accepted")
' "$first_response" >/dev/null

curl -fsS \
  -H 'Content-Type: application/json' \
  -H 'User-Agent: ETOS LLM Studio/2.7.0 (iOS; MetricKit Telemetry)' \
  --data-binary "@$request_body" \
  "http://127.0.0.1:$public_port/v1/telemetry" > "$second_response"
jq -e '
  (.results | length) == 2 and
  all(.results[]; .status == "duplicate")
' "$second_response" >/dev/null

{
  printf 'ANNOUNCEMENT_ADMIN_TOKEN=%s\n' 'synthetic-admin-token'
  printf 'ELS_ADMIN_URL=http://127.0.0.1:%s\n' "$admin_port"
} > "$test_root/.env"
chmod 600 "$test_root/.env"

PATH="$fake_bin:$PATH" \
  "$repo_root/scripts/telemetry-pull.sh" \
  --host local-test \
  --remote-dir "$test_root" \
  --archive "$archive_root" \
  --once

raw_count="$(find "$archive_root/raw" -type f -name '*.json' | wc -l | tr -d '[:space:]')"
[[ "$raw_count" == "2" ]] || {
  printf '期望拉取 2 个原始文件，实际 %s\n' "$raw_count" >&2
  exit 1
}

env \
  ANNOUNCEMENT_ADMIN_TOKEN="synthetic-admin-token" \
  ELS_ADMIN_URL="http://127.0.0.1:$admin_port" \
  "$binary" telemetry status |
  jq -e '.total_count == 0 and .total_bytes == 0' >/dev/null

(
  cd "$archive_root"
  "$repo_root/scripts/telemetry-analyze.sh" \
    --spotlight=false
) > "$test_root/analyzer-result.json"
analysis_dir="$(
  find "$archive_root/analysis" -mindepth 1 -maxdepth 1 -type d |
    sort |
    head -n 1
)"
[[ -n "$analysis_dir" ]] || {
  printf '分析结果没有写入当前工作目录\n' >&2
  exit 1
}
jq -e '
  .file_count == 2 and
  .metric_count == 1 and
  .diagnostic_count == 1 and
  .histogram_count == 1
' "$test_root/analyzer-result.json" >/dev/null
grep -F 'ModelRequestStreaming' "$analysis_dir/summary.md" >/dev/null
grep -F 'hangDiagnostics' "$analysis_dir/summary.md" >/dev/null

printf 'telemetry-e2e-local: OK\n'
