package admincli

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRunWithoutArgumentsStartsServerFlow(t *testing.T) {
	handled, err := Run(nil, strings.NewReader(""), io.Discard, io.Discard)
	if err != nil {
		t.Fatalf("无参数不应返回错误: %v", err)
	}
	if handled {
		t.Fatalf("无参数时不应拦截服务端启动流程")
	}
}

func TestAnnouncementListUsesAdminAPI(t *testing.T) {
	t.Setenv("ANNOUNCEMENT_ADMIN_TOKEN", "test-admin-token")

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet || request.URL.Path != "/v1/admin/announcements" {
			t.Fatalf("公告列表请求不正确: %s %s", request.Method, request.URL.Path)
		}
		if request.Header.Get("Authorization") != "Bearer test-admin-token" {
			t.Fatalf("公告列表缺少管理鉴权")
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"success":true,"records":[]}`))
	}))
	defer server.Close()

	var stdout bytes.Buffer
	handled, err := Run(
		[]string{"announcement", "list", "--admin-url", server.URL},
		strings.NewReader(""),
		&stdout,
		io.Discard,
	)
	if err != nil {
		t.Fatalf("列出公告失败: %v", err)
	}
	if !handled {
		t.Fatalf("公告命令应由 CLI 处理")
	}
	if !strings.Contains(stdout.String(), `"records": []`) {
		t.Fatalf("公告列表输出不正确: %s", stdout.String())
	}
}

func TestAnnouncementCreateReadsJSONFromStdin(t *testing.T) {
	t.Setenv("ANNOUNCEMENT_ADMIN_TOKEN", "test-admin-token")
	const input = `{"id":2026072301,"type":"info","title":"测试","body":"正文","enabled":false}`

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != "/v1/admin/announcements" {
			t.Fatalf("创建公告请求不正确: %s %s", request.Method, request.URL.Path)
		}
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatalf("读取创建请求失败: %v", err)
		}
		if string(body) != input {
			t.Fatalf("创建请求正文不正确: %s", body)
		}
		response.Header().Set("Content-Type", "application/json")
		response.WriteHeader(http.StatusCreated)
		_, _ = response.Write([]byte(`{"success":true,"record":{"key":"test-key"}}`))
	}))
	defer server.Close()

	var stdout bytes.Buffer
	_, err := Run(
		[]string{"announcement", "create", "--file", "-", "--admin-url", server.URL},
		strings.NewReader(input),
		&stdout,
		io.Discard,
	)
	if err != nil {
		t.Fatalf("创建公告失败: %v", err)
	}
	if !strings.Contains(stdout.String(), `"key": "test-key"`) {
		t.Fatalf("创建公告输出不正确: %s", stdout.String())
	}
}

func TestAnnouncementUpdateAndDeleteUseEscapedKey(t *testing.T) {
	t.Setenv("ANNOUNCEMENT_ADMIN_TOKEN", "test-admin-token")
	const input = `{"id":1,"type":"warning","title":"更新","body":"正文","enabled":true}`

	requests := make(chan string, 2)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		requests <- request.Method + " " + request.URL.EscapedPath()
		if request.Method == http.MethodDelete {
			response.WriteHeader(http.StatusNoContent)
			return
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"success":true,"record":{"key":"zh Hans"}}`))
	}))
	defer server.Close()

	var updateOutput bytes.Buffer
	_, err := Run(
		[]string{
			"announcement", "update",
			"--key", "zh Hans",
			"--file", "-",
			"--admin-url", server.URL,
		},
		strings.NewReader(input),
		&updateOutput,
		io.Discard,
	)
	if err != nil {
		t.Fatalf("更新公告失败: %v", err)
	}

	var deleteOutput bytes.Buffer
	_, err = Run(
		[]string{
			"announcement", "delete",
			"--key", "zh Hans",
			"--admin-url", server.URL,
		},
		strings.NewReader(""),
		&deleteOutput,
		io.Discard,
	)
	if err != nil {
		t.Fatalf("删除公告失败: %v", err)
	}

	if updateRequest := <-requests; updateRequest != "PUT /v1/admin/announcements/zh%20Hans" {
		t.Fatalf("更新请求路径不正确: %s", updateRequest)
	}
	if deleteRequest := <-requests; deleteRequest != "DELETE /v1/admin/announcements/zh%20Hans" {
		t.Fatalf("删除请求路径不正确: %s", deleteRequest)
	}
	if !strings.Contains(deleteOutput.String(), `"success": true`) {
		t.Fatalf("删除输出不正确: %s", deleteOutput.String())
	}
}

func TestAnnouncementHelpDoesNotRequireToken(t *testing.T) {
	t.Setenv("ANNOUNCEMENT_ADMIN_TOKEN", "")

	var stdout bytes.Buffer
	handled, err := Run(
		[]string{"announcement", "--help"},
		strings.NewReader(""),
		&stdout,
		io.Discard,
	)
	if err != nil {
		t.Fatalf("显示帮助不应需要管理口令: %v", err)
	}
	if !handled || !strings.Contains(stdout.String(), "announcement create") {
		t.Fatalf("公告帮助输出不正确: %s", stdout.String())
	}
}
