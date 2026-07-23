package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"els-feedback-proxy/internal/config"
	"els-feedback-proxy/internal/store"
)

type announcementTestLimiter struct{}

func (l *announcementTestLimiter) Allow(key string, limit int, window time.Duration) bool {
	return true
}

func TestPublicAnnouncementsReturnsEmptyArrayAndSupportsETag(t *testing.T) {
	server := newAnnouncementTestServer(t, "")

	request := httptest.NewRequest(http.MethodGet, "/v1/announcements", nil)
	response := httptest.NewRecorder()
	server.engine.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("期望返回 200，实际 %d body=%s", response.Code, response.Body.String())
	}
	if response.Body.String() != "[]" {
		t.Fatalf("空公告应返回 JSON 数组，实际 %s", response.Body.String())
	}
	if response.Header().Get("Cloudflare-CDN-Cache-Control") == "" {
		t.Fatalf("公告接口应设置 Cloudflare CDN 缓存策略")
	}

	etag := response.Header().Get("ETag")
	if etag == "" {
		t.Fatalf("公告接口应返回 ETag")
	}
	conditionalRequest := httptest.NewRequest(http.MethodGet, "/v1/announcements", nil)
	conditionalRequest.Header.Set("If-None-Match", etag)
	conditionalResponse := httptest.NewRecorder()
	server.engine.ServeHTTP(conditionalResponse, conditionalRequest)
	if conditionalResponse.Code != http.StatusNotModified {
		t.Fatalf("ETag 命中时应返回 304，实际 %d", conditionalResponse.Code)
	}
}

func TestAnnouncementAdminCRUDPublishesOnlyEnabledRecords(t *testing.T) {
	const adminToken = "test-admin-token"
	server := newAnnouncementTestServer(t, adminToken)

	draft := `{
		"id": 2026072301,
		"type": "info",
		"language": "zh-Hans",
		"title": "草稿公告",
		"body": "这条公告暂时不发布。",
		"enabled": false
	}`
	createResponse := performAdminRequest(
		server,
		http.MethodPost,
		"/v1/admin/announcements",
		draft,
		adminToken,
	)
	if createResponse.Code != http.StatusCreated {
		t.Fatalf("创建草稿期望 201，实际 %d body=%s", createResponse.Code, createResponse.Body.String())
	}

	var createdPayload struct {
		Record store.AnnouncementRecord `json:"record"`
	}
	if err := json.Unmarshal(createResponse.Body.Bytes(), &createdPayload); err != nil {
		t.Fatalf("解析创建响应失败: %v", err)
	}
	if createdPayload.Record.Key == "" {
		t.Fatalf("创建响应缺少公告 key")
	}

	publicDraftResponse := httptest.NewRecorder()
	server.engine.ServeHTTP(
		publicDraftResponse,
		httptest.NewRequest(http.MethodGet, "/v1/announcements", nil),
	)
	if publicDraftResponse.Body.String() != "[]" {
		t.Fatalf("草稿不应出现在公开接口，实际 %s", publicDraftResponse.Body.String())
	}

	published := `{
		"id": 2026072301,
		"type": "warning",
		"language": "zh-Hans",
		"platform": "iOS",
		"title": "已发布公告",
		"body": "这条公告应当出现在客户端接口。",
		"enabled": true
	}`
	updateResponse := performAdminRequest(
		server,
		http.MethodPut,
		"/v1/admin/announcements/"+createdPayload.Record.Key,
		published,
		adminToken,
	)
	if updateResponse.Code != http.StatusOK {
		t.Fatalf("发布公告期望 200，实际 %d body=%s", updateResponse.Code, updateResponse.Body.String())
	}

	publicResponse := httptest.NewRecorder()
	server.engine.ServeHTTP(
		publicResponse,
		httptest.NewRequest(http.MethodGet, "/v1/announcements", nil),
	)
	if !strings.Contains(publicResponse.Body.String(), `"title":"已发布公告"`) ||
		strings.Contains(publicResponse.Body.String(), `"key"`) ||
		strings.Contains(publicResponse.Body.String(), `"enabled"`) {
		t.Fatalf("公开公告字段不正确: %s", publicResponse.Body.String())
	}

	deleteResponse := performAdminRequest(
		server,
		http.MethodDelete,
		"/v1/admin/announcements/"+createdPayload.Record.Key,
		"",
		adminToken,
	)
	if deleteResponse.Code != http.StatusNoContent {
		t.Fatalf("删除公告期望 204，实际 %d body=%s", deleteResponse.Code, deleteResponse.Body.String())
	}
}

