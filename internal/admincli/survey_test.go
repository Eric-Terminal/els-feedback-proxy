package admincli

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSurveyCreateAndResultsUseAdminAPI(t *testing.T) {
	t.Setenv("ANNOUNCEMENT_ADMIN_TOKEN", "test-admin-token")
	const input = `{"id":1,"title":"测试","questions":[{"id":"q","question":"选择","type":"single_select","options":[{"id":"a","label":"A"}]}],"enabled":false}`

	requests := make(chan string, 2)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		requests <- request.Method + " " + request.URL.EscapedPath()
		if request.Header.Get("Authorization") != "Bearer test-admin-token" {
			t.Fatalf("意见征集命令缺少管理鉴权")
		}
		response.Header().Set("Content-Type", "application/json")
		if request.Method == http.MethodPost {
			body, err := io.ReadAll(request.Body)
			if err != nil || string(body) != input {
				t.Fatalf("创建意见征集请求正文不正确: %s err=%v", body, err)
			}
			response.WriteHeader(http.StatusCreated)
			_, _ = response.Write([]byte(`{"success":true,"record":{"key":"survey key"}}`))
			return
		}
		_, _ = response.Write([]byte(`{"success":true,"response_count":2,"responses":[]}`))
	}))
	defer server.Close()

	var createOutput bytes.Buffer
	handled, err := Run(
		[]string{"survey", "create", "--file", "-", "--admin-url", server.URL},
		strings.NewReader(input),
		&createOutput,
		io.Discard,
	)
	if err != nil || !handled {
		t.Fatalf("创建意见征集失败: handled=%t err=%v", handled, err)
	}

	var resultsOutput bytes.Buffer
	_, err = Run(
		[]string{"survey", "results", "--key", "survey key", "--admin-url", server.URL},
		strings.NewReader(""),
		&resultsOutput,
		io.Discard,
	)
	if err != nil {
		t.Fatalf("查看意见征集结果失败: %v", err)
	}

	if createRequest := <-requests; createRequest != "POST /v1/admin/surveys" {
		t.Fatalf("创建意见征集路径不正确: %s", createRequest)
	}
	if resultsRequest := <-requests; resultsRequest != "GET /v1/admin/surveys/survey%20key/results" {
		t.Fatalf("意见征集结果路径不正确: %s", resultsRequest)
	}
	if !strings.Contains(resultsOutput.String(), `"response_count": 2`) {
		t.Fatalf("意见征集结果输出不正确: %s", resultsOutput.String())
	}
}
