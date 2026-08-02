package server

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/handler"
	"github.com/disgoorg/omit"
	"github.com/disgoorg/snowflake/v2"
	"golang.org/x/text/language"
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
			Description: "Set your server nickname (trainer name, country code, community)",
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
		AddLabel("Country code", discord.NewShortTextInput(fieldFlag).
			WithRequired(true).
			WithMinLength(2).
			WithMaxLength(3).
			WithPlaceholder("e.g. DE")).
		AddComponents(discord.NewLabel("Community", discord.NewShortTextInput(fieldCommunity).
			WithRequired(true).
			WithMinLength(1).
			WithMaxLength(32).
			WithPlaceholder("Your community name")).
			WithDescription(renderText("nickname_community_hint")))
}

// countryCodeToFlag turns a real ISO 3166-1 alpha-2/alpha-3 country code into
// a flag emoji (regional indicator symbols), e.g. "DE"/"DEU" -> 🇩🇪. Deprecated
// aliases are canonicalized (e.g. "UK" -> "GB").
func countryCodeToFlag(code string) (string, error) {
	code = strings.ToUpper(strings.TrimSpace(code))
	if len(code) != 2 && len(code) != 3 {
		return "", fmt.Errorf("%s", renderText("nickname_invalid_country"))
	}

	region, err := language.ParseRegion(code)
	if err != nil || !region.IsCountry() {
		return "", fmt.Errorf("%s", renderText("nickname_invalid_country"))
	}

	// Always emit the alpha-2 regional-indicator flag.
	code = region.Canonicalize().String()
	if len(code) != 2 {
		return "", fmt.Errorf("%s", renderText("nickname_invalid_country"))
	}
	r0, r1 := rune(code[0]), rune(code[1])
	if r0 < 'A' || r0 > 'Z' || r1 < 'A' || r1 > 'Z' {
		return "", fmt.Errorf("%s", renderText("nickname_invalid_country"))
	}

	const regionalIndicatorA = 0x1F1E6
	return string([]rune{regionalIndicatorA + (r0 - 'A'), regionalIndicatorA + (r1 - 'A')}), nil
}

func formatNickname(trainerName, countryCode, community string) (string, error) {
	trainerName = strings.TrimSpace(trainerName)
	community = strings.TrimSpace(community)

	if trainerName == "" || strings.TrimSpace(countryCode) == "" || community == "" {
		return "", fmt.Errorf("%s", renderText("nickname_required"))
	}

	flag, err := countryCodeToFlag(countryCode)
	if err != nil {
		return "", err
	}

	nick := trainerName + " " + flag + " " + community
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
