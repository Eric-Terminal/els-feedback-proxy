package api

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"els-feedback-proxy/internal/buildinfo"
	"els-feedback-proxy/internal/config"
)

const (
	selfUpdateStateFileName = ".self-update-state.json"
	selfUpdateAPIBaseURL    = "https://api.github.com"
	selfUpdateJobTimeout    = 10 * time.Minute
)

var errSelfUpdateBusy = errors.New("自动更新正在进行中")

type selfUpdateRequest struct {
	Tag   string `json:"tag"`
	Force bool   `json:"force"`
}

type selfUpdateResult struct {
	Tag              string    `json:"tag"`
	AssetName        string    `json:"asset_name"`
	ReleaseURL       string    `json:"release_url"`
	Checksum         string    `json:"checksum"`
	BackupPath       string    `json:"backup_path"`
	RestartScheduled bool      `json:"restart_scheduled"`
	AlreadyCurrent   bool      `json:"already_current"`
	UpdatedAt        time.Time `json:"updated_at"`
}

type selfUpdateDispatchResult struct {
	Tag        string    `json:"tag"`
	Force      bool      `json:"force"`
	AcceptedAt time.Time `json:"accepted_at"`
}

type selfUpdateState struct {
	Tag         string    `json:"tag"`
	AssetName   string    `json:"asset_name"`
	ReleaseURL  string    `json:"release_url"`
	Checksum    string    `json:"checksum"`
	BackupPath  string    `json:"backup_path"`
	UpdatedAt   time.Time `json:"updated_at"`
	BinaryPath  string    `json:"binary_path"`
	ArchivePath string    `json:"archive_path"`
}

type githubReleaseAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

type githubRelease struct {
	TagName string               `json:"tag_name"`
	HTMLURL string               `json:"html_url"`
	Assets  []githubReleaseAsset `json:"assets"`
}

type selfUpdateManager struct {
	mu              sync.Mutex
	inProgress      bool
	currentTag      string
	lastRequestedAt time.Time
	lastCompletedAt time.Time
	lastError       string
	lastResult      *selfUpdateResult
	secret          string
	repoOwner       string
	repoName        string
	githubToken     string
	serviceName     string
	workdir         string
	binaryName      string
	stateFile       string
	httpClient      *http.Client
	apiBaseURL      string
	now             func() time.Time
	runtimeGOOS     string
	runtimeGOARCH   string
	scheduleRestart func(serviceName string, delay time.Duration) error
}

func newSelfUpdateManager(cfg config.Config) *selfUpdateManager {
	secret := strings.TrimSpace(cfg.SelfUpdateSecret)
	if secret == "" {
		return nil
	}

	workdir := strings.TrimSpace(cfg.SelfUpdateWorkingDir)
	if workdir == "" {
		if executablePath, err := os.Executable(); err == nil {
			workdir = filepath.Dir(executablePath)
		}
	}
	if workdir == "" {
		workdir = "."
	}

	serviceName := strings.TrimSpace(cfg.SelfUpdateServiceName)
	if serviceName == "" {
		serviceName = "els-feedback-proxy"
	}

	return &selfUpdateManager{
		secret:          secret,
		repoOwner:       strings.TrimSpace(cfg.SelfUpdateRepoOwner),
		repoName:        strings.TrimSpace(cfg.SelfUpdateRepoName),
		githubToken:     strings.TrimSpace(cfg.SelfUpdateGitHubToken),
		serviceName:     serviceName,
		workdir:         workdir,
		binaryName:      "els-feedback-proxy",
		stateFile:       filepath.Join(workdir, selfUpdateStateFileName),
		httpClient:      &http.Client{Timeout: 60 * time.Second},
		apiBaseURL:      selfUpdateAPIBaseURL,
		now:             time.Now,
		runtimeGOOS:     runtime.GOOS,
		runtimeGOARCH:   runtime.GOARCH,
		scheduleRestart: scheduleSystemdRestart,
	}
}

func (u *selfUpdateManager) isAuthorized(r *http.Request) bool {
	if u == nil {
		return false
	}

	provided := strings.TrimSpace(r.Header.Get("X-ELS-Update-Secret"))
	if provided == "" {
		authValue := strings.TrimSpace(r.Header.Get("Authorization"))
		if strings.HasPrefix(strings.ToLower(authValue), "bearer ") {
			provided = strings.TrimSpace(authValue[7:])
		}
	}
	if provided == "" {
		return false
	}

	return subtle.ConstantTimeCompare([]byte(provided), []byte(u.secret)) == 1
}

