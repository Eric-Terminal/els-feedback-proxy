package guide

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"os"
	"testing"
)

type fakeSourceArchiveGateway struct {
	calls   int
	archive []byte
}

func (g *fakeSourceArchiveGateway) DownloadSourceArchive(context.Context, string) (io.ReadCloser, error) {
	g.calls++
	return io.NopCloser(bytes.NewReader(g.archive)), nil
}

func TestSourcePackFiltersAndCachesImmutableCommit(t *testing.T) {
	sha := "0123456789abcdef0123456789abcdef01234567"
	gateway := &fakeSourceArchiveGateway{archive: makeSourceTarGzip(t, map[string][]byte{
		"repo-root/ETOSCore/Guide.swift": []byte("let guide = true\n"),
		"repo-root/README.md":            []byte("# ETOS\n"),
		"repo-root/Assets/icon.png":      {0x89, 0x50, 0x4e, 0x47},
	})}
	service := NewSourcePackService(gateway, "Eric-Terminal", "ETOS-LLM-Studio", t.TempDir())
	first, err := service.Load(context.Background(), sha)
	if err != nil {
		t.Fatalf("生成源码包失败: %v", err)
	}
	second, err := service.Load(context.Background(), sha)
	if err != nil {
		t.Fatalf("读取源码包缓存失败: %v", err)
	}
	if gateway.calls != 1 || first.Path != second.Path || first.Size <= 0 {
		t.Fatalf("不可变源码包未复用: calls=%d first=%+v second=%+v", gateway.calls, first, second)
	}

	archive, err := zip.OpenReader(first.Path)
	if err != nil {
		t.Fatalf("打开生成的源码包失败: %v", err)
	}
	defer archive.Close()
	var manifest SourcePackManifest
	names := make(map[string]bool)
	for _, entry := range archive.File {
		names[entry.Name] = true
		if entry.Name == "manifest.json" {
			stream, openErr := entry.Open()
			if openErr != nil {
				t.Fatalf("打开源码包清单失败: %v", openErr)
			}
			data, readErr := io.ReadAll(stream)
			_ = stream.Close()
			if readErr != nil || json.Unmarshal(data, &manifest) != nil {
				t.Fatalf("解析源码包清单失败: %v", readErr)
			}
		}
	}
	if !names["sources/ETOSCore/Guide.swift"] || !names["sources/README.md"] || names["sources/Assets/icon.png"] {
		t.Fatalf("源码包过滤结果错误: %#v", names)
	}
	if manifest.CommitSHA != sha || len(manifest.Files) != 2 {
		t.Fatalf("源码包清单错误: %+v", manifest)
	}
}

func TestSourcePackRejectsShortCommit(t *testing.T) {
	service := NewSourcePackService(&fakeSourceArchiveGateway{}, "Eric-Terminal", "ETOS-LLM-Studio", t.TempDir())
	if _, err := service.Load(context.Background(), "0123456"); err != ErrInvalidCommitSHA {
		t.Fatalf("短哈希应被拒绝，实际错误: %v", err)
	}
}

func makeSourceTarGzip(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var buffer bytes.Buffer
	gzipWriter := gzip.NewWriter(&buffer)
	tarWriter := tar.NewWriter(gzipWriter)
	for name, data := range files {
		if err := tarWriter.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(data)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatalf("写入测试 tar 头失败: %v", err)
		}
		if _, err := tarWriter.Write(data); err != nil {
			t.Fatalf("写入测试 tar 内容失败: %v", err)
		}
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatalf("关闭测试 tar 失败: %v", err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatalf("关闭测试 gzip 失败: %v", err)
	}
	return buffer.Bytes()
}

func TestSourcePackCacheSurvivesServiceRestart(t *testing.T) {
	sha := "0123456789abcdef0123456789abcdef01234567"
	gateway := &fakeSourceArchiveGateway{archive: makeSourceTarGzip(t, map[string][]byte{
		"repo-root/README.md": []byte("# ETOS\n"),
	})}
	dataDir := t.TempDir()
	first := NewSourcePackService(gateway, "Eric-Terminal", "ETOS-LLM-Studio", dataDir)
	if _, err := first.Load(context.Background(), sha); err != nil {
		t.Fatalf("首次生成源码包失败: %v", err)
	}
	second := NewSourcePackService(gateway, "Eric-Terminal", "ETOS-LLM-Studio", dataDir)
	if _, err := second.Load(context.Background(), sha); err != nil {
		t.Fatalf("服务重启后读取源码包失败: %v", err)
	}
	if gateway.calls != 1 {
		t.Fatalf("服务重启后不应重复下载 GitHub 归档，实际 %d 次", gateway.calls)
	}
	if _, err := os.Stat(second.cachePath(sha)); err != nil {
		t.Fatalf("源码包缓存不存在: %v", err)
	}
}
