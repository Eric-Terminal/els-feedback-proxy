package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

const githubWebhookSignaturePrefix = "sha256="

type githubWebhookPingPayload struct {
	Zen        string                  `json:"zen"`
	HookID     int64                   `json:"hook_id"`
	Repository githubWebhookRepository `json:"repository"`
}

type githubWebhookReleasePayload struct {
	Action     string                  `json:"action"`
	Repository githubWebhookRepository `json:"repository"`
	Release    githubWebhookRelease    `json:"release"`
}

type githubWebhookRepository struct {
	FullName string `json:"full_name"`
}

type githubWebhookRelease struct {
	TagName    string `json:"tag_name"`
	HTMLURL    string `json:"html_url"`
	Draft      bool   `json:"draft"`
	Prerelease bool   `json:"prerelease"`
}

func (s *Server) handleGitHubWebhook(c *gin.Context) {
	if s.selfUpdater == nil || strings.TrimSpace(s.cfg.GitHubWebhookSecret) == "" {
		writeError(c, http.StatusNotFound, "GitHub Webhook 未启用")
		return
	}

	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		writeError(c, http.StatusBadRequest, "读取 GitHub Webhook 请求体失败")
		return
	}

	signature := strings.TrimSpace(c.GetHeader("X-Hub-Signature-256"))
	if !isValidGitHubWebhookSignature(s.cfg.GitHubWebhookSecret, body, signature) {
		writeError(c, http.StatusForbidden, "GitHub Webhook 签名校验失败")
		return
	}

	event := strings.TrimSpace(c.GetHeader("X-GitHub-Event"))
	deliveryID := strings.TrimSpace(c.GetHeader("X-GitHub-Delivery"))

	switch event {
	case "ping":
		var payload githubWebhookPingPayload
		if err := json.Unmarshal(body, &payload); err != nil {
			writeError(c, http.StatusBadRequest, "GitHub Webhook ping 负载无效")
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"success":     true,
			"event":       event,
			"delivery_id": deliveryID,
			"repository":  payload.Repository.FullName,
			"zen":         payload.Zen,
		})
	case "release":
		s.handleGitHubReleaseWebhook(c, body, deliveryID)
	default:
		c.JSON(http.StatusAccepted, gin.H{
			"success":     true,
			"event":       event,
			"delivery_id": deliveryID,
			"ignored":     true,
			"reason":      "未处理该 GitHub 事件类型",
		})
	}
}

func (s *Server) handleGitHubReleaseWebhook(c *gin.Context, body []byte, deliveryID string) {
	var payload githubWebhookReleasePayload
	if err := json.Unmarshal(body, &payload); err != nil {
		writeError(c, http.StatusBadRequest, "GitHub release 事件负载无效")
		return
	}

	expectedRepository := strings.ToLower(strings.TrimSpace(fmt.Sprintf("%s/%s", s.cfg.SelfUpdateRepoOwner, s.cfg.SelfUpdateRepoName)))
	actualRepository := strings.ToLower(strings.TrimSpace(payload.Repository.FullName))
	if expectedRepository == "" || actualRepository != expectedRepository {
		c.JSON(http.StatusAccepted, gin.H{
			"success":     true,
			"event":       "release",
			"delivery_id": deliveryID,
			"ignored":     true,
			"reason":      "仓库不匹配",
		})
		return
	}

	if !shouldHandleGitHubReleaseAction(payload.Action) {
		c.JSON(http.StatusAccepted, gin.H{
			"success":     true,
			"event":       "release",
			"delivery_id": deliveryID,
			"ignored":     true,
			"reason":      "当前 release 动作不触发自动更新",
			"action":      payload.Action,
		})
		return
	}

	if payload.Release.Draft || payload.Release.Prerelease {
		c.JSON(http.StatusAccepted, gin.H{
			"success":     true,
			"event":       "release",
			"delivery_id": deliveryID,
			"ignored":     true,
			"reason":      "草稿版或预发布版本不触发自动更新",
			"tag":         payload.Release.TagName,
		})
		return
	}

	accepted, err := s.selfUpdater.startRelease(payload.Release.TagName, false)
	if err != nil {
		if errors.Is(err, errSelfUpdateBusy) {
			c.JSON(http.StatusAccepted, gin.H{
				"success":     true,
				"event":       "release",
				"delivery_id": deliveryID,
				"ignored":     true,
				"reason":      err.Error(),
				"tag":         payload.Release.TagName,
			})
			return
		}
		writeError(c, http.StatusBadGateway, fmt.Sprintf("GitHub release 自动更新失败: %v", err))
		return
	}

	log.Printf("收到 GitHub release webhook: delivery=%s repo=%s action=%s tag=%s", deliveryID, payload.Repository.FullName, payload.Action, payload.Release.TagName)
	c.JSON(http.StatusAccepted, gin.H{
		"success":     true,
		"event":       "release",
		"delivery_id": deliveryID,
		"accepted":    accepted,
		"status":      s.selfUpdater.statusSnapshot(),
	})
}

func isValidGitHubWebhookSignature(secret string, body []byte, signature string) bool {
	secret = strings.TrimSpace(secret)
	signature = strings.TrimSpace(signature)
	if secret == "" || signature == "" || !strings.HasPrefix(signature, githubWebhookSignaturePrefix) {
		return false
	}

	expectedMAC := hmac.New(sha256.New, []byte(secret))
	expectedMAC.Write(body)
	expectedHex := hex.EncodeToString(expectedMAC.Sum(nil))
	providedHex := strings.TrimPrefix(signature, githubWebhookSignaturePrefix)
	if len(providedHex) != len(expectedHex) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(strings.ToLower(providedHex)), []byte(expectedHex)) == 1
}

func shouldHandleGitHubReleaseAction(action string) bool {
	switch strings.ToLower(strings.TrimSpace(action)) {
	case "published", "released":
		return true
	default:
		return false
	}
}
