package telemetry

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestDecodeUploadRequestAcceptsSwiftCompatibleEnvelope(t *testing.T) {
	now := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	payload := []byte(`{"applicationTime":1.25,"label":"<launch>/ready&ok"}`)
	raw := makeEnvelopeRaw(t, PayloadKindMetric, payload, now)
	body := makeUploadBody(t, raw)

	envelopes, err := DecodeUploadRequest(body, now)
	if err != nil {
		t.Fatalf("Swift 兼容遥测应通过校验: %v", err)
	}
	if len(envelopes) != 1 {
		t.Fatalf("期望解析 1 条遥测，实际 %d", len(envelopes))
	}
	sum := sha256.Sum256(payload)
	if envelopes[0].Envelope.PayloadID != hex.EncodeToString(sum[:]) {
		t.Fatalf("payload_id 未按原始规范化 payload 计算")
	}
	if !bytes.Contains(envelopes[0].Raw, []byte(`"<launch>/ready&ok"`)) {
		t.Fatalf("原始遥测不应重新转义字符串")
	}
}

func TestDecodeUploadRequestAcceptsFlatV2AndLegacyV1(t *testing.T) {
	now := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	flatPayload := []byte(`{"_etos":{"call_stack_frames_emitted":1,"format":"metric-kit-flat-v1","truncated":false},"hangDiagnostics":[{"callStackTree":{"callStacks":[{"callStackFrames":[{"binaryName":"ETOS LLM Studio","depth":0,"frameID":0}]}],"format":"flat-v1","truncated":false}}]}`)
	flatEnvelope := makeEnvelopeRawForSchema(
		t,
		CurrentSchemaVersion,
		PayloadKindDiagnostic,
		flatPayload,
		now,
	)
	flatItems, err := DecodeUploadRequest(
		makeUploadBodyForSchema(t, CurrentSchemaVersion, flatEnvelope),
		now,
	)
	if err != nil || len(flatItems) != 1 {
		t.Fatalf("v2 扁平遥测应通过校验: items=%d err=%v", len(flatItems), err)
	}

	legacyPayload := []byte(`{"hang":1}`)
	legacyEnvelope := makeEnvelopeRawForSchema(
		t,
		LegacySchemaVersion,
		PayloadKindDiagnostic,
		legacyPayload,
		now,
	)
	legacyItems, err := DecodeUploadRequest(
		makeUploadBodyForSchema(t, LegacySchemaVersion, legacyEnvelope),
		now,
	)
	if err != nil || len(legacyItems) != 1 {
		t.Fatalf("v1 旧遥测应继续通过校验: items=%d err=%v", len(legacyItems), err)
	}
}

func TestDecodeUploadRequestRejectsInvalidV2FormatAndMixedVersions(t *testing.T) {
	now := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	missingMetadata := makeEnvelopeRawForSchema(
		t,
		CurrentSchemaVersion,
		PayloadKindMetric,
		[]byte(`{"cpu":1}`),
		now,
	)
	if _, err := DecodeUploadRequest(
		makeUploadBodyForSchema(t, CurrentSchemaVersion, missingMetadata),
		now,
	); err == nil || !strings.Contains(err.Error(), "_etos") {
		t.Fatalf("缺少扁平格式声明的 v2 遥测应被拒绝，实际错误: %v", err)
	}

	legacyEnvelope := makeEnvelopeRawForSchema(
		t,
		LegacySchemaVersion,
		PayloadKindMetric,
		[]byte(`{"cpu":1}`),
		now,
	)
	if _, err := DecodeUploadRequest(
		makeUploadBodyForSchema(t, CurrentSchemaVersion, legacyEnvelope),
		now,
	); err == nil || !strings.Contains(err.Error(), "批次不一致") {
		t.Fatalf("批次与信封版本不一致时应被拒绝，实际错误: %v", err)
	}
}

func TestDecodeUploadRequestRejectsExcessiveJSONNesting(t *testing.T) {
	nested := `0`
	for range MaxJSONNestingDepth + 1 {
		nested = `[` + nested + `]`
	}
	body := []byte(`{"schema_version":1,"envelopes":` + nested + `}`)
	if _, err := DecodeUploadRequest(body, time.Now().UTC()); err == nil ||
		!strings.Contains(err.Error(), "嵌套") {
		t.Fatalf("过深 JSON 应在解码前被拒绝，实际错误: %v", err)
	}
}

