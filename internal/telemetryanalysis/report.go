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

func (a *analyzer) writeReports(
	histograms []histogramRow,
	measurements []measurementRow,
	signposts []signpostRow,
	sampleGroups []sampleGroupRow,
	unknownFields []unknownFieldRow,
) error {
	if err := writeCSV(
		filepath.Join(a.options.OutputDir, "raw-index.csv"),
		[]string{
			"payload_id", "kind", "captured_at", "period_start", "period_end",
			"app_version", "app_build", "distribution", "os_version",
			"device_class", "architecture", "source_path", "size_bytes",
		},
		func(writer *csv.Writer) error {
			sort.Slice(a.indexRows, func(i, j int) bool {
				return a.indexRows[i].CapturedAt < a.indexRows[j].CapturedAt
			})
			for _, row := range a.indexRows {
				if err := writer.Write([]string{
					row.PayloadID, row.Kind, row.CapturedAt, row.PeriodStart, row.PeriodEnd,
					row.AppVersion, row.AppBuild, row.Distribution, row.OSVersion,
					row.DeviceClass, row.Architecture, row.SourcePath,
					strconv.FormatInt(row.SizeBytes, 10),
				}); err != nil {
					return err
				}
			}
			return nil
		},
	); err != nil {
		return err
	}

	if err := writeCSV(
		filepath.Join(a.options.OutputDir, "sample-groups.csv"),
		[]string{
			"date", "app_version", "app_build", "distribution", "os_version",
			"device_class", "metric_count", "diagnostic_count", "total_count",
		},
		func(writer *csv.Writer) error {
			for _, row := range sampleGroups {
				total := row.MetricCount + row.DiagnosticCount
				if err := writer.Write([]string{
					row.Date, row.AppVersion, row.AppBuild, row.Distribution,
					row.OSVersion, row.DeviceClass,
					strconv.Itoa(row.MetricCount),
					strconv.Itoa(row.DiagnosticCount),
					strconv.Itoa(total),
				}); err != nil {
					return err
				}
			}
			return nil
		},
	); err != nil {
		return err
	}

	if err := writeCSV(
		filepath.Join(a.options.OutputDir, "histograms.csv"),
		[]string{
			"app_version", "app_build", "distribution", "os_version",
			"device_class", "metric_path", "unit", "count", "p50", "p90", "p99",
		},
		func(writer *csv.Writer) error {
			for _, row := range histograms {
				if err := writer.Write([]string{
					row.AppVersion, row.AppBuild, row.Distribution, row.OSVersion,
					row.DeviceClass, row.MetricPath, row.Unit,
					strconv.FormatInt(row.Count, 10),
					csvFloat(row.P50), csvFloat(row.P90), csvFloat(row.P99),
				}); err != nil {
					return err
				}
			}
			return nil
		},
	); err != nil {
		return err
	}

	if err := writeCSV(
		filepath.Join(a.options.OutputDir, "unknown-fields.csv"),
		[]string{
			"app_version", "app_build", "distribution", "os_version",
			"device_class", "field_path", "occurrence_count",
		},
		func(writer *csv.Writer) error {
			for _, row := range unknownFields {
				if err := writer.Write([]string{
					row.AppVersion, row.AppBuild, row.Distribution, row.OSVersion,
					row.DeviceClass, row.FieldPath,
					strconv.Itoa(row.OccurrenceCount),
				}); err != nil {
					return err
				}
			}
			return nil
		},
	); err != nil {
		return err
	}

	if err := writeCSV(
		filepath.Join(a.options.OutputDir, "diagnostics.csv"),
		[]string{
			"payload_id", "app_version", "app_build", "distribution",
			"os_version", "device_class", "type", "duration_value",
			"duration_unit", "top_frame",
		},
		func(writer *csv.Writer) error {
			for _, row := range a.diagnosticRows {
				if err := writer.Write([]string{
					row.PayloadID, row.AppVersion, row.AppBuild, row.Distribution,
					row.OSVersion, row.DeviceClass, row.Type,
					csvFloat(row.DurationValue), row.DurationUnit, row.TopFrame,
				}); err != nil {
					return err
				}
			}
			return nil
		},
	); err != nil {
		return err
	}

	if err := writeCSV(
		filepath.Join(a.options.OutputDir, "measurements.csv"),
		[]string{
			"app_version", "app_build", "distribution", "os_version",
			"device_class", "metric_path", "unit", "sample_count",
			"total", "average", "minimum", "maximum",
		},
		func(writer *csv.Writer) error {
			for _, row := range measurements {
				if err := writer.Write([]string{
					row.AppVersion, row.AppBuild, row.Distribution, row.OSVersion,
					row.DeviceClass, row.MetricPath, row.Unit,
					strconv.FormatInt(row.Count, 10), csvFloat(row.Total),
					csvFloat(row.Average), csvFloat(row.Minimum), csvFloat(row.Maximum),
				}); err != nil {
					return err
				}
			}
			return nil
		},
	); err != nil {
		return err
	}

	if err := writeCSV(
		filepath.Join(a.options.OutputDir, "signposts.csv"),
		[]string{
			"app_version", "app_build", "distribution", "os_version",
			"device_class", "category", "name", "metric_entry_count", "total_count",
		},
		func(writer *csv.Writer) error {
			for _, row := range signposts {
				if err := writer.Write([]string{
					row.AppVersion, row.AppBuild, row.Distribution, row.OSVersion,
					row.DeviceClass, row.Category, row.Name,
					strconv.FormatInt(row.EntryCount, 10),
					strconv.FormatInt(row.TotalCount, 10),
				}); err != nil {
					return err
				}
			}
			return nil
		},
	); err != nil {
		return err
	}

	if err := writeCSV(
		filepath.Join(a.options.OutputDir, "parse-errors.csv"),
		[]string{"source_path", "error"},
		func(writer *csv.Writer) error {
			sort.Slice(a.parseErrors, func(i, j int) bool {
				return a.parseErrors[i].SourcePath < a.parseErrors[j].SourcePath
			})
			for _, row := range a.parseErrors {
				if err := writer.Write([]string{row.SourcePath, row.Error}); err != nil {
					return err
				}
			}
			return nil
		},
	); err != nil {
		return err
	}

	if err := a.writeDiagnosticStacks(); err != nil {
		return err
	}

	missingRows := make([]missingSymbolRow, 0, len(a.missing))
	for _, row := range a.missing {
		missingRows = append(missingRows, *row)
	}
	sort.Slice(missingRows, func(i, j int) bool {
		if missingRows[i].Count != missingRows[j].Count {
			return missingRows[i].Count > missingRows[j].Count
		}
		return missingRows[i].UUID < missingRows[j].UUID
	})
	if err := writeCSV(
		filepath.Join(a.options.OutputDir, "missing-symbols.csv"),
		[]string{"uuid", "binary_name", "architecture", "status", "frame_count"},
		func(writer *csv.Writer) error {
			for _, row := range missingRows {
				if err := writer.Write([]string{
					formatUUID(row.UUID), row.BinaryName, row.Arch, row.Status,
					strconv.Itoa(row.Count),
				}); err != nil {
					return err
				}
			}
			return nil
		},
	); err != nil {
		return err
	}

	return a.writeMarkdownSummary(
		histograms,
		measurements,
		signposts,
		sampleGroups,
		unknownFields,
		missingRows,
	)
}

