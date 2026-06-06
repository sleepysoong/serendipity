package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"serendipity/internal/config"
	"serendipity/internal/llm"
	"serendipity/internal/pipeline"
	"serendipity/internal/renderer"
)

func main() {
	mode := flag.String("mode", "pipeline", "실행 모드 (pipeline: 전체 자동 뉴스 생성, render: 제공된 카드로 렌더링만 수행)")
	topic := flag.String("topic", "", "카드뉴스 주제 (비어 있으면 실시간 뉴스로 자동 선정)")
	output := flag.String("output", "./output", "카드뉴스 결과물이 생성될 폴더 경로")
	configPath := flag.String("config", "config.yml", "설정 파일(config.yml) 경로")
	auto := flag.Bool("auto", false, "자동 뉴스 탐색 강제 여부")
	cardsJSON := flag.String("cards", "", "렌더링할 카드뉴스 JSON 데이터 (render 모드용)")
	bgPath := flag.String("bg", "", "배경 이미지 파일 경로 (render 모드용)")
	flag.Parse()

	ctx := context.Background()

	if *mode == "render" {
		if *cardsJSON == "" {
			fmt.Fprintln(os.Stderr, "Error: -cards JSON string is required for render mode")
			os.Exit(1)
		}

		var cards []llm.CardContent
		if err := json.Unmarshal([]byte(*cardsJSON), &cards); err != nil {
			fmt.Fprintf(os.Stderr, "Error parsing cards JSON: %v\n", err)
			os.Exit(1)
		}

		if err := os.MkdirAll(*output, 0755); err != nil {
			fmt.Fprintf(os.Stderr, "Error creating output directory: %v\n", err)
			os.Exit(1)
		}

		err := renderer.RenderCards(ctx, cards, *output, *bgPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error rendering cards: %v\n", err)
			os.Exit(1)
		}

		// render 모드 성공 시의 output JSON
		res := pipeline.PipelineResult{
			Topic:      cards[0].Title, // 대표 주제
			Cards:      cards,
			OutputDirs: []string{*output},
		}
		if *bgPath != "" {
			res.BgImageURLs = []string{*bgPath} // 로컬 경로를 보관
		}

		outJSON, err := json.MarshalIndent(res, "", "  ")
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error marshaling result: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(string(outJSON))
		return
	}

	// config.yml 경로 지정
	config.SetPath(*configPath)

	cfg, err := config.LoadConfig(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "설정 파일 로드 실패: %v\n", err)
		os.Exit(1)
	}

	// CLI 진행 로그는 Stderr로 스트리밍 (디스코드 봇 등 상위 프로그램이 파싱할 수 있게)
	logf := func(msg string) {
		fmt.Fprintln(os.Stderr, msg)
	}

	// 만약 토픽이 지정되지 않았으면 자동 주제 선정을 활성화
	isAuto := *auto || (*topic == "")

	res, err := pipeline.Run(ctx, cfg, *topic, *output, isAuto, logf)
	if err != nil {
		fmt.Fprintf(os.Stderr, "파이프라인 실행 중 오류 발생: %v\n", err)
		os.Exit(1)
	}

	// 성공 시 JSON 형태로 결과를 Stdout에 출력
	outJSON, err := json.MarshalIndent(res, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "결과 직렬화 실패: %v\n", err)
		os.Exit(1)
	}

	fmt.Println(string(outJSON))
}
