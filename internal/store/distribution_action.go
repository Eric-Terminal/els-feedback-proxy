package store

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"reflect"
	"sort"
	"strings"
	"time"
)

const (
	OfficialDataActionKindProviderUpsert = "provider.upsert"
	officialDataActionSchemaVersion      = 1
)

// OfficialDataActionRecord 描述一个由客户端预览并应用的数据库操作载荷。
type OfficialDataActionRecord struct {
	Key             string                      `json:"key"`
	ID              string                      `json:"id"`
	Revision        int                         `json:"revision"`
	Kind            string                      `json:"kind"`
	ProviderID      string                      `json:"provider_id"`
	ApplyOn         []string                    `json:"apply_on"`
	MinimumAppBuild *int                        `json:"minimum_app_build,omitempty"`
	Platforms       []string                    `json:"platforms,omitempty"`
	MergePolicy     OfficialProviderMergePolicy `json:"merge_policy"`
	FileName        string                      `json:"file_name"`
	ContentType     string                      `json:"content_type"`
	SHA256          string                      `json:"sha256"`
	Size            int64                       `json:"size"`
	Enabled         bool                        `json:"enabled"`
	CreatedAt       time.Time                   `json:"created_at"`
	UpdatedAt       time.Time                   `json:"updated_at"`
}

type OfficialDataActionInput struct {
	Enabled bool
}

// OfficialDataActionBundle 是上传到管理端的可读操作配方。
type OfficialDataActionBundle struct {
	SchemaVersion   int                         `json:"schema_version"`
	ID              string                      `json:"id"`
	Revision        int                         `json:"revision"`
	Kind            string                      `json:"kind"`
	ApplyOn         []string                    `json:"apply_on"`
	MinimumAppBuild *int                        `json:"minimum_app_build,omitempty"`
	Platforms       []string                    `json:"platforms,omitempty"`
	Provider        json.RawMessage             `json:"provider"`
	MergePolicy     OfficialProviderMergePolicy `json:"merge_policy"`
}

type OfficialProviderMergePolicy struct {
	ProviderFields     string                           `json:"provider_fields"`
	APIKeys            string                           `json:"api_keys"`
	HeaderOverrides    string                           `json:"header_overrides"`
	ProxyConfiguration string                           `json:"proxy_configuration"`
	Models             OfficialProviderModelMergePolicy `json:"models"`
	IfUserDeleted      string                           `json:"if_user_deleted"`
}

type OfficialProviderModelMergePolicy struct {
	Fields      string `json:"fields"`
	IsActivated string `json:"is_activated"`
	OnMissing   string `json:"on_missing"`
	OnRemoved   string `json:"on_removed"`
	UserModels  string `json:"user_models"`
}

type officialProviderValidationPayload struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	BaseURL   string `json:"baseURL"`
	APIFormat string `json:"apiFormat"`
	Models    []struct {
		ID        string `json:"id"`
		ModelName string `json:"modelName"`
	} `json:"models"`
}

func DecodeOfficialDataActionBundle(data []byte) (OfficialDataActionBundle, error) {
	var bundle OfficialDataActionBundle
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	if err := decoder.Decode(&bundle); err != nil {
		return OfficialDataActionBundle{}, fmt.Errorf("操作载荷不是有效 JSON: %w", err)
	}
	if err := validateOfficialDataActionBundle(bundle); err != nil {
		return OfficialDataActionBundle{}, err
	}
	return bundle, nil
}

func (s *DistributionStore) ListActions() []OfficialDataActionRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()
	actions := append([]OfficialDataActionRecord{}, s.actions...)
	sortOfficialDataActions(actions)
	return actions
}

func (s *DistributionStore) PublicActions() []OfficialDataActionRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()
	actions := make([]OfficialDataActionRecord, 0, len(s.actions))
	for _, action := range s.actions {
		if action.Enabled {
			actions = append(actions, action)
		}
	}
	sortOfficialDataActions(actions)
	return actions
}

