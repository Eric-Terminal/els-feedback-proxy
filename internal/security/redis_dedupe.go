package security

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisDuplicateDetector 使用 Redis 实现全局去重窗口。
// 当 Redis 不可用时，会回退到内存去重。
type RedisDuplicateDetector struct {
	client    *redis.Client
	keyPrefix string
	fallback  *DuplicateDetector
	timeout   time.Duration
}

func NewRedisDuplicateDetector(client *redis.Client, keyPrefix string) *RedisDuplicateDetector {
	return &RedisDuplicateDetector{
		client:    client,
		keyPrefix: keyPrefix,
		fallback:  NewDuplicateDetector(),
		timeout:   800 * time.Millisecond,
	}
}

// SeenRecently 在窗口内重复返回 true；首次写入窗口返回 false。
func (d *RedisDuplicateDetector) SeenRecently(key string, window time.Duration) bool {
	if d == nil {
		return false
	}
	if d.client == nil {
		return d.fallback.SeenRecently(key, window)
	}

	ctx, cancel := context.WithTimeout(context.Background(), d.timeout)
	defer cancel()

	fullKey := fmt.Sprintf("%s:dedupe:%s", d.keyPrefix, key)
	ok, err := d.client.SetNX(ctx, fullKey, "1", window).Result()
	if err != nil {
		return d.fallback.SeenRecently(key, window)
	}
	return !ok
}
