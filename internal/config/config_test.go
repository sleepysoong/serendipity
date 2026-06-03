package config

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfig_MissingEnv(t *testing.T) {
	ctx := context.Background()

	// Clear environment variables
	os.Clearenv()

	_, err := LoadConfig(ctx, "manifest.json")
	if err == nil {
		t.Fatal("환경 변수 누락으로 인한 에러를 예상했으나 nil을 받았습니다")
	}
}

func TestLoadConfig_Success(t *testing.T) {
	ctx := context.Background()

	// Setup environment
	os.Setenv("BRAVE_API_KEY", "brave-test-key")
	os.Setenv("OPENROUTER_API_KEY", "openrouter-test-key")
	os.Setenv("FIGMA_PAT", "figma-test-pat")
	os.Setenv("FIGMA_FILE_KEY", "figma-test-file-key")
	os.Setenv("FIGMA_MCP_ENDPOINT", "figma-test-mcp-endpoint")
	defer os.Clearenv()

	// Setup temp manifest file
	tempDir := t.TempDir()
	manifestPath := filepath.Join(tempDir, "test_manifest.json")
	manifestContent := `{"Card_Page_1_Title": "node-1", "Card_Page_1_Body": "node-2"}`
	if err := os.WriteFile(manifestPath, []byte(manifestContent), 0644); err != nil {
		t.Fatalf("임시 매니페스트 파일 생성 실패: %v", err)
	}

	cfg, err := LoadConfig(ctx, manifestPath)
	if err != nil {
		t.Fatalf("LoadConfig 실패: %v", err)
	}

	if cfg.BraveAPIKey != "brave-test-key" {
		t.Errorf("BraveAPIKey 값 불일치: 예상 'brave-test-key', 실제 %q", cfg.BraveAPIKey)
	}
	if cfg.OpenRouterAPIKey != "openrouter-test-key" {
		t.Errorf("OpenRouterAPIKey 값 불일치: 예상 'openrouter-test-key', 실제 %q", cfg.OpenRouterAPIKey)
	}
	if cfg.LLMModel != "google/gemma-4-31b-it:free" {
		t.Errorf("LLMModel 기본값 불일치: 예상 'google/gemma-4-31b-it:free', 실제 %q", cfg.LLMModel)
	}
	if cfg.FigmaPAT != "figma-test-pat" {
		t.Errorf("FigmaPAT 값 불일치: 예상 'figma-test-pat', 실제 %q", cfg.FigmaPAT)
	}
	if cfg.FigmaFileKey != "figma-test-file-key" {
		t.Errorf("FigmaFileKey 값 불일치: 예상 'figma-test-file-key', 실제 %q", cfg.FigmaFileKey)
	}
	if cfg.FigmaMCPEndpoint != "figma-test-mcp-endpoint" {
		t.Errorf("FigmaMCPEndpoint 값 불일치: 예상 'figma-test-mcp-endpoint', 실제 %q", cfg.FigmaMCPEndpoint)
	}

	nodeVal, ok := cfg.NodeMappings["Card_Page_1_Title"]
	if !ok || nodeVal != "node-1" {
		t.Errorf("노드 매핑 값 불일치: 예상 Card_Page_1_Title -> 'node-1', 실제 %q (존재여부=%t)", nodeVal, ok)
	}
}
