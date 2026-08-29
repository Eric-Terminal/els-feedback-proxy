package config

import (
	"strings"
	"testing"
	"time"
)

func TestLoadAdminWebAuthDisabled(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "test-github-token")
	t.Setenv("MODERATION_ENABLED", "false")
	t.Setenv("ADMIN_LISTEN_ADDR", "127.0.0.1:8521")
	t.Setenv("ANNOUNCEMENT_ADMIN_TOKEN", "test-admin-token")
	t.Setenv("ADMIN_WEB_AUTH_DISABLED", "true")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("加载免登录管理配置失败: %v", err)
	}
	if !cfg.AdminWebAuthDisabled {
		t.Fatalf("应启用管理页面免登录")
	}
}

func TestLoadAdminWebAuthDisabledRequiresSessionSecret(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "test-github-token")
	t.Setenv("MODERATION_ENABLED", "false")
	t.Setenv("ADMIN_LISTEN_ADDR", "127.0.0.1:8521")
	t.Setenv("ANNOUNCEMENT_ADMIN_TOKEN", "")
	t.Setenv("ADMIN_WEB_AUTH_DISABLED", "true")

	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "ANNOUNCEMENT_ADMIN_TOKEN") {
		t.Fatalf("缺少会话签名口令时应拒绝免登录配置，实际错误: %v", err)
	}
}

func TestLoadTelemetryDefaultsAndLimits(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "test-github-token")
	t.Setenv("MODERATION_ENABLED", "false")
	t.Setenv("TELEMETRY_RATE_LIMIT_PER_MINUTE", "0")
	t.Setenv("TELEMETRY_RETENTION_DAYS", "999")
	t.Setenv("TELEMETRY_MAX_TOTAL_BYTES", "1024")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("加载遥测配置失败: %v", err)
	}
	if cfg.TelemetryRateLimit != 1 {
		t.Fatalf("遥测限流下限应为 1，实际 %d", cfg.TelemetryRateLimit)
	}
	if cfg.TelemetryRetentionDays != 365 {
		t.Fatalf("遥测保留下限/上限未生效，实际 %d", cfg.TelemetryRetentionDays)
	}
	if cfg.TelemetryMaxTotalBytes != 1<<20 {
		t.Fatalf("遥测总配额下限应为 1 MiB，实际 %d", cfg.TelemetryMaxTotalBytes)
	}
}

func TestLoadGuideConfiguration(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "test-github-token")
	t.Setenv("MODERATION_ENABLED", "false")
	t.Setenv("GUIDE_ENABLED", "true")
	t.Setenv("GUIDE_TOKEN_SECRET", "0123456789abcdef0123456789abcdef")
	t.Setenv("GUIDE_UPSTREAM_API_KEY", "test-guide-key")
	t.Setenv("GUIDE_IP_CONCURRENCY", "99")
	t.Setenv("GUIDE_REQUEST_TIMEOUT_SECONDS", "5")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("加载向导配置失败: %v", err)
	}
	if cfg.GuideUpstreamBaseURL != "https://api.ericterminal.com/v1" {
		t.Fatalf("向导上游地址未标准化: %q", cfg.GuideUpstreamBaseURL)
	}
	if cfg.GuideIPConcurrency != 16 {
		t.Fatalf("向导 IP 并发上限未钳制: %d", cfg.GuideIPConcurrency)
	}
	if cfg.GuideRequestTimeout != 30*time.Second {
		t.Fatalf("向导响应头超时下限未生效: %s", cfg.GuideRequestTimeout)
	}
}

func TestLoadGuideRequiresServerSecrets(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "test-github-token")
	t.Setenv("MODERATION_ENABLED", "false")
	t.Setenv("GUIDE_ENABLED", "true")
	t.Setenv("GUIDE_TOKEN_SECRET", "too-short")
	t.Setenv("GUIDE_UPSTREAM_API_KEY", "")

	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "GUIDE_TOKEN_SECRET") {
		t.Fatalf("向导签名秘密不足时应拒绝启动，实际错误: %v", err)
	}
}
