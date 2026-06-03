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

func (b *Bot) handleNewsChannel(s *discordgo.Session, i *discordgo.InteractionCreate) {
	err := config.UpdateConfig(b.Ctx, func(cfg *config.Config) {
		b.mu.Lock()
		b.NewsChannelID = i.ChannelID
		cfg.NewsChannelID = i.ChannelID
		b.mu.Unlock()
	})

	resp := "이 채널이 자동 뉴스 채널로 지정되었습니다."
	if err != nil {
		resp = "설정 실패: " + err.Error()
	}

	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: resp,
		},
	})
}

func (b *Bot) handleSetting(s *discordgo.Session, i *discordgo.InteractionCreate) {
	opts := i.ApplicationCommandData().Options
	
	var msg []string
	
	err := config.UpdateConfig(b.Ctx, func(cfg *config.Config) {
		for _, opt := range opts {
			switch opt.Name {
			case "brave_api_key":
				cfg.BraveAPIKey = opt.StringValue()
				msg = append(msg, "Brave API Key 갱신됨")
			case "openrouter_api_key":
				cfg.OpenRouterAPIKey = opt.StringValue()
				msg = append(msg, "OpenRouter API Key 갱신됨")
			}
		}
	})

	resp := "설정이 변경되었습니다.\n" + strings.Join(msg, "\n")
	if err != nil {
		resp = "설정 변경 실패: " + err.Error()
	}

	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: resp,
			Flags:   discordgo.MessageFlagsEphemeral,
		},
	})
}

func (b *Bot) handleNewsCreate(s *discordgo.Session, i *discordgo.InteractionCreate) {
	// 먼저 defer response 전송
	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
	})

	var topic string
	opts := i.ApplicationCommandData().Options
	if len(opts) > 0 {
		topic = opts[0].StringValue()
	}

	go b.generateAndSend(i.ChannelID, topic, i.Interaction)
}

func (b *Bot) generateScheduledNews() {
	b.mu.Lock()
	ch := b.NewsChannelID
	b.mu.Unlock()

	if ch == "" {
		log.Println("스케줄된 뉴스: 알림 채널이 설정되어 있지 않습니다.")
		return
	}
	
	b.Session.ChannelMessageSend(ch, "🔔 **오늘의 뉴스 생성을 시작합니다...**")
	b.generateAndSend(ch, "", nil)
}

