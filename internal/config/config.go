package config

import (
	"context"
	"fmt"
	"os"
)

// Config는 환경 변수 기반 설정을 담는 구조체입니다.
type Config struct {
	BraveAPIKey      string
	OpenRouterAPIKey string
	LLMModel         string
}

// LoadConfig는 환경 변수를 파싱하여 설정 객체를 생성합니다.
// 모든 함수 시그니처는 context.Context를 첫 번째 매개변수로 받습니다.
func LoadConfig(ctx context.Context) (*Config, error) {
	// 컨텍스트 조기 취소 지원
	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("설정 로드 중 컨텍스트가 취소되었습니다: %w", ctx.Err())
	default:
	}

	braveKey := os.Getenv("BRAVE_API_KEY")
	if braveKey == "" {
		return nil, fmt.Errorf("BRAVE_API_KEY 환경 변수가 설정되지 않았습니다")
	}

	openRouterKey := os.Getenv("OPENROUTER_API_KEY")
	if openRouterKey == "" {
		return nil, fmt.Errorf("OPENROUTER_API_KEY 환경 변수가 설정되지 않았습니다")
	}

	llmModel := os.Getenv("LLM_MODEL")
	if llmModel == "" {
		llmModel = "google/gemma-4-31b-it:free"
	}

	return &Config{
		BraveAPIKey:      braveKey,
		OpenRouterAPIKey: openRouterKey,
		LLMModel:         llmModel,
	}, nil
}
