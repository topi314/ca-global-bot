package server

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/disgoorg/disgo/discord"
)

func (s *Server) logEvent(ctx context.Context, action string, user discord.User, description string, fields map[string]string) {
	if s.Cfg.Bot.LogChannelID == 0 {
		slog.InfoContext(ctx, "audit event",
			slog.String("action", action),
			slog.String("user_id", user.ID.String()),
			slog.String("description", description),
			slog.Any("fields", fields),
		)
		return
	}

	var b strings.Builder
	fmt.Fprintf(&b, "## %s\n", description)
	fmt.Fprintf(&b, "**User:** %s (`%s`)\n", user.Mention(), user.ID)
	fmt.Fprintf(&b, "**Action:** `%s`", action)
	for k, v := range fields {
		if v == "" {
			continue
		}
		fmt.Fprintf(&b, "\n**%s:** %s", k, v)
	}

	color := 0x57F287 // green
	switch action {
	case "leave", "kick", "reauth_requested":
		color = 0xED4245
	case "reauth", "recheck_pass":
		color = 0x5865F2
	}

	_, err := s.Client.Rest.CreateMessage(s.Cfg.Bot.LogChannelID, discord.NewMessageCreateV2(
		discord.NewContainer(
			discord.NewTextDisplay(b.String()),
		).WithAccentColor(color),
	))
	if err != nil {
		slog.ErrorContext(ctx, "failed to post audit log message", slog.Any("err", err), slog.String("action", action))
	}
}
