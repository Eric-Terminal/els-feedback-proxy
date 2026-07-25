#!/usr/bin/env bash

set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "$script_dir/../.." && pwd)"
test_root="$(mktemp -d "${TMPDIR:-/tmp}/etos-symbolication-test.XXXXXX")"
trap 'rm -rf "$test_root"' EXIT

binary="$test_root/symbol-fixture"
object_file="$test_root/symbol-fixture.o"
dsym="$binary.dSYM"
input_dir="$test_root/raw"
output_dir="$test_root/analysis"
mkdir -p "$input_dir"

xcrun clang -g -O0 -c \
  "$repo_root/scripts/testdata/symbol-fixture.c" \
  -o "$object_file"
xcrun clang "$object_file" -o "$binary"
xcrun dsymutil "$binary" -o "$dsym"

uuid="$(xcrun dwarfdump --uuid "$binary" | awk 'NR == 1 {print $2}')"
symbol_hex="$(xcrun nm -nm "$binary" | awk '/ _telemetry_fixture$/ {print "0x"$1; exit}')"
text_hex="$(
  xcrun otool -l "$binary" |
    awk '$1 == "segname" && $2 == "__TEXT" {in_text=1; next}
         in_text && $1 == "vmaddr" {print $2; exit}'
)"
[[ -n "$uuid" && -n "$symbol_hex" && -n "$text_hex" ]] || {
  printf '无法读取合成 Mach-O 的 UUID 或地址\n' >&2
  exit 1
}

symbol_address=$((symbol_hex))
text_address=$((text_hex))
offset=$((symbol_address - text_address))
architecture="$(uname -m)"

jq -cn \
  --arg uuid "$uuid" \
  --arg architecture "$architecture" \
  --argjson address "$symbol_address" \
  --argjson offset "$offset" \
  '{
    schema_version: 1,
    payload_id: ("a" * 64),
    kind: "diagnostic",
    captured_at: "2026-07-26T12:00:00Z",
    app: {
      version: "2.7.0",
      build: "symbol-test",
      distribution: "development"
    },
    platform: {
      name: "ios",
      os_version: "26.0",
      device_class: "test",
      architecture: $architecture
    },
    privacy: {
      contains_chat_content: false,
      contains_request_body: false,
      contains_response_body: false,
      contains_credentials: false,
      contains_user_identifier: false
    },
    payload: {
      hangDiagnostics: [{
        hangDuration: {value: 1, unit: "s"},
        callStackTree: {
          callStackPerThread: true,
          callStacks: [{
            threadAttributed: true,
            callStackRootFrames: [{
              binaryName: "symbol-fixture",
              binaryUUID: $uuid,
              address: $address,
              offsetIntoBinaryTextSegment: $offset,
              sampleCount: 1
            }]
          }]
        }
      }]
    }
  }' > "$input_dir/diagnostic_symbol.json"

cd "$repo_root"
go run ./cmd/telemetry-analyzer \
  --input "$input_dir" \
  --output "$output_dir" \
  --dsym "$dsym" \
  --spotlight=false > "$test_root/result.json"

grep -F 'telemetry_fixture' \
  "$output_dir/symbolicated/diagnostic_symbol.json" >/dev/null
if grep -F "$uuid" "$output_dir/missing-symbols.csv" >/dev/null; then
  printf '匹配 UUID 的 dSYM 不应出现在缺失报告\n' >&2
  exit 1
fi
jq -e '.symbolicated_frames == 1 and .missing_symbol_uuids == 0' \
  "$test_root/result.json" >/dev/null

printf 'telemetry-symbolication-test: OK\n'
