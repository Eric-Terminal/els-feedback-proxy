package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBlockedArchiveStoreSaveMarkdown(t *testing.T) {
	tempDir := t.TempDir()
	store, err := NewBlockedArchiveStore(tempDir)
	if err != nil {
		t.Fatalf("初始化失败: %v", err)
	}

	fileName, err := store.SaveMarkdown("abc-123", "# 标题\n内容")
	if err != nil {
		t.Fatalf("保存失败: %v", err)
	}
	if !strings.HasPrefix(fileName, "archive-abc-123") {
		t.Fatalf("文件名不符合预期: %s", fileName)
	}

	fullPath := filepath.Join(tempDir, "review-blocked", fileName)
	data, err := os.ReadFile(fullPath)
	if err != nil {
		t.Fatalf("读取文件失败: %v", err)
	}
	if !strings.Contains(string(data), "标题") {
		t.Fatalf("文件内容不完整: %s", string(data))
	}
}
