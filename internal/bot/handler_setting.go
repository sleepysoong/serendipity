package bot

import (
	"fmt"
	"strings"

	"github.com/bwmarrin/discordgo"
	"serendipity/internal/config"
)

func (b *Bot) handleNewsChannel(s *discordgo.Session, i *discordgo.InteractionCreate) {
	// 먼저 봇이 이 채널에 메시지를 보낼 수 있는지 실제로 확인합니다.
	perms, permErr := s.UserChannelPermissions(s.State.User.ID, i.ChannelID)
	
	hasView := permErr == nil && (perms&discordgo.PermissionViewChannel) != 0
	hasSend := permErr == nil && (perms&discordgo.PermissionSendMessages) != 0
	
	if !hasView || !hasSend {
		missing := []string{}
		if !hasView {
			missing = append(missing, "'채널 보기(View Channel)'")
		}
		if !hasSend {
			missing = append(missing, "'메시지 보내기(Send Messages)'")
		}
		
		resp := fmt.Sprintf("❌ **이 채널을 뉴스 채널로 지정할 수 없습니다.**\n\n봇에게 이 채널의 %s 권한이 없습니다.\n\n**해결 방법:**\n1. 서버 설정 → 역할(Roles) → 봇 역할 선택\n2. 이 채널의 권한 설정에서 위 권한을 ✅ 허용으로 변경\n3. 다시 `/뉴스채널` 명령어를 실행해 주세요.",
			strings.Join(missing, " 및 "))
		
		s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: resp,
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}
	
	// 실제로 메시지를 보내서 확인
	testMsg, testErr := s.ChannelMessageSend(i.ChannelID, "✅ 봇 권한 확인 완료! 이 채널을 뉴스 채널로 설정합니다.")
	if testErr != nil {
		s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: fmt.Sprintf("❌ **이 채널에 메시지를 보낼 수 없습니다.**\n에러: `%s`\n\n서버 설정에서 봇에게 이 채널의 '메시지 보내기' 권한을 부여해 주세요.", testErr.Error()),
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}
	
	// 테스트 메시지 삭제
	s.ChannelMessageDelete(i.ChannelID, testMsg.ID)

	err := config.UpdateConfig(b.Ctx, func(cfg *config.Config) {
		b.mu.Lock()
		b.NewsChannelID = i.ChannelID
		cfg.NewsChannelID = i.ChannelID
		b.mu.Unlock()
	})

	resp := "✅ 이 채널이 자동 뉴스 채널로 지정되었습니다. (봇 권한 확인 완료)"
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
