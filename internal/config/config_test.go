package config

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfig_CreateDefault(t *testing.T) {
	ctx := context.Background()

	// Use temp dir for config.yml
	tempDir := t.TempDir()
	configPath = filepath.Join(tempDir, "config.yml")

	cfg, err := LoadConfig(ctx)
	if err != nil {
		t.Fatalf("LoadConfig 실패: %v", err)
	}

	if cfg.LLMModel != "google/gemma-4-31b-it:free" {
		t.Errorf("LLMModel 기본값 불일치: 예상 'google/gemma-4-31b-it:free', 실제 %q", cfg.LLMModel)
	}

	// Verify file was created
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		t.Errorf("기본 config.yml 파일이 생성되지 않음")
	}
}

func TestUpdateConfig(t *testing.T) {
	ctx := context.Background()

	// Use temp dir for config.yml
	tempDir := t.TempDir()
	configPath = filepath.Join(tempDir, "config.yml")

	// 1. Initial Load creates default config
	_, err := LoadConfig(ctx)
	if err != nil {
		t.Fatalf("초기 LoadConfig 실패: %v", err)
	}

	// 2. Update config
	err = UpdateConfig(ctx, func(c *Config) {
		c.BraveAPIKey = "new-brave-key"
		c.DiscordBotToken = "new-discord-token"
	})
	if err != nil {
		t.Fatalf("UpdateConfig 실패: %v", err)
	}

	// 3. Load again to verify
	cfg, err := LoadConfig(ctx)
	if err != nil {
		t.Fatalf("두 번째 LoadConfig 실패: %v", err)
	}

	if cfg.BraveAPIKey != "new-brave-key" {
		t.Errorf("BraveAPIKey 갱신 실패")
	}
	if cfg.DiscordBotToken != "new-discord-token" {
		t.Errorf("DiscordBotToken 갱신 실패")
	}
}
