package telemetryanalysis

import (
	"math"
	"sort"
	"strconv"
	"strings"
)

// collectMeasurements 提取 MetricKit 的显式测量值，原始 JSON 仍由符号化副本完整保留。
func (a *analyzer) collectMeasurements(value any, path string, row indexRow) {
	switch typed := value.(type) {
	case map[string]any:
		if number, unit, ok := explicitMeasurement(typed); ok {
			a.addMeasurement(row, normalizeMetricPath(path), number, unit)
			return
		}
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			a.collectMeasurements(typed[key], path+"."+key, row)
		}
	case []any:
		for index, child := range typed {
			a.collectMeasurements(child, path+"["+strconv.Itoa(index)+"]", row)
		}
	case string:
		number, unit, ok := parseMeasurement(typed)
		if ok && supportedStringMeasurementUnit(unit) {
			a.addMeasurement(row, normalizeMetricPath(path), number, unit)
		}
	}
}

func explicitMeasurement(value map[string]any) (float64, string, bool) {
	if _, exists := value["value"]; !exists {
		return 0, "", false
	}
	number, unit, ok := parseMeasurement(value)
	if !ok || strings.TrimSpace(unit) == "" {
		return 0, "", false
	}
	return number, unit, true
}

func supportedStringMeasurementUnit(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "ns", "nanosecond", "nanoseconds",
		"us", "µs", "μs", "microsecond", "microseconds",
		"ms", "msec", "millisecond", "milliseconds",
		"s", "sec", "secs", "second", "seconds",
		"b", "byte", "bytes", "kb", "kib", "mb", "mib", "gb", "gib",
		"hz", "khz", "mhz", "%", "percent", "percentage",
		"j", "joule", "joules", "w", "watt", "watts", "count", "counts":
		return true
	default:
		return false
	}
}

func (a *analyzer) addMeasurement(
	row indexRow,
	metricPath string,
	value float64,
	unit string,
) {
	value, unit = normalizeUnit(value, unit)
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return
	}
	key := measurementKey{
		analysisDimensions: dimensionsFor(row),
		MetricPath:         metricPath,
		Unit:               unit,
	}
	accumulator := a.measurements[key]
	if accumulator == nil {
		accumulator = &measurementAccumulator{Key: key}
		a.measurements[key] = accumulator
	}
	accumulator.Values = append(accumulator.Values, value)
}

func (a *analyzer) finalizeMeasurements() []measurementRow {
	rows := make([]measurementRow, 0, len(a.measurements))
	for _, accumulator := range a.measurements {
		if len(accumulator.Values) == 0 {
			continue
		}
		row := measurementRow{
			measurementKey: accumulator.Key,
			Count:          int64(len(accumulator.Values)),
			Minimum:        accumulator.Values[0],
			Maximum:        accumulator.Values[0],
		}
		for _, value := range accumulator.Values {
			row.Total += value
			row.Minimum = math.Min(row.Minimum, value)
			row.Maximum = math.Max(row.Maximum, value)
		}
		row.Average = row.Total / float64(row.Count)
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool {
		return measurementSortKey(rows[i]) < measurementSortKey(rows[j])
	})
	return rows
}

func measurementSortKey(row measurementRow) string {
	return strings.Join([]string{
		row.AppVersion,
		row.AppBuild,
		row.Distribution,
		row.OSVersion,
		row.DeviceClass,
		row.MetricPath,
		row.Unit,
	}, "|")
}

// collectSignposts 单独汇总 totalCount，便于识别高频但单次很短的重复工作。
func (a *analyzer) collectSignposts(value any, row indexRow) {
	switch typed := value.(type) {
	case map[string]any:
		name := stringValue(typed["signpostName"])
		totalCount, hasCount := integerCount(typed["totalCount"])
		if name != "" && hasCount && totalCount >= 0 {
			key := signpostKey{
				analysisDimensions: dimensionsFor(row),
				Category:           stringValue(typed["signpostCategory"]),
				Name:               name,
			}
			accumulator := a.signposts[key]
			if accumulator == nil {
				accumulator = &signpostAccumulator{Key: key}
				a.signposts[key] = accumulator
			}
			accumulator.EntryCount++
			accumulator.TotalCount += totalCount
		}
		for _, child := range typed {
			a.collectSignposts(child, row)
		}
	case []any:
		for _, child := range typed {
			a.collectSignposts(child, row)
		}
	}
}

func (a *analyzer) finalizeSignposts() []signpostRow {
	rows := make([]signpostRow, 0, len(a.signposts))
	for _, accumulator := range a.signposts {
		rows = append(rows, signpostRow{
			signpostKey: accumulator.Key,
			EntryCount:  accumulator.EntryCount,
			TotalCount:  accumulator.TotalCount,
		})
	}
	sort.Slice(rows, func(i, j int) bool {
		return signpostSortKey(rows[i]) < signpostSortKey(rows[j])
	})
	return rows
}

func signpostSortKey(row signpostRow) string {
	return strings.Join([]string{
		row.AppVersion,
		row.AppBuild,
		row.Distribution,
		row.OSVersion,
		row.DeviceClass,
		row.Category,
		row.Name,
	}, "|")
}
