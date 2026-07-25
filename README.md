# ELS Feedback & Notification Service

面向 `ETOS LLM Studio` 的统一服务入口。当前承载反馈工单、客户端公告、意见征集、检查更新时间线、官方数据下发与匿名性能遥测。

## 功能概览
- `GET /v1/announcements`：返回已发布的客户端公告，支持 ETag 与 Cloudflare 边缘缓存
- `GET /v1/surveys`：返回已发布的意见征集，支持 ETag 与 Cloudflare 边缘缓存
- `POST /v1/surveys/:key/responses`：通过一次性 challenge、HMAC 与 PoW 保存匿名答卷
- `GET /v1/distribution/manifest`：返回客户端官方数据清单，支持 ETag 与 Cloudflare 边缘缓存
- `GET /v1/distribution/files/<sha256>/<文件名>`：下载内容寻址的不可变官方文件
- `GET /v1/updates/timeline`：使用服务端 GitHub 凭据读取 `dev` 分支提交与 CI 状态，并通过内存、ETag 和 Cloudflare 共享缓存
- `POST /v1/telemetry`：接收 iOS MetricKit 指标与诊断；不使用 PoW，不持久化来源 IP
- `GET /v1/admin/telemetry/status`：仅由管理监听器提供的遥测临时存储统计
- `GET /v1/admin/telemetry/manifest`：仅由管理监听器提供的待拉取文件清单
- `GET /v1/admin/telemetry/files/<payload_id>`：仅由管理监听器导出单个原始遥测文件
- `POST /v1/admin/telemetry/confirm`：仅在 Mac 完整校验落盘后精确确认并删除服务端文件
- `GET http://<内网地址>/`：仅由管理监听器提供的管理中心首页
- `GET http://<内网地址>/admin/announcements`：仅由管理监听器提供的公告编辑 WebUI
- `GET http://<内网地址>/admin/surveys`：仅由管理监听器提供的意见征集与私有统计 WebUI
- `GET http://<内网地址>/admin/distribution`：仅由管理监听器提供的官方数据管理 WebUI
- `POST /v1/feedback/challenge`：下发一次性 challenge（120 秒有效）
- `POST /v1/feedback/issues`：校验签名后先走 LLM 审核，再创建 GitHub Issue（可能为隐藏内容工单）
- `POST /v1/feedback/issues/:issue_number/comments`：在指定工单下发送评论（同样经过签名与 LLM 审核）
- `GET /v1/feedback/issues/:issue_number`：校验 ticket token 后返回过滤后的状态与公开评论
- `GET /v1/healthz`：健康检查
- `POST /v1/admin/self-update`：仅内网可用的自更新接口，下载指定 tag 的 Release 产物并替换当前二进制
- `GET /v1/admin/self-update/status`：仅内网可用的自动更新器状态接口

公告、意见征集、检查更新时间线、官方数据与反馈统一由 `https://feedback.els.ericterminal.com` 提供。检查更新接口只代理固定仓库的结构化提交数据，不接受任意 GitHub 地址；服务端复用 `GITHUB_TOKEN`，客户端不会接触 GitHub 凭据。意见征集定义保存在 `DATA_DIR/surveys.json`，匿名答卷保存在 `DATA_DIR/survey-responses.json`，仅包含答案、平台、应用版本、构建号、语言和提交时间，不记录 IP、设备标识或账号，也不会同步到 GitHub。客户端只能读取已发布内容，草稿、答卷和管理字段不会进入公开响应。

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
- `ANNOUNCEMENT_ADMIN_TOKEN`：服务管理口令（至少 16 个字符）；留空时不启动公告、意见征集、官方数据管理页面和管理 API
- `ADMIN_WEB_AUTH_DISABLED`：是否让内网 WebUI 自动建立管理会话（默认 `false`）；启用时仍需配置管理口令作为会话签名密钥
- `ANNOUNCEMENT_CACHE_MAX_AGE_SECONDS`：Cloudflare 边缘缓存秒数（默认 `300`，范围 `30~3600`）
- `ADMIN_LOGIN_LIMIT_PER_WINDOW`：管理页面每 IP 登录尝试上限（默认 `10`，每 15 分钟）
- `TELEMETRY_RATE_LIMIT_PER_MINUTE`：每个来源 IP 每分钟可提交的遥测批次数（默认 `30`，仅保存在进程内）
- `TELEMETRY_RETENTION_DAYS`：服务端临时遥测保留天数（默认 `30`，范围 `1~365`）
- `TELEMETRY_MAX_TOTAL_BYTES`：服务端临时遥测总字节上限（默认 `2147483648`）

