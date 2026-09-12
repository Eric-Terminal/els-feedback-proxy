package telemetryanalysis

import "math"

type measurementOutlier struct {
	PayloadID  string
	SourcePath string
	MetricPath string
	Value      float64
	Unit       string
	Reason     string
}

func measurementOutlierReason(path string, value float64, unit string) string {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return "non_finite"
	}
	if value < 0 {
		return "negative_measurement"
	}
	// EB 级磁盘量单独保留，不污染平均值；仅凭测量值不能断定具体溢出机制。
	if unit == "bytes" && value >= float64(uint64(1)<<63) &&
		(path == "payload.diskIOMetrics.cumulativeLogicalWrites" ||
			path == "payload.diskSpaceUsageMetrics.totalDiskSpaceCapacity" ||
			path == "payload.diskSpaceUsageMetrics.totalDiskSpaceUsed" ||
			path == "payload.diskSpaceUsageMetrics.totalDiskSpaceAvailable") {
		return "implausible_exabyte_measurement"
	}
	return ""
}

func diagnosticCallStacks(diagnostic map[string]any) []any {
	tree := nestedMap(diagnostic, "callStackTree")
	stacks, _ := tree["callStacks"].([]any)
	return stacks
}

func stackAttribution(stack any) string {
	object, _ := stack.(map[string]any)
	value, ok := object["threadAttributed"].(bool)
	if !ok {
		return "unknown"
	}
	if value {
		return "attributed"
	}
	return "not_attributed"
}

func attributedTopFrame(diagnostic map[string]any) string {
	for _, stack := range diagnosticCallStacks(diagnostic) {
		if stackAttribution(stack) == "attributed" {
			if frame := firstUsefulFrame(stack); frame != "" {
				return frame
			}
		}
	}
	return ""
}
