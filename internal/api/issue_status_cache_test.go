package api

import (
	"testing"
	"time"

	"els-feedback-proxy/internal/github"
)

func TestIssueStatusCacheUsesTTLAndDelete(t *testing.T) {
	now := time.Date(2026, 4, 19, 12, 0, 0, 0, time.UTC)
	cache := newIssueStatusCache(time.Hour)
	cache.now = func() time.Time { return now }

	cache.Set(github.IssueStatus{
		Number: 42,
		Title:  "缓存测试",
	})

	if issue, ok := cache.Get(42); !ok || issue.Title != "缓存测试" {
		t.Fatalf("期望命中缓存")
	}

	now = now.Add(30 * time.Minute)
	if issue, ok := cache.Get(42); !ok || issue.Title != "缓存测试" {
		t.Fatalf("期望 TTL 内仍可命中缓存")
	}

	cache.Delete(42)
	if _, ok := cache.Get(42); ok {
		t.Fatalf("删除后不应命中缓存")
	}

	cache.Set(github.IssueStatus{
		Number: 42,
		Title:  "缓存测试",
	})
	now = now.Add(2 * time.Hour)
	if _, ok := cache.Get(42); ok {
		t.Fatalf("超过 TTL 后不应命中缓存")
	}
}
