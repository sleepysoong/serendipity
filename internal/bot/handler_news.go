package bot

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	"serendipity/internal/pipeline"
)

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
	
	// 스케줄 뉴스는 채널 권한 확인 후 전송 시도
	_, err := b.Session.ChannelMessageSend(ch, "🔔 **오늘의 뉴스 생성을 시작합니다...**")
	if err != nil {
		log.Printf("[ERROR] 스케줄 뉴스 채널(%s) 접근 불가: %v — /뉴스채널 로 다시 설정 필요", ch, err)
		return
	}
	b.generateAndSend(ch, "", nil)
}

func (b *Bot) generateAndSend(channelID string, topic string, interaction *discordgo.Interaction) {
	outDir := filepath.Join("output", fmt.Sprintf("discord_%d", time.Now().Unix()))
	
	// 로그 전송 함수:
	// interaction이 있으면 → 무조건 FollowupMessageCreate 사용 (403 우회)
	// interaction이 없으면(스케줄) → ChannelMessageSend 사용
	logFunc := func(msg string) {
		runes := []rune(msg)
		chunkSize := 1900
		
		for i := 0; i < len(runes); i += chunkSize {
			end := i + chunkSize
			if end > len(runes) {
				end = len(runes)
			}
			
			chunk := string(runes[i:end])
			
			if i > 0 && !strings.HasPrefix(chunk, "```") {
				chunk = "```\n" + chunk
			}
			if end < len(runes) && !strings.HasSuffix(chunk, "```") && !strings.HasSuffix(chunk, "```\n") {
				chunk = chunk + "\n```"
			}
			
			var err error
			if interaction != nil {
				// 슬래시 명령 → 무조건 Followup 사용 (채널 권한 불필요)
				_, err = b.Session.FollowupMessageCreate(interaction, true, &discordgo.WebhookParams{
					Content: chunk,
				})
			} else {
				// 스케줄 → 일반 메시지
				_, err = b.Session.ChannelMessageSend(channelID, chunk)
			}
			
			if err != nil {
				log.Printf("디스코드 로그 전송 실패: %v", err)
			}
		}
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

	// 이미지 파일 로드
	var files []*discordgo.File
	nowTs := time.Now().Unix()
	for v, dir := range res.OutputDirs {
		coverPath := filepath.Join(dir, "card_page_1.png")
		f, err := os.Open(coverPath)
		if err == nil {
			files = append(files, &discordgo.File{
				Name:        fmt.Sprintf("cover_var_%d_%d.png", v+1, nowTs),
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

	msgContent := fmt.Sprintf("## 인스타그램 업로드용 기사 (%s)\n%s\n(제공된 표지 3개 중 하나를 선택해 주세요)", res.Topic, bodyText.String())
	if len(msgContent) > 2000 {
		msgContent = msgContent[:1990] + "..."
	}

	b.sendOrEdit(channelID, msgContent, files, interaction, components...)
}

func (b *Bot) sendUpdatedCards(channelID string, res *pipeline.PipelineResult, interaction *discordgo.Interaction) {
	var bodyText strings.Builder
	bodyText.WriteString(fmt.Sprintf("# %s\n\n", res.Topic))
	for _, card := range res.Cards {
		bodyText.WriteString(fmt.Sprintf("**%s**\n%s\n\n", card.Title, card.Body))
	}

	var files []*discordgo.File
	nowTs := time.Now().Unix()
	for v, dir := range res.OutputDirs {
		coverPath := filepath.Join(dir, "card_page_1.png")
		f, err := os.Open(coverPath)
		if err == nil {
			files = append(files, &discordgo.File{
				Name:        fmt.Sprintf("cover_var_%d_%d.png", v+1, nowTs),
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
