package telemetryanalysis

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testUUID = "70B89F27-1634-3580-A695-57CDB41D7743"

type fakeFrameSymbolicator struct{}

func (fakeFrameSymbolicator) Symbolicate(
	uuid string,
	binaryName string,
	architecture string,
	address uint64,
	offset uint64,
) symbolicationResult {
	if uuid == normalizeUUID(testUUID) && binaryName == "ETOS LLM Studio" &&
		architecture == "arm64" && address > 0 && offset == 4096 {
		return symbolicationResult{
			Symbol:   "ChatViewModel.processStream() (in ETOS LLM Studio) (ChatViewModel.swift:42)",
			Status:   "symbolicated",
			DSYMPath: "/fixtures/ETOS LLM Studio.app.dSYM",
		}
	}
	return symbolicationResult{Status: "missing_dsym"}
}

func TestAnalyzerExtractsSignpostPercentilesAndSymbolicatesDiagnostics(t *testing.T) {
	inputDir := filepath.Join(t.TempDir(), "raw", "2026-07-26")
	outputDir := filepath.Join(t.TempDir(), "analysis")
	if err := os.MkdirAll(inputDir, 0o755); err != nil {
		t.Fatalf("创建输入目录失败: %v", err)
	}
	fixture := testEnvelopeFixture()
	inputPath := filepath.Join(inputDir, "diagnostic_fixture.json")
	writeJSONFixture(t, inputPath, fixture)

	instance := &analyzer{
		options: Options{
			InputDir:  filepath.Dir(inputDir),
			OutputDir: outputDir,
		},
		symbolicator: fakeFrameSymbolicator{},
		histograms:   make(map[histogramKey]*histogramAccumulator),
		missing:      make(map[string]*missingSymbolRow),
	}
	if err := instance.scan(); err != nil {
		t.Fatalf("扫描合成遥测失败: %v", err)
	}
	rows := instance.finalizeHistograms()
	if len(rows) != 1 {
		t.Fatalf("应提取 1 个 MXSignpost 直方图，实际 %d", len(rows))
	}
	row := rows[0]
	if row.MetricPath != "signpost.Network.ModelRequestStreaming.duration" ||
		row.Unit != "ms" || row.Count != 4 ||
		row.P50 != 20 || row.P90 != 30 || row.P99 != 30 {
		t.Fatalf("分位数聚合错误: %+v", row)
	}
	if instance.symbolicated != 1 || len(instance.missing) != 0 {
		t.Fatalf(
			"调用栈应完整符号化: symbolicated=%d missing=%d",
			instance.symbolicated,
			len(instance.missing),
		)
	}
	if len(instance.diagnosticRows) != 1 ||
		instance.diagnosticRows[0].DurationValue != 2_000 ||
		!strings.Contains(instance.diagnosticRows[0].TopFrame, "ChatViewModel.processStream") {
		t.Fatalf("诊断摘要错误: %+v", instance.diagnosticRows)
	}
	if err := instance.writeReports(rows); err != nil {
		t.Fatalf("写分析报告失败: %v", err)
	}

	summary, err := os.ReadFile(filepath.Join(outputDir, "summary.md"))
	if err != nil {
		t.Fatalf("读取 Markdown 摘要失败: %v", err)
	}
	if !bytes.Contains(summary, []byte("ModelRequestStreaming")) ||
		!bytes.Contains(summary, []byte("ChatViewModel.processStream")) {
		t.Fatalf("摘要未包含关键 MXSignpost 或符号: %s", summary)
	}
	symbolicatedPath := filepath.Join(
		outputDir,
		"symbolicated",
		"2026-07-26",
		"diagnostic_fixture.json",
	)
	symbolicated, err := os.ReadFile(symbolicatedPath)
	if err != nil {
		t.Fatalf("读取符号化 JSON 失败: %v", err)
	}
	if !bytes.Contains(symbolicated, []byte(`"symbolicationStatus": "symbolicated"`)) {
		t.Fatalf("符号化 JSON 未写入状态: %s", symbolicated)
	}
}

