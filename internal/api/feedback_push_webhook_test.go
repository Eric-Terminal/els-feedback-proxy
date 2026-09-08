package api

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"els-feedback-proxy/internal/github"
)

func TestFeedbackPushInvalidatesStatusCacheWithoutSelfUpdater(t *testing.T) {
	for _, test := range []struct {
		name       string
		body       string
		validSign  bool
		status     int
		invalidate bool
	}{
		{"客户端推送", `{"repository":{"full_name":"Eric-Terminal/ETOS-LLM-Studio"}}`, true, http.StatusAccepted, true},
		{"仓库名大小写", `{"repository":{"full_name":"eric-terminal/etos-llm-studio"}}`, true, http.StatusAccepted, true},
		{"服务端仓库推送", `{"repository":{"full_name":"Eric-Terminal/els-feedback-proxy"}}`, true, http.StatusAccepted, false},
		{"签名不正确", `{"repository":{"full_name":"Eric-Terminal/ETOS-LLM-Studio"}}`, false, http.StatusForbidden, false},
		{"无效负载", `{`, true, http.StatusBadRequest, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := newGitHubWebhookTestServer()
			server.selfUpdater = nil
			server.cfg.GitHubOwner = "Eric-Terminal"
			server.cfg.GitHubRepo = "ETOS-LLM-Studio"
			gh := &statusQueryTestGitHub{issue: github.IssueStatus{
				TimelineEvents: []github.IssueTimelineEvent{{ID: 1, Type: "referenced_commit"}},
			}}
			server.gh = gh
			server.statusCache.Set(github.IssueStatus{Number: 133})
			server.statusCache.Set(github.IssueStatus{Number: 42})
			body := []byte(test.body)
			request := httptest.NewRequest(http.MethodPost, "/v1/github/webhooks", bytes.NewReader(body))
			request.Header.Set("X-GitHub-Event", "push")
			signature := signGitHubWebhookBody("webhook-secret", body)
			if !test.validSign {
				signature = "sha256=invalid"
			}
			request.Header.Set("X-Hub-Signature-256", signature)
			response := httptest.NewRecorder()
			server.engine.ServeHTTP(response, request)
			if response.Code != test.status {
				t.Fatalf("期望状态码 %d，实际=%d", test.status, response.Code)
			}
			if _, cached := server.statusCache.Get(42); cached == test.invalidate {
				t.Fatal("推送缓存失效范围不正确")
			}
			issue, err := server.loadIssueStatus(context.Background(), 133)
			if err != nil {
				t.Fatal(err)
			}
			if test.invalidate && (gh.getIssueCallCount != 1 || len(issue.TimelineEvents) != 1) {
				t.Fatal("推送后下次读取必须从 GitHub 获取关联提交")
			}
			if !test.invalidate && gh.getIssueCallCount != 0 {
				t.Fatal("无效或其他仓库的推送不能清空反馈缓存")
			}
		})
	}
}
