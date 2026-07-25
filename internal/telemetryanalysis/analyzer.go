package telemetryanalysis

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var arrayIndexPattern = regexp.MustCompile(`\[\d+\]`)

type inputFileError struct {
	err error
}

func (e *inputFileError) Error() string {
	return e.err.Error()
}

func (e *inputFileError) Unwrap() error {
	return e.err
}

type analyzer struct {
	options          Options
	symbolicator     frameSymbolicator
	indexRows        []indexRow
	diagnosticRows   []diagnosticRow
	diagnosticStacks []diagnosticStackRow
	histograms       map[histogramKey]*histogramAccumulator
	measurements     map[measurementKey]*measurementAccumulator
	signposts        map[signpostKey]*signpostAccumulator
	unknownFields    map[unknownFieldKey]int
	missing          map[string]*missingSymbolRow
	parseErrors      []parseErrorRow
	symbolicated     int
	metricCount      int
	diagnosticCount  int
}

// Analyze 扫描长期归档并生成索引、分位数、诊断和符号化报告。
func Analyze(options Options) (Result, error) {
	if strings.TrimSpace(options.InputDir) == "" {
		return Result{}, errors.New("缺少遥测输入目录")
	}
	if strings.TrimSpace(options.OutputDir) == "" {
		return Result{}, errors.New("缺少分析输出目录")
	}
	inputPath, err := filepath.Abs(options.InputDir)
	if err != nil {
		return Result{}, fmt.Errorf("解析遥测输入目录失败: %w", err)
	}
	outputPath, err := filepath.Abs(options.OutputDir)
	if err != nil {
		return Result{}, fmt.Errorf("解析分析输出目录失败: %w", err)
	}
	inputInfo, err := os.Stat(inputPath)
	if err != nil || !inputInfo.IsDir() {
		return Result{}, errors.New("遥测输入目录不存在或不是目录")
	}
	if outputPath == inputPath ||
		strings.HasPrefix(outputPath, inputPath+string(filepath.Separator)) {
		return Result{}, errors.New("分析输出目录不能位于原始遥测输入目录内部")
	}
	options.InputDir = inputPath
	options.OutputDir = outputPath
	if err := os.MkdirAll(options.OutputDir, 0o755); err != nil {
		return Result{}, fmt.Errorf("创建分析输出目录失败: %w", err)
	}

	catalog, err := newSymbolCatalog(options.SymbolPaths, options.UseSpotlight)
	if err != nil {
		return Result{}, err
	}
	instance := &analyzer{
		options:       options,
		symbolicator:  catalog,
		histograms:    make(map[histogramKey]*histogramAccumulator),
		measurements:  make(map[measurementKey]*measurementAccumulator),
		signposts:     make(map[signpostKey]*signpostAccumulator),
		unknownFields: make(map[unknownFieldKey]int),
		missing:       make(map[string]*missingSymbolRow),
	}
	if err := instance.scan(); err != nil {
		return Result{}, err
	}
	histogramRows := instance.finalizeHistograms()
	measurementRows := instance.finalizeMeasurements()
	signpostRows := instance.finalizeSignposts()
	sampleGroupRows := instance.finalizeSampleGroups()
	unknownFieldRows := instance.finalizeUnknownFields()
	if err := instance.writeReports(
		histogramRows,
		measurementRows,
		signpostRows,
		sampleGroupRows,
		unknownFieldRows,
	); err != nil {
		return Result{}, err
	}
	return Result{
		OutputDir:         options.OutputDir,
		FileCount:         len(instance.indexRows),
		MetricCount:       instance.metricCount,
		DiagnosticCount:   instance.diagnosticCount,
		HistogramCount:    len(histogramRows),
		MeasurementCount:  len(measurementRows),
		SignpostCount:     len(signpostRows),
		SampleGroupCount:  len(sampleGroupRows),
		UnknownFieldCount: len(unknownFieldRows),
		Symbolicated:      instance.symbolicated,
		MissingSymbols:    len(instance.missing),
		ParseErrorCount:   len(instance.parseErrors),
	}, nil
}

func (a *analyzer) scan() error {
	symbolicatedRoot := filepath.Join(a.options.OutputDir, "symbolicated")
	if err := os.MkdirAll(symbolicatedRoot, 0o755); err != nil {
		return fmt.Errorf("创建符号化目录失败: %w", err)
	}

	return filepath.WalkDir(a.options.InputDir, func(
		path string,
		entry fs.DirEntry,
		walkErr error,
	) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || strings.ToLower(filepath.Ext(path)) != ".json" {
			return nil
		}
		if err := a.processFile(path, symbolicatedRoot); err != nil {
			var inputErr *inputFileError
			if errors.As(err, &inputErr) {
				a.parseErrors = append(a.parseErrors, parseErrorRow{
					SourcePath: path,
					Error:      inputErr.Error(),
				})
				return nil
			}
			return fmt.Errorf("分析 %s 失败: %w", path, err)
		}
		return nil
	})
}

