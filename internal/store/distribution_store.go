package store

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	distributionFileVersion = 1
	maxDistributionRecords  = 200
	MaxDistributionFileSize = 32 << 20
)

// DistributionRecord 描述一个由官方数据清单下发的文件。
type DistributionRecord struct {
	Key             string    `json:"key"`
	Name            string    `json:"name"`
	DestinationPath string    `json:"destination_path"`
	FileName        string    `json:"file_name"`
	ContentType     string    `json:"content_type"`
	SHA256          string    `json:"sha256"`
	Size            int64     `json:"size"`
	Enabled         bool      `json:"enabled"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// DistributionInput 是管理端可修改的清单字段。
type DistributionInput struct {
	Name            string
	DestinationPath string
	Enabled         bool
}

// DistributionUpload 是一次经过大小限制的文件上传。
type DistributionUpload struct {
	FileName    string
	ContentType string
	Data        []byte
}

type distributionFile struct {
	Version int                  `json:"version"`
	Records []DistributionRecord `json:"records"`
}

// DistributionStore 负责官方数据元信息与不可变文件内容的本地持久化。
type DistributionStore struct {
	mu      sync.RWMutex
	file    string
	blobDir string
	records []DistributionRecord
}

func NewDistributionStore(dataDir string) (*DistributionStore, error) {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, fmt.Errorf("创建数据目录失败: %w", err)
	}

	blobDir := filepath.Join(dataDir, "distribution-files")
	if err := os.MkdirAll(blobDir, 0o700); err != nil {
		return nil, fmt.Errorf("创建官方数据文件目录失败: %w", err)
	}

	store := &DistributionStore{
		file:    filepath.Join(dataDir, "distribution.json"),
		blobDir: blobDir,
	}
	if err := store.load(); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *DistributionStore) List() []DistributionRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()

	records := append([]DistributionRecord{}, s.records...)
	sortDistributionRecords(records)
	return records
}

func (s *DistributionStore) PublicList() []DistributionRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()

	records := make([]DistributionRecord, 0, len(s.records))
	for _, record := range s.records {
		if record.Enabled {
			records = append(records, record)
		}
	}
	sortDistributionRecords(records)
	return records
}

func (s *DistributionStore) Create(
	input DistributionInput,
	upload DistributionUpload,
) (DistributionRecord, error) {
	normalizedInput, normalizedUpload, err := normalizeDistributionInput(input, &upload)
	if err != nil {
		return DistributionRecord{}, err
	}

	key, err := newDistributionKey()
	if err != nil {
		return DistributionRecord{}, err
	}
	now := time.Now().UTC()
	record := recordFromDistributionUpload(key, normalizedInput, *normalizedUpload, now, now)

	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.records) >= maxDistributionRecords {
		return DistributionRecord{}, fmt.Errorf("官方数据条目不能超过 %d 条", maxDistributionRecords)
	}
	if err := s.writeBlobLocked(record.SHA256, normalizedUpload.Data); err != nil {
		return DistributionRecord{}, err
	}

	s.records = append(s.records, record)
	if err := s.saveLocked(); err != nil {
		s.records = s.records[:len(s.records)-1]
		s.removeBlobIfUnusedLocked(record.SHA256)
		return DistributionRecord{}, err
	}
	return record, nil
}

func (s *DistributionStore) Update(
	key string,
	input DistributionInput,
	upload *DistributionUpload,
) (DistributionRecord, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return DistributionRecord{}, fmt.Errorf("官方数据 key 不能为空")
	}

	normalizedInput, normalizedUpload, err := normalizeDistributionInput(input, upload)
	if err != nil {
		return DistributionRecord{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	for index, current := range s.records {
		if current.Key != key {
			continue
		}

		replacement := current
		replacement.Name = normalizedInput.Name
		replacement.DestinationPath = normalizedInput.DestinationPath
		replacement.Enabled = normalizedInput.Enabled
		replacement.UpdatedAt = time.Now().UTC()
		if normalizedUpload != nil {
			replacement = recordFromDistributionUpload(
				current.Key,
				normalizedInput,
				*normalizedUpload,
				current.CreatedAt,
				replacement.UpdatedAt,
			)
			if err := s.writeBlobLocked(replacement.SHA256, normalizedUpload.Data); err != nil {
				return DistributionRecord{}, err
			}
		}

		s.records[index] = replacement
		if err := s.saveLocked(); err != nil {
			s.records[index] = current
			if normalizedUpload != nil && replacement.SHA256 != current.SHA256 {
				s.removeBlobIfUnusedLocked(replacement.SHA256)
			}
			return DistributionRecord{}, err
		}
		if replacement.SHA256 != current.SHA256 {
			s.removeBlobIfUnusedLocked(current.SHA256)
		}
		return replacement, nil
	}
	return DistributionRecord{}, fmt.Errorf("官方数据条目不存在")
}

func (s *DistributionStore) Delete(key string) error {
	key = strings.TrimSpace(key)
	if key == "" {
		return fmt.Errorf("官方数据 key 不能为空")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	for index, record := range s.records {
		if record.Key != key {
			continue
		}

		previous := append([]DistributionRecord{}, s.records...)
		s.records = append(s.records[:index], s.records[index+1:]...)
		if err := s.saveLocked(); err != nil {
			s.records = previous
			return err
		}
		s.removeBlobIfUnusedLocked(record.SHA256)
		return nil
	}
	return fmt.Errorf("官方数据条目不存在")
}

func (s *DistributionStore) PublicFile(
	checksum string,
	fileName string,
) (DistributionRecord, string, bool) {
	checksum = strings.ToLower(strings.TrimSpace(checksum))
	fileName = strings.TrimSpace(fileName)

	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, record := range s.records {
		if record.Enabled && record.SHA256 == checksum && record.FileName == fileName {
			return record, s.blobPath(checksum), true
		}
	}
	return DistributionRecord{}, "", false
}

func (s *DistributionStore) load() error {
	data, err := os.ReadFile(s.file)
	if err != nil {
		if os.IsNotExist(err) {
			s.records = []DistributionRecord{}
			return nil
		}
		return fmt.Errorf("读取官方数据清单失败: %w", err)
	}
	if strings.TrimSpace(string(data)) == "" {
		s.records = []DistributionRecord{}
		return nil
	}

	var payload distributionFile
	if err := json.Unmarshal(data, &payload); err != nil {
		return fmt.Errorf("解析官方数据清单失败: %w", err)
	}
	if payload.Version != distributionFileVersion {
		return fmt.Errorf("不支持的官方数据清单版本: %d", payload.Version)
	}
	if len(payload.Records) > maxDistributionRecords {
		return fmt.Errorf("官方数据条目不能超过 %d 条", maxDistributionRecords)
	}
	for index := range payload.Records {
		record := &payload.Records[index]
		normalizedInput, _, err := normalizeDistributionInput(DistributionInput{
			Name:            record.Name,
			DestinationPath: record.DestinationPath,
			Enabled:         record.Enabled,
		}, nil)
		if err != nil {
			return fmt.Errorf("第 %d 条官方数据无效: %w", index+1, err)
		}
		record.Name = normalizedInput.Name
		record.DestinationPath = normalizedInput.DestinationPath
		if err := validateStoredDistributionRecord(*record); err != nil {
			return fmt.Errorf("第 %d 条官方数据无效: %w", index+1, err)
		}
		info, err := os.Stat(s.blobPath(record.SHA256))
		if err != nil {
			return fmt.Errorf("第 %d 条官方数据文件不可用: %w", index+1, err)
		}
		if info.Size() != record.Size {
			return fmt.Errorf("第 %d 条官方数据文件大小不一致", index+1)
		}
	}
	s.records = append([]DistributionRecord{}, payload.Records...)
	return nil
}

func (s *DistributionStore) saveLocked() error {
	payload := distributionFile{
		Version: distributionFileVersion,
		Records: s.records,
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return fmt.Errorf("编码官方数据清单失败: %w", err)
	}
	data = append(data, '\n')

	temp, err := os.CreateTemp(filepath.Dir(s.file), ".distribution-*.tmp")
	if err != nil {
		return fmt.Errorf("创建官方数据清单临时文件失败: %w", err)
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		return fmt.Errorf("设置官方数据清单权限失败: %w", err)
	}
	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return fmt.Errorf("写入官方数据清单失败: %w", err)
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return fmt.Errorf("同步官方数据清单失败: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("关闭官方数据清单失败: %w", err)
	}
	if err := os.Rename(tempPath, s.file); err != nil {
		return fmt.Errorf("替换官方数据清单失败: %w", err)
	}
	return nil
}

func (s *DistributionStore) writeBlobLocked(checksum string, data []byte) error {
	finalPath := s.blobPath(checksum)
	if info, err := os.Stat(finalPath); err == nil {
		if info.Size() == int64(len(data)) {
			return nil
		}
		return fmt.Errorf("官方数据文件哈希冲突")
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("检查官方数据文件失败: %w", err)
	}

	temp, err := os.CreateTemp(s.blobDir, ".upload-*.tmp")
	if err != nil {
		return fmt.Errorf("创建上传临时文件失败: %w", err)
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		return fmt.Errorf("设置上传文件权限失败: %w", err)
	}
	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return fmt.Errorf("写入上传文件失败: %w", err)
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return fmt.Errorf("同步上传文件失败: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("关闭上传文件失败: %w", err)
	}
	if err := os.Rename(tempPath, finalPath); err != nil {
		return fmt.Errorf("保存上传文件失败: %w", err)
	}
	return nil
}

func (s *DistributionStore) removeBlobIfUnusedLocked(checksum string) {
	for _, record := range s.records {
		if record.SHA256 == checksum {
			return
		}
	}
	_ = os.Remove(s.blobPath(checksum))
}

func (s *DistributionStore) blobPath(checksum string) string {
	return filepath.Join(s.blobDir, checksum+".blob")
}

func normalizeDistributionInput(
	input DistributionInput,
	upload *DistributionUpload,
) (DistributionInput, *DistributionUpload, error) {
	input.Name = strings.TrimSpace(input.Name)
	if input.Name == "" {
		return DistributionInput{}, nil, fmt.Errorf("显示名称不能为空")
	}
	if len([]rune(input.Name)) > 120 {
		return DistributionInput{}, nil, fmt.Errorf("显示名称不能超过 120 个字符")
	}

	normalizedPath, err := normalizeDistributionDestination(input.DestinationPath)
	if err != nil {
		return DistributionInput{}, nil, err
	}
	input.DestinationPath = normalizedPath

	if upload == nil {
		return input, nil, nil
	}
	normalizedUpload := *upload
	normalizedUpload.FileName = sanitizeDistributionFileName(normalizedUpload.FileName)
	if normalizedUpload.FileName == "" {
		return DistributionInput{}, nil, fmt.Errorf("上传文件名无效")
	}
	if len([]rune(normalizedUpload.FileName)) > 255 {
		return DistributionInput{}, nil, fmt.Errorf("上传文件名不能超过 255 个字符")
	}
	if len(normalizedUpload.Data) == 0 {
		return DistributionInput{}, nil, fmt.Errorf("上传文件不能为空")
	}
	if len(normalizedUpload.Data) > MaxDistributionFileSize {
		return DistributionInput{}, nil, fmt.Errorf("上传文件不能超过 32 MiB")
	}
	normalizedUpload.ContentType = strings.TrimSpace(normalizedUpload.ContentType)
	if normalizedUpload.ContentType == "" {
		normalizedUpload.ContentType = "application/octet-stream"
	}
	return input, &normalizedUpload, nil
}

func normalizeDistributionDestination(raw string) (string, error) {
	normalized := strings.ReplaceAll(strings.TrimSpace(raw), "\\", "/")
	normalized = strings.TrimPrefix(normalized, "/")
	if normalized == "" {
		return "", fmt.Errorf("目标目录不能为空")
	}
	for _, segment := range strings.Split(normalized, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return "", fmt.Errorf("目标目录不能包含相对路径")
		}
	}
	cleaned := path.Clean("/" + normalized)
	if cleaned != "/Documents" && !strings.HasPrefix(cleaned, "/Documents/") {
		return "", fmt.Errorf("目标目录必须位于 Documents 内")
	}
	if len([]rune(cleaned)) > 500 {
		return "", fmt.Errorf("目标目录不能超过 500 个字符")
	}
	return cleaned, nil
}

func sanitizeDistributionFileName(raw string) string {
	normalized := strings.ReplaceAll(strings.TrimSpace(raw), "\\", "/")
	name := path.Base(normalized)
	if name == "." || name == "/" {
		return ""
	}
	for _, char := range name {
		if char < 0x20 || char == 0x7f {
			return ""
		}
	}
	return name
}

func validateStoredDistributionRecord(record DistributionRecord) error {
	if strings.TrimSpace(record.Key) == "" {
		return fmt.Errorf("key 不能为空")
	}
	if sanitizeDistributionFileName(record.FileName) != record.FileName {
		return fmt.Errorf("文件名无效")
	}
	if len(record.SHA256) != sha256.Size*2 {
		return fmt.Errorf("SHA256 无效")
	}
	if _, err := hex.DecodeString(record.SHA256); err != nil {
		return fmt.Errorf("SHA256 无效")
	}
	if record.Size <= 0 || record.Size > MaxDistributionFileSize {
		return fmt.Errorf("文件大小无效")
	}
	if record.CreatedAt.IsZero() || record.UpdatedAt.IsZero() {
		return fmt.Errorf("时间字段无效")
	}
	return nil
}

func recordFromDistributionUpload(
	key string,
	input DistributionInput,
	upload DistributionUpload,
	createdAt time.Time,
	updatedAt time.Time,
) DistributionRecord {
	digest := sha256.Sum256(upload.Data)
	return DistributionRecord{
		Key:             key,
		Name:            input.Name,
		DestinationPath: input.DestinationPath,
		FileName:        upload.FileName,
		ContentType:     upload.ContentType,
		SHA256:          hex.EncodeToString(digest[:]),
		Size:            int64(len(upload.Data)),
		Enabled:         input.Enabled,
		CreatedAt:       createdAt,
		UpdatedAt:       updatedAt,
	}
}

func newDistributionKey() (string, error) {
	buffer := make([]byte, 16)
	if _, err := rand.Read(buffer); err != nil {
		return "", fmt.Errorf("生成官方数据 key 失败: %w", err)
	}
	return hex.EncodeToString(buffer), nil
}

func sortDistributionRecords(records []DistributionRecord) {
	sort.SliceStable(records, func(left, right int) bool {
		if records[left].Name != records[right].Name {
			return records[left].Name < records[right].Name
		}
		return records[left].CreatedAt.Before(records[right].CreatedAt)
	})
}
