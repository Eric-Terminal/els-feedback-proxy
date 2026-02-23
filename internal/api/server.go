package api

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"els-feedback-proxy/internal/config"
	"els-feedback-proxy/internal/github"
	"els-feedback-proxy/internal/security"
	"els-feedback-proxy/internal/store"
)

// Server HTTP 服务封装
type Server struct {
	cfg        config.Config
	gh         *github.Client
	limiter    *security.FixedWindowLimiter
	dedupe     *security.DuplicateDetector
	challenges *security.ChallengeManager
	tickets    *store.TicketStore
	engine     *gin.Engine
}

func NewServer(
	cfg config.Config,
	gh *github.Client,
	limiter *security.FixedWindowLimiter,
	dedupe *security.DuplicateDetector,
	challenges *security.ChallengeManager,
	tickets *store.TicketStore,
) *Server {
	gin.SetMode(gin.ReleaseMode)

	server := &Server{
		cfg:        cfg,
		gh:         gh,
		limiter:    limiter,
		dedupe:     dedupe,
		challenges: challenges,
		tickets:    tickets,
		engine:     gin.New(),
	}

	server.engine.Use(gin.Recovery())
	server.registerRoutes()

	return server
}

func (s *Server) Run() error {
	return s.engine.Run(":" + s.cfg.Port)
}

func (s *Server) registerRoutes() {
	s.engine.GET("/v1/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true, "time": time.Now().UTC().Format(time.RFC3339)})
	})

	s.engine.POST("/v1/feedback/challenge", s.handleChallenge)
	s.engine.POST("/v1/feedback/issues", s.handleCreateIssue)
	s.engine.GET("/v1/feedback/issues/:issueNumber", s.handleGetIssueStatus)
}

func (s *Server) handleChallenge(c *gin.Context) {
	if !s.validateUA(c) {
		writeError(c, http.StatusForbidden, "无效客户端 UA")
		return
	}

	clientIP := c.ClientIP()
	if !s.allowRate("challenge", clientIP, s.cfg.ChallengeLimitPerWindow) {
		writeError(c, http.StatusTooManyRequests, "请求过于频繁")
		return
	}

	bundle := s.challenges.Issue(clientIP)
	c.JSON(http.StatusOK, gin.H{
		"success":       true,
		"challenge_id":  bundle.ChallengeID,
		"client_secret": bundle.ClientSecret,
		"nonce":         bundle.Nonce,
		"expires_at":    bundle.ExpiresAt.UTC().Format(time.RFC3339),
	})
}

func (s *Server) handleCreateIssue(c *gin.Context) {
	if !s.validateUA(c) {
		writeError(c, http.StatusForbidden, "无效客户端 UA")
		return
	}

	clientIP := c.ClientIP()
	if !s.allowRate("submit", clientIP, s.cfg.SubmitLimitPerWindow) {
		writeError(c, http.StatusTooManyRequests, "提交过于频繁")
		return
	}

	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		writeError(c, http.StatusBadRequest, "读取请求体失败")
		return
	}

	challengeID := strings.TrimSpace(c.GetHeader("X-ELS-Challenge-Id"))
	timestamp := strings.TrimSpace(c.GetHeader("X-ELS-Timestamp"))
	signature := strings.TrimSpace(c.GetHeader("X-ELS-Signature"))
	if challengeID == "" || timestamp == "" || signature == "" {
		writeError(c, http.StatusUnauthorized, "缺少签名头")
		return
	}

	verifyErr := s.challenges.VerifySubmission(
		clientIP,
		challengeID,
		timestamp,
		signature,
		http.MethodPost,
		s.cfg.IssuesPath,
		body,
	)
	if verifyErr != nil {
		if errors.Is(verifyErr, security.ErrClientBlocked) {
			writeError(c, http.StatusTooManyRequests, "签名校验失败次数过多，已临时封禁")
			return
		}
		writeError(c, http.StatusUnauthorized, fmt.Sprintf("签名校验失败: %s", verifyErr.Error()))
		return
	}

	var req SubmitIssueRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(c, http.StatusBadRequest, "请求体格式无效")
		return
	}

	req.Normalize()
	if err := req.Validate(); err != nil {
		if typed, ok := err.(apiError); ok {
			writeError(c, typed.Code, typed.Message)
			return
		}
		writeError(c, http.StatusBadRequest, err.Error())
		return
	}

	dedupeKey := s.dedupeKey(clientIP, req)
	if s.dedupe.SeenRecently(dedupeKey, s.cfg.DuplicateWindow) {
		writeError(c, http.StatusConflict, "检测到短时间重复提交")
		return
	}

	labels := []string{"source/app-feedback", "status/triage", platformLabel(req.Environment.Platform)}
	if req.Type == "bug" {
		labels = append(labels, "type/bug")
	} else {
		labels = append(labels, "type/feature")
	}

	ipHash := hashString(clientIP)
	issueTitle := renderIssueTitle(req)
	issueBody := renderIssueBody(req, ipHash)

	issue, err := s.gh.CreateIssue(c.Request.Context(), github.CreateIssueInput{
		Title:  issueTitle,
		Body:   issueBody,
		Labels: labels,
	})
	if err != nil {
		writeError(c, http.StatusBadGateway, fmt.Sprintf("GitHub 创建失败: %v", err))
		return
	}

	ticketToken := randomToken(24)
	if err := s.tickets.Set(issue.Number, ticketToken); err != nil {
		writeError(c, http.StatusInternalServerError, fmt.Sprintf("保存 ticket_token 失败: %v", err))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success":      true,
		"issue_number": issue.Number,
		"ticket_token": ticketToken,
		"public_url":   issue.URL,
		"status":       "triage",
	})
}

