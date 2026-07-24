package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"els-feedback-proxy/internal/config"
	"els-feedback-proxy/internal/security"
	"els-feedback-proxy/internal/store"
)

func TestSurveyPublicSubmissionAndPrivateResults(t *testing.T) {
	const adminToken = "survey-admin-token"
	server := newSurveyTestServer(t, adminToken)
	createResponse := performAdminRequest(
		server,
		http.MethodPost,
		"/v1/admin/surveys",
		`{
			"id": 2026072401,
			"title": "界面方案征集",
			"description": "选择你更喜欢的方案。",
			"language": "zh-Hans",
			"platform": "iOS",
			"enabled": true,
			"questions": [{
				"id": "design",
				"question": "你更喜欢哪种布局？",
				"type": "single_select",
				"allow_other": true,
				"required": true,
				"options": [
					{"id": "compact", "label": "紧凑布局"},
					{"id": "relaxed", "label": "宽松布局"}
				]
			}]
		}`,
		adminToken,
	)
	if createResponse.Code != http.StatusCreated {
		t.Fatalf("创建意见征集期望 201，实际 %d body=%s", createResponse.Code, createResponse.Body.String())
	}
	var createdPayload struct {
		Record store.SurveyRecord `json:"record"`
	}
	if err := json.Unmarshal(createResponse.Body.Bytes(), &createdPayload); err != nil {
		t.Fatalf("解析创建响应失败: %v", err)
	}

	publicResponse := httptest.NewRecorder()
	server.engine.ServeHTTP(
		publicResponse,
		httptest.NewRequest(http.MethodGet, "/v1/surveys", nil),
	)
	if publicResponse.Code != http.StatusOK ||
		!strings.Contains(publicResponse.Body.String(), `"key":"`+createdPayload.Record.Key+`"`) ||
		strings.Contains(publicResponse.Body.String(), `"enabled"`) {
		t.Fatalf("公开意见征集响应不正确: code=%d body=%s", publicResponse.Code, publicResponse.Body.String())
	}
	if publicResponse.Header().Get("Cloudflare-CDN-Cache-Control") == "" ||
		publicResponse.Header().Get("ETag") == "" {
		t.Fatal("公开意见征集接口缺少 CDN 缓存头或 ETag")
	}

	body := []byte(`{"answers":[{"question_id":"design","selected_option_ids":["compact"],"other_text":"文字更清楚"}],"platform":"iOS","app_build":"120","language":"zh-Hans"}`)
	path := "/v1/surveys/" + createdPayload.Record.Key + "/responses"
	bundle := server.challenges.Issue("192.0.2.1", 0)
	timestamp := fmt.Sprintf("%d", time.Now().Unix())
	signature := signSurveyTestRequest(bundle, timestamp, path, body)
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(string(body)))
	request.RemoteAddr = "192.0.2.1:12345"
	request.Header.Set("User-Agent", "ETOS LLM Studio/120")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-ELS-Challenge-Id", bundle.ChallengeID)
	request.Header.Set("X-ELS-Timestamp", timestamp)
	request.Header.Set("X-ELS-Signature", signature)
	submitResponse := httptest.NewRecorder()
	server.engine.ServeHTTP(submitResponse, request)
	if submitResponse.Code != http.StatusCreated {
		t.Fatalf("提交匿名答卷期望 201，实际 %d body=%s", submitResponse.Code, submitResponse.Body.String())
	}

	resultsResponse := performAdminRequest(
		server,
		http.MethodGet,
		"/v1/admin/surveys/"+createdPayload.Record.Key+"/results",
		"",
		adminToken,
	)
	if resultsResponse.Code != http.StatusOK ||
		!strings.Contains(resultsResponse.Body.String(), `"response_count":1`) ||
		!strings.Contains(resultsResponse.Body.String(), `"other_text":"文字更清楚"`) ||
		strings.Contains(strings.ToLower(resultsResponse.Body.String()), `"ip"`) {
		t.Fatalf("私有统计响应不正确: code=%d body=%s", resultsResponse.Code, resultsResponse.Body.String())
	}

	changed := strings.Replace(createResponse.Body.String(), "界面方案征集", "修改标题", 1)
	var createEnvelope struct {
		Record json.RawMessage `json:"record"`
	}
	if err := json.Unmarshal([]byte(changed), &createEnvelope); err != nil {
		t.Fatalf("准备更新请求失败: %v", err)
	}
	updateResponse := performAdminRequest(
		server,
		http.MethodPut,
		"/v1/admin/surveys/"+createdPayload.Record.Key,
		string(createEnvelope.Record),
		adminToken,
	)
	if updateResponse.Code != http.StatusConflict {
		t.Fatalf("收到答卷后修改定义应返回 409，实际 %d body=%s", updateResponse.Code, updateResponse.Body.String())
	}
}

func TestSurveyManagementPageOnlyExistsOnPrivateListener(t *testing.T) {
	server := newSurveyTestServer(t, "survey-admin-token")
	server.cfg.AdminWebAuthDisabled = true

	publicResponse := httptest.NewRecorder()
	server.engine.ServeHTTP(
		publicResponse,
		httptest.NewRequest(http.MethodGet, "/admin/surveys", nil),
	)
	if publicResponse.Code != http.StatusNotFound {
		t.Fatalf("公网监听器不应注册意见征集管理页，实际 %d", publicResponse.Code)
	}

	adminResponse := httptest.NewRecorder()
	server.adminEngine.ServeHTTP(
		adminResponse,
		httptest.NewRequest(http.MethodGet, "/admin/surveys", nil),
	)
	if adminResponse.Code != http.StatusOK ||
		!strings.Contains(adminResponse.Body.String(), "稿件与语言版本") ||
		!strings.Contains(adminResponse.Body.String(), "/admin/assets/survey.js") {
		t.Fatalf("内网意见征集管理页响应不正确: code=%d body=%s", adminResponse.Code, adminResponse.Body.String())
	}
}

func newSurveyTestServer(t *testing.T, adminToken string) *Server {
	t.Helper()
	surveys, err := store.NewSurveyStore(t.TempDir())
	if err != nil {
		t.Fatalf("初始化意见征集存储失败: %v", err)
	}
	return NewServer(
		config.Config{
			AdminListenAddr:          "127.0.0.1:8521",
			AnnouncementAdminToken:   adminToken,
			AnnouncementCacheMaxAge:  300,
			AdminLoginLimitPerWindow: 10,
			ChallengeLimitPerWindow:  30,
			SubmitLimitPerWindow:     10,
			RateWindow:               15 * time.Minute,
			DuplicateWindow:          5 * time.Minute,
			RequiredUAKeyword:        "ETOS",
		},
		nil,
		&announcementTestLimiter{},
		nil,
		security.NewChallengeManager(2*time.Minute, 90*time.Second, 5, 10*time.Minute),
		nil,
		nil,
		nil,
		nil,
		nil,
		surveys,
	)
}

func signSurveyTestRequest(
	bundle security.ChallengeBundle,
	timestamp string,
	path string,
	body []byte,
) string {
	bodyHash := sha256.Sum256(body)
	signingText := fmt.Sprintf(
		"%s\n%s\n%s\n%s\n%s",
		http.MethodPost,
		path,
		timestamp,
		hex.EncodeToString(bodyHash[:]),
		bundle.Nonce,
	)
	mac := hmac.New(sha256.New, []byte(bundle.ClientSecret))
	_, _ = mac.Write([]byte(signingText))
	return hex.EncodeToString(mac.Sum(nil))
}
