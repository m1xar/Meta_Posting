-- +goose Up
-- +goose StatementBegin

-- One row per (ad account, level, object, day), upserted. The date comes from
-- time_increment=1 over an explicit time_range, resolved in the ad account's
-- own timezone, so a row means "what this object did on that calendar day".
--
-- This is deliberately not the cumulative-snapshot model used by
-- insight_snapshots. Daily rows make backfill and gap repair idempotent: any
-- range can be re-fetched at any time and simply rewrites the same facts,
-- which matters because Meta keeps restating a day's numbers for up to 28
-- days as attribution windows close.
CREATE TABLE ad_insights_daily (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    connection_id uuid NOT NULL REFERENCES meta_connections(id) ON DELETE CASCADE,
    ad_account_id uuid NOT NULL REFERENCES ad_accounts(id) ON DELETE CASCADE,
    level text NOT NULL,
    meta_object_id text NOT NULL,
    meta_account_id text NOT NULL DEFAULT '',
    campaign_meta_id text NOT NULL DEFAULT '',
    adset_meta_id text NOT NULL DEFAULT '',
    object_name text NOT NULL DEFAULT '',
    date date NOT NULL,
    account_timezone text NOT NULL DEFAULT '',
    currency text NOT NULL DEFAULT '',
    -- How the row was produced: 'unified', or an explicit window list such as
    -- '1d_view,7d_click'. Not part of the unique key: an object has one
    -- timeline, and changing the setting forces a re-fetch of the lookback
    -- window rather than splitting the series in two.
    attribution_setting text NOT NULL DEFAULT '',

    spend numeric(24,8) NOT NULL DEFAULT 0,
    impressions bigint NOT NULL DEFAULT 0,
    -- reach, frequency, cpp and every unique_* metric are deduplicated over
    -- the query window. They are valid for this one day and must never be
    -- summed across rows; see application.NonAdditiveMetrics.
    reach bigint NOT NULL DEFAULT 0,
    frequency numeric(24,8) NOT NULL DEFAULT 0,
    clicks bigint NOT NULL DEFAULT 0,
    unique_clicks bigint NOT NULL DEFAULT 0,
    inline_link_clicks bigint NOT NULL DEFAULT 0,
    unique_inline_link_clicks bigint NOT NULL DEFAULT 0,
    ctr numeric(24,8) NOT NULL DEFAULT 0,
    unique_ctr numeric(24,8) NOT NULL DEFAULT 0,
    cpc numeric(24,8) NOT NULL DEFAULT 0,
    cpm numeric(24,8) NOT NULL DEFAULT 0,
    cpp numeric(24,8) NOT NULL DEFAULT 0,
    cost_per_unique_click numeric(24,8) NOT NULL DEFAULT 0,
    cost_per_inline_link_click numeric(24,8) NOT NULL DEFAULT 0,
    quality_ranking text NOT NULL DEFAULT '',
    engagement_rate_ranking text NOT NULL DEFAULT '',
    conversion_rate_ranking text NOT NULL DEFAULT '',

    -- Action arrays keyed by action_type, preserving every attribution window
    -- Meta returned: {"purchase": {"value": 12, "7d_click": 12, "1d_view": 3}}.
    -- A child table would multiply row count by ~30 for no query benefit.
    actions jsonb NOT NULL DEFAULT '{}'::jsonb,
    action_values jsonb NOT NULL DEFAULT '{}'::jsonb,
    cost_per_action jsonb NOT NULL DEFAULT '{}'::jsonb,
    conversions jsonb NOT NULL DEFAULT '{}'::jsonb,
    roas jsonb NOT NULL DEFAULT '{}'::jsonb,
    video jsonb NOT NULL DEFAULT '{}'::jsonb,
    -- Flat form, e.g. {"spend": 12.5, "actions.purchase": 3}. The automation
    -- rule DSL reads this shape, so rules work against these rows unchanged.
    metrics jsonb NOT NULL DEFAULT '{}'::jsonb,
    raw_json jsonb NOT NULL DEFAULT '{}'::jsonb,

    fetched_at timestamptz NOT NULL DEFAULT now(),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT uq_ad_insights_daily UNIQUE (ad_account_id, level, meta_object_id, date),
    CONSTRAINT ck_ad_insights_daily_level CHECK (level IN ('account', 'campaign', 'adset', 'ad')),
    CONSTRAINT ck_ad_insights_daily_counts CHECK (
        impressions >= 0 AND reach >= 0 AND clicks >= 0
        AND unique_clicks >= 0 AND inline_link_clicks >= 0
        AND unique_inline_link_clicks >= 0
    ),
    CONSTRAINT ck_ad_insights_daily_spend CHECK (spend >= 0),
    CONSTRAINT ck_ad_insights_daily_actions CHECK (jsonb_typeof(actions) = 'object'),
    CONSTRAINT ck_ad_insights_daily_action_values CHECK (jsonb_typeof(action_values) = 'object'),
    CONSTRAINT ck_ad_insights_daily_cost_per_action CHECK (jsonb_typeof(cost_per_action) = 'object'),
    CONSTRAINT ck_ad_insights_daily_conversions CHECK (jsonb_typeof(conversions) = 'object'),
    CONSTRAINT ck_ad_insights_daily_roas CHECK (jsonb_typeof(roas) = 'object'),
    CONSTRAINT ck_ad_insights_daily_video CHECK (jsonb_typeof(video) = 'object'),
    CONSTRAINT ck_ad_insights_daily_metrics CHECK (jsonb_typeof(metrics) = 'object'),
    CONSTRAINT ck_ad_insights_daily_raw CHECK (jsonb_typeof(raw_json) = 'object')
);

