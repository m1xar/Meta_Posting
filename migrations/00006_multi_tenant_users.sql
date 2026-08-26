-- +goose Up
-- +goose StatementBegin

CREATE TABLE users (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    login text NOT NULL,
    password_hash text NOT NULL,
    last_login_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT ck_users_login CHECK (login = lower(login) AND login ~ '^[a-z0-9][a-z0-9._-]{2,63}$')
);

CREATE UNIQUE INDEX uq_users_login_lower ON users (lower(login));

-- Existing single-admin data remains available under a disabled legacy tenant.
-- The account cannot log in; an operator may migrate its resources explicitly.
INSERT INTO users (id, login, password_hash)
VALUES ('00000000-0000-0000-0000-000000000001', 'legacy-admin', '{disabled}')
ON CONFLICT (id) DO NOTHING;

CREATE TABLE user_sessions (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash bytea NOT NULL,
    csrf_hash bytea NOT NULL,
    expires_at timestamptz NOT NULL,
    last_seen_at timestamptz NOT NULL DEFAULT now(),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT ck_user_sessions_token_hash CHECK (octet_length(token_hash) = 32),
    CONSTRAINT ck_user_sessions_csrf_hash CHECK (octet_length(csrf_hash) = 32)
);

CREATE UNIQUE INDEX uq_user_sessions_token_hash ON user_sessions (token_hash);
CREATE INDEX idx_user_sessions_user_expiry ON user_sessions (user_id, expires_at);

ALTER TABLE meta_connections
    ADD COLUMN user_id uuid REFERENCES users(id) ON DELETE CASCADE;
UPDATE meta_connections
SET user_id = '00000000-0000-0000-0000-000000000001'
WHERE user_id IS NULL;
ALTER TABLE meta_connections ALTER COLUMN user_id SET NOT NULL;
ALTER TABLE meta_connections DROP CONSTRAINT IF EXISTS uq_meta_connections_user;
ALTER TABLE meta_connections
    ADD CONSTRAINT uq_meta_connections_tenant_user UNIQUE (user_id, meta_user_id);
CREATE INDEX idx_meta_connections_user_status ON meta_connections (user_id, status, created_at DESC);

ALTER TABLE oauth_sessions
    ADD COLUMN user_id uuid REFERENCES users(id) ON DELETE CASCADE;
UPDATE oauth_sessions
SET user_id = '00000000-0000-0000-0000-000000000001'
WHERE user_id IS NULL;
ALTER TABLE oauth_sessions ALTER COLUMN user_id SET NOT NULL;
CREATE INDEX idx_oauth_sessions_user_created ON oauth_sessions (user_id, created_at DESC);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

ALTER TABLE oauth_sessions DROP COLUMN IF EXISTS user_id;

DROP INDEX IF EXISTS idx_meta_connections_user_status;
ALTER TABLE meta_connections DROP CONSTRAINT IF EXISTS uq_meta_connections_tenant_user;
ALTER TABLE meta_connections ADD CONSTRAINT uq_meta_connections_user UNIQUE (meta_user_id);
ALTER TABLE meta_connections DROP COLUMN IF EXISTS user_id;

DROP TABLE IF EXISTS user_sessions;
DROP TABLE IF EXISTS users;

-- +goose StatementEnd
