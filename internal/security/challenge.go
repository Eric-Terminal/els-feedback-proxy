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
	ErrPoWMissing          = errors.New("缺少 PoW nonce")
	ErrPoWInvalid          = errors.New("PoW 校验失败")
	ErrClientBlocked       = errors.New("客户端暂时封禁")
	ErrChallengeIPMismatch = errors.New("challenge IP 不匹配")
)

// ChallengeBundle 返回给客户端的一次性 challenge
type ChallengeBundle struct {
	ChallengeID  string    `json:"challenge_id"`
	ClientSecret string    `json:"client_secret"`
	Nonce        string    `json:"nonce"`
	PoWBits      int       `json:"pow_bits"`
	PoWSalt      string    `json:"pow_salt"`
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

func (m *ChallengeManager) Issue(clientIP string, powBits int) ChallengeBundle {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()
	m.cleanup(now)

	bundle := ChallengeBundle{
		ChallengeID:  randomHex(16),
		ClientSecret: randomHex(32),
		Nonce:        randomHex(12),
		PoWBits:      powBits,
		PoWSalt:      randomHex(8),
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
	powNonceRaw string,
	powHashRaw string,
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

	if record.Bundle.PoWBits > 0 {
		powNonce := strings.TrimSpace(powNonceRaw)
		if powNonce == "" {
			m.registerFailure(now, clientIP, challengeID, record)
			return ErrPoWMissing
		}
		if len(powNonce) > 128 {
			m.registerFailure(now, clientIP, challengeID, record)
			return ErrPoWInvalid
		}

		powMessage := buildPoWMessage(
			method,
			path,
			timestampRaw,
			bodyHashHex,
			challengeID,
			record.Bundle.PoWSalt,
			powNonce,
		)
		powDigest := sha256.Sum256([]byte(powMessage))
		if !hasLeadingZeroBits(powDigest[:], record.Bundle.PoWBits) {
			m.registerFailure(now, clientIP, challengeID, record)
			return ErrPoWInvalid
		}
		if strings.TrimSpace(powHashRaw) != "" {
			expectedPowHash := hex.EncodeToString(powDigest[:])
			if subtle.ConstantTimeCompare([]byte(strings.ToLower(expectedPowHash)), []byte(strings.ToLower(strings.TrimSpace(powHashRaw)))) != 1 {
				m.registerFailure(now, clientIP, challengeID, record)
				return ErrPoWInvalid
			}
		}
	}

	signingText := fmt.Sprintf("%s\n%s\n%s\n%s\n%s", strings.ToUpper(method), path, timestampRaw, bodyHashHex, record.Bundle.Nonce)

	mac := hmac.New(sha256.New, []byte(record.Bundle.ClientSecret))
	_, _ = mac.Write([]byte(signingText))
	expectedSignature := hex.EncodeToString(mac.Sum(nil))

	if subtle.ConstantTimeCompare([]byte(strings.ToLower(expectedSignature)), []byte(strings.ToLower(signatureRaw))) != 1 {
		m.registerFailure(now, clientIP, challengeID, record)
		return ErrSignatureInvalid
	}

	record.Used = true
	delete(m.records, challengeID)
	return nil
}

func (m *ChallengeManager) registerFailure(now time.Time, clientIP, challengeID string, record *challengeRecord) {
	record.FailCount++
	if record.FailCount >= m.failThreshold {
		m.blockedClientIP[clientIP] = now.Add(m.blockDuration)
		delete(m.records, challengeID)
	}
}

func buildPoWMessage(method, path, timestampRaw, bodyHashHex, challengeID, powSalt, powNonce string) string {
	return fmt.Sprintf(
		"%s\n%s\n%s\n%s\n%s\n%s\n%s",
		strings.ToUpper(method),
		path,
		timestampRaw,
		bodyHashHex,
		challengeID,
		powSalt,
		powNonce,
	)
}

func hasLeadingZeroBits(digest []byte, bits int) bool {
	if bits <= 0 {
		return true
	}
	for _, b := range digest {
		if bits <= 0 {
			return true
		}
		if bits >= 8 {
			if b != 0 {
				return false
			}
			bits -= 8
			continue
		}
		mask := byte(0xFF << (8 - bits))
		return (b & mask) == 0
	}
	return bits <= 0
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
