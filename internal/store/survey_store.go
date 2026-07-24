package store

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
)

const (
	surveyFileVersion        = 1
	maxSurveyRecords         = 100
	maxSurveyResponses       = 50000
	maxSurveyQuestions       = 10
	maxSurveyOptions         = 20
	maxSurveyCustomTextRunes = 1000
)

// SurveyOption 是意见征集问题中的可选项。
type SurveyOption struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

// SurveyQuestion 定义一道单选或多选题。
type SurveyQuestion struct {
	ID         string         `json:"id"`
	Question   string         `json:"question"`
	Type       string         `json:"type"`
	Options    []SurveyOption `json:"options"`
	AllowOther bool           `json:"allow_other,omitempty"`
	Required   bool           `json:"required,omitempty"`
}

// PublicSurvey 是客户端可读取的意见征集定义。
type PublicSurvey struct {
	Key         string           `json:"key"`
	ID          int              `json:"id"`
	Title       string           `json:"title"`
	Description string           `json:"description,omitempty"`
	MinBuild    string           `json:"min_build,omitempty"`
	MaxBuild    string           `json:"max_build,omitempty"`
	Language    string           `json:"language,omitempty"`
	Platform    string           `json:"platform,omitempty"`
	Questions   []SurveyQuestion `json:"questions"`
}

// SurveyRecord 在公开定义之外保存发布状态和管理元数据。
type SurveyRecord struct {
	Key         string           `json:"key"`
	ID          int              `json:"id"`
	Title       string           `json:"title"`
	Description string           `json:"description,omitempty"`
	MinBuild    string           `json:"min_build,omitempty"`
	MaxBuild    string           `json:"max_build,omitempty"`
	Language    string           `json:"language,omitempty"`
	Platform    string           `json:"platform,omitempty"`
	Questions   []SurveyQuestion `json:"questions"`
	Enabled     bool             `json:"enabled"`
	CreatedAt   time.Time        `json:"created_at"`
	UpdatedAt   time.Time        `json:"updated_at"`
}

// SurveyAnswer 是客户端对一道题的匿名回答。
type SurveyAnswer struct {
	QuestionID        string   `json:"question_id"`
	SelectedOptionIDs []string `json:"selected_option_ids,omitempty"`
	OtherText         string   `json:"other_text,omitempty"`
}

// SurveyResponseInput 是客户端提交的匿名答卷。
type SurveyResponseInput struct {
	Answers    []SurveyAnswer `json:"answers"`
	Platform   string         `json:"platform,omitempty"`
	AppVersion string         `json:"app_version,omitempty"`
	AppBuild   string         `json:"app_build,omitempty"`
	Language   string         `json:"language,omitempty"`
}

// SurveyResponseRecord 保存匿名答卷，不记录 IP 或设备标识。
type SurveyResponseRecord struct {
	Key         string         `json:"key"`
	SurveyKey   string         `json:"survey_key"`
	Answers     []SurveyAnswer `json:"answers"`
	Platform    string         `json:"platform,omitempty"`
	AppVersion  string         `json:"app_version,omitempty"`
	AppBuild    string         `json:"app_build,omitempty"`
	Language    string         `json:"language,omitempty"`
	SubmittedAt time.Time      `json:"submitted_at"`
}

type surveyDefinitionFile struct {
	Version int            `json:"version"`
	Records []SurveyRecord `json:"records"`
}

type surveyResponseFile struct {
	Version int                    `json:"version"`
	Records []SurveyResponseRecord `json:"records"`
}

// SurveyStore 分别持久化意见征集定义和匿名答卷。
type SurveyStore struct {
	mu             sync.RWMutex
	definitionFile string
	responseFile   string
	records        []SurveyRecord
	responses      []SurveyResponseRecord
}

func NewSurveyStore(dataDir string) (*SurveyStore, error) {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, fmt.Errorf("创建数据目录失败: %w", err)
	}

	store := &SurveyStore{
		definitionFile: filepath.Join(dataDir, "surveys.json"),
		responseFile:   filepath.Join(dataDir, "survey-responses.json"),
	}
	if err := store.loadDefinitions(); err != nil {
		return nil, err
	}
	if err := store.loadResponses(); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *SurveyStore) List() []SurveyRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()

	records := cloneSurveyRecords(s.records)
	sortSurveyRecords(records)
	return records
}

