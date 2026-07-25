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
		AddComponents(discord.NewLabel("Community", discord.NewShortTextInput(fieldCommunity).
			WithRequired(true).
			WithMinLength(1).
			WithMaxLength(32).
			WithPlaceholder("Your community name")).
			WithDescription(renderText("nickname_community_hint")))
}

func formatNickname(trainerName, flag, community string) (string, error) {
	trainerName = strings.TrimSpace(trainerName)
	flag = strings.TrimSpace(flag)
	community = strings.TrimSpace(community)

	if trainerName == "" || flag == "" || community == "" {
		return "", fmt.Errorf("%s", renderText("nickname_required"))
	}

	nick := trainerName + flag + community
	if utf8.RuneCountInString(nick) > nicknameMaxLength {
		return "", fmt.Errorf("%s", renderText("nickname_too_long", map[string]any{"Max": nicknameMaxLength}))
	}

	return nick, nil
}

func (s *Server) applyNickname(guildID, userID snowflake.ID, nick string) error {
	_, err := s.Client.Rest.UpdateMember(guildID, userID, discord.MemberUpdate{
		Nick: &nick,
	})
	return err
}

func (s *Server) requireConfiguredGuild(guildID *snowflake.ID) error {
	if guildID == nil || *guildID != s.Cfg.Bot.GuildID {
		return fmt.Errorf("%s", renderText("wrong_guild"))
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
			WithContent(renderText("nickname_panel_failed", map[string]any{"Error": err.Error()})).
			WithEphemeral(true))
	}

	return e.CreateMessage(discord.NewMessageCreate().
		WithContent(renderText("nickname_panel_posted")).
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
			WithContent(renderText("nickname_set_failed", map[string]any{"Error": err.Error()})).
			WithEphemeral(true))
	}

	return e.CreateMessage(discord.NewMessageCreate().
		WithContent(renderText("nickname_set", map[string]any{"Nick": nick})).
		WithEphemeral(true))
}
