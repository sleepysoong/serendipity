package bot

import (
	"strings"

	"github.com/bwmarrin/discordgo"
	"serendipity/internal/config"
)

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
