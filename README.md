# ELS Feedback Proxy

面向 `ETOS LLM Studio` 的独立反馈代理服务。客户端通过本服务提交反馈和查询工单状态，本服务再调用 GitHub Issues API。

## 功能概览
- `POST /v1/feedback/challenge`：下发一次性 challenge（120 秒有效）
- `POST /v1/feedback/issues`：校验签名后创建 GitHub Issue
- `GET /v1/feedback/issues/:issue_number`：校验 ticket token 后返回过滤后的状态与公开评论
- `GET /v1/healthz`：健康检查

## 安全策略（方案B）
- UA 校验：必须包含 `ETOS LLM Studio`（兼容 `%20` 编码）
- 限流（固定窗口 15 分钟）
  - challenge：每 IP 30 次
  - 提交：每 IP 6 次
  - 查询：每 IP 60 次
- challenge + HMAC 签名
  - 时间窗容忍：`±90 秒`
  - challenge 单次使用
  - 签名失败累计阈值：5 次，封禁 10 分钟
- 重复提交拦截：同 IP + 同内容摘要，10 分钟内重复返回 `409`

## 环境变量
- `PORT`：监听端口（默认 `8080`）
- `GITHUB_TOKEN`：Fine-grained PAT（必填）
- `GITHUB_OWNER`：默认 `Eric-Terminal`
- `GITHUB_REPO`：默认 `ETOS-LLM-Studio`
- `DATA_DIR`：本地数据目录（默认 `./data`）
- `REQUIRED_UA_KEYWORD`：默认 `ETOS LLM Studio`

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
