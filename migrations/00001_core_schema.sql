-- +goose Up
-- +goose StatementBegin

CREATE TABLE meta_connections (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    meta_user_id text NOT NULL,
    display_name text NOT NULL DEFAULT '',
    email text NOT NULL DEFAULT '',
    status text NOT NULL,
    access_token_ciphertext bytea NOT NULL,
    access_token_nonce bytea NOT NULL,
    token_key_version smallint NOT NULL DEFAULT 1,
    token_expires_at timestamptz,
    data_access_expires_at timestamptz,
    granted_scopes jsonb NOT NULL DEFAULT '[]'::jsonb,
    declined_scopes jsonb NOT NULL DEFAULT '[]'::jsonb,
    last_validated_at timestamptz,
    last_synced_at timestamptz,
    last_error text NOT NULL DEFAULT '',
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT uq_meta_connections_user UNIQUE (meta_user_id),
    CONSTRAINT ck_meta_connections_status CHECK (status IN ('active', 'expired', 'revoked', 'disconnected', 'error')),
    CONSTRAINT ck_meta_connections_nonce CHECK (octet_length(access_token_nonce) = 12),
    CONSTRAINT ck_meta_connections_ciphertext CHECK (octet_length(access_token_ciphertext) > 16),
    CONSTRAINT ck_meta_connections_key_version CHECK (token_key_version > 0),
    CONSTRAINT ck_meta_connections_granted_scopes CHECK (jsonb_typeof(granted_scopes) = 'array'),
    CONSTRAINT ck_meta_connections_declined_scopes CHECK (jsonb_typeof(declined_scopes) = 'array'),
    CONSTRAINT ck_meta_connections_metadata CHECK (jsonb_typeof(metadata) = 'object')
);

CREATE INDEX idx_meta_connections_status ON meta_connections (status);
CREATE INDEX idx_meta_connections_token_expiry ON meta_connections (token_expires_at)
    WHERE token_expires_at IS NOT NULL AND status = 'active';

CREATE TABLE oauth_sessions (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    state_hash bytea NOT NULL,
    redirect_uri text NOT NULL,
    requested_scopes jsonb NOT NULL DEFAULT '[]'::jsonb,
    status text NOT NULL DEFAULT 'pending',
    expires_at timestamptz NOT NULL,
    consumed_at timestamptz,
    completed_at timestamptz,
    completed_connection_id uuid REFERENCES meta_connections(id) ON DELETE SET NULL,
    error text NOT NULL DEFAULT '',
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT uq_oauth_sessions_state_hash UNIQUE (state_hash),
    CONSTRAINT ck_oauth_sessions_state_hash CHECK (octet_length(state_hash) = 32),
    CONSTRAINT ck_oauth_sessions_status CHECK (status IN ('pending', 'consumed', 'completed', 'failed', 'expired')),
    CONSTRAINT ck_oauth_sessions_requested_scopes CHECK (jsonb_typeof(requested_scopes) = 'array'),
    CONSTRAINT ck_oauth_sessions_metadata CHECK (jsonb_typeof(metadata) = 'object'),
    CONSTRAINT ck_oauth_sessions_timestamps CHECK (
        (consumed_at IS NULL OR consumed_at >= created_at)
        AND (completed_at IS NULL OR completed_at >= created_at)
    )
);

CREATE INDEX idx_oauth_sessions_pending_expiry ON oauth_sessions (expires_at)
    WHERE status = 'pending';

CREATE TABLE businesses (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    connection_id uuid NOT NULL REFERENCES meta_connections(id) ON DELETE CASCADE,
    meta_business_id text NOT NULL,
    name text NOT NULL DEFAULT '',
    verification_status text NOT NULL DEFAULT '',
    vertical text NOT NULL DEFAULT '',
    primary_page_id text NOT NULL DEFAULT '',
    timezone_id integer NOT NULL DEFAULT 0,
    is_hidden boolean NOT NULL DEFAULT false,
    raw_json jsonb NOT NULL DEFAULT '{}'::jsonb,
    last_synced_at timestamptz NOT NULL DEFAULT now(),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT uq_businesses_connection_meta UNIQUE (connection_id, meta_business_id),
    CONSTRAINT ck_businesses_raw CHECK (jsonb_typeof(raw_json) = 'object')
);

