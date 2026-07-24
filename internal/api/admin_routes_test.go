package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"els-feedback-proxy/internal/config"
)

func TestSelfUpdateRoutesOnlyRegisterOnAdminListener(t *testing.T) {
	const updateSecret = "test-self-update-secret"
	server := NewServer(
		config.Config{
			AdminListenAddr:       "127.0.0.1:8081",
			SelfUpdateSecret:      updateSecret,
			SelfUpdateRepoOwner:   "Eric-Terminal",
			SelfUpdateRepoName:    "els-feedback-proxy",
			SelfUpdateServiceName: "els-feedback-proxy",
		},
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
	)

	publicResponse := httptest.NewRecorder()
	publicRequest := httptest.NewRequest(http.MethodGet, "/v1/admin/self-update/status", nil)
	publicRequest.Header.Set("X-ELS-Update-Secret", updateSecret)
	server.engine.ServeHTTP(publicResponse, publicRequest)
	if publicResponse.Code != http.StatusNotFound {
		t.Fatalf("公网监听器不应注册自更新状态接口，实际返回 %d", publicResponse.Code)
	}

	adminResponse := httptest.NewRecorder()
	adminRequest := httptest.NewRequest(http.MethodGet, "/v1/admin/self-update/status", nil)
	adminRequest.Header.Set("X-ELS-Update-Secret", updateSecret)
	server.adminEngine.ServeHTTP(adminResponse, adminRequest)
	if adminResponse.Code != http.StatusOK {
		t.Fatalf("内网管理监听器应提供自更新状态接口，实际返回 %d", adminResponse.Code)
	}
}
