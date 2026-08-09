package telemetryanalysis

import (
	"sort"
	"strings"
)

// recognizedPayloadFields 是当前分析器已理解的 MetricKit 顶层类别。
// 新系统字段仍会保留在原始 JSON，并进入 unknown-fields.csv 提醒更新分析器。
var recognizedPayloadFields = map[string]struct{}{
	"_etos":                               {},
	"animationMetrics":                    {},
	"appLaunchDiagnostics":                {},
	"applicationExitMetrics":              {},
	"applicationLaunchMetrics":            {},
	"applicationTimeMetrics":              {},
	"cellularConditionMetrics":            {},
	"cpuExceptionDiagnostics":             {},
	"cpuMetrics":                          {},
	"crashDiagnostics":                    {},
	"diskIOMetrics":                       {},
	"diskWriteExceptionDiagnostics":       {},
	"displayMetrics":                      {},
	"gpuMetrics":                          {},
	"hangDiagnostics":                     {},
	"includesMultipleApplicationVersions": {},
	"latestApplicationVersion":            {},
	"locationActivityMetrics":             {},
	"memoryExceptionDiagnostics":          {},
	"memoryMetrics":                       {},
	"memoryResourceExceptionDiagnostics":  {},
	"networkTransferMetrics":              {},
	"responsivenessMetrics":               {},
	"signpostMetrics":                     {},
	"timeStampBegin":                      {},
	"timeStampEnd":                        {},
}

func (a *analyzer) collectUnknownTopLevelFields(payload map[string]any, row indexRow) {
	for field := range payload {
		if _, recognized := recognizedPayloadFields[field]; recognized {
			continue
		}
		key := unknownFieldKey{
			analysisDimensions: dimensionsFor(row),
			FieldPath:          "payload." + field,
		}
		a.unknownFields[key]++
	}
}

func (a *analyzer) finalizeSampleGroups() []sampleGroupRow {
	grouped := make(map[sampleGroupKey]*sampleGroupRow)
	for _, row := range a.indexRows {
		date := "unknown"
		if len(row.CapturedAt) >= 10 {
			date = row.CapturedAt[:10]
		}
		key := sampleGroupKey{
			Date:               date,
			analysisDimensions: dimensionsFor(row),
		}
		group := grouped[key]
		if group == nil {
			group = &sampleGroupRow{sampleGroupKey: key}
			grouped[key] = group
		}
		switch row.Kind {
		case "metric":
			group.MetricCount++
		case "diagnostic":
			group.DiagnosticCount++
		}
	}

	rows := make([]sampleGroupRow, 0, len(grouped))
	for _, row := range grouped {
		rows = append(rows, *row)
	}
	sort.Slice(rows, func(i, j int) bool {
		return sampleGroupSortKey(rows[i]) < sampleGroupSortKey(rows[j])
	})
	return rows
}

func (a *analyzer) finalizeUnknownFields() []unknownFieldRow {
	rows := make([]unknownFieldRow, 0, len(a.unknownFields))
	for key, count := range a.unknownFields {
		rows = append(rows, unknownFieldRow{
			unknownFieldKey: key,
			OccurrenceCount: count,
		})
	}
	sort.Slice(rows, func(i, j int) bool {
		return unknownFieldSortKey(rows[i]) < unknownFieldSortKey(rows[j])
	})
	return rows
}

func sampleGroupSortKey(row sampleGroupRow) string {
	return strings.Join([]string{
		row.Date,
		row.AppVersion,
		row.AppBuild,
		row.Distribution,
		row.OSVersion,
		row.DeviceClass,
	}, "|")
}

func unknownFieldSortKey(row unknownFieldRow) string {
	return strings.Join([]string{
		row.AppVersion,
		row.AppBuild,
		row.Distribution,
		row.OSVersion,
		row.DeviceClass,
		row.FieldPath,
	}, "|")
}