func TestDecodeUploadRequestRejectsUnknownAndUnsafeFields(t *testing.T) {
	now := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	raw := makeEnvelopeRaw(t, PayloadKindDiagnostic, []byte(`{"hang":1}`), now)

	withUnknown := bytes.Replace(raw, []byte(`"kind":"diagnostic"`), []byte(
		`"kind":"diagnostic","unknown":true`,
	), 1)
	if _, err := DecodeUploadRequest(makeUploadBody(t, withUnknown), now); err == nil ||
		!strings.Contains(err.Error(), "unknown") {
		t.Fatalf("未知字段应被拒绝，实际错误: %v", err)
	}

	unsafe := bytes.Replace(raw, []byte(`"contains_chat_content":false`), []byte(
		`"contains_chat_content":true`,
	), 1)
	if _, err := DecodeUploadRequest(makeUploadBody(t, unsafe), now); err == nil ||
		!strings.Contains(err.Error(), "用户内容") {
		t.Fatalf("包含聊天内容的隐私声明应被拒绝，实际错误: %v", err)
	}
}

func TestDecodeUploadRequestRejectsHashMismatchAndNonObjectPayload(t *testing.T) {
	now := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	raw := makeEnvelopeRaw(t, PayloadKindMetric, []byte(`{"cpu":1}`), now)
	mismatched := bytes.Replace(raw, []byte(`"payload":{"cpu":1}`), []byte(
		`"payload":{"cpu":2}`,
	), 1)
	if _, err := DecodeUploadRequest(makeUploadBody(t, mismatched), now); err == nil ||
		!strings.Contains(err.Error(), "不匹配") {
		t.Fatalf("内容哈希不一致应被拒绝，实际错误: %v", err)
	}

	arrayPayload := []byte(`[1,2,3]`)
	arrayRaw := makeEnvelopeRaw(t, PayloadKindMetric, arrayPayload, now)
	if _, err := DecodeUploadRequest(makeUploadBody(t, arrayRaw), now); err == nil ||
		!strings.Contains(err.Error(), "JSON 对象") {
		t.Fatalf("非对象 payload 应被拒绝，实际错误: %v", err)
	}
}

func TestDecodeUploadRequestEnforcesBatchCount(t *testing.T) {
	now := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	empty := []byte(`{"schema_version":1,"envelopes":[]}`)
	if _, err := DecodeUploadRequest(empty, now); err == nil {
		t.Fatalf("空批次应被拒绝")
	}

	raw := makeEnvelopeRaw(t, PayloadKindMetric, []byte(`{"cpu":1}`), now)
	items := make([]json.RawMessage, MaxBatchEnvelopes+1)
	for index := range items {
		items[index] = raw
	}
	body, err := json.Marshal(struct {
		SchemaVersion int               `json:"schema_version"`
		Envelopes     []json.RawMessage `json:"envelopes"`
	}{SchemaVersion: LegacySchemaVersion, Envelopes: items})
	if err != nil {
		t.Fatalf("编码超限批次失败: %v", err)
	}
	if _, err := DecodeUploadRequest(body, now); err == nil {
		t.Fatalf("超过 %d 条的批次应被拒绝", MaxBatchEnvelopes)
	}
}

func makeUploadBody(t *testing.T, envelopes ...[]byte) []byte {
	return makeUploadBodyForSchema(t, LegacySchemaVersion, envelopes...)
}

func makeUploadBodyForSchema(
	t *testing.T,
	schemaVersion int,
	envelopes ...[]byte,
) []byte {
	t.Helper()
	rawEnvelopes := make([]json.RawMessage, len(envelopes))
	for index, envelope := range envelopes {
		rawEnvelopes[index] = envelope
	}
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	err := encoder.Encode(struct {
		SchemaVersion int               `json:"schema_version"`
		Envelopes     []json.RawMessage `json:"envelopes"`
	}{
		SchemaVersion: schemaVersion,
		Envelopes:     rawEnvelopes,
	})
	if err != nil {
		t.Fatalf("编码遥测批次失败: %v", err)
	}
	return bytes.TrimSpace(buffer.Bytes())
}

func makeEnvelopeRaw(
	t *testing.T,
	kind PayloadKind,
	payload []byte,
	capturedAt time.Time,
) []byte {
	return makeEnvelopeRawForSchema(
		t,
		LegacySchemaVersion,
		kind,
		payload,
		capturedAt,
	)
}

func makeEnvelopeRawForSchema(
	t *testing.T,
	schemaVersion int,
	kind PayloadKind,
	payload []byte,
	capturedAt time.Time,
) []byte {
	t.Helper()
	sum := sha256.Sum256(payload)
	envelope := Envelope{
		SchemaVersion: schemaVersion,
		PayloadID:     hex.EncodeToString(sum[:]),
		Kind:          kind,
		CapturedAt:    capturedAt.UTC(),
		App: AppMetadata{
			Version:      "2.7.0",
			Build:        "270",
			Distribution: "testflight",
		},
		Platform: PlatformMetadata{
			Name:         "ios",
			OSVersion:    "26.0",
			DeviceClass:  "iPhone17,2",
			Architecture: "arm64",
		},
		Privacy: PrivacyDeclaration{},
		Payload: payload,
	}
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(envelope); err != nil {
		t.Fatalf("编码测试遥测失败: %v", err)
	}
	return bytes.TrimSpace(buffer.Bytes())
}
