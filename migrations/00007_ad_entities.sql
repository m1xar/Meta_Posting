-- +goose Up
-- +goose StatementBegin

-- Inventory of every campaign, ad set and ad visible in a connected ad
-- account, including objects this service did not create. published_objects
-- cannot serve this purpose: its batch_id and batch_account_result_id are
-- NOT NULL ON DELETE RESTRICT, its uniqueness is keyed on a batch idempotency
-- key that an Ads Manager campaign has no equivalent of, and its object_type
-- includes 'creative', which has no Insights level.
CREATE TABLE ad_entities (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    connection_id uuid NOT NULL REFERENCES meta_connections(id) ON DELETE CASCADE,
    ad_account_id uuid NOT NULL REFERENCES ad_accounts(id) ON DELETE CASCADE,
    level text NOT NULL,
    meta_object_id text NOT NULL,
    parent_meta_object_id text NOT NULL DEFAULT '',
    campaign_meta_id text NOT NULL DEFAULT '',
    adset_meta_id text NOT NULL DEFAULT '',
    name text NOT NULL DEFAULT '',
    status text NOT NULL DEFAULT '',
    configured_status text NOT NULL DEFAULT '',
    effective_status text NOT NULL DEFAULT '',
    objective text NOT NULL DEFAULT '',
    buying_type text NOT NULL DEFAULT '',
    optimization_goal text NOT NULL DEFAULT '',
    billing_event text NOT NULL DEFAULT '',
    destination_type text NOT NULL DEFAULT '',
    bid_strategy text NOT NULL DEFAULT '',
    daily_budget bigint NOT NULL DEFAULT 0,
    lifetime_budget bigint NOT NULL DEFAULT 0,
    budget_remaining bigint NOT NULL DEFAULT 0,
    bid_amount bigint NOT NULL DEFAULT 0,
    spend_cap bigint NOT NULL DEFAULT 0,
    start_time timestamptz,
    stop_time timestamptz,
    meta_created_time timestamptz,
    meta_updated_time timestamptz,
    -- Links an entity back to the batch that produced it, when this service
    -- published it. NULL for anything created in Ads Manager.
    published_object_id uuid REFERENCES published_objects(id) ON DELETE SET NULL,
    is_owned boolean NOT NULL DEFAULT false,
    raw_json jsonb NOT NULL DEFAULT '{}'::jsonb,
    first_seen_at timestamptz NOT NULL DEFAULT now(),
    last_seen_at timestamptz NOT NULL DEFAULT now(),
    -- Soft delete. An entity that stops appearing in the account keeps its
    -- history, because its insight rows remain valid for the days it ran.
    disappeared_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT uq_ad_entities_account_level_object UNIQUE (ad_account_id, level, meta_object_id),
    CONSTRAINT ck_ad_entities_level CHECK (level IN ('campaign', 'adset', 'ad')),
    CONSTRAINT ck_ad_entities_raw CHECK (jsonb_typeof(raw_json) = 'object')
);

CREATE INDEX idx_ad_entities_account_level_status
    ON ad_entities (ad_account_id, level, effective_status);
CREATE INDEX idx_ad_entities_campaign
    ON ad_entities (campaign_meta_id) WHERE campaign_meta_id <> '';
CREATE INDEX idx_ad_entities_connection_seen
    ON ad_entities (connection_id, last_seen_at DESC);
CREATE INDEX idx_ad_entities_live
    ON ad_entities (ad_account_id, level) WHERE disappeared_at IS NULL;
CREATE INDEX idx_ad_entities_meta_object
    ON ad_entities (meta_object_id);
CREATE INDEX idx_ad_entities_published_object
    ON ad_entities (published_object_id) WHERE published_object_id IS NOT NULL;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS ad_entities;

-- +goose StatementEnd
