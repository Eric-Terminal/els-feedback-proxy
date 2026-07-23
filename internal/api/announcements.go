package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"els-feedback-proxy/internal/store"
)

const maxAnnouncementRequestBody = 64 << 10

func (s *Server) registerAnnouncementRoutes() {
	if s.announcements == nil {
		return
	}

	s.engine.GET("/v1/announcements", s.handleListAnnouncements)
}

func (s *Server) registerAnnouncementAdminRoutes() {
	s.registerAnnouncementAdminUIRoutes()
	adminAPI := s.adminEngine.Group("/v1/admin/announcements")
	adminAPI.Use(s.requireAnnouncementAdmin)
	adminAPI.GET("", s.handleAdminListAnnouncements)
	adminAPI.POST("", s.handleAdminCreateAnnouncement)
	adminAPI.PUT("/:key", s.handleAdminUpdateAnnouncement)
	adminAPI.DELETE("/:key", s.handleAdminDeleteAnnouncement)
}

func (s *Server) handleListAnnouncements(c *gin.Context) {
	payload, err := json.Marshal(s.announcements.PublicList())
	if err != nil {
		writeError(c, http.StatusInternalServerError, "编码公告失败")
		return
	}

	digest := sha256.Sum256(payload)
	etag := `"` + hex.EncodeToString(digest[:]) + `"`
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

func (s *Server) handleAdminListAnnouncements(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"records": s.announcements.List(),
	})
}

func (s *Server) handleAdminCreateAnnouncement(c *gin.Context) {
	var record store.AnnouncementRecord
	if err := decodeAnnouncementJSON(c, &record); err != nil {
		writeError(c, http.StatusBadRequest, err.Error())
		return
	}

	created, err := s.announcements.Create(record)
	if err != nil {
		writeError(c, http.StatusBadRequest, err.Error())
		return
	}

	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusCreated, gin.H{
		"success": true,
		"record":  created,
	})
}

func (s *Server) handleAdminUpdateAnnouncement(c *gin.Context) {
	var replacement store.AnnouncementRecord
	if err := decodeAnnouncementJSON(c, &replacement); err != nil {
		writeError(c, http.StatusBadRequest, err.Error())
		return
	}

	updated, err := s.announcements.Update(c.Param("key"), replacement)
	if err != nil {
		writeAnnouncementStoreError(c, err)
		return
	}

	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"record":  updated,
	})
}

func (s *Server) handleAdminDeleteAnnouncement(c *gin.Context) {
	if err := s.announcements.Delete(c.Param("key")); err != nil {
		writeAnnouncementStoreError(c, err)
		return
	}

	c.Header("Cache-Control", "no-store")
	c.Status(http.StatusNoContent)
}

func decodeAnnouncementJSON(c *gin.Context, target any) error {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxAnnouncementRequestBody)
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("请求体格式无效: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return fmt.Errorf("请求体只能包含一个 JSON 对象")
	}
	return nil
}

func writeAnnouncementStoreError(c *gin.Context, err error) {
	if strings.Contains(err.Error(), "不存在") {
		writeError(c, http.StatusNotFound, err.Error())
		return
	}
	writeError(c, http.StatusBadRequest, err.Error())
}

func etagMatches(headerValue, etag string) bool {
	for _, candidate := range strings.Split(headerValue, ",") {
		if strings.TrimSpace(candidate) == etag {
			return true
		}
	}
	return false
}
