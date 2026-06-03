# 자동화 카드뉴스 생성기 (디스코드 봇 지원)

Brave Search API, OpenRouter LLM, Go 이미지 라이브러리(`fogleman/gg`)를 연동하여 카드뉴스 기획부터 이미지 렌더링까지 전 과정을 자동화하며, 디스코드 봇으로 통합 관리할 수 있는 프로젝트입니다.

---

## 1. 주요 특징
1. **디스코드 봇 완전 통합**: `/뉴스생성` 슬래시 커맨드를 통해 디스코드에서 직접 뉴스를 생성하고 이미지를 받아볼 수 있습니다.
2. **자동 스케줄링**: 매일 오전 00:00시에 설정된 채널로 자동 뉴스가 전송됩니다.
3. **인터랙티브 재생성**: 봇이 생성한 메시지의 버튼을 눌러 이미지나 타이틀을 손쉽게 변경할 수 있습니다.
4. **다양한 표지 이미지 지원**: Brave Search Image API를 통해 주제에 맞는 3개의 배경 이미지를 자동 탐색하고, 각각 적용된 3가지 버전을 한 번에 제시합니다.
5. **네이티브 이미지 렌더링**: Go 이미지 라이브러리(`fogleman/gg`)로 HTML 디자인(자간, 그림자, 비율, 폰트)을 정밀하게 구현했습니다.

---

## 2. 사전 요구사항

### 2.1 시스템 의존성
- **Go 1.25+**

### 2.2 설정 (`config.yml`)
기존의 환경 변수(env) 방식 대신 `config.yml` 파일을 사용하여 설정을 관리합니다. 
처음 실행 시 파일이 없으면 자동으로 생성됩니다.

```yaml
brave_api_key: "your_brave_api_key"
openrouter_api_key: "your_openrouter_api_key"
llm_model: "google/gemma-4-31b-it:free"
discord_bot_token: "your_discord_bot_token"
```

디스코드 내에서 `/세팅` 명령어를 통해서도 API Key를 동적으로 갱신할 수 있습니다.

---

## 3. 설치 및 빌드

```bash
# 저장소 클론
git clone https://github.com/sleepysoong/serendipity.git
cd serendipity

# 의존성 설치
go mod tidy

# 봇 빌드
go build -o bot ./cmd/bot/
```

---

## 4. 실행 방법

### 디스코드 봇 구동
디스코드 봇을 백그라운드로 띄워 상시 구동합니다.
```bash
./bot
```
- `/뉴스생성 [주제]`: 뉴스 생성 (주제를 비우면 자동 탐색)
- `/뉴스채널`: 봇이 스케줄링된(오전 00:00) 뉴스를 보낼 채널을 현재 채널로 설정
- `/세팅`: 봇 설정 변경 (API Key 등)

---

## 5. 프로젝트 구조

```
serendipity/
├── cmd/
│   └── bot/
│       └── main.go          # 디스코드 봇 진입점
├── internal/
│   ├── bot/               # 디스코드 슬래시 커맨드 및 스케줄러 로직
│   ├── config/            # config.yml 파싱 및 저장 로직
│   ├── llm/               # OpenRouter LLM 연동
│   ├── pipeline/          # 카드뉴스 생성 전체 파이프라인 (검색 -> LLM -> 렌더)
│   ├── renderer/          # gg 이미지 렌더링
│   └── search/            # Brave Search 연동 (웹/이미지)
└── go.mod
```

---

## 6. 테스트 실행
```bash
go test -v ./...
```
