package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"els-feedback-proxy/internal/config"
	"els-feedback-proxy/internal/github"
	"els-feedback-proxy/internal/store"
)

type statusQueryTestLimiter struct {
	callCount int
}

func (l *statusQueryTestLimiter) Allow(key string, limit int, window time.Duration) bool {
	l.callCount++
	return false
}

type statusQueryTestDedupe struct{}

func (d *statusQueryTestDedupe) SeenRecently(key string, window time.Duration) bool {
	return false
}

type statusQueryTestGitHub struct {
	issue             github.IssueStatus
	getIssueCallCount int
}

func (g *statusQueryTestGitHub) CreateIssue(ctx context.Context, input github.CreateIssueInput) (github.CreateIssueResult, error) {
	return github.CreateIssueResult{}, nil
}

func (g *statusQueryTestGitHub) CreateIssueComment(ctx context.Context, issueNumber int, body string) (github.CreateCommentResult, error) {
	return github.CreateCommentResult{}, nil
}

func (g *statusQueryTestGitHub) GetIssueStatus(ctx context.Context, issueNumber int) (github.IssueStatus, error) {
	g.getIssueCallCount++
	issue := g.issue
	issue.Number = issueNumber
	return issue, nil
}

func TestHandleGetIssueStatusDoesNotUseRateLimitAndCachesOneHour(t *testing.T) {
	ticketStore, err := store.NewTicketStore(t.TempDir())
	if err != nil {
		t.Fatalf("初始化 ticket store 失败: %v", err)
	}
	if err := ticketStore.Set(42, "token-42"); err != nil {
		t.Fatalf("写入 ticket token 失败: %v", err)
	}

	limiter := &statusQueryTestLimiter{}
	gh := &statusQueryTestGitHub{
		issue: github.IssueStatus{
			Title:     "状态缓存测试",
			State:     "open",
			Labels:    []string{"status/in-progress", "source/app-feedback"},
			UpdatedAt: time.Date(2026, 4, 19, 12, 0, 0, 0, time.UTC),
			URL:       "https://github.com/example/repo/issues/42",
		},
	}

	server := NewServer(
		config.Config{
			RequiredUAKeyword: "ETOS LLM Studio",
			IssuesPath:        "/v1/feedback/issues",
			RateWindow:        15 * time.Minute,
		},
		gh,
		limiter,
		&statusQueryTestDedupe{},
		nil,
		ticketStore,
		nil,
		nil,
	)

	requestOne := httptest.NewRequest(http.MethodGet, "/v1/feedback/issues/42?ticket_token=token-42", nil)
	requestOne.Header.Set("User-Agent", "ETOS LLM Studio/1.0")
	responseOne := httptest.NewRecorder()
	server.engine.ServeHTTP(responseOne, requestOne)

	if responseOne.Code != http.StatusOK {
		t.Fatalf("第一次查询期望 200，实际 %d，body=%s", responseOne.Code, responseOne.Body.String())
	}
	if limiter.callCount != 0 {
		t.Fatalf("状态查询不应触发限流，实际调用次数=%d", limiter.callCount)
	}
	if gh.getIssueCallCount != 1 {
		t.Fatalf("第一次查询应调用一次 GitHub，实际=%d", gh.getIssueCallCount)
	}

	requestTwo := httptest.NewRequest(http.MethodGet, "/v1/feedback/issues/42?ticket_token=token-42", nil)
	requestTwo.Header.Set("User-Agent", "ETOS LLM Studio/1.0")
	responseTwo := httptest.NewRecorder()
	server.engine.ServeHTTP(responseTwo, requestTwo)

	if responseTwo.Code != http.StatusOK {
		t.Fatalf("第二次查询期望 200，实际 %d，body=%s", responseTwo.Code, responseTwo.Body.String())
	}
	if gh.getIssueCallCount != 1 {
		t.Fatalf("命中缓存后不应重复调用 GitHub，实际=%d", gh.getIssueCallCount)
	}
}
