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
	queryFlag := flag.String("query", "한국은행 기준금리 동결", "컨텍스트 데이터를 수집하기 위한 검색 쿼리")
	manifestFlag := flag.String("manifest", "manifest.json", "Figma 노드 매핑 매니페스트 JSON 파일 경로")
	outputDirFlag := flag.String("output", "output", "내보낸 카드뉴스 PNG를 저장할 로컬 디렉토리")
	topNFlag := flag.Int("topn", 3, "수집할 Brave Search 검색 결과 개수")
	flag.Parse()

	log.Printf("자동화된 카드뉴스 생성 파이프라인을 시작합니다...")
	log.Printf("검색 쿼리: %s", *queryFlag)
	log.Printf("매니페스트: %s", *manifestFlag)
	log.Printf("출력 디렉토리: %s", *outputDirFlag)
	log.Printf("상위 N개 결과: %d", *topNFlag)

	// Execute pipeline and print structured error messages if any phase fails
	if err := runPipeline(ctx, *queryFlag, *manifestFlag, *outputDirFlag, *topNFlag); err != nil {
		log.Printf("[치명적 오류] 파이프라인 실행 실패: %v", err)
		os.Exit(1)
	}

	log.Printf("파이프라인이 성공적으로 완료되었습니다!")
}

func runPipeline(ctx context.Context, query, manifestPath, outputDir string, topN int) error {
	// Step 1: Environment and configuration loading
	log.Println("[1/6] 설정 및 매핑 매니페스트를 불러오는 중...")
	cfg, err := config.LoadConfig(ctx, manifestPath)
	if err != nil {
		return fmt.Errorf("설정 로드 단계 실패: %w", err)
	}
	log.Println("설정이 성공적으로 로드되었습니다.")

	// Step 2: Data collection from Brave Search
	log.Printf("[2/6] %q에 대한 컨텍스트 수집을 위해 Brave Search를 쿼리하는 중...", query)
	groundingContext, err := search.Search(ctx, cfg.BraveAPIKey, query, topN)
	if err != nil {
		return fmt.Errorf("데이터 수집 단계 실패: %w", err)
	}
	log.Println("기반 컨텍스트가 성공적으로 구축되었습니다.")

	// Step 3: Structured content generation via OpenRouter Gemma 4
	log.Println("[3/6] 구조화된 카드 추론을 위해 OpenRouter Gemma 4를 호출하는 중...")
	cards, err := llm.GenerateCardNews(ctx, cfg.OpenRouterAPIKey, groundingContext)
	if err != nil {
		return fmt.Errorf("추론 및 구조화 단계 실패: %w", err)
	}
	log.Printf("성공적으로 %d개의 구조화된 카드를 생성했습니다:", len(cards))
	for idx, card := range cards {
		log.Printf("  카드 %d: [%s] -> %s", idx+1, card.Title, card.Body)
	}

	// Step 4: Figma Canvas Update via Remote MCP (SSE)
	log.Println("[4/6] Figma MCP 서버에 연결하여 캔버스 노드를 업데이트하는 중...")
	beforeMCPTime, err := figma.UpdateCanvas(ctx, cfg.FigmaPAT, cfg.FigmaMCPEndpoint, cfg.NodeMappings, cards)
	if err != nil {
		return fmt.Errorf("Figma 캔버스 수정 단계 실패: %w", err)
	}
	log.Println("캔버스 텍스트 업데이트가 실행되었습니다. 동기화 대기 중...")

	// Step 5: Double-barrier synchronization (JSON-RPC + REST API version polling)
	log.Println("[5/6] 렌더링 완료 여부를 확인하기 위해 Figma 파일 API를 폴링하는 중...")
	if err := figma.PollVersion(ctx, cfg.FigmaPAT, cfg.FigmaFileKey, beforeMCPTime); err != nil {
		return fmt.Errorf("캔버스 동기화 단계 실패: %w", err)
	}
	log.Println("Figma 캔버스 그래픽 파이프라인 동기화가 완료되었습니다.")

	// Step 6: Image export and local downloading
	log.Println("[6/6] Figma REST API에 고화질 PNG 내보내기를 요청하는 중...")

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
		return fmt.Errorf("매니페스트에서 이미지 내보내기를 위한 Figma 노드를 찾을 수 없습니다")
	}

	exportUrls, err := figma.ExportImages(ctx, cfg.FigmaPAT, cfg.FigmaFileKey, exportNodeIDs)
	if err != nil {
		return fmt.Errorf("Figma 이미지 내보내기 요청 실패: %w", err)
	}

	// Ensure output directory exists
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("출력 디렉토리 %s 생성 실패: %w", outputDir, err)
	}

	for nodeID, downloadURL := range exportUrls {
		filename := nodeToFilename[nodeID]
		if filename == "" {
			filename = fmt.Sprintf("exported_node_%s.png", nodeID)
		}
		outputPath := filepath.Join(outputDir, filename)
		log.Printf("노드 %s의 렌더링된 이미지를 %s로 다운로드하는 중...", nodeID, outputPath)

		if err := figma.DownloadImage(ctx, downloadURL, outputPath); err != nil {
			return fmt.Errorf("노드 %s의 이미지 다운로드 실패: %w", nodeID, err)
		}
	}

	log.Printf("모든 %d개 이미지가 성공적으로 내보내져 %s/ 에 저장되었습니다", len(exportUrls), outputDir)
	return nil
}
