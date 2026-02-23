package config

import (
	"errors"
	"os"
	"strconv"
	"time"
)

// Config 运行时配置
type Config struct {
	Port                    string
	GitHubToken             string
	GitHubOwner             string
	GitHubRepo              string
	DataDir                 string
	RequiredUAKeyword       string
	IssuesPath              string
	RateWindow              time.Duration
	ChallengeTTL            time.Duration
	TimestampSkew           time.Duration
	DuplicateWindow         time.Duration
	SignatureFailThreshold  int
	SignatureBlockDuration  time.Duration
	ChallengeLimitPerWindow int
	SubmitLimitPerWindow    int
	QueryLimitPerWindow     int
}

// Load 从环境变量加载配置
func Load() (Config, error) {
	cfg := Config{
		Port:                    getEnv("PORT", "8080"),
		GitHubToken:             os.Getenv("GITHUB_TOKEN"),
		GitHubOwner:             getEnv("GITHUB_OWNER", "Eric-Terminal"),
		GitHubRepo:              getEnv("GITHUB_REPO", "ETOS-LLM-Studio"),
		DataDir:                 getEnv("DATA_DIR", "./data"),
		RequiredUAKeyword:       getEnv("REQUIRED_UA_KEYWORD", "ETOS LLM Studio"),
		IssuesPath:              "/v1/feedback/issues",
		RateWindow:              15 * time.Minute,
		ChallengeTTL:            120 * time.Second,
		TimestampSkew:           90 * time.Second,
		DuplicateWindow:         10 * time.Minute,
		SignatureFailThreshold:  5,
		SignatureBlockDuration:  10 * time.Minute,
		ChallengeLimitPerWindow: getEnvAsInt("CHALLENGE_LIMIT_PER_WINDOW", 30),
		SubmitLimitPerWindow:    getEnvAsInt("SUBMIT_LIMIT_PER_WINDOW", 6),
		QueryLimitPerWindow:     getEnvAsInt("QUERY_LIMIT_PER_WINDOW", 60),
	}

	if cfg.GitHubToken == "" {
		return Config{}, errors.New("缺少 GITHUB_TOKEN")
	}

	return cfg, nil
}

func getEnv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func getEnvAsInt(key string, fallback int) int {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}
