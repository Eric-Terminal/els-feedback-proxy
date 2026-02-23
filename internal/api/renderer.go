package api

import (
	"fmt"
	"strings"
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
