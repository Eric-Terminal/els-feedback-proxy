package security

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisFixedWindowLimiter 使用 Redis 实现全局固定窗口限流。
// 当 Redis 不可用时，会回退到内存限流，避免服务完全不可用。
type RedisFixedWindowLimiter struct {
	client    *redis.Client
	keyPrefix string
	fallback  *FixedWindowLimiter
	timeout   time.Duration
}

var fixedWindowAllowScript = redis.NewScript(`
local current = redis.call("INCR", KEYS[1])
if current == 1 then
  redis.call("PEXPIRE", KEYS[1], ARGV[2])
end
if current > tonumber(ARGV[1]) then
  return 0
end
return 1
`)

func NewRedisFixedWindowLimiter(client *redis.Client, keyPrefix string) *RedisFixedWindowLimiter {
	return &RedisFixedWindowLimiter{
		client:    client,
		keyPrefix: keyPrefix,
		fallback:  NewFixedWindowLimiter(),
		timeout:   800 * time.Millisecond,
	}
}

func (l *RedisFixedWindowLimiter) Allow(key string, limit int, window time.Duration) bool {
	if limit <= 0 {
		return false
	}
	if l == nil {
		return false
	}
	if l.client == nil {
		return l.fallback.Allow(key, limit, window)
	}

	ctx, cancel := context.WithTimeout(context.Background(), l.timeout)
	defer cancel()

	fullKey := fmt.Sprintf("%s:rate:%s", l.keyPrefix, key)
	windowMillis := window.Milliseconds()
	if windowMillis <= 0 {
		windowMillis = 1
	}

	result, err := fixedWindowAllowScript.Run(ctx, l.client, []string{fullKey}, limit, windowMillis).Int()
	if err != nil {
		return l.fallback.Allow(key, limit, window)
	}
	return result == 1
}
