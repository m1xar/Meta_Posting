-- +goose Up
-- +goose StatementBegin

-- Per-user API keys, so programmatic access carries a tenant identity instead
-- of relying on one shared INTERNAL_API_TOKEN that can read every tenant.
--
-- Only the SHA-256 hash is stored, matching user_sessions.token_hash: a
-- database disclosure must not yield usable credentials.
CREATE TABLE api_keys (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name text NOT NULL DEFAULT '',
    token_hash bytea NOT NULL,
    last_used_at timestamptz,
    expires_at timestamptz,
    revoked_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT uq_api_keys_token_hash UNIQUE (token_hash),
    CONSTRAINT ck_api_keys_token_hash CHECK (octet_length(token_hash) = 32)
);

CREATE INDEX idx_api_keys_user_active
    ON api_keys (user_id, created_at DESC) WHERE revoked_at IS NULL;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS api_keys;

-- +goose StatementEnd
