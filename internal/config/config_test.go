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
		t.Fatal("expected error due to missing environment variables, got nil")
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
		t.Fatalf("failed to write temp manifest: %v", err)
	}

	cfg, err := LoadConfig(ctx, manifestPath)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}

	if cfg.BraveAPIKey != "brave-test-key" {
		t.Errorf("expected BraveAPIKey 'brave-test-key', got %q", cfg.BraveAPIKey)
	}
	if cfg.OpenRouterAPIKey != "openrouter-test-key" {
		t.Errorf("expected OpenRouterAPIKey 'openrouter-test-key', got %q", cfg.OpenRouterAPIKey)
	}
	if cfg.FigmaPAT != "figma-test-pat" {
		t.Errorf("expected FigmaPAT 'figma-test-pat', got %q", cfg.FigmaPAT)
	}
	if cfg.FigmaFileKey != "figma-test-file-key" {
		t.Errorf("expected FigmaFileKey 'figma-test-file-key', got %q", cfg.FigmaFileKey)
	}
	if cfg.FigmaMCPEndpoint != "figma-test-mcp-endpoint" {
		t.Errorf("expected FigmaMCPEndpoint 'figma-test-mcp-endpoint', got %q", cfg.FigmaMCPEndpoint)
	}

	nodeVal, ok := cfg.NodeMappings["Card_Page_1_Title"]
	if !ok || nodeVal != "node-1" {
		t.Errorf("expected mapping Card_Page_1_Title -> 'node-1', got %q (ok=%t)", nodeVal, ok)
	}
}
