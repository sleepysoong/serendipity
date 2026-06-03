package bot

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/bwmarrin/discordgo"
	"serendipity/internal/renderer"
)

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
