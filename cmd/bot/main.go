package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"serendipity/internal/bot"
	"serendipity/internal/config"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	log.Println("환경 설정을 로드합니다...")
	cfg, err := config.LoadConfig(ctx)
	if err != nil {
		log.Fatalf("설정 로드 실패: %v", err)
	}

	if cfg.DiscordBotToken == "" {
		log.Println("DiscordBotToken이 config.yml에 설정되어 있지 않습니다.")
		log.Println("봇을 시작하기 전 토큰을 추가해 주세요.")
		// Wait for user to add token or proceed with manual setup if we support it via CLI, 
		// but since Discord needs token to connect, we must exit.
		os.Exit(1)
	}

	discordBot, err := bot.NewBot(cfg)
	if err != nil {
		log.Fatalf("디스코드 봇 초기화 실패: %v", err)
	}

	if err := discordBot.Start(); err != nil {
		log.Fatalf("디스코드 봇 시작 실패: %v", err)
	}
	defer discordBot.Stop()

	log.Println("디스코드 봇이 정상적으로 시작되었습니다.")
	
	// Wait until termination signal
	<-ctx.Done()
	log.Println("종료 시그널 수신, 봇을 종료합니다...")
}