func (a *analyzer) processFile(path, symbolicatedRoot string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return &inputFileError{err: fmt.Errorf("读取原始文件失败: %w", err)}
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var root map[string]any
	if err := decoder.Decode(&root); err != nil {
		return &inputFileError{err: fmt.Errorf("原始文件不是有效 JSON: %w", err)}
	}
	if stringValue(root["schema_version"]) != "1" {
		return &inputFileError{err: errors.New("不支持的 envelope schema_version")}
	}
	payload, ok := root["payload"].(map[string]any)
	if !ok {
		return &inputFileError{err: errors.New("envelope.payload 不是 JSON 对象")}
	}

	app := nestedMap(root, "app")
	platform := nestedMap(root, "platform")
	row := indexRow{
		PayloadID:    stringValue(root["payload_id"]),
		Kind:         stringValue(root["kind"]),
		CapturedAt:   stringValue(root["captured_at"]),
		PeriodStart:  stringValue(root["period_start"]),
		PeriodEnd:    stringValue(root["period_end"]),
		AppVersion:   stringValue(app["version"]),
		AppBuild:     stringValue(app["build"]),
		Distribution: stringValue(app["distribution"]),
		OSVersion:    stringValue(platform["os_version"]),
		DeviceClass:  stringValue(platform["device_class"]),
		Architecture: stringValue(platform["architecture"]),
		SourcePath:   path,
		SizeBytes:    int64(len(data)),
	}
	a.indexRows = append(a.indexRows, row)
	if row.Kind == "metric" {
		a.metricCount++
	} else if row.Kind == "diagnostic" {
		a.diagnosticCount++
	}

	binaryInfo := collectBinaryInfo(payload)
	a.symbolicateFrames(payload, row, binaryInfo)
	a.collectHistograms(payload, "payload", row, nil)
	a.collectMeasurements(payload, "payload", row)
	a.collectSignposts(payload, row)
	a.collectDiagnostics(payload, row)
	a.collectUnknownTopLevelFields(payload, row)

	relativePath, err := filepath.Rel(a.options.InputDir, path)
	if err != nil {
		return err
	}
	target := filepath.Join(symbolicatedRoot, relativePath)
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	output, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(target, append(output, '\n'), 0o600)
}

func (a *analyzer) symbolicateFrames(
	value any,
	row indexRow,
	binaryInfo map[string]string,
) {
	switch typed := value.(type) {
	case map[string]any:
		uuid := normalizeUUID(stringValue(typed["binaryUUID"]))
		offset, hasOffset := uintValue(typed["offsetIntoBinaryTextSegment"])
		address, hasAddress := uintValue(typed["address"])
		if uuid != "" && (hasOffset || hasAddress) {
			binaryName := stringValue(typed["binaryName"])
			if binaryName == "" {
				binaryName = binaryInfo[uuid]
			}
			result := a.symbolicator.Symbolicate(
				uuid,
				binaryName,
				row.Architecture,
				address,
				offset,
			)
			typed["symbolicationStatus"] = result.Status
			if result.Symbol != "" {
				typed["symbolicated"] = result.Symbol
				typed["dSYMPath"] = result.DSYMPath
				a.symbolicated++
			} else {
				a.recordMissing(uuid, binaryName, row.Architecture, result.Status)
			}
		}
		for _, child := range typed {
			a.symbolicateFrames(child, row, binaryInfo)
		}
	case []any:
		for _, child := range typed {
			a.symbolicateFrames(child, row, binaryInfo)
		}
	}
}

func (a *analyzer) collectHistograms(
	value any,
	path string,
	row indexRow,
	signpost map[string]any,
) {
	switch typed := value.(type) {
	case map[string]any:
		currentSignpost := signpost
		if name := stringValue(typed["signpostName"]); name != "" {
			currentSignpost = typed
		}
		if buckets, ok := histogramBuckets(typed["histogram"]); ok {
			metricPath := normalizeMetricPath(path)
			if currentSignpost != nil {
				category := stringValue(currentSignpost["signpostCategory"])
				name := stringValue(currentSignpost["signpostName"])
				metricPath = "signpost." + category + "." + name + ".duration"
			}
			a.addHistogram(row, metricPath, buckets)
		}
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			a.collectHistograms(typed[key], path+"."+key, row, currentSignpost)
		}
	case []any:
		for index, child := range typed {
			a.collectHistograms(child, fmt.Sprintf("%s[%d]", path, index), row, signpost)
		}
	}
}

func (a *analyzer) addHistogram(
	row indexRow,
	metricPath string,
	buckets []parsedBucket,
) {
	for _, bucket := range buckets {
		key := histogramKey{
			analysisDimensions: dimensionsFor(row),
			MetricPath:         metricPath,
			Unit:               bucket.Unit,
		}
		accumulator := a.histograms[key]
		if accumulator == nil {
			accumulator = &histogramAccumulator{Key: key}
			a.histograms[key] = accumulator
		}
		accumulator.Buckets = append(accumulator.Buckets, weightedBucket{
			Upper: bucket.Upper,
			Count: bucket.Count,
		})
	}
}

