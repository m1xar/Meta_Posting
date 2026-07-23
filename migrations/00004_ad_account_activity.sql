-- +goose Up
-- +goose StatementBegin

ALTER TABLE ad_accounts
    ADD COLUMN is_active boolean NOT NULL DEFAULT true;

CREATE INDEX idx_ad_accounts_connection_active_status
    ON ad_accounts (connection_id, is_active, account_status, name);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS idx_ad_accounts_connection_active_status;

ALTER TABLE ad_accounts
    DROP COLUMN IF EXISTS is_active;

-- +goose StatementEnd