func (s *DistributionStore) CreateAction(
	input OfficialDataActionInput,
	upload DistributionUpload,
) (OfficialDataActionRecord, error) {
	normalizedUpload, bundle, err := normalizeOfficialDataActionUpload(upload)
	if err != nil {
		return OfficialDataActionRecord{}, err
	}
	key, err := newDistributionKey()
	if err != nil {
		return OfficialDataActionRecord{}, err
	}
	now := time.Now().UTC()
	record := officialDataActionRecordFromUpload(key, input, normalizedUpload, bundle, now, now)

	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.actions) >= maxDistributionActions {
		return OfficialDataActionRecord{}, fmt.Errorf("官方数据库操作不能超过 %d 条", maxDistributionActions)
	}
	if s.actionIDExistsLocked(record.ID, "") {
		return OfficialDataActionRecord{}, fmt.Errorf("操作 ID 已存在")
	}
	if s.actionProviderIDExistsLocked(record.ProviderID, "") {
		return OfficialDataActionRecord{}, fmt.Errorf("同一提供商只能配置一个官方操作")
	}
	if err := s.writeBlobLocked(record.SHA256, normalizedUpload.Data); err != nil {
		return OfficialDataActionRecord{}, err
	}
	s.actions = append(s.actions, record)
	if err := s.saveLocked(); err != nil {
		s.actions = s.actions[:len(s.actions)-1]
		s.removeBlobIfUnusedLocked(record.SHA256)
		return OfficialDataActionRecord{}, err
	}
	return record, nil
}

func (s *DistributionStore) UpdateAction(
	key string,
	input OfficialDataActionInput,
	upload *DistributionUpload,
) (OfficialDataActionRecord, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return OfficialDataActionRecord{}, fmt.Errorf("官方数据库操作 key 不能为空")
	}

	var normalizedUpload *DistributionUpload
	var bundle *OfficialDataActionBundle
	if upload != nil {
		normalized, decoded, err := normalizeOfficialDataActionUpload(*upload)
		if err != nil {
			return OfficialDataActionRecord{}, err
		}
		normalizedUpload = &normalized
		bundle = &decoded
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	for index, current := range s.actions {
		if current.Key != key {
			continue
		}
		replacement := current
		replacement.Enabled = input.Enabled
		replacement.UpdatedAt = time.Now().UTC()
		if normalizedUpload != nil && bundle != nil {
			replacement = officialDataActionRecordFromUpload(
				current.Key,
				input,
				*normalizedUpload,
				*bundle,
				current.CreatedAt,
				replacement.UpdatedAt,
			)
			if s.actionIDExistsLocked(replacement.ID, current.Key) {
				return OfficialDataActionRecord{}, fmt.Errorf("操作 ID 已存在")
			}
			if s.actionProviderIDExistsLocked(replacement.ProviderID, current.Key) {
				return OfficialDataActionRecord{}, fmt.Errorf("同一提供商只能配置一个官方操作")
			}
			if replacement.ID == current.ID {
				if replacement.Revision < current.Revision {
					return OfficialDataActionRecord{}, fmt.Errorf("同一操作的新 revision 不能低于现有版本")
				}
				if replacement.Revision == current.Revision && replacement.SHA256 != current.SHA256 {
					return OfficialDataActionRecord{}, fmt.Errorf("同一 revision 的操作内容不能改变")
				}
			}
			if err := s.writeBlobLocked(replacement.SHA256, normalizedUpload.Data); err != nil {
				return OfficialDataActionRecord{}, err
			}
		}

		s.actions[index] = replacement
		if err := s.saveLocked(); err != nil {
			s.actions[index] = current
			if replacement.SHA256 != current.SHA256 {
				s.removeBlobIfUnusedLocked(replacement.SHA256)
			}
			return OfficialDataActionRecord{}, err
		}
		if replacement.SHA256 != current.SHA256 {
			s.removeBlobIfUnusedLocked(current.SHA256)
		}
		return replacement, nil
	}
	return OfficialDataActionRecord{}, fmt.Errorf("官方数据库操作不存在")
}

