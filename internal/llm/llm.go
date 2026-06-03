package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// CardContent represents the structured slide content required by the Figma template.
type CardContent struct {
	Title string `json:"title"`
	Body  string `json:"body"`
}

// Message represents a single message in the LLM chat history.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ChatCompletionRequest is the payload structure for OpenRouter's completions endpoint.
type ChatCompletionRequest struct {
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	Temperature float64   `json:"temperature"`
}

// Choice represents a completion choice in the LLM response.
type Choice struct {
	Message Message `json:"message"`
}

// ChatCompletionResponse is the response structure returned by OpenRouter.
type ChatCompletionResponse struct {
	Choices []Choice `json:"choices"`
}

// SanitizeJSON extracts the raw JSON array string from a potentially decorated LLM response.
// It searches for the outermost brackets '[' and ']' to ensure clean JSON unmarshaling.
func SanitizeJSON(raw string) string {
	start := strings.Index(raw, "[")
	end := strings.LastIndex(raw, "]")
	if start != -1 && end != -1 && start < end {
		return raw[start : end+1]
	}
	return raw
}

// SelectTopic analyzes trending news context and selects the single best topic for card news.
func SelectTopic(ctx context.Context, apiKey, trendingContext string) (string, error) {
	model := "google/gemma-4-31b-it:free"

	systemPrompt := `당신은 트렌디한 뉴스 편집장입니다. 제공된 최신 뉴스 검색 결과(컨텍스트)를 분석하여, 대중에게 가장 유용하고 흥미로운 단 하나의 카드뉴스 주제를 선정해야 합니다.

반드시 다음 규칙을 준수해야 합니다:
1. 피그마 검색이나 Brave Search에 재입력하기 적합한 '핵심 검색어 키워드' 또는 '구체적인 주제 명사구' 형태로 작성하십시오.
2. 마크다운 코드 블록, 따옴표, 번호 매기기, 특수 문자 및 설명(예: "주제는 ~ 입니다" 등)을 절대로 포함하지 말고, 단 한 줄의 핵심 문구만 출력하십시오.

출력 예시:
한국은행 기준금리 동결 배경
누리호 4차 발사 성공 및 향후 계획
글로벌 AI 반도체 수출 실적 개선 동향`

	userPrompt := fmt.Sprintf("다음 최신 뉴스 목록을 보고, 카드뉴스로 만들기에 가장 적합하고 흥미진진한 하나의 핵심 주제 키워드를 한 줄로 뽑아주세요.\n\n최신 뉴스 목록:\n%s", trendingContext)

	messages := []Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userPrompt},
	}

	var topic string
	var lastErr error
	baseDelay := 1 * time.Second

	for attempt := 1; attempt <= 3; attempt++ {
		topic, lastErr = callOpenRouterForTopic(ctx, apiKey, model, messages)
		if lastErr == nil {
			return topic, nil
		}

		if attempt < 3 {
			delay := baseDelay * time.Duration(1<<(attempt-1))
			select {
			case <-ctx.Done():
				return "", fmt.Errorf("주제 선정 재시도 대기 중 컨텍스트가 취소되었습니다: %w", ctx.Err())
			case <-time.After(delay):
			}
		}
	}

	return "", fmt.Errorf("3회 시도 후 카드뉴스 주제 선정에 실패했습니다: %w", lastErr)
}

