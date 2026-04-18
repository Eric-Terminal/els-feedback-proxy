package api

import (
	"sync"
	"time"

	"els-feedback-proxy/internal/github"
)

type issueStatusCacheEntry struct {
	issue     github.IssueStatus
	expiresAt time.Time
}

type issueStatusCache struct {
	mu      sync.Mutex
	ttl     time.Duration
	now     func() time.Time
	entries map[int]issueStatusCacheEntry
}

func newIssueStatusCache(ttl time.Duration) *issueStatusCache {
	if ttl <= 0 {
		ttl = time.Hour
	}
	return &issueStatusCache{
		ttl:     ttl,
		now:     time.Now,
		entries: make(map[int]issueStatusCacheEntry),
	}
}

func (c *issueStatusCache) Get(issueNumber int) (github.IssueStatus, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	entry, ok := c.entries[issueNumber]
	if !ok {
		return github.IssueStatus{}, false
	}
	if !entry.expiresAt.After(c.now()) {
		delete(c.entries, issueNumber)
		return github.IssueStatus{}, false
	}
	return entry.issue, true
}

func (c *issueStatusCache) Set(issue github.IssueStatus) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.entries[issue.Number] = issueStatusCacheEntry{
		issue:     issue,
		expiresAt: c.now().Add(c.ttl),
	}
}

func (c *issueStatusCache) Delete(issueNumber int) {
	c.mu.Lock()
	defer c.mu.Unlock()

	delete(c.entries, issueNumber)
}
