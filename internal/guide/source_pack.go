package guide

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"
)

const (
	sourcePackSchemaVersion = 1
	maxSourcePackFiles      = 10_000
	maxSourcePackFileBytes  = 8 * 1024 * 1024
	maxSourcePackTotalBytes = 128 * 1024 * 1024
	maxSourcePackZipBytes   = 64 * 1024 * 1024
)

var allowedSourcePackExtensions = map[string]struct{}{
	".swift": {}, ".md": {}, ".json": {}, ".plist": {}, ".yml": {}, ".yaml": {},
	".toml": {}, ".xml": {}, ".xcconfig": {}, ".entitlements": {}, ".strings": {},
	".stringsdict": {}, ".pbxproj": {}, ".sql": {}, ".proto": {},
	".go": {}, ".ts": {}, ".js": {}, ".vue": {}, ".html": {}, ".css": {}, ".sh": {},
	".c": {}, ".h": {}, ".cc": {}, ".cpp": {}, ".cxx": {}, ".hpp": {}, ".m": {}, ".mm": {},
	".metal": {}, ".py": {}, ".rb": {}, ".rs": {}, ".java": {}, ".kt": {}, ".kts": {},
}

type SourceArchiveGateway interface {
	DownloadSourceArchive(ctx context.Context, commitSHA string) (io.ReadCloser, error)
}

type SourcePackFile struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
}

type SourcePackManifest struct {
	SchemaVersion int              `json:"schema_version"`
	Repository    string           `json:"repository"`
	CommitSHA     string           `json:"commit_sha"`
	Files         []SourcePackFile `json:"files"`
}

type SourcePack struct {
	CommitSHA string
	Path      string
	Size      int64
}

// SourcePackService 把 GitHub 归档裁剪为不可变纯文本源码包，供客户端一次下载后本地检索。
type SourcePackService struct {
	mu         sync.Mutex
	gateway    SourceArchiveGateway
	repository string
	cacheDir   string
}

func NewSourcePackService(gateway SourceArchiveGateway, owner, repo, dataDir string) *SourcePackService {
	if gateway == nil {
		return nil
	}
	repository := strings.TrimSpace(owner) + "/" + strings.TrimSpace(repo)
	if !strings.EqualFold(repository, SourceTreeRepository) {
		return nil
	}
	return &SourcePackService{
		gateway:    gateway,
		repository: SourceTreeRepository,
		cacheDir:   filepath.Join(dataDir, "guide-source-packs"),
	}
}

func (s *SourcePackService) Load(ctx context.Context, commitSHA string) (SourcePack, error) {
	sha := strings.ToLower(strings.TrimSpace(commitSHA))
	if !isFullCommitSHA(sha) {
		return SourcePack{}, ErrInvalidCommitSHA
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if cached, err := s.readCache(sha); err == nil {
		return cached, nil
	}
	return s.generate(ctx, sha)
}

func (s *SourcePackService) generate(ctx context.Context, sha string) (SourcePack, error) {
	body, err := s.gateway.DownloadSourceArchive(ctx, sha)
	if err != nil {
		return SourcePack{}, err
	}
	defer body.Close()
	gzipReader, err := gzip.NewReader(body)
	if err != nil {
		return SourcePack{}, fmt.Errorf("打开 GitHub 源码归档失败: %w", err)
	}
	defer gzipReader.Close()

	if err := os.MkdirAll(s.cacheDir, 0o755); err != nil {
		return SourcePack{}, err
	}
	temporary, err := os.CreateTemp(s.cacheDir, ".source-pack-*.tmp")
	if err != nil {
		return SourcePack{}, err
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)

	zipWriter := zip.NewWriter(temporary)
	manifest, err := s.writeSources(ctx, tar.NewReader(gzipReader), zipWriter, sha)
	if err == nil {
		err = writeSourcePackManifest(zipWriter, manifest)
	}
	if closeErr := zipWriter.Close(); err == nil {
		err = closeErr
	}
	if syncErr := temporary.Sync(); err == nil {
		err = syncErr
	}
	if closeErr := temporary.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return SourcePack{}, fmt.Errorf("生成源码包失败: %w", err)
	}

	info, err := os.Stat(temporaryName)
	if err != nil {
		return SourcePack{}, err
	}
	if info.Size() <= 0 || info.Size() > maxSourcePackZipBytes {
		return SourcePack{}, errors.New("生成的源码包体积异常")
	}
	destination := s.cachePath(sha)
	if err := os.Remove(destination); err != nil && !errors.Is(err, os.ErrNotExist) {
		return SourcePack{}, err
	}
	if err := os.Rename(temporaryName, destination); err != nil {
		return SourcePack{}, err
	}
	return SourcePack{CommitSHA: sha, Path: destination, Size: info.Size()}, nil
}

