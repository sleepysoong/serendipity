package bot

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/robfig/cron/v3"
	
	"serendipity/internal/config"
	"serendipity/internal/pipeline"
	"serendipity/internal/renderer"
)

type CachedResult struct {
	Result *pipeline.PipelineResult
	Topic  string
}

type Bot struct {
	Session *discordgo.Session
	Config  *config.Config
	Cron    *cron.Cron
	Ctx     context.Context
	Cancel  context.CancelFunc
	
	// channel ID for scheduled news
	NewsChannelID string 
	
	Cache      map[string]CachedResult
	WaitUpload map[string]string // "userID" -> resultID
	
	mu sync.Mutex
}

func NewBot(cfg *config.Config) (*Bot, error) {
	if cfg.DiscordBotToken == "" {
		return nil, fmt.Errorf("DiscordBotToken이 설정되지 않았습니다")
	}

	s, err := discordgo.New("Bot " + cfg.DiscordBotToken)
	if err != nil {
		return nil, err
	}
	// 메시지 수신 및 내용 분석을 위해 필수적인 Gateway Intent들을 등록합니다.
	s.Identify.Intents = discordgo.IntentsGuildMessages | discordgo.IntentsDirectMessages | discordgo.IntentMessageContent | discordgo.IntentsGuilds

	ctx, cancel := context.WithCancel(context.Background())

	b := &Bot{
		Session:       s,
		Config:        cfg,
		Cron:          cron.New(),
		Ctx:           ctx,
		Cancel:        cancel,
		NewsChannelID: cfg.NewsChannelID,
		Cache:         make(map[string]CachedResult),
		WaitUpload:    make(map[string]string),
	}

	s.AddHandler(b.onReady)
	s.AddHandler(b.onInteractionCreate)
	s.AddHandler(b.onMessageCreate)

	return b, nil
}

func (b *Bot) Start() error {
	err := b.Session.Open()
	if err != nil {
		return fmt.Errorf("디스코드 연결 실패: %w", err)
	}

	// Register commands
	commands := []*discordgo.ApplicationCommand{
		{
			Name:        "뉴스생성",
			Description: "카드뉴스를 생성합니다.",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "주제",
					Description: "생성할 뉴스 주제 (비워두면 자동 선정)",
					Required:    false,
				},
			},
		},
		{
			Name:        "세팅",
			Description: "봇 설정을 변경합니다.",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "brave_api_key",
					Description: "Brave Search API Key",
					Required:    false,
				},
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "openrouter_api_key",
					Description: "OpenRouter API Key",
					Required:    false,
				},
			},
		},
		{
			Name:        "뉴스채널",
			Description: "스케줄된 자동 뉴스를 받을 채널을 현재 채널로 지정합니다.",
		},
	}

	for _, cmd := range commands {
		_, err := b.Session.ApplicationCommandCreate(b.Session.State.User.ID, "", cmd)
		if err != nil {
			log.Printf("명령어 %s 등록 실패: %v", cmd.Name, err)
		}
	}

	// 매일 오전 00:00에 실행 (서버 시간 기준)
	_, err = b.Cron.AddFunc("0 0 * * *", func() {
		b.generateScheduledNews()
	})
	if err != nil {
		return fmt.Errorf("크론 스케줄 등록 실패: %w", err)
	}
	b.Cron.Start()

	log.Println("Discord 봇이 시작되었습니다. CTRL-C를 눌러 종료하세요.")
	return nil
}

func (b *Bot) Stop() {
	b.Cancel()
	b.Cron.Stop()
	b.Session.Close()
}

func (b *Bot) onReady(s *discordgo.Session, event *discordgo.Ready) {
	log.Printf("봇이 %s#%s로 로그인했습니다!", s.State.User.Username, s.State.User.Discriminator)
}

func (b *Bot) onInteractionCreate(s *discordgo.Session, i *discordgo.InteractionCreate) {
	switch i.Type {
	case discordgo.InteractionApplicationCommand:
		b.handleSlashCommand(s, i)
	case discordgo.InteractionMessageComponent:
		b.handleComponent(s, i)
	case discordgo.InteractionModalSubmit:
		b.handleModalSubmit(s, i)
	}
}

func (b *Bot) handleSlashCommand(s *discordgo.Session, i *discordgo.InteractionCreate) {
	cmdName := i.ApplicationCommandData().Name

	switch cmdName {
	case "뉴스생성":
		b.handleNewsCreate(s, i)
	case "세팅":
		b.handleSetting(s, i)
	case "뉴스채널":
		b.handleNewsChannel(s, i)
	}
}

func (b *Bot) sendOrEdit(channelID, content string, files []*discordgo.File, interaction *discordgo.Interaction, components ...discordgo.MessageComponent) {
	if interaction != nil {
		emptyAttachments := make([]*discordgo.MessageAttachment, 0)
		_, err := b.Session.InteractionResponseEdit(interaction, &discordgo.WebhookEdit{
			Content:     &content,
			Files:       files,
			Components:  &components,
			Attachments: &emptyAttachments,
		})
		if err != nil {
			log.Printf("Interaction Edit 실패: %v", err)
		}
	} else {
		_, err := b.Session.ChannelMessageSendComplex(channelID, &discordgo.MessageSend{
			Content:    content,
			Files:      files,
			Components: components,
		})
		if err != nil {
			log.Printf("Message Send 실패: %v", err)
		}
	}
}