当配置 `REDIS_ADDR` 且可连通时，限流与去重会自动升级为 Redis 全局模式；连接失败会自动回退到内存模式。

性能遥测是一个独立边界：它不使用反馈 challenge、PoW、Redis 限流或 App
Attest。来源 IP 只进入进程内一分钟固定窗口限流键，既不写入遥测文件，也不写入
磁盘去重状态。服务端严格校验 schema、原始 payload SHA-256、时间、应用与平台
元数据，并要求聊天原文、API 请求体、API 响应体、凭据和用户标识五项隐私声明
全部为 `false`。

每次请求最多包含 16 条遥测且总请求体不超过 4 MiB，不接受压缩请求体。通过
校验的原始 envelope 按接收日期写入
`DATA_DIR/telemetry/records/YYYY-MM-DD/`，持久化去重键避免客户端重试产生副本。
默认临时保留 30 天、总量上限 2 GiB；启动、每次写入及每六小时会执行清理。
超出配额时先删除最旧普通指标，再删除最旧诊断；新普通指标不会为了腾出空间而
淘汰已有诊断。

## 内网管理页面

配置 `ANNOUNCEMENT_ADMIN_TOKEN` 和受保护的管理监听地址。仅在管理端口已经由局域网、防火墙或 VPN 隔离时，才启用 WebUI 免登录：

```text
ADMIN_LISTEN_ADDR=192.168.31.102:8521
ADMIN_WEB_AUTH_DISABLED=true
```

然后在同一局域网内访问：

```text
http://192.168.31.102:8521/
http://192.168.31.102:8521/admin/announcements
http://192.168.31.102:8521/admin/surveys
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

意见征集页面支持：

- 创建单选、多选与允许自定义输入的问题
- 保存草稿或按语言、平台和构建号开始征集
- 查看只存在于服务端本地的选项统计和自定义回答
- 复制语言版本；收到首份答卷后冻结题目，只允许开始或停止征集

客户端通过 PoW 提交答卷，成功提交或主动关闭后不会再次展示同一条征集。客户端只显示简短的“匿名提交”提示。

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

./els-feedback-proxy survey list
./els-feedback-proxy survey create --file survey.json
./els-feedback-proxy survey update --key <征集-key> --file survey.json
./els-feedback-proxy survey results --key <征集-key>
./els-feedback-proxy survey delete --key <征集-key>

./els-feedback-proxy distribution list
./els-feedback-proxy distribution upload --name <名称> --path /Documents/<目录> --file <本地文件>
./els-feedback-proxy distribution update --key <数据-key> --name <名称> --path /Documents/<目录> [--file <替换文件>]
./els-feedback-proxy distribution delete --key <数据-key>

./els-feedback-proxy telemetry status
./els-feedback-proxy telemetry manifest
./els-feedback-proxy telemetry export --payload-id <SHA-256>
printf '%s' '{"payload_ids":["<SHA-256>"]}' | ./els-feedback-proxy telemetry confirm --file -
```

默认管理 API 地址为 `http://127.0.0.1:8521`。使用其他监听地址时，可以设置 `ELS_ADMIN_URL`，也可以为单次命令传入 `--admin-url`：

```bash
ELS_ADMIN_URL=http://192.168.31.102:8521 ./els-feedback-proxy announcement list
```

公告与意见征集的 `create`、`update` 支持用 `--file -` 从标准输入读取 JSON。官方数据 `upload` 和 `update` 可加 `--disabled` 暂停公开下发。所有成功响应均输出格式化 JSON，方便人工查看或继续交给其他命令处理。完整用法可通过对应命令的 `--help` 查看。

