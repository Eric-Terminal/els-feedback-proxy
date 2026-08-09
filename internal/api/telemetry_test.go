package api

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"els-feedback-proxy/internal/config"
	"els-feedback-proxy/internal/telemetry"
)

func TestTelemetryUploadAcceptsAndPersistentlyDeduplicates(t *testing.T) {
	server, store := newTelemetryTestServer(t)
	body, payloadID := telemetryTestBody(t, `{"cpu":1}`)

	response := performTelemetryUpload(server, body, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("首次上传应成功，实际 %d: %s", response.Code, response.Body.String())
	}
	var first telemetry.UploadResponse
	if err := json.Unmarshal(response.Body.Bytes(), &first); err != nil {
		t.Fatalf("解析首次响应失败: %v", err)
	}
	if first.SchemaVersion != telemetry.CurrentSchemaVersion ||
		len(first.Results) != 1 || first.Results[0].Status != "accepted" {
		t.Fatalf("首次上传状态应为 accepted: %+v", first.Results)
	}

	response = performTelemetryUpload(server, body, nil)
	var second telemetry.UploadResponse
	if err := json.Unmarshal(response.Body.Bytes(), &second); err != nil {
		t.Fatalf("解析重复响应失败: %v", err)
	}
	if response.Code != http.StatusOK || len(second.Results) != 1 ||
		second.Results[0].Status != "duplicate" {
		t.Fatalf("重复上传应返回 duplicate: code=%d results=%+v", response.Code, second.Results)
	}
	if len(store.Manifest().Entries) != 1 ||
		store.Manifest().Entries[0].PayloadID != payloadID {
		t.Fatalf("重复上传不应产生第二个文件")
	}
}

func TestTelemetryUploadEchoesLegacySchemaVersion(t *testing.T) {
	server, _ := newTelemetryTestServer(t)
	body, _ := telemetryTestBodyForSchema(
		t,
		telemetry.LegacySchemaVersion,
		`{"cpu":1}`,
	)
	response := performTelemetryUpload(server, body, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("v1 旧客户端上传应继续成功，实际 %d: %s", response.Code, response.Body.String())
	}
	var decoded telemetry.UploadResponse
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("解析 v1 上传响应失败: %v", err)
	}
	if decoded.SchemaVersion != telemetry.LegacySchemaVersion ||
		len(decoded.Results) != 1 || decoded.Results[0].Status != "accepted" {
		t.Fatalf("v1 响应必须回显请求版本: %+v", decoded)
	}
}

func TestTelemetryUploadRejectsUnsafeCompressedAndOversizedRequests(t *testing.T) {
	server, _ := newTelemetryTestServer(t)
	body, _ := telemetryTestBody(t, `{"cpu":1}`)
	unsafe := bytes.Replace(body, []byte(`"contains_chat_content":false`), []byte(
		`"contains_chat_content":true`,
	), 1)
	response := performTelemetryUpload(server, unsafe, nil)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("不安全隐私声明应返回 400，实际 %d", response.Code)
	}

	response = performTelemetryUpload(server, body, map[string]string{
		"Content-Encoding": "gzip",
	})
	if response.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("压缩请求应返回 415，实际 %d", response.Code)
	}

	oversized := bytes.Repeat([]byte("x"), telemetry.MaxRequestBodyBytes+1)
	response = performTelemetryUpload(server, oversized, nil)
	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("超过 4 MiB 应返回 413，实际 %d", response.Code)
	}
}

func TestTelemetryAdminExportAndConfirmOnlyExistOnAdminListener(t *testing.T) {
	server, _ := newTelemetryTestServer(t)
	body, payloadID := telemetryTestBody(t, `{"hang":1}`)
	if response := performTelemetryUpload(server, body, nil); response.Code != http.StatusOK {
		t.Fatalf("准备测试遥测失败: %d %s", response.Code, response.Body.String())
	}

	public := httptest.NewRecorder()
	server.engine.ServeHTTP(
		public,
		httptest.NewRequest(http.MethodGet, "/v1/admin/telemetry/manifest", nil),
	)
	if public.Code != http.StatusNotFound {
		t.Fatalf("公网监听器不应注册遥测管理 API，实际 %d", public.Code)
	}

	unauthorized := httptest.NewRecorder()
	server.adminEngine.ServeHTTP(
		unauthorized,
		httptest.NewRequest(http.MethodGet, "/v1/admin/telemetry/manifest", nil),
	)
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("未鉴权管理请求应返回 401，实际 %d", unauthorized.Code)
	}

	fileResponse := performTelemetryAdminRequest(
		server,
		http.MethodGet,
		"/v1/admin/telemetry/files/"+payloadID,
		nil,
	)
	if fileResponse.Code != http.StatusOK || !json.Valid(fileResponse.Body.Bytes()) {
		t.Fatalf("管理端应导出有效原始 JSON: %d %s", fileResponse.Code, fileResponse.Body.String())
	}
	fileSum := sha256.Sum256(fileResponse.Body.Bytes())
	if fileResponse.Header().Get("X-ELS-File-SHA256") != hex.EncodeToString(fileSum[:]) {
		t.Fatalf("导出响应哈希头与文件不一致")
	}

	confirmBody, err := json.Marshal(map[string]any{"payload_ids": []string{payloadID}})
	if err != nil {
		t.Fatalf("编码确认请求失败: %v", err)
	}
	confirmResponse := performTelemetryAdminRequest(
		server,
		http.MethodPost,
		"/v1/admin/telemetry/confirm",
		confirmBody,
	)
	if confirmResponse.Code != http.StatusOK ||
		!strings.Contains(confirmResponse.Body.String(), payloadID) {
		t.Fatalf("精确确认失败: %d %s", confirmResponse.Code, confirmResponse.Body.String())
	}
	missing := performTelemetryAdminRequest(
		server,
		http.MethodGet,
		"/v1/admin/telemetry/files/"+payloadID,
		nil,
	)
	if missing.Code != http.StatusNotFound {
		t.Fatalf("确认后服务端原始文件应不存在，实际 %d", missing.Code)
	}
}

