-- +goose Up
-- +goose StatementBegin

-- Whether an ad account can actually be launched into.
--
-- balance is not the answer: Meta reports it as the amount *due*, not funds
-- available, so the busiest accounts on a credit line show balance = 0 while
-- having spent thousands. What decides it is whether a funding instrument is
-- attached at all, combined with account_status and the user's tasks.
ALTER TABLE ad_accounts
    ADD COLUMN funding_source text NOT NULL DEFAULT '',
    ADD COLUMN funding_source_details jsonb NOT NULL DEFAULT '{}'::jsonb,
    ADD COLUMN is_prepay_account boolean NOT NULL DEFAULT false,
    ADD COLUMN user_tasks jsonb NOT NULL DEFAULT '[]'::jsonb;

ALTER TABLE ad_accounts
    ADD CONSTRAINT ck_ad_accounts_funding_details
        CHECK (jsonb_typeof(funding_source_details) = 'object'),
    ADD CONSTRAINT ck_ad_accounts_user_tasks
        CHECK (jsonb_typeof(user_tasks) = 'array');

-- Backfill user_tasks from the raw payload, which discovery already stored.
UPDATE ad_accounts
SET user_tasks = COALESCE(raw_json->'user_tasks', '[]'::jsonb)
WHERE jsonb_typeof(raw_json->'user_tasks') = 'array';

-- The launcher lists accounts by readiness, so this is the index it needs.
CREATE INDEX idx_ad_accounts_launchable
    ON ad_accounts (connection_id, account_status)
    WHERE is_active AND account_status = 1 AND disable_reason = 0;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS idx_ad_accounts_launchable;
ALTER TABLE ad_accounts
    DROP CONSTRAINT IF EXISTS ck_ad_accounts_funding_details,
    DROP CONSTRAINT IF EXISTS ck_ad_accounts_user_tasks;
ALTER TABLE ad_accounts
    DROP COLUMN IF EXISTS funding_source,
    DROP COLUMN IF EXISTS funding_source_details,
    DROP COLUMN IF EXISTS is_prepay_account,
    DROP COLUMN IF EXISTS user_tasks;

-- +goose StatementEnd
