package telemetry

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const stateVersion = 1

var ErrPayloadExceedsCapacity = errors.New("遥测文件超过服务端可用容量")

type StoreOptions struct {
	Retention    time.Duration
	MaxTotalSize int64
	Now          func() time.Time
}

type ManifestEntry struct {
	PayloadID    string      `json:"payload_id"`
	Kind         PayloadKind `json:"kind"`
	CapturedAt   time.Time   `json:"captured_at"`
	ReceivedAt   time.Time   `json:"received_at"`
	RelativePath string      `json:"relative_path"`
	SizeBytes    int64       `json:"size_bytes"`
	FileSHA256   string      `json:"file_sha256"`
}

type Manifest struct {
	SchemaVersion int             `json:"schema_version"`
	GeneratedAt   time.Time       `json:"generated_at"`
	Entries       []ManifestEntry `json:"entries"`
}

type Status struct {
	SchemaVersion      int        `json:"schema_version"`
	GeneratedAt        time.Time  `json:"generated_at"`
	RetentionDays      int        `json:"retention_days"`
	MaxTotalBytes      int64      `json:"max_total_bytes"`
	TotalBytes         int64      `json:"total_bytes"`
	TotalCount         int        `json:"total_count"`
	MetricCount        int        `json:"metric_count"`
	DiagnosticCount    int        `json:"diagnostic_count"`
	OldestReceived     *time.Time `json:"oldest_received_at,omitempty"`
	NewestReceived     *time.Time `json:"newest_received_at,omitempty"`
	LastReceivedAt     *time.Time `json:"last_received_at,omitempty"`
	LastCleanupAt      *time.Time `json:"last_cleanup_at,omitempty"`
	LastCleanupCount   int        `json:"last_cleanup_count"`
	LastConfirmedAt    *time.Time `json:"last_confirmed_at,omitempty"`
	LastConfirmedCount int        `json:"last_confirmed_count"`
}

type ConfirmResult struct {
	ConfirmedPayloadIDs []string `json:"confirmed_payload_ids"`
	MissingPayloadIDs   []string `json:"missing_payload_ids"`
}

type manifestState struct {
	Version  int             `json:"version"`
	Entries  []ManifestEntry `json:"entries"`
	Activity activityState   `json:"activity"`
}

type seenState struct {
	Version int                  `json:"version"`
	Records map[string]time.Time `json:"records"`
}

type activityState struct {
	LastReceivedAt     *time.Time `json:"last_received_at,omitempty"`
	LastCleanupAt      *time.Time `json:"last_cleanup_at,omitempty"`
	LastCleanupCount   int        `json:"last_cleanup_count"`
	LastConfirmedAt    *time.Time `json:"last_confirmed_at,omitempty"`
	LastConfirmedCount int        `json:"last_confirmed_count"`
}

type Store struct {
	mu           sync.RWMutex
	root         string
	recordsDir   string
	manifestFile string
	seenFile     string
	retention    time.Duration
	maxTotalSize int64
	now          func() time.Time
	entries      map[string]ManifestEntry
	seen         map[string]time.Time
	activity     activityState
}

func NewStore(dataDir string, options StoreOptions) (*Store, error) {
	if options.Retention <= 0 {
		options.Retention = 30 * 24 * time.Hour
	}
	if options.MaxTotalSize <= 0 {
		options.MaxTotalSize = 2 << 30
	}
	if options.Now == nil {
		options.Now = time.Now
	}

	root := filepath.Join(dataDir, "telemetry")
	recordsDir := filepath.Join(root, "records")
	if err := os.MkdirAll(recordsDir, 0o700); err != nil {
		return nil, fmt.Errorf("创建遥测存储目录失败: %w", err)
	}
	store := &Store{
		root:         root,
		recordsDir:   recordsDir,
		manifestFile: filepath.Join(root, "manifest.json"),
		seenFile:     filepath.Join(root, "seen.json"),
		retention:    options.Retention,
		maxTotalSize: options.MaxTotalSize,
		now:          options.Now,
		entries:      make(map[string]ManifestEntry),
		seen:         make(map[string]time.Time),
	}
	if err := store.load(); err != nil {
		return nil, err
	}
	if _, err := store.Cleanup(); err != nil {
		return nil, fmt.Errorf("清理遥测存储失败: %w", err)
	}
	return store, nil
}

