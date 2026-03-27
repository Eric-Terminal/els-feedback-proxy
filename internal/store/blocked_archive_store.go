package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

var archiveIDSanitizer = regexp.MustCompile(`[^a-zA-Z0-9_-]+`)

// BlockedArchiveStore 负责保存未通过审核的本地档案。
type BlockedArchiveStore struct {
	mu  sync.Mutex
	dir string
}

func NewBlockedArchiveStore(dataDir string) (*BlockedArchiveStore, error) {
	dir := filepath.Join(dataDir, "review-blocked")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("创建审核留档目录失败: %w", err)
	}
	return &BlockedArchiveStore{dir: dir}, nil
}

func (s *BlockedArchiveStore) SaveMarkdown(archiveID string, markdown string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	safeID := sanitizeArchiveID(archiveID)
	if safeID == "" {
		return "", errors.New("archiveID 为空")
	}
	content := strings.TrimSpace(markdown)
	if content == "" {
		return "", errors.New("markdown 为空")
	}

	fileName := fmt.Sprintf("archive-%s.md", safeID)
	targetPath := filepath.Join(s.dir, fileName)
	if _, err := os.Stat(targetPath); err == nil {
		fileName = fmt.Sprintf("archive-%s-%d.md", safeID, time.Now().Unix())
		targetPath = filepath.Join(s.dir, fileName)
	}

	if err := os.WriteFile(targetPath, []byte(content+"\n"), 0o600); err != nil {
		return "", fmt.Errorf("写入审核留档失败: %w", err)
	}
	return fileName, nil
}

func sanitizeArchiveID(archiveID string) string {
	cleaned := strings.TrimSpace(archiveID)
	cleaned = archiveIDSanitizer.ReplaceAllString(cleaned, "-")
	cleaned = strings.Trim(cleaned, "-_")
	return cleaned
}
