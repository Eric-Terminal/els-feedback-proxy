package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"els-feedback-proxy/internal/github"
	"els-feedback-proxy/internal/guide"
)

type guideSourceTreeGateway struct {
	calls int
}

func (g *guideSourceTreeGateway) GetSourceTree(context.Context, string) (github.SourceTree, error) {
	g.calls++
	size := 64
	return github.SourceTree{Entries: []github.SourceTreeEntry{
		{Path: "ETOSCore/Guide.swift", Type: "blob", Size: &size},
	}}, nil
}

func TestGuideTokenIsNoStoreAndChatRequiresIt(t *testing.T) {
	proxy, err := guide.NewProxy(guide.ProxyConfig{
		UpstreamBaseURL: "https://api.example.com/v1",
		UpstreamAPIKey:  "upstream-key",
		TokenSecret:     "0123456789abcdef0123456789abcdef",
		IPConcurrency:   1,
		RequestTimeout:  time.Minute,
	})
	if err != nil {
		t.Fatalf("初始化测试向导代理失败: %v", err)
	}
	gin.SetMode(gin.TestMode)
	server := &Server{engine: gin.New(), guideProxy: proxy}
	server.registerGuideRoutes()

	tokenRequest := httptest.NewRequest(http.MethodPost, "/v1/guide/token", nil)
	tokenRequest.RemoteAddr = "203.0.113.10:1234"
	tokenResponse := httptest.NewRecorder()
	server.engine.ServeHTTP(tokenResponse, tokenRequest)
	if tokenResponse.Code != http.StatusOK || tokenResponse.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("临时令牌响应错误: code=%d headers=%v", tokenResponse.Code, tokenResponse.Header())
	}
	var bundle guide.TokenBundle
	if err := json.Unmarshal(tokenResponse.Body.Bytes(), &bundle); err != nil || bundle.Token == "" {
		t.Fatalf("临时令牌响应无效: %v body=%s", err, tokenResponse.Body.String())
	}

	chatRequest := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"messages":[{"role":"user","content":"帮助"}]}`))
	chatRequest.Header.Set("X-Guide-Session-ID", "session-one")
	chatResponse := httptest.NewRecorder()
	server.engine.ServeHTTP(chatResponse, chatRequest)
	if chatResponse.Code != http.StatusUnauthorized || !strings.Contains(chatResponse.Body.String(), "invalid_token") {
		t.Fatalf("无令牌请求应被拒绝: code=%d body=%s", chatResponse.Code, chatResponse.Body.String())
	}
}

func TestGuideSourceTreeUsesImmutableCache(t *testing.T) {
	gateway := &guideSourceTreeGateway{}
	service := guide.NewSourceTreeService(gateway, "Eric-Terminal", "ETOS-LLM-Studio", t.TempDir())
	gin.SetMode(gin.TestMode)
	server := &Server{engine: gin.New(), guideSourceTrees: service}
	server.registerGuideRoutes()
	sha := "0123456789abcdef0123456789abcdef01234567"

	first := httptest.NewRecorder()
	server.engine.ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/v1/guide/source-trees/"+sha, nil))
	if first.Code != http.StatusOK || !strings.Contains(first.Header().Get("Cache-Control"), "immutable") {
		t.Fatalf("源码树响应错误: code=%d headers=%v body=%s", first.Code, first.Header(), first.Body.String())
	}
	etag := first.Header().Get("ETag")
	secondRequest := httptest.NewRequest(http.MethodGet, "/v1/guide/source-trees/"+sha, nil)
	secondRequest.Header.Set("If-None-Match", etag)
	second := httptest.NewRecorder()
	server.engine.ServeHTTP(second, secondRequest)
	if second.Code != http.StatusNotModified || gateway.calls != 1 {
		t.Fatalf("不可变缓存未命中: code=%d calls=%d", second.Code, gateway.calls)
	}
}
