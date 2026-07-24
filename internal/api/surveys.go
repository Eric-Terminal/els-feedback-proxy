package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"els-feedback-proxy/internal/security"
	"els-feedback-proxy/internal/store"
)

const maxSurveyRequestBody = 128 << 10

func (s *Server) registerSurveyRoutes() {
	if s.surveys == nil {
		return
	}

	s.engine.GET("/v1/surveys", s.handleListSurveys)
	s.engine.POST("/v1/surveys/challenge", s.handleChallenge)
	s.engine.POST("/v1/surveys/:key/responses", s.handleSubmitSurveyResponse)
}

func (s *Server) registerSurveyAdminRoutes() {
	adminAPI := s.adminEngine.Group("/v1/admin/surveys")
	adminAPI.Use(s.requireAdmin)
	adminAPI.GET("", s.handleAdminListSurveys)
	adminAPI.POST("", s.handleAdminCreateSurvey)
	adminAPI.PUT("/:key", s.handleAdminUpdateSurvey)
	adminAPI.DELETE("/:key", s.handleAdminDeleteSurvey)
	adminAPI.GET("/:key/results", s.handleAdminSurveyResults)
}

func (s *Server) handleListSurveys(c *gin.Context) {
	payload, err := json.Marshal(s.surveys.PublicList())
	if err != nil {
		writeError(c, http.StatusInternalServerError, "编码意见征集失败")
		return
	}

	etag := payloadETag(payload)
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

func (s *Server) handleSubmitSurveyResponse(c *gin.Context) {
	if !s.validateUA(c) {
		writeError(c, http.StatusForbidden, "无效客户端 UA")
		return
	}

	clientIP := c.ClientIP()
	if !s.allowRate("survey-submit", clientIP, s.cfg.SubmitLimitPerWindow) {
		writeError(c, http.StatusTooManyRequests, "提交过于频繁")
		return
	}

	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxSurveyRequestBody)
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		writeError(c, http.StatusBadRequest, "读取请求体失败")
		return
	}

	path := "/v1/surveys/" + c.Param("key") + "/responses"
	if err := s.verifySignedSubmission(c, clientIP, path, body); err != nil {
		if errors.Is(err, security.ErrClientBlocked) {
			writeError(c, http.StatusTooManyRequests, "签名校验失败次数过多，已临时封禁")
			return
		}
		writeError(c, http.StatusUnauthorized, fmt.Sprintf("签名校验失败: %s", err.Error()))
		return
	}

	var input store.SurveyResponseInput
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeError(c, http.StatusBadRequest, "请求体格式无效")
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		writeError(c, http.StatusBadRequest, "请求体只能包含一个 JSON 对象")
		return
	}

	if s.dedupe != nil {
		dedupeKey := hashString(clientIP + "|" + c.Param("key") + "|" + string(body))
		if s.dedupe.SeenRecently(dedupeKey, s.cfg.DuplicateWindow) {
			writeError(c, http.StatusConflict, "检测到短时间重复提交")
			return
		}
	}

	if _, err := s.surveys.Submit(c.Param("key"), input); err != nil {
		writeSurveyStoreError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusCreated, gin.H{"success": true})
}

func (s *Server) verifySignedSubmission(c *gin.Context, clientIP, path string, body []byte) error {
	challengeID := strings.TrimSpace(c.GetHeader("X-ELS-Challenge-Id"))
	timestamp := strings.TrimSpace(c.GetHeader("X-ELS-Timestamp"))
	signature := strings.TrimSpace(c.GetHeader("X-ELS-Signature"))
	powNonce := strings.TrimSpace(c.GetHeader("X-ELS-PoW-Nonce"))
	powHash := strings.TrimSpace(c.GetHeader("X-ELS-PoW-Hash"))
	if challengeID == "" || timestamp == "" || signature == "" {
		return fmt.Errorf("缺少签名请求头")
	}
	return s.challenges.VerifySubmission(
		clientIP,
		challengeID,
		timestamp,
		signature,
		powNonce,
		powHash,
		http.MethodPost,
		path,
		body,
	)
}

func (s *Server) handleAdminListSurveys(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"records": s.surveys.List(),
	})
}

func (s *Server) handleAdminCreateSurvey(c *gin.Context) {
	var record store.SurveyRecord
	if err := decodeSurveyJSON(c, &record); err != nil {
		writeError(c, http.StatusBadRequest, err.Error())
		return
	}
	created, err := s.surveys.Create(record)
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

func (s *Server) handleAdminUpdateSurvey(c *gin.Context) {
	var replacement store.SurveyRecord
	if err := decodeSurveyJSON(c, &replacement); err != nil {
		writeError(c, http.StatusBadRequest, err.Error())
		return
	}
	updated, err := s.surveys.Update(c.Param("key"), replacement)
	if err != nil {
		writeSurveyStoreError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"record":  updated,
	})
}

func (s *Server) handleAdminDeleteSurvey(c *gin.Context) {
	if err := s.surveys.Delete(c.Param("key")); err != nil {
		writeSurveyStoreError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.Status(http.StatusNoContent)
}

func (s *Server) handleAdminSurveyResults(c *gin.Context) {
	survey, responses, err := s.surveys.Results(c.Param("key"))
	if err != nil {
		writeSurveyStoreError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{
		"success":        true,
		"survey":         survey,
		"response_count": len(responses),
		"responses":      responses,
	})
}

func decodeSurveyJSON(c *gin.Context, target any) error {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxSurveyRequestBody)
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

func writeSurveyStoreError(c *gin.Context, err error) {
	message := err.Error()
	switch {
	case strings.Contains(message, "不存在"):
		writeError(c, http.StatusNotFound, message)
	case strings.Contains(message, "已停止"):
		writeError(c, http.StatusGone, message)
	case strings.Contains(message, "不能修改"), strings.Contains(message, "不能删除"):
		writeError(c, http.StatusConflict, message)
	default:
		writeError(c, http.StatusBadRequest, message)
	}
}