func (s *Store) Save(item ValidatedEnvelope) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	payloadID := item.Envelope.PayloadID
	if _, exists := s.seen[payloadID]; exists {
		return "duplicate", nil
	}

	now := s.now().UTC()
	date := now.Format("2006-01-02")
	relativePath := filepath.ToSlash(filepath.Join(
		"records",
		date,
		fmt.Sprintf("%s_%s.json", item.Envelope.Kind, payloadID),
	))
	target, err := s.resolveRelativePath(relativePath)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return "", fmt.Errorf("创建遥测日期目录失败: %w", err)
	}

	data := append(append([]byte(nil), item.Raw...), '\n')
	if err := s.makeRoomLocked(int64(len(data)), item.Envelope.Kind); err != nil {
		return "", err
	}
	if err := writeAtomic(target, data, 0o600); err != nil {
		return "", fmt.Errorf("写入遥测文件失败: %w", err)
	}
	sum := sha256.Sum256(data)
	entry := ManifestEntry{
		PayloadID:    payloadID,
		Kind:         item.Envelope.Kind,
		CapturedAt:   item.Envelope.CapturedAt.UTC(),
		ReceivedAt:   now,
		RelativePath: relativePath,
		SizeBytes:    int64(len(data)),
		FileSHA256:   hex.EncodeToString(sum[:]),
	}
	s.entries[payloadID] = entry
	s.seen[payloadID] = now
	previousActivity := s.activity
	s.activity.LastReceivedAt = &now
	if err := s.saveStateLocked(); err != nil {
		delete(s.entries, payloadID)
		delete(s.seen, payloadID)
		s.activity = previousActivity
		_ = os.Remove(target)
		return "", err
	}
	return "accepted", nil
}

func (s *Store) Manifest() Manifest {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.manifestLocked()
}

func (s *Store) Status() Status {
	s.mu.RLock()
	defer s.mu.RUnlock()

	status := Status{
		SchemaVersion:      AdminSchemaVersion,
		GeneratedAt:        s.now().UTC(),
		RetentionDays:      int(s.retention / (24 * time.Hour)),
		MaxTotalBytes:      s.maxTotalSize,
		TotalCount:         len(s.entries),
		LastReceivedAt:     s.activity.LastReceivedAt,
		LastCleanupAt:      s.activity.LastCleanupAt,
		LastCleanupCount:   s.activity.LastCleanupCount,
		LastConfirmedAt:    s.activity.LastConfirmedAt,
		LastConfirmedCount: s.activity.LastConfirmedCount,
	}
	entries := s.sortedEntriesLocked()
	for _, entry := range entries {
		status.TotalBytes += entry.SizeBytes
		if entry.Kind == PayloadKindDiagnostic {
			status.DiagnosticCount++
		} else {
			status.MetricCount++
		}
	}
	if len(entries) > 0 {
		oldest := entries[0].ReceivedAt
		newest := entries[len(entries)-1].ReceivedAt
		status.OldestReceived = &oldest
		status.NewestReceived = &newest
	}
	return status
}