CREATE INDEX idx_businesses_connection_name ON businesses (connection_id, name);

CREATE TABLE ad_accounts (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    connection_id uuid NOT NULL REFERENCES meta_connections(id) ON DELETE CASCADE,
    business_id uuid REFERENCES businesses(id) ON DELETE SET NULL,
    meta_ad_account_id text NOT NULL,
    account_id text NOT NULL DEFAULT '',
    name text NOT NULL DEFAULT '',
    currency text NOT NULL DEFAULT '',
    timezone_name text NOT NULL DEFAULT '',
    timezone_offset_utc double precision NOT NULL DEFAULT 0,
    account_status integer NOT NULL DEFAULT 0,
    disable_reason integer NOT NULL DEFAULT 0,
    business_name text NOT NULL DEFAULT '',
    amount_spent bigint NOT NULL DEFAULT 0,
    balance bigint NOT NULL DEFAULT 0,
    spend_cap bigint NOT NULL DEFAULT 0,
    capabilities jsonb NOT NULL DEFAULT '[]'::jsonb,
    raw_json jsonb NOT NULL DEFAULT '{}'::jsonb,
    last_synced_at timestamptz NOT NULL DEFAULT now(),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT uq_ad_accounts_connection_meta UNIQUE (connection_id, meta_ad_account_id),
    CONSTRAINT ck_ad_accounts_capabilities CHECK (jsonb_typeof(capabilities) = 'array'),
    CONSTRAINT ck_ad_accounts_raw CHECK (jsonb_typeof(raw_json) = 'object')
);

CREATE INDEX idx_ad_accounts_connection_status ON ad_accounts (connection_id, account_status, name);
CREATE INDEX idx_ad_accounts_business ON ad_accounts (business_id) WHERE business_id IS NOT NULL;
CREATE INDEX idx_ad_accounts_meta_id ON ad_accounts (meta_ad_account_id);

CREATE TABLE assets (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    connection_id uuid NOT NULL REFERENCES meta_connections(id) ON DELETE CASCADE,
    business_id uuid REFERENCES businesses(id) ON DELETE SET NULL,
    ad_account_id uuid REFERENCES ad_accounts(id) ON DELETE SET NULL,
    asset_type text NOT NULL,
    meta_asset_id text NOT NULL,
    parent_meta_id text NOT NULL DEFAULT '',
    name text NOT NULL DEFAULT '',
    status text NOT NULL DEFAULT '',
    is_active boolean NOT NULL DEFAULT true,
    normalized jsonb NOT NULL DEFAULT '{}'::jsonb,
    raw_json jsonb NOT NULL DEFAULT '{}'::jsonb,
    last_synced_at timestamptz NOT NULL DEFAULT now(),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT uq_assets_connection_type_meta UNIQUE (connection_id, asset_type, meta_asset_id),
    CONSTRAINT ck_assets_type CHECK (asset_type IN (
        'page', 'instagram_account', 'pixel', 'dataset', 'custom_conversion',
        'custom_audience', 'lookalike_audience', 'meta_app', 'post'
    )),
    CONSTRAINT ck_assets_normalized CHECK (jsonb_typeof(normalized) = 'object'),
    CONSTRAINT ck_assets_raw CHECK (jsonb_typeof(raw_json) = 'object')
);

CREATE INDEX idx_assets_connection_type_active ON assets (connection_id, asset_type, is_active, name);
CREATE INDEX idx_assets_ad_account_type ON assets (ad_account_id, asset_type)
    WHERE ad_account_id IS NOT NULL;
