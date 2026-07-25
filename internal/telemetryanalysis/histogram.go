package telemetryanalysis

import (
	"encoding/json"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var measurementPattern = regexp.MustCompile(
	`^\s*([-+]?(?:\d+(?:\.\d*)?|\.\d+)(?:[eE][-+]?\d+)?)\s*(.*?)\s*$`,
)

type parsedBucket struct {
	Upper float64
	Unit  string
	Count int64
}

func histogramBuckets(value any) ([]parsedBucket, bool) {
	items, ok := value.([]any)
	if !ok || len(items) == 0 {
		return nil, false
	}
	result := make([]parsedBucket, 0, len(items))
	for _, item := range items {
		bucket, ok := item.(map[string]any)
		if !ok {
			return nil, false
		}
		count, ok := integerCount(bucket["bucketCount"])
		if !ok || count < 0 {
			return nil, false
		}
		upper, unit, ok := parseMeasurement(bucket["bucketEnd"])
		if !ok {
			upper, unit, ok = parseMeasurement(bucket["bucketStart"])
		}
		if !ok {
			return nil, false
		}
		upper, unit = normalizeUnit(upper, unit)
		result = append(result, parsedBucket{Upper: upper, Unit: unit, Count: count})
	}
	return result, true
}

func parseMeasurement(value any) (float64, string, bool) {
	switch typed := value.(type) {
	case string:
		matches := measurementPattern.FindStringSubmatch(typed)
		if len(matches) != 3 {
			return 0, "", false
		}
		number, err := strconv.ParseFloat(matches[1], 64)
		return number, strings.TrimSpace(matches[2]), err == nil
	case map[string]any:
		number, ok := numberValue(typed["value"])
		if !ok {
			return 0, "", false
		}
		unit := stringValue(typed["unit"])
		if unit == "" {
			unit = stringValue(typed["unitDescription"])
		}
		return number, unit, true
	default:
		number, ok := numberValue(value)
		return number, "", ok
	}
}

func normalizeUnit(value float64, rawUnit string) (float64, string) {
	unit := strings.ToLower(strings.TrimSpace(rawUnit))
	switch unit {
	case "s", "sec", "secs", "second", "seconds":
		return value * 1_000, "ms"
	case "ms", "msec", "millisecond", "milliseconds":
		return value, "ms"
	case "us", "µs", "μs", "microsecond", "microseconds":
		return value / 1_000, "ms"
	case "ns", "nanosecond", "nanoseconds":
		return value / 1_000_000, "ms"
	case "kb", "kib":
		return value * 1_024, "bytes"
	case "mb", "mib":
		return value * 1_024 * 1_024, "bytes"
	case "gb", "gib":
		return value * 1_024 * 1_024 * 1_024, "bytes"
	case "byte", "bytes", "b":
		return value, "bytes"
	default:
		return value, rawUnit
	}
}

func integerCount(value any) (int64, bool) {
	switch typed := value.(type) {
	case json.Number:
		number, err := strconv.ParseInt(typed.String(), 10, 64)
		return number, err == nil
	case float64:
		if typed < 0 || typed != math.Trunc(typed) {
			return 0, false
		}
		return int64(typed), true
	case int:
		return int64(typed), true
	case int64:
		return typed, true
	default:
		return 0, false
	}
}

func (a *analyzer) finalizeHistograms() []histogramRow {
	rows := make([]histogramRow, 0, len(a.histograms))
	for _, accumulator := range a.histograms {
		sort.Slice(accumulator.Buckets, func(i, j int) bool {
			return accumulator.Buckets[i].Upper < accumulator.Buckets[j].Upper
		})
		count := int64(0)
		for _, bucket := range accumulator.Buckets {
			count += bucket.Count
		}
		rows = append(rows, histogramRow{
			histogramKey: accumulator.Key,
			Count:        count,
			P50:          weightedPercentile(accumulator.Buckets, count, 0.50),
			P90:          weightedPercentile(accumulator.Buckets, count, 0.90),
			P99:          weightedPercentile(accumulator.Buckets, count, 0.99),
		})
	}
	sort.Slice(rows, func(i, j int) bool {
		left := strings.Join([]string{
			rows[i].AppVersion,
			rows[i].AppBuild,
			rows[i].Distribution,
			rows[i].OSVersion,
			rows[i].DeviceClass,
			rows[i].MetricPath,
			rows[i].Unit,
		}, "|")
		right := strings.Join([]string{
			rows[j].AppVersion,
			rows[j].AppBuild,
			rows[j].Distribution,
			rows[j].OSVersion,
			rows[j].DeviceClass,
			rows[j].MetricPath,
			rows[j].Unit,
		}, "|")
		return left < right
	})
	return rows
}

func weightedPercentile(buckets []weightedBucket, total int64, percentile float64) float64 {
	if total <= 0 {
		return 0
	}
	target := int64(math.Ceil(float64(total) * percentile))
	seen := int64(0)
	for _, bucket := range buckets {
		seen += bucket.Count
		if seen >= target {
			return bucket.Upper
		}
	}
	return buckets[len(buckets)-1].Upper
}

func findDuration(value any) (float64, string) {
	switch typed := value.(type) {
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			if strings.Contains(strings.ToLower(key), "duration") {
				if number, unit, ok := parseMeasurement(typed[key]); ok {
					return normalizeUnit(number, unit)
				}
			}
		}
		for _, key := range keys {
			if number, unit := findDuration(typed[key]); number != 0 || unit != "" {
				return number, unit
			}
		}
	case []any:
		for _, child := range typed {
			if number, unit := findDuration(child); number != 0 || unit != "" {
				return number, unit
			}
		}
	}
	return 0, ""
}