func (a *analyzer) writeDiagnosticStacks() error {
	var builder strings.Builder
	builder.WriteString("# ETOS 性能诊断调用栈\n\n")
	builder.WriteString(
		"> 调用栈来自匿名 MetricKit 诊断；同一诊断可能包含多个线程，" +
			"Release 优化会使部分帧和行号近似。\n\n",
	)
	for _, row := range a.diagnosticStacks {
		builder.WriteString(fmt.Sprintf(
			"## %s · 构建 %s · iOS %s · %s\n\n",
			escapeMarkdown(row.Type),
			escapeMarkdown(row.AppBuild),
			escapeMarkdown(row.OSVersion),
			escapeMarkdown(row.DeviceClass),
		))
		builder.WriteString(fmt.Sprintf(
			"- Payload：`%s`\n- 分发：%s\n\n",
			row.PayloadID,
			escapeMarkdown(row.Distribution),
		))
		if len(row.Frames) == 0 {
			builder.WriteString("- 未发现可展示的调用栈帧。\n\n")
			continue
		}
		for index, frame := range row.Frames {
			builder.WriteString(fmt.Sprintf(
				"%d. `%s`\n",
				index+1,
				escapeMarkdownCode(frame),
			))
		}
		builder.WriteString("\n")
	}
	if len(a.diagnosticStacks) == 0 {
		builder.WriteString("暂无诊断调用栈。\n")
	}
	return os.WriteFile(
		filepath.Join(a.options.OutputDir, "diagnostic-stacks.md"),
		[]byte(builder.String()),
		0o600,
	)
}

