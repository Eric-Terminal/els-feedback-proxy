# ELS Feedback & Notification Service

面向 `ETOS LLM Studio` 的统一服务入口。当前承载反馈工单、客户端公告与官方数据下发，后续遥测等服务可继续按 `/v1/<module>` 拆分接入同一域名。

## 功能概览
- `GET /v1/announcements`：返回已发布的客户端公告，支持 ETag 与 Cloudflare 边缘缓存
- `GET /v1/distribution/manifest`：返回客户端官方数据清单，支持 ETag 与 Cloudflare 边缘缓存
- `GET /v1/distribution/files/<sha256>/<文件名>`：下载内容寻址的不可变官方文件
- `GET http://<内网地址>/admin/announcements`：仅由管理监听器提供的公告编辑 WebUI
- `GET http://<内网地址>/admin/distribution`：仅由管理监听器提供的官方数据管理 WebUI
- `POST /v1/feedback/challenge`：下发一次性 challenge（120 秒有效）
- `POST /v1/feedback/issues`：校验签名后先走 LLM 审核，再创建 GitHub Issue（可能为隐藏内容工单）
- `POST /v1/feedback/issues/:issue_number/comments`：在指定工单下发送评论（同样经过签名与 LLM 审核）
- `GET /v1/feedback/issues/:issue_number`：校验 ticket token 后返回过滤后的状态与公开评论
- `GET /v1/healthz`：健康检查
- `POST /v1/admin/self-update`：仅内网可用的自更新接口，下载指定 tag 的 Release 产物并替换当前二进制
- `GET /v1/admin/self-update/status`：仅内网可用的自动更新器状态接口

公告、官方数据与反馈统一由 `https://feedback.els.ericterminal.com` 提供。公告管理数据保存在 `DATA_DIR/announcements.json`；官方数据元信息保存在 `DATA_DIR/distribution.json`，文件内容保存在 `DATA_DIR/distribution-files/`。客户端只能读取已发布内容，草稿和管理字段不会进入公开响应。

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
- `ADMIN_LISTEN_ADDR`：管理服务监听地址；启用公告管理或自更新时必填，例如 `:8521`、`127.0.0.1:8521` 或 `192.168.31.102:8521`
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
- `TRUSTED_PROXY_CIDRS`：可信反向代理网段（默认仅本机）；Tunnel 在其他主机时应填写其内网地址，例如 `192.168.31.101/32`
- `COMMENT_LIMIT_PER_WINDOW`：评论限流（默认 `20`，每 15 分钟）
- `SELF_UPDATE_SECRET`：自动更新 webhook 密钥；留空则禁用自动更新接口
- `SELF_UPDATE_REPO_OWNER`：自动更新下载源仓库 owner（默认 `Eric-Terminal`）
- `SELF_UPDATE_REPO_NAME`：自动更新下载源仓库名（默认 `els-feedback-proxy`）
- `SELF_UPDATE_GITHUB_TOKEN`：自动更新读取 Release 时使用的 GitHub Token（可选，公开仓库可不填）
- `SELF_UPDATE_SERVICE_NAME`：更新完成后重启的 systemd 服务名（默认 `els-feedback-proxy`）
- `SELF_UPDATE_WORKING_DIR`：自动更新工作目录（可选，默认取当前可执行文件所在目录）
- `ANNOUNCEMENT_ADMIN_TOKEN`：服务管理口令（至少 16 个字符）；留空时不启动公告、官方数据管理页面和管理 API
- `ANNOUNCEMENT_CACHE_MAX_AGE_SECONDS`：Cloudflare 边缘缓存秒数（默认 `300`，范围 `30~3600`）
- `ADMIN_LOGIN_LIMIT_PER_WINDOW`：管理页面每 IP 登录尝试上限（默认 `10`，每 15 分钟）

当配置 `REDIS_ADDR` 且可连通时，限流与去重会自动升级为 Redis 全局模式；连接失败会自动回退到内存模式。

## 内网管理页面

配置 `ANNOUNCEMENT_ADMIN_TOKEN` 和受保护的管理监听地址：

```text
ADMIN_LISTEN_ADDR=192.168.31.102:8521
```

然后在同一局域网内访问：

```text
http://192.168.31.102:8521/admin/announcements
http://192.168.31.102:8521/admin/distribution
```

