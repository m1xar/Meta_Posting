-- +goose Up
-- +goose StatementBegin

-- Registration moves from a single opaque "login" to email + username, and
-- gains a role so an operator can see across tenants without sharing one
-- static bearer token.
--
-- This migration DROPS users.login, which makes rolling back to the previous
-- binary impossible without restoring a dump. It is deliberately isolated in
-- its own release for that reason.

ALTER TABLE users
    ADD COLUMN email text,
    ADD COLUMN username text,
    ADD COLUMN role text NOT NULL DEFAULT 'user';

-- Existing logins already satisfy the username rules, having been validated
-- by ck_users_login.
UPDATE users SET username = login WHERE username IS NULL;

-- .invalid is reserved by RFC 2606 and guaranteed never to resolve, so a
-- backfilled address cannot accidentally receive mail or collide with a real
-- one a user later registers.
UPDATE users SET email = login || '@legacy.invalid' WHERE email IS NULL;

-- The legacy tenant holds pre-multi-tenant data and must never authenticate.
UPDATE users
SET role = 'disabled',
    username = 'legacy-admin',
    email = 'legacy-admin@legacy.invalid'
WHERE id = '00000000-0000-0000-0000-000000000001';

ALTER TABLE users
    ALTER COLUMN email SET NOT NULL,
    ALTER COLUMN username SET NOT NULL;

CREATE UNIQUE INDEX uq_users_email_lower ON users (lower(email));
CREATE UNIQUE INDEX uq_users_username_lower ON users (lower(username));

ALTER TABLE users
    ADD CONSTRAINT ck_users_username
        CHECK (username = lower(username) AND username ~ '^[a-z0-9][a-z0-9._-]{2,63}$'),
    -- A backstop, not the validator: the application parses addresses with
    -- net/mail, which rejects forms a regex would accept.
    ADD CONSTRAINT ck_users_email
        CHECK (
            email = lower(email)
            AND email ~ '^[^@[:space:]]+@[^@[:space:]]+\.[^@[:space:]]+$'
            AND length(email) <= 254
        ),
    ADD CONSTRAINT ck_users_role CHECK (role IN ('user', 'admin', 'disabled'));

CREATE INDEX idx_users_role ON users (role) WHERE role <> 'user';

ALTER TABLE users DROP CONSTRAINT IF EXISTS ck_users_login;
DROP INDEX IF EXISTS uq_users_login_lower;
ALTER TABLE users DROP COLUMN login;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

ALTER TABLE users ADD COLUMN login text;
UPDATE users SET login = username WHERE login IS NULL;
ALTER TABLE users ALTER COLUMN login SET NOT NULL;
ALTER TABLE users
    ADD CONSTRAINT ck_users_login
        CHECK (login = lower(login) AND login ~ '^[a-z0-9][a-z0-9._-]{2,63}$');
CREATE UNIQUE INDEX uq_users_login_lower ON users (lower(login));

DROP INDEX IF EXISTS idx_users_role;
DROP INDEX IF EXISTS uq_users_username_lower;
DROP INDEX IF EXISTS uq_users_email_lower;
ALTER TABLE users
    DROP CONSTRAINT IF EXISTS ck_users_username,
    DROP CONSTRAINT IF EXISTS ck_users_email,
    DROP CONSTRAINT IF EXISTS ck_users_role;
ALTER TABLE users
    DROP COLUMN IF EXISTS role,
    DROP COLUMN IF EXISTS username,
    DROP COLUMN IF EXISTS email;

-- +goose StatementEnd