func (b *Bot) onMessageCreate(s *discordgo.Session, m *discordgo.MessageCreate) {
	if m.Author.ID == s.State.User.ID {
		return
	}

	b.mu.Lock()
	resultID, waiting := b.WaitUpload[m.Author.ID]
	cached, hasCache := b.Cache[resultID]
	b.mu.Unlock()

	// 디버그 용도의 로그 기록
	log.Printf("[DEBUG] 메시지 수신: 채널=%s, 작성자=%s (%s), 내용=%q, 첨부파일수=%d, 대기여부=%v, 캐시보유=%v",
		m.ChannelID, m.Author.Username, m.Author.ID, m.Content, len(m.Attachments), waiting, hasCache)

	if waiting && hasCache {
		if len(m.Attachments) > 0 {
			att := m.Attachments[0]
			
			// ContentType 검사 외에도 파일 확장자로 이미지 판단 여부 보강
			contentType := strings.ToLower(att.ContentType)
			isImage := strings.HasPrefix(contentType, "image/")
			if !isImage {
				ext := strings.ToLower(filepath.Ext(att.Filename))
				if ext == ".png" || ext == ".jpg" || ext == ".jpeg" || ext == ".webp" || ext == ".gif" {
					isImage = true
				}
			}

			if isImage {
				_, sendErr := s.ChannelMessageSend(m.ChannelID, "이미지를 다운로드하여 카드를 생성 중입니다...")
				if sendErr != nil {
					log.Printf("[ERROR] 진행 상황 메시지 발송 실패: %v", sendErr)
				}
				
				// 대기열 상태 정리
				b.mu.Lock()
				delete(b.WaitUpload, m.Author.ID)
				b.mu.Unlock()

				go func() {
					// 새로운 커스텀 출력 디렉토리 생성
					outBase := filepath.Join("output", fmt.Sprintf("discord_custom_%d", time.Now().Unix()))
					if err := os.MkdirAll(outBase, 0755); err != nil {
						log.Printf("[ERROR] 커스텀 출력 폴더 생성 실패: %v", err)
						s.ChannelMessageSend(m.ChannelID, "❌ 폴더 생성 중 오류가 발생했습니다.")
						return
					}
					
					customBgPath := filepath.Join(outBase, "custom_bg.jpg")
					req, err := http.NewRequestWithContext(b.Ctx, "GET", att.URL, nil)
					if err != nil {
						log.Printf("[ERROR] 다운로드 요청 생성 실패: %v", err)
						s.ChannelMessageSend(m.ChannelID, "❌ 이미지 요청 생성 중 오류가 발생했습니다.")
						return
					}
					
					resp, err := http.DefaultClient.Do(req)
					if err != nil {
						log.Printf("[ERROR] 이미지 파일 다운로드 실패: %v", err)
						s.ChannelMessageSend(m.ChannelID, "❌ 이미지를 다운로드하지 못했습니다.")
						return
					}
					defer resp.Body.Close()

					if resp.StatusCode != http.StatusOK {
						log.Printf("[ERROR] 이미지 다운로드 HTTP 에러: %d", resp.StatusCode)
						s.ChannelMessageSend(m.ChannelID, "❌ 이미지를 다운로드하지 못했습니다 (서버 HTTP 에러).")
						return
					}
					
					out, err := os.Create(customBgPath)
					if err != nil {
						log.Printf("[ERROR] 다운로드할 빈 파일 생성 실패: %v", err)
						s.ChannelMessageSend(m.ChannelID, "❌ 이미지 파일 생성 중 오류가 발생했습니다.")
						return
					}
					
					if _, err := io.Copy(out, resp.Body); err != nil {
						out.Close()
						log.Printf("[ERROR] 이미지 파일 작성 실패: %v", err)
						s.ChannelMessageSend(m.ChannelID, "❌ 이미지를 디스크에 쓰는 도중 실패했습니다.")
						return
					}
					out.Close()

					outDir := filepath.Join(outBase, "variation_custom")
					
					err = renderer.RenderCards(b.Ctx, cached.Result.Cards, outDir, customBgPath)
					if err != nil {
						log.Printf("[ERROR] 카드뉴스 렌더링 실패: %v", err)
						s.ChannelMessageSend(m.ChannelID, "❌ 카드뉴스 렌더링 중 오류가 발생했습니다: "+err.Error())
						return
					}

					// 커스텀 variation 한 개만 결과 목록에 반영
					cached.Result.OutputDirs = []string{outDir}
					b.sendUpdatedCards(m.ChannelID, cached.Result, nil)
				}()
			} else {
				log.Printf("[WARNING] 대기 상태에서 이미지가 아닌 파일(%s, ContentType: %s)이 전송되었습니다.", att.Filename, att.ContentType)
			}
		} else {
			log.Printf("[WARNING] 대기 상태에서 첨부파일이 없는 메시지가 왔습니다: %q", m.Content)
		}
	}
}
