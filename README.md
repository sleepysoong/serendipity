# 자동화 카드뉴스 생성 파이프라인 (Serendipity)

Brave Search API, OpenRouter LLM, headless Chrome(chromedp)을 연동하여 카드뉴스 기획부터 이미지 렌더링까지 전 과정을 무인으로 자동화하는 Go 기반 파이프라인입니다.

---

## 1. 주요 특징
1. **자동 뉴스거리 선정 모드 (Search + LLM)**: 사용자가 직접 주제를 입력하지 않아도 실시간 핫이슈를 검색하여 흥미로운 주제를 자동 선정합니다.
2. **엄격한 자연어 추론**: 제목 15자, 본문 50자 이내의 글자수 유효성 검증과 JSON 위생처리가 탑재된 LLM 파싱 시스템입니다.
3. **HTML 기반 카드뉴스 렌더링**: Go 내장 HTML 템플릿과 headless Chrome(chromedp)을 활용하여 고품질 1080×1080 PNG 카드 이미지를 자동 생성합니다.
4. **커스텀 LLM 모델 지원**: 환경 변수 또는 CLI 플래그로 LLM 모델을 자유롭게 변경할 수 있습니다.

---

## 2. 사전 요구사항

### 2.1 시스템 의존성
- **Go 1.25+**
- **Google Chrome** 또는 **Chromium** (headless 모드로 카드 이미지를 렌더링하는 데 사용)

```bash
# Ubuntu/Debian 기준 Chrome 설치
sudo apt-get install -y chromium-browser

# macOS (Homebrew)
brew install --cask google-chrome
```

### 2.2 환경 변수 설정
프로젝트 구동을 위해 다음 환경 변수들을 세팅해야 합니다.

```bash
# Brave Search API 키 (데이터 수집용)
export BRAVE_API_KEY="your_brave_search_api_key"

# OpenRouter API 키 (LLM 추론용)
export OPENROUTER_API_KEY="your_openrouter_api_key"

# 사용할 LLM 모델명 (선택사항, 기본값: google/gemma-4-31b-it:free)
export LLM_MODEL="google/gemma-4-31b-it:free"
```

#### API 키 발급 방법
| 서비스 | 발급 URL | 비고 |
|--------|----------|------|
| Brave Search | https://brave.com/search/api/ | Free 플랜 사용 가능 |
| OpenRouter | https://openrouter.ai/keys | 무료 모델(`google/gemma-4-31b-it:free`) 지원 |

---

## 3. 설치 및 빌드

```bash
# 저장소 클론
git clone https://github.com/sleepysoong/serendipity.git
cd serendipity

# 의존성 설치
go mod tidy

# 빌드 (선택사항)
go build -o cardnews ./cmd/cardnews/
```

---

## 4. 실행 방법

### 4.1 자동 뉴스거리 선정 모드 (기본값)
인기 있는 시사 이슈를 검색하여 최적의 뉴스 주제를 자동 선정한 후 카드뉴스를 생성합니다.
```bash
go run cmd/cardnews/main.go -output output -topn 3
```

### 4.2 수동 검색 쿼리 모드
특정 주제를 지정하여 카드뉴스를 추출하려면 `-query` 옵션을 전달합니다. 이 경우 자동 선정은 건너뜁니다.
```bash
go run cmd/cardnews/main.go -query "한국은행 기준금리 동결" -output output -topn 3
```

### 4.3 모델 커스텀 모드
특정 LLM 모델을 사용하려면 `-model` 플래그를 넘겨 실행하거나 `LLM_MODEL` 환경 변수를 설정합니다.
```bash
go run cmd/cardnews/main.go -model "meta-llama/llama-3-70b-instruct:free" -output output
```

### CLI 플래그 요약
| 플래그 | 기본값 | 설명 |
|--------|--------|------|
| `-query` | (빈 문자열) | 수동 검색 쿼리 (설정 시 자동 선정 비활성화) |
| `-auto` | `true` | 자동 뉴스거리 선정 모드 활성화 여부 |
| `-model` | 환경변수 or `gemma-4` | 사용할 LLM 모델명 |
| `-output` | `output` | 생성된 PNG 저장 디렉토리 |
| `-topn` | `3` | Brave Search 수집 결과 개수 |

---

## 5. 파이프라인 동작 흐름

```
[1/4] 설정 로드 (환경 변수)
  ↓
[1.5/4] (자동 모드) Brave Search + LLM으로 뉴스거리 자동 선정
  ↓
[2/4] Brave Search로 선정된 주제의 세부 컨텍스트 수집
  ↓
[3/4] OpenRouter LLM으로 구조화된 카드 콘텐츠(제목/본문) 생성
  ↓
[4/4] HTML 템플릿 렌더링 → headless Chrome 스크린샷 → PNG 저장
```

---

## 6. 프로젝트 구조

```
serendipity/
├── cmd/
│   └── cardnews/
│       └── main.go          # 파이프라인 진입점
├── internal/
│   ├── config/
│   │   └── config.go        # 환경 변수 설정 로드
│   ├── llm/
│   │   └── llm.go           # LLM 주제 선정 및 카드 콘텐츠 생성
│   ├── renderer/
│   │   └── renderer.go      # HTML 템플릿 + chromedp 이미지 렌더링
│   └── search/
│       └── search.go        # Brave Search API 연동
├── go.mod
├── go.sum
└── README.md
```

---

## 7. 테스트 실행
```bash
go test -v ./...
```
