package server

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/snowflake/v2"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/topi314/ca-global-bot/server/database/sqlc"
)

func (s *Server) runRecheckLoop() {
	for {
		wait := s.durationUntilNextRecheck()
		slog.Info("next membership recheck scheduled", slog.Duration("wait", wait))

		timer := time.NewTimer(wait)
		select {
		case <-s.recheckStop:
			timer.Stop()
			return
		case <-timer.C:
			s.runRecheck()
		}
	}
}

func (s *Server) durationUntilNextRecheck() time.Duration {
	now := time.Now().UTC()
	next := time.Date(now.Year(), now.Month(), now.Day(), s.Cfg.Recheck.Hour, s.Cfg.Recheck.Minute, 0, 0, time.UTC)
	if !next.After(now) {
		next = next.Add(24 * time.Hour)
	}
	return next.Sub(now)
}

func (s *Server) runRecheck() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	slog.InfoContext(ctx, "starting membership recheck")

	tokens, err := s.DB.Queries.ListOAuthTokens(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "failed to list oauth tokens", slog.Any("err", err))
		return
	}

	now := time.Now().UTC()
	for _, row := range tokens {
		if err := s.recheckMember(ctx, row, now); err != nil {
			slog.ErrorContext(ctx, "recheck failed for member",
				slog.Any("err", err),
				slog.Int64("user_id", row.UserID),
			)
		}
		// mild rate limit between members
		select {
		case <-ctx.Done():
			return
		case <-time.After(500 * time.Millisecond):
		}
	}

	slog.InfoContext(ctx, "membership recheck finished", slog.Int("count", len(tokens)))
}

func (s *Server) recheckMember(ctx context.Context, row sqlc.OauthToken, now time.Time) error {
	userID := snowflake.ID(row.UserID)
	user := discord.User{ID: userID}

	if row.ReauthDeadline.Valid && !row.ReauthDeadline.Time.After(now) {
		return s.kickMember(ctx, userID, user, "reauth_deadline_expired", "Reauth grace period expired")
	}

	session := sessionFromToken(row)
	session, err := s.OAuth.VerifySession(session)
	if err != nil {
		return s.handleTokenFailure(ctx, row, user, err)
	}

	// Persist refreshed tokens if they changed.
	if session.AccessToken != row.AccessToken || session.RefreshToken != row.RefreshToken {
		if _, err = s.DB.Queries.UpdateTokensAndSources(ctx, sqlc.UpdateTokensAndSourcesParams{
			UserID:         row.UserID,
			AccessToken:    session.AccessToken,
			RefreshToken:   session.RefreshToken,
			ExpiresAt:      pgtype.Timestamptz{Time: session.Expiration, Valid: true},
			SourceGuildIds: row.SourceGuildIds,
		}); err != nil {
			return fmt.Errorf("update refreshed tokens: %w", err)
		}
		row.AccessToken = session.AccessToken
		row.RefreshToken = session.RefreshToken
		row.ExpiresAt = pgtype.Timestamptz{Time: session.Expiration, Valid: true}
		row.ReauthDeadline = pgtype.Timestamptz{}
	}

	guilds, err := s.OAuth.GetGuilds(session)
	if err != nil {
		// Treat guild fetch failure similarly to token issues if unauthorized.
		if session.Expired() {
			return s.handleTokenFailure(ctx, row, user, err)
		}
		return fmt.Errorf("get guilds: %w", err)
	}

	matched := s.matchedSourceGuilds(guilds)
	if len(matched) == 0 {
		return s.kickMember(ctx, userID, user, "lost_eligibility", "No longer in a whitelisted CA server")
	}

	roles := s.regionRoles(matched)
	if err = s.syncRegionRoles(ctx, userID, roles); err != nil {
		return fmt.Errorf("sync roles: %w", err)
	}

	if _, err = s.DB.Queries.UpdateTokensAndSources(ctx, sqlc.UpdateTokensAndSourcesParams{
		UserID:         row.UserID,
		AccessToken:    session.AccessToken,
		RefreshToken:   session.RefreshToken,
		ExpiresAt:      pgtype.Timestamptz{Time: session.Expiration, Valid: true},
		SourceGuildIds: snowflakesToInt64(matched),
	}); err != nil {
		return fmt.Errorf("update sources: %w", err)
	}

	return nil
}

func (s *Server) handleTokenFailure(ctx context.Context, row sqlc.OauthToken, user discord.User, cause error) error {
	userID := snowflake.ID(row.UserID)
	slog.WarnContext(ctx, "oauth token refresh failed",
		slog.Any("err", cause),
		slog.String("user_id", userID.String()),
	)

	if row.ReauthDeadline.Valid {
		// Already in grace; wait for deadline.
		return nil
	}

	deadline := time.Now().UTC().Add(s.Cfg.Recheck.ReauthGrace.Duration())
	if _, err := s.DB.Queries.SetReauthDeadline(ctx, sqlc.SetReauthDeadlineParams{
		UserID:         row.UserID,
		ReauthDeadline: pgtype.Timestamptz{Time: deadline, Valid: true},
	}); err != nil {
		return fmt.Errorf("set reauth deadline: %w", err)
	}

	joinURL := s.Cfg.JoinURL()
	dmErr := s.sendReauthDM(ctx, userID, joinURL, deadline)
	if dmErr != nil {
		slog.WarnContext(ctx, "failed to DM reauth prompt", slog.Any("err", dmErr), slog.String("user_id", userID.String()))
	}

	s.logEvent(ctx, "reauth_requested", user, "OAuth token invalid — reauth requested", map[string]string{
		"deadline": deadline.Format(time.RFC3339),
		"dm_ok":    fmt.Sprintf("%t", dmErr == nil),
		"join_url": joinURL,
	})
	return nil
}

func (s *Server) sendReauthDM(ctx context.Context, userID snowflake.ID, joinURL string, deadline time.Time) error {
	channel, err := s.Client.Rest.CreateDMChannel(userID)
	if err != nil {
		return fmt.Errorf("create dm: %w", err)
	}

	content := fmt.Sprintf(
		"Your CA Global verification needs to be renewed.\n\nPlease re-authorize here before **%s** (UTC), or you will be removed from the server:\n%s",
		deadline.UTC().Format(time.RFC1123),
		joinURL,
	)
	_, err = s.Client.Rest.CreateMessage(channel.ID(), discord.MessageCreate{Content: content})
	if err != nil {
		return fmt.Errorf("send dm: %w", err)
	}
	return nil
}

func (s *Server) kickMember(ctx context.Context, userID snowflake.ID, user discord.User, reason, description string) error {
	if err := s.Client.Rest.RemoveMember(s.Cfg.Bot.GuildID, userID); err != nil {
		return fmt.Errorf("kick member: %w", err)
	}
	if err := s.DB.Queries.DeleteOAuthToken(ctx, int64(userID)); err != nil {
		slog.ErrorContext(ctx, "failed to delete oauth token after kick", slog.Any("err", err))
	}
	s.logEvent(ctx, "kick", user, description, map[string]string{"reason": reason})
	return nil
}