func (s *Server) handleGetIssueStatus(c *gin.Context) {
	if !s.validateUA(c) {
		writeError(c, http.StatusForbidden, "无效客户端 UA")
		return
	}

	clientIP := c.ClientIP()
	if !s.allowRate("query", clientIP, s.cfg.QueryLimitPerWindow) {
		writeError(c, http.StatusTooManyRequests, "查询过于频繁")
		return
	}

	issueNumber, err := strconv.Atoi(strings.TrimSpace(c.Param("issueNumber")))
	if err != nil || issueNumber <= 0 {
		writeError(c, http.StatusBadRequest, "issue_number 无效")
		return
	}

	ticketToken := strings.TrimSpace(c.Query("ticket_token"))
	if ticketToken == "" {
		writeError(c, http.StatusForbidden, "缺少 ticket_token")
		return
	}

	if !s.tickets.Validate(issueNumber, ticketToken) {
		writeError(c, http.StatusForbidden, "ticket_token 无效")
		return
	}

	issue, err := s.gh.GetIssueStatus(c.Request.Context(), issueNumber)
	if err != nil {
		writeError(c, http.StatusBadGateway, fmt.Sprintf("GitHub 查询失败: %v", err))
		return
	}

	comments := make([]gin.H, 0, len(issue.Comments))
	for _, comment := range issue.Comments {
		comments = append(comments, gin.H{
			"id":         fmt.Sprintf("%d", comment.ID),
			"author":     comment.Author,
			"body":       comment.Body,
			"created_at": comment.CreatedAt.UTC().Format(time.RFC3339),
		})
	}

	visibleLabels := filterVisibleLabels(issue.Labels)
	status := mapIssueStatus(issue.State, issue.Labels)

	c.JSON(http.StatusOK, gin.H{
		"success":      true,
		"issue_number": issue.Number,
		"status":       status,
		"title":        issue.Title,
		"updated_at":   issue.UpdatedAt.UTC().Format(time.RFC3339),
		"labels":       visibleLabels,
		"public_url":   issue.URL,
		"closed":       strings.EqualFold(issue.State, "closed"),
		"comments":     comments,
	})
}

func (s *Server) validateUA(c *gin.Context) bool {
	ua := strings.TrimSpace(c.GetHeader("User-Agent"))
	if ua == "" {
		return false
	}

	decoded := ua
	if unescaped, err := url.QueryUnescape(ua); err == nil {
		decoded = unescaped
	}

	return strings.Contains(strings.ToLower(decoded), strings.ToLower(s.cfg.RequiredUAKeyword))
}

func (s *Server) allowRate(action, clientIP string, limit int) bool {
	key := fmt.Sprintf("%s:%s", action, clientIP)
	return s.limiter.Allow(key, limit, s.cfg.RateWindow)
}

func (s *Server) dedupeKey(clientIP string, req SubmitIssueRequest) string {
	input := strings.Join([]string{clientIP, req.Type, req.Title, req.Detail}, "|")
	return hashString(input)
}

func platformLabel(platform string) string {
	normalized := strings.TrimSpace(strings.ToLower(platform))
	switch normalized {
	case "ios":
		return "platform/ios"
	case "watchos":
		return "platform/watchos"
	default:
		return "platform/unknown"
	}
}

func filterVisibleLabels(labels []string) []string {
	hiddenPrefixes := []string{"internal/", "security/", "meta/", "source/"}

	result := make([]string, 0, len(labels))
	for _, label := range labels {
		normalized := strings.ToLower(strings.TrimSpace(label))
		if normalized == "" {
			continue
		}

		hidden := false
		for _, prefix := range hiddenPrefixes {
			if strings.HasPrefix(normalized, prefix) {
				hidden = true
				break
			}
		}
		if hidden {
			continue
		}

		result = append(result, label)
	}
	return result
}

func mapIssueStatus(state string, labels []string) string {
	lowered := make([]string, 0, len(labels))
	for _, label := range labels {
		lowered = append(lowered, strings.ToLower(label))
	}

	if containsLabel(lowered, "status/triage") {
		return "triage"
	}
	if containsLabel(lowered, "status/in-progress") {
		return "in_progress"
	}
	if containsLabel(lowered, "status/blocked") {
		return "blocked"
	}
	if containsLabel(lowered, "status/resolved") {
		return "resolved"
	}

	if strings.EqualFold(state, "closed") {
		return "closed"
	}
	return "in_progress"
}

func containsLabel(labels []string, target string) bool {
	for _, label := range labels {
		if label == target {
			return true
		}
	}
	return false
}

func randomToken(length int) string {
	if length <= 0 {
		return ""
	}

	buf := make([]byte, length)
	if _, err := rand.Read(buf); err != nil {
		return hashString(fmt.Sprintf("%d", time.Now().UnixNano()))
	}
	return base64.RawURLEncoding.EncodeToString(buf)
}

func hashString(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

func writeError(c *gin.Context, code int, message string) {
	c.JSON(code, gin.H{
		"success": false,
		"error":   message,
	})
}
