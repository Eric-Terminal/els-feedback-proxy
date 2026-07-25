package admincli

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTelemetryStatusAndManifestUseAdminAPI(t *testing.T) {
	const token = "telemetry-cli-admin-token"
	var requestedPaths []string
	server := httptest.NewServer(http.HandlerFunc(func(
		response http.ResponseWriter,
		request *http.Request,
	) {
		if request.Header.Get("Authorization") != "Bearer "+token {
			t.Fatalf("遥测 CLI 未携带管理口令")
		}
		requestedPaths = append(requestedPaths, request.URL.Path)
		response.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(response, `{"schema_version":1,"entries":[]}`)
	}))
	defer server.Close()
	t.Setenv("ANNOUNCEMENT_ADMIN_TOKEN", token)

	for _, command := range []string{"status", "manifest"} {
		var output bytes.Buffer
		handled, err := Run(
			[]string{"telemetry", command, "--admin-url", server.URL},
			strings.NewReader(""),
			&output,
			io.Discard,
		)
		if !handled || err != nil {
			t.Fatalf("telemetry %s 执行失败: handled=%v err=%v", command, handled, err)
		}
		if !strings.Contains(output.String(), `"schema_version": 1`) {
			t.Fatalf("telemetry %s 应输出格式化 JSON: %s", command, output.String())
		}
	}
	if len(requestedPaths) != 2 ||
		requestedPaths[0] != "/v1/admin/telemetry/status" ||
		requestedPaths[1] != "/v1/admin/telemetry/manifest" {
		t.Fatalf("遥测 CLI 请求路径错误: %+v", requestedPaths)
	}
}

func TestTelemetryExportPreservesRawJSON(t *testing.T) {
	const (
		token     = "telemetry-cli-admin-token"
		payloadID = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		raw       = `{"schema_version":1,"payload":{"value":1}}` + "\n"
	)
	server := httptest.NewServer(http.HandlerFunc(func(
		response http.ResponseWriter,
		request *http.Request,
	) {
		if request.URL.Path != "/v1/admin/telemetry/files/"+payloadID {
			t.Fatalf("导出路径错误: %s", request.URL.Path)
		}
		if request.Header.Get("Authorization") != "Bearer "+token {
			t.Fatalf("导出请求未携带管理口令")
		}
		_, _ = io.WriteString(response, raw)
	}))
	defer server.Close()
	t.Setenv("ANNOUNCEMENT_ADMIN_TOKEN", token)

	var output bytes.Buffer
	handled, err := Run(
		[]string{
			"telemetry", "export",
			"--payload-id", payloadID,
			"--admin-url", server.URL,
		},
		strings.NewReader(""),
		&output,
		io.Discard,
	)
	if !handled || err != nil {
		t.Fatalf("telemetry export 执行失败: handled=%v err=%v", handled, err)
	}
	if output.String() != raw {
		t.Fatalf("导出必须逐字节保留原始 JSON，实际 %q", output.String())
	}
}

func TestTelemetryConfirmForwardsVerifiedIDList(t *testing.T) {
	const token = "telemetry-cli-admin-token"
	confirmJSON := `{"payload_ids":["bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"]}`
	server := httptest.NewServer(http.HandlerFunc(func(
		response http.ResponseWriter,
		request *http.Request,
	) {
		if request.Method != http.MethodPost ||
			request.URL.Path != "/v1/admin/telemetry/confirm" {
			t.Fatalf("确认请求错误: %s %s", request.Method, request.URL.Path)
		}
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatalf("读取确认请求失败: %v", err)
		}
		if string(body) != confirmJSON {
			t.Fatalf("确认 JSON 被意外修改: %s", body)
		}
		_, _ = io.WriteString(response, `{"confirmed_payload_ids":[],"missing_payload_ids":[]}`)
	}))
	defer server.Close()
	t.Setenv("ANNOUNCEMENT_ADMIN_TOKEN", token)

	var output bytes.Buffer
	handled, err := Run(
		[]string{"telemetry", "confirm", "--file", "-", "--admin-url", server.URL},
		strings.NewReader(confirmJSON),
		&output,
		io.Discard,
	)
	if !handled || err != nil {
		t.Fatalf("telemetry confirm 执行失败: handled=%v err=%v", handled, err)
	}
}
