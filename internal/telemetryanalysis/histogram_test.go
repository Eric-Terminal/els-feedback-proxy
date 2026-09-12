package telemetryanalysis

import (
	"encoding/json"
	"math"
	"testing"
)

func TestDictionaryHistogramUsesCountsRatherThanBucketEntries(t *testing.T) {
	buckets, ok := histogramBuckets(map[string]any{
		"2": map[string]any{"bucketStart": "10 ms", "bucketEnd": "20 ms", "bucketCount": json.Number("9")},
		"1": map[string]any{"bucketStart": "0 ms", "bucketEnd": "10 ms", "bucketCount": true},
		"3": map[string]any{"bucketStart": "20 ms", "bucketEnd": "30 ms", "bucketCount": false},
	})
	if !ok {
		t.Fatal("未识别字典直方图与历史布尔计数")
	}
	a := &analyzer{histograms: map[histogramKey]*histogramAccumulator{}}
	a.addHistogram(indexRow{}, "fixture", buckets)
	rows := a.finalizeHistograms()
	if len(rows) != 1 || rows[0].Count != 10 || rows[0].P50 != 20 || rows[0].P90 != 20 || rows[0].P99 != 20 {
		t.Fatalf("应按桶内样本数加权: %+v", rows)
	}
	for _, invalid := range []float64{math.Inf(1), math.NaN(), 1.5, float64(math.MaxInt64)} {
		if _, ok := integerCount(invalid); ok {
			t.Fatalf("不应接受无效次数: %v", invalid)
		}
	}
}

func TestMetricKitHistogramValueIsCollectedAndExcludedFromResources(t *testing.T) {
	for _, field := range []string{"histogramValue", "histogram"} {
		for _, dictionary := range []bool{false, true} {
			first := map[string]any{"bucketStart": "0 ms", "bucketEnd": "10 ms", "bucketCount": true}
			second := map[string]any{"bucketStart": "10 ms", "bucketEnd": "20 ms", "bucketCount": json.Number("9")}
			var buckets any = []any{first, second}
			if dictionary {
				buckets = map[string]any{"1": first, "2": second}
			}
			payload := map[string]any{"applicationLaunchMetrics": map[string]any{"histogrammedTimeToFirstDrawKey": map[string]any{field: buckets}}}
			a := &analyzer{histograms: map[histogramKey]*histogramAccumulator{}, measurements: map[measurementKey]*measurementAccumulator{}}
			a.collectHistograms(payload, "payload", indexRow{}, nil)
			a.collectMeasurements(payload, "payload", indexRow{})
			rows := a.finalizeHistograms()
			if len(rows) != 1 || rows[0].Count != 10 || rows[0].P50 != 20 || len(a.finalizeMeasurements()) != 0 {
				t.Fatalf("真实容器格式必须产生加权分位数且不能把桶边界当作资源读数: field=%s dictionary=%v rows=%+v", field, dictionary, rows)
			}
		}
	}
	if _, ok := histogramBuckets([]any{map[string]any{"bucketStart": "1 ms", "bucketCount": 1}}); ok {
		t.Fatal("缺失上界不能把下界冒充为分位数上界")
	}
}

func TestDecimalAndBinaryStorageUnitsRemainDistinct(t *testing.T) {
	decimal, _ := normalizeUnit(1, "MB")
	binary, _ := normalizeUnit(1, "MiB")
	if decimal != 1_000_000 || binary != 1_048_576 {
		t.Fatalf("单位混淆: %v %v", decimal, binary)
	}
}

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
