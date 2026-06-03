package bot

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/robfig/cron/v3"
	
	"serendipity/internal/config"
	"serendipity/internal/pipeline"
)

type Bot struct {
	Session *discordgo.Session
	Config  *config.Config
	Cron    *cron.Cron
	Ctx     context.Context
	Cancel  context.CancelFunc
	
	// channel ID for scheduled news
	NewsChannelID string 
	
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
		Session: s,
		Config:  cfg,
		Cron:    cron.New(),
		Ctx:     ctx,
		Cancel:  cancel,
	}

	s.AddHandler(b.onReady)
	s.AddHandler(b.onInteractionCreate)

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
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "news_channel",
					Description: "자동 생성된 뉴스를 받을 채널 ID",
					Required:    false,
				},
			},
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
	}
}

func (b *Bot) handleSlashCommand(s *discordgo.Session, i *discordgo.InteractionCreate) {
	cmdName := i.ApplicationCommandData().Name

	switch cmdName {
	case "뉴스생성":
		b.handleNewsCreate(s, i)
	case "세팅":
		b.handleSetting(s, i)
	}
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
			case "news_channel":
				b.mu.Lock()
				b.NewsChannelID = opt.StringValue()
				b.mu.Unlock()
				msg = append(msg, "뉴스 알림 채널이 설정됨: "+opt.StringValue())
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
	
	res, err := pipeline.Run(b.Ctx, b.Config, topic, outDir, topic == "")
	
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

	// 컴포넌트 추가 (이미지 변경, 타이틀 변경 버튼)
	components := []discordgo.MessageComponent{
		discordgo.ActionsRow{
			Components: []discordgo.MessageComponent{
				discordgo.Button{
					Label:    "다른 이미지로 재생성",
					Style:    discordgo.PrimaryButton,
					CustomID: "regen_image:" + res.Topic,
				},
				discordgo.Button{
					Label:    "타이틀 재생성",
					Style:    discordgo.SecondaryButton,
					CustomID: "regen_title:" + res.Topic,
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
		_, err := b.Session.InteractionResponseEdit(interaction, &discordgo.WebhookEdit{
			Content:    &content,
			Files:      files,
			Components: &components,
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
	
	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: "요청을 처리중입니다...",
			Flags:   discordgo.MessageFlagsEphemeral,
		},
	})
	
	if strings.HasPrefix(id, "regen_image:") {
		topic := strings.TrimPrefix(id, "regen_image:")
		// 새로운 변형만 생성하는 로직이 필요하지만 현재는 전체 재생성으로 단순화
		go b.generateAndSend(i.ChannelID, topic, nil)
	} else if strings.HasPrefix(id, "regen_title:") {
		topic := strings.TrimPrefix(id, "regen_title:")
		go b.generateAndSend(i.ChannelID, topic, nil)
	}
}