func (s *Store) Read(payloadID string) (ManifestEntry, []byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	entry, exists := s.entries[strings.TrimSpace(payloadID)]
	if !exists {
		return ManifestEntry{}, nil, fs.ErrNotExist
	}
	target, err := s.resolveRelativePath(entry.RelativePath)
	if err != nil {
		return ManifestEntry{}, nil, err
	}
	data, err := os.ReadFile(target)
	if err != nil {
		return ManifestEntry{}, nil, fmt.Errorf("读取遥测文件失败: %w", err)
	}
	if int64(len(data)) != entry.SizeBytes {
		return ManifestEntry{}, nil, errors.New("遥测文件大小与清单不一致")
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != entry.FileSHA256 {
		return ManifestEntry{}, nil, errors.New("遥测文件校验失败")
	}
	return entry, data, nil
}

func (s *Store) Confirm(payloadIDs []string) (ConfirmResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	unique := make(map[string]struct{}, len(payloadIDs))
	result := ConfirmResult{}
	for _, rawID := range payloadIDs {
		payloadID := strings.TrimSpace(rawID)
		if !lowerHexSHA256.MatchString(payloadID) {
			return ConfirmResult{}, fmt.Errorf("无效 payload_id: %q", rawID)
		}
		if _, duplicate := unique[payloadID]; duplicate {
			continue
		}
		unique[payloadID] = struct{}{}

		entry, exists := s.entries[payloadID]
		if exists {
			target, err := s.resolveRelativePath(entry.RelativePath)
			if err != nil {
				return ConfirmResult{}, err
			}
			if err := os.Remove(target); err != nil && !os.IsNotExist(err) {
				return ConfirmResult{}, fmt.Errorf("删除已确认遥测失败: %w", err)
			}
			delete(s.entries, payloadID)
			result.ConfirmedPayloadIDs = append(result.ConfirmedPayloadIDs, payloadID)
			continue
		}
		if _, previouslySeen := s.seen[payloadID]; previouslySeen {
			result.ConfirmedPayloadIDs = append(result.ConfirmedPayloadIDs, payloadID)
		} else {
			result.MissingPayloadIDs = append(result.MissingPayloadIDs, payloadID)
		}
	}
	sort.Strings(result.ConfirmedPayloadIDs)
	sort.Strings(result.MissingPayloadIDs)
	if len(result.ConfirmedPayloadIDs) > 0 {
		confirmedAt := s.now().UTC()
		s.activity.LastConfirmedAt = &confirmedAt
		s.activity.LastConfirmedCount = len(result.ConfirmedPayloadIDs)
	}
	if err := s.saveManifestLocked(); err != nil {
		return ConfirmResult{}, err
	}
	return result, nil
}

func (s *Store) Cleanup() (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cleanupLocked()
}

func (s *Store) cleanupLocked() (int, error) {
	now := s.now().UTC()
	cutoff := now.Add(-s.retention)
	removed := 0
	total := int64(0)

	for payloadID, entry := range s.entries {
		if entry.ReceivedAt.Before(cutoff) {
			if err := s.removeEntryFileLocked(entry); err != nil {
				return removed, err
			}
			delete(s.entries, payloadID)
			removed++
			continue
		}
		total += entry.SizeBytes
	}

	if total > s.maxTotalSize {
		candidates := s.sortedEntriesLocked()
		sort.SliceStable(candidates, func(i, j int) bool {
			if candidates[i].Kind != candidates[j].Kind {
				return candidates[i].Kind == PayloadKindMetric
			}
			return candidates[i].ReceivedAt.Before(candidates[j].ReceivedAt)
		})
		for _, entry := range candidates {
			if total <= s.maxTotalSize {
				break
			}
			if _, exists := s.entries[entry.PayloadID]; !exists {
				continue
			}
			if err := s.removeEntryFileLocked(entry); err != nil {
				return removed, err
			}
			delete(s.entries, entry.PayloadID)
			total -= entry.SizeBytes
			removed++
		}
	}

	for payloadID, seenAt := range s.seen {
		if seenAt.Before(cutoff) {
			delete(s.seen, payloadID)
		}
	}
	s.activity.LastCleanupAt = &now
	s.activity.LastCleanupCount = removed
	if err := s.saveStateLocked(); err != nil {
		return removed, err
	}
	return removed, nil
}

func (s *Store) makeRoomLocked(required int64, incomingKind PayloadKind) error {
	if required > s.maxTotalSize {
		return ErrPayloadExceedsCapacity
	}

	total := int64(0)
	candidates := make([]ManifestEntry, 0, len(s.entries))
	for _, entry := range s.entries {
		total += entry.SizeBytes
		if incomingKind == PayloadKindDiagnostic || entry.Kind == PayloadKindMetric {
			candidates = append(candidates, entry)
		}
	}
	if total+required <= s.maxTotalSize {
		return nil
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].Kind != candidates[j].Kind {
			return candidates[i].Kind == PayloadKindMetric
		}
		return candidates[i].ReceivedAt.Before(candidates[j].ReceivedAt)
	})

	removable := int64(0)
	for _, entry := range candidates {
		removable += entry.SizeBytes
	}
	if total-removable+required > s.maxTotalSize {
		return ErrPayloadExceedsCapacity
	}
	for _, entry := range candidates {
		if total+required <= s.maxTotalSize {
			break
		}
		if err := s.removeEntryFileLocked(entry); err != nil {
			return err
		}
		delete(s.entries, entry.PayloadID)
		total -= entry.SizeBytes
	}
	return nil
}