CREATE INDEX idx_assets_business_type ON assets (business_id, asset_type)
    WHERE business_id IS NOT NULL;
CREATE INDEX idx_assets_normalized_gin ON assets USING gin (normalized);

CREATE TABLE media (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    connection_id uuid REFERENCES meta_connections(id) ON DELETE SET NULL,
    ad_account_id uuid REFERENCES ad_accounts(id) ON DELETE SET NULL,
    kind text NOT NULL,
    status text NOT NULL DEFAULT 'pending',
    original_name text NOT NULL,
    local_path text NOT NULL,
    mime_type text NOT NULL,
    sha256 text NOT NULL,
    size_bytes bigint NOT NULL,
    width integer NOT NULL DEFAULT 0,
    height integer NOT NULL DEFAULT 0,
    duration_ms bigint NOT NULL DEFAULT 0,
    meta_image_hash text NOT NULL DEFAULT '',
    meta_video_id text NOT NULL DEFAULT '',
    last_error text NOT NULL DEFAULT '',
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT ck_media_kind CHECK (kind IN ('image', 'video')),
    CONSTRAINT ck_media_status CHECK (status IN ('pending', 'processing', 'ready', 'failed')),
    CONSTRAINT ck_media_size CHECK (size_bytes >= 0 AND width >= 0 AND height >= 0 AND duration_ms >= 0),
    CONSTRAINT ck_media_sha256 CHECK (sha256 ~ '^[0-9a-f]{64}$'),
    CONSTRAINT ck_media_metadata CHECK (jsonb_typeof(metadata) = 'object')
);

CREATE UNIQUE INDEX uq_media_local_path ON media (local_path);
CREATE INDEX idx_media_connection_created ON media (connection_id, created_at DESC)
    WHERE connection_id IS NOT NULL;
CREATE INDEX idx_media_sha256 ON media (sha256);
CREATE INDEX idx_media_status ON media (status, created_at);

CREATE TABLE batches (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    connection_id uuid NOT NULL REFERENCES meta_connections(id) ON DELETE RESTRICT,
    name text NOT NULL DEFAULT '',
    status text NOT NULL DEFAULT 'draft',
    idempotency_key text NOT NULL,
    specification jsonb NOT NULL DEFAULT '{}'::jsonb,
    total_accounts integer NOT NULL DEFAULT 0,
    succeeded_accounts integer NOT NULL DEFAULT 0,
    failed_accounts integer NOT NULL DEFAULT 0,
    started_at timestamptz,
    completed_at timestamptz,
    created_by text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT uq_batches_connection_idempotency UNIQUE (connection_id, idempotency_key),
    CONSTRAINT ck_batches_status CHECK (status IN (
        'draft', 'queued', 'running', 'partially_succeeded', 'succeeded', 'failed', 'cancelled'
    )),
    CONSTRAINT ck_batches_counts CHECK (
        total_accounts >= 0 AND succeeded_accounts >= 0 AND failed_accounts >= 0
        AND succeeded_accounts + failed_accounts <= total_accounts
    ),
    CONSTRAINT ck_batches_specification CHECK (jsonb_typeof(specification) = 'object'),
    CONSTRAINT ck_batches_timestamps CHECK (
        (started_at IS NULL OR started_at >= created_at)
        AND (completed_at IS NULL OR completed_at >= created_at)
    )
);

CREATE INDEX idx_batches_connection_created ON batches (connection_id, created_at DESC);
CREATE INDEX idx_batches_status_created ON batches (status, created_at);

