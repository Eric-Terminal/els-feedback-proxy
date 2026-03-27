package api

import (
	"strings"
	"testing"
	"time"

	"els-feedback-proxy/internal/config"
	"els-feedback-proxy/internal/github"
)

func TestMapIssueStatusClosedHasPriority(t *testing.T) {
	status := mapIssueStatus("closed", []string{"status/triage", "status/in-progress"})
	if status != "closed" {
		t.Fatalf("期望 closed，实际 %s", status)
	}
}

func TestMapIssueStatusFallsBackToInProgress(t *testing.T) {
	status := mapIssueStatus("open", []string{"type/bug"})
	if status != "in_progress" {
		t.Fatalf("期望 in_progress，实际 %s", status)
	}
}

func TestBuildDeveloperLoginSetIncludesTokenOwner(t *testing.T) {
	set := buildDeveloperLoginSet(config.Config{
		GitHubOwner:      "Eric-Terminal",
		GitHubTokenLogin: "feedback-bot",
		DeveloperLogins:  []string{"another-dev"},
	})

	if _, ok := set["eric-terminal"]; !ok {
		t.Fatalf("期望包含仓库所有者")
	}
	if _, ok := set["feedback-bot"]; !ok {
		t.Fatalf("期望包含 token 账号")
	}
	if _, ok := set["another-dev"]; !ok {
		t.Fatalf("期望包含额外开发者")
	}
}

func TestBuildCommentModerationContextContainsIssueAndComments(t *testing.T) {
	contextText := buildCommentModerationContext(
		"标题A",
		"正文B",
		[]github.IssueComment{
			{
				ID:        1,
				Author:    "dev",
				Body:      "历史评论",
				CreatedAt: time.Date(2026, 3, 29, 8, 0, 0, 0, time.UTC),
			},
		},
	)

	if !strings.Contains(contextText, "正文B") || !strings.Contains(contextText, "历史评论") {
		t.Fatalf("上下文应包含工单正文与历史评论")
	}
}
