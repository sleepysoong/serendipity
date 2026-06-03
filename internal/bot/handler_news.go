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
		
		runes := []rune(msg)
		chunkSize := 1900
		
		for i := 0; i < len(runes); i += chunkSize {
			end := i + chunkSize
			if end > len(runes) {
				end = len(runes)
			}
			
			chunk := string(runes[i:end])
			
			// If message is split, add markdown codeblock fences to preserve formatting roughly
			if i > 0 && !strings.HasPrefix(chunk, "```") {
				chunk = "```\n" + chunk
			}
			if end < len(runes) && !strings.HasSuffix(chunk, "```") && !strings.HasSuffix(chunk, "```\n") {
				chunk = chunk + "\n```"
			}
			
			var err error
			// 상호작용(슬래시 명령)이 존재하고 로그 타겟 채널이 명령이 내려진 채널과 동일한 경우,
			// 채널 메시지 전송 권한(403 Missing Access 등) 문제를 우회하기 위해 Interaction Followup을 우선 사용합니다.
			if interaction != nil && targetChannel == channelID {
				_, err = b.Session.FollowupMessageCreate(interaction, true, &discordgo.WebhookParams{
					Content: chunk,
				})
			} else {
				_, err = b.Session.ChannelMessageSend(targetChannel, chunk)
			}
			
			if err != nil {
				log.Printf("디스코드 로그 전송 실패 (채널 %s): %v", targetChannel, err)
				// 만약 실패했고 interaction이 존재한다면 최후의 수단으로 interaction followup을 통해 보냅니다.
				if interaction != nil {
					log.Printf("Interaction Followup을 통해 로그 전송 폴백 시도...")
					_, fallbackErr := b.Session.FollowupMessageCreate(interaction, true, &discordgo.WebhookParams{
						Content: "[로그 폴백] " + chunk,
					})
					if fallbackErr != nil {
						log.Printf("Interaction Followup 폴백도 실패: %v", fallbackErr)
					}
				} else if targetChannel != channelID && channelID != "" {
					log.Printf("현재 채널(%s)로 로그 전송 폴백 시도...", channelID)
					_, fallbackErr := b.Session.ChannelMessageSend(channelID, "[로그 폴백] "+chunk)
					if fallbackErr != nil {
						log.Printf("현재 채널(%s)로의 로그 폴백 전송도 실패: %v", channelID, fallbackErr)
					}
				}
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

	// 이미지 파일 로드 (첫 번째 변형의 첫 번째 카드 표지)
	// 디스크 캐싱 방지를 위해 파일명에 타임스탬프를 부여합니다.
	var files []*discordgo.File
	nowTs := time.Now().Unix()
	for v, dir := range res.OutputDirs {
		// 각 variation의 첫 페이지(표지)
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

	warningMsg := ""
	perms, permErr := b.Session.UserChannelPermissions(b.Session.State.User.ID, channelID)
	if permErr != nil || (perms&discordgo.PermissionViewChannel) == 0 || (perms&discordgo.PermissionSendMessages) == 0 {
		warningMsg = "\n\n⚠️ **[권한 경고] 봇의 채널 권한 설정이 필요합니다!**\n현재 봇에게 이 채널의 **'채널 보기(View Channel)'** 및 **'메시지 보내기(Send Messages)'** 권한이 부여되지 않았습니다.\n이 권한이 없으면 **로그 전송** 및 **이미지 직접 업로드** 기능이 작동할 수 없습니다. 서버 설정에서 봇에게 해당 권한을 꼭 부여해 주세요!"
	}

	msgContent := fmt.Sprintf("## 인스타그램 업로드용 기사 (%s)\n%s\n(제공된 표지 3개 중 하나를 선택해 주세요)%s", res.Topic, bodyText.String(), warningMsg)
	if len(msgContent) > 2000 {
		msgContent = msgContent[:1990] + "..." // Discord limit
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

	warningMsg := ""
	perms, permErr := b.Session.UserChannelPermissions(b.Session.State.User.ID, channelID)
	if permErr != nil || (perms&discordgo.PermissionViewChannel) == 0 || (perms&discordgo.PermissionSendMessages) == 0 {
		warningMsg = "\n\n⚠️ **[권한 경고] 봇의 채널 권한 설정이 필요합니다!**\n현재 봇에게 이 채널의 **'채널 보기(View Channel)'** 및 **'메시지 보내기(Send Messages)'** 권한이 부여되지 않았습니다.\n이 권한이 없으면 **로그 전송** 및 **이미지 직접 업로드** 기능이 작동할 수 없습니다. 서버 설정에서 봇에게 해당 권한을 꼭 부여해 주세요!"
	}

	msgContent := fmt.Sprintf("## 인스타그램 업로드용 기사 (%s)\n%s\n(제공된 표지 중 하나를 선택해 주세요)%s", res.Topic, bodyText.String(), warningMsg)
	if len(msgContent) > 2000 {
		msgContent = msgContent[:1990] + "..."
	}

	b.sendOrEdit(channelID, msgContent, files, interaction, components...)
}
