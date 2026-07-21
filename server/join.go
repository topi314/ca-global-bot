package server

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/oauth2"
	"github.com/disgoorg/snowflake/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/topi314/ca-global-bot/server/database/sqlc"
)

func (s *Server) handleJoin(w http.ResponseWriter, r *http.Request) {
	authURL := s.OAuth.GenerateAuthorizationURL(oauth2.AuthorizationURLParams{
		RedirectURI: s.Cfg.RedirectURI(),
		Scopes: []discord.OAuth2Scope{
			discord.OAuth2ScopeIdentify,
			discord.OAuth2ScopeGuilds,
			discord.OAuth2ScopeGuildsJoin,
		},
	})
	http.Redirect(w, r, authURL, http.StatusTemporaryRedirect)
}

func (s *Server) handleJoinCallback(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	query := r.URL.Query()
	code := query.Get("code")
	state := query.Get("state")

	if code == "" || state == "" {
		http.Error(w, "Missing OAuth code or state", http.StatusBadRequest)
		return
	}

	session, _, err := s.OAuth.StartSession(code, state)
	if err != nil {
		slog.ErrorContext(ctx, "failed to start oauth session", slog.Any("err", err))
		http.Error(w, "Invalid or expired OAuth state", http.StatusBadRequest)
		return
	}

	user, err := s.OAuth.GetUser(session)
	if err != nil {
		slog.ErrorContext(ctx, "failed to get oauth user", slog.Any("err", err))
		http.Error(w, "Failed to get Discord user", http.StatusInternalServerError)
		return
	}

	guilds, err := s.OAuth.GetGuilds(session)
	if err != nil {
		slog.ErrorContext(ctx, "failed to get oauth guilds", slog.Any("err", err))
		http.Error(w, "Failed to get Discord guilds", http.StatusInternalServerError)
		return
	}

	matched := s.matchedSourceGuilds(guilds)
	if len(matched) == 0 {
		writeHTML(w, http.StatusForbidden, "Not eligible", renderText("html_not_eligible"))
		return
	}

	_, err = s.DB.Queries.GetOAuthToken(ctx, int64(user.ID))
	isReauth := err == nil
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		slog.ErrorContext(ctx, "failed to lookup oauth token", slog.Any("err", err), slog.String("user_id", user.ID.String()))
		http.Error(w, "Failed to save your login", http.StatusInternalServerError)
		return
	}

	roles := s.regionRoles(matched)
	member, err := s.Client.Rest.AddMember(s.Cfg.Bot.GuildID, user.ID, discord.MemberAdd{
		AccessToken: session.AccessToken,
		Roles:       roles,
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to add member", slog.Any("err", err), slog.String("user_id", user.ID.String()))
		http.Error(w, "Failed to add you to the server", http.StatusInternalServerError)
		return
	}

	// Already a member returns 204 with nil body from Discord; still sync region roles.
	if member == nil {
		if syncErr := s.syncRegionRoles(user.ID, roles); syncErr != nil {
			slog.ErrorContext(ctx, "failed to sync region roles", slog.Any("err", syncErr), slog.String("user_id", user.ID.String()))
			http.Error(w, "Failed to update your roles", http.StatusInternalServerError)
			return
		}
	}

	sourceIDs := snowflakesToInt64(matched)
	expiresAt := pgtype.Timestamptz{Time: session.Expiration, Valid: true}
	if _, err = s.DB.Queries.UpsertOAuthToken(ctx, sqlc.UpsertOAuthTokenParams{
		UserID:         int64(user.ID),
		AccessToken:    session.AccessToken,
		RefreshToken:   session.RefreshToken,
		ExpiresAt:      expiresAt,
		SourceGuildIds: sourceIDs,
	}); err != nil {
		slog.ErrorContext(ctx, "failed to upsert oauth token", slog.Any("err", err), slog.String("user_id", user.ID.String()))
		http.Error(w, "Failed to save your login", http.StatusInternalServerError)
		return
	}

	fields := map[string]string{
		"sources": joinSnowflakes(matched),
		"roles":   joinSnowflakes(roles),
	}
	if isReauth {
		s.logEvent(ctx, "reauth", user.User, "Member re-authorized OAuth", fields)
		writeHTML(w, http.StatusOK, "Re-authorized", renderText("html_reauth"))
		return
	}

	s.logEvent(ctx, "join", user.User, "Member joined via OAuth", fields)
	writeHTML(w, http.StatusOK, "You're in", renderText("html_joined"))
}

func (s *Server) syncRegionRoles(userID snowflake.ID, desiredRoles []snowflake.ID) error {
	member, err := s.Client.Rest.GetMember(s.Cfg.Bot.GuildID, userID)
	if err != nil {
		return fmt.Errorf("get member: %w", err)
	}

	desired := make(map[snowflake.ID]struct{}, len(desiredRoles))
	for _, roleID := range desiredRoles {
		desired[roleID] = struct{}{}
	}
	managed := s.allRegionRoleIDs()

	currentManaged := make(map[snowflake.ID]struct{})
	for _, roleID := range member.RoleIDs {
		if _, ok := managed[roleID]; ok {
			currentManaged[roleID] = struct{}{}
		}
	}

	for roleID := range desired {
		if _, has := currentManaged[roleID]; !has {
			if err = s.Client.Rest.AddMemberRole(s.Cfg.Bot.GuildID, userID, roleID); err != nil {
				return fmt.Errorf("add role %s: %w", roleID, err)
			}
		}
	}
	for roleID := range currentManaged {
		if _, keep := desired[roleID]; !keep {
			if err = s.Client.Rest.RemoveMemberRole(s.Cfg.Bot.GuildID, userID, roleID); err != nil {
				return fmt.Errorf("remove role %s: %w", roleID, err)
			}
		}
	}
	return nil
}

func snowflakesToInt64(ids []snowflake.ID) []int64 {
	out := make([]int64, len(ids))
	for i, id := range ids {
		out[i] = int64(id)
	}
	return out
}

func joinSnowflakes(ids []snowflake.ID) string {
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = id.String()
	}
	return strings.Join(parts, ", ")
}

func sessionFromToken(row sqlc.OauthToken) oauth2.Session {
	return oauth2.Session{
		AccessToken:  row.AccessToken,
		RefreshToken: row.RefreshToken,
		Scopes: []discord.OAuth2Scope{
			discord.OAuth2ScopeIdentify,
			discord.OAuth2ScopeGuilds,
			discord.OAuth2ScopeGuildsJoin,
		},
		TokenType:  discord.TokenTypeBearer,
		Expiration: row.ExpiresAt.Time,
	}
}
