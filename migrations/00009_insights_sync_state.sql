-- +goose Up
-- +goose StatementBegin

-- Per-ad-account ingestion bookkeeping: how far each level has been synced,
-- how far back the backfill has walked, and whether Meta is currently
-- throttling this account.
CREATE TABLE ad_account_sync_state (
    ad_account_id uuid PRIMARY KEY REFERENCES ad_accounts(id) ON DELETE CASCADE,
    connection_id uuid NOT NULL REFERENCES meta_connections(id) ON DELETE CASCADE,
    entities_synced_at timestamptz,
    attribution_setting text NOT NULL DEFAULT 'unified',
    -- Oldest date we intend to hold, versus the oldest actually stored.
    -- backfilled_through walks backwards towards backfill_target_date.
    backfill_target_date date,
    backfilled_through date,
    account_synced_through date,
    campaign_synced_through date,
    adset_synced_through date,
    ad_synced_through date,
    last_ad_level_run_at timestamptz,
    consecutive_failures integer NOT NULL DEFAULT 0,
    -- Set from X-App-Usage / X-Ad-Account-Usage / X-Business-Use-Case-Usage so
    -- throttling survives a worker restart instead of being relearned by
    -- getting blocked again.
    throttled_until timestamptz,
    last_usage jsonb NOT NULL DEFAULT '{}'::jsonb,
    last_error text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT ck_ad_account_sync_state_usage CHECK (jsonb_typeof(last_usage) = 'object'),
    CONSTRAINT ck_ad_account_sync_state_failures CHECK (consecutive_failures >= 0)
);

CREATE INDEX idx_ad_account_sync_state_connection
    ON ad_account_sync_state (connection_id);
CREATE INDEX idx_ad_account_sync_state_throttled
    ON ad_account_sync_state (throttled_until) WHERE throttled_until IS NOT NULL;
CREATE INDEX idx_ad_account_sync_state_account_due
    ON ad_account_sync_state (account_synced_through NULLS FIRST);

-- Round-robin position for polling a connection's ad accounts. Keyed by
-- (connection, level) because the access token, and therefore the quota
-- boundary, is per connection. Read with SELECT ... FOR UPDATE so several
-- scheduler replicas hand out disjoint slices.
CREATE TABLE insights_sync_cursors (
    connection_id uuid NOT NULL REFERENCES meta_connections(id) ON DELETE CASCADE,
    level text NOT NULL,
    next_offset integer NOT NULL DEFAULT 0,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (connection_id, level),
    CONSTRAINT ck_insights_sync_cursors_level
        CHECK (level IN ('account', 'campaign', 'adset', 'ad', 'entities')),
    CONSTRAINT ck_insights_sync_cursors_offset CHECK (next_offset >= 0)
);

-- Records that a (account, level, date) was fetched, and how many rows came
-- back. This is what makes gap repair terminate: a day with no delivery
-- legitimately returns zero rows, so the absence of a row in
-- ad_insights_daily cannot distinguish "never fetched" from "fetched, nothing
-- ran". Without this table every zero-spend day is re-fetched forever.
CREATE TABLE ad_insights_coverage (
    ad_account_id uuid NOT NULL REFERENCES ad_accounts(id) ON DELETE CASCADE,
    level text NOT NULL,
    date date NOT NULL,
    row_count integer NOT NULL DEFAULT 0,
    fetched_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (ad_account_id, level, date),
    CONSTRAINT ck_ad_insights_coverage_level
        CHECK (level IN ('account', 'campaign', 'adset', 'ad')),
    CONSTRAINT ck_ad_insights_coverage_rows CHECK (row_count >= 0)
);

CREATE INDEX idx_ad_insights_coverage_account_level_date
    ON ad_insights_coverage (ad_account_id, level, date DESC);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS ad_insights_coverage;
DROP TABLE IF EXISTS insights_sync_cursors;
DROP TABLE IF EXISTS ad_account_sync_state;

-- +goose StatementEnd
