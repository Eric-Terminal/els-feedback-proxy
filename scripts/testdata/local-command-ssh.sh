#!/usr/bin/env bash

set -euo pipefail

# 合成端到端测试把 SSH 的远端命令放回本机执行，不接触真实服务器。
if [[ " $* " == *" -n "* ]]; then
  exec /bin/bash -c "${*: -1}" </dev/null
fi
exec /bin/bash -c "${*: -1}"
