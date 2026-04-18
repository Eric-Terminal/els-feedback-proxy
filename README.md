# ELS Feedback Proxy

面向 `ETOS LLM Studio` 的独立反馈代理服务。客户端通过本服务提交反馈和查询工单状态，本服务再调用 GitHub Issues API。

## 功能概览
- `POST /v1/feedback/challenge`：下发一次性 challenge（120 秒有效）
- `POST /v1/feedback/issues`：校验签名后先走 LLM 审核，再创建 GitHub Issue（可能为隐藏内容工单）
- `POST /v1/feedback/issues/:issue_number/comments`：在指定工单下发送评论（同样经过签名与 LLM 审核）
- `GET /v1/feedback/issues/:issue_number`：校验 ticket token 后返回过滤后的状态与公开评论
- `GET /v1/healthz`：健康检查
- `POST /v1/admin/self-update`：受保护的自更新 webhook，下载指定 tag 的 Release 产物并替换当前二进制
- `GET /v1/admin/self-update/status`：查看自动更新器状态（同样需要鉴权）

## 安全策略（方案B）
- UA 校验：必须包含 `ETOS LLM Studio`（兼容 `%20` 编码）
- 限流（固定窗口 15 分钟）
  - challenge：每 IP 30 次
  - 提交：每 IP 6 次
  - 查询：每 IP 60 次
- PoW（工作量证明）
  - challenge 下发 `pow_bits` 与 `pow_salt`
  - 提交时必须附带 `X-ELS-PoW-Nonce`（可选附带 `X-ELS-PoW-Hash`）
  - 服务端验证 `SHA256(METHOD\\nPATH\\nTIMESTAMP\\nBODY_HASH\\nCHALLENGE_ID\\nPOW_SALT\\nPOW_NONCE)` 前导零位
- challenge + HMAC 签名
  - 时间窗容忍：`±90 秒`
  - challenge 单次使用
  - 签名失败累计阈值：5 次，封禁 10 分钟
- 重复提交拦截：同 IP + 同内容摘要，10 分钟内重复返回 `409`
- LLM 审核
  - 非违规内容优先放行
  - 违规或审核异常（最多重试 3 次后仍失败）会走“隐藏内容工单”
  - 原文写入 `DATA_DIR/review-blocked/` 单条 Markdown 留档，GitHub 仅保留 archive_id 提示

## 环境变量
- `PORT`：监听端口（默认 `8080`）
- `GITHUB_TOKEN`：Fine-grained PAT（必填）
- `GITHUB_OWNER`：默认 `Eric-Terminal`
- `GITHUB_REPO`：默认 `ETOS-LLM-Studio`
- `GITHUB_TOKEN_LOGIN`：令牌所属账号 login（可选，不填会尝试自动调用 GitHub `/user` 获取）
- `DEVELOPER_GITHUB_LOGINS`：额外开发者账号列表（可选，逗号分隔）
- `DATA_DIR`：本地数据目录（默认 `./data`）
- `REQUIRED_UA_KEYWORD`：默认 `ETOS LLM Studio`
- `POW_DIFFICULTY_BITS`：PoW 难度（默认 `20`，范围 `0~30`）
- `MODERATION_ENABLED`：是否启用审核（默认 `true`）
- `MODERATION_API_BASE_URL`：审核 API 基础地址（必填，OpenAI 兼容接口）
- `MODERATION_API_KEY`：审核 API Key（必填）
- `MODERATION_MODEL`：审核模型名（必填）
- `MODERATION_TIMEOUT_SECONDS`：单次审核超时秒数（默认 `15`）
- `MODERATION_MAX_RETRIES`：审核失败重试次数（默认 `3`）
- `MODERATION_TEMPERATURE`：审核温度（默认 `0`）
- `REDIS_ADDR`：Redis 地址（可选，示例 `127.0.0.1:6379`）
- `REDIS_PASSWORD`：Redis 密码（可选）
- `REDIS_DB`：Redis DB（默认 `0`）
- `REDIS_KEY_PREFIX`：Redis Key 前缀（默认 `els-feedback`）
- `COMMENT_LIMIT_PER_WINDOW`：评论限流（默认 `20`，每 15 分钟）
- `SELF_UPDATE_SECRET`：自动更新 webhook 密钥；留空则禁用自动更新接口
- `SELF_UPDATE_REPO_OWNER`：自动更新下载源仓库 owner（默认 `Eric-Terminal`）
- `SELF_UPDATE_REPO_NAME`：自动更新下载源仓库名（默认 `els-feedback-proxy`）
- `SELF_UPDATE_GITHUB_TOKEN`：自动更新读取 Release 时使用的 GitHub Token（可选，公开仓库可不填）
- `SELF_UPDATE_SERVICE_NAME`：更新完成后重启的 systemd 服务名（默认 `els-feedback-proxy`）
- `SELF_UPDATE_WORKING_DIR`：自动更新工作目录（可选，默认取当前可执行文件所在目录）