公网监听器不会注册 `/admin/*` 和 `/v1/admin/*`。管理监听地址完全由部署配置决定；当前家庭服务器通过防火墙、端口映射和 Cloudflare Tunnel 路由保证 `8521` 不暴露到公网。

公告页面支持：

- 创建、编辑和删除公告
- 保存草稿或立即发布
- 为同一公告复制不同语言版本
- 限制 iOS、watchOS、最低构建号和最高构建号
- 预览客户端标题、正文与通知级别

公告编号相同的条目会被客户端视为同一公告的语言版本。客户端按语言选择最佳匹配项；需要同时发布多条独立公告时，使用不同编号。

官方数据页面支持：

- 上传、替换、停用和删除官方文件
- 为每个文件配置显示名称与客户端 `Documents` 内的目标目录
- 展示文件大小、SHA-256 和公开下载地址
- 单文件最大 32 MiB；目标目录不允许跳出客户端 `Documents`

公开下发的数据可以被任何客户端和访问者下载。不要在这里上传拥有服务端权限的 API Key、管理口令或其他机密；需要保密的能力应由服务端代为调用。

## 管理 CLI

CLI 通过独立管理监听器调用与 WebUI 相同的管理 API，不会直接修改数据文件。通过 SSH 登录服务器后，先将 `ANNOUNCEMENT_ADMIN_TOKEN` 注入当前进程环境，再执行：

```bash
./els-feedback-proxy announcement list
./els-feedback-proxy announcement create --file announcement.json
./els-feedback-proxy announcement update --key <公告-key> --file announcement.json
./els-feedback-proxy announcement delete --key <公告-key>

./els-feedback-proxy distribution list
./els-feedback-proxy distribution upload --name <名称> --path /Documents/<目录> --file <本地文件>
./els-feedback-proxy distribution update --key <数据-key> --name <名称> --path /Documents/<目录> [--file <替换文件>]
./els-feedback-proxy distribution delete --key <数据-key>
```

默认管理 API 地址为 `http://127.0.0.1:8521`。使用其他监听地址时，可以设置 `ELS_ADMIN_URL`，也可以为单次命令传入 `--admin-url`：

```bash
ELS_ADMIN_URL=http://192.168.31.102:8521 ./els-feedback-proxy announcement list
```

公告 `create` 和 `update` 的 `--file` 支持使用 `-` 从标准输入读取 JSON。官方数据 `upload` 和 `update` 可加 `--disabled` 暂停公开下发。所有成功响应均输出格式化 JSON，方便人工查看或继续交给其他命令处理。完整用法可通过对应命令的 `--help` 查看。

## Cloudflare 缓存与防护

服务会为公告、官方数据清单和文件返回 `Cloudflare-CDN-Cache-Control`。公告与清单提供内容 ETag；文件 URL 包含 SHA-256，内容变化后 URL 也会变化，因此可以长期不可变缓存。建议在 Cloudflare Cache Rules 中缓存这些只读路径，同时让反馈提交接口保持绕过缓存。

推荐规则：

- `/v1/announcements`：Eligible for cache，Edge TTL 遵循源站缓存控制
- `/v1/distribution/manifest`：Eligible for cache，Edge TTL 遵循源站缓存控制
- `/v1/distribution/files/*`：Eligible for cache，Edge TTL 遵循源站缓存控制
- `/v1/feedback/*`、`/v1/github/webhooks`：Bypass cache
- 在 Cloudflare Rate Limiting Rules 中为 challenge、提交和评论入口设置边缘限流；源站仍保留 Redis 限流与 PoW 作为第二层保护

Cloudflare Tunnel 隐藏了家庭网络源站地址。不要把公开端口或管理端口映射到家庭公网；管理端口只能通过局域网直连。

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
3. GitHub `release` webhook 向生产环境发送 `published` 事件
4. 服务端校验 webhook 签名后，根据 tag 调用 GitHub Releases API，自动下载适用于当前机器的归档与 `checksums.txt`
5. 服务端校验 SHA256、备份当前二进制、原地替换并调度 `systemctl restart`

这样服务器不需要安装 Go 工具链，也不会在生产机上重新编译。

### GitHub Webhook

- Payload URL：`https://feedback.els.ericterminal.com/v1/github/webhooks`
- Content type：`application/json`
- Event：`Releases`
- Secret：与服务器 `GITHUB_WEBHOOK_SECRET` 相同

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
