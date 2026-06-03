# 자동화 카드뉴스 생성 파이프라인 (Automated Cardnews Auto-Pipeline)

이 시스템은 Brave Search API, OpenRouter (google/gemma-4-31b-it:free), Figma 생태계(Remote MCP Server 및 REST API)를 연동하여 카드뉴스 기획부터 렌더링, 파일 다운로드까지 전 과정을 무인으로 자동화하는 파이프라인입니다.

---

## 1. 주요 특징
1. **자동 뉴스거리 선정 모드 (Search + LLM)**: 사용자가 직접 주제를 입력하지 않아도 실시간 핫이슈를 검색하여 흥미로운 주제를 자동 선정합니다.
2. **엄격한 자연어 추론 (Gemma 4)**: 제목 15자, 본문 50자 이내의 글자수 유효성 검증과 JSON 위생처리가 탑재된 LLM 파싱 시스템입니다.
3. **2단계 동기화 배리어 (Double Barrier Sync)**: 피그마 서버의 비동기 그래픽 처리 시점을 감지하기 위해 JSON-RPC 성공 응답 대기 및 metadata `lastModified` 폴링(최대 5초/10회 제한) 메커니즘을 사용해 레이스 컨디션을 방지합니다.
4. **유연한 매니페스트 설계**: 논리적 키값과 물리 노드 ID를 분리하여 재컴파일 없이 디자이너의 수정 사항을 즉시 연동합니다.

---

## 2. 환경 설정 및 세팅 방법

### 2.1 환경 변수 설정
프로젝트 구동을 위해 다음 환경 변수들을 세팅해야 합니다.

```bash
# Brave Search API 키 (데이터 수집용)
export BRAVE_API_KEY="your_brave_search_api_key"

# OpenRouter API 키 (LLM 추론용)
export OPENROUTER_API_KEY="your_openrouter_api_key"

# Figma 개인 액세스 토큰 (Figma PAT, REST API 호출용)
export FIGMA_PAT="your_figma_personal_access_token"

# Figma 대상 템플릿 파일 키
export FIGMA_FILE_KEY="your_figma_file_key"

# Figma Remote MCP 서버 SSE 엔드포인트 URL
export FIGMA_MCP_ENDPOINT="http://localhost:8080/sse"
```

### 2.2 피그마 캔버스 설정
* 디자이너는 Figma 카드뉴스 템플릿 내 텍스트 노드의 크기를 **Fixed size**로 지정하고, 텍스트 넘침 속성을 **Truncate text**로 지정해야 합니다. 이는 텍스트 박스 이탈 및 디자인 붕괴를 막기 위한 물리 장벽입니다.

### 2.3 매니페스트 구성 (`manifest.json`)
바이너리 재컴파일 없이 노드 ID를 바인딩하기 위해 실행 경로에 `manifest.json` 파일을 작성합니다.
* 예시:
```json
{
  "Card_Page_1_Frame": "0:10",
  "Card_Page_1_Title": "0:1",
  "Card_Page_1_Body": "0:2",
  "Card_Page_2_Frame": "0:20",
  "Card_Page_2_Title": "0:3",
  "Card_Page_2_Body": "0:4",
  "Card_Page_3_Frame": "0:30",
  "Card_Page_3_Title": "0:5",
  "Card_Page_3_Body": "0:6"
}
```

---

## 3. 실행 방법

### 3.1 자동 뉴스거리 선정 모드 (기본값)
인기 있는 시사 이슈를 검색하여 최적의 뉴스 주제를 자동 선정한 후 카드뉴스를 생성합니다.
```bash
go run cmd/cardnews/main.go -output output -topn 3
```

### 3.2 수동 검색 쿼리 모드
특정 주제를 지정하여 카드뉴스를 추출하려면 `-query` 옵션을 전달합니다. 이 경우 자동 선정은 건너뜁니다.
```bash
go run cmd/cardnews/main.go -query "한국은행 기준금리 동결" -output output -topn 3
```

### 3.3 로컬 목업(Mock) 서버 테스트 모드
실제 피그마 계정이 없거나 오프라인 환경인 경우, 로컬 목업 서버를 띄워 전체 파이프라인의 갱신/동기화/다운로드 흐름을 검증할 수 있습니다.

1. **Figma 목업 서버 구동** (터미널 1)
```bash
go run cmd/mock_figma/main.go
```

2. **환경변수 임시 등록** (터미널 2)
```bash
# Brave Search와 OpenRouter LLM의 실제 API 키는 필요합니다. (피그마 PAT/File은 가짜값 가능)
export BRAVE_API_KEY="your_real_brave_api_key"
export OPENROUTER_API_KEY="your_real_openrouter_api_key"
export FIGMA_PAT="mock-pat"
export FIGMA_FILE_KEY="mock-file-key"
export FIGMA_MCP_ENDPOINT="http://localhost:8080/sse"
```

3. **파이프라인 작동** (터미널 2)
```bash
go run cmd/cardnews/main.go -output local_output
```
* 파이프라인이 종료되면 `local_output` 디렉토리에 모사된 투명 PNG 카드뉴스 파일들(`card_page_1.png` 등)이 성공적으로 생성되는 것을 확인할 수 있습니다.

---

## 4. 테스트 실행
작성된 유효성 검사 및 정제 모듈 단위 테스트는 아래 명령어로 실행할 수 있습니다.
```bash
go test -v ./...
```
