package security

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	ErrChallengeMissing    = errors.New("challenge 不存在")
	ErrChallengeExpired    = errors.New("challenge 已过期")
	ErrChallengeUsed       = errors.New("challenge 已使用")
	ErrSignatureInvalid    = errors.New("签名无效")
	ErrTimestampInvalid    = errors.New("时间戳无效")
	ErrClientBlocked       = errors.New("客户端暂时封禁")
	ErrChallengeIPMismatch = errors.New("challenge IP 不匹配")
)

// ChallengeBundle 返回给客户端的一次性 challenge
type ChallengeBundle struct {
	ChallengeID  string    `json:"challenge_id"`
	ClientSecret string    `json:"client_secret"`
	Nonce        string    `json:"nonce"`
	ExpiresAt    time.Time `json:"expires_at"`
}

type challengeRecord struct {
	Bundle    ChallengeBundle
	IssuedIP  string
	Used      bool
	FailCount int
}

// ChallengeManager 管理 challenge 的签发与校验
type ChallengeManager struct {
	ttl             time.Duration
	timestampSkew   time.Duration
	failThreshold   int
	blockDuration   time.Duration
	mu              sync.Mutex
	records         map[string]*challengeRecord
	blockedClientIP map[string]time.Time
}

func NewChallengeManager(ttl, timestampSkew time.Duration, failThreshold int, blockDuration time.Duration) *ChallengeManager {
	return &ChallengeManager{
		ttl:             ttl,
		timestampSkew:   timestampSkew,
		failThreshold:   failThreshold,
		blockDuration:   blockDuration,
		records:         make(map[string]*challengeRecord),
		blockedClientIP: make(map[string]time.Time),
	}
}

func (m *ChallengeManager) Issue(clientIP string) ChallengeBundle {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()
	m.cleanup(now)

	bundle := ChallengeBundle{
		ChallengeID:  randomHex(16),
		ClientSecret: randomHex(32),
		Nonce:        randomHex(12),
		ExpiresAt:    now.Add(m.ttl),
	}

	m.records[bundle.ChallengeID] = &challengeRecord{
		Bundle:   bundle,
		IssuedIP: clientIP,
	}

	return bundle
}

func (m *ChallengeManager) VerifySubmission(
	clientIP string,
	challengeID string,
	timestampRaw string,
	signatureRaw string,
	method string,
	path string,
	body []byte,
) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()
	m.cleanup(now)

	if blockedUntil, blocked := m.blockedClientIP[clientIP]; blocked && now.Before(blockedUntil) {
		return ErrClientBlocked
	}

	record, exists := m.records[challengeID]
	if !exists {
		return ErrChallengeMissing
	}

	if record.Used {
		return ErrChallengeUsed
	}

	if record.Bundle.ExpiresAt.Before(now) {
		delete(m.records, challengeID)
		return ErrChallengeExpired
	}

	if record.IssuedIP != clientIP {
		return ErrChallengeIPMismatch
	}

	timestampUnix, err := strconv.ParseInt(timestampRaw, 10, 64)
	if err != nil {
		return ErrTimestampInvalid
	}

	timestamp := time.Unix(timestampUnix, 0)
	delta := now.Sub(timestamp)
	if delta < 0 {
		delta = -delta
	}
	if delta > m.timestampSkew {
		return ErrTimestampInvalid
	}

	bodyHash := sha256.Sum256(body)
	bodyHashHex := hex.EncodeToString(bodyHash[:])
	signingText := fmt.Sprintf("%s\n%s\n%s\n%s\n%s", strings.ToUpper(method), path, timestampRaw, bodyHashHex, record.Bundle.Nonce)

	mac := hmac.New(sha256.New, []byte(record.Bundle.ClientSecret))
	_, _ = mac.Write([]byte(signingText))
	expectedSignature := hex.EncodeToString(mac.Sum(nil))

	if subtle.ConstantTimeCompare([]byte(strings.ToLower(expectedSignature)), []byte(strings.ToLower(signatureRaw))) != 1 {
		record.FailCount++
		if record.FailCount >= m.failThreshold {
			m.blockedClientIP[clientIP] = now.Add(m.blockDuration)
			delete(m.records, challengeID)
		}
		return ErrSignatureInvalid
	}

	record.Used = true
	delete(m.records, challengeID)
	return nil
}

func (m *ChallengeManager) cleanup(now time.Time) {
	for id, record := range m.records {
		if record.Bundle.ExpiresAt.Before(now) {
			delete(m.records, id)
		}
	}
	for ip, blockedUntil := range m.blockedClientIP {
		if now.After(blockedUntil) {
			delete(m.blockedClientIP, ip)
		}
	}
}

func randomHex(length int) string {
	if length <= 0 {
		return ""
	}
	bytes := make([]byte, length)
	if _, err := rand.Read(bytes); err != nil {
		// 降级兜底：使用时间戳哈希
		fallback := sha256.Sum256([]byte(fmt.Sprintf("%d", time.Now().UnixNano())))
		return hex.EncodeToString(fallback[:])[:length*2]
	}
	return hex.EncodeToString(bytes)
}
