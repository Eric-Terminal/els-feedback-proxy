package guide

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strconv"
	"strings"
	"time"
)

const tokenWindow = 30 * time.Second

var ErrInvalidToken = errors.New("向导临时令牌无效或已过期")

// TokenBundle 是客户端在当前 30 秒时间窗内可使用的短期凭据。
type TokenBundle struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
}

// TokenManager 用服务端秘密把时间窗与来源 IP 绑定，不保存设备标识或令牌状态。
type TokenManager struct {
	secret []byte
}

func NewTokenManager(secret string) *TokenManager {
	return &TokenManager{secret: []byte(secret)}
}

func (m *TokenManager) Issue(clientIP string, now time.Time) TokenBundle {
	slot := now.Unix() / int64(tokenWindow/time.Second)
	return TokenBundle{
		Token:     m.token(clientIP, slot),
		ExpiresAt: time.Unix((slot+1)*int64(tokenWindow/time.Second), 0).UTC(),
	}
}

func (m *TokenManager) Validate(token, clientIP string, now time.Time) error {
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[0] != "v1" {
		return ErrInvalidToken
	}
	slot, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || slot != now.Unix()/int64(tokenWindow/time.Second) {
		return ErrInvalidToken
	}
	actualMAC, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return ErrInvalidToken
	}
	expectedMAC := m.mac(clientIP, slot)
	if !hmac.Equal(actualMAC, expectedMAC) {
		return ErrInvalidToken
	}
	return nil
}

func (m *TokenManager) token(clientIP string, slot int64) string {
	return "v1." + strconv.FormatInt(slot, 10) + "." + base64.RawURLEncoding.EncodeToString(m.mac(clientIP, slot))
}

func (m *TokenManager) mac(clientIP string, slot int64) []byte {
	hash := hmac.New(sha256.New, m.secret)
	hash.Write([]byte("etos-guide-v1\n"))
	hash.Write([]byte(strings.TrimSpace(clientIP)))
	hash.Write([]byte("\n"))
	hash.Write([]byte(strconv.FormatInt(slot, 10)))
	return hash.Sum(nil)
}