func TestTelemetryUsesIndependentInMemoryRateLimit(t *testing.T) {
	server, _ := newTelemetryTestServer(t)
	server.telemetryLimiter = telemetryDenyLimiter{}
	body, _ := telemetryTestBody(t, `{"cpu":1}`)
	response := performTelemetryUpload(server, body, nil)
	if response.Code != http.StatusTooManyRequests {
		t.Fatalf("独立遥测限流拒绝时应返回 429，实际 %d", response.Code)
	}
}

type telemetryDenyLimiter struct{}

func (telemetryDenyLimiter) Allow(string, int, time.Duration) bool {
	return false
}

func newTelemetryTestServer(t *testing.T) (*Server, *telemetry.Store) {
	t.Helper()
	store, err := telemetry.NewStore(t.TempDir(), telemetry.StoreOptions{
		Retention:    30 * 24 * time.Hour,
		MaxTotalSize: 16 << 20,
	})
	if err != nil {
		t.Fatalf("初始化遥测测试存储失败: %v", err)
	}
	const adminToken = "telemetry-admin-token"
	server := NewServer(
		config.Config{
			AdminListenAddr:        "127.0.0.1:8521",
			AnnouncementAdminToken: adminToken,
			RequiredUAKeyword:      "ETOS LLM Studio",
			TelemetryRateLimit:     30,
			TrustedProxyCIDRs:      []string{},
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
		store,
	)
	return server, store
}

func telemetryTestBody(t *testing.T, rawPayload string) ([]byte, string) {
	return telemetryTestBodyForSchema(t, telemetry.CurrentSchemaVersion, rawPayload)
}

func telemetryTestBodyForSchema(
	t *testing.T,
	schemaVersion int,
	rawPayload string,
) ([]byte, string) {
	t.Helper()
	payload := []byte(rawPayload)
	if schemaVersion == telemetry.CurrentSchemaVersion {
		var payloadObject map[string]any
		if err := json.Unmarshal(payload, &payloadObject); err != nil {
			t.Fatalf("解析遥测测试 payload 失败: %v", err)
		}
		payloadObject["_etos"] = map[string]any{
			"call_stack_frames_emitted": 0,
			"format":                    "metric-kit-flat-v1",
			"truncated":                 false,
		}
		var err error
		payload, err = json.Marshal(payloadObject)
		if err != nil {
			t.Fatalf("编码 v2 遥测测试 payload 失败: %v", err)
		}
	}
	sum := sha256.Sum256(payload)
	payloadID := hex.EncodeToString(sum[:])
	envelope := telemetry.Envelope{
		SchemaVersion: schemaVersion,
		PayloadID:     payloadID,
		Kind:          telemetry.PayloadKindMetric,
		CapturedAt:    time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC),
		App: telemetry.AppMetadata{
			Version:      "2.7.0",
			Build:        "270",
			Distribution: "testflight",
		},
		Platform: telemetry.PlatformMetadata{
			Name:         "ios",
			OSVersion:    "26.0",
			DeviceClass:  "iPhone17,2",
			Architecture: "arm64",
		},
		Privacy: telemetry.PrivacyDeclaration{},
		Payload: payload,
	}
	body, err := json.Marshal(struct {
		SchemaVersion int                  `json:"schema_version"`
		Envelopes     []telemetry.Envelope `json:"envelopes"`
	}{
		SchemaVersion: schemaVersion,
		Envelopes:     []telemetry.Envelope{envelope},
	})
	if err != nil {
		t.Fatalf("编码遥测测试请求失败: %v", err)
	}
	return body, payloadID
}

func performTelemetryUpload(
	server *Server,
	body []byte,
	headers map[string]string,
) *httptest.ResponseRecorder {
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/telemetry", bytes.NewReader(body))
	request.Header.Set("User-Agent", "ETOS LLM Studio/2.7.0 (iOS; MetricKit Telemetry)")
	request.Header.Set("Content-Type", "application/json")
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	server.engine.ServeHTTP(response, request)
	return response
}

func performTelemetryAdminRequest(
	server *Server,
	method string,
	path string,
	body []byte,
) *httptest.ResponseRecorder {
	response := httptest.NewRecorder()
	request := httptest.NewRequest(method, path, bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer telemetry-admin-token")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	server.adminEngine.ServeHTTP(response, request)
	return response
}
