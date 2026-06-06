package pipeline

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"serendipity/internal/config"
	"serendipity/internal/llm"
	"serendipity/internal/renderer"
	"serendipity/internal/search"
)

// PipelineResult holds the outcome of a pipeline run
type PipelineResult struct {
	Topic       string
	Cards       []llm.CardContent
	OutputDirs  []string // Paths to directories containing the generated images for each background variation
	BgImageURLs []string
}

func Run(ctx context.Context, cfg *config.Config, query, outputBaseDir string, autoSelect bool, logf func(string)) (*PipelineResult, error) {
	log.Println("[1/5] 뉴스거리 탐색 및 선정...")
	selectedTopic := query
	var groundingContext string

	if autoSelect || query == "" {
		log.Printf("연합뉴스 실시간 주요 기사 검색 중...")
		if logf != nil {
			logf("● **`서칭을 시작합니다`**  |  `연합뉴스 실시간 주요 기사`")
		}
		
		articles, err := search.FetchYonhapTopNews(ctx, 10)
		if err != nil || len(articles) == 0 {
			// Fallback to old behavior if Yonhap fails
			log.Printf("연합뉴스 검색 실패, 기본 Brave Search로 폴백합니다: %v", err)
			trendingQuery := "오늘의 주요 뉴스 시사 핫이슈"
			trendingContext, err := search.Search(ctx, cfg.BraveAPIKey, trendingQuery, 5)
			if err != nil {
				return nil, fmt.Errorf("자동 주제 선정을 위한 검색 실패: %w", err)
			}
	
			topic, err := llm.SelectTopic(ctx, cfg.OpenRouterAPIKey, cfg.LLMModel, trendingContext, logf)
			if err != nil {
				return nil, fmt.Errorf("자동 카드뉴스 주제 선정 실패: %w", err)
			}
			selectedTopic = topic

			log.Printf("[2/5] %q에 대한 세부 컨텍스트 수집...", selectedTopic)
			if logf != nil {
				logf(fmt.Sprintf("● **`세부 정보 검색을 시작합니다`**  |  `%s`", selectedTopic))
			}
			groundingContext, err = search.Search(ctx, cfg.BraveAPIKey, selectedTopic, 3)
			if err != nil {
				return nil, fmt.Errorf("데이터 수집 실패: %w", err)
			}
		} else {
			// Format articles for LLM
			var contextBuilder strings.Builder
			for _, art := range articles {
				contextBuilder.WriteString(fmt.Sprintf("ID: %s\n제목: %s\n요약: %s\n섹션: %s\n\n", art.ID, art.Title, art.Description, art.Section))
			}

			// Ask LLM to pick one
			selectedID, err := llm.SelectArticleID(ctx, cfg.OpenRouterAPIKey, cfg.LLMModel, contextBuilder.String(), logf)
			if err != nil {
				return nil, fmt.Errorf("기사 선정 실패: %w", err)
			}

			var selectedArticle *search.ArticleMeta
			for _, art := range articles {
				if art.ID == selectedID {
					selectedArticle = &art
					break
				}
			}

			if selectedArticle == nil {
				// Fallback if LLM hallucinations an ID
				selectedArticle = &articles[0]
			}

			selectedTopic = selectedArticle.Title
			log.Printf("선정된 카드뉴스 주제: %q", selectedTopic)

			body, err := search.FetchYonhapArticleBody(ctx, selectedArticle.URL)
			if err != nil {
				return nil, fmt.Errorf("기사 본문 추출 실패: %w", err)
			}

			// 교차 검증을 위한 추가 웹 검색 수행
			log.Printf("교차 검증을 위해 추가 세부 컨텍스트 수집 중: %q...", selectedTopic)
			if logf != nil {
				logf(fmt.Sprintf("● **`교차 검증 및 보완 검색을 진행합니다`**  |  `%s`", selectedTopic))
			}
			crossCheckContext, err := search.Search(ctx, cfg.BraveAPIKey, selectedTopic, 3)
			if err != nil {
				log.Printf("교차 검증 검색 실패 (연합뉴스 본문 정보만 사용): %v", err)
				crossCheckContext = "추가 웹 검색에 실패하였습니다."
			}

			groundingContext = fmt.Sprintf("제목: %s\n요약: %s\n본문: %s\n\n[교차 검증 및 보완 정보]\n%s", selectedArticle.Title, selectedArticle.Description, body, crossCheckContext)
		}
	} else {
		log.Printf("[2/5] %q에 대한 세부 컨텍스트 수집...", selectedTopic)
		if logf != nil {
			logf(fmt.Sprintf("● **`서칭을 시작합니다`**  |  `%s`", selectedTopic))
		}
		var err error
		groundingContext, err = search.Search(ctx, cfg.BraveAPIKey, selectedTopic, 3)
		if err != nil {
			return nil, fmt.Errorf("데이터 수집 실패: %w", err)
		}
	}

	log.Printf("[3/5] 카드 콘텐츠 생성...")
	cards, err := llm.GenerateCardNews(ctx, cfg.OpenRouterAPIKey, cfg.LLMModel, groundingContext, logf)
	if err != nil {
		return nil, fmt.Errorf("카드 콘텐츠 생성 실패: %w", err)
	}
	
	log.Printf("[4/5] 표지용 배경 이미지 3개 탐색...")
	// Search for images based on the first card's title or the selected topic
	imageQuery := cards[0].Title
	if imageQuery == "" {
		imageQuery = selectedTopic
	}
	imageURLs, err := search.SearchImages(ctx, cfg.BraveAPIKey, imageQuery, 3)
	if err != nil {
		log.Printf("이미지 탐색 실패 (기본 배경 사용): %v", err)
		imageURLs = []string{}
	}

	log.Printf("[5/5] 이미지 다운로드 및 렌더링...")
	var outputDirs []string

	// Create base output dir
	if err := os.MkdirAll(outputBaseDir, 0755); err != nil {
		return nil, fmt.Errorf("기본 출력 디렉토리 생성 실패: %w", err)
	}

	if len(imageURLs) == 0 {
		// Render with default background
		outDir := filepath.Join(outputBaseDir, "variation_default")
		if err := renderer.RenderCards(ctx, cards, outDir, ""); err != nil {
			return nil, fmt.Errorf("기본 배경 렌더링 실패: %w", err)
		}
		outputDirs = append(outputDirs, outDir)
	} else {
		for i, imgURL := range imageURLs {
			outDir := filepath.Join(outputBaseDir, fmt.Sprintf("variation_%d", i+1))
			
			// Download image
			bgPath := filepath.Join(outputBaseDir, fmt.Sprintf("bg_%d.jpg", i+1))
			if err := downloadImage(ctx, imgURL, bgPath); err != nil {
				log.Printf("배경 이미지 다운로드 실패 (%s): %v. 기본 배경 사용", imgURL, err)
				bgPath = "" // Use default
			}
			
			if err := renderer.RenderCards(ctx, cards, outDir, bgPath); err != nil {
				log.Printf("변형 %d 렌더링 실패: %v", i+1, err)
				continue
			}
			outputDirs = append(outputDirs, outDir)
		}
	}

	return &PipelineResult{
		Topic:       selectedTopic,
		Cards:       cards,
		OutputDirs:  outputDirs,
		BgImageURLs: imageURLs,
	}, nil
}

func downloadImage(ctx context.Context, url, dest string) error {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return err
	}
	
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("bad status: %s", resp.Status)
	}

	out, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, resp.Body)
	return err
}
