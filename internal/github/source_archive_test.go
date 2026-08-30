package github

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDownloadSourceArchiveUsesExactCommitAndCredentials(t *testing.T) {
	sha := "0123456789abcdef0123456789abcdef01234567"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/owner/repo/tarball/"+sha {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer token" {
			http.Error(w, "missing credentials", http.StatusUnauthorized)
			return
		}
		_, _ = io.WriteString(w, "archive")
	}))
	defer server.Close()

	client := NewClient("token", "owner", "repo")
	client.apiBaseURL = server.URL
	client.httpClient = server.Client()
	body, err := client.DownloadSourceArchive(context.Background(), sha)
	if err != nil {
		t.Fatalf("下载源码归档失败: %v", err)
	}
	defer body.Close()
	data, err := io.ReadAll(body)
	if err != nil || string(data) != "archive" {
		t.Fatalf("源码归档内容错误: data=%q err=%v", data, err)
	}
}
