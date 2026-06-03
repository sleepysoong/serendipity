package config

import (
	"context"
	"fmt"
	"os"
	"sync"

	"gopkg.in/yaml.v3"
)

// Config는 yaml 기반 설정을 담는 구조체입니다.
type Config struct {
	BraveAPIKey      string `yaml:"brave_api_key"`
	OpenRouterAPIKey string `yaml:"openrouter_api_key"`
	LLMModel         string `yaml:"llm_model"`
	DiscordBotToken  string `yaml:"discord_bot_token"`
}

var (
	configPath = "config.yml"
	mu         sync.Mutex
)

// LoadConfig는 config.yml을 파싱하여 설정 객체를 생성합니다.
// 모든 함수 시그니처는 context.Context를 첫 번째 매개변수로 받습니다.
func LoadConfig(ctx context.Context) (*Config, error) {
	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("설정 로드 중 컨텍스트가 취소되었습니다: %w", ctx.Err())
	default:
	}

	mu.Lock()
	defer mu.Unlock()

	file, err := os.Open(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			// 기본 설정 파일 생성
			defaultCfg := &Config{
				LLMModel: "google/gemma-4-31b-it:free",
			}
			err = saveConfigWithoutLock(defaultCfg)
			if err != nil {
				return nil, fmt.Errorf("기본 config.yml 생성 실패: %w", err)
			}
			return defaultCfg, nil
		}
		return nil, fmt.Errorf("config.yml 열기 실패: %w", err)
	}
	defer file.Close()

	var cfg Config
	decoder := yaml.NewDecoder(file)
	if err := decoder.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("config.yml 디코딩 실패: %w", err)
	}

	if cfg.LLMModel == "" {
		cfg.LLMModel = "google/gemma-4-31b-it:free"
	}

	return &cfg, nil
}

// UpdateConfig는 설정을 갱신하고 config.yml에 저장합니다.
func UpdateConfig(ctx context.Context, modifyFunc func(*Config)) error {
	select {
	case <-ctx.Done():
		return fmt.Errorf("설정 갱신 중 컨텍스트가 취소되었습니다: %w", ctx.Err())
	default:
	}

	mu.Lock()
	defer mu.Unlock()

	var cfg Config
	file, err := os.Open(configPath)
	if err == nil {
		decoder := yaml.NewDecoder(file)
		_ = decoder.Decode(&cfg)
		file.Close()
	}

	// 콜백을 통해 수정 적용
	modifyFunc(&cfg)

	return saveConfigWithoutLock(&cfg)
}

func saveConfigWithoutLock(cfg *Config) error {
	file, err := os.OpenFile(configPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	defer file.Close()

	encoder := yaml.NewEncoder(file)
	defer encoder.Close()

	return encoder.Encode(cfg)
}
