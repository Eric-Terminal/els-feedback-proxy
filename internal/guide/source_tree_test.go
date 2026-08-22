package guide

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"els-feedback-proxy/internal/github"
)

type fakeSourceTreeGateway struct {
	calls int
	tree  github.SourceTree
}

func (g *fakeSourceTreeGateway) GetSourceTree(context.Context, string) (github.SourceTree, error) {
	g.calls++
	return g.tree, nil
}

func TestSourceTreeCachesImmutableCommit(t *testing.T) {
	sha := "0123456789abcdef0123456789abcdef01234567"
	size := 128
	gateway := &fakeSourceTreeGateway{tree: github.SourceTree{Entries: []github.SourceTreeEntry{
		{Path: "ETOSCore/Guide.swift", Type: "blob", Size: &size},
	}}}
	service := NewSourceTreeService(gateway, "Eric-Terminal", "ETOS-LLM-Studio", t.TempDir())
	first, err := service.Load(context.Background(), sha)
	if err != nil {
		t.Fatalf("首次加载源码树失败: %v", err)
	}
	second, err := service.Load(context.Background(), sha)
	if err != nil {
		t.Fatalf("读取缓存源码树失败: %v", err)
	}
	if gateway.calls != 1 {
		t.Fatalf("不可变提交只应请求 GitHub 一次，实际 %d", gateway.calls)
	}
	if !reflect.DeepEqual(first, second) || first.Repository != "Eric-Terminal/ETOS-LLM-Studio" || first.Truncated {
		t.Fatalf("源码树响应不稳定: %#v %#v", first, second)
	}
	cachePath := filepath.Join(service.cacheDir, sha+".json")
	if _, err := os.Stat(cachePath); err != nil {
		t.Fatalf("源码树缓存未落盘: %v", err)
	}
}

func TestSourceTreeRejectsShortCommit(t *testing.T) {
	service := NewSourceTreeService(&fakeSourceTreeGateway{}, "Eric-Terminal", "ETOS-LLM-Studio", t.TempDir())
	if _, err := service.Load(context.Background(), "0123456"); err != ErrInvalidCommitSHA {
		t.Fatalf("短哈希应被拒绝，实际错误: %v", err)
	}
}

func TestSourceTreeRejectsUnexpectedRepository(t *testing.T) {
	service := NewSourceTreeService(&fakeSourceTreeGateway{}, "someone", "private-repo", t.TempDir())
	if service != nil {
		t.Fatal("源码树服务不应暴露非 ETOS 仓库")
	}
}
