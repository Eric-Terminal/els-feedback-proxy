package api

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestChecksumForAsset(t *testing.T) {
	checksums := "abc123  els-feedback-proxy_1.0.0_linux_amd64.tar.gz\ndef456  checksums.txt\n"
	checksum, err := checksumForAsset(checksums, "els-feedback-proxy_1.0.0_linux_amd64.tar.gz")
	if err != nil {
		t.Fatalf("解析 checksum 失败: %v", err)
	}
	if checksum != "abc123" {
		t.Fatalf("期望 checksum=abc123，实际=%s", checksum)
	}
}

func TestSelfUpdaterApplyReleaseReplacesBinaryAndWritesState(t *testing.T) {
	workdir := t.TempDir()
	binaryPath := filepath.Join(workdir, "els-feedback-proxy")
	if err := os.WriteFile(binaryPath, []byte("old-binary"), 0o755); err != nil {
		t.Fatalf("写入旧二进制失败: %v", err)
	}

	newBinary := []byte("new-binary")
	archiveBytes := makeTarGzArchive(t, "els-feedback-proxy", newBinary)
	checksum := sha256Hex(archiveBytes)

	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	defer server.Close()

	mux.HandleFunc("/repos/Eric-Terminal/els-feedback-proxy/releases/tags/v1.2.3", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(githubRelease{
			TagName: "v1.2.3",
			HTMLURL: "https://github.com/Eric-Terminal/els-feedback-proxy/releases/tag/v1.2.3",
			Assets: []githubReleaseAsset{
				{
					Name:               "checksums.txt",
					BrowserDownloadURL: server.URL + "/download/checksums",
				},
				{
					Name:               "els-feedback-proxy_1.2.3_linux_amd64.tar.gz",
					BrowserDownloadURL: server.URL + "/download/archive",
				},
			},
		})
	})
	mux.HandleFunc("/download/checksums", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(checksum + "  els-feedback-proxy_1.2.3_linux_amd64.tar.gz\n"))
	})
	mux.HandleFunc("/download/archive", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(archiveBytes)
	})

	restartCalled := false
	fixedNow := time.Date(2026, 4, 19, 2, 30, 0, 0, time.UTC)
	updater := &selfUpdateManager{
		secret:        "secret",
		repoOwner:     "Eric-Terminal",
		repoName:      "els-feedback-proxy",
		serviceName:   "els-feedback-proxy",
		workdir:       workdir,
		binaryName:    "els-feedback-proxy",
		stateFile:     filepath.Join(workdir, selfUpdateStateFileName),
		httpClient:    server.Client(),
		apiBaseURL:    server.URL,
		now:           func() time.Time { return fixedNow },
		runtimeGOOS:   "linux",
		runtimeGOARCH: "amd64",
		scheduleRestart: func(serviceName string, delay time.Duration) error {
			restartCalled = true
			if serviceName != "els-feedback-proxy" {
				t.Fatalf("重启服务名错误: %s", serviceName)
			}
			return nil
		},
	}

	result, err := updater.applyRelease(context.Background(), "v1.2.3", false)
	if err != nil {
		t.Fatalf("执行自动更新失败: %v", err)
	}
	if !restartCalled {
		t.Fatalf("期望调度服务重启")
	}
	if result.Tag != "v1.2.3" {
		t.Fatalf("期望 tag=v1.2.3，实际=%s", result.Tag)
	}
	if result.Checksum != checksum {
		t.Fatalf("期望 checksum=%s，实际=%s", checksum, result.Checksum)
	}

	currentBinary, err := os.ReadFile(binaryPath)
	if err != nil {
		t.Fatalf("读取当前二进制失败: %v", err)
	}
	if string(currentBinary) != string(newBinary) {
		t.Fatalf("当前二进制未替换为新内容")
	}

	backupBinary, err := os.ReadFile(result.BackupPath)
	if err != nil {
		t.Fatalf("读取备份二进制失败: %v", err)
	}
	if string(backupBinary) != "old-binary" {
		t.Fatalf("备份二进制内容错误")
	}

	state, err := updater.loadState()
	if err != nil {
		t.Fatalf("读取自动更新状态失败: %v", err)
	}
	if state.Tag != "v1.2.3" {
		t.Fatalf("状态文件中的 tag 错误: %s", state.Tag)
	}
	if state.AssetName != "els-feedback-proxy_1.2.3_linux_amd64.tar.gz" {
		t.Fatalf("状态文件中的资产名错误: %s", state.AssetName)
	}
}

