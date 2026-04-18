package api

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"els-feedback-proxy/internal/config"
)

type githubWebhookTestUpdater struct {
	startTag    string
	startForce  bool
	startCalls  int
	startResult selfUpdateDispatchResult
	startErr    error
	status      map[string]any
}

func (u *githubWebhookTestUpdater) isAuthorized(r *http.Request) bool {
	return true
}

func (u *githubWebhookTestUpdater) statusSnapshot() map[string]any {
	if u.status == nil {
		return map[string]any{"enabled": true}
	}
	return u.status
}

func (u *githubWebhookTestUpdater) startRelease(rawTag string, force bool) (selfUpdateDispatchResult, error) {
	u.startCalls++
	u.startTag = rawTag
	u.startForce = force
	return u.startResult, u.startErr
}

func TestHandleGitHubWebhookPingAccepted(t *testing.T) {
	server := newGitHubWebhookTestServer()
	server.selfUpdater = &githubWebhookTestUpdater{
		status: map[string]any{"enabled": true},
	}

	body := []byte(`{"zen":"Keep it logically awesome.","hook_id":1,"repository":{"full_name":"Eric-Terminal/els-feedback-proxy"}}`)
	request := httptest.NewRequest(http.MethodPost, "/v1/github/webhooks", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-GitHub-Event", "ping")
	request.Header.Set("X-GitHub-Delivery", "delivery-ping-1")
	request.Header.Set("X-Hub-Signature-256", signGitHubWebhookBody("webhook-secret", body))

	response := httptest.NewRecorder()
	server.engine.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("期望 ping 返回 200，实际=%d body=%s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"delivery-ping-1"`) {
		t.Fatalf("响应应包含 delivery id，body=%s", response.Body.String())
	}
}

func TestHandleGitHubWebhookRejectsInvalidSignature(t *testing.T) {
	server := newGitHubWebhookTestServer()
	server.selfUpdater = &githubWebhookTestUpdater{}

	body := []byte(`{"repository":{"full_name":"Eric-Terminal/els-feedback-proxy"}}`)
	request := httptest.NewRequest(http.MethodPost, "/v1/github/webhooks", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-GitHub-Event", "ping")
	request.Header.Set("X-Hub-Signature-256", "sha256=deadbeef")

	response := httptest.NewRecorder()
	server.engine.ServeHTTP(response, request)

	if response.Code != http.StatusForbidden {
		t.Fatalf("期望签名错误返回 403，实际=%d body=%s", response.Code, response.Body.String())
	}
}

func TestHandleGitHubWebhookReleaseStartsSelfUpdate(t *testing.T) {
	server := newGitHubWebhookTestServer()
	updater := &githubWebhookTestUpdater{
		startResult: selfUpdateDispatchResult{
			Tag:   "v0.1.7",
			Force: false,
		},
		status: map[string]any{"enabled": true, "current_tag": "v0.1.7"},
	}
	server.selfUpdater = updater

	body := []byte(`{"action":"published","repository":{"full_name":"Eric-Terminal/els-feedback-proxy"},"release":{"tag_name":"v0.1.7","draft":false,"prerelease":false,"html_url":"https://github.com/Eric-Terminal/els-feedback-proxy/releases/tag/v0.1.7"}}`)
	request := httptest.NewRequest(http.MethodPost, "/v1/github/webhooks", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-GitHub-Event", "release")
	request.Header.Set("X-GitHub-Delivery", "delivery-release-1")
	request.Header.Set("X-Hub-Signature-256", signGitHubWebhookBody("webhook-secret", body))

	response := httptest.NewRecorder()
	server.engine.ServeHTTP(response, request)

	if response.Code != http.StatusAccepted {
		t.Fatalf("期望 release 返回 202，实际=%d body=%s", response.Code, response.Body.String())
	}
	if updater.startCalls != 1 {
		t.Fatalf("期望触发一次自动更新，实际=%d", updater.startCalls)
	}
	if updater.startTag != "v0.1.7" {
		t.Fatalf("期望更新 tag=v0.1.7，实际=%s", updater.startTag)
	}
	if updater.startForce {
		t.Fatalf("GitHub release 触发的更新不应强制覆盖")
	}
}

func TestHandleGitHubWebhookReleaseIgnoresUnsupportedAction(t *testing.T) {
	server := newGitHubWebhookTestServer()
	updater := &githubWebhookTestUpdater{}
	server.selfUpdater = updater

	body := []byte(`{"action":"edited","repository":{"full_name":"Eric-Terminal/els-feedback-proxy"},"release":{"tag_name":"v0.1.7","draft":false,"prerelease":false}}`)
	request := httptest.NewRequest(http.MethodPost, "/v1/github/webhooks", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-GitHub-Event", "release")
	request.Header.Set("X-Hub-Signature-256", signGitHubWebhookBody("webhook-secret", body))

	response := httptest.NewRecorder()
	server.engine.ServeHTTP(response, request)

	if response.Code != http.StatusAccepted {
		t.Fatalf("期望忽略动作时返回 202，实际=%d body=%s", response.Code, response.Body.String())
	}
	if updater.startCalls != 0 {
		t.Fatalf("不应触发自动更新，实际=%d", updater.startCalls)
	}
}

func TestIsValidGitHubWebhookSignature(t *testing.T) {
	body := []byte(`{"hello":"world"}`)
	signature := signGitHubWebhookBody("webhook-secret", body)
	if !isValidGitHubWebhookSignature("webhook-secret", body, signature) {
		t.Fatalf("期望签名校验通过")
	}
	if isValidGitHubWebhookSignature("wrong-secret", body, signature) {
		t.Fatalf("错误密钥不应通过签名校验")
	}
}

func newGitHubWebhookTestServer() *Server {
	return NewServer(
		config.Config{
			GitHubWebhookSecret: "webhook-secret",
			SelfUpdateSecret:    "admin-secret",
			SelfUpdateRepoOwner: "Eric-Terminal",
			SelfUpdateRepoName:  "els-feedback-proxy",
		},
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
	)
}

func signGitHubWebhookBody(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return githubWebhookSignaturePrefix + hex.EncodeToString(mac.Sum(nil))
}
