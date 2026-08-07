package store

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestDistributionStoreOfficialActionLifecycle(t *testing.T) {
	dataDir := t.TempDir()
	distributionStore, err := NewDistributionStore(dataDir)
	if err != nil {
		t.Fatalf("初始化官方数据存储失败: %v", err)
	}

	firstPayload := testOfficialProviderActionPayload(1, "官方提供商")
	created, err := distributionStore.CreateAction(
		OfficialDataActionInput{Enabled: true},
		DistributionUpload{FileName: "provider-action.json", Data: firstPayload},
	)
	if err != nil {
		t.Fatalf("创建官方数据库操作失败: %v", err)
	}
	if created.ID != "official-provider.test" ||
		created.ProviderID != "11111111-1111-4111-8111-111111111111" ||
		created.Revision != 1 || !created.Enabled {
		t.Fatalf("创建后的操作元数据不正确: %#v", created)
	}
	if file, path, ok := distributionStore.PublicFile(created.SHA256, created.FileName); !ok {
		t.Fatalf("已启用操作载荷应可公开读取")
	} else if file.ContentType != "application/json" {
		t.Fatalf("操作载荷内容类型不正确: %s", file.ContentType)
	} else if data, readErr := os.ReadFile(path); readErr != nil || string(data) != string(firstPayload) {
		t.Fatalf("操作载荷内容不正确: data=%q err=%v", data, readErr)
	}

	if _, err := distributionStore.CreateAction(
		OfficialDataActionInput{Enabled: true},
		DistributionUpload{FileName: "duplicate.json", Data: firstPayload},
	); err == nil || !strings.Contains(err.Error(), "操作 ID 已存在") {
		t.Fatalf("重复操作 ID 应被拒绝，实际错误: %v", err)
	}
	differentActionSameProvider := []byte(strings.Replace(
		string(firstPayload),
		"official-provider.test",
		"official-provider.same-provider",
		1,
	))
	if _, err := distributionStore.CreateAction(
		OfficialDataActionInput{Enabled: true},
		DistributionUpload{FileName: "same-provider.json", Data: differentActionSameProvider},
	); err == nil || !strings.Contains(err.Error(), "同一提供商") {
		t.Fatalf("同一提供商的重复操作应被拒绝，实际错误: %v", err)
	}

	changedSameRevision := testOfficialProviderActionPayload(1, "被改写的提供商")
	if _, err := distributionStore.UpdateAction(
		created.Key,
		OfficialDataActionInput{Enabled: true},
		&DistributionUpload{FileName: "provider-action.json", Data: changedSameRevision},
	); err == nil || !strings.Contains(err.Error(), "同一 revision") {
		t.Fatalf("同一 revision 的不同内容应被拒绝，实际错误: %v", err)
	}

	secondPayload := testOfficialProviderActionPayload(2, "官方提供商 v2")
	updated, err := distributionStore.UpdateAction(
		created.Key,
		OfficialDataActionInput{Enabled: false},
		&DistributionUpload{FileName: "provider-action-v2.json", Data: secondPayload},
	)
	if err != nil {
		t.Fatalf("更新官方数据库操作失败: %v", err)
	}
	if updated.Revision != 2 || updated.Enabled {
		t.Fatalf("更新后的操作元数据不正确: %#v", updated)
	}
	if _, _, ok := distributionStore.PublicFile(updated.SHA256, updated.FileName); ok {
		t.Fatalf("停用后的操作载荷不应公开")
	}

	reloaded, err := NewDistributionStore(dataDir)
	if err != nil {
		t.Fatalf("重新加载官方数据库操作失败: %v", err)
	}
	if actions := reloaded.ListActions(); len(actions) != 1 || actions[0].Revision != 2 {
		t.Fatalf("重新加载后的操作记录不正确: %#v", actions)
	}
	if err := reloaded.DeleteAction(created.Key); err != nil {
		t.Fatalf("删除官方数据库操作失败: %v", err)
	}
}

func TestOfficialProviderActionRejectsInvalidPolicy(t *testing.T) {
	payload := strings.Replace(
		string(testOfficialProviderActionPayload(1, "官方提供商")),
		`"api_keys": "preserve_local_if_nonempty"`,
		`"api_keys": "run_sql"`,
		1,
	)
	if _, err := DecodeOfficialDataActionBundle([]byte(payload)); err == nil || !strings.Contains(err.Error(), "api_keys") {
		t.Fatalf("无效合并策略应被拒绝，实际错误: %v", err)
	}
}

func testOfficialProviderActionPayload(revision int, providerName string) []byte {
	return []byte(fmt.Sprintf(`{
  "schema_version": 1,
  "id": "official-provider.test",
  "revision": %d,
  "kind": "provider.upsert",
  "apply_on": ["initial_sync", "manual_sync"],
  "platforms": ["ios", "watchos"],
  "provider": {
    "id": "11111111-1111-4111-8111-111111111111",
    "name": %q,
    "baseURL": "https://example.com/v1",
    "apiFormat": "openai-compatible",
    "models": [
      {
        "id": "22222222-2222-4222-8222-222222222222",
        "modelName": "example-chat",
        "displayName": "Example Chat",
        "isActivated": true
      }
    ]
  },
  "merge_policy": {
    "provider_fields": "update_if_unmodified",
    "api_keys": "preserve_local_if_nonempty",
    "header_overrides": "update_if_unmodified",
    "proxy_configuration": "preserve_local",
    "models": {
      "fields": "update_if_unmodified",
      "is_activated": "preserve_local",
      "on_missing": "insert",
      "on_removed": "delete_if_unmodified",
      "user_models": "preserve"
    },
    "if_user_deleted": "restore_on_manual_sync"
  }
}`, revision, providerName))
}
