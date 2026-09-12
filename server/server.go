package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/disgoorg/disgo"
	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/gateway"
	"github.com/disgoorg/disgo/handler"
	"github.com/disgoorg/disgo/oauth2"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"

	"github.com/topi314/ca-global-bot/server/database"
)

type Server struct {
	Cfg         Config
	DB          *database.Database
	Client      *bot.Client
	OAuth       *oauth2.Client
	HTTPServer  *http.Server
	recheckStop chan struct{}
	recheckMu   sync.Mutex
	wg          sync.WaitGroup
}

func New(cfg Config) (*Server, error) {
	db, err := database.New(cfg.Database)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize database: %w", err)
	}

	oauthClient := oauth2.New(cfg.OAuth.ClientID, cfg.OAuth.ClientSecret)

	s := &Server{
		Cfg:         cfg,
		DB:          db,
		OAuth:       oauthClient,
		recheckStop: make(chan struct{}),
	}

	r := handler.New()
	s.registerNicknameHandlers(r)
	s.registerRecheckHandlers(r)

	client, err := disgo.New(cfg.Bot.Token,
		bot.WithGatewayConfigOpts(
			gateway.WithIntents(
				gateway.IntentGuilds,
				gateway.IntentGuildMembers,
			),
		),
		bot.WithEventListeners(r),
		bot.WithEventListenerFunc(s.onGuildMemberJoin),
		bot.WithEventListenerFunc(s.onGuildMemberLeave),
	)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to create discord client: %w", err)
	}
	s.Client = client

	if cfg.Bot.GuildID != 0 {
		commands := append(nicknameCommands(), recheckCommands()...)
		if err = handler.SyncCommands(client, commands, []snowflake.ID{cfg.Bot.GuildID}); err != nil {
			db.Close()
			client.Close(context.Background())
			return nil, fmt.Errorf("failed to sync guild commands: %w", err)
		}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /", s.handleJoin)
	mux.HandleFunc("GET /callback", s.handleJoinCallback)

	s.HTTPServer = &http.Server{
		Addr:    cfg.Server.Addr,
		Handler: mux,
	}

	return s, nil
}

func (s *Server) Start() {
	s.wg.Go(func() {
		slog.Info("HTTP server listening", slog.String("addr", s.Cfg.Server.Addr))
		if err := s.HTTPServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("HTTP server failed", slog.Any("err", err))
		}
	})

	s.wg.Go(func() {
		if err := s.Client.OpenGateway(context.Background()); err != nil {
			slog.Error("failed to open gateway", slog.Any("err", err))
		}
	})

	s.wg.Go(func() {
		s.runRecheckLoop()
	})
}

func (s *Server) Stop() {
	close(s.recheckStop)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := s.HTTPServer.Shutdown(ctx); err != nil {
		slog.Error("HTTP server shutdown failed", slog.Any("err", err))
	}

	s.Client.Close(ctx)
	s.DB.Close()
	s.wg.Wait()
}

// matchedSourceGuilds probes each whitelist guild via
// GET /users/@me/guilds/{guild.id}/member (guilds.members.read).
// 404 means the user is not in that guild; other errors are returned.
func (s *Server) matchedSourceGuilds(accessToken string) ([]snowflake.ID, error) {
	var matched []snowflake.ID
	for guildID := range s.Cfg.whitelist {
		_, err := s.OAuth.Rest.GetCurrentMember(accessToken, guildID)
		if err == nil {
			matched = append(matched, guildID)
			continue
		}
		if isNotGuildMember(err) {
			continue
		}
		return nil, err
	}
	return matched, nil
}

func isNotGuildMember(err error) bool {
	return restStatus(err) == http.StatusNotFound ||
		rest.IsJSONErrorCode(err, rest.JSONErrorCodeUnknownMember)
}

func (s *Server) regionRoles(sourceGuildIDs []snowflake.ID) []snowflake.ID {
	seen := make(map[snowflake.ID]struct{})
	var roles []snowflake.ID
	for _, guildID := range sourceGuildIDs {
		roleID, ok := s.Cfg.whitelist[guildID]
		if !ok || roleID == 0 {
			continue
		}
		if _, exists := seen[roleID]; exists {
			continue
		}
		seen[roleID] = struct{}{}
		roles = append(roles, roleID)
	}
	return roles
}

func (s *Server) allRegionRoleIDs() map[snowflake.ID]struct{} {
	roles := make(map[snowflake.ID]struct{})
	for _, roleID := range s.Cfg.whitelist {
		if roleID != 0 {
			roles[roleID] = struct{}{}
		}
	}
	return roles
}

func (s *Server) onGuildMemberJoin(e *events.GuildMemberJoin) {
	if e.GuildID != s.Cfg.Bot.GuildID {
		return
	}
	if e.Member.User.Bot || e.Member.User.System {
		return
	}

	ctx := context.Background()
	if err := s.sendNicknameWelcomeDM(e.Member.User.ID); err != nil {
		slog.WarnContext(ctx, "failed to send welcome DM", slog.Any("err", err), slog.String("user_id", e.Member.User.ID.String()))
	}
}

func (s *Server) sendNicknameWelcomeDM(userID snowflake.ID) error {
	channel, err := s.Client.Rest.CreateDMChannel(userID)
	if err != nil {
		return fmt.Errorf("create dm channel: %w", err)
	}

	_, err = s.Client.Rest.CreateMessage(channel.ID(), discord.NewMessageCreateV2(
		discord.NewContainer(
			discord.NewTextDisplay(renderText("welcome_dm")),
			discord.NewActionRow(
				discord.NewPrimaryButton("Set nickname", customIDNicknameOpen),
			),
		).WithAccentColor(0x5865F2),
	))
	if err != nil {
		return fmt.Errorf("send dm: %w", err)
	}
	return nil
}

func (s *Server) onGuildMemberLeave(e *events.GuildMemberLeave) {
	if e.GuildID != s.Cfg.Bot.GuildID {
		return
	}

	ctx := context.Background()
	if err := s.DB.Queries.DeleteOAuthToken(ctx, int64(e.User.ID)); err != nil {
		slog.ErrorContext(ctx, "failed to delete oauth token on leave", slog.Any("err", err), slog.String("user_id", e.User.ID.String()))
	}

	s.logEvent(ctx, "leave", e.User, "Member left the server", nil)
}
