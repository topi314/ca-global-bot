package auth

import (
	"fmt"
	"strings"

	"github.com/disgoorg/snowflake/v2"
)

type Config struct {
	ClientID     snowflake.ID `toml:"client_id"`
	ClientSecret string       `toml:"client_secret"`
}

func (c Config) String() string {
	return fmt.Sprintf("\n ClientID: %s\n ClientSecret: %s",
		c.ClientID,
		strings.Repeat("*", len(c.ClientSecret)),
	)
}