func TestAnnouncementAdminLoginCreatesProtectedSession(t *testing.T) {
	const adminToken = "browser-admin-token"
	server := newAnnouncementTestServer(t, adminToken)

	form := url.Values{"password": {adminToken}}
	loginRequest := httptest.NewRequest(
		http.MethodPost,
		"/admin/login",
		strings.NewReader(form.Encode()),
	)
	loginRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	loginResponse := httptest.NewRecorder()
	server.adminEngine.ServeHTTP(loginResponse, loginRequest)

	if loginResponse.Code != http.StatusSeeOther {
		t.Fatalf("登录成功应返回 303，实际 %d", loginResponse.Code)
	}
	cookies := loginResponse.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != announcementAdminCookieName ||
		!cookies[0].HttpOnly || cookies[0].Secure {
		t.Fatalf("管理会话 Cookie 安全属性不正确: %+v", cookies)
	}

	pageRequest := httptest.NewRequest(http.MethodGet, "/admin/announcements", nil)
	pageRequest.AddCookie(cookies[0])
	pageResponse := httptest.NewRecorder()
	server.adminEngine.ServeHTTP(pageResponse, pageRequest)
	if pageResponse.Code != http.StatusOK ||
		!strings.Contains(pageResponse.Body.String(), "内容与语言版本") {
		t.Fatalf("登录后未返回管理页面: code=%d body=%s", pageResponse.Code, pageResponse.Body.String())
	}

	crossOriginRequest := httptest.NewRequest(
		http.MethodPost,
		"/v1/admin/announcements",
		strings.NewReader(`{"id":1,"type":"info","title":"标题","body":"正文","enabled":true}`),
	)
	crossOriginRequest.Host = "feedback.els.ericterminal.com"
	crossOriginRequest.Header.Set("Origin", "https://attacker.example")
	crossOriginRequest.Header.Set("Content-Type", "application/json")
	crossOriginRequest.AddCookie(cookies[0])
	crossOriginResponse := httptest.NewRecorder()
	server.adminEngine.ServeHTTP(crossOriginResponse, crossOriginRequest)
	if crossOriginResponse.Code != http.StatusForbidden {
		t.Fatalf("跨站管理请求应被拒绝，实际 %d", crossOriginResponse.Code)
	}
}

func TestPublicListenerDoesNotRegisterAdminRoutes(t *testing.T) {
	server := newAnnouncementTestServer(t, "browser-admin-token")

	for _, path := range []string{"/admin/announcements", "/v1/admin/announcements"} {
		response := httptest.NewRecorder()
		server.engine.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusNotFound {
			t.Fatalf("公网监听器不应注册 %s，实际返回 %d", path, response.Code)
		}
	}

	adminResponse := httptest.NewRecorder()
	server.adminEngine.ServeHTTP(
		adminResponse,
		httptest.NewRequest(http.MethodGet, "/admin/announcements", nil),
	)
	if adminResponse.Code != http.StatusOK {
		t.Fatalf("内网管理监听器应提供登录页，实际返回 %d", adminResponse.Code)
	}
}

func newAnnouncementTestServer(t *testing.T, adminToken string) *Server {
	t.Helper()
	announcementStore, err := store.NewAnnouncementStore(t.TempDir())
	if err != nil {
		t.Fatalf("初始化公告存储失败: %v", err)
	}
	return NewServer(
		config.Config{
			AdminListenAddr:          "127.0.0.1:8081",
			AnnouncementAdminToken:   adminToken,
			AnnouncementCacheMaxAge:  300,
			AdminLoginLimitPerWindow: 10,
			RateWindow:               15 * time.Minute,
		},
		nil,
		&announcementTestLimiter{},
		nil,
		nil,
		nil,
		nil,
		nil,
		announcementStore,
	)
}

func performAdminRequest(
	server *Server,
	method string,
	path string,
	body string,
	adminToken string,
) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+adminToken)
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	response := httptest.NewRecorder()
	server.adminEngine.ServeHTTP(response, request)
	return response
}
