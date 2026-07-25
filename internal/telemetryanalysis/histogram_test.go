package telemetryanalysis

import "testing"

func TestHistogramNormalizesDurationUnits(t *testing.T) {
	buckets, ok := histogramBuckets([]any{
		map[string]any{
			"bucketStart": "0 s",
			"bucketEnd":   "0.5 s",
			"bucketCount": 2,
		},
	})
	if !ok || len(buckets) != 1 {
		t.Fatalf("应识别 MetricKit 直方图")
	}
	if buckets[0].Upper != 500 || buckets[0].Unit != "ms" || buckets[0].Count != 2 {
		t.Fatalf("秒应规范化为毫秒: %+v", buckets[0])
	}
}

func TestUUIDFormattingMatchesDwarfdumpConvention(t *testing.T) {
	normalized := normalizeUUID("<70b89f27-1634-3580-a695-57cdb41d7743>")
	if normalized != "70B89F2716343580A69557CDB41D7743" {
		t.Fatalf("UUID 规范化错误: %s", normalized)
	}
	if formatUUID(normalized) != testUUID {
		t.Fatalf("UUID 分组格式错误: %s", formatUUID(normalized))
	}
}
