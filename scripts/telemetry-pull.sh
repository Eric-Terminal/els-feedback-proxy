#!/usr/bin/env bash

set -uo pipefail

archive_root="${ETOS_TELEMETRY_ARCHIVE_ROOT:-$PWD}"
ssh_host="${ETOS_TELEMETRY_SSH_HOST:-}"
remote_dir="${ETOS_TELEMETRY_REMOTE_DIR:-/root/els-feedback-proxy}"
interval_seconds=900
run_once=false
active_temp_files=()
lock_dir=""

usage() {
  cat <<'USAGE'
用法:
  scripts/telemetry-pull.sh --host USER@HOST [--once]
  scripts/telemetry-pull.sh --host USER@HOST --interval 900

选项:
  --host USER@HOST     SSH 目标；也可设置 ETOS_TELEMETRY_SSH_HOST
  --archive PATH       长期归档目录（默认当前工作目录）
  --remote-dir PATH    服务器程序目录（默认 /root/els-feedback-proxy）
  --interval SECONDS   前台循环间隔（默认 900）
  --once               只执行一轮
  --help               显示帮助

脚本始终在前台运行。管理口令只在远端通过 .env 注入服务端 CLI，不会传到 Mac。
USAGE
}

log() {
  printf '[%s] %s\n' "$(date -u '+%Y-%m-%dT%H:%M:%SZ')" "$*" >&2
}

fail() {
  log "错误: $*"
  return 1
}

cleanup_temp_files() {
  local path
  for path in "${active_temp_files[@]:-}"; do
    if [[ -n "$path" && -f "$path" ]]; then
      rm -f -- "$path"
    fi
  done
  active_temp_files=()
}

cleanup() {
  cleanup_temp_files
  if [[ -n "$lock_dir" && -d "$lock_dir" ]]; then
    rmdir -- "$lock_dir" 2>/dev/null || true
  fi
}

handle_signal() {
  cleanup
  trap - EXIT
  exit 130
}

require_command() {
  command -v "$1" >/dev/null 2>&1 || fail "缺少命令: $1"
}

file_size() {
  local path="$1"
  if stat -f '%z' "$path" >/dev/null 2>&1; then
    stat -f '%z' "$path"
  else
    stat -c '%s' "$path"
  fi
}

file_sha256() {
  shasum -a 256 "$1" | awk '{print $1}'
}

remote_cli() {
  local arguments="$1"
  # 管理口令只在远端 shell 读取，不能通过 SSH 输出或本机环境传递。
  ssh -o BatchMode=yes -o ConnectTimeout=15 "$ssh_host" \
    "cd $remote_dir && set -a && . ./.env && set +a && ./els-feedback-proxy telemetry $arguments"
}

verify_telemetry_file() {
  local path="$1"
  local payload_id="$2"
  local expected_size="$3"
  local expected_sha="$4"
  local actual_size
  local actual_sha

  actual_size="$(file_size "$path")" || return 1
  [[ "$actual_size" == "$expected_size" ]] ||
    fail "文件大小不一致: ${payload_id}（期望 ${expected_size}，实际 ${actual_size}）" ||
    return 1
  actual_sha="$(file_sha256 "$path")" || return 1
  [[ "$actual_sha" == "$expected_sha" ]] ||
    fail "文件 SHA-256 不一致: $payload_id" ||
    return 1
  jq -e --arg payload_id "$payload_id" '
    .schema_version == 1 and
    .payload_id == $payload_id and
    (.kind == "metric" or .kind == "diagnostic") and
    (.payload | type == "object") and
    .privacy.contains_chat_content == false and
    .privacy.contains_request_body == false and
    .privacy.contains_response_body == false and
    .privacy.contains_credentials == false and
    .privacy.contains_user_identifier == false
  ' "$path" >/dev/null || fail "遥测 JSON 或隐私声明无效: $payload_id"
}

confirm_verified_files() {
  local ids_file="$1"
  local total
  local offset=0
  local batch_size=512
  local body_file
  local response_file

  total="$(wc -l < "$ids_file" | tr -d '[:space:]')"
  while (( offset < total )); do
    body_file="$(mktemp "$archive_root/.staging/confirm-body.XXXXXX")" || return 1
    active_temp_files+=("$body_file")
    response_file="$(mktemp "$archive_root/.staging/confirm-response.XXXXXX")" || return 1
    active_temp_files+=("$response_file")

    jq -Rn \
      --argjson start "$offset" \
      --argjson size "$batch_size" \
      '[inputs] | {payload_ids: .[$start:($start + $size)]}' \
      "$ids_file" > "$body_file" || return 1
    if ! remote_cli "confirm --file -" < "$body_file" > "$response_file"; then
      fail "服务器确认失败；本地文件已保留，下轮会安全重试"
      return 1
    fi
    if ! jq -e --slurpfile request "$body_file" '
      (.missing_payload_ids | length) == 0 and
      ((.confirmed_payload_ids | sort) == ($request[0].payload_ids | sort))
    ' "$response_file" >/dev/null; then
      fail "服务器确认响应不完整；停止本轮"
      return 1
    fi
    offset=$((offset + batch_size))
  done
}

