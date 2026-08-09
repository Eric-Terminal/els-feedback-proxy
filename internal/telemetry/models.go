package telemetry

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"
)

const (
	LegacySchemaVersion  = 1
	CurrentSchemaVersion = 2
	AdminSchemaVersion   = 1
	MaxBatchEnvelopes    = 16
	MaxRequestBodyBytes  = 4 << 20
	MaxEnvelopeBytes     = 3 << 20
	MaxJSONNestingDepth  = 512
)

var (
	lowerHexSHA256 = regexp.MustCompile(`^[0-9a-f]{64}$`)
	safeMetadata   = regexp.MustCompile(`^[a-zA-Z0-9._,+() -]+$`)
)

type PayloadKind string

const (
	PayloadKindMetric     PayloadKind = "metric"
	PayloadKindDiagnostic PayloadKind = "diagnostic"
)

type AppMetadata struct {
	Version      string `json:"version"`
	Build        string `json:"build"`
	Distribution string `json:"distribution"`
}

type PlatformMetadata struct {
	Name         string `json:"name"`
	OSVersion    string `json:"os_version"`
	DeviceClass  string `json:"device_class"`
	Architecture string `json:"architecture"`
}

type PrivacyDeclaration struct {
	ContainsChatContent    bool `json:"contains_chat_content"`
	ContainsRequestBody    bool `json:"contains_request_body"`
	ContainsResponseBody   bool `json:"contains_response_body"`
	ContainsCredentials    bool `json:"contains_credentials"`
	ContainsUserIdentifier bool `json:"contains_user_identifier"`
}

func (p PrivacyDeclaration) IsSafe() bool {
	return !p.ContainsChatContent &&
		!p.ContainsRequestBody &&
		!p.ContainsResponseBody &&
		!p.ContainsCredentials &&
		!p.ContainsUserIdentifier
}

type Envelope struct {
	SchemaVersion int                `json:"schema_version"`
	PayloadID     string             `json:"payload_id"`
	Kind          PayloadKind        `json:"kind"`
	CapturedAt    time.Time          `json:"captured_at"`
	PeriodStart   *time.Time         `json:"period_start,omitempty"`
	PeriodEnd     *time.Time         `json:"period_end,omitempty"`
	App           AppMetadata        `json:"app"`
	Platform      PlatformMetadata   `json:"platform"`
	Privacy       PrivacyDeclaration `json:"privacy"`
	Payload       json.RawMessage    `json:"payload"`
}

type ValidatedEnvelope struct {
	Envelope Envelope
	Raw      []byte
}

type uploadRequest struct {
	SchemaVersion int               `json:"schema_version"`
	Envelopes     []json.RawMessage `json:"envelopes"`
}

type UploadResult struct {
	PayloadID string `json:"payload_id"`
	Status    string `json:"status"`
}

type UploadResponse struct {
	SchemaVersion int            `json:"schema_version"`
	Results       []UploadResult `json:"results"`
}

func DecodeUploadRequest(body []byte, now time.Time) ([]ValidatedEnvelope, error) {
	if maximumJSONNestingDepth(body) > MaxJSONNestingDepth {
		return nil, fmt.Errorf("请求体 JSON 嵌套超过 %d 层", MaxJSONNestingDepth)
	}
	var request uploadRequest
	if err := decodeStrict(body, &request); err != nil {
		return nil, fmt.Errorf("请求体不是有效的遥测批次: %w", err)
	}
	if !isSupportedSchemaVersion(request.SchemaVersion) {
		return nil, fmt.Errorf("不支持的 schema_version: %d", request.SchemaVersion)
	}
	if len(request.Envelopes) == 0 || len(request.Envelopes) > MaxBatchEnvelopes {
		return nil, fmt.Errorf("envelopes 数量必须在 1 到 %d 之间", MaxBatchEnvelopes)
	}

	result := make([]ValidatedEnvelope, 0, len(request.Envelopes))
	for index, raw := range request.Envelopes {
		if len(raw) > MaxEnvelopeBytes {
			return nil, fmt.Errorf("第 %d 条遥测超过单条大小限制", index+1)
		}
		var envelope Envelope
		if err := decodeStrict(raw, &envelope); err != nil {
			return nil, fmt.Errorf("第 %d 条遥测结构无效: %w", index+1, err)
		}
		if err := envelope.Validate(now); err != nil {
			return nil, fmt.Errorf("第 %d 条遥测无效: %w", index+1, err)
		}
		if envelope.SchemaVersion != request.SchemaVersion {
			return nil, fmt.Errorf(
				"第 %d 条遥测 schema_version 与批次不一致",
				index+1,
			)
		}

		result = append(result, ValidatedEnvelope{
			Envelope: envelope,
			Raw:      append([]byte(nil), bytes.TrimSpace(raw)...),
		})
	}
	return result, nil
}

