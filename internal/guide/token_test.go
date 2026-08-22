package guide

import (
	"errors"
	"testing"
	"time"
)

func TestTokenBindsIPAndThirtySecondWindow(t *testing.T) {
	manager := NewTokenManager("0123456789abcdef0123456789abcdef")
	now := time.Unix(1_800_000_005, 0)
	bundle := manager.Issue("203.0.113.10", now)
	if err := manager.Validate(bundle.Token, "203.0.113.10", now.Add(20*time.Second)); err != nil {
		t.Fatalf("同一时间窗与 IP 的令牌应有效: %v", err)
	}
	if err := manager.Validate(bundle.Token, "203.0.113.11", now); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("令牌不能换 IP 使用，实际错误: %v", err)
	}
	if err := manager.Validate(bundle.Token, "203.0.113.10", now.Add(30*time.Second)); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("跨过 30 秒时间窗后令牌应失效，实际错误: %v", err)
	}
	if bundle.ExpiresAt.Sub(now) > 30*time.Second || !bundle.ExpiresAt.After(now) {
		t.Fatalf("令牌过期时间不在当前 30 秒窗末尾: %s", bundle.ExpiresAt)
	}
}

func TestConcurrencyLimiterRestrictsSessionAndIP(t *testing.T) {
	limiter := NewConcurrencyLimiter(1)
	release, err := limiter.Acquire("203.0.113.10", "session-one")
	if err != nil {
		t.Fatalf("首次并发占用失败: %v", err)
	}
	if _, err := limiter.Acquire("203.0.113.10", "session-two"); !errors.Is(err, ErrIPBusy) {
		t.Fatalf("同 IP 第二个向导应被拒绝: %v", err)
	}
	if _, err := limiter.Acquire("203.0.113.11", "session-one"); !errors.Is(err, ErrSessionBusy) {
		t.Fatalf("同会话第二个请求应被拒绝: %v", err)
	}
	release()
	release()
	if releaseAgain, err := limiter.Acquire("203.0.113.10", "session-two"); err != nil {
		t.Fatalf("请求完成后应释放并发名额: %v", err)
	} else {
		releaseAgain()
	}
}
