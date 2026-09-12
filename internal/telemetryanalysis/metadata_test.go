package telemetryanalysis

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestReportMetadataUsesEventBuildWithoutLeakingBetweenEvents(t *testing.T) {
	input := filepath.Join(t.TempDir(), "raw")
	output := filepath.Join(t.TempDir(), "analysis")
	if err := os.MkdirAll(input, 0o700); err != nil {
		t.Fatal(err)
	}
	fixture := testEnvelopeFixture()
	fixture["app"].(map[string]any)["build"] = "442"
	payload := fixture["payload"].(map[string]any)
	original := payload["hangDiagnostics"].([]any)[0].(map[string]any)
	original["diagnosticMetaData"] = map[string]any{"appBuildVersion": "419", "appVersion": "1.9.0", "bundleIdentifier": "com.ericterminal.els", "deviceType": "iPhone17,1", "osVersion": "iPhone OS 18.7.8 (22H352)", "isTestFlightApp": false}
	payload["hangDiagnostics"] = []any{original, map[string]any{"diagnosticMetaData": map[string]any{"appBuildVersion": "420", "bundleIdentifier": "another.bundle"}}}
	path := filepath.Join(input, "fixture.json")
	writeJSONFixture(t, path, fixture)
	before, _ := os.ReadFile(path)
	a := &analyzer{options: Options{InputDir: input, OutputDir: output}, symbolicator: fakeFrameSymbolicator{}, histograms: map[histogramKey]*histogramAccumulator{}, measurements: map[measurementKey]*measurementAccumulator{}, signposts: map[signpostKey]*signpostAccumulator{}, unknownFields: map[unknownFieldKey]int{}, missing: map[string]*missingSymbolRow{}}
	if err := a.scan(); err != nil {
		t.Fatal(err)
	}
	if len(a.diagnosticRows) != 2 {
		t.Fatalf("应分别保留两个事件: %+v", a.diagnosticRows)
	}
	first, second := a.diagnosticRows[0], a.diagnosticRows[1]
	if first.AppBuild != "419" || first.CapturedBuild != "442" || first.BuildSource != "payload" || first.OSVersion != "18.7.8" || first.DistributionEvidence != "metric-kit:false;envelope-conflict" {
		t.Fatalf("内部元数据归因错误: %+v", first)
	}
	if second.AppBuild != "420" || second.BundleID != "another.bundle" || second.DistributionEvidence != "envelope_only" {
		t.Fatalf("事件间元数据串用: %+v", second)
	}
	if len(a.finalizeSampleGroups()) != 2 {
		t.Fatal("不同事件 Bundle/构建被合并")
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("分析修改了原始数据")
	}
}

func TestMetricPayloadClassesAndDimensionsStaySeparate(t *testing.T) {
	a := &analyzer{histograms: map[histogramKey]*histogramAccumulator{}, measurements: map[measurementKey]*measurementAccumulator{}}
	full := map[string]any{"cpuMetrics": map[string]any{}}
	disk := map[string]any{"diskSpaceUsageMetrics": map[string]any{}}
	omitted := map[string]any{"_etos": map[string]any{"source_omitted": "source_nesting_limit"}}
	if payloadClass("metric", full) != "full_metric" || payloadClass("metric", disk) != "disk_space_only" || payloadClass("diagnostic", omitted) != "source_omitted" {
		t.Fatal("报告类别识别错误")
	}
	row := indexRow{AppBuild: "442", BundleID: "com.ericterminal.els", PayloadClass: "full_metric"}
	a.addMeasurement(row, "payload.space", 1, "MB")
	row.PayloadClass = "disk_space_only"
	a.addMeasurement(row, "payload.space", 1, "MB")
	row.BundleID = "another.bundle"
	a.addMeasurement(row, "payload.space", 1, "MB")
	if len(a.finalizeMeasurements()) != 3 {
		t.Fatal("完整指标、磁盘指标或其他 Bundle 被混合")
	}
}

func TestAttributionNeverFallsBackToUnrelatedThread(t *testing.T) {
	unrelated := map[string]any{"threadAttributed": false, "callStackFrames": []any{map[string]any{"symbolicated": "unrelated", "sampleCount": true, "frameID": 0, "depth": 2}}}
	diagnostic := map[string]any{"callStackTree": map[string]any{"callStacks": []any{unrelated, map[string]any{"threadAttributed": true, "callStackFrames": []any{}}}}}
	if attributedTopFrame(diagnostic) != "" {
		t.Fatal("空归因栈被其他线程冒充")
	}
	frames := collectUsefulFrames(unrelated)
	if len(frames) != 1 || frames[0] != "depth=2 samples=1 frame=0 | unrelated" {
		t.Fatalf("栈深度或旧数值布尔解释错误: %v", frames)
	}
	if stackAttribution(map[string]any{}) != "unknown" {
		t.Fatal("缺少标记的线程应保持未知")
	}
}

func TestSignpostUsesOfficialFieldAndPreservesLegacyNumericBooleans(t *testing.T) {
	a := &analyzer{signposts: map[signpostKey]*signpostAccumulator{}}
	a.collectSignposts(map[string]any{"signpostName": "Render", "totalSignpostCount": true, "totalCount": 99}, indexRow{})
	rows := a.finalizeSignposts()
	if len(rows) != 1 || rows[0].TotalCount != 1 {
		t.Fatalf("未优先使用真实次数: %+v", rows)
	}
	if _, ok := numberValue(true); ok {
		t.Fatal("不允许全局将布尔值解释为资源测量")
	}
}

func TestExabyteWritesAreIsolatedWithoutClampingRealDiskWrites(t *testing.T) {
	a := &analyzer{measurements: map[measurementKey]*measurementAccumulator{}}
	row := indexRow{PayloadID: "fixture"}
	a.addMeasurement(row, "payload.diskIOMetrics.cumulativeLogicalWrites", 1.8014398505260997e19, "bytes")
	a.addMeasurement(row, "payload.diskIOMetrics.cumulativeLogicalWrites", 17_180, "MB")
	values := a.finalizeMeasurements()
	if len(a.outliers) != 1 || len(values) != 1 || values[0].Total != 17_180_000_000 {
		t.Fatalf("异常值隔离或十进制单位错误: %v %+v", a.outliers, values)
	}
	if _, err := json.Marshal(a.outliers); err != nil {
		t.Fatal(err)
	}
}