CREATE TABLE batch_account_results (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    batch_id uuid NOT NULL REFERENCES batches(id) ON DELETE CASCADE,
    ad_account_id uuid NOT NULL REFERENCES ad_accounts(id) ON DELETE RESTRICT,
    status text NOT NULL DEFAULT 'pending',
    attempts integer NOT NULL DEFAULT 0,
    request_json jsonb NOT NULL DEFAULT '{}'::jsonb,
    response_json jsonb NOT NULL DEFAULT '{}'::jsonb,
    error_code text NOT NULL DEFAULT '',
    error_subcode text NOT NULL DEFAULT '',
    error_message text NOT NULL DEFAULT '',
    started_at timestamptz,
    completed_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT uq_batch_account_results_batch_account UNIQUE (batch_id, ad_account_id),
    CONSTRAINT ck_batch_account_results_status CHECK (status IN ('pending', 'running', 'succeeded', 'failed', 'skipped')),
    CONSTRAINT ck_batch_account_results_attempts CHECK (attempts >= 0),
    CONSTRAINT ck_batch_account_results_request CHECK (jsonb_typeof(request_json) = 'object'),
    CONSTRAINT ck_batch_account_results_response CHECK (jsonb_typeof(response_json) = 'object')
);

CREATE INDEX idx_batch_account_results_batch_status ON batch_account_results (batch_id, status);
CREATE INDEX idx_batch_account_results_account_created ON batch_account_results (ad_account_id, created_at DESC);

CREATE TABLE published_objects (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    batch_id uuid NOT NULL REFERENCES batches(id) ON DELETE RESTRICT,
    batch_account_result_id uuid NOT NULL REFERENCES batch_account_results(id) ON DELETE RESTRICT,
    connection_id uuid NOT NULL REFERENCES meta_connections(id) ON DELETE RESTRICT,
    ad_account_id uuid NOT NULL REFERENCES ad_accounts(id) ON DELETE RESTRICT,
    object_type text NOT NULL,
    meta_object_id text NOT NULL,
    parent_meta_object_id text NOT NULL DEFAULT '',
    name text NOT NULL DEFAULT '',
    desired_status text NOT NULL DEFAULT '',
    effective_status text NOT NULL DEFAULT '',
    idempotency_key text NOT NULL,
    request_json jsonb NOT NULL DEFAULT '{}'::jsonb,
    response_json jsonb NOT NULL DEFAULT '{}'::jsonb,
    last_synced_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT uq_published_objects_account_type_meta UNIQUE (ad_account_id, object_type, meta_object_id),
    CONSTRAINT uq_published_objects_account_idempotency UNIQUE (ad_account_id, idempotency_key),
    CONSTRAINT ck_published_objects_type CHECK (object_type IN ('campaign', 'ad_set', 'creative', 'ad')),
    CONSTRAINT ck_published_objects_request CHECK (jsonb_typeof(request_json) = 'object'),
    CONSTRAINT ck_published_objects_response CHECK (jsonb_typeof(response_json) = 'object')
);

CREATE INDEX idx_published_objects_batch_type ON published_objects (batch_id, object_type);
CREATE INDEX idx_published_objects_result_type ON published_objects (batch_account_result_id, object_type);
CREATE INDEX idx_published_objects_connection_created ON published_objects (connection_id, created_at DESC);
CREATE INDEX idx_published_objects_parent ON published_objects (ad_account_id, parent_meta_object_id)
    WHERE parent_meta_object_id <> '';

CREATE TABLE jobs (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    connection_id uuid REFERENCES meta_connections(id) ON DELETE SET NULL,
    type text NOT NULL,
    status text NOT NULL DEFAULT 'pending',
    priority integer NOT NULL DEFAULT 0,
    payload jsonb NOT NULL DEFAULT '{}'::jsonb,
    dedupe_key text,
    attempts integer NOT NULL DEFAULT 0,
    max_attempts integer NOT NULL DEFAULT 5,
    available_at timestamptz NOT NULL DEFAULT now(),
    locked_by text NOT NULL DEFAULT '',
    locked_at timestamptz,
    locked_until timestamptz,
    last_error text NOT NULL DEFAULT '',
    finished_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT ck_jobs_status CHECK (status IN ('pending', 'running', 'succeeded', 'dead', 'cancelled')),
    CONSTRAINT ck_jobs_attempts CHECK (attempts >= 0 AND max_attempts > 0 AND attempts <= max_attempts),
    CONSTRAINT ck_jobs_payload CHECK (jsonb_typeof(payload) = 'object'),
    CONSTRAINT ck_jobs_lock CHECK (
        (status = 'running' AND locked_by <> '' AND locked_at IS NOT NULL AND locked_until IS NOT NULL)
        OR status <> 'running'
    )
);

