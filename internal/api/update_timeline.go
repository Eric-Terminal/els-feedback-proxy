package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"els-feedback-proxy/internal/github"
)

const (
	updateTimelineBranch          = "dev"
	updateTimelineMaxCount        = 1_000
	updateTimelineOriginCacheTTL  = 30 * time.Minute
	updateTimelineFailureRetryTTL = time.Minute
)

type updateTimelineGateway interface {
	GetUpdateTimeline(ctx context.Context, branch string, maxCount int) (github.UpdateTimeline, error)
}

type updateTimelineCache struct {
	mu        sync.RWMutex
	refreshMu sync.Mutex
	payload   []byte
	etag      string
	expiresAt time.Time
}

func (s *Server) registerUpdateTimelineRoutes() {
	if s.updateTimeline == nil {
		return
	}
	s.engine.GET("/v1/updates/timeline", s.handleUpdateTimeline)
}

func (s *Server) handleUpdateTimeline(c *gin.Context) {
	payload, etag, err := s.loadUpdateTimeline(c.Request.Context())
	if err != nil {
		writeError(c, http.StatusBadGateway, fmt.Sprintf("获取检查更新时间线失败: %v", err))
		return
	}

	cacheMaxAge := s.cfg.AnnouncementCacheMaxAge
	if cacheMaxAge < 30 {
		cacheMaxAge = 300
	}
	c.Header("Cache-Control", "public, max-age=60, stale-if-error=86400")
	c.Header(
		"Cloudflare-CDN-Cache-Control",
		fmt.Sprintf("public, max-age=%d, stale-while-revalidate=60, stale-if-error=86400", cacheMaxAge),
	)
	c.Header("ETag", etag)
	c.Header("Vary", "Accept-Encoding")
	c.Header("X-Content-Type-Options", "nosniff")
	if etagMatches(c.GetHeader("If-None-Match"), etag) {
		c.Status(http.StatusNotModified)
		return
	}
	c.Data(http.StatusOK, "application/json; charset=utf-8", payload)
}

func (s *Server) loadUpdateTimeline(ctx context.Context) ([]byte, string, error) {
	now := time.Now()
	if payload, etag, ok := s.updateTimelineCache.fresh(now); ok {
		return payload, etag, nil
	}

	s.updateTimelineCache.refreshMu.Lock()
	defer s.updateTimelineCache.refreshMu.Unlock()

	now = time.Now()
	if payload, etag, ok := s.updateTimelineCache.fresh(now); ok {
		return payload, etag, nil
	}

	timeline, err := s.updateTimeline.GetUpdateTimeline(
		ctx,
		updateTimelineBranch,
		updateTimelineMaxCount,
	)
	if err != nil {
		if payload, etag, ok := s.updateTimelineCache.stale(); ok {
			s.updateTimelineCache.extend(now.Add(updateTimelineFailureRetryTTL))
			return payload, etag, nil
		}
		return nil, "", err
	}
	if len(timeline.Commits) == 0 {
		if payload, etag, ok := s.updateTimelineCache.stale(); ok {
			s.updateTimelineCache.extend(now.Add(updateTimelineFailureRetryTTL))
			return payload, etag, nil
		}
		return nil, "", fmt.Errorf("GitHub 未返回任何提交")
	}

	payload, err := json.Marshal(timeline)
	if err != nil {
		return nil, "", fmt.Errorf("编码检查更新时间线失败: %w", err)
	}
	etag := payloadETag(payload)
	cacheTTL := time.Duration(s.cfg.AnnouncementCacheMaxAge) * time.Second
	if cacheTTL < updateTimelineOriginCacheTTL {
		cacheTTL = updateTimelineOriginCacheTTL
	}
	s.updateTimelineCache.store(payload, etag, now.Add(cacheTTL))
	return payload, etag, nil
}

func (c *updateTimelineCache) fresh(now time.Time) ([]byte, string, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if len(c.payload) == 0 || !now.Before(c.expiresAt) {
		return nil, "", false
	}
	return append([]byte(nil), c.payload...), c.etag, true
}

func (c *updateTimelineCache) stale() ([]byte, string, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if len(c.payload) == 0 {
		return nil, "", false
	}
	return append([]byte(nil), c.payload...), c.etag, true
}

func (c *updateTimelineCache) store(payload []byte, etag string, expiresAt time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.payload = append([]byte(nil), payload...)
	c.etag = etag
	c.expiresAt = expiresAt
}

func (c *updateTimelineCache) extend(expiresAt time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.payload) > 0 {
		c.expiresAt = expiresAt
	}
}
