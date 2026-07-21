package server

import (
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/disgoorg/snowflake/v2"

	"github.com/topi314/ca-global-bot/server/database"
)

func LoadConfig(cfgPath string) (Config, error) {
	file, err := os.Open(cfgPath)
	if err != nil {
		return Config{}, fmt.Errorf("failed to open config file: %w", err)
	}
	defer func() {
		_ = file.Close()
	}()

	cfg := defaultConfig()
	if _, err = toml.NewDecoder(file).Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("failed to decode config file: %w", err)
	}

	whitelist, err := cfg.ParsedWhitelist()
	if err != nil {
		return Config{}, err
	}
	cfg.whitelist = whitelist

	if err = cfg.Validate(); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

// Validate checks that all required configuration is present.
func (c Config) Validate() error {
	if c.Bot.Token == "" {
		return fmt.Errorf("bot.token is required")
	}
	if c.Bot.GuildID == 0 {
		return fmt.Errorf("bot.guild_id is required")
	}
	if c.OAuth.ClientID == 0 {
		return fmt.Errorf("oauth.client_id is required")
	}
	if c.OAuth.ClientSecret == "" {
		return fmt.Errorf("oauth.client_secret is required")
	}
	if c.Server.PublicURL == "" {
		return fmt.Errorf("server.public_url is required")
	}
	if c.Recheck.Interval <= 0 {
		return fmt.Errorf("recheck.interval must be greater than 0")
	}
	return nil
}

func defaultConfig() Config {
	return Config{
		Log: LogConfig{
			Level:     slog.LevelInfo,
			Format:    LogFormatText,
			AddSource: false,
		},
		Server: ServerConfig{
			Addr: ":8080",
		},
		Database: database.Config{
			Host:     "localhost",
			Port:     5432,
			Username: "ca-global-bot",
			Password: "password",
			Database: "ca-global-bot",
			SSLMode:  "disable",
		},
		Recheck: RecheckConfig{
			Interval:    Duration(24 * time.Hour),
			ReauthGrace: Duration(72 * time.Hour),
		},
		Whitelist: map[string]string{},
	}
}

type Config struct {
	Log       LogConfig         `toml:"log"`
	Server    ServerConfig      `toml:"server"`
	Database  database.Config   `toml:"database"`
	Bot       BotConfig         `toml:"bot"`
	OAuth     OAuthConfig       `toml:"oauth"`
	Recheck   RecheckConfig     `toml:"recheck"`
	Whitelist map[string]string `toml:"whitelist"`

	whitelist map[snowflake.ID]snowflake.ID
}

func (c Config) String() string {
	return fmt.Sprintf("Log: %s\nServer: %s\nDatabase: %s\nBot: %s\nOAuth: %s\nRecheck: %s\nWhitelist: %v",
		c.Log,
		c.Server,
		c.Database,
		c.Bot,
		c.OAuth,
		c.Recheck,
		c.Whitelist,
	)
}

func (c Config) RedirectURI() string {
	return strings.TrimRight(c.Server.PublicURL, "/") + "/callback"
}

func (c Config) JoinURL() string {
	return c.Server.PublicURL
}

func (c Config) ParsedWhitelist() (map[snowflake.ID]snowflake.ID, error) {
	out := make(map[snowflake.ID]snowflake.ID, len(c.Whitelist))
	for guildStr, roleStr := range c.Whitelist {
		guildID, err := snowflake.Parse(guildStr)
		if err != nil {
			return nil, fmt.Errorf("invalid whitelist guild id %q: %w", guildStr, err)
		}
		roleID, err := snowflake.Parse(roleStr)
		if err != nil {
			return nil, fmt.Errorf("invalid whitelist role id %q for guild %q: %w", roleStr, guildStr, err)
		}
		out[guildID] = roleID
	}
	return out, nil
}

type LogFormat string

const (
	LogFormatJSON LogFormat = "json"
	LogFormatText LogFormat = "text"
)

type LogConfig struct {
	Level     slog.Level `toml:"level"`
	Format    LogFormat  `toml:"format"`
	AddSource bool       `toml:"add_source"`
}

func (c LogConfig) String() string {
	return fmt.Sprintf("\n Level: %s\n Format: %s\n AddSource: %t",
		c.Level,
		c.Format,
		c.AddSource,
	)
}

type ServerConfig struct {
	Addr      string `toml:"addr"`
	PublicURL string `toml:"public_url"`
}

func (c ServerConfig) String() string {
	return fmt.Sprintf("\n Addr: %s\n PublicURL: %s",
		c.Addr,
		c.PublicURL,
	)
}

type BotConfig struct {
	Token        string       `toml:"token"`
	GuildID      snowflake.ID `toml:"guild_id"`
	LogChannelID snowflake.ID `toml:"log_channel_id"`
}

func (c BotConfig) String() string {
	return fmt.Sprintf("\n Token: %s\n GuildID: %s\n LogChannelID: %s",
		strings.Repeat("*", len(c.Token)),
		c.GuildID,
		c.LogChannelID,
	)
}

type OAuthConfig struct {
	ClientID     snowflake.ID `toml:"client_id"`
	ClientSecret string       `toml:"client_secret"`
}

func (c OAuthConfig) String() string {
	return fmt.Sprintf("\n ClientID: %s\n ClientSecret: %s",
		c.ClientID,
		strings.Repeat("*", len(c.ClientSecret)),
	)
}

type RecheckConfig struct {
	// Interval is how often members are rechecked.
	Interval    Duration `toml:"interval"`
	ReauthGrace Duration `toml:"reauth_grace"`
}

func (c RecheckConfig) String() string {
	return fmt.Sprintf("\n Interval: %s\n ReauthGrace: %s",
		time.Duration(c.Interval),
		time.Duration(c.ReauthGrace),
	)
}

// Duration wraps time.Duration for TOML string decoding (e.g. "72h").
type Duration time.Duration

func (d *Duration) UnmarshalText(text []byte) error {
	if len(text) == 0 {
		*d = 0
		return nil
	}
	parsed, err := time.ParseDuration(string(text))
	if err != nil {
		return err
	}
	*d = Duration(parsed)
	return nil
}

func (d Duration) Duration() time.Duration {
	return time.Duration(d)
}
