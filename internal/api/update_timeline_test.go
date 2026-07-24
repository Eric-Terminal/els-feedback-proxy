package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"els-feedback-proxy/internal/config"
	"els-feedback-proxy/internal/github"
)

type updateTimelineTestGitHub struct {
	mu        sync.Mutex
	callCount int
	err       error
}

func (g *updateTimelineTestGitHub) CreateIssue(
	context.Context,
	github.CreateIssueInput,
) (github.CreateIssueResult, error) {
	return github.CreateIssueResult{}, nil
}

func (g *updateTimelineTestGitHub) CreateIssueComment(
	context.Context,
	int,
	string,
) (github.CreateCommentResult, error) {
	return github.CreateCommentResult{}, nil
}

func (g *updateTimelineTestGitHub) GetIssueStatus(
	context.Context,
	int,
) (github.IssueStatus, error) {
	return github.IssueStatus{}, nil
}

func (g *updateTimelineTestGitHub) GetUpdateTimeline(
	_ context.Context,
	branch string,
	maxCount int,
) (github.UpdateTimeline, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.callCount++
	if g.err != nil {
		return github.UpdateTimeline{}, g.err
	}
	return github.UpdateTimeline{
		Version: 1,
		Branch:  branch,
		Commits: []github.UpdateTimelineCommit{
			{
				OID:             "abcdef1234567890",
				MessageHeadline: "feat: 测试",
				Message:         "feat: 测试",
				CommittedAt:     time.Date(2026, 7, 24, 7, 0, 0, 0, time.UTC),
				URL:             "https://github.com/example/repo/commit/abcdef1234567890",
				CIContexts:      []string{"Xcode Cloud / 构建"},
			},
		},
	}, nil
}

func (g *updateTimelineTestGitHub) calls() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.callCount
}

func TestUpdateTimelineUsesServerCacheAndETag(t *testing.T) {
	gateway := &updateTimelineTestGitHub{}
	server := newUpdateTimelineTestServer(gateway)

	firstResponse := httptest.NewRecorder()
	server.engine.ServeHTTP(
		firstResponse,
		httptest.NewRequest(http.MethodGet, "/v1/updates/timeline", nil),
	)
	if firstResponse.Code != http.StatusOK {
		t.Fatalf("首次请求应返回 200，实际 %d body=%s", firstResponse.Code, firstResponse.Body.String())
	}
	if gateway.calls() != 1 {
		t.Fatalf("首次请求应调用一次 GitHub，实际 %d", gateway.calls())
	}
	if firstResponse.Header().Get("Cloudflare-CDN-Cache-Control") == "" {
		t.Fatal("检查更新接口缺少 Cloudflare CDN 缓存头")
	}
	etag := firstResponse.Header().Get("ETag")
	if etag == "" {
		t.Fatal("检查更新接口缺少 ETag")
	}

	conditionalRequest := httptest.NewRequest(http.MethodGet, "/v1/updates/timeline", nil)
	conditionalRequest.Header.Set("If-None-Match", etag)
	conditionalResponse := httptest.NewRecorder()
	server.engine.ServeHTTP(conditionalResponse, conditionalRequest)
	if conditionalResponse.Code != http.StatusNotModified {
		t.Fatalf("ETag 命中应返回 304，实际 %d", conditionalResponse.Code)
	}
	if gateway.calls() != 1 {
		t.Fatalf("服务端缓存命中不应重复请求 GitHub，实际 %d", gateway.calls())
	}
}

func TestUpdateTimelineFallsBackToStaleCache(t *testing.T) {
	gateway := &updateTimelineTestGitHub{}
	server := newUpdateTimelineTestServer(gateway)

	firstResponse := httptest.NewRecorder()
	server.engine.ServeHTTP(
		firstResponse,
		httptest.NewRequest(http.MethodGet, "/v1/updates/timeline", nil),
	)
	if firstResponse.Code != http.StatusOK {
		t.Fatalf("首次请求应返回 200，实际 %d", firstResponse.Code)
	}

	server.updateTimelineCache.mu.Lock()
	server.updateTimelineCache.expiresAt = time.Now().Add(-time.Minute)
	server.updateTimelineCache.mu.Unlock()
	gateway.err = errors.New("GitHub 暂时不可用")

	staleResponse := httptest.NewRecorder()
	server.engine.ServeHTTP(
		staleResponse,
		httptest.NewRequest(http.MethodGet, "/v1/updates/timeline", nil),
	)
	if staleResponse.Code != http.StatusOK {
		t.Fatalf("GitHub 失败时应返回旧缓存，实际 %d body=%s", staleResponse.Code, staleResponse.Body.String())
	}
	if staleResponse.Body.String() != firstResponse.Body.String() {
		t.Fatal("GitHub 失败时返回的内容应与旧缓存一致")
	}
	if gateway.calls() != 2 {
		t.Fatalf("缓存过期后应尝试刷新一次，实际 %d", gateway.calls())
	}
}

func newUpdateTimelineTestServer(gateway *updateTimelineTestGitHub) *Server {
	return NewServer(
		config.Config{
			AnnouncementCacheMaxAge: 300,
		},
		gateway,
		&announcementTestLimiter{},
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
	)
}
