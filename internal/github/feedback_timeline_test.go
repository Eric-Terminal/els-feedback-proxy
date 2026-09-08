package github

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestFeedbackTimelinePreservesCommitAndLongMarkdownWithoutIssueUpdate(t *testing.T) {
	const sha = "5ce123649c0298c1c0c5b3c1d5f0bfe0b66bdb60"
	const headline = "fix(MCP工具): 修复工具失败状态与空搜索调用 #133"
	body := "# 回复\n\n" + strings.Repeat("**完整内容**\n", 2000) + "```swift\nprint(1)\n```\n正文结束"
	message := headline + "\n\n" + body
	var timelinePages atomic.Int32
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Error("反馈 GitHub 请求应带服务端凭据")
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/repos/owner/repo/issues/133":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"number": 133, "title": "反馈", "body": body, "state": "open",
				"updated_at": "2026-08-20T18:12:42Z", "comments_url": server.URL + "/comments",
			})
		case "/comments":
			_ = json.NewEncoder(w).Encode([]any{map[string]any{
				"id": 1, "body": body, "created_at": "2026-08-20T18:12:42Z", "user": map[string]any{"login": "Eric-Terminal"},
			}})
		case "/repos/owner/repo/issues/133/timeline":
			timelinePages.Add(1)
			if r.URL.Query().Get("per_page") != "100" {
				t.Error("反馈时间线应按 100 条分页")
			}
			if r.URL.Query().Get("page") == "1" {
				items := make([]map[string]any, 100)
				for i := range items {
					items[i] = map[string]any{"id": i + 1, "event": "labeled"}
				}
				_ = json.NewEncoder(w).Encode(items)
				return
			}
			_ = json.NewEncoder(w).Encode([]any{map[string]any{
				"id": 29851959939, "event": "referenced", "commit_id": sha,
				"created_at": "2026-08-22T16:18:35Z", "actor": map[string]any{"login": "Eric-Terminal"},
			}})
		case "/repos/owner/repo/commits/" + sha:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"sha": sha, "html_url": "https://github.com/owner/repo/commit/" + sha,
				"commit": map[string]any{"message": message, "author": map[string]any{"date": "2026-08-22T16:00:00Z"}, "verification": map[string]any{"verified": true}},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := NewClient("test-token", "owner", "repo")
	client.apiBaseURL = server.URL
	client.httpClient = server.Client()
	issue, err := client.GetIssueStatus(context.Background(), 133)
	if err != nil {
		t.Fatal(err)
	}
	if timelinePages.Load() != 2 || len(issue.TimelineEvents) != 1 {
		t.Fatalf("应获取第二页的引用提交，实际页数=%d 事件数=%d", timelinePages.Load(), len(issue.TimelineEvents))
	}
	commit := issue.TimelineEvents[0].Commit
	if commit == nil || commit.SHA != sha || commit.MessageHeadline != headline || commit.Message != message || !commit.Verified {
		t.Fatal("关联提交的标题、完整正文、SHA 或签名状态丢失")
	}
	if issue.Body != body || len(issue.Comments) != 1 || issue.Comments[0].Body != body {
		t.Fatal("长 Markdown 正文和回复不能截断")
	}
	if !issue.UpdatedAt.Equal(time.Date(2026, 8, 22, 16, 18, 35, 0, time.UTC)) {
		t.Fatalf("引用提交应计入最后动态时间，实际=%s", issue.UpdatedAt)
	}
}

func TestFeedbackTimelineFailuresAreNotSilentlyConvertedToEmptyEvents(t *testing.T) {
	for _, failure := range []string{"timeline", "commit"} {
		t.Run(failure, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/repos/owner/repo/issues/133":
					_, _ = w.Write([]byte(`{"number":133,"state":"open","updated_at":"2026-08-20T18:12:42Z"}`))
				case "/repos/owner/repo/issues/133/timeline":
					if failure == "timeline" {
						http.Error(w, "时间线暂时不可用", http.StatusBadGateway)
						return
					}
					_, _ = w.Write([]byte(`[{"id":1,"event":"referenced","commit_id":"abcdef","created_at":"2026-08-22T16:18:35Z"}]`))
				default:
					http.Error(w, "提交暂时不可用", http.StatusBadGateway)
				}
			}))
			defer server.Close()
			client := NewClient("test-token", "owner", "repo")
			client.apiBaseURL = server.URL
			client.httpClient = server.Client()
			if _, err := client.GetIssueStatus(context.Background(), 133); err == nil {
				t.Fatal("上游读取失败必须返回错误，避免缓存不完整动态")
			}
		})
	}
}
