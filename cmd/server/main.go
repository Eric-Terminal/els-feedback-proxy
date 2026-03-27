package main

import (
	"context"
	"log"
	"time"

	"els-feedback-proxy/internal/api"
	"els-feedback-proxy/internal/config"
	"els-feedback-proxy/internal/github"
	"els-feedback-proxy/internal/moderation"
	"els-feedback-proxy/internal/security"
	"els-feedback-proxy/internal/store"

	"github.com/redis/go-redis/v9"
)

type rateLimiter interface {
	Allow(key string, limit int, window time.Duration) bool
}

type duplicateDetector interface {
	SeenRecently(key string, window time.Duration) bool
}

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("配置加载失败: %v", err)
	}

	ghClient := github.NewClient(cfg.GitHubToken, cfg.GitHubOwner, cfg.GitHubRepo)
	if cfg.GitHubTokenLogin == "" {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		login, loginErr := ghClient.GetAuthenticatedLogin(ctx)
		cancel()
		if loginErr != nil {
			log.Printf("自动识别 token 所属账号失败: %v", loginErr)
		} else {
			cfg.GitHubTokenLogin = login
			log.Printf("已识别 token 所属账号: %s", login)
		}
	}
	var limiter rateLimiter = security.NewFixedWindowLimiter()
	var dedupe duplicateDetector = security.NewDuplicateDetector()

	if cfg.RedisAddr != "" {
		redisClient := redis.NewClient(&redis.Options{
			Addr:     cfg.RedisAddr,
			Password: cfg.RedisPassword,
			DB:       cfg.RedisDB,
		})

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		pingErr := redisClient.Ping(ctx).Err()
		cancel()
		if pingErr != nil {
			log.Printf("Redis 连接失败，回退到内存风控: %v", pingErr)
		} else {
			log.Printf("Redis 已连接，启用全局限流与去重")
			limiter = security.NewRedisFixedWindowLimiter(redisClient, cfg.RedisKeyPrefix)
			dedupe = security.NewRedisDuplicateDetector(redisClient, cfg.RedisKeyPrefix)
		}
	}

	challenges := security.NewChallengeManager(
		cfg.ChallengeTTL,
		cfg.TimestampSkew,
		cfg.SignatureFailThreshold,
		cfg.SignatureBlockDuration,
	)

	ticketStore, err := store.NewTicketStore(cfg.DataDir)
	if err != nil {
		log.Fatalf("票据存储初始化失败: %v", err)
	}

	blockedArchiveStore, err := store.NewBlockedArchiveStore(cfg.DataDir)
	if err != nil {
		log.Fatalf("审核留档存储初始化失败: %v", err)
	}

	var reviewer moderation.Reviewer = moderation.AllowAllReviewer{}
	if cfg.ModerationEnabled {
		reviewer = moderation.NewOpenAIReviewer(moderation.OpenAIReviewerConfig{
			BaseURL:     cfg.ModerationAPIBaseURL,
			APIKey:      cfg.ModerationAPIKey,
			Model:       cfg.ModerationModel,
			Timeout:     cfg.ModerationTimeout,
			MaxRetries:  cfg.ModerationMaxRetries,
			Temperature: cfg.ModerationTemperature,
		})
	}

	srv := api.NewServer(cfg, ghClient, limiter, dedupe, challenges, ticketStore, reviewer, blockedArchiveStore)

	log.Printf("ELS Feedback Proxy 启动: :%s", cfg.Port)
	if err := srv.Run(); err != nil {
		log.Fatalf("服务异常退出: %v", err)
	}
}
