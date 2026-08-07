package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"els-feedback-proxy/internal/store"
)

func TestDistributionActionAdminPublishesPreviewableManifest(t *testing.T) {
	const adminToken = "distribution-action-admin-token"
	server := newDistributionTestServer(t, adminToken)
	payload := testAPIOfficialProviderActionPayload()

	createResponse := performDistributionRequest(
		t,
		server,
		http.MethodPost,
		"/v1/admin/distribution/actions",
		adminToken,
		map[string]string{"enabled": "true"},
		"provider-action.json",
		payload,
	)
	if createResponse.Code != http.StatusCreated {
		t.Fatalf("创建官方数据库操作期望 201，实际 %d body=%s", createResponse.Code, createResponse.Body.String())
	}
	var created struct {
		Action store.OfficialDataActionRecord `json:"action"`
	}
	if err := json.Unmarshal(createResponse.Body.Bytes(), &created); err != nil {
		t.Fatalf("解析操作创建响应失败: %v", err)
	}

	manifestResponse := httptest.NewRecorder()
	server.engine.ServeHTTP(
		manifestResponse,
		httptest.NewRequest(http.MethodGet, "/v1/distribution/manifest", nil),
	)
	expectedURL := "/v1/distribution/files/" + created.Action.SHA256 + "/provider-action.json"
	if manifestResponse.Code != http.StatusOK ||
		!strings.Contains(manifestResponse.Body.String(), `"kind":"provider.upsert"`) ||
		!strings.Contains(manifestResponse.Body.String(), `"apply_on":["initial_sync","manual_sync"]`) ||
		!strings.Contains(manifestResponse.Body.String(), `"payload_url":"`+expectedURL+`"`) ||
		!strings.Contains(manifestResponse.Body.String(), `"merge_policy":`) {
		t.Fatalf("公开清单缺少数据库操作: %s", manifestResponse.Body.String())
	}

	fileResponse := httptest.NewRecorder()
	server.engine.ServeHTTP(
		fileResponse,
		httptest.NewRequest(http.MethodGet, expectedURL, nil),
	)
	if fileResponse.Code != http.StatusOK || fileResponse.Body.String() != string(payload) {
		t.Fatalf("公开操作载荷不正确: code=%d body=%s", fileResponse.Code, fileResponse.Body.String())
	}

	deleteRequest := httptest.NewRequest(
		http.MethodDelete,
		"/v1/admin/distribution/actions/"+created.Action.Key,
		nil,
	)
	deleteRequest.Header.Set("Authorization", "Bearer "+adminToken)
	deleteResponse := httptest.NewRecorder()
	server.adminEngine.ServeHTTP(deleteResponse, deleteRequest)
	if deleteResponse.Code != http.StatusNoContent {
		t.Fatalf("删除官方数据库操作期望 204，实际 %d", deleteResponse.Code)
	}
}

func testAPIOfficialProviderActionPayload() []byte {
	return []byte(`{"schema_version":1,"id":"official-provider.api-test","revision":1,"kind":"provider.upsert","apply_on":["initial_sync","manual_sync"],"provider":{"id":"33333333-3333-4333-8333-333333333333","name":"API Test","baseURL":"https://example.com/v1","apiFormat":"openai-compatible","models":[{"id":"44444444-4444-4444-8444-444444444444","modelName":"api-test"}]},"merge_policy":{"provider_fields":"update_if_unmodified","api_keys":"preserve_local_if_nonempty","header_overrides":"update_if_unmodified","proxy_configuration":"preserve_local","models":{"fields":"update_if_unmodified","is_activated":"preserve_local","on_missing":"insert","on_removed":"delete_if_unmodified","user_models":"preserve"},"if_user_deleted":"restore_on_manual_sync"}}`)
}
