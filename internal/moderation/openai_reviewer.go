package moderation

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// OpenAIReviewerConfig 是审核客户端配置。
type OpenAIReviewerConfig struct {
	BaseURL     string
	APIKey      string
	Model       string
	Timeout     time.Duration
	MaxRetries  int
	Temperature float64
}

// OpenAIReviewer 使用 OpenAI 兼容接口完成文本审核。
type OpenAIReviewer struct {
	endpoint    string
	apiKey      string
	model       string
	timeout     time.Duration
	maxRetries  int
	temperature float64
	httpClient  *http.Client
}

func NewOpenAIReviewer(cfg OpenAIReviewerConfig) *OpenAIReviewer {
	retries := cfg.MaxRetries
	if retries <= 0 {
		retries = 3
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	base := strings.TrimSpace(strings.TrimRight(cfg.BaseURL, "/"))
	if !strings.HasSuffix(base, "/v1") {
		base += "/v1"
	}

	return &OpenAIReviewer{
		endpoint:    base + "/chat/completions",
		apiKey:      cfg.APIKey,
		model:       cfg.Model,
		timeout:     timeout,
		maxRetries:  retries,
		temperature: cfg.Temperature,
		httpClient: &http.Client{
			Timeout: timeout + 2*time.Second,
		},
	}
}

func (r *OpenAIReviewer) Review(ctx context.Context, input ReviewInput) (Decision, error) {
	var lastErr error
	for attempt := 1; attempt <= r.maxRetries; attempt++ {
		decision, err := r.reviewOnce(ctx, input)
		if err == nil {
			return decision, nil
		}
		lastErr = err
	}
	return Decision{}, fmt.Errorf("审核请求失败，已重试 %d 次: %w", r.maxRetries, lastErr)
}

func (r *OpenAIReviewer) reviewOnce(ctx context.Context, input ReviewInput) (Decision, error) {
	reviewCtx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	payload := map[string]any{
		"model":       r.model,
		"temperature": r.temperature,
		"messages": []map[string]string{
			{
				"role":    "system",
				"content": moderationSystemPrompt,
			},
			{
				"role":    "user",
				"content": renderReviewUserPrompt(input),
			},
		},
		"response_format": map[string]string{
			"type": "json_object",
		},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return Decision{}, fmt.Errorf("构建审核请求失败: %w", err)
	}

	req, err := http.NewRequestWithContext(reviewCtx, http.MethodPost, r.endpoint, bytes.NewReader(body))
	if err != nil {
		return Decision{}, fmt.Errorf("创建审核请求失败: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+r.apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "ELS-Feedback-Proxy-Moderation")

	resp, err := r.httpClient.Do(req)
	if err != nil {
		return Decision{}, fmt.Errorf("调用审核模型失败: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Decision{}, fmt.Errorf("审核模型返回异常: HTTP %d body=%s", resp.StatusCode, string(respBody))
	}

	content, err := extractMessageContent(respBody)
	if err != nil {
		return Decision{}, err
	}

	decision, err := parseDecision(content)
	if err != nil {
		return Decision{}, err
	}
	return decision, nil
}

type chatCompletionsResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

func extractMessageContent(raw []byte) (string, error) {
	var response chatCompletionsResponse
	if err := json.Unmarshal(raw, &response); err != nil {
		return "", fmt.Errorf("解析审核模型响应失败: %w", err)
	}
	if len(response.Choices) == 0 {
		return "", fmt.Errorf("审核模型未返回候选结果")
	}
	content := strings.TrimSpace(response.Choices[0].Message.Content)
	if content == "" {
		return "", fmt.Errorf("审核模型内容为空")
	}
	return content, nil
}

type decisionEnvelope struct {
	Allow      *bool    `json:"allow"`
	Reasons    []string `json:"reasons"`
	Categories []string `json:"categories"`
	Confidence *float64 `json:"confidence"`
}

func parseDecision(content string) (Decision, error) {
	jsonPayload := extractJSONPayload(content)
	var envelope decisionEnvelope
	if err := json.Unmarshal([]byte(jsonPayload), &envelope); err != nil {
		return Decision{}, fmt.Errorf("解析审核结论失败: %w", err)
	}
	if envelope.Allow == nil {
		return Decision{}, fmt.Errorf("审核结论缺少 allow 字段")
	}
	reasons := normalizeNonEmpty(envelope.Reasons)
	categories := normalizeNonEmpty(envelope.Categories)
	confidence := 0.5
	if envelope.Confidence != nil {
		confidence = *envelope.Confidence
	}
	return Decision{
		Allow:      *envelope.Allow,
		Reasons:    reasons,
		Categories: categories,
		Confidence: clampConfidence(confidence),
	}, nil
}

func extractJSONPayload(content string) string {
	trimmed := strings.TrimSpace(content)
	trimmed = strings.TrimPrefix(trimmed, "```json")
	trimmed = strings.TrimPrefix(trimmed, "```JSON")
	trimmed = strings.TrimPrefix(trimmed, "```")
	trimmed = strings.TrimSuffix(trimmed, "```")
	trimmed = strings.TrimSpace(trimmed)

	start := strings.Index(trimmed, "{")
	if start < 0 {
		return trimmed
	}
	level := 0
	for idx := start; idx < len(trimmed); idx++ {
		switch trimmed[idx] {
		case '{':
			level++
		case '}':
			level--
			if level == 0 {
				return trimmed[start : idx+1]
			}
		}
	}
	return trimmed[start:]
}

func normalizeNonEmpty(raw []string) []string {
	result := make([]string, 0, len(raw))
	for _, item := range raw {
		value := strings.TrimSpace(item)
		if value != "" {
			result = append(result, value)
		}
	}
	return result
}

func clampConfidence(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 1 {
		return 1
	}
	return value
}

func renderReviewUserPrompt(input ReviewInput) string {
	return fmt.Sprintf(
		"请审核下面反馈是否适合公开进入开发工单。请只输出 JSON，并按以下顺序组织字段：reasons、categories、confidence、allow。\n"+
			"要求先给出简短推理风格理由（写入 reasons），最后再输出 allow 布尔值。\n\n"+
			"【反馈类型】\n%s\n\n"+
			"【标题】\n%s\n\n"+
			"【详细描述】\n%s\n\n"+
			"【可复现步骤】\n%s\n\n"+
			"【预期行为】\n%s\n\n"+
			"【实际行为】\n%s\n\n"+
			"【补充信息】\n%s\n",
		input.Type,
		input.Title,
		input.Detail,
		input.ReproductionSteps,
		input.ExpectedBehavior,
		input.ActualBehavior,
		input.ExtraContext,
	)
}

const moderationSystemPrompt = `你是反馈内容审核器。任务是判断内容是否可以公开展示在 GitHub Issue 中。
判定原则：
1) 明显色情、违法犯罪教唆、极端暴力威胁、骚扰辱骂、明显精神失控刷屏内容，应判定不通过。
2) 普通 bug 报告、功能建议、表达不满但可理解的内容，可通过。
3) 若不构成明显违规，优先通过，不要过度拦截。
输出要求：
- 必须输出 JSON 对象，不要输出额外文本。
- 字段顺序固定为：
  reasons: string[]（2~5条，简洁，先输出）
  categories: string[]（例如 "色情", "违法", "骚扰辱骂", "精神失控", "正常反馈"）
  confidence: number（0到1）
  allow: boolean（最后输出）
`
