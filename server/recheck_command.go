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
			WithContent(renderText("recheck_already_running")).
			WithEphemeral(true))
	}

	return e.CreateMessage(discord.NewMessageCreate().
		WithContent(renderText("recheck_started")).
		WithEphemeral(true))
}