func (a *analyzer) collectDiagnostics(payload map[string]any, row indexRow) {
	diagnosticKeys := []string{
		"crashDiagnostics",
		"hangDiagnostics",
		"cpuExceptionDiagnostics",
		"diskWriteExceptionDiagnostics",
		"appLaunchDiagnostics",
		"memoryExceptionDiagnostics",
	}
	for _, key := range diagnosticKeys {
		items, _ := payload[key].([]any)
		for _, item := range items {
			diagnostic, _ := item.(map[string]any)
			duration, unit := findDuration(diagnostic)
			a.diagnosticRows = append(a.diagnosticRows, diagnosticRow{
				PayloadID:     row.PayloadID,
				AppVersion:    row.AppVersion,
				AppBuild:      row.AppBuild,
				Distribution:  row.Distribution,
				OSVersion:     row.OSVersion,
				DeviceClass:   row.DeviceClass,
				Type:          key,
				DurationValue: duration,
				DurationUnit:  unit,
				TopFrame:      firstUsefulFrame(diagnostic),
			})
			a.diagnosticStacks = append(a.diagnosticStacks, diagnosticStackRow{
				PayloadID:    row.PayloadID,
				AppBuild:     row.AppBuild,
				Distribution: row.Distribution,
				OSVersion:    row.OSVersion,
				DeviceClass:  row.DeviceClass,
				Type:         key,
				Frames:       collectUsefulFrames(diagnostic),
			})
		}
	}
}

func (a *analyzer) recordMissing(uuid, binaryName, arch, status string) {
	key := strings.Join([]string{uuid, binaryName, arch, status}, "|")
	row := a.missing[key]
	if row == nil {
		row = &missingSymbolRow{
			UUID:       uuid,
			BinaryName: binaryName,
			Arch:       arch,
			Status:     status,
		}
		a.missing[key] = row
	}
	row.Count++
}

func collectBinaryInfo(value any) map[string]string {
	result := make(map[string]string)
	var walk func(any)
	walk = func(current any) {
		switch typed := current.(type) {
		case map[string]any:
			if info, ok := typed["binaryInfo"].(map[string]any); ok {
				for rawUUID, rawMetadata := range info {
					metadata, _ := rawMetadata.(map[string]any)
					name := stringValue(metadata["binaryName"])
					if name == "" {
						name = stringValue(metadata["name"])
					}
					result[normalizeUUID(rawUUID)] = name
				}
			}
			for _, child := range typed {
				walk(child)
			}
		case []any:
			for _, child := range typed {
				walk(child)
			}
		}
	}
	walk(value)
	return result
}

func firstUsefulFrame(value any) string {
	if symbol := firstSymbolicatedFrame(value); symbol != "" {
		return symbol
	}
	return firstRawFrame(value)
}

func firstSymbolicatedFrame(value any) string {
	switch typed := value.(type) {
	case map[string]any:
		if symbol := stringValue(typed["symbolicated"]); symbol != "" {
			return symbol
		}
		for _, key := range sortedMapKeys(typed) {
			if frame := firstSymbolicatedFrame(typed[key]); frame != "" {
				return frame
			}
		}
	case []any:
		for _, child := range typed {
			if frame := firstSymbolicatedFrame(child); frame != "" {
				return frame
			}
		}
	}
	return ""
}

func firstRawFrame(value any) string {
	switch typed := value.(type) {
	case map[string]any:
		if binaryName := stringValue(typed["binaryName"]); binaryName != "" {
			if offset, ok := uintValue(typed["offsetIntoBinaryTextSegment"]); ok {
				return fmt.Sprintf("%s + 0x%x", binaryName, offset)
			}
		}
		for _, key := range sortedMapKeys(typed) {
			if frame := firstRawFrame(typed[key]); frame != "" {
				return frame
			}
		}
	case []any:
		for _, child := range typed {
			if frame := firstRawFrame(child); frame != "" {
				return frame
			}
		}
	}
	return ""
}

func normalizeMetricPath(path string) string {
	return arrayIndexPattern.ReplaceAllString(path, "[]")
}

func collectUsefulFrames(value any) []string {
	result := make([]string, 0)
	var walk func(any)
	walk = func(current any) {
		switch typed := current.(type) {
		case map[string]any:
			if symbol := stringValue(typed["symbolicated"]); symbol != "" {
				result = append(result, symbol)
			} else if binaryName := stringValue(typed["binaryName"]); binaryName != "" {
				if offset, ok := uintValue(typed["offsetIntoBinaryTextSegment"]); ok {
					result = append(result, fmt.Sprintf("%s + 0x%x", binaryName, offset))
				}
			}
			for _, key := range sortedMapKeys(typed) {
				walk(typed[key])
			}
		case []any:
			for _, child := range typed {
				walk(child)
			}
		}
	}
	walk(value)
	return result
}

func sortedMapKeys(value map[string]any) []string {
	keys := make([]string, 0, len(value))
	for key := range value {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
