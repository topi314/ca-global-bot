package server

import (
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/handler"
	"github.com/disgoorg/omit"
)

func recheckCommands() []discord.ApplicationCommandCreate {
	return []discord.ApplicationCommandCreate{
		discord.SlashCommandCreate{
			Name:                     "recheck",
			Description:              "Manually run membership recheck for all members",
			DefaultMemberPermissions: omit.NewPtr(discord.PermissionManageGuild),
			Contexts: []discord.InteractionContextType{
				discord.InteractionContextTypeGuild,
			},
		},
	}
}

func (s *Server) registerRecheckHandlers(r *handler.Mux) {
	r.SlashCommand("/recheck", s.handleRecheckCommand)
}

func (s *Server) handleRecheckCommand(_ discord.SlashCommandInteractionData, e *handler.CommandEvent) error {
	if err := s.requireConfiguredGuild(e.GuildID()); err != nil {
		return e.CreateMessage(discord.NewMessageCreate().
			WithContent(err.Error()).
			WithEphemeral(true))
	}

	if !s.tryStartRecheck() {
		return e.CreateMessage(discord.NewMessageCreate().
			WithContent("A membership recheck is already in progress.").
			WithEphemeral(true))
	}

	return e.CreateMessage(discord.NewMessageCreate().
		WithContent("Membership recheck started.").
		WithEphemeral(true))
}
