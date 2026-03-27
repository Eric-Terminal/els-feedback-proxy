package api

import (
	"context"
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
	"els-feedback-proxy/internal/moderation"
	"els-feedback-proxy/internal/security"
	"els-feedback-proxy/internal/store"
)

// Server HTTP 服务封装
type Server struct {
	cfg        config.Config
	gh         githubGateway
	limiter    rateLimiter
	dedupe     duplicateDetector
	challenges *security.ChallengeManager
	tickets    *store.TicketStore
	reviewer   moderation.Reviewer
	archives   *store.BlockedArchiveStore
	developers map[string]struct{}
	engine     *gin.Engine
}

type githubGateway interface {
	CreateIssue(ctx context.Context, input github.CreateIssueInput) (github.CreateIssueResult, error)
	CreateIssueComment(ctx context.Context, issueNumber int, body string) (github.CreateCommentResult, error)
	GetIssueStatus(ctx context.Context, issueNumber int) (github.IssueStatus, error)
}

type rateLimiter interface {
	Allow(key string, limit int, window time.Duration) bool
}

type duplicateDetector interface {
	SeenRecently(key string, window time.Duration) bool
}

func NewServer(
	cfg config.Config,
	gh githubGateway,
	limiter rateLimiter,
	dedupe duplicateDetector,
	challenges *security.ChallengeManager,
	tickets *store.TicketStore,
	reviewer moderation.Reviewer,
	archives *store.BlockedArchiveStore,
) *Server {
	gin.SetMode(gin.ReleaseMode)

	if reviewer == nil {
		reviewer = moderation.AllowAllReviewer{}
	}

	server := &Server{
		cfg:        cfg,
		gh:         gh,
		limiter:    limiter,
		dedupe:     dedupe,
		challenges: challenges,
		tickets:    tickets,
		reviewer:   reviewer,
		archives:   archives,
		developers: buildDeveloperLoginSet(cfg),
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
	s.engine.POST("/v1/feedback/issues/:issueNumber/comments", s.handleCreateIssueComment)
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

	bundle := s.challenges.Issue(clientIP, s.cfg.PoWDifficultyBits)
	c.JSON(http.StatusOK, gin.H{
		"success":       true,
		"challenge_id":  bundle.ChallengeID,
		"client_secret": bundle.ClientSecret,
		"nonce":         bundle.Nonce,
		"pow_bits":      bundle.PoWBits,
		"pow_salt":      bundle.PoWSalt,
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
	powNonce := strings.TrimSpace(c.GetHeader("X-ELS-PoW-Nonce"))
	powHash := strings.TrimSpace(c.GetHeader("X-ELS-PoW-Hash"))
	if challengeID == "" || timestamp == "" || signature == "" {
		writeError(c, http.StatusUnauthorized, "缺少签名请求头")
		return
	}

	verifyErr := s.challenges.VerifySubmission(
		clientIP,
		challengeID,
		timestamp,
		signature,
		powNonce,
		powHash,
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

	ipHash := hashString(clientIP)
	reviewDecision, reviewErr := s.reviewer.Review(c.Request.Context(), moderation.ReviewInput{
		Type:              req.Type,
		Title:             req.Title,
		Detail:            req.Detail,
		ReproductionSteps: req.ReproductionSteps,
		ExpectedBehavior:  req.ExpectedBehavior,
		ActualBehavior:    req.ActualBehavior,
		ExtraContext:      req.ExtraContext,
	})
	moderationBlocked := reviewErr != nil || !reviewDecision.Allow

	labels := []string{"source/app-feedback", platformLabel(req.Environment.Platform)}
	if req.Type == "bug" {
		labels = append(labels, "type/bug")
	} else {
		labels = append(labels, "type/feature")
	}

	issueTitle := renderIssueTitle(req)
	issueBody := renderIssueBody(req, ipHash)
	publicStatus := "triage"
	httpStatus := http.StatusOK
	var archiveID string
	var moderationMessage string

	if moderationBlocked {
		if s.archives == nil {
			writeError(c, http.StatusInternalServerError, "审核留档存储未初始化")
			return
		}
		archiveID = randomToken(12)
		moderationMessage = buildModerationMessage(reviewDecision, reviewErr)
		archiveMarkdown := renderBlockedArchiveMarkdown(
			archiveID,
			ipHash,
			req,
			reviewDecision,
			reviewErr,
			time.Now().UTC(),
		)
		archiveFile, err := s.archives.SaveMarkdown(archiveID, archiveMarkdown)
		if err != nil {
			writeError(c, http.StatusInternalServerError, fmt.Sprintf("保存审核留档失败: %v", err))
			return
		}
		labels = append(labels, "status/blocked", "moderation/blocked")
		issueTitle = renderBlockedIssueTitle(req)
		issueBody = renderBlockedIssueBody(archiveID, archiveFile, moderationMessage)
		publicStatus = "blocked"
		httpStatus = http.StatusAccepted
	} else {
		labels = append(labels, "status/triage")
	}

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

	response := gin.H{
		"success":      true,
		"issue_number": issue.Number,
		"ticket_token": ticketToken,
		"public_url":   issue.URL,
		"status":       publicStatus,
	}
	if moderationBlocked {
		response["moderation_blocked"] = true
		response["moderation_message"] = moderationMessage
		response["archive_id"] = archiveID
	}

	c.JSON(httpStatus, response)
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

	issueNumber, err := parseIssueNumber(c.Param("issueNumber"))
	if err != nil {
		writeError(c, http.StatusBadRequest, "issue_number 无效")
		return
	}

	if !s.validateTicketToken(issueNumber, strings.TrimSpace(c.Query("ticket_token"))) {
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
			"id":           fmt.Sprintf("%d", comment.ID),
			"author":       comment.Author,
			"body":         comment.Body,
			"created_at":   comment.CreatedAt.UTC().Format(time.RFC3339),
			"is_developer": s.isDeveloperLogin(comment.Author),
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

func (s *Server) handleCreateIssueComment(c *gin.Context) {
	if !s.validateUA(c) {
		writeError(c, http.StatusForbidden, "无效客户端 UA")
		return
	}

	clientIP := c.ClientIP()
	if !s.allowRate("comment", clientIP, s.cfg.CommentLimitPerWindow) {
		writeError(c, http.StatusTooManyRequests, "评论提交过于频繁")
		return
	}

	issueNumber, err := parseIssueNumber(c.Param("issueNumber"))
	if err != nil {
		writeError(c, http.StatusBadRequest, "issue_number 无效")
		return
	}
	ticketToken := strings.TrimSpace(c.Query("ticket_token"))
	if !s.validateTicketToken(issueNumber, ticketToken) {
		writeError(c, http.StatusForbidden, "ticket_token 无效")
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
	powNonce := strings.TrimSpace(c.GetHeader("X-ELS-PoW-Nonce"))
	powHash := strings.TrimSpace(c.GetHeader("X-ELS-PoW-Hash"))
	if challengeID == "" || timestamp == "" || signature == "" {
		writeError(c, http.StatusUnauthorized, "缺少签名请求头")
		return
	}

	commentPath := fmt.Sprintf("%s/%d/comments", s.cfg.IssuesPath, issueNumber)
	verifyErr := s.challenges.VerifySubmission(
		clientIP,
		challengeID,
		timestamp,
		signature,
		powNonce,
		powHash,
		http.MethodPost,
		commentPath,
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

	var req SubmitCommentRequest
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

	issueStatus, err := s.gh.GetIssueStatus(c.Request.Context(), issueNumber)
	if err != nil {
		writeError(c, http.StatusBadGateway, fmt.Sprintf("GitHub 查询失败: %v", err))
		return
	}

	commentDedupeKey := hashString(strings.Join([]string{
		clientIP,
		strconv.Itoa(issueNumber),
		req.Body,
	}, "|"))
	if s.dedupe.SeenRecently(commentDedupeKey, s.cfg.DuplicateWindow) {
		writeError(c, http.StatusConflict, "检测到短时间重复评论")
		return
	}

	reviewDecision, reviewErr := s.reviewer.Review(c.Request.Context(), moderation.ReviewInput{
		Type:   "comment",
		Title:  issueStatus.Title,
		Detail: req.Body,
		ExtraContext: buildCommentModerationContext(
			issueStatus.Title,
			issueStatus.Body,
			issueStatus.Comments,
		),
	})
	moderationBlocked := reviewErr != nil || !reviewDecision.Allow
	moderationMessage := buildModerationMessage(reviewDecision, reviewErr)

	statusCode := http.StatusOK
	response := gin.H{
		"success": true,
	}

	if moderationBlocked {
		if s.archives == nil {
			writeError(c, http.StatusInternalServerError, "审核留档存储未初始化")
			return
		}
		archiveID := randomToken(12)
		archiveMarkdown := renderBlockedCommentArchiveMarkdown(
			archiveID,
			hashString(clientIP),
			issueNumber,
			issueStatus.Title,
			issueStatus.Body,
			req.Body,
			issueStatus.Comments,
			reviewDecision,
			reviewErr,
			time.Now().UTC(),
		)
		archiveFile, err := s.archives.SaveMarkdown(archiveID, archiveMarkdown)
		if err != nil {
			writeError(c, http.StatusInternalServerError, fmt.Sprintf("保存审核留档失败: %v", err))
			return
		}

		placeholder := renderBlockedCommentBody(archiveID, archiveFile, moderationMessage)
		createdComment, err := s.gh.CreateIssueComment(c.Request.Context(), issueNumber, placeholder)
		if err != nil {
			writeError(c, http.StatusBadGateway, fmt.Sprintf("GitHub 评论创建失败: %v", err))
			return
		}

		statusCode = http.StatusAccepted
		response["moderation_blocked"] = true
		response["moderation_message"] = moderationMessage
		response["archive_id"] = archiveID
		response["comment"] = gin.H{
			"id":           fmt.Sprintf("%d", createdComment.ID),
			"author":       createdComment.Author,
			"body":         createdComment.Body,
			"created_at":   createdComment.CreatedAt.UTC().Format(time.RFC3339),
			"is_developer": s.isDeveloperLogin(createdComment.Author),
		}
		c.JSON(statusCode, response)
		return
	}

	createdComment, err := s.gh.CreateIssueComment(c.Request.Context(), issueNumber, req.Body)
	if err != nil {
		writeError(c, http.StatusBadGateway, fmt.Sprintf("GitHub 评论创建失败: %v", err))
		return
	}

	response["comment"] = gin.H{
		"id":           fmt.Sprintf("%d", createdComment.ID),
		"author":       createdComment.Author,
		"body":         createdComment.Body,
		"created_at":   createdComment.CreatedAt.UTC().Format(time.RFC3339),
		"is_developer": s.isDeveloperLogin(createdComment.Author),
	}
	c.JSON(statusCode, response)
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

func (s *Server) validateTicketToken(issueNumber int, ticketToken string) bool {
	if strings.TrimSpace(ticketToken) == "" {
		return false
	}
	return s.tickets.Validate(issueNumber, ticketToken)
}

func parseIssueNumber(raw string) (int, error) {
	issueNumber, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || issueNumber <= 0 {
		return 0, fmt.Errorf("invalid issue number")
	}
	return issueNumber, nil
}

func buildModerationMessage(decision moderation.Decision, reviewErr error) string {
	if reviewErr != nil {
		return fmt.Sprintf("AI 审核异常，系统已按保护策略暂时隐藏内容：%s", reviewErr.Error())
	}
	if decision.Allow {
		return ""
	}
	if len(decision.Reasons) == 0 {
		return "AI 审核判定当前反馈不适合公开展示，已暂时隐藏。"
	}
	return "AI 审核暂时隐藏： " + strings.Join(decision.Reasons, "；")
}

func buildDeveloperLoginSet(cfg config.Config) map[string]struct{} {
	result := map[string]struct{}{}
	addLogin := func(raw string) {
		value := strings.ToLower(strings.TrimSpace(raw))
		if value == "" {
			return
		}
		result[value] = struct{}{}
	}

	addLogin(cfg.GitHubOwner)
	addLogin(cfg.GitHubTokenLogin)
	for _, login := range cfg.DeveloperLogins {
		addLogin(login)
	}
	return result
}

func (s *Server) isDeveloperLogin(author string) bool {
	_, ok := s.developers[strings.ToLower(strings.TrimSpace(author))]
	return ok
}

func buildCommentModerationContext(issueTitle string, issueBody string, comments []github.IssueComment) string {
	builder := &strings.Builder{}
	builder.WriteString("### 当前工单标题\n")
	builder.WriteString(issueTitle)
	builder.WriteString("\n\n### 当前工单正文\n")
	if strings.TrimSpace(issueBody) == "" {
		builder.WriteString("- 无正文\n")
	} else {
		builder.WriteString(issueBody)
		builder.WriteString("\n")
	}
	builder.WriteString("\n### 历史评论（按时间顺序）\n")

	if len(comments) == 0 {
		builder.WriteString("- 无历史评论\n")
		return builder.String()
	}

	for index, comment := range comments {
		builder.WriteString(fmt.Sprintf("%d) [%s] %s: %s\n", index+1, comment.CreatedAt.UTC().Format(time.RFC3339), comment.Author, comment.Body))
	}
	return builder.String()
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
	if strings.EqualFold(state, "closed") {
		return "closed"
	}

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
