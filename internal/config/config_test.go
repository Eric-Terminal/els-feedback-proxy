package config

import (
	"strings"
	"testing"
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
