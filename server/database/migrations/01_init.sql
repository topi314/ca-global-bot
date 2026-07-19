CREATE TABLE oauth_tokens
(
    user_id          BIGINT PRIMARY KEY,
    access_token     TEXT        NOT NULL,
    refresh_token    TEXT        NOT NULL,
    expires_at       TIMESTAMPTZ NOT NULL,
    source_guild_ids BIGINT[]    NOT NULL DEFAULT '{}',
    reauth_deadline  TIMESTAMPTZ,
    last_checked_at  TIMESTAMPTZ,
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);