func (b *Bot) generateAndSend(channelID string, topic string, interaction *discordgo.Interaction) {
	outDir := filepath.Join("output", fmt.Sprintf("discord_%d", time.Now().Unix()))
	
	logFunc := func(msg string) {
		b.mu.Lock()
		targetChannel := b.NewsChannelID
		b.mu.Unlock()
		
		if targetChannel == "" {
			targetChannel = channelID // fallback to current channel if no news channel is set
		}
		
		// Discord limits messages to 2000 chars.
		if len(msg) > 1990 {
			msg = msg[:1980] + "\n```" // Truncate and close markdown block safely
		}
		b.Session.ChannelMessageSend(targetChannel, msg)
	}
	
	res, err := pipeline.Run(b.Ctx, b.Config, topic, outDir, topic == "", logFunc)
	
	if err != nil {
		msg := "뉴스 생성 중 오류가 발생했습니다: " + err.Error()
		b.sendOrEdit(channelID, msg, nil, interaction)
		return
	}

	// 기사 내용 텍스트화
	var bodyText strings.Builder
	bodyText.WriteString(fmt.Sprintf("# %s\n\n", res.Topic))
	for _, card := range res.Cards {
		bodyText.WriteString(fmt.Sprintf("**%s**\n%s\n\n", card.Title, card.Body))
	}

	// 이미지 파일 로드 (첫 번째 변형의 첫 번째 카드 표지)
	var files []*discordgo.File
	for v, dir := range res.OutputDirs {
		// 각 variation의 첫 페이지(표지)
		coverPath := filepath.Join(dir, "card_page_1.png")
		f, err := os.Open(coverPath)
		if err == nil {
			files = append(files, &discordgo.File{
				Name:        fmt.Sprintf("cover_var_%d.png", v+1),
				ContentType: "image/png",
				Reader:      f,
			})
		}
	}

	b.mu.Lock()
	resultID := fmt.Sprintf("%d", time.Now().UnixNano())
	b.Cache[resultID] = CachedResult{Result: res, Topic: res.Topic}
	b.mu.Unlock()

	// 컴포넌트 추가 (텍스트 변경, 이미지 다시 찾기, 이미지 직접 업로드)
	components := []discordgo.MessageComponent{
		discordgo.ActionsRow{
			Components: []discordgo.MessageComponent{
				discordgo.Button{
					Label:    "텍스트 변경",
					Style:    discordgo.SecondaryButton,
					CustomID: "text_edit:" + resultID,
				},
				discordgo.Button{
					Label:    "다른 이미지 찾기",
					Style:    discordgo.PrimaryButton,
					CustomID: "img_search:" + resultID,
				},
				discordgo.Button{
					Label:    "이미지 직접 업로드",
					Style:    discordgo.SecondaryButton,
					CustomID: "img_upload:" + resultID,
				},
			},
		},
	}

	msgContent := fmt.Sprintf("## 인스타그램 업로드용 기사 (%s)\n%s\n(제공된 표지 3개 중 하나를 선택해 주세요)", res.Topic, bodyText.String())
	if len(msgContent) > 2000 {
		msgContent = msgContent[:1990] + "..." // Discord limit
	}

	b.sendOrEdit(channelID, msgContent, files, interaction, components...)
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

func (b *Bot) handleComponent(s *discordgo.Session, i *discordgo.InteractionCreate) {
	id := i.MessageComponentData().CustomID

	if strings.HasPrefix(id, "text_edit:") {
		resultID := strings.TrimPrefix(id, "text_edit:")
		b.mu.Lock()
		cached, ok := b.Cache[resultID]
		b.mu.Unlock()
		
		if !ok || len(cached.Result.Cards) == 0 {
			s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
				Type: discordgo.InteractionResponseChannelMessageWithSource,
				Data: &discordgo.InteractionResponseData{
					Content: "캐시가 만료되었습니다. 새로 뉴스를 생성해주세요.",
					Flags:   discordgo.MessageFlagsEphemeral,
				},
			})
			return
		}

		s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseModal,
			Data: &discordgo.InteractionResponseData{
				CustomID: "text_modal:" + resultID,
				Title:    "텍스트 변경",
				Components: []discordgo.MessageComponent{
					discordgo.ActionsRow{
						Components: []discordgo.MessageComponent{
							discordgo.TextInput{
								CustomID: "title_input",
								Label:    "타이틀 (최대 15자)",
								Style:    discordgo.TextInputShort,
								Required: true,
								Value:    cached.Result.Cards[0].Title,
							},
						},
					},
					discordgo.ActionsRow{
						Components: []discordgo.MessageComponent{
							discordgo.TextInput{
								CustomID: "body_input",
								Label:    "서브타이틀/본문 (최대 50자)",
								Style:    discordgo.TextInputParagraph,
								Required: true,
								Value:    cached.Result.Cards[0].Body,
							},
						},
					},
				},
			},
		})
		return
	}



	if strings.HasPrefix(id, "img_search:") {
		resultID := strings.TrimPrefix(id, "img_search:")
		s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseDeferredMessageUpdate,
		})

		b.mu.Lock()
		cached, ok := b.Cache[resultID]
		b.mu.Unlock()
		if ok {
			// Pass interaction down so it overwrites the original message
			go b.generateAndSend(i.ChannelID, cached.Topic, i.Interaction)
		}
		return
	}

	if strings.HasPrefix(id, "img_upload:") {
		resultID := strings.TrimPrefix(id, "img_upload:")
		userID := ""
		if i.Member != nil {
			userID = i.Member.User.ID
		} else if i.User != nil {
			userID = i.User.ID
		}

		b.mu.Lock()
		b.WaitUpload[userID] = resultID
		b.mu.Unlock()

		s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "✅ 이 채널에 사용하실 이미지를 파일 첨부하여 전송해주세요! (현재 텍스트 유지)",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}
}

func (b *Bot) handleModalSubmit(s *discordgo.Session, i *discordgo.InteractionCreate) {
	id := i.ModalSubmitData().CustomID
	if strings.HasPrefix(id, "text_modal:") {
		resultID := strings.TrimPrefix(id, "text_modal:")
		
		data := i.ModalSubmitData()
		title := data.Components[0].(*discordgo.ActionsRow).Components[0].(*discordgo.TextInput).Value
		body := data.Components[1].(*discordgo.ActionsRow).Components[0].(*discordgo.TextInput).Value

		s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseDeferredMessageUpdate,
		})

		go func() {
			b.mu.Lock()
			cached, ok := b.Cache[resultID]
			b.mu.Unlock()

			if ok && len(cached.Result.Cards) > 0 {
				cached.Result.Cards[0].Title = title
				cached.Result.Cards[0].Body = body
				
				// Rerender the 3 variations locally
				for v, dir := range cached.Result.OutputDirs {
					bgPath := ""
					if v < len(cached.Result.BgImageURLs) && cached.Result.BgImageURLs[v] != "" {
						// Ensure we use the absolute or cleanly resolved path
						bgPath = filepath.Clean(filepath.Join(dir, "..", fmt.Sprintf("bg_%d.jpg", v+1)))
					}
					
					// If bg path doesn't exist, ignore and use default to prevent silent failure
					if bgPath != "" {
						if _, statErr := os.Stat(bgPath); os.IsNotExist(statErr) {
							log.Printf("배경 이미지 없음 (기본 배경 사용): %s", bgPath)
							bgPath = ""
						}
					}
					
					err := renderer.RenderCards(b.Ctx, cached.Result.Cards, dir, bgPath)
					if err != nil {
						log.Printf("변형 %d 렌더링 실패: %v", v+1, err)
					}
				}
				
				// Send updated message
				b.sendUpdatedCards(i.ChannelID, cached.Result, i.Interaction)
			}
		}()
	}
}

