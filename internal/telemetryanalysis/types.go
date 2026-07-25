package telemetryanalysis

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// Options 描述一次只读分析的输入、输出和符号来源。
type Options struct {
	InputDir     string
	OutputDir    string
	SymbolPaths  []string
	UseSpotlight bool
}

// Result 是便于脚本继续校验的分析结果摘要。
type Result struct {
	OutputDir        string `json:"output_dir"`
	FileCount        int    `json:"file_count"`
	MetricCount      int    `json:"metric_count"`
	DiagnosticCount  int    `json:"diagnostic_count"`
	HistogramCount   int    `json:"histogram_count"`
	MeasurementCount int    `json:"measurement_count"`
	SignpostCount    int    `json:"signpost_count"`
	Symbolicated     int    `json:"symbolicated_frames"`
	MissingSymbols   int    `json:"missing_symbol_uuids"`
	ParseErrorCount  int    `json:"parse_error_count"`
}

type indexRow struct {
	PayloadID    string
	Kind         string
	CapturedAt   string
	PeriodStart  string
	PeriodEnd    string
	AppVersion   string
	AppBuild     string
	Distribution string
	OSVersion    string
	DeviceClass  string
	Architecture string
	SourcePath   string
	SizeBytes    int64
}

type diagnosticRow struct {
	PayloadID     string
	AppVersion    string
	AppBuild      string
	Distribution  string
	OSVersion     string
	DeviceClass   string
	Type          string
	DurationValue float64
	DurationUnit  string
	TopFrame      string
}

type analysisDimensions struct {
	AppVersion   string
	AppBuild     string
	Distribution string
	OSVersion    string
	DeviceClass  string
}

type histogramKey struct {
	analysisDimensions
	MetricPath string
	Unit       string
}

type weightedBucket struct {
	Upper float64
	Count int64
}

type histogramAccumulator struct {
	Key     histogramKey
	Buckets []weightedBucket
}

type histogramRow struct {
	histogramKey
	Count int64
	P50   float64
	P90   float64
	P99   float64
}

type measurementKey struct {
	analysisDimensions
	MetricPath string
	Unit       string
}

type measurementAccumulator struct {
	Key    measurementKey
	Values []float64
}

type measurementRow struct {
	measurementKey
	Count   int64
	Total   float64
	Average float64
	Minimum float64
	Maximum float64
}

type signpostKey struct {
	analysisDimensions
	Category string
	Name     string
}

type signpostAccumulator struct {
	Key        signpostKey
	EntryCount int64
	TotalCount int64
}

type signpostRow struct {
	signpostKey
	EntryCount int64
	TotalCount int64
}

type diagnosticStackRow struct {
	PayloadID    string
	AppBuild     string
	Distribution string
	OSVersion    string
	DeviceClass  string
	Type         string
	Frames       []string
}

type parseErrorRow struct {
	SourcePath string
	Error      string
}

type missingSymbolRow struct {
	UUID       string
	BinaryName string
	Arch       string
	Status     string
	Count      int
}

type symbolicationResult struct {
	Symbol   string
	Status   string
	DSYMPath string
}

type frameSymbolicator interface {
	Symbolicate(
		uuid string,
		binaryName string,
		architecture string,
		address uint64,
		offset uint64,
	) symbolicationResult
}

func dimensionsFor(row indexRow) analysisDimensions {
	return analysisDimensions{
		AppVersion:   row.AppVersion,
		AppBuild:     row.AppBuild,
		Distribution: row.Distribution,
		OSVersion:    row.OSVersion,
		DeviceClass:  row.DeviceClass,
	}
}

func stringValue(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case json.Number:
		return typed.String()
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case int:
		return strconv.Itoa(typed)
	default:
		return ""
	}
}

func numberValue(value any) (float64, bool) {
	switch typed := value.(type) {
	case json.Number:
		number, err := typed.Float64()
		return number, err == nil
	case float64:
		return typed, true
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	default:
		return 0, false
	}
}

func uintValue(value any) (uint64, bool) {
	switch typed := value.(type) {
	case json.Number:
		number, err := strconv.ParseUint(typed.String(), 10, 64)
		return number, err == nil
	case float64:
		if typed < 0 || typed != float64(uint64(typed)) {
			return 0, false
		}
		return uint64(typed), true
	case string:
		cleaned := strings.TrimSpace(typed)
		base := 10
		if strings.HasPrefix(strings.ToLower(cleaned), "0x") {
			base = 16
			cleaned = cleaned[2:]
		}
		number, err := strconv.ParseUint(cleaned, base, 64)
		return number, err == nil
	default:
		return 0, false
	}
}

func nestedMap(root map[string]any, key string) map[string]any {
	value, _ := root[key].(map[string]any)
	return value
}

func csvFloat(value float64) string {
	return strconv.FormatFloat(value, 'f', 3, 64)
}

func displayMeasurement(value float64, unit string) string {
	if unit == "" {
		return csvFloat(value)
	}
	return fmt.Sprintf("%.3f %s", value, unit)
}
