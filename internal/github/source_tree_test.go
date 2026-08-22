package github

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGetSourceTreeExpandsTruncatedTree(t *testing.T) {
	sha := "0123456789abcdef0123456789abcdef01234567"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/repos/owner/repo/git/commits/"+sha:
			_, _ = fmt.Fprint(w, `{"tree":{"sha":"root-tree"}}`)
		case r.URL.Query().Get("recursive") == "1":
			_, _ = fmt.Fprint(w, `{"truncated":true,"tree":[{"path":"partial.swift","type":"blob","sha":"partial","size":1}]}`)
		case r.URL.Path == "/repos/owner/repo/git/trees/root-tree":
			_, _ = fmt.Fprint(w, `{"truncated":false,"tree":[{"path":"Sources","type":"tree","sha":"tree-sha"},{"path":"README.md","type":"blob","sha":"readme","size":12}]}`)
		case r.URL.Path == "/repos/owner/repo/git/trees/tree-sha":
			_, _ = fmt.Fprint(w, `{"truncated":false,"tree":[{"path":"Guide.swift","type":"blob","sha":"guide","size":128}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := NewClient("token", "owner", "repo")
	client.apiBaseURL = server.URL
	client.httpClient = server.Client()
	tree, err := client.GetSourceTree(context.Background(), sha)
	if err != nil {
		t.Fatalf("补齐截断源码树失败: %v", err)
	}
	if tree.Truncated || len(tree.Entries) != 3 {
		t.Fatalf("源码树未补齐: %#v", tree)
	}
	if tree.Entries[2].Path != "Sources/Guide.swift" {
		t.Fatalf("嵌套路径拼接错误: %#v", tree.Entries)
	}
}
