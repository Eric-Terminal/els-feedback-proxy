package telemetryanalysis

import (
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// 每个聚合表都输出完整维度，防止不同 Bundle 或不完整报告看起来像同一组。
func dimensionHeader() []string {
	return []string{"app_version", "app_build", "distribution", "os_version", "device_class", "bundle_id", "payload_class", "distribution_evidence"}
}

func dimensionValues(d analysisDimensions) []string {
	return []string{d.AppVersion, d.AppBuild, d.Distribution, d.OSVersion, d.DeviceClass, d.BundleID, d.PayloadClass, d.DistributionEvidence}
}

func (a *analyzer) writeReports(histograms []histogramRow, measurements []measurementRow, signposts []signpostRow, sampleGroups []sampleGroupRow, unknownFields []unknownFieldRow) error {
	type table struct {
		name   string
		header []string
		rows   [][]string
	}
	index := table{name: "raw-index.csv", header: append([]string{"payload_id", "kind", "captured_at", "period_start", "period_end"}, dimensionHeader()...)}
	index.header = append(index.header, "architecture", "source_path", "size_bytes", "build_source", "captured_build")
	sort.SliceStable(a.indexRows, func(i, j int) bool { return a.indexRows[i].CapturedAt < a.indexRows[j].CapturedAt })
	for _, r := range a.indexRows {
		values := append([]string{r.PayloadID, r.Kind, r.CapturedAt, r.PeriodStart, r.PeriodEnd}, dimensionValues(dimensionsFor(r))...)
		index.rows = append(index.rows, append(values, r.Architecture, r.SourcePath, strconv.FormatInt(r.SizeBytes, 10), r.BuildSource, r.CapturedBuild))
	}
	groups := table{name: "sample-groups.csv", header: append([]string{"date"}, dimensionHeader()...)}
	groups.header = append(groups.header, "metric_count", "diagnostic_count", "total_count")
	for _, r := range sampleGroups {
		values := append([]string{r.Date}, dimensionValues(r.analysisDimensions)...)
		groups.rows = append(groups.rows, append(values, strconv.Itoa(r.MetricCount), strconv.Itoa(r.DiagnosticCount), strconv.Itoa(r.MetricCount+r.DiagnosticCount)))
	}
	histogram := table{name: "histograms.csv", header: append(dimensionHeader(), "metric_path", "unit", "count", "p50", "p90", "p99")}
	for _, r := range histograms {
		histogram.rows = append(histogram.rows, append(dimensionValues(r.analysisDimensions), r.MetricPath, r.Unit, strconv.FormatInt(r.Count, 10), csvFloat(r.P50), csvFloat(r.P90), csvFloat(r.P99)))
	}
	measurement := table{name: "measurements.csv", header: append(dimensionHeader(), "metric_path", "unit", "sample_count", "total", "average", "minimum", "maximum")}
	for _, r := range measurements {
		measurement.rows = append(measurement.rows, append(dimensionValues(r.analysisDimensions), r.MetricPath, r.Unit, strconv.FormatInt(r.Count, 10), csvFloat(r.Total), csvFloat(r.Average), csvFloat(r.Minimum), csvFloat(r.Maximum)))
	}
	signpost := table{name: "signposts.csv", header: append(dimensionHeader(), "category", "name", "metric_entry_count", "total_count")}
	for _, r := range signposts {
		signpost.rows = append(signpost.rows, append(dimensionValues(r.analysisDimensions), r.Category, r.Name, strconv.FormatInt(r.EntryCount, 10), strconv.FormatInt(r.TotalCount, 10)))
	}
	unknown := table{name: "unknown-fields.csv", header: append(dimensionHeader(), "field_path", "occurrence_count")}
	for _, r := range unknownFields {
		unknown.rows = append(unknown.rows, append(dimensionValues(r.analysisDimensions), r.FieldPath, strconv.Itoa(r.OccurrenceCount)))
	}
	diagnostics := table{name: "diagnostics.csv", header: append([]string{"payload_id"}, dimensionHeader()...)}
	diagnostics.header = append(diagnostics.header, "type", "duration_value", "duration_unit", "top_frame", "build_source", "captured_build")
	for _, r := range a.diagnosticRows {
		dims := analysisDimensions{AppVersion: r.AppVersion, AppBuild: r.AppBuild, Distribution: r.Distribution, OSVersion: r.OSVersion, DeviceClass: r.DeviceClass, BundleID: r.BundleID, PayloadClass: r.PayloadClass, DistributionEvidence: r.DistributionEvidence}
		values := append([]string{r.PayloadID}, dimensionValues(dims)...)
		diagnostics.rows = append(diagnostics.rows, append(values, r.Type, csvFloat(r.DurationValue), r.DurationUnit, r.TopFrame, r.BuildSource, r.CapturedBuild))
	}
	outliers := table{name: "measurement-outliers.csv", header: []string{"payload_id", "source_path", "metric_path", "value", "unit", "reason"}}
	for _, r := range a.outliers {
		outliers.rows = append(outliers.rows, []string{r.PayloadID, r.SourcePath, r.MetricPath, strconv.FormatFloat(r.Value, 'g', -1, 64), r.Unit, r.Reason})
	}
	parseErrors := table{name: "parse-errors.csv", header: []string{"source_path", "error"}}
	sort.Slice(a.parseErrors, func(i, j int) bool { return a.parseErrors[i].SourcePath < a.parseErrors[j].SourcePath })
	for _, r := range a.parseErrors {
		parseErrors.rows = append(parseErrors.rows, []string{r.SourcePath, r.Error})
	}
	missingRows := make([]missingSymbolRow, 0, len(a.missing))
	for _, r := range a.missing {
		missingRows = append(missingRows, *r)
	}
	sort.Slice(missingRows, func(i, j int) bool {
		if missingRows[i].Count != missingRows[j].Count {
			return missingRows[i].Count > missingRows[j].Count
		}
		return missingRows[i].UUID < missingRows[j].UUID
	})
	missing := table{name: "missing-symbols.csv", header: []string{"uuid", "binary_name", "architecture", "status", "frame_count"}}
	for _, r := range missingRows {
		missing.rows = append(missing.rows, []string{formatUUID(r.UUID), r.BinaryName, r.Arch, r.Status, strconv.Itoa(r.Count)})
	}
	for _, t := range []table{index, groups, histogram, measurement, signpost, unknown, diagnostics, outliers, parseErrors, missing} {
		if err := writeCSV(filepath.Join(a.options.OutputDir, t.name), t.header, func(w *csv.Writer) error {
			for _, row := range t.rows {
				if err := w.Write(row); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			return err
		}
	}
	if err := a.writeDiagnosticStacks(); err != nil {
		return err
	}
	return a.writeMarkdownSummary(histograms, measurements, signposts, sampleGroups, unknownFields, missingRows)
}

func (a *analyzer) writeDiagnosticStacks() error {
	var b strings.Builder
	b.WriteString("# ETOS 性能诊断调用栈\n\n线程按原始数组序号分开。只有 attributed 是明确归因线程；unknown 和 not_attributed 不作为崩溃原因。完整父帧、深度、样本数和 UUID 保留在 symbolicated JSON。\n\n")
	for _, r := range a.diagnosticStacks {
		fmt.Fprintf(&b, "## %s · 构建 %s · 线程 %d（%s）\n\n- Bundle：%s\n- Payload：`%s`\n- 分发：%s\n- iOS：%s · %s\n\n", escapeMarkdown(r.Type), escapeMarkdown(r.AppBuild), r.ThreadIndex, r.Attribution, escapeMarkdown(r.BundleID), r.PayloadID, escapeMarkdown(r.Distribution), escapeMarkdown(r.OSVersion), escapeMarkdown(r.DeviceClass))
		if len(r.Frames) == 0 {
			b.WriteString("原始线程未包含可展示栈帧。\n\n")
			continue
		}
		for i, frame := range r.Frames {
			fmt.Fprintf(&b, "%d. `%s`\n", i+1, escapeMarkdownCode(frame))
		}
		b.WriteString("\n")
	}
	if len(a.diagnosticStacks) == 0 {
		b.WriteString("暂无诊断调用栈。\n")
	}
	return os.WriteFile(filepath.Join(a.options.OutputDir, "diagnostic-stacks.md"), []byte(b.String()), 0o600)
}

func (a *analyzer) writeMarkdownSummary(histograms []histogramRow, measurements []measurementRow, signposts []signpostRow, sampleGroups []sampleGroupRow, unknownFields []unknownFieldRow, missingRows []missingSymbolRow) error {
	var b strings.Builder
	b.WriteString("# ETOS 性能遥测分析摘要\n\n")
	fmt.Fprintf(&b, "- 原始文件：%d（指标 %d，诊断信封 %d）\n- 可读取诊断事件：%d\n- 已符号化栈帧：%d\n- 缺少或无法使用的符号 UUID：%d\n- 无法解析的文件：%d\n- 已隔离异常测量：%d\n\n", len(a.indexRows), a.metricCount, a.diagnosticCount, len(a.diagnosticRows), a.symbolicated, len(missingRows), len(a.parseErrors), len(a.outliers))
	b.WriteString("版本优先来自 MetricKit 内部元数据；raw-index.csv 同时保留 captured_build 和 build_source。诊断事件各自归因版本，信封索引没有内部版本时标记 envelope_fallback。非数字构建标记单独保留，不能并入数字构建区间。\n\n")
	b.WriteString("分组包含 Bundle、报告类别和渠道证据。full_metric、disk_space_only、source_omitted 分别统计；source_omitted 的诊断计数仅表示省略信封，其余诊断计数表示可读取事件。envelope-conflict 表示渠道信息冲突，不能据此比较 TestFlight 与正式发布表现。计数不是用户数或崩溃率。\n\n")
	b.WriteString("直方图 P50/P90/P99 按桶计数加权，结果是所在桶的上界近似。十进制 kB/MB/GB 与二进制 KiB/MiB/GiB 分开换算。旧客户端数值布尔化只在已知数值字段解释，原始文件不改写。异常值详见 measurement-outliers.csv；缺少 dSYM 不影响资源和直方图分析。\n\n")
	b.WriteString("## 按日期与构建的样本分布\n\n| 日期 | Bundle | 构建 | 类别 | 分发证据 | iOS | 设备 | Metric | Diagnostic |\n| --- | --- | --- | --- | --- | --- | --- | ---: | ---: |\n")
	for _, r := range sampleGroups {
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %s | %s | %d | %d |\n", escapeMarkdown(r.Date), escapeMarkdown(r.BundleID), escapeMarkdown(r.AppBuild), r.PayloadClass, escapeMarkdown(r.Distribution+" / "+r.DistributionEvidence), escapeMarkdown(r.OSVersion), escapeMarkdown(r.DeviceClass), r.MetricCount, r.DiagnosticCount)
	}
	b.WriteString("\n## 待复查的直方图长尾\n\n")
	for _, clue := range performanceClues(histograms) {
		b.WriteString("- " + clue + "\n")
	}
	b.WriteString("\n## MXSignpost 次数\n\n| Bundle | 构建 | 类别 | Signpost | 报告条目 | 累计次数 |\n| --- | --- | --- | --- | ---: | ---: |\n")
	for _, r := range signposts {
		fmt.Fprintf(&b, "| %s | %s | %s | %s.%s | %d | %d |\n", escapeMarkdown(r.BundleID), escapeMarkdown(r.AppBuild), r.PayloadClass, escapeMarkdown(r.Category), escapeMarkdown(r.Name), r.EntryCount, r.TotalCount)
	}
	b.WriteString("\n## MetricKit 与 MXSignpost 直方图\n\n| Bundle | 构建 | 类别 | 指标 | 样本 | P50 | P90 | P99 |\n| --- | --- | --- | --- | ---: | ---: | ---: | ---: |\n")
	for _, r := range histograms {
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %d | %s | %s | %s |\n", escapeMarkdown(r.BundleID), escapeMarkdown(r.AppBuild), r.PayloadClass, escapeMarkdown(r.MetricPath), r.Count, displayMeasurement(r.P50, r.Unit), displayMeasurement(r.P90, r.Unit), displayMeasurement(r.P99, r.Unit))
	}
	b.WriteString("\n## 资源测量\n\n| Bundle | 构建 | 类别 | 指标 | 样本 | 合计 | 平均 | 最大 |\n| --- | --- | --- | --- | ---: | ---: | ---: | ---: |\n")
	for _, r := range measurements {
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %d | %s | %s | %s |\n", escapeMarkdown(r.BundleID), escapeMarkdown(r.AppBuild), r.PayloadClass, escapeMarkdown(r.MetricPath), r.Count, displayMeasurement(r.Total, r.Unit), displayMeasurement(r.Average, r.Unit), displayMeasurement(r.Maximum, r.Unit))
	}
	b.WriteString("\n## 诊断事件\n\n| Bundle | 构建 | 类型 | 首个可用归因帧 |\n| --- | --- | --- | --- |\n")
	for _, r := range a.diagnosticRows {
		fmt.Fprintf(&b, "| %s | %s | %s | %s |\n", escapeMarkdown(r.BundleID), escapeMarkdown(r.AppBuild), escapeMarkdown(r.Type), escapeMarkdown(r.TopFrame))
	}
	b.WriteString("\n## 未识别字段\n\n")
	for _, r := range unknownFields {
		fmt.Fprintf(&b, "- %s / 构建 %s：`%s`（%d 次）\n", escapeMarkdown(r.BundleID), escapeMarkdown(r.AppBuild), escapeMarkdownCode(r.FieldPath), r.OccurrenceCount)
	}
	b.WriteString("\n完整分析维度见各 CSV；缺失 UUID 见 missing-symbols.csv；解析失败见 parse-errors.csv；逐线程证据见 diagnostic-stacks.md。高频、长尾和栈帧只提供复查线索，不自动证明根因。\n")
	return os.WriteFile(filepath.Join(a.options.OutputDir, "summary.md"), []byte(b.String()), 0o600)
}

func performanceClues(histograms []histogramRow) []string {
	type clue struct {
		ratio float64
		text  string
	}
	var ranked []clue
	for _, r := range histograms {
		if r.Count < 10 || r.P50 <= 0 || r.P99 < r.P50*3 {
			continue
		}
		ratio := r.P99 / r.P50
		ranked = append(ranked, clue{ratio, fmt.Sprintf("%s / 构建 %s / %s / iOS %s / %s 的 `%s`：P99 是 P50 的 %.1f 倍（%s → %s）。", escapeMarkdown(r.BundleID), escapeMarkdown(r.AppBuild), escapeMarkdown(r.Distribution), escapeMarkdown(r.OSVersion), escapeMarkdown(r.DeviceClass), escapeMarkdownCode(r.MetricPath), ratio, displayMeasurement(r.P50, r.Unit), displayMeasurement(r.P99, r.Unit))})
	}
	sort.SliceStable(ranked, func(i, j int) bool { return ranked[i].ratio > ranked[j].ratio })
	var result []string
	for i, r := range ranked {
		if i >= 20 {
			break
		}
		result = append(result, r.text)
	}
	return result
}

func writeCSV(path string, header []string, writeRows func(*csv.Writer) error) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("创建 CSV 失败: %w", err)
	}
	defer file.Close()
	writer := csv.NewWriter(file)
	if err := writer.Write(header); err != nil {
		return err
	}
	if err := writeRows(writer); err != nil {
		return err
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return err
	}
	return file.Sync()
}

func escapeMarkdown(value string) string {
	return strings.ReplaceAll(strings.ReplaceAll(value, "|", "\\|"), "\n", " ")
}
func escapeMarkdownCode(value string) string {
	return strings.ReplaceAll(strings.ReplaceAll(value, "`", "ˋ"), "\n", " ")
}