func callOpenRouterForTopic(ctx context.Context, apiKey, model string, messages []Message) (string, error) {
	reqBody := ChatCompletionRequest{
		Model:       model,
		Messages:    messages,
		Temperature: 0.3,
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("OpenRouter 요청 직렬화 실패: %w", err)
	}

	apiCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(apiCtx, "POST", "https://openrouter.ai/api/v1/chat/completions", bytes.NewBuffer(jsonData))
	if err != nil {
		return "", fmt.Errorf("OpenRouter HTTP 요청 생성 실패: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("HTTP-Referer", "https://github.com/sleepysoong/serendipity")
	req.Header.Set("X-Title", "Serendipity Cardnews Autopipeline")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("OpenRouter API 호출 실패: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("OpenRouter가 상태 코드 %d를 반환했습니다. 응답: %s", resp.StatusCode, string(bodyBytes))
	}

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("OpenRouter 응답 읽기 실패: %w", err)
	}

	var chatResponse ChatCompletionResponse
	if err := json.Unmarshal(bodyBytes, &chatResponse); err != nil {
		return "", fmt.Errorf("OpenRouter 응답 JSON 파싱 실패: %w", err)
	}

	if len(chatResponse.Choices) == 0 {
		return "", fmt.Errorf("OpenRouter 응답에 완성 결과(Choices)가 없습니다")
	}

	rawContent := chatResponse.Choices[0].Message.Content
	topic := strings.TrimSpace(rawContent)
	topic = strings.Trim(topic, "`'\" \n\r\t")
	if topic == "" {
		return "", fmt.Errorf("추출된 주제명이 비어있습니다")
	}

	return topic, nil
}

// GenerateCardNews orchestrates the OpenRouter request and handles response parsing with retries and exponential backoff.
func GenerateCardNews(ctx context.Context, apiKey, groundingContext string) ([]CardContent, error) {
	model := "google/gemma-4-31b-it:free"

	systemPrompt := `당신은 전문 콘텐츠 크리에이터입니다. 제공된 검색 컨텍스트에서 주요 뉴스 포인트를 추출하여 순차적인 카드뉴스 슬라이드로 포맷팅하는 것이 당신의 임무입니다.

반드시 다음 규칙을 준수해야 합니다:
1. 출력은 반드시 순수 JSON 배열 형식이어야 합니다. 마크다운 코드 블록 표기(예: ` + "`" + "`" + "`" + `json)나 JSON 외부의 어떠한 설명도 포함하지 마십시오.
2. 배열의 각 객체는 오직 "title"과 "body" 두 개의 키만 가져야 합니다.
3. 엄격한 글자 수 제한:
   - "title": 반드시 한글/영어 공통 15자 이내여야 합니다. 강렬하고 핵심적이게 만드십시오!
   - "body": 반드시 한글/영어 공통 50자 이내여야 합니다. 간결하게 요약하십시오!
4. 각 슬라이드는 논리적으로 자연스럽게 이어져야 하며 이해하기 쉬워야 합니다.

예상되는 출력 형식의 Few-Shot 예시:

예시 1:
[
  {
    "title": "금리 동결 결정",
    "body": "한국은행이 기준금리를 연 3.5%로 유지하며 향후 추이를 지켜보기로 했습니다."
  },
  {
    "title": "물가 상승 지속",
    "body": "소비자 물가 상승률이 3%대를 이어가 고금리 장기화 가능성이 대두되었습니다."
  }
]

예시 2:
[
  {
    "title": "AI 반도체 급성장",
    "body": "글로벌 인공지능 수요의 폭증으로 인해 차세대 메모리 칩 판매가 급증했습니다."
  },
  {
    "title": "패키징 기술 경쟁",
    "body": "시장 주도권 선점을 위한 차세대 고대역폭 메모리 공정 경쟁이 심화되고 있습니다."
  }
]

예시 3:
[
  {
    "title": "전기차 판매 둔화",
    "body": "보조금 축소와 충전기 부족 여파로 올해 글로벌 친환경차 성장이 둔화되었습니다."
  },
  {
    "title": "하이브리드 대안",
    "body": "제조사들은 공백을 극복하기 위해 신형 하이브리드 제품 출시를 확대 중입니다."
  }
]`

	userPrompt := fmt.Sprintf("다음 뉴스 검색 결과를 기반으로 순차적인 3개의 카드뉴스 슬라이드를 생성해 주세요. 글자 수 제한(제목 15자 이내, 본문 50자 이내)을 반드시 기억하세요.\n\n컨텍스트:\n%s", groundingContext)

	messages := []Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userPrompt},
	}

	var cards []CardContent
	var lastErr error
	baseDelay := 1 * time.Second

	// Run up to 3 attempts with exponential backoff if error occurs in API call or JSON parsing
	for attempt := 1; attempt <= 3; attempt++ {
		cards, lastErr = callOpenRouterAndParse(ctx, apiKey, model, messages)
		if lastErr == nil {
			return cards, nil
		}

		if attempt < 3 {
			delay := baseDelay * time.Duration(1<<(attempt-1))
			select {
			case <-ctx.Done():
				return nil, fmt.Errorf("LLM 생성 재시도 대기 중 컨텍스트가 취소되었습니다: %w", ctx.Err())
			case <-time.After(delay):
			}
		}
	}

	return nil, fmt.Errorf("3회 시도 후 카드뉴스 생성 및 파싱에 실패했습니다: %w", lastErr)
}

func callOpenRouterAndParse(ctx context.Context, apiKey, model string, messages []Message) ([]CardContent, error) {
	reqBody := ChatCompletionRequest{
		Model:       model,
		Messages:    messages,
		Temperature: 0.2,
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("OpenRouter 요청 직렬화 실패: %w", err)
	}

	// Set API call timeout to 15 seconds
	apiCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(apiCtx, "POST", "https://openrouter.ai/api/v1/chat/completions", bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("OpenRouter HTTP 요청 생성 실패: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("HTTP-Referer", "https://github.com/sleepysoong/serendipity")
	req.Header.Set("X-Title", "Serendipity Cardnews Autopipeline")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("OpenRouter API 호출 실패: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("OpenRouter가 상태 코드 %d를 반환했습니다. 응답: %s", resp.StatusCode, string(bodyBytes))
	}

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("OpenRouter 응답 읽기 실패: %w", err)
	}

	var chatResponse ChatCompletionResponse
	if err := json.Unmarshal(bodyBytes, &chatResponse); err != nil {
		return nil, fmt.Errorf("OpenRouter 응답 JSON 파싱 실패: %w", err)
	}

	if len(chatResponse.Choices) == 0 {
		return nil, fmt.Errorf("OpenRouter 응답에 완성 결과(Choices)가 없습니다")
	}

	rawContent := chatResponse.Choices[0].Message.Content
	sanitized := SanitizeJSON(rawContent)

	var cards []CardContent
	if err := json.Unmarshal([]byte(sanitized), &cards); err != nil {
		return nil, fmt.Errorf("정제된 JSON을 CardContent 슬라이스로 파싱하는 데 실패했습니다 (Raw: %q, Sanitized: %q): %w", rawContent, sanitized, err)
	}

	// Validate character counts as an extra programmatic guard rail
	for idx, card := range cards {
		if len([]rune(card.Title)) > 15 {
			return nil, fmt.Errorf("유효성 검사 실패: %d번째 카드의 제목 길이(%d자)가 15자를 초과합니다", idx+1, len([]rune(card.Title)))
		}
		if len([]rune(card.Body)) > 50 {
			return nil, fmt.Errorf("유효성 검사 실패: %d번째 카드의 본문 길이(%d자)가 50자를 초과합니다", idx+1, len([]rune(card.Body)))
		}
	}

	return cards, nil
}