func (e Envelope) Validate(now time.Time) error {
	if !isSupportedSchemaVersion(e.SchemaVersion) {
		return fmt.Errorf("不支持的 schema_version: %d", e.SchemaVersion)
	}
	if !lowerHexSHA256.MatchString(e.PayloadID) {
		return errors.New("payload_id 必须是小写 SHA-256")
	}
	if e.Kind != PayloadKindMetric && e.Kind != PayloadKindDiagnostic {
		return errors.New("kind 仅支持 metric 或 diagnostic")
	}
	if e.CapturedAt.IsZero() ||
		e.CapturedAt.Before(time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)) ||
		e.CapturedAt.After(now.UTC().Add(24*time.Hour)) {
		return errors.New("captured_at 超出允许范围")
	}
	if (e.PeriodStart == nil) != (e.PeriodEnd == nil) {
		return errors.New("period_start 与 period_end 必须同时存在或同时省略")
	}
	if e.PeriodStart != nil && e.PeriodEnd != nil && e.PeriodStart.After(*e.PeriodEnd) {
		return errors.New("period_start 不能晚于 period_end")
	}
	if err := validateMetadata("app.version", e.App.Version, 1, 64); err != nil {
		return err
	}
	if err := validateMetadata("app.build", e.App.Build, 1, 64); err != nil {
		return err
	}
	switch e.App.Distribution {
	case "testflight", "appstore", "development", "unknown":
	default:
		return errors.New("app.distribution 无效")
	}
	if e.Platform.Name != "ios" {
		return errors.New("platform.name 仅支持 ios")
	}
	if err := validateMetadata("platform.os_version", e.Platform.OSVersion, 1, 64); err != nil {
		return err
	}
	if err := validateMetadata("platform.device_class", e.Platform.DeviceClass, 1, 128); err != nil {
		return err
	}
	if err := validateMetadata("platform.architecture", e.Platform.Architecture, 1, 32); err != nil {
		return err
	}
	if !e.Privacy.IsSafe() {
		return errors.New("隐私声明表明遥测包含用户内容或凭据")
	}

	trimmedPayload := bytes.TrimSpace(e.Payload)
	if len(trimmedPayload) < 2 || trimmedPayload[0] != '{' {
		return errors.New("payload 必须是 JSON 对象")
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(trimmedPayload, &payload); err != nil || payload == nil {
		return errors.New("payload 必须是有效 JSON 对象")
	}
	if e.SchemaVersion == CurrentSchemaVersion {
		if err := validateFlatPayloadMetadata(payload); err != nil {
			return err
		}
	}

	// Swift 在上传前已按键排序并压缩 payload。直接校验收到的原始片段，
	// 避免 Go 在 Compact 时转义 HTML 字符或重新编码浮点数而改变跨语言哈希。
	sum := sha256.Sum256(trimmedPayload)
	if hex.EncodeToString(sum[:]) != e.PayloadID {
		return errors.New("payload_id 与 payload 内容不匹配")
	}
	return nil
}

func isSupportedSchemaVersion(version int) bool {
	return version == LegacySchemaVersion || version == CurrentSchemaVersion
}

func validateFlatPayloadMetadata(payload map[string]json.RawMessage) error {
	rawMetadata, exists := payload["_etos"]
	if !exists {
		return errors.New("schema_version 2 payload 缺少 _etos 格式声明")
	}
	var metadata struct {
		Format string `json:"format"`
	}
	if err := json.Unmarshal(rawMetadata, &metadata); err != nil ||
		metadata.Format != "metric-kit-flat-v1" {
		return errors.New("schema_version 2 payload 格式无效")
	}
	return nil
}

func maximumJSONNestingDepth(data []byte) int {
	depth := 0
	maximumDepth := 0
	insideString := false
	escaping := false

	for _, value := range data {
		if insideString {
			if escaping {
				escaping = false
			} else if value == '\\' {
				escaping = true
			} else if value == '"' {
				insideString = false
			}
			continue
		}

		switch value {
		case '"':
			insideString = true
		case '{', '[':
			depth++
			if depth > maximumDepth {
				maximumDepth = depth
			}
		case '}', ']':
			if depth > 0 {
				depth--
			}
		}
	}
	return maximumDepth
}

func decodeStrict(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("JSON 后存在多余内容")
		}
		return err
	}
	return nil
}

func validateMetadata(field, value string, minLength, maxLength int) error {
	if value != strings.TrimSpace(value) {
		return fmt.Errorf("%s 首尾不能包含空白", field)
	}
	length := len([]rune(value))
	if length < minLength || length > maxLength || !safeMetadata.MatchString(value) {
		return fmt.Errorf("%s 包含无效字符或长度超限", field)
	}
	return nil
}
