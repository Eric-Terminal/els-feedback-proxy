package telemetry

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStorePersistsDeduplicatesAndConfirmsPrecisely(t *testing.T) {
	dataDir := t.TempDir()
	now := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	options := StoreOptions{
		Retention:    30 * 24 * time.Hour,
		MaxTotalSize: 1 << 20,
		Now:          func() time.Time { return now },
	}
	store, err := NewStore(dataDir, options)
	if err != nil {
		t.Fatalf("初始化遥测存储失败: %v", err)
	}
	item := decodeStoredTestEnvelope(t, PayloadKindDiagnostic, `{"hang":1}`, now)

	status, err := store.Save(item)
	if err != nil || status != "accepted" {
		t.Fatalf("首次保存应 accepted，status=%q err=%v", status, err)
	}
	status, err = store.Save(item)
	if err != nil || status != "duplicate" {
		t.Fatalf("重复保存应 duplicate，status=%q err=%v", status, err)
	}

	manifest := store.Manifest()
	if len(manifest.Entries) != 1 {
		t.Fatalf("清单应只有 1 条，实际 %d", len(manifest.Entries))
	}
	entry, data, err := store.Read(item.Envelope.PayloadID)
	if err != nil {
		t.Fatalf("读取遥测文件失败: %v", err)
	}
	if int64(len(data)) != entry.SizeBytes || !json.Valid(data) {
		t.Fatalf("导出文件应与清单大小一致且为有效 JSON")
	}

	reloaded, err := NewStore(dataDir, options)
	if err != nil {
		t.Fatalf("重启加载遥测存储失败: %v", err)
	}
	status, err = reloaded.Save(item)
	if err != nil || status != "duplicate" {
		t.Fatalf("重启后仍应持久去重，status=%q err=%v", status, err)
	}
	confirm, err := reloaded.Confirm([]string{item.Envelope.PayloadID})
	if err != nil || len(confirm.ConfirmedPayloadIDs) != 1 {
		t.Fatalf("精确确认失败: result=%+v err=%v", confirm, err)
	}
	if _, _, err := reloaded.Read(item.Envelope.PayloadID); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("确认后原始文件应被删除，实际错误: %v", err)
	}
	confirm, err = reloaded.Confirm([]string{item.Envelope.PayloadID})
	if err != nil || len(confirm.ConfirmedPayloadIDs) != 1 {
		t.Fatalf("重复确认应保持幂等: result=%+v err=%v", confirm, err)
	}
}

