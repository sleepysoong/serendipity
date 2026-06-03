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

// GenerateCardNews orchestrates the OpenRouter request and handles response parsing with retries and exponential backoff.
func GenerateCardNews(ctx context.Context, apiKey, groundingContext string) ([]CardContent, error) {
	model := "google/gemma-4-31b-it:free"

	systemPrompt := `You are a professional content creator. Your task is to extract key news points from the provided search context and format them as sequential card news slides.

You MUST follow these rules:
1. Output MUST be a pure JSON array of objects. Do not include markdown code block wrappers (like ` + "`" + "`" + "`" + `json) or any explanation outside the JSON.
2. Each object in the array must contain only two keys: "title" and "body".
3. STRICT CHARACTER LIMITS:
   - "title": MUST NOT exceed 15 characters (Korean/English inclusive). Keep it punchy!
   - "body": MUST NOT exceed 50 characters (Korean/English inclusive). Make it concise!
4. The slides should flow logically and be easy to understand.

Few-Shot Examples of Expected Output format:

Example 1:
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

Example 2:
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

Example 3:
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

	userPrompt := fmt.Sprintf("Based on the following news search results, generate 3 sequential card news slides. Remember the character limits: Title <= 15 chars, Body <= 50 chars.\n\nContext:\n%s", groundingContext)

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
				return nil, fmt.Errorf("context cancelled during LLM generation retry backoff: %w", ctx.Err())
			case <-time.After(delay):
			}
		}
	}

	return nil, fmt.Errorf("failed to generate and parse card news after 3 attempts: %w", lastErr)
}

func callOpenRouterAndParse(ctx context.Context, apiKey, model string, messages []Message) ([]CardContent, error) {
	reqBody := ChatCompletionRequest{
		Model:       model,
		Messages:    messages,
		Temperature: 0.2,
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal OpenRouter request: %w", err)
	}

	// Set API call timeout to 15 seconds
	apiCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(apiCtx, "POST", "https://openrouter.ai/api/v1/chat/completions", bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("failed to create OpenRouter HTTP request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("HTTP-Referer", "https://github.com/sleepysoong/serendipity")
	req.Header.Set("X-Title", "Serendipity Cardnews Autopipeline")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("OpenRouter API call failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("OpenRouter returned status code: %d, response: %s", resp.StatusCode, string(bodyBytes))
	}

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read OpenRouter response: %w", err)
	}

	var chatResponse ChatCompletionResponse
	if err := json.Unmarshal(bodyBytes, &chatResponse); err != nil {
		return nil, fmt.Errorf("failed to unmarshal OpenRouter response JSON: %w", err)
	}

	if len(chatResponse.Choices) == 0 {
		return nil, fmt.Errorf("OpenRouter returned response with no completion choices")
	}

	rawContent := chatResponse.Choices[0].Message.Content
	sanitized := SanitizeJSON(rawContent)

	var cards []CardContent
	if err := json.Unmarshal([]byte(sanitized), &cards); err != nil {
		return nil, fmt.Errorf("failed to unmarshal sanitized JSON into CardContent slice (Raw: %q, Sanitized: %q): %w", rawContent, sanitized, err)
	}

	// Validate character counts as an extra programmatic guard rail
	for idx, card := range cards {
		if len([]rune(card.Title)) > 15 {
			return nil, fmt.Errorf("validation failed: card %d title length %d exceeds 15 chars", idx+1, len([]rune(card.Title)))
		}
		if len([]rune(card.Body)) > 50 {
			return nil, fmt.Errorf("validation failed: card %d body length %d exceeds 50 chars", idx+1, len([]rune(card.Body)))
		}
	}

	return cards, nil
}
