package api

import (
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"els-feedback-proxy/internal/guide"
)

func (s *Server) registerGuideRoutes() {
	if s.guideProxy != nil {
		s.engine.POST("/v1/guide/token", s.handleGuideToken)
		s.engine.POST("/v1/chat/completions", s.handleGuideChatCompletions)
	}
	if s.guideSourceTrees != nil {
		s.engine.GET("/v1/guide/source-trees/:commitSHA", s.handleGuideSourceTree)
	}
}

func (s *Server) handleGuideToken(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	c.Header("X-Content-Type-Options", "nosniff")
	c.JSON(http.StatusOK, s.guideProxy.IssueToken(c.ClientIP(), time.Now()))
}

func (s *Server) handleGuideChatCompletions(c *gin.Context) {
	startedAt := time.Now()
	requestID := randomToken(12)
	sessionID := strings.TrimSpace(c.GetHeader("X-Guide-Session-ID"))
	if !validGuideSessionID(sessionID) {
		writeGuideError(c, http.StatusBadRequest, "invalid_session", "缺少有效的向导会话 ID")
		logGuideRequest(requestID, http.StatusBadRequest, startedAt, "invalid_session")
		return
	}
	bearer := strings.TrimSpace(c.GetHeader("Authorization"))
	if !strings.HasPrefix(strings.ToLower(bearer), "bearer ") {
		writeGuideError(c, http.StatusUnauthorized, "invalid_token", "缺少向导临时令牌")
		logGuideRequest(requestID, http.StatusUnauthorized, startedAt, "invalid_token")
		return
	}
	c.Header("X-Request-ID", requestID)
	err := s.guideProxy.Forward(
		c.Request.Context(),
		c.Writer,
		c.Request.Body,
		strings.TrimSpace(bearer[len("Bearer "):]),
		c.ClientIP(),
		sessionID,
		requestID,
		time.Now(),
	)
	if err == nil {
		logGuideRequest(requestID, http.StatusOK, startedAt, "")
		return
	}
	var httpError *guide.HTTPError
	if errors.As(err, &httpError) {
		if !c.Writer.Written() {
			writeGuideError(c, httpError.Status, httpError.Kind, httpError.Message)
		}
		logGuideRequest(requestID, httpError.Status, startedAt, httpError.Kind)
		return
	}
	if !c.Writer.Written() && !errors.Is(err, c.Request.Context().Err()) {
		writeGuideError(c, http.StatusBadGateway, "stream_failed", "内置向导响应中断")
	}
	logGuideRequest(requestID, http.StatusBadGateway, startedAt, "stream_failed")
}

func (s *Server) handleGuideSourceTree(c *gin.Context) {
	commitSHA := strings.ToLower(strings.TrimSpace(c.Param("commitSHA")))
	response, err := s.guideSourceTrees.Load(c.Request.Context(), commitSHA)
	if err != nil {
		if errors.Is(err, guide.ErrInvalidCommitSHA) {
			writeError(c, http.StatusBadRequest, err.Error())
			return
		}
		writeError(c, http.StatusBadGateway, "暂时无法获取这个版本的源码目录")
		return
	}
	etag := `"guide-source-tree-` + response.CommitSHA + `"`
	c.Header("Cache-Control", "public, max-age=31536000, immutable")
	c.Header("Cloudflare-CDN-Cache-Control", "public, max-age=31536000, immutable")
	c.Header("ETag", etag)
	c.Header("Vary", "Accept-Encoding")
	c.Header("X-Content-Type-Options", "nosniff")
	if etagMatches(c.GetHeader("If-None-Match"), etag) {
		c.Status(http.StatusNotModified)
		return
	}
	c.JSON(http.StatusOK, response)
}

func validGuideSessionID(value string) bool {
	if len(value) < 8 || len(value) > 128 {
		return false
	}
	for _, character := range value {
		if !((character >= 'a' && character <= 'z') ||
			(character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') ||
			character == '-' || character == '_') {
			return false
		}
	}
	return true
}

func writeGuideError(c *gin.Context, status int, kind, message string) {
	c.Header("Cache-Control", "no-store")
	c.JSON(status, gin.H{
		"error": gin.H{
			"message": message,
			"type":    kind,
		},
	})
}

func logGuideRequest(requestID string, status int, startedAt time.Time, errorKind string) {
	log.Printf(
		"guide request_id=%s route=built_in status=%d duration_ms=%d error=%s",
		requestID,
		status,
		time.Since(startedAt).Milliseconds(),
		errorKind,
	)
}
