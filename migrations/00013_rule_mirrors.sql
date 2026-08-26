-- +goose Up
-- +goose StatementBegin

-- Rules mirrored into Meta's own automated-rules library.
--
-- The mirror is a backstop, not the mechanism. This service evaluates its
-- rules every minute; Meta evaluates its own roughly every half hour, which is
-- far slower but keeps running when this service does not. Recording the pair
-- lets a disabled rule take its mirror down with it, so a guard cannot outlive
-- the intent behind it.
CREATE TABLE rule_mirrors (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    rule_id uuid NOT NULL REFERENCES automation_rules(id) ON DELETE CASCADE,
    ad_account_id uuid NOT NULL REFERENCES ad_accounts(id) ON DELETE CASCADE,
    meta_rule_id text NOT NULL,
    status text NOT NULL DEFAULT 'active',
    last_error text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT uq_rule_mirrors UNIQUE (rule_id, ad_account_id),
    CONSTRAINT ck_rule_mirrors_status CHECK (status IN ('active', 'removed', 'failed'))
);

CREATE INDEX idx_rule_mirrors_rule ON rule_mirrors (rule_id);
CREATE INDEX idx_rule_mirrors_active
    ON rule_mirrors (ad_account_id) WHERE status = 'active';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS rule_mirrors;

-- +goose StatementEnd