func (b *Bot) sendUpdatedCards(channelID string, res *pipeline.PipelineResult, interaction *discordgo.Interaction) {
	var bodyText strings.Builder
	bodyText.WriteString(fmt.Sprintf("# %s\n\n", res.Topic))
	for _, card := range res.Cards {
		bodyText.WriteString(fmt.Sprintf("**%s**\n%s\n\n", card.Title, card.Body))
	}

	var files []*discordgo.File
	for v, dir := range res.OutputDirs {
		coverPath := filepath.Join(dir, "card_page_1.png")
		f, err := os.Open(coverPath)
		if err == nil {
			files = append(files, &discordgo.File{
				Name:        fmt.Sprintf("cover_var_%d.png", v+1),
				ContentType: "image/png",
				Reader:      f,
			})
		}
	}

	b.mu.Lock()
	resultID := fmt.Sprintf("%d", time.Now().UnixNano())
	b.Cache[resultID] = CachedResult{Result: res, Topic: res.Topic}
	b.mu.Unlock()

	components := []discordgo.MessageComponent{
		discordgo.ActionsRow{
			Components: []discordgo.MessageComponent{
				discordgo.Button{
					Label:    "텍스트 변경",
					Style:    discordgo.SecondaryButton,
					CustomID: "text_edit:" + resultID,
				},
				discordgo.Button{
					Label:    "다른 이미지 찾기",
					Style:    discordgo.PrimaryButton,
					CustomID: "img_search:" + resultID,
				},
				discordgo.Button{
					Label:    "이미지 직접 업로드",
					Style:    discordgo.SecondaryButton,
					CustomID: "img_upload:" + resultID,
				},
			},
		},
	}

	msgContent := fmt.Sprintf("## 인스타그램 업로드용 기사 (%s)\n%s\n(제공된 표지 중 하나를 선택해 주세요)", res.Topic, bodyText.String())
	if len(msgContent) > 2000 {
		msgContent = msgContent[:1990] + "..."
	}

	b.sendOrEdit(channelID, msgContent, files, interaction, components...)
}

func (b *Bot) onMessageCreate(s *discordgo.Session, m *discordgo.MessageCreate) {
	if m.Author.ID == s.State.User.ID {
		return
	}

	b.mu.Lock()
	resultID, waiting := b.WaitUpload[m.Author.ID]
	cached, hasCache := b.Cache[resultID]
	b.mu.Unlock()

	if waiting && hasCache {
		if len(m.Attachments) > 0 {
			att := m.Attachments[0]
			if strings.HasPrefix(att.ContentType, "image/") {
				s.ChannelMessageSend(m.ChannelID, "이미지를 다운로드하여 카드를 생성 중입니다...")
				
				// Clean up wait state
				b.mu.Lock()
				delete(b.WaitUpload, m.Author.ID)
				b.mu.Unlock()

				go func() {
					// Create a new custom output dir
					outBase := filepath.Join("output", fmt.Sprintf("discord_custom_%d", time.Now().Unix()))
					os.MkdirAll(outBase, 0755)
					
					customBgPath := filepath.Join(outBase, "custom_bg.jpg")
					req, _ := http.NewRequestWithContext(b.Ctx, "GET", att.URL, nil)
					resp, err := http.DefaultClient.Do(req)
					if err == nil {
						defer resp.Body.Close()
						out, _ := os.Create(customBgPath)
						io.Copy(out, resp.Body)
						out.Close()
					}

					outDir := filepath.Join(outBase, "variation_custom")
					
					renderer.RenderCards(b.Ctx, cached.Result.Cards, outDir, customBgPath)

					// Update Result to only have this one custom variation
					cached.Result.OutputDirs = []string{outDir}
					b.sendUpdatedCards(m.ChannelID, cached.Result, nil)
				}()
			}
		}
	}
}