func (s *SurveyStore) PublicList() []PublicSurvey {
	s.mu.RLock()
	defer s.mu.RUnlock()

	records := make([]SurveyRecord, 0, len(s.records))
	for _, record := range s.records {
		if record.Enabled {
			records = append(records, record)
		}
	}
	sortSurveyRecords(records)

	result := make([]PublicSurvey, 0, len(records))
	for _, record := range records {
		result = append(result, record.Public())
	}
	return result
}

func (s *SurveyStore) Create(record SurveyRecord) (SurveyRecord, error) {
	key, err := newSurveyKey()
	if err != nil {
		return SurveyRecord{}, err
	}
	now := time.Now().UTC()
	record.Key = key
	record.CreatedAt = now
	record.UpdatedAt = now
	normalizeSurveyRecord(&record)
	if err := validateSurveyRecord(record); err != nil {
		return SurveyRecord{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.records) >= maxSurveyRecords {
		return SurveyRecord{}, fmt.Errorf("意见征集不能超过 %d 条", maxSurveyRecords)
	}

	s.records = append(s.records, record)
	if err := s.saveDefinitionsLocked(); err != nil {
		s.records = s.records[:len(s.records)-1]
		return SurveyRecord{}, err
	}
	return cloneSurveyRecord(record), nil
}

func (s *SurveyStore) Update(key string, replacement SurveyRecord) (SurveyRecord, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return SurveyRecord{}, fmt.Errorf("意见征集 key 不能为空")
	}
	replacement.Key = key
	normalizeSurveyRecord(&replacement)
	if err := validateSurveyRecord(replacement); err != nil {
		return SurveyRecord{}, err
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
		if s.responseCountLocked(key) > 0 && !sameSurveyDefinition(current, replacement) {
			return SurveyRecord{}, fmt.Errorf("已有答卷的意见征集不能修改题目内容，只能调整发布状态")
		}

		s.records[index] = replacement
		if err := s.saveDefinitionsLocked(); err != nil {
			s.records[index] = current
			return SurveyRecord{}, err
		}
		return cloneSurveyRecord(replacement), nil
	}
	return SurveyRecord{}, fmt.Errorf("意见征集不存在")
}

