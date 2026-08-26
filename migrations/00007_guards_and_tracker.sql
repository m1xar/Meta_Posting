-- +goose Up
-- +goose StatementBegin

-- The free-form rule DSL is replaced by spend-checkpoint guards.
DROP TABLE IF EXISTS rule_evaluations;
DROP TABLE IF EXISTS automation_rules;

CREATE TABLE campaign_guards (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    connection_id uuid NOT NULL REFERENCES meta_connections(id) ON DELETE CASCADE,
    batch_id uuid REFERENCES batches(id) ON DELETE CASCADE,
    published_object_id uuid REFERENCES published_objects(id) ON DELETE CASCADE,
    name text NOT NULL,
    status text NOT NULL DEFAULT 'active',
    checkpoints jsonb NOT NULL DEFAULT '[]',
    evaluation_interval_seconds bigint NOT NULL DEFAULT 300,
    next_evaluation_at timestamptz NOT NULL DEFAULT now(),
    last_evaluated_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT ck_campaign_guards_status CHECK (status IN ('active', 'disabled')),
    CONSTRAINT ck_campaign_guards_scope CHECK (batch_id IS NOT NULL OR published_object_id IS NOT NULL)
);

CREATE INDEX idx_campaign_guards_due ON campaign_guards (next_evaluation_at) WHERE status = 'active';
CREATE INDEX idx_campaign_guards_connection ON campaign_guards (connection_id, created_at DESC);
CREATE INDEX idx_campaign_guards_batch ON campaign_guards (batch_id) WHERE batch_id IS NOT NULL;
CREATE UNIQUE INDEX uq_campaign_guards_object ON campaign_guards (published_object_id) WHERE published_object_id IS NOT NULL;

CREATE TABLE guard_checks (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    guard_id uuid NOT NULL REFERENCES campaign_guards(id) ON DELETE CASCADE,
    published_object_id uuid NOT NULL REFERENCES published_objects(id) ON DELETE CASCADE,
    meta_object_id text NOT NULL,
    checkpoint_index integer NOT NULL,
    checkpoint_spend numeric(24,8) NOT NULL,
    status text NOT NULL,
    observed jsonb NOT NULL DEFAULT '{}',
    thresholds jsonb NOT NULL DEFAULT '{}',
    paused boolean NOT NULL DEFAULT false,
    error text NOT NULL DEFAULT '',
    evaluated_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT ck_guard_checks_status CHECK (status IN ('passed', 'failed', 'overridden'))
);

CREATE UNIQUE INDEX uq_guard_checks_checkpoint ON guard_checks (guard_id, published_object_id, checkpoint_index);
CREATE INDEX idx_guard_checks_object ON guard_checks (published_object_id, checkpoint_index);

CREATE TABLE tracker_stats (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    connection_id uuid REFERENCES meta_connections(id) ON DELETE CASCADE,
    published_object_id uuid REFERENCES published_objects(id) ON DELETE CASCADE,
    meta_campaign_id text NOT NULL DEFAULT '',
    campaign_name text NOT NULL DEFAULT '',
    clicks bigint NOT NULL DEFAULT 0,
    unique_clicks bigint NOT NULL DEFAULT 0,
    leads numeric(24,8) NOT NULL DEFAULT 0,
    sales numeric(24,8) NOT NULL DEFAULT 0,
    revenue numeric(24,8) NOT NULL DEFAULT 0,
    raw jsonb NOT NULL DEFAULT '{}',
    last_synced_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT ck_tracker_stats_key CHECK (meta_campaign_id <> '' OR campaign_name <> '')
);

CREATE UNIQUE INDEX uq_tracker_stats_campaign ON tracker_stats (meta_campaign_id, campaign_name);
CREATE INDEX idx_tracker_stats_object ON tracker_stats (published_object_id) WHERE published_object_id IS NOT NULL;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS tracker_stats;
DROP TABLE IF EXISTS guard_checks;
DROP TABLE IF EXISTS campaign_guards;
-- +goose StatementEnd
