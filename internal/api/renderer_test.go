package api

import (
	"strings"
	"testing"
	"time"

	"els-feedback-proxy/internal/moderation"
)

func TestRenderIssueBodyContainsAutoUpdateMarker(t *testing.T) {
	req := SubmitIssueRequest{
		Type:   "bug",
		Title:  "测试标题",
		Detail: "测试详情",
		Environment: EnvironmentSnapshot{
			Platform:           "ios",
			AppVersion:         "1.0.0",
			AppBuild:           "100",
			OSVersion:          "iOS 18",
			DeviceModel:        "iPhone",
			LocaleIdentifier:   "zh_Hans",
			TimezoneIdentifier: "Asia/Shanghai",
		},
	}

	body := renderIssueBody(req, "ip-hash")
	if !strings.Contains(body, "由用户提出自动更新的") {
		t.Fatalf("Issue Markdown 缺少自动更新标记，body=%s", body)
	}
}

func TestRenderBlockedIssueBodyNoRawContent(t *testing.T) {
	body := renderBlockedIssueBody("archive-123", "archive-archive-123.md", "AI 判定不适合公开")
	if !strings.Contains(body, "archive-123") {
		t.Fatalf("隐藏工单缺少 archive_id")
	}
	if strings.Contains(body, "详细描述") {
		t.Fatalf("隐藏工单不应包含原始反馈正文")
	}
}

func TestRenderBlockedArchiveMarkdownContainsOriginalText(t *testing.T) {
	req := SubmitIssueRequest{
		Type:   "bug",
		Title:  "标题A",
		Detail: "详细描述B",
	}
	md := renderBlockedArchiveMarkdown("archive-xyz", "ip-hash", req, moderation.Decision{
		Allow:      false,
		Reasons:    []string{"违规内容"},
		Categories: []string{"违法"},
		Confidence: 0.87,
	}, nil, testNow())
	if !strings.Contains(md, "标题A") || !strings.Contains(md, "详细描述B") {
		t.Fatalf("留档应包含原始反馈内容")
	}
}

func TestRenderBlockedCommentBodyContainsArchiveInfo(t *testing.T) {
	body := renderBlockedCommentBody("archive-comment-1", "archive-archive-comment-1.md", "审核拦截")
	if !strings.Contains(body, "archive-comment-1") {
		t.Fatalf("隐藏评论模板应包含 archive_id")
	}
	if !strings.Contains(body, "review-blocked") {
		t.Fatalf("隐藏评论模板应提示服务器文件路径")
	}
}

func testNow() (now time.Time) {
	return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
}
