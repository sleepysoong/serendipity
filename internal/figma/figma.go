package figma

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
	"serendipity/internal/llm"
)

// FileResponse is used to parse the file metadata for the lastModified timestamp.
type FileResponse struct {
	LastModified string `json:"lastModified"`
}

// ExportResponse is used to parse the image export result containing AWS S3 URLs.
type ExportResponse struct {
	Images map[string]string `json:"images"`
	Err    any               `json:"err"`
}

// UpdateCanvas connects to the Figma Remote MCP Server and updates the text nodes
// with contents from LLM generated slides.
func UpdateCanvas(ctx context.Context, figmaPAT, figmaMCPEndpoint string, mappings map[string]string, cards []llm.CardContent) (time.Time, error) {
	// 1. Capture UTC timestamp immediately prior to sending any state-altering MCP requests
	beforeMCPTime := time.Now().UTC()

	// 2. Initialize the SSE MCP Client
	// We inject the Figma PAT into the HTTP headers for transport authentication
	cli, err := client.NewSSEMCPClient(figmaMCPEndpoint, transport.WithHeaders(map[string]string{
		"Authorization": "Bearer " + figmaPAT,
		"X-Figma-Token": figmaPAT,
	}))
	if err != nil {
		return beforeMCPTime, fmt.Errorf("Figma SSE MCP 클라이언트 생성 실패: %w", err)
	}

	// 3. Start client transport
	if err := cli.Start(ctx); err != nil {
		return beforeMCPTime, fmt.Errorf("Figma MCP 클라이언트 전송 채널(Start) 시작 실패: %w", err)
	}
	defer cli.Close()

	// 4. Initialize MCP handshake
	initRequest := mcp.InitializeRequest{}
	initRequest.Method = "initialize"
	initRequest.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initRequest.Params.ClientInfo = mcp.Implementation{
		Name:    "serendipity-figma-client",
		Version: "1.0.0",
	}

	if _, err := cli.Initialize(ctx, initRequest); err != nil {
		return beforeMCPTime, fmt.Errorf("Figma MCP 세션 초기화 실패: %w", err)
	}

	// 5. Iterate through LLM cards and update each corresponding Figma text node
	for i, card := range cards {
		cardIndex := i + 1
		titleLogicalKey := fmt.Sprintf("Card_Page_%d_Title", cardIndex)
		bodyLogicalKey := fmt.Sprintf("Card_Page_%d_Body", cardIndex)

		titleNodeID, ok := mappings[titleLogicalKey]
		if !ok {
			return beforeMCPTime, fmt.Errorf("매니페스트에서 논리적 식별자 %q에 해당하는 노드 매핑을 찾을 수 없습니다: %w", titleLogicalKey, err)
		}

		bodyNodeID, ok := mappings[bodyLogicalKey]
		if !ok {
			return beforeMCPTime, fmt.Errorf("매니페스트에서 논리적 식별자 %q에 해당하는 노드 매핑을 찾을 수 없습니다: %w", bodyLogicalKey, err)
		}

		// Update Title Node
		if err := setNodeText(ctx, cli, titleNodeID, card.Title); err != nil {
			return beforeMCPTime, fmt.Errorf("%s (%s)의 텍스트 콘텐츠 설정 실패: %w", titleLogicalKey, titleNodeID, err)
		}

		// Update Body Node
		if err := setNodeText(ctx, cli, bodyNodeID, card.Body); err != nil {
			return beforeMCPTime, fmt.Errorf("%s (%s)의 텍스트 콘텐츠 설정 실패: %w", bodyLogicalKey, bodyNodeID, err)
		}
	}

	return beforeMCPTime, nil
}

// setNodeText executes the set_text_content tool call and waits for success response (Stage 1 Barrier).
func setNodeText(ctx context.Context, cli *client.Client, nodeID, textContent string) error {
	req := mcp.CallToolRequest{}
	req.Method = "tools/call"
	req.Params.Name = "set_text_content"
	// To be robust, we supply both "text" and "characters" argument fields
	req.Params.Arguments = map[string]any{
		"nodeId":     nodeID,
		"text":       textContent,
		"characters": textContent,
	}

	res, err := cli.CallTool(ctx, req)
	if err != nil {
		return fmt.Errorf("MCP CallTool set_text_content 호출 실패: %w", err)
	}

	if res.IsError {
		return fmt.Errorf("MCP CallTool set_text_content 응답에서 에러 상태를 반환했습니다")
	}

	return nil
}

