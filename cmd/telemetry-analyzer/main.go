package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"els-feedback-proxy/internal/telemetryanalysis"
)

const defaultArchiveRoot = "."

type repeatedPaths []string

func (paths *repeatedPaths) String() string {
	return strings.Join(*paths, ",")
}

func (paths *repeatedPaths) Set(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return errors.New("符号路径不能为空")
	}
	*paths = append(*paths, value)
	return nil
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "遥测分析失败: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	flags := flag.NewFlagSet("telemetry-analyzer", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	archiveRoot := flags.String("archive-root", defaultArchiveRoot, "遥测长期归档根目录（默认当前工作目录）")
	inputDir := flags.String("input", "", "原始遥测目录（默认 <archive-root>/raw）")
	outputDir := flags.String("output", "", "分析输出目录（默认按 UTC 时间创建）")
	useSpotlight := flags.Bool("spotlight", true, "缺少 UUID 时使用 mdfind 查找匹配 dSYM")
	var symbolPaths repeatedPaths
	flags.Var(&symbolPaths, "xcarchive", "Xcode Cloud 下载的 .xcarchive；可以重复")
	flags.Var(&symbolPaths, "dsym", "单个 .dSYM 或包含 dSYM 的目录；可以重复")
	flags.Usage = func() {
		fmt.Fprintln(flags.Output(), `用法:
  go run ./cmd/telemetry-analyzer [选项]

选项:
  --archive-root PATH   长期归档根目录（默认当前工作目录）
  --input PATH          原始 JSON 目录
  --output PATH         本次分析输出目录
  --xcarchive PATH      匹配版本的 .xcarchive，可重复
  --dsym PATH           .dSYM 或其上级目录，可重复
  --spotlight=false     禁止使用 Spotlight 自动查找 UUID

输出包含 raw-index.csv、histograms.csv、measurements.csv、signposts.csv、
diagnostics.csv、diagnostic-stacks.md、parse-errors.csv、missing-symbols.csv、
summary.md 和逐文件 symbolicated/ JSON。`)
	}
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("存在无法识别的参数: %s", strings.Join(flags.Args(), " "))
	}

	if strings.TrimSpace(*inputDir) == "" {
		*inputDir = filepath.Join(*archiveRoot, "raw")
	}
	if strings.TrimSpace(*outputDir) == "" {
		runName := time.Now().UTC().Format("2006-01-02T150405Z")
		*outputDir = filepath.Join(*archiveRoot, "analysis", runName)
	}
	result, err := telemetryanalysis.Analyze(telemetryanalysis.Options{
		InputDir:     *inputDir,
		OutputDir:    *outputDir,
		SymbolPaths:  symbolPaths,
		UseSpotlight: *useSpotlight,
	})
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	encoder.SetEscapeHTML(false)
	return encoder.Encode(result)
}
