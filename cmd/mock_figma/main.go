package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Figma 목업 서버의 전역 그래픽 상태 및 클라이언트 세션 관리
var (
	lastModified      time.Time
	lastModifiedMutex sync.RWMutex
	sessions          = make(map[string]chan string)
	sessionsMutex     sync.Mutex
)

// 1x1 투명 PNG 이미지의 Base64 데이터 (로컬 이미지 저장 테스트용)
const mockPNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNkYAAAAAYAAjCB0C8AAAAASUVORK5CYII="

type JSONRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	ID      any             `json:"id,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type JSONRPCResponse struct {
	JSONRPC string `json:"jsonrpc"`
	ID      any    `json:"id"`
	Result  any    `json:"result,omitempty"`
	Error   any    `json:"error,omitempty"`
}

func main() {
	// 초기 Figma 수정 시각을 현재로부터 2시간 전으로 설정
	lastModified = time.Now().Add(-2 * time.Hour)

	http.HandleFunc("/sse", handleSSE)
	http.HandleFunc("/message", handleMessage)
	http.HandleFunc("/v1/files/", handleFigmaFileAPI)
	http.HandleFunc("/v1/images/", handleFigmaImageAPI)
	http.HandleFunc("/mock_image.png", handleMockImage)

	port := ":8080"
	log.Printf("[Figma Mock Server] 서버가 시작되었습니다. 포트 %s", port)
	log.Printf("[Figma Mock Server] http://localhost%s/sse (MCP SSE 엔드포인트)", port)
	log.Printf("[Figma Mock Server] http://localhost%s/v1/files/ (Figma REST API)", port)

	if err := http.ListenAndServe(port, nil); err != nil {
		log.Fatalf("목업 서버 구동 실패: %v", err)
	}
}

// handleSSE: MCP SSE 연결 수립 핸들러
func handleSSE(w http.ResponseWriter, r *http.Request) {
	log.Println("[MOCK SSE] 새로운 SSE 연결 요청이 감지되었습니다.")
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	// 새로운 세션 채널 개설
	sessionID := fmt.Sprintf("session-%d", time.Now().UnixNano())
	msgChan := make(chan string, 100)

	sessionsMutex.Lock()
	sessions[sessionID] = msgChan
	sessionsMutex.Unlock()

	defer func() {
		sessionsMutex.Lock()
		delete(sessions, sessionID)
		close(msgChan)
		sessionsMutex.Unlock()
		log.Printf("[MOCK SSE] 세션 %s 종료 및 정리 완료.", sessionID)
	}()

	// 1. 클라이언트에게 JSON-RPC 메시지를 전송받을 POST 엔드포인트 URL 전달 (MCP SSE 표준)
	endpointURL := fmt.Sprintf("http://%s/message?session_id=%s", r.Host, sessionID)
	fmt.Fprintf(w, "event: endpoint\ndata: %s\n\n", endpointURL)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	log.Printf("[MOCK SSE] 세션 %s 등록 완료. 전송 엔드포인트: %s", sessionID, endpointURL)

	// 세션 종료 시까지 대기하며 채널에 들어오는 JSON-RPC 응답들을 SSE 연결에 쓰기 작업 수행
	for {
		select {
		case <-r.Context().Done():
			log.Printf("[MOCK SSE] 클라이언트 연결 끊김 (세션: %s)", sessionID)
			return
		case msg := <-msgChan:
			fmt.Fprintf(w, "event: message\ndata: %s\n\n", msg)
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
	}
}

// handleMessage: 클라이언트가 보낸 JSON-RPC 요청(POST)을 파싱하여 가짜 응답을 SSE 채널로 반송
func handleMessage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST 메소드만 허용됩니다.", http.StatusMethodNotAllowed)
		return
	}

	sessionID := r.URL.Query().Get("session_id")
	sessionsMutex.Lock()
	msgChan, ok := sessions[sessionID]
	sessionsMutex.Unlock()

	if !ok {
		http.Error(w, "유효하지 않은 세션 ID입니다.", http.StatusBadRequest)
		return
	}

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "요청 바디 읽기 실패", http.StatusInternalServerError)
		return
	}
	r.Body.Close()

	var req JSONRPCRequest
	if err := json.Unmarshal(bodyBytes, &req); err != nil {
		http.Error(w, "JSON 파싱 에러", http.StatusBadRequest)
		return
	}

	log.Printf("[MOCK MCP] 요청 수신 (세션: %s) -> 메소드: %s, ID: %v", sessionID, req.Method, req.ID)

	var response JSONRPCResponse
	response.JSONRPC = "2.0"
	response.ID = req.ID

	switch req.Method {
	case "initialize":
		// MCP 초기화 응답
		response.Result = map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]any{},
			"serverInfo": map[string]any{
				"name":    "figma-mock-mcp-server",
				"version": "1.0.0",
			},
		}
	case "tools/list":
		// 제공 도구 목록
		response.Result = map[string]any{
			"tools": []map[string]any{
				{
					"name":        "set_text_content",
					"description": "Figma 텍스트 노드의 문자열을 설정합니다.",
					"inputSchema": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"nodeId":     map[string]any{"type": "string"},
							"text":       map[string]any{"type": "string"},
							"characters": map[string]any{"type": "string"},
						},
						"required": []string{"nodeId"},
					},
				},
			},
		}
	case "tools/call":
		// 도구 실행 처리 (set_text_content)
		var callParams struct {
			Name      string `json:"name"`
			Arguments struct {
				NodeID     string `json:"nodeId"`
				Text       string `json:"text"`
				Characters string `json:"characters"`
			} `json:"arguments"`
		}
		_ = json.Unmarshal(bodyBytes, &callParams)

		valText := callParams.Arguments.Text
		if valText == "" {
			valText = callParams.Arguments.Characters
		}

		log.Printf("[MOCK FIGMA MCP] set_text_content 호출됨 -> 노드: %s, 내용: %q", callParams.Arguments.NodeID, valText)

		// 그래픽스 엔진 변경 트리거: Figma metadata의 lastModified 시간을 현재 타임스탬프로 업데이트
		lastModifiedMutex.Lock()
		lastModified = time.Now().UTC()
		lastModifiedMutex.Unlock()

		response.Result = map[string]any{
			"content": []map[string]any{
				{
					"type": "text",
					"text": "노드 텍스트 업데이트에 성공했습니다.",
				},
			},
			"isError": false,
		}
	default:
		response.Result = map[string]any{}
	}

	respBytes, _ := json.Marshal(response)
	msgChan <- string(respBytes)

	// POST 요청 자체는 200 OK로 즉시 리턴
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("Accepted"))
}

// handleFigmaFileAPI: Figma REST API 파일 정보 반환 (lastModified 비교용)
func handleFigmaFileAPI(w http.ResponseWriter, r *http.Request) {
	log.Println("[MOCK REST API] GET /v1/files/ 파일 메타데이터 조회 요청이 감지되었습니다.")
	w.Header().Set("Content-Type", "application/json")

	lastModifiedMutex.RLock()
	modTime := lastModified.Format(time.RFC3339)
	lastModifiedMutex.RUnlock()

	// 클라이언트에서 depth=1을 포함해 요청한 최소 메타데이터 포맷 반환
	resp := map[string]any{
		"name":         "Mock Card News Design File",
		"lastModified": modTime,
		"version":      "1.0.0",
		"document": map[string]any{
			"type": "DOCUMENT",
			"id":   "0:0",
		},
	}

	json.NewEncoder(w).Encode(resp)
}

// handleFigmaImageAPI: Figma REST API 이미지 내보내기 요청 (AWS S3 URL 모사)
func handleFigmaImageAPI(w http.ResponseWriter, r *http.Request) {
	log.Println("[MOCK REST API] GET /v1/images/ 내보내기 요청이 감지되었습니다.")
	w.Header().Set("Content-Type", "application/json")

	idsQuery := r.URL.Query().Get("ids")
	ids := strings.Split(idsQuery, ",")

	imagesMap := make(map[string]string)
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}

	for _, id := range ids {
		if id != "" {
			// 로컬 mock_image.png 다운로드 링크 바인딩
			imagesMap[id] = fmt.Sprintf("%s://%s/mock_image.png?node=%s", scheme, r.Host, id)
		}
	}

	resp := map[string]any{
		"images": imagesMap,
		"err":    nil,
	}

	json.NewEncoder(w).Encode(resp)
}

// handleMockImage: 1x1 투명 PNG 바이너리 파일을 서빙하여 로컬 저장 다운로드 기능 지원
func handleMockImage(w http.ResponseWriter, r *http.Request) {
	nodeID := r.URL.Query().Get("node")
	log.Printf("[MOCK REST API] 이미지 다운로드 서빙 중 (노드 ID: %s)", nodeID)

	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-cache")

	data, err := base64.StdEncoding.DecodeString(mockPNG)
	if err != nil {
		http.Error(w, "PNG 디코딩 오류", http.StatusInternalServerError)
		return
	}

	w.Write(data)
}
