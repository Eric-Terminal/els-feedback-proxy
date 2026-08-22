package guide

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"els-feedback-proxy/internal/github"
)

const (
	sourceTreeSchemaVersion = 1
	SourceTreeRepository    = "Eric-Terminal/ETOS-LLM-Studio"
)

var ErrInvalidCommitSHA = errors.New("源码树只接受完整 40 位提交哈希")

type SourceTreeGateway interface {
	GetSourceTree(ctx context.Context, commitSHA string) (github.SourceTree, error)
}

type SourceTreeEntry struct {
	Path string `json:"path"`
	Type string `json:"type"`
	Size *int   `json:"size,omitempty"`
}

type SourceTreeResponse struct {
	SchemaVersion int               `json:"schema_version"`
	Repository    string            `json:"repository"`
	CommitSHA     string            `json:"commit_sha"`
	Truncated     bool              `json:"truncated"`
	Entries       []SourceTreeEntry `json:"entries"`
}

// SourceTreeService 为不可变 Commit 缓存完整目录树，避免客户端直接消耗匿名 GitHub 配额。
type SourceTreeService struct {
	mu         sync.Mutex
	gateway    SourceTreeGateway
	repository string
	cacheDir   string
}

func NewSourceTreeService(gateway SourceTreeGateway, owner, repo, dataDir string) *SourceTreeService {
	if gateway == nil {
		return nil
	}
	repository := strings.TrimSpace(owner) + "/" + strings.TrimSpace(repo)
	// 这个公开接口只为 ETOS 客户端提供精确版本目录，避免部署配置意外把别的仓库暴露出去。
	if !strings.EqualFold(repository, SourceTreeRepository) {
		return nil
	}
	return &SourceTreeService{
		gateway:    gateway,
		repository: SourceTreeRepository,
		cacheDir:   filepath.Join(dataDir, "guide-source-trees"),
	}
}

func (s *SourceTreeService) Load(ctx context.Context, commitSHA string) (SourceTreeResponse, error) {
	sha := strings.ToLower(strings.TrimSpace(commitSHA))
	if !isFullCommitSHA(sha) {
		return SourceTreeResponse{}, ErrInvalidCommitSHA
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if cached, err := s.readCache(sha); err == nil && s.valid(cached, sha) {
		return cached, nil
	}
	tree, err := s.gateway.GetSourceTree(ctx, sha)
	if err != nil {
		return SourceTreeResponse{}, err
	}
	if tree.Truncated {
		return SourceTreeResponse{}, errors.New("GitHub 源码树仍然不完整")
	}
	entries := make([]SourceTreeEntry, 0, len(tree.Entries))
	for _, entry := range tree.Entries {
		cleanPath := strings.TrimSpace(entry.Path)
		if cleanPath == "" || strings.HasPrefix(cleanPath, "/") || strings.Contains(cleanPath, "../") {
			continue
		}
		entries = append(entries, SourceTreeEntry{Path: cleanPath, Type: entry.Type, Size: entry.Size})
	}
	response := SourceTreeResponse{
		SchemaVersion: sourceTreeSchemaVersion,
		Repository:    s.repository,
		CommitSHA:     sha,
		Truncated:     false,
		Entries:       entries,
	}
	if err := s.writeCache(response); err != nil {
		return SourceTreeResponse{}, fmt.Errorf("缓存源码树失败: %w", err)
	}
	return response, nil
}

func (s *SourceTreeService) readCache(sha string) (SourceTreeResponse, error) {
	data, err := os.ReadFile(filepath.Join(s.cacheDir, sha+".json"))
	if err != nil {
		return SourceTreeResponse{}, err
	}
	var response SourceTreeResponse
	if err := json.Unmarshal(data, &response); err != nil {
		return SourceTreeResponse{}, err
	}
	return response, nil
}

func (s *SourceTreeService) writeCache(response SourceTreeResponse) error {
	if err := os.MkdirAll(s.cacheDir, 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(response)
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(s.cacheDir, ".source-tree-*.tmp")
	if err != nil {
		return err
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	if _, err := temporary.Write(data); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryName, filepath.Join(s.cacheDir, response.CommitSHA+".json"))
}

func (s *SourceTreeService) valid(response SourceTreeResponse, sha string) bool {
	return response.SchemaVersion == sourceTreeSchemaVersion &&
		response.Repository == s.repository &&
		strings.EqualFold(response.CommitSHA, sha) &&
		!response.Truncated
}

func isFullCommitSHA(value string) bool {
	if len(value) != 40 {
		return false
	}
	for _, character := range value {
		if !((character >= '0' && character <= '9') || (character >= 'a' && character <= 'f') || (character >= 'A' && character <= 'F')) {
			return false
		}
	}
	return true
}