func (a *analyzer) writeMarkdownSummary(
	histograms []histogramRow,
	measurements []measurementRow,
	signposts []signpostRow,
	sampleGroups []sampleGroupRow,
	unknownFields []unknownFieldRow,
	missingRows []missingSymbolRow,
) error {
	var builder strings.Builder
	builder.WriteString("# ETOS 性能遥测分析摘要\n\n")
	builder.WriteString(fmt.Sprintf(
		"- 原始文件：%d（指标 %d，诊断 %d）\n",
		len(a.indexRows),
		a.metricCount,
		a.diagnosticCount,
	))
	builder.WriteString(fmt.Sprintf("- 已符号化调用栈帧：%d\n", a.symbolicated))
	builder.WriteString(fmt.Sprintf("- 缺少或无法使用的符号 UUID：%d\n", len(missingRows)))
	builder.WriteString(fmt.Sprintf("- 当前分析器未识别的顶层字段：%d\n", len(unknownFields)))
	builder.WriteString(fmt.Sprintf("- 无法解析的原始文件：%d\n", len(a.parseErrors)))

	builder.WriteString("\n## 按日期与构建的样本分布\n\n")
	builder.WriteString("| 日期 | 版本 | 构建 | 分发 | iOS | 设备 | Metric | Diagnostic | 合计 |\n")
	builder.WriteString("| --- | --- | --- | --- | --- | --- | ---: | ---: | ---: |\n")
	for _, row := range sampleGroups {
		builder.WriteString(fmt.Sprintf(
			"| %s | %s | %s | %s | %s | %s | %d | %d | %d |\n",
			escapeMarkdown(row.Date),
			escapeMarkdown(row.AppVersion),
			escapeMarkdown(row.AppBuild),
			escapeMarkdown(row.Distribution),
			escapeMarkdown(row.OSVersion),
			escapeMarkdown(row.DeviceClass),
			row.MetricCount,
			row.DiagnosticCount,
			row.MetricCount+row.DiagnosticCount,
		))
	}
	if len(sampleGroups) == 0 {
		builder.WriteString("| — | — | — | — | — | — | 0 | 0 | 0 |\n")
	}

	builder.WriteString("\n## 待优先复查的性能线索\n\n")
	clues := performanceClues(histograms)
	if len(clues) == 0 {
		builder.WriteString("当前样本没有达到自动提示阈值；这不代表不存在性能问题。\n")
	} else {
		for _, clue := range clues {
			builder.WriteString("- " + clue + "\n")
		}
	}

	builder.WriteString("\n## MXSignpost 调用次数\n\n")
	builder.WriteString("| 构建 | 分发 | iOS | 设备 | Signpost | 报告条目 | 累计次数 |\n")
	builder.WriteString("| --- | --- | --- | --- | --- | ---: | ---: |\n")
	rankedSignposts := append([]signpostRow(nil), signposts...)
	sort.Slice(rankedSignposts, func(i, j int) bool {
		if rankedSignposts[i].TotalCount != rankedSignposts[j].TotalCount {
			return rankedSignposts[i].TotalCount > rankedSignposts[j].TotalCount
		}
		return signpostSortKey(rankedSignposts[i]) < signpostSortKey(rankedSignposts[j])
	})
	for index, row := range rankedSignposts {
		if index >= 50 {
			break
		}
		builder.WriteString(fmt.Sprintf(
			"| %s | %s | %s | %s | %s.%s | %d | %d |\n",
			escapeMarkdown(row.AppBuild),
			escapeMarkdown(row.Distribution),
			escapeMarkdown(row.OSVersion),
			escapeMarkdown(row.DeviceClass),
			escapeMarkdown(row.Category),
			escapeMarkdown(row.Name),
			row.EntryCount,
			row.TotalCount,
		))
	}
	if len(rankedSignposts) == 0 {
		builder.WriteString("| — | — | — | — | 暂无 Signpost 次数 | 0 | 0 |\n")
	}

	builder.WriteString("\n## MetricKit 与 MXSignpost 直方图\n\n")
	builder.WriteString("| 构建 | 分发 | iOS | 设备 | 指标 | 样本 | P50 | P90 | P99 |\n")
	builder.WriteString("| --- | --- | --- | --- | --- | ---: | ---: | ---: | ---: |\n")
	for _, row := range histograms {
		builder.WriteString(fmt.Sprintf(
			"| %s | %s | %s | %s | %s | %d | %s | %s | %s |\n",
			escapeMarkdown(row.AppBuild),
			escapeMarkdown(row.Distribution),
			escapeMarkdown(row.OSVersion),
			escapeMarkdown(row.DeviceClass),
			escapeMarkdown(row.MetricPath),
			row.Count,
			displayMeasurement(row.P50, row.Unit),
			displayMeasurement(row.P90, row.Unit),
			displayMeasurement(row.P99, row.Unit),
		))
	}
	if len(histograms) == 0 {
		builder.WriteString("| — | — | — | — | 暂无可解析直方图 | 0 | — | — | — |\n")
	}

	builder.WriteString("\n## CPU、内存、磁盘、网络与运行时间测量\n\n")
	builder.WriteString("| 构建 | 分发 | iOS | 设备 | 指标 | 样本 | 合计 | 平均 | 最大 |\n")
	builder.WriteString("| --- | --- | --- | --- | --- | ---: | ---: | ---: | ---: |\n")
	for _, row := range measurements {
		builder.WriteString(fmt.Sprintf(
			"| %s | %s | %s | %s | %s | %d | %s | %s | %s |\n",
			escapeMarkdown(row.AppBuild),
			escapeMarkdown(row.Distribution),
			escapeMarkdown(row.OSVersion),
			escapeMarkdown(row.DeviceClass),
			escapeMarkdown(row.MetricPath),
			row.Count,
			displayMeasurement(row.Total, row.Unit),
			displayMeasurement(row.Average, row.Unit),
			displayMeasurement(row.Maximum, row.Unit),
		))
	}
	if len(measurements) == 0 {
		builder.WriteString("| — | — | — | — | 暂无可解析测量值 | 0 | — | — | — |\n")
	}

	builder.WriteString("\n## 诊断事件\n\n")
	builder.WriteString("| 构建 | 分发 | iOS | 类型 | 设备 | 时长 | 首个可用栈帧 |\n")
	builder.WriteString("| --- | --- | --- | --- | --- | ---: | --- |\n")
	for _, row := range a.diagnosticRows {
		builder.WriteString(fmt.Sprintf(
			"| %s | %s | %s | %s | %s | %s | %s |\n",
			escapeMarkdown(row.AppBuild),
			escapeMarkdown(row.Distribution),
			escapeMarkdown(row.OSVersion),
			escapeMarkdown(row.Type),
			escapeMarkdown(row.DeviceClass),
			displayMeasurement(row.DurationValue, row.DurationUnit),
			escapeMarkdown(row.TopFrame),
		))
	}
	if len(a.diagnosticRows) == 0 {
		builder.WriteString("| — | — | — | 暂无诊断 | — | — | — |\n")
	}

	builder.WriteString("\n## 缺失符号\n\n")
	builder.WriteString("| UUID | 二进制 | 架构 | 状态 | 帧数 |\n")
	builder.WriteString("| --- | --- | --- | --- | ---: |\n")
	for _, row := range missingRows {
		builder.WriteString(fmt.Sprintf(
			"| %s | %s | %s | %s | %d |\n",
			formatUUID(row.UUID),
			escapeMarkdown(row.BinaryName),
			escapeMarkdown(row.Arch),
			escapeMarkdown(row.Status),
			row.Count,
		))
	}
	if len(missingRows) == 0 {
		builder.WriteString("| — | — | — | 无 | 0 |\n")
	}

	builder.WriteString("\n## 解析失败\n\n")
	builder.WriteString("| 原始文件 | 原因 |\n")
	builder.WriteString("| --- | --- |\n")
	for _, row := range a.parseErrors {
		builder.WriteString(fmt.Sprintf(
			"| %s | %s |\n",
			escapeMarkdown(row.SourcePath),
			escapeMarkdown(row.Error),
		))
	}
	if len(a.parseErrors) == 0 {
		builder.WriteString("| — | 无 |\n")
	}

	builder.WriteString("\n## 当前分析器未识别的 MetricKit 顶层字段\n\n")
	builder.WriteString("| 构建 | 分发 | iOS | 设备 | 字段 | 出现次数 |\n")
	builder.WriteString("| --- | --- | --- | --- | --- | ---: |\n")
	for _, row := range unknownFields {
		builder.WriteString(fmt.Sprintf(
			"| %s | %s | %s | %s | `%s` | %d |\n",
			escapeMarkdown(row.AppBuild),
			escapeMarkdown(row.Distribution),
			escapeMarkdown(row.OSVersion),
			escapeMarkdown(row.DeviceClass),
			escapeMarkdownCode(row.FieldPath),
			row.OccurrenceCount,
		))
	}
	if len(unknownFields) == 0 {
		builder.WriteString("| — | — | — | — | 无 | 0 |\n")
	}
	builder.WriteString(
		"\n未识别字段仍完整保留在 raw 与 symbolicated JSON 中；此清单用于提示更新本地分析规则。\n",
	)

	builder.WriteString(
		"\n> 分位数使用 MetricKit 直方图桶上界近似；报告中的“样本”和次数不是用户数。" +
			"跨构建比较时应保持分发渠道、iOS、设备类型和单位一致。" +
			"高频或长尾提示是复查线索，不会自动证明某段代码就是根因。\n",
	)
	return os.WriteFile(
		filepath.Join(a.options.OutputDir, "summary.md"),
		[]byte(builder.String()),
		0o600,
	)
}

