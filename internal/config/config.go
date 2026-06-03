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
		return nil, fmt.Errorf("context cancelled during config load: %w", ctx.Err())
	default:
	}

	braveKey := os.Getenv("BRAVE_API_KEY")
	if braveKey == "" {
		return nil, fmt.Errorf("BRAVE_API_KEY environment variable is required")
	}

	openRouterKey := os.Getenv("OPENROUTER_API_KEY")
	if openRouterKey == "" {
		return nil, fmt.Errorf("OPENROUTER_API_KEY environment variable is required")
	}

	figmaPAT := os.Getenv("FIGMA_PAT")
	if figmaPAT == "" {
		return nil, fmt.Errorf("FIGMA_PAT environment variable is required")
	}

	figmaFileKey := os.Getenv("FIGMA_FILE_KEY")
	if figmaFileKey == "" {
		return nil, fmt.Errorf("FIGMA_FILE_KEY environment variable is required")
	}

	figmaMCPEndpoint := os.Getenv("FIGMA_MCP_ENDPOINT")
	if figmaMCPEndpoint == "" {
		return nil, fmt.Errorf("FIGMA_MCP_ENDPOINT environment variable is required")
	}

	if manifestPath == "" {
		manifestPath = "manifest.json"
	}

	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read manifest file at %s: %w", manifestPath, err)
	}

	var mappings map[string]string
	if err := json.Unmarshal(data, &mappings); err != nil {
		return nil, fmt.Errorf("failed to unmarshal manifest data from %s: %w", manifestPath, err)
	}

	return &Config{
		BraveAPIKey:      braveKey,
		OpenRouterAPIKey: openRouterKey,
		FigmaPAT:         figmaPAT,
		FigmaFileKey:     figmaFileKey,
		FigmaMCPEndpoint: figmaMCPEndpoint,
		NodeMappings:     mappings,
	}, nil
}
