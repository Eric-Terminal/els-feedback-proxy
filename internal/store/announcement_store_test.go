package store

import (
	"testing"
)

func TestAnnouncementStoreCRUDAndPersistence(t *testing.T) {
	dataDir := t.TempDir()
	store, err := NewAnnouncementStore(dataDir)
	if err != nil {
		t.Fatalf("初始化公告存储失败: %v", err)
	}

	created, err := store.Create(AnnouncementRecord{
		ID:       2026072301,
		Type:     "warning",
		MinBuild: "10",
		MaxBuild: "99",
		Language: "zh-Hans",
		Platform: "ios",
		Title:    "测试公告",
		Body:     "这是一条用于验证持久化的公告。",
		Enabled:  true,
	})
	if err != nil {
		t.Fatalf("创建公告失败: %v", err)
	}
	if created.Key == "" || created.Platform != "iOS" {
		t.Fatalf("创建结果未完成规范化: %+v", created)
	}

	created.Title = "更新后的标题"
	updated, err := store.Update(created.Key, created)
	if err != nil {
		t.Fatalf("更新公告失败: %v", err)
	}
	if updated.Title != "更新后的标题" {
		t.Fatalf("更新标题未保存: %+v", updated)
	}

	reloaded, err := NewAnnouncementStore(dataDir)
	if err != nil {
		t.Fatalf("重新加载公告存储失败: %v", err)
	}
	public := reloaded.PublicList()
	if len(public) != 1 || public[0].Title != "更新后的标题" {
		t.Fatalf("持久化内容不正确: %+v", public)
	}

	if err := reloaded.Delete(created.Key); err != nil {
		t.Fatalf("删除公告失败: %v", err)
	}
	if len(reloaded.List()) != 0 {
		t.Fatalf("公告删除后仍存在")
	}
}

func TestAnnouncementStoreRejectsInvalidBuildRange(t *testing.T) {
	store, err := NewAnnouncementStore(t.TempDir())
	if err != nil {
		t.Fatalf("初始化公告存储失败: %v", err)
	}

	_, err = store.Create(AnnouncementRecord{
		ID:       1,
		Type:     "info",
		MinBuild: "20",
		MaxBuild: "10",
		Title:    "标题",
		Body:     "正文",
		Enabled:  true,
	})
	if err == nil {
		t.Fatalf("无效构建号范围应被拒绝")
	}
}