func TestSelfUpdaterStartReleaseRunsInBackgroundAndTracksStatus(t *testing.T) {
	workdir := t.TempDir()
	binaryPath := filepath.Join(workdir, "els-feedback-proxy")
	if err := os.WriteFile(binaryPath, []byte("old-binary"), 0o755); err != nil {
		t.Fatalf("写入旧二进制失败: %v", err)
	}

	newBinary := []byte("new-binary")
	archiveBytes := makeTarGzArchive(t, "els-feedback-proxy", newBinary)
	checksum := sha256Hex(archiveBytes)
	archiveGate := make(chan struct{})

	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	defer server.Close()

	mux.HandleFunc("/repos/Eric-Terminal/els-feedback-proxy/releases/tags/v1.2.3", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(githubRelease{
			TagName: "v1.2.3",
			HTMLURL: "https://github.com/Eric-Terminal/els-feedback-proxy/releases/tag/v1.2.3",
			Assets: []githubReleaseAsset{
				{
					Name:               "checksums.txt",
					BrowserDownloadURL: server.URL + "/download/checksums",
				},
				{
					Name:               "els-feedback-proxy_1.2.3_linux_amd64.tar.gz",
					BrowserDownloadURL: server.URL + "/download/archive",
				},
			},
		})
	})
	mux.HandleFunc("/download/checksums", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(checksum + "  els-feedback-proxy_1.2.3_linux_amd64.tar.gz\n"))
	})
	mux.HandleFunc("/download/archive", func(w http.ResponseWriter, r *http.Request) {
		<-archiveGate
		_, _ = w.Write(archiveBytes)
	})

	restartCalled := false
	fixedNow := time.Date(2026, 4, 19, 3, 0, 0, 0, time.UTC)
	updater := &selfUpdateManager{
		secret:        "secret",
		repoOwner:     "Eric-Terminal",
		repoName:      "els-feedback-proxy",
		serviceName:   "els-feedback-proxy",
		workdir:       workdir,
		binaryName:    "els-feedback-proxy",
		stateFile:     filepath.Join(workdir, selfUpdateStateFileName),
		httpClient:    server.Client(),
		apiBaseURL:    server.URL,
		now:           func() time.Time { return fixedNow },
		runtimeGOOS:   "linux",
		runtimeGOARCH: "amd64",
		scheduleRestart: func(serviceName string, delay time.Duration) error {
			restartCalled = true
			return nil
		},
	}

	dispatch, err := updater.startRelease("1.2.3", false)
	if err != nil {
		t.Fatalf("启动后台自动更新失败: %v", err)
	}
	if dispatch.Tag != "v1.2.3" {
		t.Fatalf("期望受理 tag=v1.2.3，实际=%s", dispatch.Tag)
	}
	if dispatch.AcceptedAt != fixedNow {
		t.Fatalf("期望受理时间=%s，实际=%s", fixedNow, dispatch.AcceptedAt)
	}

	if _, err := updater.startRelease("1.2.4", false); !errors.Is(err, errSelfUpdateBusy) {
		t.Fatalf("期望第二次启动返回忙碌错误，实际=%v", err)
	}

	status := updater.statusSnapshot()
	if inProgress, ok := status["in_progress"].(bool); !ok || !inProgress {
		t.Fatalf("期望状态显示正在执行，实际=%v", status["in_progress"])
	}
	if currentTag, ok := status["current_tag"].(string); !ok || currentTag != "v1.2.3" {
		t.Fatalf("期望当前 tag=v1.2.3，实际=%v", status["current_tag"])
	}

	close(archiveGate)

	waitForCondition(t, 2*time.Second, func() bool {
		snapshot := updater.statusSnapshot()
		inProgress, _ := snapshot["in_progress"].(bool)
		return !inProgress
	})

	if !restartCalled {
		t.Fatalf("期望后台任务调度服务重启")
	}

	finalStatus := updater.statusSnapshot()
	if finalStatus["last_error"] != nil {
		t.Fatalf("期望没有错误，实际=%v", finalStatus["last_error"])
	}
	if _, ok := finalStatus["last_result"].(selfUpdateResult); !ok {
		t.Fatalf("期望记录最近一次更新结果")
	}

	currentBinary, err := os.ReadFile(binaryPath)
	if err != nil {
		t.Fatalf("读取当前二进制失败: %v", err)
	}
	if string(currentBinary) != string(newBinary) {
		t.Fatalf("后台任务未完成二进制替换")
	}
}

func makeTarGzArchive(t *testing.T, fileName string, content []byte) []byte {
	t.Helper()

	buffer := &bytes.Buffer{}
	gzipWriter := gzip.NewWriter(buffer)
	tarWriter := tar.NewWriter(gzipWriter)

	header := &tar.Header{
		Name: fileName,
		Mode: 0o755,
		Size: int64(len(content)),
	}
	if err := tarWriter.WriteHeader(header); err != nil {
		t.Fatalf("写入 tar header 失败: %v", err)
	}
	if _, err := tarWriter.Write(content); err != nil {
		t.Fatalf("写入 tar 内容失败: %v", err)
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatalf("关闭 tar writer 失败: %v", err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatalf("关闭 gzip writer 失败: %v", err)
	}
	return buffer.Bytes()
}

func waitForCondition(t *testing.T, timeout time.Duration, condition func() bool) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("等待条件成立超时: %s", timeout)
}
