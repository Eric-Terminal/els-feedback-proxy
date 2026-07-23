package store

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"testing"
)

func TestDistributionStoreCRUDAndPublicFiles(t *testing.T) {
	dataDir := t.TempDir()
	distributionStore, err := NewDistributionStore(dataDir)
	if err != nil {
		t.Fatalf("初始化官方数据存储失败: %v", err)
	}
	if records := distributionStore.List(); records == nil || len(records) != 0 {
		t.Fatalf("空存储应返回空数组: %#v", records)
	}

	firstData := []byte(`{"name":"first"}`)
	created, err := distributionStore.Create(
		DistributionInput{
			Name:            "默认提供商",
			DestinationPath: "Documents/Providers",
			Enabled:         true,
		},
		DistributionUpload{
			FileName:    `folder\provider.json`,
			ContentType: "application/json",
			Data:        firstData,
		},
	)
	if err != nil {
		t.Fatalf("创建官方数据失败: %v", err)
	}
	if created.DestinationPath != "/Documents/Providers" ||
		created.FileName != "provider.json" ||
		created.Size != int64(len(firstData)) {
		t.Fatalf("创建记录未正确规范化: %#v", created)
	}

	digest := sha256.Sum256(firstData)
	expectedChecksum := hex.EncodeToString(digest[:])
	if created.SHA256 != expectedChecksum {
		t.Fatalf("SHA256 不正确: %s", created.SHA256)
	}
	if _, path, ok := distributionStore.PublicFile(created.SHA256, created.FileName); !ok {
		t.Fatalf("已启用文件应可公开读取")
	} else if storedData, err := os.ReadFile(path); err != nil || string(storedData) != string(firstData) {
		t.Fatalf("公开文件内容不正确: data=%q err=%v", storedData, err)
	}

	updated, err := distributionStore.Update(
		created.Key,
		DistributionInput{
			Name:            "默认提供商配置",
			DestinationPath: "/Documents/Providers",
			Enabled:         false,
		},
		nil,
	)
	if err != nil {
		t.Fatalf("更新官方数据失败: %v", err)
	}
	if updated.SHA256 != created.SHA256 || updated.Enabled {
		t.Fatalf("仅更新元信息时不应替换文件: %#v", updated)
	}
	if _, _, ok := distributionStore.PublicFile(updated.SHA256, updated.FileName); ok {
		t.Fatalf("停用后的文件不应公开读取")
	}

	secondData := []byte(`{"name":"second"}`)
	replaced, err := distributionStore.Update(
		created.Key,
		DistributionInput{
			Name:            "默认提供商配置",
			DestinationPath: "/Documents/Providers",
			Enabled:         true,
		},
		&DistributionUpload{
			FileName:    "provider-v2.json",
			ContentType: "application/json",
			Data:        secondData,
		},
	)
	if err != nil {
		t.Fatalf("替换官方数据文件失败: %v", err)
	}
	if replaced.SHA256 == created.SHA256 || replaced.FileName != "provider-v2.json" {
		t.Fatalf("替换文件后元信息未更新: %#v", replaced)
	}
	if _, _, ok := distributionStore.PublicFile(created.SHA256, created.FileName); ok {
		t.Fatalf("旧文件不应继续公开读取")
	}

	reloaded, err := NewDistributionStore(dataDir)
	if err != nil {
		t.Fatalf("重新加载官方数据存储失败: %v", err)
	}
	if records := reloaded.PublicList(); len(records) != 1 || records[0].Key != created.Key {
		t.Fatalf("重新加载后的公开清单不正确: %#v", records)
	}

	if err := reloaded.Delete(created.Key); err != nil {
		t.Fatalf("删除官方数据失败: %v", err)
	}
	if records := reloaded.List(); len(records) != 0 {
		t.Fatalf("删除后仍有官方数据: %#v", records)
	}
}

func TestDistributionStoreRejectsUnsafeDestinations(t *testing.T) {
	distributionStore, err := NewDistributionStore(t.TempDir())
	if err != nil {
		t.Fatalf("初始化官方数据存储失败: %v", err)
	}

	for _, destination := range []string{
		"/Library",
		"/Documents/../Library",
		"Documents/Providers/../../Library",
		"Documents//Providers",
	} {
		_, err := distributionStore.Create(
			DistributionInput{
				Name:            "危险路径",
				DestinationPath: destination,
				Enabled:         true,
			},
			DistributionUpload{
				FileName: "provider.json",
				Data:     []byte("{}"),
			},
		)
		if err == nil {
			t.Fatalf("应拒绝不安全目标目录: %s", destination)
		}
	}
}
