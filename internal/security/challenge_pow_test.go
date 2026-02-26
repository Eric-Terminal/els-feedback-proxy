package security

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"testing"
	"time"
)

func TestVerifySubmissionWithPoW(t *testing.T) {
	manager := NewChallengeManager(2*time.Minute, 90*time.Second, 5, 10*time.Minute)
	clientIP := "127.0.0.1"
	bundle := manager.Issue(clientIP, 8)

	body := []byte(`{"title":"hello"}`)
	bodyHash := sha256.Sum256(body)
	bodyHashHex := hex.EncodeToString(bodyHash[:])
	timestamp := fmt.Sprintf("%d", time.Now().Unix())

	powNonce, powHash, ok := solvePoWForTest(
		http.MethodPost,
		"/v1/feedback/issues",
		timestamp,
		bodyHashHex,
		bundle.ChallengeID,
		bundle.PoWSalt,
		bundle.PoWBits,
		100000,
	)
	if !ok {
		t.Fatal("未能在测试迭代内求出 PoW")
	}

	signingText := fmt.Sprintf(
		"%s\n%s\n%s\n%s\n%s",
		http.MethodPost,
		"/v1/feedback/issues",
		timestamp,
		bodyHashHex,
		bundle.Nonce,
	)
	mac := hmac.New(sha256.New, []byte(bundle.ClientSecret))
	_, _ = mac.Write([]byte(signingText))
	signature := hex.EncodeToString(mac.Sum(nil))

	err := manager.VerifySubmission(
		clientIP,
		bundle.ChallengeID,
		timestamp,
		signature,
		powNonce,
		powHash,
		http.MethodPost,
		"/v1/feedback/issues",
		body,
	)
	if err != nil {
		t.Fatalf("期望校验成功，实际失败: %v", err)
	}
}

func TestVerifySubmissionMissingPoWNonce(t *testing.T) {
	manager := NewChallengeManager(2*time.Minute, 90*time.Second, 5, 10*time.Minute)
	clientIP := "127.0.0.1"
	bundle := manager.Issue(clientIP, 8)

	body := []byte(`{"title":"hello"}`)
	bodyHash := sha256.Sum256(body)
	bodyHashHex := hex.EncodeToString(bodyHash[:])
	timestamp := fmt.Sprintf("%d", time.Now().Unix())

	signingText := fmt.Sprintf(
		"%s\n%s\n%s\n%s\n%s",
		http.MethodPost,
		"/v1/feedback/issues",
		timestamp,
		bodyHashHex,
		bundle.Nonce,
	)
	mac := hmac.New(sha256.New, []byte(bundle.ClientSecret))
	_, _ = mac.Write([]byte(signingText))
	signature := hex.EncodeToString(mac.Sum(nil))

	err := manager.VerifySubmission(
		clientIP,
		bundle.ChallengeID,
		timestamp,
		signature,
		"",
		"",
		http.MethodPost,
		"/v1/feedback/issues",
		body,
	)
	if err != ErrPoWMissing {
		t.Fatalf("期望 ErrPoWMissing，实际: %v", err)
	}
}

func solvePoWForTest(method, path, timestamp, bodyHashHex, challengeID, powSalt string, bits int, maxIterations int) (string, string, bool) {
	for i := 0; i < maxIterations; i++ {
		nonce := fmt.Sprintf("%x", i)
		message := buildPoWMessage(method, path, timestamp, bodyHashHex, challengeID, powSalt, nonce)
		digest := sha256.Sum256([]byte(message))
		if hasLeadingZeroBits(digest[:], bits) {
			return nonce, hex.EncodeToString(digest[:]), true
		}
	}
	return "", "", false
}
