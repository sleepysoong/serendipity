package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"serendipity/internal/config"
	"serendipity/internal/figma"
	"serendipity/internal/llm"
	"serendipity/internal/search"
)

func main() {
	// 1. Establish central context with cancellation support on interrupt signals
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// 2. Parse command-line flags
	queryFlag := flag.String("query", "한국은행 기준금리 동결", "Search query to gather context data")
	manifestFlag := flag.String("manifest", "manifest.json", "Path to Figma node mapping manifest JSON file")
	outputDirFlag := flag.String("output", "output", "Local directory to write exported card news PNGs")
	topNFlag := flag.Int("topn", 3, "Number of Brave Search results to compile")
	flag.Parse()

	log.Printf("Starting automated card news generation pipeline...")
	log.Printf("Query: %s", *queryFlag)
	log.Printf("Manifest: %s", *manifestFlag)
	log.Printf("Output directory: %s", *outputDirFlag)
	log.Printf("Top N results: %d", *topNFlag)

	// Execute pipeline and print structured error messages if any phase fails
	if err := runPipeline(ctx, *queryFlag, *manifestFlag, *outputDirFlag, *topNFlag); err != nil {
		log.Printf("[FATAL ERROR] Pipeline execution failed: %v", err)
		os.Exit(1)
	}

	log.Printf("Pipeline completed successfully!")
}

func runPipeline(ctx context.Context, query, manifestPath, outputDir string, topN int) error {
	// Step 1: Environment and configuration loading
	log.Println("[1/6] Loading configurations and mapping manifest...")
	cfg, err := config.LoadConfig(ctx, manifestPath)
	if err != nil {
		return fmt.Errorf("configuration phase failed: %w", err)
	}
	log.Println("Configurations successfully loaded.")

	// Step 2: Data collection from Brave Search
	log.Printf("[2/6] Querying Brave Search for context on %q...", query)
	groundingContext, err := search.Search(ctx, cfg.BraveAPIKey, query, topN)
	if err != nil {
		return fmt.Errorf("data collection phase failed: %w", err)
	}
	log.Println("Grounding context compiled successfully.")

	// Step 3: Structured content generation via OpenRouter Gemma 4
	log.Println("[3/6] Invoking OpenRouter Gemma 4 for structured card reasoning...")
	cards, err := llm.GenerateCardNews(ctx, cfg.OpenRouterAPIKey, groundingContext)
	if err != nil {
		return fmt.Errorf("reasoning/structuring phase failed: %w", err)
	}
	log.Printf("Successfully generated %d structured cards:", len(cards))
	for idx, card := range cards {
		log.Printf("  Card %d: [%s] -> %s", idx+1, card.Title, card.Body)
	}

	// Step 4: Figma Canvas Update via Remote MCP (SSE)
	log.Println("[4/6] Connecting to Figma MCP Server and updating canvas nodes...")
	beforeMCPTime, err := figma.UpdateCanvas(ctx, cfg.FigmaPAT, cfg.FigmaMCPEndpoint, cfg.NodeMappings, cards)
	if err != nil {
		return fmt.Errorf("figma canvas modification phase failed: %w", err)
	}
	log.Println("Canvas text updates executed. Waiting for synchronization barriers...")

	// Step 5: Double-barrier synchronization (JSON-RPC + REST API version polling)
	log.Println("[5/6] Polling Figma file API to verify rendering completion...")
	if err := figma.PollVersion(ctx, cfg.FigmaPAT, cfg.FigmaFileKey, beforeMCPTime); err != nil {
		return fmt.Errorf("canvas synchronization phase failed: %w", err)
	}
	log.Println("Figma canvas graphics pipeline synchronization complete.")

	// Step 6: Image export and local downloading
	log.Println("[6/6] Requesting high-fidelity PNG export from Figma REST API...")

	// Determine node IDs to export from our mapping config
	var exportNodeIDs []string
	nodeToFilename := make(map[string]string)

	for i := 1; i <= len(cards); i++ {
		frameKey := fmt.Sprintf("Card_Page_%d_Frame", i)
		if frameID, ok := cfg.NodeMappings[frameKey]; ok {
			exportNodeIDs = append(exportNodeIDs, frameID)
			nodeToFilename[frameID] = fmt.Sprintf("card_page_%d.png", i)
		} else {
			// Fallback to title and body elements if parent frame mapping is missing
			titleKey := fmt.Sprintf("Card_Page_%d_Title", i)
			if titleID, ok := cfg.NodeMappings[titleKey]; ok {
				exportNodeIDs = append(exportNodeIDs, titleID)
				nodeToFilename[titleID] = fmt.Sprintf("card_page_%d_title.png", i)
			}
			bodyKey := fmt.Sprintf("Card_Page_%d_Body", i)
			if bodyID, ok := cfg.NodeMappings[bodyKey]; ok {
				exportNodeIDs = append(exportNodeIDs, bodyID)
				nodeToFilename[bodyID] = fmt.Sprintf("card_page_%d_body.png", i)
			}
		}
	}

	if len(exportNodeIDs) == 0 {
		return fmt.Errorf("no Figma nodes could be resolved for image export from manifest")
	}

	exportUrls, err := figma.ExportImages(ctx, cfg.FigmaPAT, cfg.FigmaFileKey, exportNodeIDs)
	if err != nil {
		return fmt.Errorf("figma image export request failed: %w", err)
	}

	// Ensure output directory exists
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("failed to create output directory %s: %w", outputDir, err)
	}

	for nodeID, downloadURL := range exportUrls {
		filename := nodeToFilename[nodeID]
		if filename == "" {
			filename = fmt.Sprintf("exported_node_%s.png", nodeID)
		}
		outputPath := filepath.Join(outputDir, filename)
		log.Printf("Downloading rendered image for node %s to %s...", nodeID, outputPath)

		if err := figma.DownloadImage(ctx, downloadURL, outputPath); err != nil {
			return fmt.Errorf("failed to download image for node %s: %w", nodeID, err)
		}
	}

	log.Printf("All %d images successfully exported and saved to %s/", len(exportUrls), outputDir)
	return nil
}
