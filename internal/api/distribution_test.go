package api

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"els-feedback-proxy/internal/config"
	"els-feedback-proxy/internal/store"
)

func TestDistributionAdminUploadPublishesImmutableFile(t *testing.T) {
	const adminToken = "distribution-admin-token"
	server := newDistributionTestServer(t, adminToken)

	createResponse := performDistributionRequest(
		t,
		server,
		http.MethodPost,
		"/v1/admin/distribution",
		adminToken,
		map[string]string{
			"name":             "默认提供商",
			"destination_path": "/Documents/Providers",
			"enabled":          "true",
		},
		"provider.json",
		[]byte(`{"api_format":"openai-compatible"}`),
	)
	if createResponse.Code != http.StatusCreated {
		t.Fatalf("创建官方数据期望 201，实际 %d body=%s", createResponse.Code, createResponse.Body.String())
	}

	var createdPayload struct {
		Record store.DistributionRecord `json:"record"`
	}
	if err := json.Unmarshal(createResponse.Body.Bytes(), &createdPayload); err != nil {
		t.Fatalf("解析创建响应失败: %v", err)
	}
	record := createdPayload.Record
	if record.Key == "" || record.SHA256 == "" {
		t.Fatalf("创建响应缺少文件标识: %#v", record)
	}

	manifestRequest := httptest.NewRequest(http.MethodGet, "/v1/distribution/manifest", nil)
	manifestResponse := httptest.NewRecorder()
	server.engine.ServeHTTP(manifestResponse, manifestRequest)
	if manifestResponse.Code != http.StatusOK {
		t.Fatalf("官方数据清单期望 200，实际 %d", manifestResponse.Code)
	}
	expectedURL := "/v1/distribution/files/" + record.SHA256 + "/provider.json"
	if !strings.Contains(manifestResponse.Body.String(), `"url":"`+expectedURL+`"`) ||
		!strings.Contains(manifestResponse.Body.String(), `"path":"/Documents/Providers"`) {
		t.Fatalf("官方数据清单内容不正确: %s", manifestResponse.Body.String())
	}
	if manifestResponse.Header().Get("ETag") == "" {
		t.Fatalf("官方数据清单应返回 ETag")
	}

	conditionalRequest := httptest.NewRequest(http.MethodGet, "/v1/distribution/manifest", nil)
	conditionalRequest.Header.Set("If-None-Match", manifestResponse.Header().Get("ETag"))
	conditionalResponse := httptest.NewRecorder()
	server.engine.ServeHTTP(conditionalResponse, conditionalRequest)
	if conditionalResponse.Code != http.StatusNotModified {
		t.Fatalf("官方数据清单 ETag 命中应返回 304，实际 %d", conditionalResponse.Code)
	}

	fileResponse := httptest.NewRecorder()
	server.engine.ServeHTTP(
		fileResponse,
		httptest.NewRequest(http.MethodGet, expectedURL, nil),
	)
	if fileResponse.Code != http.StatusOK ||
		fileResponse.Body.String() != `{"api_format":"openai-compatible"}` {
		t.Fatalf("公开文件响应不正确: code=%d body=%s", fileResponse.Code, fileResponse.Body.String())
	}
	if !strings.Contains(fileResponse.Header().Get("Cache-Control"), "immutable") ||
		fileResponse.Header().Get("ETag") != `"`+record.SHA256+`"` {
		t.Fatalf("公开文件缓存头不正确: %#v", fileResponse.Header())
	}

	publicAdminResponse := httptest.NewRecorder()
	server.engine.ServeHTTP(
		publicAdminResponse,
		httptest.NewRequest(http.MethodGet, "/v1/admin/distribution", nil),
	)
	if publicAdminResponse.Code != http.StatusNotFound {
		t.Fatalf("公网监听器不应注册官方数据管理 API，实际 %d", publicAdminResponse.Code)
	}
}