func (u *selfUpdateManager) statusSnapshot() map[string]any {
	snapshot := map[string]any{
		"enabled":      true,
		"repo_owner":   u.repoOwner,
		"repo_name":    u.repoName,
		"service_name": u.serviceName,
		"version":      buildinfo.Version,
		"commit":       buildinfo.Commit,
		"build_time":   buildinfo.BuildTime,
	}

	u.mu.Lock()
	snapshot["in_progress"] = u.inProgress
	if u.currentTag != "" {
		snapshot["current_tag"] = u.currentTag
	}
	if !u.lastRequestedAt.IsZero() {
		snapshot["last_requested_at"] = u.lastRequestedAt.UTC().Format(time.RFC3339)
	}
	if !u.lastCompletedAt.IsZero() {
		snapshot["last_completed_at"] = u.lastCompletedAt.UTC().Format(time.RFC3339)
	}
	if u.lastError != "" {
		snapshot["last_error"] = u.lastError
	}
	if u.lastResult != nil {
		resultCopy := *u.lastResult
		snapshot["last_result"] = resultCopy
	}
	u.mu.Unlock()

	if state, err := u.loadState(); err == nil {
		snapshot["last_deployed_tag"] = state.Tag
		snapshot["last_updated_at"] = state.UpdatedAt.UTC().Format(time.RFC3339)
		snapshot["last_asset_name"] = state.AssetName
	}
	return snapshot
}

func (u *selfUpdateManager) startRelease(rawTag string, force bool) (selfUpdateDispatchResult, error) {
	tag := normalizeReleaseTag(rawTag)
	if tag == "" {
		return selfUpdateDispatchResult{}, fmt.Errorf("tag 不能为空")
	}

	acceptedAt := u.now().UTC()
	u.mu.Lock()
	if u.inProgress {
		u.mu.Unlock()
		return selfUpdateDispatchResult{}, errSelfUpdateBusy
	}
	u.inProgress = true
	u.currentTag = tag
	u.lastRequestedAt = acceptedAt
	u.lastCompletedAt = time.Time{}
	u.lastError = ""
	u.lastResult = nil
	u.mu.Unlock()

	go u.runRelease(tag, force)

	return selfUpdateDispatchResult{
		Tag:        tag,
		Force:      force,
		AcceptedAt: acceptedAt,
	}, nil
}

func (u *selfUpdateManager) runRelease(tag string, force bool) {
	ctx, cancel := context.WithTimeout(context.Background(), selfUpdateJobTimeout)
	defer cancel()

	result, err := u.applyRelease(ctx, tag, force)

	u.mu.Lock()
	u.inProgress = false
	u.currentTag = ""
	u.lastCompletedAt = u.now().UTC()
	if err != nil {
		u.lastError = err.Error()
		u.lastResult = nil
		u.mu.Unlock()
		log.Printf("自动更新失败: tag=%s err=%v", tag, err)
		return
	}

	resultCopy := result
	u.lastError = ""
	u.lastResult = &resultCopy
	u.mu.Unlock()

	log.Printf("自动更新任务完成: tag=%s asset=%s restart_scheduled=%t", result.Tag, result.AssetName, result.RestartScheduled)
}

