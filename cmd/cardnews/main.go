package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"serendipity/internal/config"
	"serendipity/internal/llm"
	"serendipity/internal/renderer"
	"serendipity/internal/search"
)

func main() {
	// 1. 인터럽트 시그널 기반 중앙 컨텍스트 생성
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// 2. 커맨드라인 플래그 파싱
	queryFlag := flag.String("query", "", "특정 주제로 카드뉴스를 생성할 경우의 검색 쿼리 (비어있으면 자동 주제 선정 모드)")
	autoFlag := flag.Bool("auto", true, "자동으로 오늘의 뉴스 주제를 선정할지 여부")
	modelFlag := flag.String("model", "", "사용할 LLM 모델명 (비어있으면 환경 변수 LLM_MODEL 또는 기본 gemma-4 모델 사용)")
	outputDirFlag := flag.String("output", "output", "생성된 카드뉴스 PNG를 저장할 로컬 디렉토리")
	topNFlag := flag.Int("topn", 3, "수집할 Brave Search 검색 결과 개수")
	flag.Parse()

	// queryFlag가 직접 입력되었으면 자동 모드를 비활성화하여 수동 입력 쿼리를 우선합니다.
	autoSelect := *autoFlag
	if *queryFlag != "" {
		autoSelect = false
	}

	log.Printf("자동화된 카드뉴스 생성 파이프라인을 시작합니다...")
	if autoSelect {
		log.Printf("모드: 자동 뉴스거리 선정 모드 (Search + LLM)")
	} else {
		log.Printf("모드: 수동 검색 쿼리 모드 (%q)", *queryFlag)
	}
	log.Printf("출력 디렉토리: %s", *outputDirFlag)
	log.Printf("상위 N개 결과: %d", *topNFlag)

	// 파이프라인 실행
	if err := runPipeline(ctx, *queryFlag, *modelFlag, *outputDirFlag, *topNFlag, autoSelect); err != nil {
		log.Printf("[치명적 오류] 파이프라인 실행 실패: %v", err)
		os.Exit(1)
	}

	log.Printf("파이프라인이 성공적으로 완료되었습니다!")
}

// runPipeline은 파이프라인 단계를 오케스트레이션합니다:
// 1. 설정 로드
// 1.5. (자동 모드) 뉴스거리 자동 선정
// 2. Brave Search로 데이터 수집
// 3. LLM으로 구조화된 카드 콘텐츠 생성
// 4. HTML 렌더링 + chromedp 스크린샷으로 PNG 생성
func runPipeline(ctx context.Context, query, modelOverride, outputDir string, topN int, autoSelect bool) error {
	// 1단계: 환경 설정 로드
	log.Println("[1/4] 설정을 불러오는 중...")
	cfg, err := config.LoadConfig(ctx)
	if err != nil {
		return fmt.Errorf("설정 로드 단계 실패: %w", err)
	}
	log.Println("설정이 성공적으로 로드되었습니다.")

	// 사용할 모델 결정
	model := cfg.LLMModel
	if modelOverride != "" {
		model = modelOverride
	}
	log.Printf("사용할 LLM 모델: %s", model)

	selectedTopic := query

	// 1.5단계: 자동 뉴스거리 선정
	if autoSelect {
		log.Println("[1.5/4] 최신 이슈 검색 및 자동 카드뉴스 주제 선정 중...")
		trendingQuery := "오늘의 주요 뉴스 시사 핫이슈"
		log.Printf("인기 시사 이슈 검색 중 (%q)...", trendingQuery)
		trendingContext, err := search.Search(ctx, cfg.BraveAPIKey, trendingQuery, 5)
		if err != nil {
			return fmt.Errorf("자동 주제 선정을 위한 검색 실패: %w", err)
		}

		log.Println("최신 뉴스 분석 및 적합한 주제 선정을 위한 LLM 가동 중...")
		topic, err := llm.SelectTopic(ctx, cfg.OpenRouterAPIKey, model, trendingContext)
		if err != nil {
			return fmt.Errorf("자동 카드뉴스 주제 선정 실패: %w", err)
		}
		log.Printf("선정된 카드뉴스 주제: %q", topic)
		selectedTopic = topic
	}

	// 2단계: Brave Search로 데이터 수집
	log.Printf("[2/4] %q에 대한 세부 컨텍스트 수집을 위해 Brave Search를 쿼리하는 중...", selectedTopic)
	groundingContext, err := search.Search(ctx, cfg.BraveAPIKey, selectedTopic, topN)
	if err != nil {
		return fmt.Errorf("데이터 수집 단계 실패: %w", err)
	}
	log.Println("기반 컨텍스트가 성공적으로 구축되었습니다.")

	// 3단계: LLM으로 구조화된 카드 콘텐츠 생성
	log.Printf("[3/4] 구조화된 카드 추론을 위해 OpenRouter %s 모델을 호출하는 중...", model)
	cards, err := llm.GenerateCardNews(ctx, cfg.OpenRouterAPIKey, model, groundingContext)
	if err != nil {
		return fmt.Errorf("추론 및 구조화 단계 실패: %w", err)
	}
	log.Printf("성공적으로 %d개의 구조화된 카드를 생성했습니다:", len(cards))
	for idx, card := range cards {
		log.Printf("  카드 %d: [%s] -> %s", idx+1, card.Title, card.Body)
	}

	// 4단계: HTML 렌더링 + chromedp 스크린샷으로 PNG 생성
	log.Println("[4/4] HTML 템플릿 렌더링 및 headless Chrome으로 카드 이미지를 생성하는 중...")
	if err := renderer.RenderCards(ctx, cards, outputDir); err != nil {
		return fmt.Errorf("카드 이미지 렌더링 단계 실패: %w", err)
	}

	log.Printf("모든 카드 이미지가 성공적으로 %s/ 에 저장되었습니다", outputDir)
	return nil
}
