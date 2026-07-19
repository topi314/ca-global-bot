-- name: UpsertOAuthToken :one
INSERT INTO oauth_tokens (user_id, access_token, refresh_token, expires_at, source_guild_ids, reauth_deadline,
                          last_checked_at, updated_at)
VALUES ($1, $2, $3, $4, $5, NULL, now(), now())
ON CONFLICT (user_id) DO UPDATE
    SET access_token     = EXCLUDED.access_token,
        refresh_token    = EXCLUDED.refresh_token,
        expires_at       = EXCLUDED.expires_at,
        source_guild_ids = EXCLUDED.source_guild_ids,
        reauth_deadline  = NULL,
        last_checked_at  = now(),
        updated_at       = now()
RETURNING *;

-- name: ListOAuthTokens :many
SELECT *
FROM oauth_tokens
ORDER BY user_id;

-- name: GetOAuthToken :one
SELECT *
FROM oauth_tokens
WHERE user_id = $1;

-- name: UpdateTokensAndSources :one
UPDATE oauth_tokens
SET access_token     = $2,
    refresh_token    = $3,
    expires_at       = $4,
    source_guild_ids = $5,
    reauth_deadline  = NULL,
    last_checked_at  = now(),
    updated_at       = now()
WHERE user_id = $1
RETURNING *;

-- name: SetReauthDeadline :one
UPDATE oauth_tokens
SET reauth_deadline = $2,
    updated_at      = now()
WHERE user_id = $1
RETURNING *;

-- name: ClearReauthDeadline :exec
UPDATE oauth_tokens
SET reauth_deadline = NULL,
    updated_at      = now()
WHERE user_id = $1;

-- name: UpdateLastChecked :exec
UPDATE oauth_tokens
SET last_checked_at = now(),
    updated_at      = now()
WHERE user_id = $1;

-- name: DeleteOAuthToken :exec
DELETE
FROM oauth_tokens
WHERE user_id = $1;

-- name: ListExpiredReauth :many
SELECT *
FROM oauth_tokens
WHERE reauth_deadline IS NOT NULL
  AND reauth_deadline <= $1
ORDER BY user_id;
