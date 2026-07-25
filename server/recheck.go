package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/rest"
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

// durationUntilNextRecheck returns the time until the next recheck, anchored to
// 00:00 UTC rather than boot time. With a 24h interval this fires at midnight,
// with e.g. 6h at 00:00/06:00/12:00/18:00 UTC.
func (s *Server) durationUntilNextRecheck() time.Duration {
	interval := int64(s.Cfg.Recheck.Interval.Duration())

	now := time.Now().UTC()
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	elapsed := int64(now.Sub(midnight))

	steps := elapsed/interval + 1
	next := midnight.Add(time.Duration(steps * interval))
	return next.Sub(now)
}

func (s *Server) runRecheck() {
	s.recheckMu.Lock()
	defer s.recheckMu.Unlock()
	s.doRecheck()
}

// tryStartRecheck starts a membership recheck in a new goroutine if none is
// already running. Returns false when a recheck is already in progress.
func (s *Server) tryStartRecheck() bool {
	if !s.recheckMu.TryLock() {
		return false
	}
	go func() {
		defer s.recheckMu.Unlock()
		s.doRecheck()
	}()
	return true
}

func (s *Server) doRecheck() {
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
		return s.handleTokenFailure(ctx, row, user, err)
	}

	matched := s.matchedSourceGuilds(guilds)
	if len(matched) == 0 {
		return s.kickMember(ctx, userID, user, "lost_eligibility", "No longer in a whitelisted CA server")
	}

	roles := s.regionRoles(matched)
	if err = s.syncRegionRoles(userID, roles); err != nil {
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

	// Only handle unauthorized / bad request (e.g. invalid_grant); other errors
	// are likely transient and should be retried later.
	restCause, ok := errors.AsType[*rest.Error](cause)
	if !ok || restCause.Response == nil {
		return nil
	}
	switch restCause.Response.StatusCode {
	case http.StatusUnauthorized, http.StatusBadRequest:
		// continue — permanent auth failure
	default:
		return nil
	}

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
	dmErr := s.sendReauthDM(userID, joinURL, deadline)
	if dmErr != nil {
		slog.WarnContext(ctx, "failed to DM reauth prompt", slog.Any("err", dmErr), slog.String("user_id", userID.String()))
	}

	s.logEvent(ctx, "reauth_requested", user, "OAuth token invalid — reauth requested", map[string]string{
		"deadline": discordTimestamp(deadline),
		"dm_ok":    fmt.Sprintf("%t", dmErr == nil),
		"join_url": joinURL,
	})
	return nil
}

func (s *Server) sendReauthDM(userID snowflake.ID, joinURL string, deadline time.Time) error {
	channel, err := s.Client.Rest.CreateDMChannel(userID)
	if err != nil {
		return fmt.Errorf("create dm: %w", err)
	}

	content := fmt.Sprintf(
		"Your CA Global verification needs to be renewed.\n\nPlease re-authorize here before %s, or you will be removed from the server:\n%s",
		discordTimestamp(deadline),
		joinURL,
	)
	_, err = s.Client.Rest.CreateMessage(channel.ID(), discord.MessageCreate{Content: content})
	if err != nil {
		return fmt.Errorf("send dm: %w", err)
	}
	return nil
}

// discordTimestamp formats t as a Discord timestamp (absolute + relative).
func discordTimestamp(t time.Time) string {
	unix := t.Unix()
	return fmt.Sprintf("<t:%d:f> (<t:%d:R>)", unix, unix)
}

func (s *Server) kickMember(ctx context.Context, userID snowflake.ID, user discord.User, reason, description string) error {
	joinURL := s.Cfg.JoinURL()
	dmErr := s.sendKickDM(userID, reason, joinURL)
	if dmErr != nil {
		slog.WarnContext(ctx, "failed to DM kick notice", slog.Any("err", dmErr), slog.String("user_id", userID.String()))
	}

	if err := s.Client.Rest.RemoveMember(s.Cfg.Bot.GuildID, userID); err != nil {
		return fmt.Errorf("kick member: %w", err)
	}
	if err := s.DB.Queries.DeleteOAuthToken(ctx, int64(userID)); err != nil {
		slog.ErrorContext(ctx, "failed to delete oauth token after kick", slog.Any("err", err))
	}
	s.logEvent(ctx, "kick", user, description, map[string]string{
		"reason": reason,
		"dm_ok":  fmt.Sprintf("%t", dmErr == nil),
	})
	return nil
}

func (s *Server) sendKickDM(userID snowflake.ID, reason, joinURL string) error {
	channel, err := s.Client.Rest.CreateDMChannel(userID)
	if err != nil {
		return fmt.Errorf("create dm: %w", err)
	}

	var content string
	switch reason {
	case "reauth_deadline_expired":
		content = fmt.Sprintf(
			"You have been removed from CA Global because your verification was not renewed in time.\n\nYou can rejoin anytime by authorizing again here:\n%s",
			joinURL,
		)
	case "lost_eligibility":
		content = fmt.Sprintf(
			"You have been removed from CA Global because you are no longer a member of a whitelisted CA regional Discord.\n\nOnce you are back in an eligible regional server, you can rejoin here:\n%s",
			joinURL,
		)
	default:
		content = fmt.Sprintf(
			"You have been removed from CA Global.\n\nIf you believe this was a mistake, you can try rejoining here:\n%s",
			joinURL,
		)
	}

	_, err = s.Client.Rest.CreateMessage(channel.ID(), discord.MessageCreate{Content: content})
	if err != nil {
		return fmt.Errorf("send dm: %w", err)
	}
	return nil
}