func (s *SourcePackService) writeSources(
	ctx context.Context,
	tarReader *tar.Reader,
	zipWriter *zip.Writer,
	sha string,
) (SourcePackManifest, error) {
	files := make([]SourcePackFile, 0, 1024)
	var totalBytes int64
	for {
		if err := ctx.Err(); err != nil {
			return SourcePackManifest{}, err
		}
		header, err := tarReader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return SourcePackManifest{}, err
		}
		if header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeRegA {
			continue
		}
		relativePath := sourceArchiveRelativePath(header.Name)
		if !isAllowedSourcePackPath(relativePath) {
			continue
		}
		if header.Size < 0 || header.Size > maxSourcePackFileBytes {
			continue
		}
		if len(files) >= maxSourcePackFiles || totalBytes+header.Size > maxSourcePackTotalBytes {
			return SourcePackManifest{}, errors.New("源码包超过文件数量或文本体积上限")
		}
		data, err := io.ReadAll(io.LimitReader(tarReader, header.Size+1))
		if err != nil || int64(len(data)) != header.Size {
			return SourcePackManifest{}, errors.New("读取源码归档文件失败")
		}
		if !utf8.Valid(data) {
			continue
		}
		entryHeader := &zip.FileHeader{Name: "sources/" + relativePath, Method: zip.Deflate}
		entryHeader.SetMode(0o644)
		entryWriter, err := zipWriter.CreateHeader(entryHeader)
		if err != nil {
			return SourcePackManifest{}, err
		}
		if _, err := entryWriter.Write(data); err != nil {
			return SourcePackManifest{}, err
		}
		files = append(files, SourcePackFile{Path: relativePath, Size: header.Size})
		totalBytes += header.Size
	}
	if len(files) == 0 {
		return SourcePackManifest{}, errors.New("GitHub 归档中没有可用源码")
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return SourcePackManifest{
		SchemaVersion: sourcePackSchemaVersion,
		Repository:    s.repository,
		CommitSHA:     sha,
		Files:         files,
	}, nil
}

func writeSourcePackManifest(zipWriter *zip.Writer, manifest SourcePackManifest) error {
	data, err := json.Marshal(manifest)
	if err != nil {
		return err
	}
	entry, err := zipWriter.CreateHeader(&zip.FileHeader{Name: "manifest.json", Method: zip.Deflate})
	if err != nil {
		return err
	}
	_, err = entry.Write(data)
	return err
}

func (s *SourcePackService) readCache(sha string) (SourcePack, error) {
	cachePath := s.cachePath(sha)
	info, err := os.Stat(cachePath)
	if err != nil || info.Size() <= 0 || info.Size() > maxSourcePackZipBytes {
		return SourcePack{}, errors.New("源码包缓存无效")
	}
	reader, err := zip.OpenReader(cachePath)
	if err != nil {
		return SourcePack{}, err
	}
	defer reader.Close()
	for _, entry := range reader.File {
		if entry.Name != "manifest.json" {
			continue
		}
		stream, err := entry.Open()
		if err != nil {
			return SourcePack{}, err
		}
		data, readErr := io.ReadAll(io.LimitReader(stream, 1024*1024))
		closeErr := stream.Close()
		if readErr != nil {
			return SourcePack{}, readErr
		}
		if closeErr != nil {
			return SourcePack{}, closeErr
		}
		var manifest SourcePackManifest
		if err := json.Unmarshal(data, &manifest); err != nil || !s.validManifest(manifest, sha) {
			return SourcePack{}, errors.New("源码包清单无效")
		}
		return SourcePack{CommitSHA: sha, Path: cachePath, Size: info.Size()}, nil
	}
	return SourcePack{}, errors.New("源码包缺少清单")
}

func (s *SourcePackService) validManifest(manifest SourcePackManifest, sha string) bool {
	return manifest.SchemaVersion == sourcePackSchemaVersion &&
		manifest.Repository == s.repository &&
		strings.EqualFold(manifest.CommitSHA, sha) &&
		len(manifest.Files) > 0 && len(manifest.Files) <= maxSourcePackFiles
}

func (s *SourcePackService) cachePath(sha string) string {
	return filepath.Join(s.cacheDir, sha+".zip")
}

func sourceArchiveRelativePath(raw string) string {
	raw = strings.TrimPrefix(strings.TrimSpace(raw), "./")
	if raw == "" || strings.HasPrefix(raw, "/") || strings.ContainsRune(raw, '\x00') {
		return ""
	}
	cleaned := path.Clean(raw)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return ""
	}
	separator := strings.IndexByte(cleaned, '/')
	if separator < 0 || separator == len(cleaned)-1 {
		return ""
	}
	return cleaned[separator+1:]
}

func isAllowedSourcePackPath(value string) bool {
	if value == "" || strings.HasPrefix(value, "/") || strings.Contains(value, "\\") {
		return false
	}
	for _, component := range strings.Split(value, "/") {
		if component == "" || component == "." || component == ".." {
			return false
		}
	}
	_, allowed := allowedSourcePackExtensions[strings.ToLower(path.Ext(value))]
	return allowed
}