当配置 `REDIS_ADDR` 且可连通时，限流与去重会自动升级为 Redis 全局模式；连接失败会自动回退到内存模式。

## 本地运行
```bash
go mod tidy
go run ./cmd/server
```

## Docker 运行
```bash
docker build -t els-feedback-proxy .
docker run --rm -p 8080:8080 \
  -e GITHUB_TOKEN=your_token \
  -e GITHUB_OWNER=Eric-Terminal \
  -e GITHUB_REPO=ETOS-LLM-Studio \
  els-feedback-proxy
```

## Docker Compose（含 Redis）
```bash
docker compose up -d --build
```

## 自动发布与自动更新
推荐链路：

1. 本仓库 push 新 tag，例如 `v0.1.4`
2. GitHub Actions 运行测试并通过 GoReleaser 生成 Release 资产
3. Workflow 向生产环境 `POST /v1/admin/self-update`
4. 服务端根据 tag 调用 GitHub Releases API，自动下载适用于当前机器的 `linux_amd64` 归档与 `checksums.txt`
5. 服务端校验 SHA256、备份当前二进制、原地替换并调度 `systemctl restart`

这样服务器不需要安装 Go 工具链，也不会在生产机上重新编译。

### GitHub Actions Secrets
- `DEPLOY_WEBHOOK_URL`：生产 webhook 地址，例如 `https://feedback.els.ericterminal.com/v1/admin/self-update`
- `DEPLOY_WEBHOOK_SECRET`：与服务器 `SELF_UPDATE_SECRET` 相同的密钥

### 健康检查返回
`GET /v1/healthz` 现在会额外返回：
- `version`
- `commit`
- `build_time`
- `self_update_enabled`

## 客户端签名串
提交反馈时签名文本格式：

```text
METHOD
PATH
TIMESTAMP
SHA256(BODY)
NONCE
```

默认 `METHOD=POST`，`PATH=/v1/feedback/issues`。

## 客户端 PoW 串
提交反馈时 PoW 文本格式：

```text
METHOD
PATH
TIMESTAMP
SHA256(BODY)
CHALLENGE_ID
POW_SALT
POW_NONCE
```

评论提交时签名串与 PoW 串与创建工单一致，仅 `PATH` 改为：

```text
/v1/feedback/issues/{issue_number}/comments
```

## 审核响应说明
- 正常放行：`200`
- 隐藏内容工单：`202`
  - 额外字段：
    - `moderation_blocked: true`
    - `archive_id: string`
    - `moderation_message: string`

评论接口同样适用 `200/202` 语义：
- `200`：评论已公开发布
- `202`：评论已被隐藏并改发占位评论（附 `archive_id`）

## 令牌账号与仓库所有者分离说明
可以使用“小号 token + 主号仓库”模式：
- `GITHUB_TOKEN` 使用小号 PAT
- `GITHUB_OWNER/GITHUB_REPO` 指向主仓库
- 小号需具备目标仓库 Issues 读写权限

服务会用 `GITHUB_TOKEN_LOGIN`（或自动识别 login）与 `DEVELOPER_GITHUB_LOGINS` 标记开发者评论身份，便于客户端区分“开发者回复”。
