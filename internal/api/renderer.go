package api

import (
	"fmt"
	"strings"
	"time"

	"els-feedback-proxy/internal/github"
	"els-feedback-proxy/internal/moderation"
)

func renderIssueTitle(req SubmitIssueRequest) string {
	platform := req.Environment.Platform
	if platform == "" {
		platform = "unknown"
	}
	return fmt.Sprintf("[App反馈][%s] %s", strings.ToUpper(platform), req.Title)
}

func renderIssueBody(req SubmitIssueRequest, clientIPHash string) string {
	builder := &strings.Builder{}

	builder.WriteString("## 反馈类型\n")
	if req.Type == "bug" {
		builder.WriteString("- 问题反馈（Bug）\n\n")
	} else {
		builder.WriteString("- 功能建议（Feature）\n\n")
	}

	builder.WriteString("## 详细描述\n")
	builder.WriteString(req.Detail)
	builder.WriteString("\n\n")

	if req.ReproductionSteps != "" {
		builder.WriteString("## 可复现步骤\n")
		builder.WriteString(req.ReproductionSteps)
		builder.WriteString("\n\n")
	}

	if req.ExpectedBehavior != "" {
		builder.WriteString("## 预期行为\n")
		builder.WriteString(req.ExpectedBehavior)
		builder.WriteString("\n\n")
	}

	if req.ActualBehavior != "" {
		builder.WriteString("## 实际行为\n")
		builder.WriteString(req.ActualBehavior)
		builder.WriteString("\n\n")
	}

	if req.ExtraContext != "" {
		builder.WriteString("## 补充信息\n")
		builder.WriteString(req.ExtraContext)
		builder.WriteString("\n\n")
	}

	builder.WriteString("## 环境信息\n")
	builder.WriteString(fmt.Sprintf("- 平台: %s\n", req.Environment.Platform))
	builder.WriteString(fmt.Sprintf("- App 版本: %s (Build %s)\n", req.Environment.AppVersion, req.Environment.AppBuild))
	builder.WriteString(fmt.Sprintf("- 系统版本: %s\n", req.Environment.OSVersion))
	builder.WriteString(fmt.Sprintf("- 设备型号: %s\n", req.Environment.DeviceModel))
	builder.WriteString(fmt.Sprintf("- 语言: %s\n", req.Environment.LocaleIdentifier))
	builder.WriteString(fmt.Sprintf("- 时区: %s\n", req.Environment.TimezoneIdentifier))
	builder.WriteString("\n")

	builder.WriteString("## 最小诊断日志\n")
	if len(req.Logs) == 0 {
		builder.WriteString("- 无\n")
	} else {
		for _, line := range req.Logs {
			builder.WriteString("- ")
			builder.WriteString(line)
			builder.WriteString("\n")
		}
	}
	builder.WriteString("\n")

	builder.WriteString("## 服务端附注\n")
	builder.WriteString("- 来源: source/app-feedback\n")
	builder.WriteString("- 同步标记: 由用户提出自动更新的\n")
	builder.WriteString(fmt.Sprintf("- 客户端IP哈希: %s\n", clientIPHash))

	return builder.String()
}

func renderBlockedIssueTitle(req SubmitIssueRequest) string {
	platform := strings.ToUpper(strings.TrimSpace(req.Environment.Platform))
	if platform == "" {
		platform = "UNKNOWN"
	}
	feedbackType := strings.ToUpper(strings.TrimSpace(req.Type))
	if feedbackType == "" {
		feedbackType = "UNKNOWN"
	}
	return fmt.Sprintf("[App反馈][隐藏内容][%s][%s] 请到服务器查看留档", platform, feedbackType)
}

