package api

import (
	"strings"
	"testing"
)

func TestRenderIssueBodyContainsAutoUpdateMarker(t *testing.T) {
	req := SubmitIssueRequest{
		Type:   "bug",
		Title:  "测试标题",
		Detail: "测试详情",
		Environment: EnvironmentSnapshot{
			Platform:           "ios",
			AppVersion:         "1.0.0",
			AppBuild:           "100",
			OSVersion:          "iOS 18",
			DeviceModel:        "iPhone",
			LocaleIdentifier:   "zh_Hans",
			TimezoneIdentifier: "Asia/Shanghai",
		},
	}

	body := renderIssueBody(req, "ip-hash")
	if !strings.Contains(body, "由用户提出自动更新的") {
		t.Fatalf("Issue Markdown 缺少自动更新标记，body=%s", body)
	}
}
