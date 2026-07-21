package server

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/handler"
	"github.com/disgoorg/omit"
	"github.com/disgoorg/snowflake/v2"
)

const (
	nicknameMaxLength = 32

	customIDNicknameOpen  = "/nickname/open"
	customIDNicknameModal = "/nickname/modal"

	fieldTrainerName = "trainername"
	fieldFlag        = "flag"
	fieldCommunity   = "community"
)

func nicknameCommands() []discord.ApplicationCommandCreate {
	return []discord.ApplicationCommandCreate{
		discord.SlashCommandCreate{
			Name:        "set-nickname",
			Description: "Set your server nickname (trainer name, flag, community)",
			Contexts: []discord.InteractionContextType{
				discord.InteractionContextTypeGuild,
			},
		},
		discord.SlashCommandCreate{
			Name:                     "post-nickname-panel",
			Description:              "Post a message with a button to set nicknames",
			DefaultMemberPermissions: omit.NewPtr(discord.PermissionManageGuild),
			Contexts: []discord.InteractionContextType{
				discord.InteractionContextTypeGuild,
			},
		},
	}
}

func (s *Server) registerNicknameHandlers(r *handler.Mux) {
	r.SlashCommand("/set-nickname", s.handleSetNicknameCommand)
	r.SlashCommand("/post-nickname-panel", s.handlePostNicknamePanel)
	r.ButtonComponent(customIDNicknameOpen, s.handleNicknameOpenButton)
	r.Modal(customIDNicknameModal, s.handleNicknameModalSubmit)
}

func nicknameModal() discord.ModalCreate {
	return discord.NewModalCreate(customIDNicknameModal, "Set nickname").
		AddLabel("Trainer name", discord.NewShortTextInput(fieldTrainerName).
			WithRequired(true).
			WithMinLength(1).
			WithMaxLength(32).
			WithPlaceholder("Your in-game trainer name")).
		AddLabel("Flag", discord.NewShortTextInput(fieldFlag).
			WithRequired(true).
			WithMinLength(1).
			WithMaxLength(8).
			WithPlaceholder("e.g. 🇩🇪")).
		AddLabel("Community", discord.NewShortTextInput(fieldCommunity).
			WithRequired(true).
			WithMinLength(1).
			WithMaxLength(32).
			WithPlaceholder("Your community name"))
}

func formatNickname(trainerName, flag, community string) (string, error) {
	trainerName = strings.TrimSpace(trainerName)
	flag = strings.TrimSpace(flag)
	community = strings.TrimSpace(community)

	if trainerName == "" || flag == "" || community == "" {
		return "", fmt.Errorf("trainer name, flag, and community are required")
	}

	// Budget for trainer + community after flag and the two separating spaces.
	budget := nicknameMaxLength - utf8.RuneCountInString(flag) - 2
	if budget < 2 {
		return "", fmt.Errorf("flag is too long to fit trainer name and community (max %d characters)", nicknameMaxLength)
	}

	trainerRunes := []rune(trainerName)
	communityRunes := []rune(community)

	if len(trainerRunes)+len(communityRunes) > budget {
		// Shorten community first.
		communityMax := budget - len(trainerRunes)
		if communityMax < 1 {
			// Shorten trainer too, keeping at least 1 character for community.
			trainerMax := budget - 1
			if trainerMax < 1 {
				return "", fmt.Errorf("flag leaves no room for trainer name and community (max %d characters)", nicknameMaxLength)
			}
			trainerRunes = trainerRunes[:trainerMax]
			communityMax = budget - len(trainerRunes)
		}
		if len(communityRunes) > communityMax {
			communityRunes = communityRunes[:communityMax]
		}
	}

	trainerName = strings.TrimSpace(string(trainerRunes))
	community = strings.TrimSpace(string(communityRunes))
	if trainerName == "" || community == "" {
		return "", fmt.Errorf("nickname parts cannot fit within %d characters", nicknameMaxLength)
	}

	return trainerName + " " + flag + " " + community, nil
}

func (s *Server) applyNickname(guildID, userID snowflake.ID, nick string) error {
	_, err := s.Client.Rest.UpdateMember(guildID, userID, discord.MemberUpdate{
		Nick: &nick,
	})
	return err
}

func (s *Server) requireConfiguredGuild(guildID *snowflake.ID) error {
	if guildID == nil || *guildID != s.Cfg.Bot.GuildID {
		return fmt.Errorf("this command can only be used in the configured server")
	}
	return nil
}

func (s *Server) handleSetNicknameCommand(_ discord.SlashCommandInteractionData, e *handler.CommandEvent) error {
	if err := s.requireConfiguredGuild(e.GuildID()); err != nil {
		return e.CreateMessage(discord.NewMessageCreate().
			WithContent(err.Error()).
			WithEphemeral(true))
	}
	return e.Modal(nicknameModal())
}

func (s *Server) handlePostNicknamePanel(_ discord.SlashCommandInteractionData, e *handler.CommandEvent) error {
	if err := s.requireConfiguredGuild(e.GuildID()); err != nil {
		return e.CreateMessage(discord.NewMessageCreate().
			WithContent(err.Error()).
			WithEphemeral(true))
	}

	_, err := s.Client.Rest.CreateMessage(e.Channel().ID(), discord.NewMessageCreateV2(
		discord.NewContainer(
			discord.NewTextDisplay(renderText("nickname_panel")),
			discord.NewActionRow(
				discord.NewPrimaryButton("Set nickname", customIDNicknameOpen),
			),
		).WithAccentColor(0x5865F2),
	))
	if err != nil {
		return e.CreateMessage(discord.NewMessageCreate().
			WithContent("Failed to post nickname panel: " + err.Error()).
			WithEphemeral(true))
	}

	return e.CreateMessage(discord.NewMessageCreate().
		WithContent("Nickname panel posted.").
		WithEphemeral(true))
}

func (s *Server) handleNicknameOpenButton(_ discord.ButtonInteractionData, e *handler.ComponentEvent) error {
	return e.Modal(nicknameModal())
}

func (s *Server) handleNicknameModalSubmit(e *handler.ModalEvent) error {
	nick, err := formatNickname(
		e.Data.Text(fieldTrainerName),
		e.Data.Text(fieldFlag),
		e.Data.Text(fieldCommunity),
	)
	if err != nil {
		return e.CreateMessage(discord.NewMessageCreate().
			WithContent(err.Error()).
			WithEphemeral(true))
	}

	user := e.User()
	if err = s.applyNickname(s.Cfg.Bot.GuildID, user.ID, nick); err != nil {
		return e.CreateMessage(discord.NewMessageCreate().
			WithContent("Failed to set nickname: " + err.Error()).
			WithEphemeral(true))
	}

	return e.CreateMessage(discord.NewMessageCreate().
		WithContent(fmt.Sprintf("Your nickname is now **%s**.", nick)).
		WithEphemeral(true))
}
