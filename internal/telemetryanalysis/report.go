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

func (a *analyzer) writeReports(histograms []histogramRow) error {
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
		filepath.Join(a.options.OutputDir, "histograms.csv"),
		[]string{
			"app_version", "app_build", "device_class", "metric_path",
			"unit", "count", "p50", "p90", "p99",
		},
		func(writer *csv.Writer) error {
			for _, row := range histograms {
				if err := writer.Write([]string{
					row.AppVersion, row.AppBuild, row.DeviceClass, row.MetricPath,
					row.Unit, strconv.FormatInt(row.Count, 10),
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
		filepath.Join(a.options.OutputDir, "diagnostics.csv"),
		[]string{
			"payload_id", "app_version", "app_build", "device_class",
			"type", "duration_value", "duration_unit", "top_frame",
		},
		func(writer *csv.Writer) error {
			for _, row := range a.diagnosticRows {
				if err := writer.Write([]string{
					row.PayloadID, row.AppVersion, row.AppBuild, row.DeviceClass,
					row.Type, csvFloat(row.DurationValue), row.DurationUnit, row.TopFrame,
				}); err != nil {
					return err
				}
			}
			return nil
		},
	); err != nil {
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

	return a.writeMarkdownSummary(histograms, missingRows)
}

func (a *analyzer) writeMarkdownSummary(
	histograms []histogramRow,
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
	builder.WriteString("\n## MXSignpost 与直方图\n\n")
	builder.WriteString("| 构建 | 设备 | 指标 | 样本 | P50 | P90 | P99 |\n")
	builder.WriteString("| --- | --- | --- | ---: | ---: | ---: | ---: |\n")
	for _, row := range histograms {
		builder.WriteString(fmt.Sprintf(
			"| %s | %s | %s | %d | %s | %s | %s |\n",
			escapeMarkdown(row.AppBuild),
			escapeMarkdown(row.DeviceClass),
			escapeMarkdown(row.MetricPath),
			row.Count,
			displayMeasurement(row.P50, row.Unit),
			displayMeasurement(row.P90, row.Unit),
			displayMeasurement(row.P99, row.Unit),
		))
	}
	if len(histograms) == 0 {
		builder.WriteString("| — | — | 暂无可解析直方图 | 0 | — | — | — |\n")
	}

	builder.WriteString("\n## 诊断事件\n\n")
	builder.WriteString("| 构建 | 类型 | 设备 | 时长 | 首个可用栈帧 |\n")
	builder.WriteString("| --- | --- | --- | ---: | --- |\n")
	for _, row := range a.diagnosticRows {
		builder.WriteString(fmt.Sprintf(
			"| %s | %s | %s | %s | %s |\n",
			escapeMarkdown(row.AppBuild),
			escapeMarkdown(row.Type),
			escapeMarkdown(row.DeviceClass),
			displayMeasurement(row.DurationValue, row.DurationUnit),
			escapeMarkdown(row.TopFrame),
		))
	}
	if len(a.diagnosticRows) == 0 {
		builder.WriteString("| — | 暂无诊断 | — | — | — |\n")
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

	builder.WriteString(
		"\n> 分位数使用 MetricKit 直方图桶上界近似；跨构建比较时请保持设备类型和单位一致。\n",
	)
	return os.WriteFile(
		filepath.Join(a.options.OutputDir, "summary.md"),
		[]byte(builder.String()),
		0o600,
	)
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
