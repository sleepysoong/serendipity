package renderer

import (
	"context"
	"fmt"
	"image/color"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"

	"github.com/fogleman/gg"
	"serendipity/internal/llm"
)

const (
	cardWidth  = 1080
	cardHeight = 1080
)

// 폰트 다운로드 URL (fonts-archive CDN)
var fontSources = map[string]string{
	"cafe24": "https://cdn.jsdelivr.net/gh/fonts-archive/Cafe24MeongiBlack/Cafe24MeongiBlack.ttf",
	"poster": "https://cdn.jsdelivr.net/gh/fonts-archive/HakgyoansimPosterB/HakgyoansimPosterB.ttf",
}

// ensureFont는 폰트 파일이 로컬에 없으면 CDN에서 다운로드하여 캐싱합니다.
func ensureFont(name, url, cacheDir string) (string, error) {
	fontPath := filepath.Join(cacheDir, name+".ttf")

	// 이미 캐시된 폰트가 있으면 바로 반환
	if info, err := os.Stat(fontPath); err == nil && info.Size() > 0 {
		return fontPath, nil
	}

	log.Printf("폰트 '%s' 다운로드 중: %s", name, url)
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return "", fmt.Errorf("폰트 캐시 디렉토리 생성 실패: %w", err)
	}

	resp, err := http.Get(url)
	if err != nil {
		return "", fmt.Errorf("폰트 다운로드 요청 실패: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("폰트 다운로드 실패 (HTTP %d)", resp.StatusCode)
	}

	f, err := os.Create(fontPath)
	if err != nil {
		return "", fmt.Errorf("폰트 파일 생성 실패: %w", err)
	}
	defer f.Close()

	if _, err := io.Copy(f, resp.Body); err != nil {
		return "", fmt.Errorf("폰트 파일 저장 실패: %w", err)
	}

	log.Printf("폰트 '%s' 캐싱 완료: %s", name, fontPath)
	return fontPath, nil
}

// RenderCards는 LLM이 생성한 카드 콘텐츠를 Go 이미지 라이브러리(gg)로
// 직접 그려서 1080×1080 PNG 카드뉴스 이미지를 생성합니다.
func RenderCards(ctx context.Context, cards []llm.CardContent, outputDir string) error {
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("출력 디렉토리 %s 생성 실패: %w", outputDir, err)
	}

	// 폰트 다운로드 및 캐싱
	fontDir := filepath.Join(outputDir, ".fonts")
	cafe24Path, err := ensureFont("cafe24", fontSources["cafe24"], fontDir)
	if err != nil {
		return fmt.Errorf("Cafe24 폰트 준비 실패: %w", err)
	}
	posterPath, err := ensureFont("poster", fontSources["poster"], fontDir)
	if err != nil {
		return fmt.Errorf("Poster 폰트 준비 실패: %w", err)
	}

	for i, card := range cards {
		select {
		case <-ctx.Done():
			return fmt.Errorf("카드 렌더링 중 컨텍스트 취소: %w", ctx.Err())
		default:
		}

		cardIndex := i + 1
		log.Printf("카드 %d/%d 렌더링 중: [%s]", cardIndex, len(cards), card.Title)

		if err := renderSingleCard(card, cardIndex, cafe24Path, posterPath, outputDir); err != nil {
			return fmt.Errorf("카드 %d 렌더링 실패: %w", cardIndex, err)
		}
	}

	log.Printf("모든 %d개 카드 이미지가 %s/ 에 저장되었습니다", len(cards), outputDir)
	return nil
}

// renderSingleCard는 단일 카드 한 장을 이미지로 렌더링합니다.
func renderSingleCard(card llm.CardContent, index int, cafe24Path, posterPath, outputDir string) error {
	dc := gg.NewContext(cardWidth, cardHeight)

	// ── 1. 그라디언트 배경 (135도: 좌상단 → 우하단, #667eea → #764ba2) ──
	grad := gg.NewLinearGradient(0, 0, cardWidth, cardHeight)
	grad.AddColorStop(0, color.RGBA{R: 0x66, G: 0x7E, B: 0xEA, A: 0xFF})
	grad.AddColorStop(1, color.RGBA{R: 0x76, G: 0x4B, B: 0xA2, A: 0xFF})
	dc.SetFillStyle(grad)
	dc.DrawRectangle(0, 0, cardWidth, cardHeight)
	dc.Fill()

	// ── 2. 페이지 번호 뱃지 (우상단 원형) ──
	badgeCX := float64(cardWidth) - 70
	badgeCY := 70.0
	badgeRadius := 30.0

	// 반투명 흰색 원 배경 (rgba 255,255,255,0.2)
	dc.SetColor(color.RGBA{R: 255, G: 255, B: 255, A: 51})
	dc.DrawCircle(badgeCX, badgeCY, badgeRadius)
	dc.Fill()

	// 뱃지 숫자
	if err := dc.LoadFontFace(posterPath, 24); err != nil {
		return fmt.Errorf("뱃지 폰트 로드 실패: %w", err)
	}
	dc.SetColor(color.White)
	dc.DrawStringAnchored(fmt.Sprintf("%d", index), badgeCX, badgeCY, 0.5, 0.5)

	// ── 3. 제목 텍스트 (중앙 상단) ──
	if err := dc.LoadFontFace(cafe24Path, 64); err != nil {
		return fmt.Errorf("제목 폰트 로드 실패: %w", err)
	}
	dc.SetColor(color.White)
	titleY := float64(cardHeight)/2 - 60
	dc.DrawStringWrapped(card.Title, float64(cardWidth)/2, titleY, 0.5, 0.5, float64(cardWidth)-160, 1.3, gg.AlignCenter)

	// ── 4. 장식용 구분선 ──
	lineY := float64(cardHeight) / 2
	dc.SetColor(color.RGBA{R: 255, G: 255, B: 255, A: 153}) // rgba(255,255,255,0.6)
	dc.SetLineWidth(4)
	dc.DrawLine(float64(cardWidth)/2-40, lineY, float64(cardWidth)/2+40, lineY)
	dc.Stroke()

	// ── 5. 본문 텍스트 (중앙 하단) ──
	if err := dc.LoadFontFace(posterPath, 32); err != nil {
		return fmt.Errorf("본문 폰트 로드 실패: %w", err)
	}
	dc.SetColor(color.RGBA{R: 255, G: 255, B: 255, A: 230}) // rgba(255,255,255,0.9)
	bodyY := float64(cardHeight)/2 + 70
	dc.DrawStringWrapped(card.Body, float64(cardWidth)/2, bodyY, 0.5, 0.5, float64(cardWidth)-160, 1.6, gg.AlignCenter)

	// ── 6. 하단 브랜딩 ──
	if err := dc.LoadFontFace(posterPath, 16); err != nil {
		return fmt.Errorf("브랜딩 폰트 로드 실패: %w", err)
	}
	dc.SetColor(color.RGBA{R: 255, G: 255, B: 255, A: 102}) // rgba(255,255,255,0.4)
	dc.DrawStringAnchored("Serendipity 카드뉴스", float64(cardWidth)/2, float64(cardHeight)-40, 0.5, 0.5)

	// ── 7. PNG 저장 ──
	outputPath := filepath.Join(outputDir, fmt.Sprintf("card_page_%d.png", index))
	if err := dc.SavePNG(outputPath); err != nil {
		return fmt.Errorf("PNG 저장 실패: %w", err)
	}

	log.Printf("카드 %d 렌더링 완료: %s", index, outputPath)
	return nil
}