func (u *selfUpdateManager) applyRelease(ctx context.Context, rawTag string, force bool) (selfUpdateResult, error) {
	tag := normalizeReleaseTag(rawTag)
	if tag == "" {
		return selfUpdateResult{}, fmt.Errorf("tag 不能为空")
	}

	if !force {
		if versionMatchesTag(buildinfo.Version, tag) {
			return selfUpdateResult{
				Tag:            tag,
				AlreadyCurrent: true,
				UpdatedAt:      u.now().UTC(),
			}, nil
		}
		if state, err := u.loadState(); err == nil && versionMatchesTag(state.Tag, tag) {
			return selfUpdateResult{
				Tag:            tag,
				AssetName:      state.AssetName,
				ReleaseURL:     state.ReleaseURL,
				Checksum:       state.Checksum,
				BackupPath:     state.BackupPath,
				AlreadyCurrent: true,
				UpdatedAt:      state.UpdatedAt.UTC(),
			}, nil
		}
	}

	release, err := u.fetchRelease(ctx, tag)
	if err != nil {
		return selfUpdateResult{}, err
	}

	archiveAsset, err := u.selectArchiveAsset(release.Assets)
	if err != nil {
		return selfUpdateResult{}, err
	}
	checksumAsset, err := u.selectChecksumsAsset(release.Assets)
	if err != nil {
		return selfUpdateResult{}, err
	}

	checksumBytes, err := u.download(ctx, checksumAsset.BrowserDownloadURL)
	if err != nil {
		return selfUpdateResult{}, fmt.Errorf("下载 checksums.txt 失败: %w", err)
	}
	expectedChecksum, err := checksumForAsset(string(checksumBytes), archiveAsset.Name)
	if err != nil {
		return selfUpdateResult{}, err
	}

	archiveBytes, err := u.download(ctx, archiveAsset.BrowserDownloadURL)
	if err != nil {
		return selfUpdateResult{}, fmt.Errorf("下载发布资产失败: %w", err)
	}
	actualChecksum := sha256Hex(archiveBytes)
	if !strings.EqualFold(actualChecksum, expectedChecksum) {
		return selfUpdateResult{}, fmt.Errorf("发布资产校验失败: 期望 %s，实际 %s", expectedChecksum, actualChecksum)
	}

	binaryBytes, err := extractBinaryFromTarGz(archiveBytes, u.binaryName)
	if err != nil {
		return selfUpdateResult{}, err
	}

	archivePath, backupPath, err := u.installRelease(tag, archiveAsset.Name, archiveBytes, binaryBytes)
	if err != nil {
		return selfUpdateResult{}, err
	}

	result := selfUpdateResult{
		Tag:              release.TagName,
		AssetName:        archiveAsset.Name,
		ReleaseURL:       release.HTMLURL,
		Checksum:         actualChecksum,
		BackupPath:       backupPath,
		RestartScheduled: true,
		UpdatedAt:        u.now().UTC(),
	}

	if err := u.saveState(selfUpdateState{
		Tag:         release.TagName,
		AssetName:   archiveAsset.Name,
		ReleaseURL:  release.HTMLURL,
		Checksum:    actualChecksum,
		BackupPath:  backupPath,
		UpdatedAt:   result.UpdatedAt,
		BinaryPath:  filepath.Join(u.workdir, u.binaryName),
		ArchivePath: archivePath,
	}); err != nil {
		return selfUpdateResult{}, fmt.Errorf("写入自动更新状态失败: %w", err)
	}

	if err := u.scheduleRestart(u.serviceName, 2*time.Second); err != nil {
		return selfUpdateResult{}, fmt.Errorf("调度服务重启失败: %w", err)
	}

	return result, nil
}

func (u *selfUpdateManager) fetchRelease(ctx context.Context, tag string) (githubRelease, error) {
	endpoint := fmt.Sprintf("%s/repos/%s/%s/releases/tags/%s", strings.TrimRight(u.apiBaseURL, "/"), u.repoOwner, u.repoName, tag)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return githubRelease{}, fmt.Errorf("创建 Release 查询请求失败: %w", err)
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("User-Agent", "ELS-Feedback-Proxy-SelfUpdate")
	if u.githubToken != "" {
		request.Header.Set("Authorization", "Bearer "+u.githubToken)
	}

	response, err := u.httpClient.Do(request)
	if err != nil {
		return githubRelease{}, fmt.Errorf("调用 GitHub Release API 失败: %w", err)
	}
	defer response.Body.Close()

	body, _ := io.ReadAll(response.Body)
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return githubRelease{}, fmt.Errorf("GitHub Release 查询失败: HTTP %d, body=%s", response.StatusCode, string(body))
	}

	var release githubRelease
	if err := json.Unmarshal(body, &release); err != nil {
		return githubRelease{}, fmt.Errorf("解析 GitHub Release 响应失败: %w", err)
	}
	return release, nil
}

func (u *selfUpdateManager) selectArchiveAsset(assets []githubReleaseAsset) (githubReleaseAsset, error) {
	expectedSuffix := fmt.Sprintf("_%s_%s.tar.gz", u.runtimeGOOS, u.runtimeGOARCH)
	for _, asset := range assets {
		if strings.HasSuffix(asset.Name, expectedSuffix) {
			return asset, nil
		}
	}
	return githubReleaseAsset{}, fmt.Errorf("未找到适用于 %s/%s 的发布资产", u.runtimeGOOS, u.runtimeGOARCH)
}

func (u *selfUpdateManager) selectChecksumsAsset(assets []githubReleaseAsset) (githubReleaseAsset, error) {
	for _, asset := range assets {
		if asset.Name == "checksums.txt" {
			return asset, nil
		}
	}
	return githubReleaseAsset{}, fmt.Errorf("未找到 checksums.txt")
}

func (u *selfUpdateManager) download(ctx context.Context, downloadURL string) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
	if err != nil {
		return nil, fmt.Errorf("创建下载请求失败: %w", err)
	}
	request.Header.Set("User-Agent", "ELS-Feedback-Proxy-SelfUpdate")
	if u.githubToken != "" && strings.Contains(downloadURL, "github.com") {
		request.Header.Set("Authorization", "Bearer "+u.githubToken)
	}

	response, err := u.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("下载请求失败: %w", err)
	}
	defer response.Body.Close()

	body, _ := io.ReadAll(response.Body)
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("下载失败: HTTP %d, body=%s", response.StatusCode, string(body))
	}
	return body, nil
}

