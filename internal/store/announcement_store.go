package store

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	announcementFileVersion = 1
	maxAnnouncementRecords  = 500
)

// PublicAnnouncement 是向客户端公开的兼容公告结构。
type PublicAnnouncement struct {
	ID       int    `json:"id"`
	Type     string `json:"type"`
	MinBuild string `json:"min_build,omitempty"`
	MaxBuild string `json:"max_build,omitempty"`
	Language string `json:"language,omitempty"`
	Platform string `json:"platform,omitempty"`
	Title    string `json:"title"`
	Body     string `json:"body"`
}

// AnnouncementRecord 在公开公告字段之外保存管理状态。
type AnnouncementRecord struct {
	Key       string    `json:"key"`
	ID        int       `json:"id"`
	Type      string    `json:"type"`
	MinBuild  string    `json:"min_build,omitempty"`
	MaxBuild  string    `json:"max_build,omitempty"`
	Language  string    `json:"language,omitempty"`
	Platform  string    `json:"platform,omitempty"`
	Title     string    `json:"title"`
	Body      string    `json:"body"`
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type announcementFile struct {
	Version int                  `json:"version"`
	Records []AnnouncementRecord `json:"records"`
}

// AnnouncementStore 负责公告内容与发布状态的本地持久化。
type AnnouncementStore struct {
	mu      sync.RWMutex
	file    string
	records []AnnouncementRecord
}

func NewAnnouncementStore(dataDir string) (*AnnouncementStore, error) {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, fmt.Errorf("创建数据目录失败: %w", err)
	}

	store := &AnnouncementStore{
		file: filepath.Join(dataDir, "announcements.json"),
	}
	if err := store.load(); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *AnnouncementStore) List() []AnnouncementRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()

	records := append([]AnnouncementRecord{}, s.records...)
	sortAnnouncementRecords(records)
	return records
}

func (s *AnnouncementStore) PublicList() []PublicAnnouncement {
	s.mu.RLock()
	defer s.mu.RUnlock()

	records := make([]AnnouncementRecord, 0, len(s.records))
	for _, record := range s.records {
		if record.Enabled {
			records = append(records, record)
		}
	}
	sortAnnouncementRecords(records)

	result := make([]PublicAnnouncement, 0, len(records))
	for _, record := range records {
		result = append(result, record.Public())
	}
	return result
}