func (s *DistributionStore) DeleteAction(key string) error {
	key = strings.TrimSpace(key)
	if key == "" {
		return fmt.Errorf("官方数据库操作 key 不能为空")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for index, action := range s.actions {
		if action.Key != key {
			continue
		}
		previous := append([]OfficialDataActionRecord{}, s.actions...)
		s.actions = append(s.actions[:index], s.actions[index+1:]...)
		if err := s.saveLocked(); err != nil {
			s.actions = previous
			return err
		}
		s.removeBlobIfUnusedLocked(action.SHA256)
		return nil
	}
	return fmt.Errorf("官方数据库操作不存在")
}

func normalizeOfficialDataActionUpload(
	upload DistributionUpload,
) (DistributionUpload, OfficialDataActionBundle, error) {
	normalized := upload
	normalized.FileName = sanitizeDistributionFileName(upload.FileName)
	if normalized.FileName == "" || len([]rune(normalized.FileName)) > 255 {
		return DistributionUpload{}, OfficialDataActionBundle{}, fmt.Errorf("操作载荷文件名无效")
	}
	if len(normalized.Data) == 0 {
		return DistributionUpload{}, OfficialDataActionBundle{}, fmt.Errorf("操作载荷不能为空")
	}
	if len(normalized.Data) > MaxDistributionFileSize {
		return DistributionUpload{}, OfficialDataActionBundle{}, fmt.Errorf("操作载荷不能超过 32 MiB")
	}
	normalized.ContentType = "application/json"
	bundle, err := DecodeOfficialDataActionBundle(normalized.Data)
	if err != nil {
		return DistributionUpload{}, OfficialDataActionBundle{}, err
	}
	return normalized, bundle, nil
}

func validateOfficialDataActionBundle(bundle OfficialDataActionBundle) error {
	if bundle.SchemaVersion != officialDataActionSchemaVersion {
		return fmt.Errorf("不支持的操作载荷版本: %d", bundle.SchemaVersion)
	}
	trimmedID := strings.TrimSpace(bundle.ID)
	if bundle.ID != trimmedID || trimmedID == "" || len([]rune(trimmedID)) > 160 {
		return fmt.Errorf("操作 ID 无效")
	}
	for _, char := range trimmedID {
		if !(char == '.' || char == '-' || char == '_' || char >= '0' && char <= '9' || char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z') {
			return fmt.Errorf("操作 ID 只能包含字母、数字、点、横线和下划线")
		}
	}
	if bundle.Revision <= 0 {
		return fmt.Errorf("操作 revision 必须大于 0")
	}
	if bundle.Kind != OfficialDataActionKindProviderUpsert {
		return fmt.Errorf("不支持的操作类型: %s", bundle.Kind)
	}
	if err := validateStringSet(bundle.ApplyOn, map[string]struct{}{
		"initial_sync": {},
		"manual_sync":  {},
	}, "apply_on", true); err != nil {
		return err
	}
	if err := validateStringSet(bundle.Platforms, map[string]struct{}{
		"ios":     {},
		"watchos": {},
	}, "platforms", false); err != nil {
		return err
	}
	if bundle.MinimumAppBuild != nil && *bundle.MinimumAppBuild < 0 {
		return fmt.Errorf("minimum_app_build 不能小于 0")
	}
	if err := validateOfficialProvider(bundle.Provider); err != nil {
		return err
	}
	return validateOfficialProviderMergePolicy(bundle.MergePolicy)
}

func validateOfficialProvider(data json.RawMessage) error {
	var provider officialProviderValidationPayload
	if err := json.Unmarshal(data, &provider); err != nil {
		return fmt.Errorf("provider 不是有效配置: %w", err)
	}
	if !isUUID(provider.ID) {
		return fmt.Errorf("provider.id 必须是 UUID")
	}
	if strings.TrimSpace(provider.Name) == "" || strings.TrimSpace(provider.APIFormat) == "" {
		return fmt.Errorf("provider.name 和 provider.apiFormat 不能为空")
	}
	baseURL, err := url.Parse(strings.TrimSpace(provider.BaseURL))
	if err != nil || baseURL.Scheme != "https" || baseURL.Host == "" {
		return fmt.Errorf("provider.baseURL 必须是 HTTPS 地址")
	}
	seenModelIDs := make(map[string]struct{}, len(provider.Models))
	for index, model := range provider.Models {
		if !isUUID(model.ID) || strings.TrimSpace(model.ModelName) == "" {
			return fmt.Errorf("第 %d 个模型的 id 或 modelName 无效", index+1)
		}
		if _, exists := seenModelIDs[strings.ToLower(model.ID)]; exists {
			return fmt.Errorf("模型 UUID 重复")
		}
		seenModelIDs[strings.ToLower(model.ID)] = struct{}{}
	}
	return nil
}

func validateOfficialProviderMergePolicy(policy OfficialProviderMergePolicy) error {
	fieldPolicies := map[string]struct{}{
		"update_if_unmodified": {},
		"server_wins":          {},
		"preserve_local":       {},
	}
	if _, ok := fieldPolicies[policy.ProviderFields]; !ok {
		return fmt.Errorf("merge_policy.provider_fields 无效")
	}
	if _, ok := map[string]struct{}{
		"preserve_local_if_nonempty": {},
		"server_wins":                {},
		"append_unique":              {},
		"update_if_unmodified":       {},
	}[policy.APIKeys]; !ok {
		return fmt.Errorf("merge_policy.api_keys 无效")
	}
	if _, ok := map[string]struct{}{
		"update_if_unmodified": {},
		"server_wins":          {},
		"preserve_local":       {},
		"merge_server_wins":    {},
	}[policy.HeaderOverrides]; !ok {
		return fmt.Errorf("merge_policy.header_overrides 无效")
	}
	if _, ok := fieldPolicies[policy.ProxyConfiguration]; !ok {
		return fmt.Errorf("merge_policy.proxy_configuration 无效")
	}
	if _, ok := fieldPolicies[policy.Models.Fields]; !ok {
		return fmt.Errorf("merge_policy.models.fields 无效")
	}
	if _, ok := fieldPolicies[policy.Models.IsActivated]; !ok {
		return fmt.Errorf("merge_policy.models.is_activated 无效")
	}
	if policy.Models.OnMissing != "insert" && policy.Models.OnMissing != "skip" {
		return fmt.Errorf("merge_policy.models.on_missing 无效")
	}
	if policy.Models.OnRemoved != "preserve" && policy.Models.OnRemoved != "delete_if_unmodified" {
		return fmt.Errorf("merge_policy.models.on_removed 无效")
	}
	if policy.Models.UserModels != "preserve" && policy.Models.UserModels != "delete" {
		return fmt.Errorf("merge_policy.models.user_models 无效")
	}
	if _, ok := map[string]struct{}{
		"keep_deleted":           {},
		"restore_on_manual_sync": {},
		"force_restore":          {},
	}[policy.IfUserDeleted]; !ok {
		return fmt.Errorf("merge_policy.if_user_deleted 无效")
	}
	return nil
}

func validateStringSet(values []string, allowed map[string]struct{}, field string, required bool) error {
	if required && len(values) == 0 {
		return fmt.Errorf("%s 不能为空", field)
	}
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if _, ok := allowed[value]; !ok {
			return fmt.Errorf("%s 包含不支持的值: %s", field, value)
		}
		if _, exists := seen[value]; exists {
			return fmt.Errorf("%s 不能包含重复值", field)
		}
		seen[value] = struct{}{}
	}
	return nil
}