run_cycle() {
  local staging_dir="$archive_root/.staging"
  local manifest_tmp
  local ids_tmp
  local received_date
  local payload_id
  local kind
  local size_bytes
  local file_sha
  local date_dir
  local target
  local partial
  local manifest_date
  local manifest_target
  local count=0

  cleanup_temp_files
  manifest_tmp="$(mktemp "$staging_dir/manifest.XXXXXX")" || return 1
  active_temp_files+=("$manifest_tmp")
  ids_tmp="$(mktemp "$staging_dir/confirmed-ids.XXXXXX")" || return 1
  active_temp_files+=("$ids_tmp")

  log "读取服务端遥测清单"
  if ! remote_cli "manifest" > "$manifest_tmp"; then
    fail "读取服务端清单失败"
    return 1
  fi
  if ! jq -e '
    .schema_version == 1 and
    (.entries | type == "array") and
    all(.entries[];
      (.payload_id | test("^[0-9a-f]{64}$")) and
      (.kind == "metric" or .kind == "diagnostic") and
      (.received_at | test("^[0-9]{4}-[0-9]{2}-[0-9]{2}T")) and
      (.size_bytes | type == "number") and .size_bytes > 0 and
      (.file_sha256 | test("^[0-9a-f]{64}$"))
    ) and
    (([.entries[].payload_id] | unique | length) == (.entries | length))
  ' "$manifest_tmp" >/dev/null; then
    fail "服务端清单结构或字段无效"
    return 1
  fi

  manifest_date="$(date -u '+%Y-%m-%d')"
  mkdir -p "$archive_root/manifests/$manifest_date" || return 1
  manifest_target="$archive_root/manifests/$manifest_date/manifest-$(date -u '+%H%M%S')-$$.json"
  active_temp_files+=("$manifest_target.partial")
  cp "$manifest_tmp" "$manifest_target.partial" || return 1
  mv "$manifest_target.partial" "$manifest_target" || return 1

  while IFS=$'\t' read -r payload_id kind received_date size_bytes file_sha; do
    [[ -n "$payload_id" ]] || continue
    if [[ ! "$received_date" =~ ^[0-9]{4}-[0-9]{2}-[0-9]{2}$ ]]; then
      fail "清单日期无效: $received_date"
      return 1
    fi
    date_dir="$archive_root/raw/$received_date"
    mkdir -p "$date_dir" || return 1
    target="$date_dir/${kind}_${payload_id}.json"

    if [[ -f "$target" ]]; then
      verify_telemetry_file "$target" "$payload_id" "$size_bytes" "$file_sha" ||
        return 1
    else
      partial="$date_dir/.${kind}_${payload_id}.$$.partial"
      active_temp_files+=("$partial")
      log "拉取 $kind $payload_id"
      if ! remote_cli "export --payload-id $payload_id" > "$partial"; then
        fail "导出遥测失败: $payload_id"
        return 1
      fi
      verify_telemetry_file "$partial" "$payload_id" "$size_bytes" "$file_sha" ||
        return 1
      mv "$partial" "$target" || return 1
    fi
    printf '%s\n' "$payload_id" >> "$ids_tmp" || return 1
    count=$((count + 1))
  done < <(
    jq -r '
      .entries[] |
      [.payload_id, .kind, (.received_at[0:10]), (.size_bytes | tostring), .file_sha256] |
      @tsv
    ' "$manifest_tmp"
  )

  # 只有本轮所有文件都完成持久化校验，才进入精确确认阶段。
  if (( count > 0 )); then
    confirm_verified_files "$ids_tmp" || return 1
  fi
  log "本轮完成：校验并确认 $count 条；清单保存到 $manifest_target"
  cleanup_temp_files
  return 0
}

while (($# > 0)); do
  case "$1" in
    --host)
      [[ $# -ge 2 ]] || { usage >&2; exit 2; }
      ssh_host="$2"
      shift 2
      ;;
    --archive)
      [[ $# -ge 2 ]] || { usage >&2; exit 2; }
      archive_root="$2"
      shift 2
      ;;
    --remote-dir)
      [[ $# -ge 2 ]] || { usage >&2; exit 2; }
      remote_dir="$2"
      shift 2
      ;;
    --interval)
      [[ $# -ge 2 ]] || { usage >&2; exit 2; }
      interval_seconds="$2"
      shift 2
      ;;
    --once)
      run_once=true
      shift
      ;;
    --help|-h)
      usage
      exit 0
      ;;
    *)
      printf '无法识别的参数: %s\n' "$1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

[[ -n "$ssh_host" ]] || { fail "必须通过 --host 或 ETOS_TELEMETRY_SSH_HOST 指定 SSH 目标"; exit 2; }
[[ "$ssh_host" =~ ^[A-Za-z0-9._@:%-]+$ ]] || { fail "SSH 目标包含不安全字符"; exit 2; }
[[ "$remote_dir" =~ ^/[A-Za-z0-9._/-]+$ ]] || { fail "远端目录必须是无空格的绝对路径"; exit 2; }
[[ "$interval_seconds" =~ ^[0-9]+$ ]] && (( interval_seconds >= 60 )) ||
  { fail "循环间隔必须是至少 60 秒的整数"; exit 2; }

require_command ssh || exit 1
require_command jq || exit 1
require_command shasum || exit 1
require_command awk || exit 1

mkdir -p "$archive_root/.staging" "$archive_root/raw" "$archive_root/manifests" || exit 1
lock_dir="$archive_root/.pull.lock"
if ! mkdir "$lock_dir" 2>/dev/null; then
  fail "已有另一个拉取进程持有锁: $lock_dir"
  exit 1
fi
trap cleanup EXIT
trap handle_signal INT TERM

if $run_once; then
  run_cycle
  exit $?
fi

log "前台定时拉取已启动，间隔 ${interval_seconds}s；按 Ctrl-C 停止"
while true; do
  if ! run_cycle; then
    log "本轮失败；本地已校验文件会保留，${interval_seconds}s 后重试"
  fi
  sleep "$interval_seconds"
done