func renderBlockedIssueBody(archiveID, archiveFileName, moderationMessage string) string {
	builder := &strings.Builder{}
	builder.WriteString("## 内容已隐藏\n")
	builder.WriteString("- 该反馈被 AI 审核流程暂时隐藏，未在 GitHub 公开原文。\n")
	builder.WriteString("- 请登录服务器查看本地留档后再手动处理。\n\n")

	builder.WriteString("## 服务器留档信息\n")
	builder.WriteString(fmt.Sprintf("- archive_id: `%s`\n", archiveID))
	builder.WriteString(fmt.Sprintf("- 参考文件: `DATA_DIR/review-blocked/%s`\n\n", archiveFileName))

	builder.WriteString("## 审核说明\n")
	if strings.TrimSpace(moderationMessage) == "" {
		builder.WriteString("- AI 审核判定当前内容不适合公开。\n")
	} else {
		builder.WriteString("- ")
		builder.WriteString(moderationMessage)
		builder.WriteString("\n")
	}

	builder.WriteString("\n> 本工单仅用于提醒开发者查看服务器留档，不包含用户原始反馈文本。\n")
	return builder.String()
}

func renderBlockedArchiveMarkdown(
	archiveID string,
	clientIPHash string,
	req SubmitIssueRequest,
	decision moderation.Decision,
	reviewErr error,
	now time.Time,
) string {
	builder := &strings.Builder{}
	builder.WriteString("# 反馈审核留档（隐藏内容）\n\n")
	builder.WriteString("## 元数据\n")
	builder.WriteString(fmt.Sprintf("- archive_id: %s\n", archiveID))
	builder.WriteString(fmt.Sprintf("- created_at: %s\n", now.Format(time.RFC3339)))
	builder.WriteString(fmt.Sprintf("- client_ip_hash: %s\n", clientIPHash))
	builder.WriteString(fmt.Sprintf("- feedback_type: %s\n", req.Type))
	builder.WriteString("\n")

	builder.WriteString("## 审核结论\n")
	if reviewErr != nil {
		builder.WriteString("- allow: false\n")
		builder.WriteString(fmt.Sprintf("- error: %s\n", reviewErr.Error()))
	} else {
		builder.WriteString(fmt.Sprintf("- allow: %t\n", decision.Allow))
		builder.WriteString(fmt.Sprintf("- confidence: %.2f\n", decision.Confidence))
		if len(decision.Categories) > 0 {
			builder.WriteString(fmt.Sprintf("- categories: %s\n", strings.Join(decision.Categories, ", ")))
		}
		if len(decision.Reasons) > 0 {
			builder.WriteString("- reasons:\n")
			for _, reason := range decision.Reasons {
				builder.WriteString(fmt.Sprintf("  - %s\n", reason))
			}
		}
	}
	builder.WriteString("\n")

	builder.WriteString("## 原始反馈内容\n")
	builder.WriteString(fmt.Sprintf("### 标题\n%s\n\n", req.Title))
	builder.WriteString(fmt.Sprintf("### 详细描述\n%s\n\n", req.Detail))
	if strings.TrimSpace(req.ReproductionSteps) != "" {
		builder.WriteString(fmt.Sprintf("### 可复现步骤\n%s\n\n", req.ReproductionSteps))
	}
	if strings.TrimSpace(req.ExpectedBehavior) != "" {
		builder.WriteString(fmt.Sprintf("### 预期行为\n%s\n\n", req.ExpectedBehavior))
	}
	if strings.TrimSpace(req.ActualBehavior) != "" {
		builder.WriteString(fmt.Sprintf("### 实际行为\n%s\n\n", req.ActualBehavior))
	}
	if strings.TrimSpace(req.ExtraContext) != "" {
		builder.WriteString(fmt.Sprintf("### 补充信息\n%s\n\n", req.ExtraContext))
	}

	builder.WriteString("## 环境信息\n")
	builder.WriteString(fmt.Sprintf("- 平台: %s\n", req.Environment.Platform))
	builder.WriteString(fmt.Sprintf("- App 版本: %s (Build %s)\n", req.Environment.AppVersion, req.Environment.AppBuild))
	builder.WriteString(fmt.Sprintf("- 系统版本: %s\n", req.Environment.OSVersion))
	builder.WriteString(fmt.Sprintf("- 设备型号: %s\n", req.Environment.DeviceModel))
	builder.WriteString(fmt.Sprintf("- 语言: %s\n", req.Environment.LocaleIdentifier))
	builder.WriteString(fmt.Sprintf("- 时区: %s\n\n", req.Environment.TimezoneIdentifier))

	builder.WriteString("## 最小诊断日志\n")
	if len(req.Logs) == 0 {
		builder.WriteString("- 无\n")
	} else {
		for _, line := range req.Logs {
			builder.WriteString(fmt.Sprintf("- %s\n", line))
		}
	}
	return builder.String()
}

