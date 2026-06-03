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

	"serendipity/prompts"
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
func SelectTopic(ctx context.Context, apiKey, model, trendingContext string, logf func(string)) (string, error) {
	systemPrompt := prompts.SelectTopicSystem

	userPrompt := fmt.Sprintf("다음 최신 뉴스 목록을 보고, 카드뉴스로 만들기에 가장 적합하고 흥미진진한 하나의 핵심 주제 키워드를 한 줄로 뽑아주세요.\n\n최신 뉴스 목록:\n%s", trendingContext)

	messages := []Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userPrompt},
	}

	var topic string
	var lastErr error
	baseDelay := 1 * time.Second

	for attempt := 1; attempt <= 3; attempt++ {
		if logf != nil {
			logf(fmt.Sprintf("● **`인공지능 응답을 요청합니다`**  |  ```\n%s\n```", userPrompt))
		}
		topic, lastErr = callOpenRouterForTopic(ctx, apiKey, model, messages, logf)
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

func callOpenRouterForTopic(ctx context.Context, apiKey, model string, messages []Message, logf func(string)) (string, error) {
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
	
	if logf != nil {
		logf(fmt.Sprintf("● **`인공지능 응답을 받았습니다`**  |  ```\n%s\n```", rawContent))
	}
	
	topic := strings.TrimSpace(rawContent)
	topic = strings.Trim(topic, "`'\" \n\r\t")
	if topic == "" {
		return "", fmt.Errorf("추출된 주제명이 비어있습니다")
	}

	return topic, nil
}

// GenerateCardNews orchestrates the OpenRouter request and handles response parsing with retries and exponential backoff.
func GenerateCardNews(ctx context.Context, apiKey, model, groundingContext string, logf func(string)) ([]CardContent, error) {
	systemPrompt := prompts.GenerateCardNewsSystem

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
		if logf != nil {
			logf(fmt.Sprintf("● **`인공지능 응답을 요청합니다`**  |  ```\n%s\n```", userPrompt))
		}
		cards, lastErr = callOpenRouterAndParse(ctx, apiKey, model, messages, logf)
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

func callOpenRouterAndParse(ctx context.Context, apiKey, model string, messages []Message, logf func(string)) ([]CardContent, error) {
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
	if logf != nil {
		logf(fmt.Sprintf("● **`인공지능 응답을 받았습니다`**  |  ```\n%s\n```", rawContent))
	}
	
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