func TestDistributionAdminCanDisableAndDeleteRecord(t *testing.T) {
	const adminToken = "distribution-admin-token"
	server := newDistributionTestServer(t, adminToken)

	createResponse := performDistributionRequest(
		t,
		server,
		http.MethodPost,
		"/v1/admin/distribution",
		adminToken,
		map[string]string{
			"name":             "测试文件",
			"destination_path": "/Documents",
			"enabled":          "true",
		},
		"test.json",
		[]byte("{}"),
	)
	var createdPayload struct {
		Record store.DistributionRecord `json:"record"`
	}
	if err := json.Unmarshal(createResponse.Body.Bytes(), &createdPayload); err != nil {
		t.Fatalf("解析创建响应失败: %v", err)
	}

	updateResponse := performDistributionRequest(
		t,
		server,
		http.MethodPut,
		"/v1/admin/distribution/"+createdPayload.Record.Key,
		adminToken,
		map[string]string{
			"name":             "已停用文件",
			"destination_path": "/Documents",
			"enabled":          "false",
		},
		"",
		nil,
	)
	if updateResponse.Code != http.StatusOK {
		t.Fatalf("停用官方数据期望 200，实际 %d body=%s", updateResponse.Code, updateResponse.Body.String())
	}

	manifestResponse := httptest.NewRecorder()
	server.engine.ServeHTTP(
		manifestResponse,
		httptest.NewRequest(http.MethodGet, "/v1/distribution/manifest", nil),
	)
	if !strings.Contains(manifestResponse.Body.String(), `"downloads":[]`) {
		t.Fatalf("停用条目不应进入公开清单: %s", manifestResponse.Body.String())
	}

	deleteRequest := httptest.NewRequest(
		http.MethodDelete,
		"/v1/admin/distribution/"+createdPayload.Record.Key,
		nil,
	)
	deleteRequest.Header.Set("Authorization", "Bearer "+adminToken)
	deleteResponse := httptest.NewRecorder()
	server.adminEngine.ServeHTTP(deleteResponse, deleteRequest)
	if deleteResponse.Code != http.StatusNoContent {
		t.Fatalf("删除官方数据期望 204，实际 %d", deleteResponse.Code)
	}
}

func TestDistributionAdminPageRequiresProtectedSession(t *testing.T) {
	const adminToken = "distribution-admin-token"
	server := newDistributionTestServer(t, adminToken)

	publicResponse := httptest.NewRecorder()
	server.engine.ServeHTTP(
		publicResponse,
		httptest.NewRequest(http.MethodGet, "/admin/distribution", nil),
	)
	if publicResponse.Code != http.StatusNotFound {
		t.Fatalf("公网监听器不应注册官方数据页面，实际 %d", publicResponse.Code)
	}

	unauthenticatedResponse := httptest.NewRecorder()
	server.adminEngine.ServeHTTP(
		unauthenticatedResponse,
		httptest.NewRequest(http.MethodGet, "/admin/distribution", nil),
	)
	if unauthenticatedResponse.Code != http.StatusSeeOther {
		t.Fatalf("未登录访问官方数据页面应跳转，实际 %d", unauthenticatedResponse.Code)
	}

	form := url.Values{"password": {adminToken}}
	loginRequest := httptest.NewRequest(
		http.MethodPost,
		"/admin/login",
		strings.NewReader(form.Encode()),
	)
	loginRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	loginResponse := httptest.NewRecorder()
	server.adminEngine.ServeHTTP(loginResponse, loginRequest)

	cookies := loginResponse.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("登录后未获得管理会话")
	}
	pageRequest := httptest.NewRequest(http.MethodGet, "/admin/distribution", nil)
	pageRequest.AddCookie(cookies[0])
	pageResponse := httptest.NewRecorder()
	server.adminEngine.ServeHTTP(pageResponse, pageRequest)
	if pageResponse.Code != http.StatusOK ||
		!strings.Contains(pageResponse.Body.String(), "配置下发内容") {
		t.Fatalf("登录后未返回官方数据页面: code=%d body=%s", pageResponse.Code, pageResponse.Body.String())
	}
}

func newDistributionTestServer(t *testing.T, adminToken string) *Server {
	t.Helper()
	distributionStore, err := store.NewDistributionStore(t.TempDir())
	if err != nil {
		t.Fatalf("初始化官方数据存储失败: %v", err)
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
		nil,
		distributionStore,
	)
}

func performDistributionRequest(
	t *testing.T,
	server *Server,
	method string,
	requestPath string,
	adminToken string,
	fields map[string]string,
	fileName string,
	fileData []byte,
) *httptest.ResponseRecorder {
	t.Helper()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for key, value := range fields {
		if err := writer.WriteField(key, value); err != nil {
			t.Fatalf("写入 multipart 字段失败: %v", err)
		}
	}
	if fileName != "" {
		fileWriter, err := writer.CreateFormFile("file", fileName)
		if err != nil {
			t.Fatalf("创建 multipart 文件失败: %v", err)
		}
		if _, err := fileWriter.Write(fileData); err != nil {
			t.Fatalf("写入 multipart 文件失败: %v", err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("关闭 multipart 表单失败: %v", err)
	}

	request := httptest.NewRequest(method, requestPath, &body)
	request.Header.Set("Authorization", "Bearer "+adminToken)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response := httptest.NewRecorder()
	server.adminEngine.ServeHTTP(response, request)
	return response
}