CREATE UNIQUE INDEX uq_jobs_type_dedupe ON jobs (type, dedupe_key)
    WHERE dedupe_key IS NOT NULL;
CREATE INDEX idx_jobs_claim ON jobs (priority DESC, available_at, created_at)
    WHERE status = 'pending';
CREATE INDEX idx_jobs_expired_lease ON jobs (locked_until)
    WHERE status = 'running';
CREATE INDEX idx_jobs_connection_created ON jobs (connection_id, created_at DESC)
    WHERE connection_id IS NOT NULL;

CREATE TABLE insight_snapshots (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    connection_id uuid NOT NULL REFERENCES meta_connections(id) ON DELETE RESTRICT,
    ad_account_id uuid NOT NULL REFERENCES ad_accounts(id) ON DELETE RESTRICT,
    published_object_id uuid REFERENCES published_objects(id) ON DELETE SET NULL,
    meta_object_id text NOT NULL,
    level text NOT NULL,
    date_start date NOT NULL,
    date_stop date NOT NULL,
    window_start timestamptz NOT NULL,
    window_end timestamptz NOT NULL,
    account_timezone text NOT NULL DEFAULT '',
    attribution_setting text NOT NULL DEFAULT '',
    query_hash text NOT NULL,
    impressions bigint NOT NULL DEFAULT 0,
    reach bigint NOT NULL DEFAULT 0,
    clicks bigint NOT NULL DEFAULT 0,
    link_clicks bigint NOT NULL DEFAULT 0,
    landing_page_views numeric(24,8) NOT NULL DEFAULT 0,
    actions numeric(24,8) NOT NULL DEFAULT 0,
    conversions numeric(24,8) NOT NULL DEFAULT 0,
    leads numeric(24,8) NOT NULL DEFAULT 0,
    registrations numeric(24,8) NOT NULL DEFAULT 0,
    purchases numeric(24,8) NOT NULL DEFAULT 0,
    spend numeric(24,8) NOT NULL DEFAULT 0,
    frequency numeric(24,8) NOT NULL DEFAULT 0,
    ctr numeric(24,8) NOT NULL DEFAULT 0,
    cpc numeric(24,8) NOT NULL DEFAULT 0,
    cpm numeric(24,8) NOT NULL DEFAULT 0,
    cost_per_action numeric(24,8) NOT NULL DEFAULT 0,
    purchase_value numeric(24,8) NOT NULL DEFAULT 0,
    roas numeric(24,8) NOT NULL DEFAULT 0,
    breakdowns jsonb NOT NULL DEFAULT '{}'::jsonb,
    metrics jsonb NOT NULL DEFAULT '{}'::jsonb,
    raw_json jsonb NOT NULL DEFAULT '{}'::jsonb,
    fetched_at timestamptz NOT NULL DEFAULT now(),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT uq_insight_snapshots_query UNIQUE (
        connection_id, meta_object_id, level, query_hash, window_start, window_end
    ),
    CONSTRAINT ck_insight_snapshots_level CHECK (level IN ('account', 'campaign', 'adset', 'ad')),
    CONSTRAINT ck_insight_snapshots_dates CHECK (
        date_stop >= date_start AND window_end > window_start
    ),
    CONSTRAINT ck_insight_snapshots_counts CHECK (
        impressions >= 0 AND reach >= 0 AND clicks >= 0 AND link_clicks >= 0
    ),
    CONSTRAINT ck_insight_snapshots_breakdowns CHECK (jsonb_typeof(breakdowns) = 'object'),
    CONSTRAINT ck_insight_snapshots_metrics CHECK (jsonb_typeof(metrics) = 'object'),
    CONSTRAINT ck_insight_snapshots_raw CHECK (jsonb_typeof(raw_json) = 'object')
);

