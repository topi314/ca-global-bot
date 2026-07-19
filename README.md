# ca-global-bot

Discord bot that gates entry to the CA Global server via OAuth2. Members must belong to a whitelisted CA regional Discord; region roles are assigned from that membership. A daily job re-checks guild membership using stored OAuth refresh tokens. If a token fails, the bot DMs a re-auth link and kicks after a grace period.

## Features

- `GET /` — Discord OAuth (`identify`, `guilds`, `guilds.join`)
- Adds eligible users to the Global guild and assigns region roles
- Daily membership recheck with reauth grace + DM
- Join / leave / kick / reauth logging to a Discord channel (Components V2)

## Development

Requires Go 1.25+, [sqlc](https://docs.sqlc.dev/), and Postgres.

```bash
cp example.config.toml config.toml
# edit config.toml

docker compose up -d db
go run . -config config.toml
```

After changing SQL migrations or queries:

```bash
sqlc generate
```

Local stack with the bot:

```bash
docker compose up -d --build
```

Join URL (behind your reverse proxy): `https://discord.cmpf-tools.de`

## Discord Developer Portal

- Redirect URI: `https://discord.cmpf-tools.de/callback`
- Privileged intent: **Server Members Intent**
- Bot in the Global guild with Kick Members + Manage Roles (bot role above region roles)
- OAuth2 scopes: identify, guilds, guilds.join

## License

Apache License 2.0 — see [LICENSE](LICENSE).
