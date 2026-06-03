package config

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
)

// Config holds environment configurations and Figma node mapping manifests.
type Config struct {
	BraveAPIKey      string
	OpenRouterAPIKey string
	LLMModel         string
	FigmaPAT         string
	FigmaFileKey     string
	FigmaMCPEndpoint string
	NodeMappings     map[string]string
}

// LoadConfig parses environment variables and loads node mapping schema into memory.
// It complies with the requirement that all function signatures must accept context.Context as their first parameter.
func LoadConfig(ctx context.Context, manifestPath string) (*Config, error) {
	// Context check to support early cancellation
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

	figmaPAT := os.Getenv("FIGMA_PAT")
	if figmaPAT == "" {
		return nil, fmt.Errorf("FIGMA_PAT 환경 변수가 설정되지 않았습니다")
	}

	figmaFileKey := os.Getenv("FIGMA_FILE_KEY")
	if figmaFileKey == "" {
		return nil, fmt.Errorf("FIGMA_FILE_KEY 환경 변수가 설정되지 않았습니다")
	}

	figmaMCPEndpoint := os.Getenv("FIGMA_MCP_ENDPOINT")
	if figmaMCPEndpoint == "" {
		return nil, fmt.Errorf("FIGMA_MCP_ENDPOINT 환경 변수가 설정되지 않았습니다")
	}

	if manifestPath == "" {
		manifestPath = "manifest.json"
	}

	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("매니페스트 파일(%s)을 읽지 못했습니다: %w", manifestPath, err)
	}

	var mappings map[string]string
	if err := json.Unmarshal(data, &mappings); err != nil {
		return nil, fmt.Errorf("매니페스트 파일(%s) 파싱에 실패했습니다: %w", manifestPath, err)
	}

	return &Config{
		BraveAPIKey:      braveKey,
		OpenRouterAPIKey: openRouterKey,
		LLMModel:         llmModel,
		FigmaPAT:         figmaPAT,
		FigmaFileKey:     figmaFileKey,
		FigmaMCPEndpoint: figmaMCPEndpoint,
		NodeMappings:     mappings,
	}, nil
}