func (s *Store) load() error {
	if err := loadJSONState(s.manifestFile, func(data []byte) error {
		var state manifestState
		if err := json.Unmarshal(data, &state); err != nil {
			return err
		}
		if state.Version != stateVersion {
			return fmt.Errorf("不支持的遥测清单版本: %d", state.Version)
		}
		for _, entry := range state.Entries {
			if err := validateManifestEntry(entry); err != nil {
				return err
			}
			s.entries[entry.PayloadID] = entry
		}
		s.activity = state.Activity
		return nil
	}); err != nil {
		return fmt.Errorf("加载遥测清单失败: %w", err)
	}
	if err := loadJSONState(s.seenFile, func(data []byte) error {
		var state seenState
		if err := json.Unmarshal(data, &state); err != nil {
			return err
		}
		if state.Version != stateVersion {
			return fmt.Errorf("不支持的遥测去重状态版本: %d", state.Version)
		}
		for payloadID, seenAt := range state.Records {
			if lowerHexSHA256.MatchString(payloadID) {
				s.seen[payloadID] = seenAt.UTC()
			}
		}
		return nil
	}); err != nil {
		return fmt.Errorf("加载遥测去重状态失败: %w", err)
	}
	for payloadID, entry := range s.entries {
		if _, exists := s.seen[payloadID]; !exists {
			s.seen[payloadID] = entry.ReceivedAt
		}
	}
	if s.activity.LastReceivedAt == nil && len(s.entries) > 0 {
		entries := s.sortedEntriesLocked()
		receivedAt := entries[len(entries)-1].ReceivedAt
		s.activity.LastReceivedAt = &receivedAt
	}
	return s.reconcileLocked()
}

func (s *Store) reconcileLocked() error {
	referenced := make(map[string]struct{}, len(s.entries))
	for payloadID, entry := range s.entries {
		target, err := s.resolveRelativePath(entry.RelativePath)
		if err != nil {
			return fmt.Errorf("遥测清单路径无效: %w", err)
		}
		info, err := os.Stat(target)
		if os.IsNotExist(err) {
			delete(s.entries, payloadID)
			continue
		}
		if err != nil {
			return fmt.Errorf("检查遥测文件失败: %w", err)
		}
		if info.Size() != entry.SizeBytes {
			return fmt.Errorf("遥测文件大小与清单不一致: %s", payloadID)
		}
		data, err := os.ReadFile(target)
		if err != nil {
			return fmt.Errorf("读取遥测文件失败: %w", err)
		}
		sum := sha256.Sum256(data)
		if hex.EncodeToString(sum[:]) != entry.FileSHA256 {
			return fmt.Errorf("遥测文件哈希与清单不一致: %s", payloadID)
		}
		referenced[filepath.Clean(target)] = struct{}{}
	}

	err := filepath.WalkDir(s.recordsDir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || filepath.Ext(path) != ".json" {
			return nil
		}
		if _, exists := referenced[filepath.Clean(path)]; exists {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var envelope Envelope
		if err := decodeStrict(data, &envelope); err != nil {
			return fmt.Errorf("发现无法恢复的遥测文件 %s: %w", path, err)
		}
		if err := envelope.Validate(s.now().UTC()); err != nil {
			return fmt.Errorf("发现无法恢复的遥测文件 %s: %w", path, err)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		relativePath, err := filepath.Rel(s.root, path)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		receivedAt := info.ModTime().UTC()
		s.entries[envelope.PayloadID] = ManifestEntry{
			PayloadID:    envelope.PayloadID,
			Kind:         envelope.Kind,
			CapturedAt:   envelope.CapturedAt.UTC(),
			ReceivedAt:   receivedAt,
			RelativePath: filepath.ToSlash(relativePath),
			SizeBytes:    info.Size(),
			FileSHA256:   hex.EncodeToString(sum[:]),
		}
		s.seen[envelope.PayloadID] = receivedAt
		return nil
	})
	if err != nil {
		return fmt.Errorf("核对遥测文件失败: %w", err)
	}
	return s.saveStateLocked()
}

func (s *Store) manifestLocked() Manifest {
	return Manifest{
		SchemaVersion: AdminSchemaVersion,
		GeneratedAt:   s.now().UTC(),
		Entries:       s.sortedEntriesLocked(),
	}
}

func (s *Store) sortedEntriesLocked() []ManifestEntry {
	entries := make([]ManifestEntry, 0, len(s.entries))
	for _, entry := range s.entries {
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].ReceivedAt.Equal(entries[j].ReceivedAt) {
			return entries[i].PayloadID < entries[j].PayloadID
		}
		return entries[i].ReceivedAt.Before(entries[j].ReceivedAt)
	})
	return entries
}

