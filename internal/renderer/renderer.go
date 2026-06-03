package renderer

import (
	"context"
	"fmt"
	"image"
	"image/color"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/fogleman/gg"
	"serendipity/internal/llm"
)

const (
	// 캔버스 크기: 3:4 비율 (HTML의 aspect-ratio: 3/4 동일)
	cardW = 1080
	cardH = 1440

	// HTML 프리뷰(450px 너비) → 1080px 출력 스케일 팩터
	scaleFactor = 2.4
)

var fontSources = map[string]string{
	"cafe24": "https://cdn.jsdelivr.net/gh/fonts-archive/Cafe24MeongiBlack/Cafe24Meongi-B-v1.0.ttf",
	"poster": "https://cdn.jsdelivr.net/gh/fonts-archive/HakgyoansimPosterB/Hakgyoansim_PosterB.ttf",
}

// ensureFont는 폰트 파일이 로컬에 없으면 CDN에서 다운로드하여 캐싱합니다.
func ensureFont(name, url, cacheDir string) (string, error) {
	fontPath := filepath.Join(cacheDir, name+".ttf")
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
// HTML 카드뉴스 메이커와 동일한 디자인으로 1080×1440 PNG 이미지를 생성합니다.
//
// HTML 디자인 사양 정밀 매핑:
//   - 비율: 3:4 (1080×1440)
//   - 배경: 사용자 지정 이미지 (background-size: cover) 또는 기본 어두운 그라디언트
//   - 하단 딤(bottom-shadow): 높이 55%, gradient(to top, rgba(0,0,0,0.85)→rgba(0,0,0,0.4) 40%→transparent)
//   - 워터마크: @sleepysoong, Cafe24 Meongi B, text-xl, letter-spacing -0.12em, text-shadow
//   - 타이틀: Hakgyoansim Poster B, 48px(→스케일), letter-spacing -0.12em, line-height 1.2, text-shadow
func RenderCards(ctx context.Context, cards []llm.CardContent, outputDir, bgImagePath string) error {
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

	// 배경 이미지 로드 (선택사항)
	var bgImage image.Image
	if bgImagePath != "" {
		bgImage, err = gg.LoadImage(bgImagePath)
		if err != nil {
			return fmt.Errorf("배경 이미지 '%s' 로드 실패: %w", bgImagePath, err)
		}
		log.Printf("배경 이미지 로드 완료: %s", bgImagePath)
	}

	for i, card := range cards {
		select {
		case <-ctx.Done():
			return fmt.Errorf("카드 렌더링 중 컨텍스트 취소: %w", ctx.Err())
		default:
		}

		cardIndex := i + 1
		log.Printf("카드 %d/%d 렌더링 중: [%s]", cardIndex, len(cards), card.Title)

		if err := renderSingleCard(card, cafe24Path, posterPath, bgImage, outputDir, cardIndex); err != nil {
			return fmt.Errorf("카드 %d 렌더링 실패: %w", cardIndex, err)
		}
	}

	log.Printf("모든 %d개 카드 이미지가 %s/ 에 저장되었습니다", len(cards), outputDir)
	return nil
}

// renderSingleCard는 HTML 카드뉴스 메이커와 동일한 레이아웃으로 한 장의 카드를 렌더링합니다.
func renderSingleCard(card llm.CardContent, cafe24Path, posterPath string, bgImage image.Image, outputDir string, index int) error {
	dc := gg.NewContext(cardW, cardH)

	// ── 1. 배경 (CSS: background-size: cover; background-position: center) ──
	if bgImage != nil {
		drawBackgroundCover(dc, bgImage)
	} else {
		drawDefaultBackground(dc)
	}

	// ── 2. 하단 딤 그라디언트 (CSS: .bottom-shadow, h-[55%]) ──
	drawBottomDim(dc)

	// ── 3. 워터마크 "@sleepysoong" (CSS: .font-cafe24, top-6, left-6, text-xl) ──
	drawWatermark(dc, cafe24Path)

	// ── 4. 타이틀 (CSS: .font-hakgyoansim, bottom-10, left-6, right-6, leading-[1.2]) ──
	drawTitleBlock(dc, card, posterPath)

	// ── 5. PNG 저장 ──
	outputPath := filepath.Join(outputDir, fmt.Sprintf("card_page_%d.png", index))
	if err := dc.SavePNG(outputPath); err != nil {
		return fmt.Errorf("PNG 저장 실패: %w", err)
	}

	log.Printf("카드 %d 렌더링 완료: %s", index, outputPath)
	return nil
}

// ============================================================================
// 배경 관련 함수
// ============================================================================

// drawBackgroundCover는 이미지를 CSS background-size: cover 모드로 캔버스에 그립니다.
// 이미지가 캔버스를 완전히 덮도록 스케일링하고 중앙에 배치합니다.
func drawBackgroundCover(dc *gg.Context, img image.Image) {
	imgW := float64(img.Bounds().Dx())
	imgH := float64(img.Bounds().Dy())

	// cover 모드: 큰 스케일 팩터 사용 (캔버스를 완전히 덮음)
	scaleX := float64(cardW) / imgW
	scaleY := float64(cardH) / imgH
	s := math.Max(scaleX, scaleY)

	// 스케일 후 중앙 정렬 오프셋 계산
	newW := imgW * s
	newH := imgH * s
	offsetX := (float64(cardW) - newW) / 2
	offsetY := (float64(cardH) - newH) / 2

	dc.Push()
	dc.Translate(offsetX, offsetY)
	dc.Scale(s, s)
	dc.DrawImage(img, 0, 0)
	dc.Pop()
}

// drawDefaultBackground는 배경 이미지가 없을 때 어두운 시네마틱 그라디언트를 그립니다.
func drawDefaultBackground(dc *gg.Context) {
	grad := gg.NewLinearGradient(0, 0, float64(cardW), float64(cardH))
	grad.AddColorStop(0, color.RGBA{R: 20, G: 20, B: 35, A: 255})
	grad.AddColorStop(0.5, color.RGBA{R: 15, G: 12, B: 30, A: 255})
	grad.AddColorStop(1, color.RGBA{R: 10, G: 10, B: 20, A: 255})
	dc.SetFillStyle(grad)
	dc.DrawRectangle(0, 0, float64(cardW), float64(cardH))
	dc.Fill()
}

// ============================================================================
// 하단 딤 그라디언트
// ============================================================================

// drawBottomDim은 하단 55% 영역에 딤 그라디언트를 그립니다.
// CSS: .bottom-shadow { background: linear-gradient(to top, rgba(0,0,0,0.85) 0%, rgba(0,0,0,0.4) 40%, transparent 100%); }
// CSS: .h-[55%] → 카드 높이의 55%
func drawBottomDim(dc *gg.Context) {
	gradH := float64(cardH) * 0.55
	gradTop := float64(cardH) - gradH

	// CSS gradient 방향: to top (bottom→top)
	// gg.NewLinearGradient(x0, y0, x1, y1): 색상 정지점 0.0은 (x0,y0), 1.0은 (x1,y1)
	grad := gg.NewLinearGradient(0, float64(cardH), 0, gradTop)
	grad.AddColorStop(0, color.RGBA{A: 255})  // 0%: rgba(0,0,0,1.0) (강하게 수정)
	grad.AddColorStop(0.4, color.RGBA{A: 178}) // 40%: rgba(0,0,0,0.7) (강하게 수정)
	grad.AddColorStop(1, color.RGBA{A: 0})     // 100%: transparent

	dc.SetFillStyle(grad)
	dc.DrawRectangle(0, gradTop, float64(cardW), gradH)
	dc.Fill()
}

// ============================================================================
// 워터마크
// ============================================================================

// drawWatermark는 좌측 상단에 "@sleepysoong" 워터마크를 그립니다.
//
// HTML 사양:
//   - CSS class: .font-cafe24 (Cafe24 Meongi B, letter-spacing: -0.12em)
//   - 위치: absolute top-6 left-6 (24px × 2.4 = 58px)
//   - 크기: text-xl (Tailwind 20px → 20 × 2.4 = 48px)
//   - 색상: text-white, text-shadow: 1px 1px 3px rgba(0,0,0,0.5)
func drawWatermark(dc *gg.Context, cafe24Path string) {
	fontSize := 48.0                // text-xl(20px) × 2.4
	spacing := -0.10 * fontSize     // 자간 -10%
	x := 58.0                       // left-6(24px) × 2.4
	topY := 58.0                    // top-6(24px) × 2.4
	baselineY := topY + fontSize*0.8 // 대략적 ascent 위치

	if err := dc.LoadFontFace(cafe24Path, fontSize); err != nil {
		log.Printf("워터마크 폰트 로드 실패 (무시): %v", err)
		return
	}

	// 텍스트 그림자 (CSS: text-shadow: 1px 1px 3px rgba(0,0,0,0.5) → 스케일 적용)
	dc.SetColor(color.RGBA{A: 128})
	drawTextWithSpacing(dc, "@brrreeeeeze", x+scaleFactor, baselineY+scaleFactor, spacing)

	// 실제 텍스트 (흰색)
	dc.SetColor(color.White)
	drawTextWithSpacing(dc, "@brrreeeeeze", x, baselineY, spacing)
}

// ============================================================================
// 타이틀 + 본문 텍스트
// ============================================================================

// drawTitleBlock은 카드 하단에 타이틀과 본문 텍스트를 그립니다.
//
// HTML 사양:
//   - CSS class: .font-hakgyoansim (Hakgyoansim Poster B, letter-spacing: -0.12em, white-space: pre-wrap, word-break: keep-all)
//   - 위치: absolute bottom-10 left-6 right-6
//   - 크기: font-size 48px (→ 48 × 2.4 = 115px)
//   - 줄간격: leading-[1.2] (line-height: 1.2)
//   - 색상: text-white, text-shadow: 2px 2px 8px rgba(0,0,0,0.5)
//
// 파이프라인 확장: 타이틀(큰 글씨) + 본문(작은 글씨) 2단 구성
func drawTitleBlock(dc *gg.Context, card llm.CardContent, posterPath string) {
	// 마진 (HTML 기준 스케일 적용)
	leftX := 58.0         // left-6(24px) × 2.4
	bottomMargin := 96.0  // bottom-10(40px) × 2.4
	maxWidth := float64(cardW) - 58 - 58 // left-6, right-6

	// ── 타이틀 설정 ──
	titleFontSize := 96.0 // 카드뉴스 파이프라인에 적합하도록 조절
	titleSpacing := -0.12 * titleFontSize
	titleLineH := titleFontSize * 1.2

	if err := dc.LoadFontFace(posterPath, titleFontSize); err != nil {
		log.Printf("타이틀 폰트 로드 실패 (무시): %v", err)
		return
	}
	titleLines := wrapText(dc, card.Title, maxWidth, titleSpacing)
	titleBlockH := float64(len(titleLines)) * titleLineH

	// ── 본문 설정 ──
	bodyFontSize := 48.0 // 본문은 타이틀의 절반 크기
	bodySpacing := -0.12 * bodyFontSize
	bodyLineH := bodyFontSize * 1.4

	if err := dc.LoadFontFace(posterPath, bodyFontSize); err != nil {
		log.Printf("본문 폰트 로드 실패 (무시): %v", err)
		return
	}
	bodyLines := wrapText(dc, card.Body, maxWidth, bodySpacing)
	bodyBlockH := float64(len(bodyLines)) * bodyLineH

	// ── 레이아웃 계산 (하단에서 위로) ──
	gap := 24.0 // 타이틀과 본문 사이 간격

	// 본문이 맨 아래, 타이틀이 그 위
	bodyBlockBottom := float64(cardH) - bottomMargin
	bodyBlockTop := bodyBlockBottom - bodyBlockH
	titleBlockBottom := bodyBlockTop - gap
	titleBlockTop := titleBlockBottom - titleBlockH

	// ── 타이틀 렌더링 ──
	if err := dc.LoadFontFace(posterPath, titleFontSize); err != nil {
		return
	}
	shadowOff := 4.8 // CSS text-shadow 2px × 2.4
	for i, line := range titleLines {
		baseline := titleBlockTop + float64(i)*titleLineH + titleFontSize*0.8

		// 텍스트 그림자 (CSS: text-shadow: 2px 2px 8px rgba(0,0,0,0.5))
		dc.SetColor(color.RGBA{A: 128})
		drawTextWithSpacing(dc, line, leftX+shadowOff, baseline+shadowOff, titleSpacing)

		// 실제 텍스트 (흰색)
		dc.SetColor(color.White)
		drawTextWithSpacing(dc, line, leftX, baseline, titleSpacing)
	}

	// ── 본문 렌더링 ──
	if err := dc.LoadFontFace(posterPath, bodyFontSize); err != nil {
		return
	}
	bodyShadowOff := 2.4
	for i, line := range bodyLines {
		baseline := bodyBlockTop + float64(i)*bodyLineH + bodyFontSize*0.8

		// 텍스트 그림자
		dc.SetColor(color.RGBA{A: 100})
		drawTextWithSpacing(dc, line, leftX+bodyShadowOff, baseline+bodyShadowOff, bodySpacing)

		// 실제 텍스트 (약간 투명한 흰색: rgba(255,255,255,0.85))
		dc.SetColor(color.RGBA{R: 255, G: 255, B: 255, A: 217})
		drawTextWithSpacing(dc, line, leftX, baseline, bodySpacing)
	}
}

// ============================================================================
// 자간(letter-spacing) 텍스트 드로잉 유틸리티
// ============================================================================

// drawTextWithSpacing은 자간(letter-spacing: -0.12em)을 적용하여 텍스트를 한 줄 그립니다.
// x, y는 텍스트의 좌측 baseline 위치입니다.
//
// CSS에서 letter-spacing은 각 글자 사이에 추가되는 여백입니다.
// -0.12em은 글자 간격을 fontSize의 12%만큼 좁힙니다.
func drawTextWithSpacing(dc *gg.Context, text string, x, y, spacing float64) {
	runes := []rune(text)
	currentX := x
	for i, r := range runes {
		dc.DrawString(string(r), currentX, y)
		w, _ := dc.MeasureString(string(r))
		currentX += w
		if i < len(runes)-1 {
			currentX += spacing // CSS letter-spacing에 해당
		}
	}
}

// measureTextWithSpacing은 자간이 적용된 텍스트의 전체 너비를 측정합니다.
func measureTextWithSpacing(dc *gg.Context, text string, spacing float64) float64 {
	runes := []rune(text)
	total := 0.0
	for i, r := range runes {
		w, _ := dc.MeasureString(string(r))
		total += w
		if i < len(runes)-1 {
			total += spacing
		}
	}
	return total
}

// wrapText는 자간이 적용된 텍스트를 최대 너비에 맞게 줄바꿈합니다.
// CSS white-space: pre-wrap (명시적 개행 유지) + word-break: keep-all (글자 단위 줄바꿈) 동작을 재현합니다.
func wrapText(dc *gg.Context, text string, maxWidth, spacing float64) []string {
	var result []string

	// pre-wrap: 명시적 \n 줄바꿈을 먼저 처리
	for _, paragraph := range strings.Split(text, "\n") {
		if paragraph == "" {
			result = append(result, "")
			continue
		}

		runes := []rune(paragraph)
		lineStart := 0
		lineWidth := 0.0

		for i, r := range runes {
			charW, _ := dc.MeasureString(string(r))
			addedWidth := charW
			if i > lineStart {
				addedWidth += spacing // 첫 글자가 아니면 자간 추가
			}

			// 현재 줄에 글자가 넘치면 줄바꿈
			if lineWidth+addedWidth > maxWidth && i > lineStart {
				result = append(result, string(runes[lineStart:i]))
				lineStart = i
				lineWidth = charW // 새 줄은 현재 글자부터 시작
			} else {
				lineWidth += addedWidth
			}
		}
		// 남은 텍스트 추가
		if lineStart < len(runes) {
			result = append(result, string(runes[lineStart:]))
		}
	}

	return result
}
