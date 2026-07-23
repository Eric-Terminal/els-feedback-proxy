package admincli

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDistributionUploadUsesMultipartAdminAPI(t *testing.T) {
	t.Setenv("ANNOUNCEMENT_ADMIN_TOKEN", "test-admin-token")
	filePath := filepath.Join(t.TempDir(), "provider.json")
	if err := os.WriteFile(filePath, []byte(`{"name":"测试"}`), 0o600); err != nil {
		t.Fatalf("写入测试上传文件失败: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != "/v1/admin/distribution" {
			t.Fatalf("上传请求不正确: %s %s", request.Method, request.URL.Path)
		}
		if request.Header.Get("Authorization") != "Bearer test-admin-token" {
			t.Fatalf("上传请求缺少管理鉴权")
		}
		if err := request.ParseMultipartForm(1 << 20); err != nil {
			t.Fatalf("解析上传表单失败: %v", err)
		}
		if request.FormValue("name") != "默认提供商" ||
			request.FormValue("destination_path") != "/Documents/Providers" ||
			request.FormValue("enabled") != "true" {
			t.Fatalf("上传字段不正确: %#v", request.MultipartForm.Value)
		}
		file, header, err := request.FormFile("file")
		if err != nil {
			t.Fatalf("读取上传文件失败: %v", err)
		}
		defer file.Close()
		data, err := io.ReadAll(file)
		if err != nil {
			t.Fatalf("读取上传内容失败: %v", err)
		}
		if header.Filename != "provider.json" || string(data) != `{"name":"测试"}` {
			t.Fatalf("上传文件内容不正确: name=%s data=%s", header.Filename, data)
		}
		response.Header().Set("Content-Type", "application/json")
		response.WriteHeader(http.StatusCreated)
		_, _ = response.Write([]byte(`{"success":true,"record":{"key":"provider"}}`))
	}))
	defer server.Close()

	var stdout bytes.Buffer
	handled, err := Run(
		[]string{
			"distribution", "upload",
			"--name", "默认提供商",
			"--path", "/Documents/Providers",
			"--file", filePath,
			"--admin-url", server.URL,
		},
		strings.NewReader(""),
		&stdout,
		io.Discard,
	)
	if err != nil {
		t.Fatalf("上传官方数据失败: %v", err)
	}
	if !handled || !strings.Contains(stdout.String(), `"key": "provider"`) {
		t.Fatalf("上传输出不正确: %s", stdout.String())
	}
}

func TestDistributionUpdateAndDeleteUseEscapedKey(t *testing.T) {
	t.Setenv("ANNOUNCEMENT_ADMIN_TOKEN", "test-admin-token")

	requests := make(chan string, 2)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		requests <- request.Method + " " + request.URL.EscapedPath()
		if request.Method == http.MethodDelete {
			response.WriteHeader(http.StatusNoContent)
			return
		}
		reader, err := request.MultipartReader()
		if err != nil {
			t.Fatalf("更新请求不是 multipart 表单: %v", err)
		}
		fields := readMultipartFields(t, reader)
		if fields["name"] != "更新名称" ||
			fields["destination_path"] != "/Documents/Providers" ||
			fields["enabled"] != "false" {
			t.Fatalf("更新字段不正确: %#v", fields)
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"success":true,"record":{"key":"zh Hans"}}`))
	}))
	defer server.Close()

	var updateOutput bytes.Buffer
	_, err := Run(
		[]string{
			"distribution", "update",
			"--key", "zh Hans",
			"--name", "更新名称",
			"--path", "/Documents/Providers",
			"--disabled",
			"--admin-url", server.URL,
		},
		strings.NewReader(""),
		&updateOutput,
		io.Discard,
	)
	if err != nil {
		t.Fatalf("更新官方数据失败: %v", err)
	}

	var deleteOutput bytes.Buffer
	_, err = Run(
		[]string{
			"distribution", "delete",
			"--key", "zh Hans",
			"--admin-url", server.URL,
		},
		strings.NewReader(""),
		&deleteOutput,
		io.Discard,
	)
	if err != nil {
		t.Fatalf("删除官方数据失败: %v", err)
	}

	if updateRequest := <-requests; updateRequest != "PUT /v1/admin/distribution/zh%20Hans" {
		t.Fatalf("更新请求路径不正确: %s", updateRequest)
	}
	if deleteRequest := <-requests; deleteRequest != "DELETE /v1/admin/distribution/zh%20Hans" {
		t.Fatalf("删除请求路径不正确: %s", deleteRequest)
	}
	if !strings.Contains(deleteOutput.String(), `"success": true`) {
		t.Fatalf("删除输出不正确: %s", deleteOutput.String())
	}
}

func readMultipartFields(t *testing.T, reader *multipart.Reader) map[string]string {
	t.Helper()
	fields := make(map[string]string)
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			return fields
		}
		if err != nil {
			t.Fatalf("读取 multipart 字段失败: %v", err)
		}
		data, err := io.ReadAll(part)
		part.Close()
		if err != nil {
			t.Fatalf("读取 multipart 字段内容失败: %v", err)
		}
		fields[part.FormName()] = string(data)
	}
}