遥测 `export` 会逐字节输出原始 JSON，不做格式化。只有接收端已经核对清单中的
`size_bytes`、`file_sha256` 且确认文件是有效 JSON 后，才可调用 `confirm`。
确认接口按 `payload_id` 精确删除，并允许网络重试导致的重复确认。

## Mac 遥测归档与分析

`scripts/telemetry-pull.sh` 是需要在 iTerm 中保持运行的前台脚本，不会安装
LaunchAgent 或后台服务。它通过 SSH 在服务器上加载 `.env` 并调用管理 CLI，
因此 `ANNOUNCEMENT_ADMIN_TOKEN` 不会进入 Mac 的参数、文件或日志。长期归档
默认写入运行命令时所在的目录，也可通过 `--archive` 显式指定。

执行单轮拉取或保持前台定时拉取：

```bash
cd /path/to/ETOS-Telemetry
/path/to/els-feedback-proxy/scripts/telemetry-pull.sh --host <SSH用户@服务器> --once
/path/to/els-feedback-proxy/scripts/telemetry-pull.sh --host <SSH用户@服务器> --interval 900
```

脚本会保存每轮服务端清单，并按服务端 UTC 接收日期写入
`raw/YYYY-MM-DD/`。每个文件必须通过清单字节数、文件 SHA-256、JSON schema
与五项隐私声明校验，之后才会从 `.partial` 原子替换为正式文件。所有文件完成
校验后，脚本最多每 512 个 ID 调用一次精确确认；中途中断时，下轮会重新验证已
落盘文件再确认。

分析脚本会按 App 构建、分发渠道、iOS 版本和设备类型生成原始索引，
按日期与构建的样本分组、MetricKit/MXSignpost 直方图的 P50/P90/P99、
CPU/内存/磁盘/网络等测量值、Signpost 调用次数、诊断列表与完整调用栈、
分析器未识别字段和解析失败清单、Markdown 摘要、逐文件符号化 JSON 与缺失
dSYM UUID 清单：

```bash
/path/to/els-feedback-proxy/scripts/telemetry-analyze.sh \
  --xcarchive '/path/to/Xcode Cloud Build.xcarchive'
```

可以重复传入 `--xcarchive` 或 `--dsym`。工具先使用 `dwarfdump --uuid` 做严格
UUID 匹配，再用 `atos` 将地址转换为函数和源码行；没有显式路径时默认按 UUID
使用 Spotlight 查找本机 dSYM。Apple 说明只有构建 UUID 匹配的 dSYM 才能正确
符号化，相关规则见
[Adding identifiable symbol names to a crash report](https://developer.apple.com/documentation/xcode/adding-identifiable-symbol-names-to-a-crash-report)。
直方图分位数以桶上界近似。跨构建比较时应保持分发渠道、iOS 版本、设备类型
与单位一致；摘要中的长尾和高频项是定位线索，不会自动证明某段代码就是根因。

本地回归包含损坏传输、重复确认、Swift 生成请求到 Go 再到 Mac 归档分析，以及
真实临时 dSYM/UUID/`atos` 符号化：

```bash
scripts/tests/telemetry-pull-test.sh
scripts/tests/telemetry-e2e-local.sh
scripts/tests/telemetry-symbolication-test.sh
```

## Cloudflare 缓存与防护

服务会为公告、意见征集定义、官方数据清单和文件返回 `Cloudflare-CDN-Cache-Control`。公告、征集与清单提供内容 ETag；文件 URL 包含 SHA-256，内容变化后 URL 也会变化，因此可以长期不可变缓存。建议在 Cloudflare Cache Rules 中缓存这些只读路径，同时让答卷和反馈提交接口保持绕过缓存。

推荐规则：

- `/v1/announcements`：Eligible for cache，Edge TTL 遵循源站缓存控制
- `/v1/surveys`：Eligible for cache，Edge TTL 遵循源站缓存控制
- `/v1/distribution/manifest`：Eligible for cache，Edge TTL 遵循源站缓存控制
- `/v1/distribution/files/*`：Eligible for cache，Edge TTL 遵循源站缓存控制
- `/v1/surveys/*`、`/v1/feedback/*`、`/v1/telemetry`、`/v1/github/webhooks`：Bypass cache；精确的 `/v1/surveys` 读取规则应排在通配规则之前
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