func performanceClues(histograms []histogramRow) []string {
	type rankedClue struct {
		ratio float64
		text  string
	}
	ranked := make([]rankedClue, 0)
	for _, row := range histograms {
		if row.Count < 10 || row.P50 <= 0 || row.P99 < row.P50*3 {
			continue
		}
		ratio := row.P99 / row.P50
		ranked = append(ranked, rankedClue{
			ratio: ratio,
			text: fmt.Sprintf(
				"构建 %s / %s / iOS %s / %s 的 `%s` 长尾明显：P99 是 P50 的 %.1f 倍（%s → %s）。",
				escapeMarkdown(row.AppBuild),
				escapeMarkdown(row.Distribution),
				escapeMarkdown(row.OSVersion),
				escapeMarkdown(row.DeviceClass),
				escapeMarkdownCode(row.MetricPath),
				ratio,
				displayMeasurement(row.P50, row.Unit),
				displayMeasurement(row.P99, row.Unit),
			),
		})
	}
	sort.Slice(ranked, func(i, j int) bool {
		return ranked[i].ratio > ranked[j].ratio
	})
	result := make([]string, 0, min(len(ranked), 20))
	for index, clue := range ranked {
		if index >= 20 {
			break
		}
		result = append(result, clue.text)
	}
	return result
}

func writeCSV(
	path string,
	header []string,
	writeRows func(*csv.Writer) error,
) error {
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
	value = strings.ReplaceAll(value, "|", "\\|")
	value = strings.ReplaceAll(value, "\n", " ")
	return value
}

func escapeMarkdownCode(value string) string {
	value = strings.ReplaceAll(value, "`", "ˋ")
	return strings.ReplaceAll(value, "\n", " ")
}