func isUUID(value string) bool {
	parts := strings.Split(value, "-")
	if len(parts) != 5 || len(parts[0]) != 8 || len(parts[1]) != 4 || len(parts[2]) != 4 || len(parts[3]) != 4 || len(parts[4]) != 12 {
		return false
	}
	_, err := hex.DecodeString(strings.Join(parts, ""))
	return err == nil
}

func validateStoredOfficialDataAction(action OfficialDataActionRecord) error {
	if strings.TrimSpace(action.Key) == "" || strings.TrimSpace(action.ID) == "" {
		return fmt.Errorf("key 和 id 不能为空")
	}
	if action.Revision <= 0 || action.Kind != OfficialDataActionKindProviderUpsert {
		return fmt.Errorf("revision 或 kind 无效")
	}
	if !isUUID(action.ProviderID) {
		return fmt.Errorf("provider_id 必须是 UUID")
	}
	if err := validateStringSet(action.ApplyOn, map[string]struct{}{
		"initial_sync": {},
		"manual_sync":  {},
	}, "apply_on", true); err != nil {
		return err
	}
	if err := validateStringSet(action.Platforms, map[string]struct{}{
		"ios":     {},
		"watchos": {},
	}, "platforms", false); err != nil {
		return err
	}
	if action.MinimumAppBuild != nil && *action.MinimumAppBuild < 0 {
		return fmt.Errorf("minimum_app_build 不能小于 0")
	}
	if err := validateOfficialProviderMergePolicy(action.MergePolicy); err != nil {
		return err
	}
	if sanitizeDistributionFileName(action.FileName) != action.FileName {
		return fmt.Errorf("文件名无效")
	}
	if len(action.SHA256) != sha256.Size*2 {
		return fmt.Errorf("SHA256 无效")
	}
	if _, err := hex.DecodeString(action.SHA256); err != nil {
		return fmt.Errorf("SHA256 无效")
	}
	if action.Size <= 0 || action.Size > MaxDistributionFileSize {
		return fmt.Errorf("文件大小无效")
	}
	if action.CreatedAt.IsZero() || action.UpdatedAt.IsZero() {
		return fmt.Errorf("时间字段无效")
	}
	return nil
}