CREATE INDEX idx_ad_insights_daily_account_level_date
    ON ad_insights_daily (ad_account_id, level, date DESC);
CREATE INDEX idx_ad_insights_daily_object_date
    ON ad_insights_daily (meta_object_id, level, date DESC);
CREATE INDEX idx_ad_insights_daily_campaign_date
    ON ad_insights_daily (campaign_meta_id, date DESC) WHERE campaign_meta_id <> '';
CREATE INDEX idx_ad_insights_daily_connection_date
    ON ad_insights_daily (connection_id, date DESC);
CREATE INDEX idx_ad_insights_daily_metrics_gin
    ON ad_insights_daily USING gin (metrics);

-- Deduplicated reach and frequency for a whole window, fetched with
-- time_increment omitted so Meta does the deduplication. This is the only
-- correct way to report reach over more than one day.
CREATE TABLE ad_insights_windowed (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    connection_id uuid NOT NULL REFERENCES meta_connections(id) ON DELETE CASCADE,
    ad_account_id uuid NOT NULL REFERENCES ad_accounts(id) ON DELETE CASCADE,
    level text NOT NULL,
    meta_object_id text NOT NULL,
    since date NOT NULL,
    until date NOT NULL,
    account_timezone text NOT NULL DEFAULT '',
    attribution_setting text NOT NULL DEFAULT '',
    reach bigint NOT NULL DEFAULT 0,
    frequency numeric(24,8) NOT NULL DEFAULT 0,
    impressions bigint NOT NULL DEFAULT 0,
    spend numeric(24,8) NOT NULL DEFAULT 0,
    raw_json jsonb NOT NULL DEFAULT '{}'::jsonb,
    fetched_at timestamptz NOT NULL DEFAULT now(),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT uq_ad_insights_windowed UNIQUE (ad_account_id, level, meta_object_id, since, until),
    CONSTRAINT ck_ad_insights_windowed_level CHECK (level IN ('account', 'campaign', 'adset', 'ad')),
    CONSTRAINT ck_ad_insights_windowed_range CHECK (since <= until),
    CONSTRAINT ck_ad_insights_windowed_raw CHECK (jsonb_typeof(raw_json) = 'object')
);

CREATE INDEX idx_ad_insights_windowed_account_level
    ON ad_insights_windowed (ad_account_id, level, until DESC);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS ad_insights_windowed;
DROP TABLE IF EXISTS ad_insights_daily;

-- +goose StatementEnd