func TestAnalyzeReportsMissingDSYMWithoutDiscardingRawIndex(t *testing.T) {
	inputDir := filepath.Join(t.TempDir(), "raw")
	outputDir := filepath.Join(t.TempDir(), "analysis")
	if err := os.MkdirAll(inputDir, 0o755); err != nil {
		t.Fatalf("创建输入目录失败: %v", err)
	}
	writeJSONFixture(t, filepath.Join(inputDir, "fixture.json"), testEnvelopeFixture())

	result, err := Analyze(Options{
		InputDir:     inputDir,
		OutputDir:    outputDir,
		UseSpotlight: false,
	})
	if err != nil {
		t.Fatalf("无 dSYM 时仍应产出索引和缺失报告: %v", err)
	}
	if result.FileCount != 1 || result.MissingSymbols != 1 {
		t.Fatalf("分析结果计数错误: %+v", result)
	}
	missing, err := os.ReadFile(filepath.Join(outputDir, "missing-symbols.csv"))
	if err != nil {
		t.Fatalf("读取缺失符号报告失败: %v", err)
	}
	if !bytes.Contains(missing, []byte(testUUID)) ||
		!bytes.Contains(missing, []byte("missing_dsym")) {
		t.Fatalf("缺失符号报告内容错误: %s", missing)
	}
	if _, err := os.Stat(filepath.Join(outputDir, "raw-index.csv")); err != nil {
		t.Fatalf("无 dSYM 时仍应生成原始索引: %v", err)
	}
}

func TestAnalyzeRejectsInvalidExplicitSymbolPath(t *testing.T) {
	_, err := Analyze(Options{
		InputDir:    t.TempDir(),
		OutputDir:   t.TempDir(),
		SymbolPaths: []string{filepath.Join(t.TempDir(), "missing.xcarchive")},
	})
	if err == nil || !strings.Contains(err.Error(), "加载符号路径") {
		t.Fatalf("明确提供的无效符号路径应报错，实际: %v", err)
	}
}

func TestAnalyzeRejectsOutputInsideRawInput(t *testing.T) {
	inputDir := t.TempDir()
	_, err := Analyze(Options{
		InputDir:  inputDir,
		OutputDir: filepath.Join(inputDir, "analysis"),
	})
	if err == nil || !strings.Contains(err.Error(), "不能位于") {
		t.Fatalf("应阻止输出目录递归落入原始输入目录，实际: %v", err)
	}
}

func testEnvelopeFixture() map[string]any {
	return map[string]any{
		"schema_version": 1,
		"payload_id":     strings.Repeat("a", 64),
		"kind":           "diagnostic",
		"captured_at":    "2026-07-26T12:00:00Z",
		"period_start":   "2026-07-25T12:00:00Z",
		"period_end":     "2026-07-26T12:00:00Z",
		"app": map[string]any{
			"version":      "2.7.0",
			"build":        "270",
			"distribution": "testflight",
		},
		"platform": map[string]any{
			"name":         "ios",
			"os_version":   "26.0",
			"device_class": "iPhone17,2",
			"architecture": "arm64",
		},
		"privacy": map[string]any{
			"contains_chat_content":    false,
			"contains_request_body":    false,
			"contains_response_body":   false,
			"contains_credentials":     false,
			"contains_user_identifier": false,
		},
		"payload": map[string]any{
			"hangDiagnostics": []any{
				map[string]any{
					"hangDuration": map[string]any{"value": 2, "unit": "s"},
					"callStackTree": map[string]any{
						"callStacks": []any{
							map[string]any{
								"threadAttributed": true,
								"callStackRootFrames": []any{
									map[string]any{
										"binaryName":                  "ETOS LLM Studio",
										"binaryUUID":                  testUUID,
										"address":                     4_294_971_392,
										"offsetIntoBinaryTextSegment": 4096,
										"sampleCount":                 1,
									},
								},
							},
						},
						"callStackPerThread": true,
					},
				},
			},
			"signpostMetrics": []any{
				map[string]any{
					"signpostCategory": "Network",
					"signpostName":     "ModelRequestStreaming",
					"totalCount":       4,
					"signpostIntervalData": map[string]any{
						"histogrammedSignpostDuration": map[string]any{
							"histogram": []any{
								map[string]any{
									"bucketStart": "0 ms",
									"bucketEnd":   "10 ms",
									"bucketCount": 1,
								},
								map[string]any{
									"bucketStart": "10 ms",
									"bucketEnd":   "20 ms",
									"bucketCount": 2,
								},
								map[string]any{
									"bucketStart": "20 ms",
									"bucketEnd":   "30 ms",
									"bucketCount": 1,
								},
							},
						},
					},
				},
			},
		},
	}
}

func writeJSONFixture(t *testing.T, path string, payload any) {
	t.Helper()
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		t.Fatalf("编码合成遥测失败: %v", err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		t.Fatalf("写入合成遥测失败: %v", err)
	}
}
