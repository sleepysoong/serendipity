package renderer

import (
	"context"
	"fmt"
	"html/template"
	"log"
	"os"
	"path/filepath"
	"strings"

	"serendipity/internal/llm"

	"github.com/chromedp/chromedp"
)

// cardTemplateData는 HTML 템플릿에 주입할 단일 카드 데이터입니다.
type cardTemplateData struct {
	Index int
	Title string
	Body  string
}

// htmlTemplate는 카드뉴스 한 장을 렌더링하기 위한 HTML 템플릿입니다.
// 사용자가 제공한 카드뉴스 메이커 HTML과 동일한 디자인을 적용합니다.
const htmlTemplate = `<!DOCTYPE html>
<html lang="ko">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <link rel="stylesheet" href="https://cdn.jsdelivr.net/gh/fonts-archive/Cafe24MeongiBlack/Cafe24Meongi-B-v1.0.css" type="text/css" />
    <link rel="stylesheet" href="https://cdn.jsdelivr.net/gh/fonts-archive/HakgyoansimPosterB/Hakgyoansim_PosterB.css" type="text/css" />
    <style>
        * { margin: 0; padding: 0; box-sizing: border-box; }

        .font-cafe24 {
            font-family: "Cafe24 Meongi B", -apple-system, BlinkMacSystemFont, sans-serif;
        }
        .font-poster {
            font-family: "Hakgyoansim Poster B", -apple-system, BlinkMacSystemFont, sans-serif;
        }
        .tracking-tight { letter-spacing: -0.02em; }

        .card-container {
            width: 1080px;
            height: 1080px;
            position: relative;
            overflow: hidden;
        }

        /* 그라디언트 배경 */
        .bg-gradient {
            background: linear-gradient(135deg, #667eea 0%, #764ba2 100%);
        }

        /* 카드 내부 레이아웃 */
        .card-inner {
            width: 100%;
            height: 100%;
            display: flex;
            flex-direction: column;
            align-items: center;
            justify-content: center;
            padding: 80px;
            text-align: center;
        }

        /* 페이지 번호 뱃지 */
        .page-badge {
            position: absolute;
            top: 40px;
            right: 40px;
            background: rgba(255,255,255,0.2);
            backdrop-filter: blur(10px);
            border-radius: 50%;
            width: 60px;
            height: 60px;
            display: flex;
            align-items: center;
            justify-content: center;
            color: white;
            font-size: 24px;
            font-weight: bold;
        }

        /* 장식용 라인 */
        .decorative-line {
            width: 80px;
            height: 4px;
            background: rgba(255,255,255,0.6);
            border-radius: 2px;
            margin: 30px 0;
        }

        .title-text {
            color: white;
            font-size: 64px;
            line-height: 1.3;
            margin-bottom: 10px;
            word-break: keep-all;
        }

        .body-text {
            color: rgba(255,255,255,0.9);
            font-size: 32px;
            line-height: 1.6;
            word-break: keep-all;
        }

        /* 하단 브랜딩 */
        .branding {
            position: absolute;
            bottom: 40px;
            left: 0;
            right: 0;
            text-align: center;
            color: rgba(255,255,255,0.4);
            font-size: 16px;
        }
    </style>
</head>
<body>
    <div id="card" class="card-container bg-gradient">
        <div class="card-inner">
            <div class="page-badge font-poster">{{.Index}}</div>
            <h1 class="title-text font-cafe24 tracking-tight">{{.Title}}</h1>
            <div class="decorative-line"></div>
            <p class="body-text font-poster">{{.Body}}</p>
        </div>
        <div class="branding font-poster">Serendipity 카드뉴스</div>
    </div>
</body>
</html>`

// RenderCards는 LLM이 생성한 카드 콘텐츠를 HTML로 렌더링한 뒤
// chromedp(headless Chrome)를 사용하여 각 카드를 PNG 이미지로 캡처합니다.
func RenderCards(ctx context.Context, cards []llm.CardContent, outputDir string) error {
	// 출력 디렉토리 생성
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("출력 디렉토리 %s 생성 실패: %w", outputDir, err)
	}

	// HTML 템플릿 파싱
	tmpl, err := template.New("card").Parse(htmlTemplate)
	if err != nil {
		return fmt.Errorf("HTML 템플릿 파싱 실패: %w", err)
	}

	// chromedp 컨텍스트 생성 (headless Chrome)
	allocCtx, allocCancel := chromedp.NewExecAllocator(ctx,
		append(chromedp.DefaultExecAllocatorOptions[:],
			chromedp.WindowSize(1080, 1080),
			chromedp.Flag("disable-gpu", true),
			chromedp.Flag("no-sandbox", true),
		)...,
	)
	defer allocCancel()

	browserCtx, browserCancel := chromedp.NewContext(allocCtx)
	defer browserCancel()

	for i, card := range cards {
		cardIndex := i + 1
		log.Printf("카드 %d/%d 렌더링 중: [%s]", cardIndex, len(cards), card.Title)

		// HTML 문자열 생성
		data := cardTemplateData{
			Index: cardIndex,
			Title: card.Title,
			Body:  card.Body,
		}

		var htmlBuf strings.Builder
		if err := tmpl.Execute(&htmlBuf, data); err != nil {
			return fmt.Errorf("카드 %d HTML 생성 실패: %w", cardIndex, err)
		}

		// 임시 HTML 파일 저장 (chromedp가 파일 URL로 로드)
		tmpHTMLPath := filepath.Join(outputDir, fmt.Sprintf("_temp_card_%d.html", cardIndex))
		if err := os.WriteFile(tmpHTMLPath, []byte(htmlBuf.String()), 0644); err != nil {
			return fmt.Errorf("카드 %d 임시 HTML 파일 작성 실패: %w", cardIndex, err)
		}
		defer os.Remove(tmpHTMLPath) // 렌더링 완료 후 임시 파일 삭제

		// chromedp로 스크린샷 캡처
		var buf []byte
		fileURL := "file://" + tmpHTMLPath

		if err := chromedp.Run(browserCtx,
			chromedp.Navigate(fileURL),
			chromedp.WaitReady("#card"),
			chromedp.Screenshot("#card", &buf, chromedp.NodeVisible),
		); err != nil {
			return fmt.Errorf("카드 %d 스크린샷 캡처 실패: %w", cardIndex, err)
		}

		// PNG 파일 저장
		outputPath := filepath.Join(outputDir, fmt.Sprintf("card_page_%d.png", cardIndex))
		if err := os.WriteFile(outputPath, buf, 0644); err != nil {
			return fmt.Errorf("카드 %d 이미지 저장 실패: %w", cardIndex, err)
		}

		log.Printf("카드 %d 렌더링 완료: %s", cardIndex, outputPath)
	}

	log.Printf("모든 %d개 카드 이미지가 %s/ 에 저장되었습니다", len(cards), outputDir)
	return nil
}