// PollVersion performs Stage 2 Barrier (version polling) against Figma REST API
// to verify figma's graphics rendering pipeline has completed and flushed edits.
func PollVersion(ctx context.Context, figmaPAT, fileKey string, beforeMCPTime time.Time) error {
	// Create context with maximum 5 seconds timeout as requested
	pollCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	client := &http.Client{}
	endpoint := fmt.Sprintf("https://api.figma.com/v1/files/%s?depth=1", fileKey)

	maxAttempts := 10
	interval := 500 * time.Millisecond

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		select {
		case <-pollCtx.Done():
			return fmt.Errorf("버전 폴링 컨텍스트가 취소되었거나 타임아웃되었습니다: %w", pollCtx.Err())
		default:
		}

		req, err := http.NewRequestWithContext(pollCtx, "GET", endpoint, nil)
		if err != nil {
			return fmt.Errorf("파일 메타데이터 HTTP 요청 생성 실패: %w", err)
		}

		req.Header.Set("X-Figma-Token", figmaPAT)

		resp, err := client.Do(req)
		if err != nil {
			return fmt.Errorf("Figma 파일 메타데이터 요청 실패: %w", err)
		}

		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			if resp.StatusCode == http.StatusTooManyRequests {
				// Retry on 429
				time.Sleep(interval)
				continue
			}
			return fmt.Errorf("Figma 파일 API가 200이 아닌 상태 코드 %d를 반환했습니다", resp.StatusCode)
		}

		bodyBytes, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return fmt.Errorf("Figma 파일 응답 바디 읽기 실패: %w", err)
		}

		var fileResp FileResponse
		if err := json.Unmarshal(bodyBytes, &fileResp); err != nil {
			return fmt.Errorf("Figma 파일 응답 JSON 파싱 실패: %w", err)
		}

		if fileResp.LastModified == "" {
			return fmt.Errorf("Figma 응답에 lastModified 필드가 누락되었습니다")
		}

		lastModifiedTime, err := time.Parse(time.RFC3339, fileResp.LastModified)
		if err != nil {
			return fmt.Errorf("Figma lastModified 시간(%s) 파싱 실패: %w", fileResp.LastModified, err)
		}

		// Check if Figma lastModified is strictly in the future relative to our pre-MCP capture timestamp
		if lastModifiedTime.After(beforeMCPTime) {
			// Success, Figma backend has finished rendering!
			return nil
		}

		// Wait 500ms before next poll attempt
		select {
		case <-pollCtx.Done():
			return fmt.Errorf("버전 폴링 타임아웃: %w", pollCtx.Err())
		case <-time.After(interval):
		}
	}

	return fmt.Errorf("최대 10회 버전 폴링 시도 후 캔버스 업데이트 확인 실패")
}

// ExportImages calls Figma REST API's image export endpoint to render target frame node IDs.
func ExportImages(ctx context.Context, figmaPAT, fileKey string, nodeIDs []string) (map[string]string, error) {
	if len(nodeIDs) == 0 {
		return nil, fmt.Errorf("이미지 내보내기를 위한 노드 ID가 지정되지 않았습니다")
	}

	idsParam := strings.Join(nodeIDs, ",")
	endpoint := fmt.Sprintf("https://api.figma.com/v1/images/%s?ids=%s&format=png", fileKey, idsParam)

	req, err := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("이미지 내보내기 요청 생성 실패: %w", err)
	}

	req.Header.Set("X-Figma-Token", figmaPAT)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Figma 이미지 내보내기 요청 실패: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("Figma 이미지 내보내기가 상태 코드 %d를 반환했습니다. 응답: %s", resp.StatusCode, string(bodyBytes))
	}

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("내보내기 응답 바디 읽기 실패: %w", err)
	}

	var exportResp ExportResponse
	if err := json.Unmarshal(bodyBytes, &exportResp); err != nil {
		return nil, fmt.Errorf("이미지 내보내기 응답 JSON 파싱 실패: %w", err)
	}

	if len(exportResp.Images) == 0 {
		return nil, fmt.Errorf("Figma 내보내기 응답에 이미지 링크가 없습니다")
	}

	return exportResp.Images, nil
}

// DownloadImage retrieves a binary image file from a URL and saves it to local disk.
func DownloadImage(ctx context.Context, imageURL, outputPath string) error {
	req, err := http.NewRequestWithContext(ctx, "GET", imageURL, nil)
	if err != nil {
		return fmt.Errorf("이미지 다운로드 요청 생성 실패: %w", err)
	}

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("이미지 다운로드 실행 실패: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("이미지 다운로드가 상태 코드 %d를 반환했습니다", resp.StatusCode)
	}

	out, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("로컬 출력 파일 %s 생성 실패: %w", outputPath, err)
	}
	defer out.Close()

	_, err = io.Copy(out, resp.Body)
	if err != nil {
		return fmt.Errorf("바이너리 스트림을 로컬 파일로 복사하는 데 실패했습니다: %w", err)
	}

	return nil
}