func (s *SurveyStore) Delete(key string) error {
	key = strings.TrimSpace(key)
	if key == "" {
		return fmt.Errorf("意见征集 key 不能为空")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	for index, record := range s.records {
		if record.Key != key {
			continue
		}
		if s.responseCountLocked(key) > 0 {
			return fmt.Errorf("已有答卷的意见征集不能删除，请改为停止发布")
		}

		previous := cloneSurveyRecords(s.records)
		s.records = append(s.records[:index], s.records[index+1:]...)
		if err := s.saveDefinitionsLocked(); err != nil {
			s.records = previous
			return err
		}
		return nil
	}
	return fmt.Errorf("意见征集不存在")
}

func (s *SurveyStore) Submit(key string, input SurveyResponseInput) (SurveyResponseRecord, error) {
	key = strings.TrimSpace(key)
	normalizeSurveyResponseInput(&input)

	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.findLocked(key)
	if !ok {
		return SurveyResponseRecord{}, fmt.Errorf("意见征集不存在")
	}
	if !record.Enabled {
		return SurveyResponseRecord{}, fmt.Errorf("意见征集已停止")
	}
	if err := validateSurveyResponse(record, input); err != nil {
		return SurveyResponseRecord{}, err
	}
	if len(s.responses) >= maxSurveyResponses {
		return SurveyResponseRecord{}, fmt.Errorf("匿名答卷存储已达到上限")
	}

	responseKey, err := newSurveyKey()
	if err != nil {
		return SurveyResponseRecord{}, err
	}
	response := SurveyResponseRecord{
		Key:         responseKey,
		SurveyKey:   key,
		Answers:     cloneSurveyAnswers(input.Answers),
		Platform:    input.Platform,
		AppVersion:  input.AppVersion,
		AppBuild:    input.AppBuild,
		Language:    input.Language,
		SubmittedAt: time.Now().UTC(),
	}
	s.responses = append(s.responses, response)
	if err := s.saveResponsesLocked(); err != nil {
		s.responses = s.responses[:len(s.responses)-1]
		return SurveyResponseRecord{}, err
	}
	return response, nil
}

func (s *SurveyStore) Results(key string) (SurveyRecord, []SurveyResponseRecord, error) {
	key = strings.TrimSpace(key)

	s.mu.RLock()
	defer s.mu.RUnlock()
	record, ok := s.findLocked(key)
	if !ok {
		return SurveyRecord{}, nil, fmt.Errorf("意见征集不存在")
	}

	responses := make([]SurveyResponseRecord, 0)
	for _, response := range s.responses {
		if response.SurveyKey == key {
			responses = append(responses, cloneSurveyResponse(response))
		}
	}
	sort.SliceStable(responses, func(i, j int) bool {
		return responses[i].SubmittedAt.After(responses[j].SubmittedAt)
	})
	return cloneSurveyRecord(record), responses, nil
}

func (record SurveyRecord) Public() PublicSurvey {
	return PublicSurvey{
		Key:         record.Key,
		ID:          record.ID,
		Title:       record.Title,
		Description: record.Description,
		MinBuild:    record.MinBuild,
		MaxBuild:    record.MaxBuild,
		Language:    record.Language,
		Platform:    record.Platform,
		Questions:   cloneSurveyQuestions(record.Questions),
	}
}

func (s *SurveyStore) loadDefinitions() error {
	data, err := os.ReadFile(s.definitionFile)
	if err != nil {
		if os.IsNotExist(err) {
			s.records = []SurveyRecord{}
			return nil
		}
		return fmt.Errorf("读取意见征集文件失败: %w", err)
	}
	if strings.TrimSpace(string(data)) == "" {
		s.records = []SurveyRecord{}
		return nil
	}

	var payload surveyDefinitionFile
	if err := json.Unmarshal(data, &payload); err != nil {
		return fmt.Errorf("解析意见征集文件失败: %w", err)
	}
	if payload.Version != surveyFileVersion {
		return fmt.Errorf("不支持的意见征集文件版本: %d", payload.Version)
	}
	if len(payload.Records) > maxSurveyRecords {
		return fmt.Errorf("意见征集不能超过 %d 条", maxSurveyRecords)
	}

	keys := make(map[string]struct{}, len(payload.Records))
	for index := range payload.Records {
		normalizeSurveyRecord(&payload.Records[index])
		if err := validateSurveyRecord(payload.Records[index]); err != nil {
			return fmt.Errorf("第 %d 条意见征集无效: %w", index+1, err)
		}
		if _, exists := keys[payload.Records[index].Key]; exists {
			return fmt.Errorf("意见征集 key 重复: %s", payload.Records[index].Key)
		}
		keys[payload.Records[index].Key] = struct{}{}
	}
	s.records = payload.Records
	return nil
}

func (s *SurveyStore) loadResponses() error {
	data, err := os.ReadFile(s.responseFile)
	if err != nil {
		if os.IsNotExist(err) {
			s.responses = []SurveyResponseRecord{}
			return nil
		}
		return fmt.Errorf("读取匿名答卷文件失败: %w", err)
	}
	if strings.TrimSpace(string(data)) == "" {
		s.responses = []SurveyResponseRecord{}
		return nil
	}

	var payload surveyResponseFile
	if err := json.Unmarshal(data, &payload); err != nil {
		return fmt.Errorf("解析匿名答卷文件失败: %w", err)
	}
	if payload.Version != surveyFileVersion {
		return fmt.Errorf("不支持的匿名答卷文件版本: %d", payload.Version)
	}
	if len(payload.Records) > maxSurveyResponses {
		return fmt.Errorf("匿名答卷不能超过 %d 条", maxSurveyResponses)
	}
	for index := range payload.Records {
		normalizeSurveyResponseRecord(&payload.Records[index])
		survey, ok := s.findLocked(payload.Records[index].SurveyKey)
		if !ok {
			return fmt.Errorf("第 %d 份匿名答卷关联的意见征集不存在", index+1)
		}
		if payload.Records[index].Key == "" || payload.Records[index].SubmittedAt.IsZero() {
			return fmt.Errorf("第 %d 份匿名答卷元数据无效", index+1)
		}
		if err := validateSurveyResponse(survey, SurveyResponseInput{
			Answers:  payload.Records[index].Answers,
			Platform: payload.Records[index].Platform,
			AppBuild: payload.Records[index].AppBuild,
			Language: payload.Records[index].Language,
		}); err != nil {
			return fmt.Errorf("第 %d 份匿名答卷无效: %w", index+1, err)
		}
	}
	s.responses = payload.Records
	return nil
}

func (s *SurveyStore) saveDefinitionsLocked() error {
	return writeSurveyJSONAtomically(
		s.definitionFile,
		".surveys-*.tmp",
		surveyDefinitionFile{Version: surveyFileVersion, Records: s.records},
		"意见征集",
	)
}

func (s *SurveyStore) saveResponsesLocked() error {
	return writeSurveyJSONAtomically(
		s.responseFile,
		".survey-responses-*.tmp",
		surveyResponseFile{Version: surveyFileVersion, Records: s.responses},
		"匿名答卷",
	)
}

func writeSurveyJSONAtomically(file, pattern string, payload any, label string) error {
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return fmt.Errorf("编码%s文件失败: %w", label, err)
	}
	data = append(data, '\n')

	temp, err := os.CreateTemp(filepath.Dir(file), pattern)
	if err != nil {
		return fmt.Errorf("创建%s临时文件失败: %w", label, err)
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)

	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		return fmt.Errorf("设置%s文件权限失败: %w", label, err)
	}
	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return fmt.Errorf("写入%s临时文件失败: %w", label, err)
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return fmt.Errorf("同步%s临时文件失败: %w", label, err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("关闭%s临时文件失败: %w", label, err)
	}
	if err := os.Rename(tempPath, file); err != nil {
		return fmt.Errorf("替换%s文件失败: %w", label, err)
	}
	return nil
}