func (s *Store) saveStateLocked() error {
	if err := s.saveManifestLocked(); err != nil {
		return err
	}
	state := seenState{Version: stateVersion, Records: s.seen}
	if err := writeJSONAtomic(s.seenFile, state); err != nil {
		return fmt.Errorf("保存遥测去重状态失败: %w", err)
	}
	return nil
}

func (s *Store) saveManifestLocked() error {
	state := manifestState{
		Version:  stateVersion,
		Entries:  s.sortedEntriesLocked(),
		Activity: s.activity,
	}
	if err := writeJSONAtomic(s.manifestFile, state); err != nil {
		return fmt.Errorf("保存遥测清单失败: %w", err)
	}
	return nil
}

func (s *Store) removeEntryFileLocked(entry ManifestEntry) error {
	target, err := s.resolveRelativePath(entry.RelativePath)
	if err != nil {
		return err
	}
	if err := os.Remove(target); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("删除遥测文件失败: %w", err)
	}
	return nil
}

func (s *Store) resolveRelativePath(relativePath string) (string, error) {
	cleaned := filepath.Clean(filepath.FromSlash(relativePath))
	if cleaned == "." || filepath.IsAbs(cleaned) || cleaned == ".." ||
		strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return "", errors.New("遥测相对路径无效")
	}
	target := filepath.Join(s.root, cleaned)
	rootPrefix := filepath.Clean(s.root) + string(filepath.Separator)
	if !strings.HasPrefix(filepath.Clean(target), rootPrefix) {
		return "", errors.New("遥测路径越界")
	}
	return target, nil
}

func writeJSONAtomic(path string, payload any) error {
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(path, append(data, '\n'), 0o600)
}

func writeAtomic(path string, data []byte, mode fs.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(mode); err != nil {
		temp.Close()
		return err
	}
	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempPath, path)
}

func loadJSONState(path string, load func([]byte) error) error {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	return load(data)
}

func validateManifestEntry(entry ManifestEntry) error {
	if !lowerHexSHA256.MatchString(entry.PayloadID) {
		return errors.New("遥测清单包含无效 payload_id")
	}
	if entry.Kind != PayloadKindMetric && entry.Kind != PayloadKindDiagnostic {
		return errors.New("遥测清单包含无效 kind")
	}
	if entry.CapturedAt.IsZero() || entry.ReceivedAt.IsZero() {
		return errors.New("遥测清单包含无效时间")
	}
	if entry.SizeBytes <= 0 || !lowerHexSHA256.MatchString(entry.FileSHA256) {
		return errors.New("遥测清单包含无效文件元数据")
	}
	return nil
}
