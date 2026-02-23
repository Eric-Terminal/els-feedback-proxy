package main

import (
	"log"

	"els-feedback-proxy/internal/api"
	"els-feedback-proxy/internal/config"
	"els-feedback-proxy/internal/github"
	"els-feedback-proxy/internal/security"
	"els-feedback-proxy/internal/store"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("配置加载失败: %v", err)
	}

	ghClient := github.NewClient(cfg.GitHubToken, cfg.GitHubOwner, cfg.GitHubRepo)
	limiter := security.NewFixedWindowLimiter()
	dedupe := security.NewDuplicateDetector()
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

	srv := api.NewServer(cfg, ghClient, limiter, dedupe, challenges, ticketStore)

	log.Printf("ELS Feedback Proxy 启动: :%s", cfg.Port)
	if err := srv.Run(); err != nil {
		log.Fatalf("服务异常退出: %v", err)
	}
}