func (u *selfUpdateManager) installRelease(tag, archiveName string, archiveBytes []byte, binaryBytes []byte) (archivePath string, backupPath string, err error) {
	if err := os.MkdirAll(u.workdir, 0o755); err != nil {
		return "", "", fmt.Errorf("创建工作目录失败: %w", err)
	}

	archivePath = filepath.Join(u.workdir, archiveName)
	if err := os.WriteFile(archivePath, archiveBytes, 0o644); err != nil {
		return "", "", fmt.Errorf("保存发布归档失败: %w", err)
	}

	currentBinaryPath := filepath.Join(u.workdir, u.binaryName)
	currentBinaryBytes, err := os.ReadFile(currentBinaryPath)
	if err != nil {
		return "", "", fmt.Errorf("读取当前二进制失败: %w", err)
	}

	backupPath = filepath.Join(u.workdir, fmt.Sprintf("%s.bak_%s", u.binaryName, u.now().UTC().Format("20060102-150405")))
	if err := os.WriteFile(backupPath, currentBinaryBytes, 0o755); err != nil {
		return "", "", fmt.Errorf("写入备份二进制失败: %w", err)
	}

	newBinaryPath := currentBinaryPath + ".new"
	if err := os.WriteFile(newBinaryPath, binaryBytes, 0o755); err != nil {
		return "", "", fmt.Errorf("写入新二进制失败: %w", err)
	}
	if err := os.Rename(newBinaryPath, currentBinaryPath); err != nil {
		_ = os.Remove(newBinaryPath)
		return "", "", fmt.Errorf("替换二进制失败: %w", err)
	}

	return archivePath, backupPath, nil
}

func (u *selfUpdateManager) loadState() (selfUpdateState, error) {
	data, err := os.ReadFile(u.stateFile)
	if err != nil {
		return selfUpdateState{}, err
	}
	var state selfUpdateState
	if err := json.Unmarshal(data, &state); err != nil {
		return selfUpdateState{}, err
	}
	return state, nil
}

func (u *selfUpdateManager) saveState(state selfUpdateState) error {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(u.stateFile, data, 0o600)
}

func checksumForAsset(checksumsText string, assetName string) (string, error) {
	for _, line := range strings.Split(checksumsText, "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) != 2 {
			continue
		}
		if fields[1] == assetName {
			return strings.TrimSpace(fields[0]), nil
		}
	}
	return "", fmt.Errorf("checksums.txt 中未找到资产 %s 的校验值", assetName)
}

func extractBinaryFromTarGz(archiveBytes []byte, binaryName string) ([]byte, error) {
	gzipReader, err := gzip.NewReader(bytes.NewReader(archiveBytes))
	if err != nil {
		return nil, fmt.Errorf("解压 tar.gz 失败: %w", err)
	}
	defer gzipReader.Close()

	tarReader := tar.NewReader(gzipReader)
	for {
		header, err := tarReader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("读取 tar 归档失败: %w", err)
		}
		if header.Typeflag != tar.TypeReg {
			continue
		}
		if filepath.Base(header.Name) != binaryName {
			continue
		}
		data, err := io.ReadAll(tarReader)
		if err != nil {
			return nil, fmt.Errorf("读取二进制内容失败: %w", err)
		}
		if len(data) == 0 {
			return nil, fmt.Errorf("归档中的二进制内容为空")
		}
		return data, nil
	}
	return nil, fmt.Errorf("归档中未找到二进制 %s", binaryName)
}

func normalizeReleaseTag(raw string) string {
	tag := strings.TrimSpace(raw)
	if tag == "" {
		return ""
	}
	if strings.HasPrefix(tag, "v") {
		return tag
	}
	return "v" + tag
}

func versionMatchesTag(version string, tag string) bool {
	left := strings.TrimPrefix(normalizeReleaseTag(version), "v")
	right := strings.TrimPrefix(normalizeReleaseTag(tag), "v")
	return left != "" && left == right
}

func sha256Hex(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func scheduleSystemdRestart(serviceName string, delay time.Duration) error {
	serviceName = strings.TrimSpace(serviceName)
	if serviceName == "" {
		return fmt.Errorf("serviceName 不能为空")
	}
	for _, char := range serviceName {
		if !(char == '-' || char == '_' || char == '.' || char == '@' || (char >= '0' && char <= '9') || (char >= 'A' && char <= 'Z') || (char >= 'a' && char <= 'z')) {
			return fmt.Errorf("serviceName 包含非法字符")
		}
	}
	delaySeconds := int(delay / time.Second)
	if delaySeconds < 1 {
		delaySeconds = 1
	}

	command := fmt.Sprintf("nohup /bin/sh -c 'sleep %d; systemctl restart %s' >/dev/null 2>&1 &", delaySeconds, serviceName)
	return exec.Command("/bin/sh", "-c", command).Run()
}