func normalizeSurveyRecord(record *SurveyRecord) {
	record.Key = strings.TrimSpace(record.Key)
	record.Title = strings.TrimSpace(record.Title)
	record.Description = strings.TrimSpace(record.Description)
	record.MinBuild = strings.TrimSpace(record.MinBuild)
	record.MaxBuild = strings.TrimSpace(record.MaxBuild)
	record.Language = strings.TrimSpace(record.Language)
	record.Platform = normalizeAnnouncementPlatform(record.Platform)
	for questionIndex := range record.Questions {
		question := &record.Questions[questionIndex]
		question.ID = strings.TrimSpace(question.ID)
		question.Question = strings.TrimSpace(question.Question)
		question.Type = strings.ToLower(strings.TrimSpace(question.Type))
		for optionIndex := range question.Options {
			option := &question.Options[optionIndex]
			option.ID = strings.TrimSpace(option.ID)
			option.Label = strings.TrimSpace(option.Label)
			option.Description = strings.TrimSpace(option.Description)
		}
	}
}

func normalizeSurveyResponseInput(input *SurveyResponseInput) {
	input.Platform = normalizeAnnouncementPlatform(input.Platform)
	input.AppVersion = strings.TrimSpace(input.AppVersion)
	input.AppBuild = strings.TrimSpace(input.AppBuild)
	input.Language = strings.TrimSpace(input.Language)
	for index := range input.Answers {
		answer := &input.Answers[index]
		answer.QuestionID = strings.TrimSpace(answer.QuestionID)
		answer.OtherText = strings.TrimSpace(answer.OtherText)
		selected := make([]string, 0, len(answer.SelectedOptionIDs))
		seen := make(map[string]struct{}, len(answer.SelectedOptionIDs))
		for _, rawID := range answer.SelectedOptionIDs {
			optionID := strings.TrimSpace(rawID)
			if optionID == "" {
				continue
			}
			if _, exists := seen[optionID]; exists {
				continue
			}
			seen[optionID] = struct{}{}
			selected = append(selected, optionID)
		}
		answer.SelectedOptionIDs = selected
	}
}