CREATE INDEX idx_insight_snapshots_object_window ON insight_snapshots
    (meta_object_id, level, window_end DESC);
CREATE INDEX idx_insight_snapshots_account_window ON insight_snapshots
    (ad_account_id, window_end DESC);
CREATE INDEX idx_insight_snapshots_published_window ON insight_snapshots
    (published_object_id, window_end DESC)
    WHERE published_object_id IS NOT NULL;
CREATE INDEX idx_insight_snapshots_fetched ON insight_snapshots (fetched_at DESC);
CREATE INDEX idx_insight_snapshots_metrics_gin ON insight_snapshots USING gin (metrics);

CREATE TABLE automation_rules (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    connection_id uuid NOT NULL REFERENCES meta_connections(id) ON DELETE RESTRICT,
    ad_account_id uuid REFERENCES ad_accounts(id) ON DELETE CASCADE,
    batch_id uuid REFERENCES batches(id) ON DELETE CASCADE,
    name text NOT NULL,
    status text NOT NULL DEFAULT 'active',
    scope_level text NOT NULL,
    action text NOT NULL DEFAULT 'pause',
    conditions jsonb NOT NULL DEFAULT '{}'::jsonb,
    lookback_seconds bigint NOT NULL,
    evaluation_interval_seconds bigint NOT NULL,
    grace_period_seconds bigint NOT NULL DEFAULT 0,
    minimum_spend numeric(24,8) NOT NULL DEFAULT 0,
    minimum_impressions bigint NOT NULL DEFAULT 0,
    timezone text NOT NULL DEFAULT '',
    next_evaluation_at timestamptz NOT NULL DEFAULT now(),
    last_evaluated_at timestamptz,
    last_triggered_at timestamptz,
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT ck_automation_rules_status CHECK (status IN ('active', 'disabled', 'archived')),
    CONSTRAINT ck_automation_rules_scope CHECK (scope_level IN ('account', 'campaign', 'adset', 'ad')),
    CONSTRAINT ck_automation_rules_action CHECK (action IN ('pause')),
    CONSTRAINT ck_automation_rules_intervals CHECK (
        lookback_seconds > 0 AND evaluation_interval_seconds > 0 AND grace_period_seconds >= 0
    ),
    CONSTRAINT ck_automation_rules_minimums CHECK (minimum_spend >= 0 AND minimum_impressions >= 0),
    CONSTRAINT ck_automation_rules_conditions CHECK (jsonb_typeof(conditions) = 'object'),
    CONSTRAINT ck_automation_rules_metadata CHECK (jsonb_typeof(metadata) = 'object'),
    CONSTRAINT ck_automation_rules_scope_target CHECK (ad_account_id IS NOT NULL OR batch_id IS NOT NULL)
);

CREATE INDEX idx_automation_rules_due ON automation_rules (next_evaluation_at, id)
    WHERE status = 'active';
CREATE INDEX idx_automation_rules_account ON automation_rules (ad_account_id, status)
    WHERE ad_account_id IS NOT NULL;
CREATE INDEX idx_automation_rules_batch ON automation_rules (batch_id, status)
    WHERE batch_id IS NOT NULL;
CREATE INDEX idx_automation_rules_conditions_gin ON automation_rules USING gin (conditions);

