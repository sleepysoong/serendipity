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
	"serendipity/internal/pipeline"
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

	// 파이프라인 실행
	cfg, err := config.LoadConfig(ctx)
	if err != nil {
		log.Fatalf("설정 로드 단계 실패: %v", err)
	}

	if *modelFlag != "" {
		cfg.LLMModel = *modelFlag
	}

	res, err := pipeline.Run(ctx, cfg, *queryFlag, *outputDirFlag, autoSelect)
	if err != nil {
		log.Printf("[치명적 오류] 파이프라인 실행 실패: %v", err)
		os.Exit(1)
	}

	log.Printf("파이프라인이 성공적으로 완료되었습니다! 저장된 변형:")
	for _, dir := range res.OutputDirs {
		fmt.Printf(" - %s\n", dir)
	}
}