func normalizeSurveyResponseRecord(record *SurveyResponseRecord) {
	record.Key = strings.TrimSpace(record.Key)
	record.SurveyKey = strings.TrimSpace(record.SurveyKey)
	input := SurveyResponseInput{
		Answers:    record.Answers,
		Platform:   record.Platform,
		AppVersion: record.AppVersion,
		AppBuild:   record.AppBuild,
		Language:   record.Language,
	}
	normalizeSurveyResponseInput(&input)
	record.Answers = input.Answers
	record.Platform = input.Platform
	record.AppVersion = input.AppVersion
	record.AppBuild = input.AppBuild
	record.Language = input.Language
}

func validateSurveyRecord(record SurveyRecord) error {
	if record.Key == "" {
		return fmt.Errorf("意见征集 key 不能为空")
	}
	if record.ID <= 0 {
		return fmt.Errorf("意见征集编号必须大于 0")
	}
	if count := len([]rune(record.Title)); count < 1 || count > 200 {
		return fmt.Errorf("标题长度必须在 1 到 200 个字符之间")
	}
	if len([]rune(record.Description)) > 2000 {
		return fmt.Errorf("说明不能超过 2000 个字符")
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
	if len(record.Questions) < 1 || len(record.Questions) > maxSurveyQuestions {
		return fmt.Errorf("题目数量必须在 1 到 %d 道之间", maxSurveyQuestions)
	}

	questionIDs := make(map[string]struct{}, len(record.Questions))
	for questionIndex, question := range record.Questions {
		if !validSurveyIdentifier(question.ID) {
			return fmt.Errorf("第 %d 道题的 ID 无效", questionIndex+1)
		}
		if _, exists := questionIDs[question.ID]; exists {
			return fmt.Errorf("题目 ID 不能重复: %s", question.ID)
		}
		questionIDs[question.ID] = struct{}{}
		if count := len([]rune(question.Question)); count < 1 || count > 500 {
			return fmt.Errorf("第 %d 道题长度必须在 1 到 500 个字符之间", questionIndex+1)
		}
		if question.Type != "single_select" && question.Type != "multi_select" {
			return fmt.Errorf("第 %d 道题仅支持 single_select 或 multi_select", questionIndex+1)
		}
		if len(question.Options) < 1 || len(question.Options) > maxSurveyOptions {
			return fmt.Errorf("第 %d 道题的选项数量必须在 1 到 %d 个之间", questionIndex+1, maxSurveyOptions)
		}

		optionIDs := make(map[string]struct{}, len(question.Options))
		for optionIndex, option := range question.Options {
			if !validSurveyIdentifier(option.ID) {
				return fmt.Errorf("第 %d 道题第 %d 个选项的 ID 无效", questionIndex+1, optionIndex+1)
			}
			if _, exists := optionIDs[option.ID]; exists {
				return fmt.Errorf("第 %d 道题的选项 ID 不能重复: %s", questionIndex+1, option.ID)
			}
			optionIDs[option.ID] = struct{}{}
			if count := len([]rune(option.Label)); count < 1 || count > 200 {
				return fmt.Errorf("第 %d 道题第 %d 个选项长度必须在 1 到 200 个字符之间", questionIndex+1, optionIndex+1)
			}
			if len([]rune(option.Description)) > 500 {
				return fmt.Errorf("第 %d 道题第 %d 个选项说明不能超过 500 个字符", questionIndex+1, optionIndex+1)
			}
		}
	}
	return nil
}

func validateSurveyResponse(survey SurveyRecord, input SurveyResponseInput) error {
	if len(input.Answers) > len(survey.Questions) {
		return fmt.Errorf("答卷包含未知题目")
	}
	if input.Platform != "" && input.Platform != "iOS" && input.Platform != "watchOS" {
		return fmt.Errorf("平台仅支持 iOS 或 watchOS")
	}
	if len([]rune(input.AppVersion)) > 32 {
		return fmt.Errorf("应用版本不能超过 32 个字符")
	}
	if len([]rune(input.AppBuild)) > 32 {
		return fmt.Errorf("构建号不能超过 32 个字符")
	}
	if len([]rune(input.Language)) > 32 {
		return fmt.Errorf("语言标识不能超过 32 个字符")
	}

	answers := make(map[string]SurveyAnswer, len(input.Answers))
	for _, answer := range input.Answers {
		if _, exists := answers[answer.QuestionID]; exists {
			return fmt.Errorf("题目回答不能重复: %s", answer.QuestionID)
		}
		answers[answer.QuestionID] = answer
	}

	for _, question := range survey.Questions {
		answer, exists := answers[question.ID]
		if !exists {
			if question.Required {
				return fmt.Errorf("请回答必填题目: %s", question.Question)
			}
			continue
		}
		if question.Type == "single_select" && len(answer.SelectedOptionIDs) > 1 {
			return fmt.Errorf("单选题只能选择一个选项: %s", question.Question)
		}
		if answer.OtherText != "" && !question.AllowOther {
			return fmt.Errorf("题目不允许自定义输入: %s", question.Question)
		}
		if len([]rune(answer.OtherText)) > maxSurveyCustomTextRunes {
			return fmt.Errorf("自定义输入不能超过 %d 个字符", maxSurveyCustomTextRunes)
		}

		validOptions := make(map[string]struct{}, len(question.Options))
		for _, option := range question.Options {
			validOptions[option.ID] = struct{}{}
		}
		for _, optionID := range answer.SelectedOptionIDs {
			if _, valid := validOptions[optionID]; !valid {
				return fmt.Errorf("题目包含未知选项: %s", question.Question)
			}
		}
		if question.Required && len(answer.SelectedOptionIDs) == 0 && answer.OtherText == "" {
			return fmt.Errorf("请回答必填题目: %s", question.Question)
		}
		delete(answers, question.ID)
	}
	if len(answers) > 0 {
		return fmt.Errorf("答卷包含未知题目")
	}
	return nil
}

func validSurveyIdentifier(value string) bool {
	if value == "" || len(value) > 64 {
		return false
	}
	for _, character := range value {
		if unicode.IsLetter(character) || unicode.IsDigit(character) ||
			character == '-' || character == '_' || character == '.' {
			continue
		}
		return false
	}
	return true
}

func sameSurveyDefinition(left, right SurveyRecord) bool {
	return left.ID == right.ID &&
		left.Title == right.Title &&
		left.Description == right.Description &&
		left.MinBuild == right.MinBuild &&
		left.MaxBuild == right.MaxBuild &&
		left.Language == right.Language &&
		left.Platform == right.Platform &&
		reflect.DeepEqual(left.Questions, right.Questions)
}

func (s *SurveyStore) findLocked(key string) (SurveyRecord, bool) {
	for _, record := range s.records {
		if record.Key == key {
			return record, true
		}
	}
	return SurveyRecord{}, false
}

func (s *SurveyStore) responseCountLocked(key string) int {
	count := 0
	for _, response := range s.responses {
		if response.SurveyKey == key {
			count++
		}
	}
	return count
}

func sortSurveyRecords(records []SurveyRecord) {
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

func cloneSurveyRecords(records []SurveyRecord) []SurveyRecord {
	result := make([]SurveyRecord, len(records))
	for index, record := range records {
		result[index] = cloneSurveyRecord(record)
	}
	return result
}

func cloneSurveyRecord(record SurveyRecord) SurveyRecord {
	record.Questions = cloneSurveyQuestions(record.Questions)
	return record
}

func cloneSurveyQuestions(questions []SurveyQuestion) []SurveyQuestion {
	result := make([]SurveyQuestion, len(questions))
	for index, question := range questions {
		result[index] = question
		result[index].Options = append([]SurveyOption(nil), question.Options...)
	}
	return result
}

func cloneSurveyAnswers(answers []SurveyAnswer) []SurveyAnswer {
	result := make([]SurveyAnswer, len(answers))
	for index, answer := range answers {
		result[index] = answer
		result[index].SelectedOptionIDs = append([]string(nil), answer.SelectedOptionIDs...)
	}
	return result
}

func cloneSurveyResponse(response SurveyResponseRecord) SurveyResponseRecord {
	response.Answers = cloneSurveyAnswers(response.Answers)
	return response
}

func newSurveyKey() (string, error) {
	bytes := make([]byte, 12)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("生成意见征集 key 失败: %w", err)
	}
	return hex.EncodeToString(bytes), nil
}