CREATE TABLE rule_evaluations (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    rule_id uuid NOT NULL REFERENCES automation_rules(id) ON DELETE CASCADE,
    published_object_id uuid REFERENCES published_objects(id) ON DELETE SET NULL,
    meta_object_id text NOT NULL DEFAULT '',
    status text NOT NULL,
    window_start timestamptz NOT NULL,
    window_end timestamptz NOT NULL,
    observed_metrics jsonb NOT NULL DEFAULT '{}'::jsonb,
    condition_results jsonb NOT NULL DEFAULT '{}'::jsonb,
    action_attempted boolean NOT NULL DEFAULT false,
    action_response jsonb NOT NULL DEFAULT '{}'::jsonb,
    error text NOT NULL DEFAULT '',
    evaluated_at timestamptz NOT NULL DEFAULT now(),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT ck_rule_evaluations_status CHECK (status IN (
        'no_match', 'matched', 'action_succeeded', 'action_failed', 'skipped', 'error'
    )),
    CONSTRAINT ck_rule_evaluations_window CHECK (window_end > window_start),
    CONSTRAINT ck_rule_evaluations_observed CHECK (jsonb_typeof(observed_metrics) = 'object'),
    CONSTRAINT ck_rule_evaluations_results CHECK (jsonb_typeof(condition_results) = 'object'),
    CONSTRAINT ck_rule_evaluations_response CHECK (jsonb_typeof(action_response) = 'object')
);

CREATE INDEX idx_rule_evaluations_rule_time ON rule_evaluations (rule_id, evaluated_at DESC);
CREATE INDEX idx_rule_evaluations_object_time ON rule_evaluations (published_object_id, evaluated_at DESC)
    WHERE published_object_id IS NOT NULL;
CREATE INDEX idx_rule_evaluations_action_failures ON rule_evaluations (evaluated_at DESC)
    WHERE status IN ('action_failed', 'error');

CREATE TABLE audit_events (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    connection_id uuid REFERENCES meta_connections(id) ON DELETE SET NULL,
    actor_type text NOT NULL,
    actor_id text NOT NULL DEFAULT '',
    action text NOT NULL,
    entity_type text NOT NULL,
    entity_id text NOT NULL DEFAULT '',
    severity text NOT NULL DEFAULT 'info',
    request_id text NOT NULL DEFAULT '',
    ip_address text NOT NULL DEFAULT '',
    before jsonb NOT NULL DEFAULT '{}'::jsonb,
    after jsonb NOT NULL DEFAULT '{}'::jsonb,
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT ck_audit_events_severity CHECK (severity IN ('info', 'warning', 'critical')),
    CONSTRAINT ck_audit_events_before CHECK (jsonb_typeof(before) = 'object'),
    CONSTRAINT ck_audit_events_after CHECK (jsonb_typeof(after) = 'object'),
    CONSTRAINT ck_audit_events_metadata CHECK (jsonb_typeof(metadata) = 'object')
);

CREATE INDEX idx_audit_events_entity ON audit_events (entity_type, entity_id, created_at DESC);
CREATE INDEX idx_audit_events_connection_time ON audit_events (connection_id, created_at DESC)
    WHERE connection_id IS NOT NULL;
CREATE INDEX idx_audit_events_request ON audit_events (request_id)
    WHERE request_id <> '';
CREATE INDEX idx_audit_events_critical ON audit_events (created_at DESC)
    WHERE severity = 'critical';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS audit_events;
DROP TABLE IF EXISTS rule_evaluations;
DROP TABLE IF EXISTS automation_rules;
DROP TABLE IF EXISTS insight_snapshots;
DROP TABLE IF EXISTS jobs;
DROP TABLE IF EXISTS published_objects;
DROP TABLE IF EXISTS batch_account_results;
DROP TABLE IF EXISTS batches;
DROP TABLE IF EXISTS media;
DROP TABLE IF EXISTS assets;
DROP TABLE IF EXISTS ad_accounts;
DROP TABLE IF EXISTS businesses;
DROP TABLE IF EXISTS oauth_sessions;
DROP TABLE IF EXISTS meta_connections;

-- +goose StatementEnd