func TestStoreQuotaEvictsMetricsBeforeDiagnostics(t *testing.T) {
	dataDir := t.TempDir()
	now := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	diagnostic := decodeStoredTestEnvelope(t, PayloadKindDiagnostic, `{"value":0}`, now)
	metricOne := decodeStoredTestEnvelope(t, PayloadKindMetric, `{"value":1}`, now)
	metricTwo := decodeStoredTestEnvelope(t, PayloadKindMetric, `{"value":2}`, now)
	fileSize := int64(len(diagnostic.Raw) + 1)
	store, err := NewStore(dataDir, StoreOptions{
		Retention:    30 * 24 * time.Hour,
		MaxTotalSize: fileSize * 2,
		Now:          func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("初始化遥测存储失败: %v", err)
	}

	if _, err := store.Save(diagnostic); err != nil {
		t.Fatalf("保存诊断失败: %v", err)
	}
	now = now.Add(time.Minute)
	if _, err := store.Save(metricOne); err != nil {
		t.Fatalf("保存第一条指标失败: %v", err)
	}
	now = now.Add(time.Minute)
	if _, err := store.Save(metricTwo); err != nil {
		t.Fatalf("保存第二条指标失败: %v", err)
	}

	manifest := store.Manifest()
	if len(manifest.Entries) != 2 {
		t.Fatalf("配额下应保留 2 条，实际 %d", len(manifest.Entries))
	}
	ids := map[string]bool{}
	for _, entry := range manifest.Entries {
		ids[entry.PayloadID] = true
	}
	if !ids[diagnostic.Envelope.PayloadID] || !ids[metricTwo.Envelope.PayloadID] {
		t.Fatalf("应优先保留诊断和最新指标: %+v", manifest.Entries)
	}
	if ids[metricOne.Envelope.PayloadID] {
		t.Fatalf("最旧普通指标应先被淘汰")
	}
}

func TestStoreRejectsMetricWhenOnlyDiagnosticsCanMakeRoom(t *testing.T) {
	dataDir := t.TempDir()
	now := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	diagnostic := decodeStoredTestEnvelope(t, PayloadKindDiagnostic, `{"value":0}`, now)
	metric := decodeStoredTestEnvelope(t, PayloadKindMetric, `{"value":1}`, now)
	store, err := NewStore(dataDir, StoreOptions{
		Retention:    30 * 24 * time.Hour,
		MaxTotalSize: int64(len(diagnostic.Raw) + 1),
		Now:          func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("初始化遥测存储失败: %v", err)
	}
	if _, err := store.Save(diagnostic); err != nil {
		t.Fatalf("保存诊断失败: %v", err)
	}
	if _, err := store.Save(metric); !errors.Is(err, ErrPayloadExceedsCapacity) {
		t.Fatalf("普通指标不应淘汰诊断，实际错误: %v", err)
	}
	manifest := store.Manifest()
	if len(manifest.Entries) != 1 ||
		manifest.Entries[0].PayloadID != diagnostic.Envelope.PayloadID {
		t.Fatalf("容量拒绝后诊断文件必须保持不变: %+v", manifest.Entries)
	}
}

func TestStoreCleanupExpiresRecordsAndSeenState(t *testing.T) {
	dataDir := t.TempDir()
	now := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	store, err := NewStore(dataDir, StoreOptions{
		Retention:    24 * time.Hour,
		MaxTotalSize: 1 << 20,
		Now:          func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("初始化遥测存储失败: %v", err)
	}
	item := decodeStoredTestEnvelope(t, PayloadKindMetric, `{"cpu":1}`, now)
	if _, err := store.Save(item); err != nil {
		t.Fatalf("保存遥测失败: %v", err)
	}

	now = now.Add(25 * time.Hour)
	removed, err := store.Cleanup()
	if err != nil || removed != 1 {
		t.Fatalf("过期清理应删除 1 条，removed=%d err=%v", removed, err)
	}
	if len(store.Manifest().Entries) != 0 {
		t.Fatalf("过期后清单应为空")
	}
	if status, err := store.Save(item); err != nil || status != "accepted" {
		t.Fatalf("去重状态过期后应允许重新接收，status=%q err=%v", status, err)
	}
}

func TestStoreRejectsManifestPathTraversal(t *testing.T) {
	dataDir := t.TempDir()
	root := filepath.Join(dataDir, "telemetry")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatalf("创建测试目录失败: %v", err)
	}
	payloadID := strings.Repeat("a", 64)
	state := manifestState{
		Version: stateVersion,
		Entries: []ManifestEntry{{
			PayloadID:    payloadID,
			Kind:         PayloadKindMetric,
			CapturedAt:   time.Now().UTC(),
			ReceivedAt:   time.Now().UTC(),
			RelativePath: "../escape.json",
			SizeBytes:    1,
			FileSHA256:   strings.Repeat("b", 64),
		}},
	}
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("编码恶意清单失败: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "manifest.json"), data, 0o600); err != nil {
		t.Fatalf("写入恶意清单失败: %v", err)
	}

	if _, err := NewStore(dataDir, StoreOptions{}); err == nil ||
		!strings.Contains(err.Error(), "路径") {
		t.Fatalf("路径穿越清单应被拒绝，实际错误: %v", err)
	}
}

func decodeStoredTestEnvelope(
	t *testing.T,
	kind PayloadKind,
	payload string,
	now time.Time,
) ValidatedEnvelope {
	t.Helper()
	raw := makeEnvelopeRaw(t, kind, []byte(payload), now)
	items, err := DecodeUploadRequest(makeUploadBody(t, raw), now)
	if err != nil {
		t.Fatalf("创建测试遥测失败: %v", err)
	}
	return items[0]
}
