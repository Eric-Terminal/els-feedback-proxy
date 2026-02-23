package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// TicketStore 负责 issue_number 与 ticket_token 的本地持久化
type TicketStore struct {
	mu      sync.Mutex
	file    string
	records map[string]string
}

type ticketFile struct {
	Records map[string]string `json:"records"`
}

func NewTicketStore(dataDir string) (*TicketStore, error) {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, fmt.Errorf("创建数据目录失败: %w", err)
	}

	filePath := filepath.Join(dataDir, "ticket_tokens.json")
	store := &TicketStore{
		file:    filePath,
		records: make(map[string]string),
	}

	if err := store.load(); err != nil {
		return nil, err
	}

	return store, nil
}

func (s *TicketStore) Set(issueNumber int, token string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := fmt.Sprintf("%d", issueNumber)
	s.records[key] = token
	return s.save()
}

func (s *TicketStore) Validate(issueNumber int, token string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := fmt.Sprintf("%d", issueNumber)
	saved, exists := s.records[key]
	if !exists {
		return false
	}
	return saved == token
}

func (s *TicketStore) load() error {
	data, err := os.ReadFile(s.file)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("读取票据文件失败: %w", err)
	}

	var payload ticketFile
	if err := json.Unmarshal(data, &payload); err != nil {
		return fmt.Errorf("解析票据文件失败: %w", err)
	}

	if payload.Records == nil {
		payload.Records = make(map[string]string)
	}
	s.records = payload.Records
	return nil
}

func (s *TicketStore) save() error {
	payload := ticketFile{Records: s.records}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return fmt.Errorf("编码票据文件失败: %w", err)
	}

	if err := os.WriteFile(s.file, data, 0o600); err != nil {
		return fmt.Errorf("写入票据文件失败: %w", err)
	}
	return nil
}