func officialDataActionRecordFromUpload(
	key string,
	input OfficialDataActionInput,
	upload DistributionUpload,
	bundle OfficialDataActionBundle,
	createdAt time.Time,
	updatedAt time.Time,
) OfficialDataActionRecord {
	digest := sha256.Sum256(upload.Data)
	providerID := officialProviderID(bundle.Provider)
	return OfficialDataActionRecord{
		Key:             key,
		ID:              bundle.ID,
		Revision:        bundle.Revision,
		Kind:            bundle.Kind,
		ProviderID:      providerID,
		ApplyOn:         append([]string{}, bundle.ApplyOn...),
		MinimumAppBuild: bundle.MinimumAppBuild,
		Platforms:       append([]string{}, bundle.Platforms...),
		MergePolicy:     bundle.MergePolicy,
		FileName:        upload.FileName,
		ContentType:     upload.ContentType,
		SHA256:          hex.EncodeToString(digest[:]),
		Size:            int64(len(upload.Data)),
		Enabled:         input.Enabled,
		CreatedAt:       createdAt,
		UpdatedAt:       updatedAt,
	}
}

func (s *DistributionStore) actionIDExistsLocked(id string, excludingKey string) bool {
	for _, action := range s.actions {
		if action.Key != excludingKey && action.ID == id {
			return true
		}
	}
	return false
}

func (s *DistributionStore) actionProviderIDExistsLocked(providerID string, excludingKey string) bool {
	for _, action := range s.actions {
		if action.Key != excludingKey && strings.EqualFold(action.ProviderID, providerID) {
			return true
		}
	}
	return false
}

func officialProviderID(data json.RawMessage) string {
	var provider officialProviderValidationPayload
	if json.Unmarshal(data, &provider) != nil {
		return ""
	}
	return provider.ID
}

func officialActionRecordMatchesBundle(
	record OfficialDataActionRecord,
	bundle OfficialDataActionBundle,
) bool {
	return bundle.ID == record.ID &&
		bundle.Revision == record.Revision &&
		bundle.Kind == record.Kind &&
		officialProviderID(bundle.Provider) == record.ProviderID &&
		reflect.DeepEqual(bundle.ApplyOn, record.ApplyOn) &&
		reflect.DeepEqual(bundle.MinimumAppBuild, record.MinimumAppBuild) &&
		reflect.DeepEqual(bundle.Platforms, record.Platforms) &&
		reflect.DeepEqual(bundle.MergePolicy, record.MergePolicy)
}

func sortOfficialDataActions(actions []OfficialDataActionRecord) {
	sort.SliceStable(actions, func(left, right int) bool {
		if actions[left].ID != actions[right].ID {
			return actions[left].ID < actions[right].ID
		}
		return actions[left].Revision < actions[right].Revision
	})
}