func renderBlockedCommentBody(archiveID, archiveFileName, moderationMessage string) string {
	builder := &strings.Builder{}
	builder.WriteString("🔒 该评论已被 AI 审核暂时隐藏。\n\n")
	builder.WriteString("请开发者登录服务器查看留档后处理：\n")
	builder.WriteString(fmt.Sprintf("- archive_id: `%s`\n", archiveID))
	builder.WriteString(fmt.Sprintf("- 文件: `DATA_DIR/review-blocked/%s`\n", archiveFileName))
	if strings.TrimSpace(moderationMessage) != "" {
		builder.WriteString(fmt.Sprintf("- 审核说明: %s\n", moderationMessage))
	}
	return builder.String()
}

func renderBlockedCommentArchiveMarkdown(
	archiveID string,
	clientIPHash string,
	issueNumber int,
	issueTitle string,
	issueBody string,
	commentBody string,
	existingComments []github.IssueComment,
	decision moderation.Decision,
	reviewErr error,
	now time.Time,
) string {
	builder := &strings.Builder{}
	builder.WriteString("# 评论审核留档（隐藏内容）\n\n")
	builder.WriteString("## 元数据\n")
	builder.WriteString(fmt.Sprintf("- archive_id: %s\n", archiveID))
	builder.WriteString(fmt.Sprintf("- created_at: %s\n", now.Format(time.RFC3339)))
	builder.WriteString(fmt.Sprintf("- issue_number: %d\n", issueNumber))
	builder.WriteString(fmt.Sprintf("- issue_title: %s\n", issueTitle))
	builder.WriteString(fmt.Sprintf("- client_ip_hash: %s\n\n", clientIPHash))

	builder.WriteString("## 工单正文\n")
	if strings.TrimSpace(issueBody) == "" {
		builder.WriteString("- 无正文\n\n")
	} else {
		builder.WriteString(issueBody)
		builder.WriteString("\n\n")
	}

	builder.WriteString("## 审核结论\n")
	if reviewErr != nil {
		builder.WriteString("- allow: false\n")
		builder.WriteString(fmt.Sprintf("- error: %s\n\n", reviewErr.Error()))
	} else {
		builder.WriteString(fmt.Sprintf("- allow: %t\n", decision.Allow))
		builder.WriteString(fmt.Sprintf("- confidence: %.2f\n", decision.Confidence))
		if len(decision.Categories) > 0 {
			builder.WriteString(fmt.Sprintf("- categories: %s\n", strings.Join(decision.Categories, ", ")))
		}
		if len(decision.Reasons) > 0 {
			builder.WriteString("- reasons:\n")
			for _, reason := range decision.Reasons {
				builder.WriteString(fmt.Sprintf("  - %s\n", reason))
			}
		}
		builder.WriteString("\n")
	}

	builder.WriteString("## 用户待发送评论原文\n")
	builder.WriteString(commentBody)
	builder.WriteString("\n\n")

	builder.WriteString("## 审核上下文（历史评论）\n")
	if len(existingComments) == 0 {
		builder.WriteString("- 无历史评论\n")
		return builder.String()
	}
	for index, item := range existingComments {
		builder.WriteString(fmt.Sprintf("%d) [%s] %s: %s\n", index+1, item.CreatedAt.UTC().Format(time.RFC3339), item.Author, item.Body))
	}
	return builder.String()
}
