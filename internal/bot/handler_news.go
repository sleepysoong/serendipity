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

// createLogThread는 일반 ChannelMessageSend로 메시지를 보내고,
// 그 메시지에 스레드를 생성합니다. Followup 메시지에는 스레드를 만들 수 없으므로
// 반드시 일반 메시지를 사용합니다.
func (b *Bot) createLogThread(channelID string) string {
	threadName := fmt.Sprintf("📋 뉴스 생성 로그 (%s)", time.Now().Format("15:04"))

	// 일반 메시지로 시작 알림 전송
	startMsg, err := b.Session.ChannelMessageSend(channelID, "🔄 **뉴스 생성을 시작합니다...**\n_아래 스레드에서 실시간 로그를 확인하세요._")
	if err != nil {
		log.Printf("로그 시작 메시지 전송 실패: %v", err)
		return ""
	}

	// 시작 메시지에 스레드 생성
	thread, err := b.Session.MessageThreadStart(channelID, startMsg.ID, threadName, 60)
	if err != nil {
		log.Printf("스레드 생성 실패: %v", err)
		return ""
	}

	log.Printf("로그 스레드 생성 완료: %s (ID: %s)", threadName, thread.ID)
	return thread.ID
}

func (b *Bot) generateAndSend(channelID string, topic string, interaction *discordgo.Interaction) {
	outDir := filepath.Join("output", fmt.Sprintf("discord_%d", time.Now().Unix()))

	// 현재 채널이 이미 스레드인지 확인
	isThread := false
	if ch, err := b.Session.Channel(channelID); err == nil {
		if ch.Type == discordgo.ChannelTypeGuildPublicThread ||
			ch.Type == discordgo.ChannelTypeGuildPrivateThread ||
			ch.Type == discordgo.ChannelTypeGuildNewsThread {
			isThread = true
		}
	}

	var logThreadID string
	if isThread {
		// 이미 스레드 내부인 경우 기존 스레드를 계속 사용
		logThreadID = channelID
		// 재진행 알림
		_, _ = b.Session.ChannelMessageSend(logThreadID, "🔄 **새로운 이미지를 검색하여 뉴스 카드를 다시 생성 중입니다...**")
	} else {
		// 메인 채널인 경우 새로운 로그 스레드 생성
		logThreadID = b.createLogThread(channelID)

		// 메인 채널의 Slash Command Interaction이 있는 경우,
		// 메인 채널의 interaction response를 스레드 생성 안내 메시지로 변경해 둠
		if interaction != nil {
			statusMsg := "🔄 **뉴스 생성을 시작합니다...**\n생성 진행 상황과 최종 결과물은 아래 생성된 스레드에서 확인해 주세요!"
			_, err := b.Session.InteractionResponseEdit(interaction, &discordgo.WebhookEdit{
				Content: &statusMsg,
			})
			if err != nil {
				log.Printf("Interaction 초기 Status Edit 실패: %v", err)
			}
		}
	}

	// 로그 전송 함수:
	// 1순위: 스레드가 있으면 → 스레드에 전송 (채널 깨끗하게 유지)
	// 2순위: interaction이 있으면 → FollowupMessageCreate (폴백)
	// 3순위: 스케줄 → ChannelMessageSend (폴백)
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
			if logThreadID != "" {
				// 스레드에 전송 (메인 채널 깨끗하게 유지)
				_, err = b.Session.ChannelMessageSend(logThreadID, chunk)
			} else if interaction != nil {
				// 스레드 없으면 Followup으로 폴백
				_, err = b.Session.FollowupMessageCreate(interaction, true, &discordgo.WebhookParams{
					Content: chunk,
				})
			} else {
				// 스케줄 → 일반 메시지
				_, err = b.Session.ChannelMessageSend(channelID, chunk)
			}

			if err != nil {
				log.Printf("로그 전송 실패: %v", err)
			}
		}
	}

	res, err := pipeline.Run(b.Ctx, b.Config, topic, outDir, topic == "", logFunc)

	// 스레드에 완료/에러 메시지 남기기
	if logThreadID != "" {
		if err != nil {
			_, _ = b.Session.ChannelMessageSend(logThreadID, fmt.Sprintf("❌ **생성 실패:** %s", err.Error()))
		} else {
			_, _ = b.Session.ChannelMessageSend(logThreadID, "✅ **뉴스 생성 완료!**")
		}
	}

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

	// 전송 또는 업데이트
	if !isThread && logThreadID != "" {
		// 메인 채널에서 최초 생성 후, 최종 결과물(인터랙션 메시지)을 메인 채널이 아니라 새로 생성된 스레드로 전송!
		_, errMsg := b.Session.ChannelMessageSendComplex(logThreadID, &discordgo.MessageSend{
			Content:    msgContent,
			Files:      files,
			Components: components,
		})
		if errMsg != nil {
			log.Printf("스레드에 최종 결과물 전송 실패: %v", errMsg)
			// 폴백으로 메인 채널에 전송 시도
			b.sendOrEdit(channelID, msgContent, files, interaction, components...)
		} else {
			// 메인 채널의 Slash Command Interaction 완료 처리
			if interaction != nil {
				doneMsg := "✅ **뉴스 생성이 완료되었습니다!** 아래 생성된 스레드에서 결과물을 확인해 주세요."
				_, errEdit := b.Session.InteractionResponseEdit(interaction, &discordgo.WebhookEdit{
					Content: &doneMsg,
				})
				if errEdit != nil {
					log.Printf("Interaction 완료 업데이트 실패: %v", errEdit)
				}
			}
		}
	} else {
		// 이미 스레드 내부이거나 스레드 생성을 하지 않은 경우 (예: 스레드 내부에서 '다른 이미지 찾기'를 눌러 interaction을 edit해야 하는 경우)
		b.sendOrEdit(channelID, msgContent, files, interaction, components...)
	}
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
