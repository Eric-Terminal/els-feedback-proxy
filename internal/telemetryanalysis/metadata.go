package telemetryanalysis

import (
	"regexp"
	"strings"
)

var reportOSVersionPattern = regexp.MustCompile(`(?:iPhone OS|iOS|watchOS)\s+([0-9]+(?:\.[0-9]+)*)`)

// 上传信封描述采集时的进程；MetricKit 内部元数据才描述事件发生时的版本和设备。
// 保留上传版本与渠道证据，不把 isTestFlightApp=false 推断为某个其他分发渠道。
func reportMetadata(row indexRow, payload map[string]any) indexRow {
	metadata := nestedMap(payload, "metaData")
	if diagnostic := nestedMap(payload, "diagnosticMetaData"); diagnostic != nil {
		metadata = diagnostic
	}
	if version := stringValue(payload["appVersion"]); version != "" {
		row.AppVersion = version
	}
	if version := stringValue(metadata["appVersion"]); version != "" {
		row.AppVersion = version
	}
	if build := stringValue(metadata["appBuildVersion"]); build != "" {
		row.AppBuild = build
		row.BuildSource = "payload"
	}
	if bundle := stringValue(metadata["bundleIdentifier"]); bundle != "" {
		row.BundleID = bundle
	}
	if device := stringValue(metadata["deviceType"]); device != "" {
		row.DeviceClass = device
	}
	if arch := stringValue(metadata["platformArchitecture"]); arch != "" {
		row.Architecture = arch
	}
	if version := stringValue(metadata["osVersion"]); version != "" {
		row.OSVersion = version
		if match := reportOSVersionPattern.FindStringSubmatch(version); len(match) == 2 {
			row.OSVersion = match[1]
		}
	}
	if testflight, ok := metadata["isTestFlightApp"].(bool); ok {
		row.DistributionEvidence = "metric-kit:false"
		if testflight {
			row.DistributionEvidence = "metric-kit:true"
		}
		if (testflight && row.Distribution != "testflight") || (!testflight && row.Distribution == "testflight") {
			row.DistributionEvidence += ";envelope-conflict"
		}
	}
	return row
}

func payloadClass(kind string, payload map[string]any) string {
	info := nestedMap(payload, "_etos")
	if stringValue(info["source_omitted"]) != "" {
		return "source_omitted"
	}
	if kind != "metric" {
		return kind
	}
	_, disk := payload["diskSpaceUsageMetrics"]
	for key := range payload {
		if strings.HasSuffix(key, "Metrics") && key != "diskSpaceUsageMetrics" {
			return "full_metric"
		}
	}
	if disk {
		return "disk_space_only"
	}
	return "other_metric"
}
