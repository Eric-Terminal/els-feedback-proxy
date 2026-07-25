#!/usr/bin/env bash

set -euo pipefail

# 合成端到端测试把 SSH 的远端命令放回本机执行，不接触真实服务器。
exec /bin/bash -c "${*: -1}"