func (s *AnnouncementStore) Create(record AnnouncementRecord) (AnnouncementRecord, error) {
	now := time.Now().UTC()
	key, err := newAnnouncementKey()
	if err != nil {
		return AnnouncementRecord{}, err
	}
	record.Key = key
	record.CreatedAt = now
	record.UpdatedAt = now
	normalizeAnnouncementRecord(&record)
	if err := validateAnnouncementRecord(record); err != nil {
		return AnnouncementRecord{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.records) >= maxAnnouncementRecords {
		return AnnouncementRecord{}, fmt.Errorf("公告条目不能超过 %d 条", maxAnnouncementRecords)
	}

	s.records = append(s.records, record)
	if err := s.saveLocked(); err != nil {
		s.records = s.records[:len(s.records)-1]
		return AnnouncementRecord{}, err
	}
	return record, nil
}

func (s *AnnouncementStore) Update(key string, replacement AnnouncementRecord) (AnnouncementRecord, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return AnnouncementRecord{}, fmt.Errorf("公告 key 不能为空")
	}
	replacement.Key = key
	normalizeAnnouncementRecord(&replacement)
	if err := validateAnnouncementRecord(replacement); err != nil {
		return AnnouncementRecord{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	for index, current := range s.records {
		if current.Key != key {
			continue
		}

		replacement.Key = current.Key
		replacement.CreatedAt = current.CreatedAt
		replacement.UpdatedAt = time.Now().UTC()
		s.records[index] = replacement
		if err := s.saveLocked(); err != nil {
			s.records[index] = current
			return AnnouncementRecord{}, err
		}
		return replacement, nil
	}
	return AnnouncementRecord{}, fmt.Errorf("公告不存在")
}

func (s *AnnouncementStore) Delete(key string) error {
	key = strings.TrimSpace(key)
	if key == "" {
		return fmt.Errorf("公告 key 不能为空")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	for index, record := range s.records {
		if record.Key != key {
			continue
		}

		previous := append([]AnnouncementRecord(nil), s.records...)
		s.records = append(s.records[:index], s.records[index+1:]...)
		if err := s.saveLocked(); err != nil {
			s.records = previous
			return err
		}
		return nil
	}
	return fmt.Errorf("公告不存在")
}

func (r AnnouncementRecord) Public() PublicAnnouncement {
	return PublicAnnouncement{
		ID:       r.ID,
		Type:     r.Type,
		MinBuild: r.MinBuild,
		MaxBuild: r.MaxBuild,
		Language: r.Language,
		Platform: r.Platform,
		Title:    r.Title,
		Body:     r.Body,
	}
}

func (s *AnnouncementStore) load() error {
	data, err := os.ReadFile(s.file)
	if err != nil {
		if os.IsNotExist(err) {
			s.records = []AnnouncementRecord{}
			return nil
		}
		return fmt.Errorf("读取公告文件失败: %w", err)
	}

	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		s.records = []AnnouncementRecord{}
		return nil
	}

	var payload announcementFile
	if err := json.Unmarshal(data, &payload); err != nil {
		return fmt.Errorf("解析公告文件失败: %w", err)
	}
	if payload.Version != announcementFileVersion {
		return fmt.Errorf("不支持的公告文件版本: %d", payload.Version)
	}
	if len(payload.Records) > maxAnnouncementRecords {
		return fmt.Errorf("公告条目不能超过 %d 条", maxAnnouncementRecords)
	}
	for index := range payload.Records {
		normalizeAnnouncementRecord(&payload.Records[index])
		if err := validateAnnouncementRecord(payload.Records[index]); err != nil {
			return fmt.Errorf("第 %d 条公告无效: %w", index+1, err)
		}
	}
	s.records = payload.Records
	return nil
}

func (s *AnnouncementStore) saveLocked() error {
	payload := announcementFile{
		Version: announcementFileVersion,
		Records: s.records,
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return fmt.Errorf("编码公告文件失败: %w", err)
	}
	data = append(data, '\n')

	temp, err := os.CreateTemp(filepath.Dir(s.file), ".announcements-*.tmp")
	if err != nil {
		return fmt.Errorf("创建公告临时文件失败: %w", err)
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)

	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		return fmt.Errorf("设置公告文件权限失败: %w", err)
	}
	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return fmt.Errorf("写入公告临时文件失败: %w", err)
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return fmt.Errorf("同步公告临时文件失败: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("关闭公告临时文件失败: %w", err)
	}
	if err := os.Rename(tempPath, s.file); err != nil {
		return fmt.Errorf("替换公告文件失败: %w", err)
	}
	return nil
}

func normalizeAnnouncementRecord(record *AnnouncementRecord) {
	record.Key = strings.TrimSpace(record.Key)
	record.Type = strings.ToLower(strings.TrimSpace(record.Type))
	record.MinBuild = strings.TrimSpace(record.MinBuild)
	record.MaxBuild = strings.TrimSpace(record.MaxBuild)
	record.Language = strings.TrimSpace(record.Language)
	record.Platform = normalizeAnnouncementPlatform(record.Platform)
	record.Title = strings.TrimSpace(record.Title)
	record.Body = strings.TrimSpace(record.Body)
}

func validateAnnouncementRecord(record AnnouncementRecord) error {
	if record.Key == "" {
		return fmt.Errorf("公告 key 不能为空")
	}
	if record.ID <= 0 {
		return fmt.Errorf("公告编号必须大于 0")
	}
	switch record.Type {
	case "info", "warning", "blocking":
	default:
		return fmt.Errorf("公告类型仅支持 info、warning 或 blocking")
	}
	if err := validateBuildRange(record.MinBuild, record.MaxBuild); err != nil {
		return err
	}
	if len([]rune(record.Language)) > 32 {
		return fmt.Errorf("语言标识不能超过 32 个字符")
	}
	if record.Platform != "" && record.Platform != "iOS" && record.Platform != "watchOS" {
		return fmt.Errorf("平台仅支持 iOS 或 watchOS")
	}
	titleLength := len([]rune(record.Title))
	if titleLength < 1 || titleLength > 200 {
		return fmt.Errorf("标题长度必须在 1 到 200 个字符之间")
	}
	bodyLength := len([]rune(record.Body))
	if bodyLength < 1 || bodyLength > 20000 {
		return fmt.Errorf("正文长度必须在 1 到 20000 个字符之间")
	}
	return nil
}

func validateBuildRange(minBuild, maxBuild string) error {
	parse := func(label, value string) (int, error) {
		if value == "" {
			return 0, nil
		}
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 0 {
			return 0, fmt.Errorf("%s必须是非负整数", label)
		}
		return parsed, nil
	}

	minimum, err := parse("最低构建号", minBuild)
	if err != nil {
		return err
	}
	maximum, err := parse("最高构建号", maxBuild)
	if err != nil {
		return err
	}
	if minBuild != "" && maxBuild != "" && minimum > maximum {
		return fmt.Errorf("最低构建号不能高于最高构建号")
	}
	return nil
}

func normalizeAnnouncementPlatform(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "":
		return ""
	case "ios":
		return "iOS"
	case "watchos":
		return "watchOS"
	default:
		return strings.TrimSpace(value)
	}
}

func sortAnnouncementRecords(records []AnnouncementRecord) {
	sort.SliceStable(records, func(i, j int) bool {
		if records[i].ID != records[j].ID {
			return records[i].ID > records[j].ID
		}
		if records[i].Language != records[j].Language {
			return records[i].Language < records[j].Language
		}
		return records[i].CreatedAt.Before(records[j].CreatedAt)
	})
}

func newAnnouncementKey() (string, error) {
	bytes := make([]byte, 12)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("生成公告 key 失败: %w", err)
	}
	return hex.EncodeToString(bytes), nil
}
