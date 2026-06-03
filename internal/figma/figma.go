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
		return beforeMCPTime, fmt.Errorf("failed to create Figma SSE MCP Client: %w", err)
	}

	// 3. Start client transport
	if err := cli.Start(ctx); err != nil {
		return beforeMCPTime, fmt.Errorf("failed to start Figma MCP client transport: %w", err)
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
		return beforeMCPTime, fmt.Errorf("failed to initialize Figma MCP session: %w", err)
	}

	// 5. Iterate through LLM cards and update each corresponding Figma text node
	for i, card := range cards {
		cardIndex := i + 1
		titleLogicalKey := fmt.Sprintf("Card_Page_%d_Title", cardIndex)
		bodyLogicalKey := fmt.Sprintf("Card_Page_%d_Body", cardIndex)

		titleNodeID, ok := mappings[titleLogicalKey]
		if !ok {
			return beforeMCPTime, fmt.Errorf("node mapping for logical identifier %q not found in manifest", titleLogicalKey)
		}

		bodyNodeID, ok := mappings[bodyLogicalKey]
		if !ok {
			return beforeMCPTime, fmt.Errorf("node mapping for logical identifier %q not found in manifest", bodyLogicalKey)
		}

		// Update Title Node
		if err := setNodeText(ctx, cli, titleNodeID, card.Title); err != nil {
			return beforeMCPTime, fmt.Errorf("failed to set text content for %s (%s): %w", titleLogicalKey, titleNodeID, err)
		}

		// Update Body Node
		if err := setNodeText(ctx, cli, bodyNodeID, card.Body); err != nil {
			return beforeMCPTime, fmt.Errorf("failed to set text content for %s (%s): %w", bodyLogicalKey, bodyNodeID, err)
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
		return fmt.Errorf("MCP CallTool set_text_content failed: %w", err)
	}

	if res.IsError {
		return fmt.Errorf("MCP CallTool set_text_content returned error status in response")
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
			return fmt.Errorf("version polling context cancelled or timed out: %w", pollCtx.Err())
		default:
		}

		req, err := http.NewRequestWithContext(pollCtx, "GET", endpoint, nil)
		if err != nil {
			return fmt.Errorf("failed to create file metadata HTTP request: %w", err)
		}

		req.Header.Set("X-Figma-Token", figmaPAT)

		resp, err := client.Do(req)
		if err != nil {
			return fmt.Errorf("figma file metadata request failed: %w", err)
		}

		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			if resp.StatusCode == http.StatusTooManyRequests {
				// Retry on 429
				time.Sleep(interval)
				continue
			}
			return fmt.Errorf("figma file API returned non-200 status code: %d", resp.StatusCode)
		}

		bodyBytes, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return fmt.Errorf("failed to read figma file response body: %w", err)
		}

		var fileResp FileResponse
		if err := json.Unmarshal(bodyBytes, &fileResp); err != nil {
			return fmt.Errorf("failed to unmarshal figma file response JSON: %w", err)
		}

		if fileResp.LastModified == "" {
			return fmt.Errorf("lastModified field missing in Figma response")
		}

		lastModifiedTime, err := time.Parse(time.RFC3339, fileResp.LastModified)
		if err != nil {
			return fmt.Errorf("failed to parse Figma lastModified time (%s): %w", fileResp.LastModified, err)
		}

		// Check if Figma lastModified is strictly in the future relative to our pre-MCP capture timestamp
		if lastModifiedTime.After(beforeMCPTime) {
			// Success, Figma backend has finished rendering!
			return nil
		}

		// Wait 500ms before next poll attempt
		select {
		case <-pollCtx.Done():
			return fmt.Errorf("version polling timed out: %w", pollCtx.Err())
		case <-time.After(interval):
		}
	}

	return fmt.Errorf("failed to verify canvas update after maximum 10 version poll attempts")
}

// ExportImages calls Figma REST API's image export endpoint to render target frame node IDs.
func ExportImages(ctx context.Context, figmaPAT, fileKey string, nodeIDs []string) (map[string]string, error) {
	if len(nodeIDs) == 0 {
		return nil, fmt.Errorf("no node IDs specified for image export")
	}

	idsParam := strings.Join(nodeIDs, ",")
	endpoint := fmt.Sprintf("https://api.figma.com/v1/images/%s?ids=%s&format=png", fileKey, idsParam)

	req, err := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create image export request: %w", err)
	}

	req.Header.Set("X-Figma-Token", figmaPAT)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("figma image export request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("figma image export returned status: %d, response: %s", resp.StatusCode, string(bodyBytes))
	}

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read export response body: %w", err)
	}

	var exportResp ExportResponse
	if err := json.Unmarshal(bodyBytes, &exportResp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal image export response JSON: %w", err)
	}

	if len(exportResp.Images) == 0 {
		return nil, fmt.Errorf("figma export returned no image links in response")
	}

	return exportResp.Images, nil
}

// DownloadImage retrieves a binary image file from a URL and saves it to local disk.
func DownloadImage(ctx context.Context, imageURL, outputPath string) error {
	req, err := http.NewRequestWithContext(ctx, "GET", imageURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create image download request: %w", err)
	}

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to execute image download: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("image download returned status code: %d", resp.StatusCode)
	}

	out, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("failed to create local output file %s: %w", outputPath, err)
	}
	defer out.Close()

	_, err = io.Copy(out, resp.Body)
	if err != nil {
		return fmt.Errorf("failed to copy binary stream to local file: %w", err)
	}

	return nil
}
