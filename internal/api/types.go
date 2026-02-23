package api

import "strings"

// SubmitIssueRequest 客户端提交反馈请求体
type SubmitIssueRequest struct {
	Type              string              `json:"type"`
	Title             string              `json:"title"`
	Detail            string              `json:"detail"`
	ReproductionSteps string              `json:"reproduction_steps"`
	ExpectedBehavior  string              `json:"expected_behavior"`
	ActualBehavior    string              `json:"actual_behavior"`
	ExtraContext      string              `json:"extra_context"`
	Environment       EnvironmentSnapshot `json:"environment"`
	Logs              []string            `json:"logs"`
}

// EnvironmentSnapshot 客户端自动采集环境
type EnvironmentSnapshot struct {
	Platform           string `json:"platform"`
	AppVersion         string `json:"appVersion"`
	AppBuild           string `json:"appBuild"`
	OSVersion          string `json:"osVersion"`
	DeviceModel        string `json:"deviceModel"`
	LocaleIdentifier   string `json:"localeIdentifier"`
	TimezoneIdentifier string `json:"timezoneIdentifier"`
}

func (r *SubmitIssueRequest) Normalize() {
	r.Type = strings.TrimSpace(strings.ToLower(r.Type))
	r.Title = strings.TrimSpace(r.Title)
	r.Detail = strings.TrimSpace(r.Detail)
	r.ReproductionSteps = strings.TrimSpace(r.ReproductionSteps)
	r.ExpectedBehavior = strings.TrimSpace(r.ExpectedBehavior)
	r.ActualBehavior = strings.TrimSpace(r.ActualBehavior)
	r.ExtraContext = strings.TrimSpace(r.ExtraContext)
	r.Environment.Platform = strings.TrimSpace(strings.ToLower(r.Environment.Platform))
	r.Environment.AppVersion = strings.TrimSpace(r.Environment.AppVersion)
	r.Environment.AppBuild = strings.TrimSpace(r.Environment.AppBuild)
	r.Environment.OSVersion = strings.TrimSpace(r.Environment.OSVersion)
	r.Environment.DeviceModel = strings.TrimSpace(r.Environment.DeviceModel)
	r.Environment.LocaleIdentifier = strings.TrimSpace(r.Environment.LocaleIdentifier)
	r.Environment.TimezoneIdentifier = strings.TrimSpace(r.Environment.TimezoneIdentifier)

	normalizedLogs := make([]string, 0, len(r.Logs))
	for _, item := range r.Logs {
		trimmed := strings.TrimSpace(item)
		if trimmed != "" {
			normalizedLogs = append(normalizedLogs, trimmed)
		}
	}
	r.Logs = normalizedLogs
}

func (r *SubmitIssueRequest) Validate() error {
	if r.Type != "bug" && r.Type != "suggestion" {
		return errBadRequest("type 仅支持 bug 或 suggestion")
	}
	if len([]rune(r.Title)) < 4 || len([]rune(r.Title)) > 120 {
		return errBadRequest("title 长度必须在 4 到 120 字符之间")
	}
	if len([]rune(r.Detail)) < 10 || len([]rune(r.Detail)) > 4000 {
		return errBadRequest("detail 长度必须在 10 到 4000 字符之间")
	}
	if len(r.Logs) > 50 {
		return errBadRequest("logs 条目过多")
	}
	return nil
}

type apiError struct {
	Message string
	Code    int
}

func (e apiError) Error() string {
	return e.Message
}

func errBadRequest(message string) error {
	return apiError{Message: message, Code: 400}
}
