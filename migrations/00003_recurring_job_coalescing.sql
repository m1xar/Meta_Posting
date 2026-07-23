-- +goose Up
-- +goose StatementBegin

CREATE UNIQUE INDEX uq_jobs_recurring_active
    ON jobs (connection_id, type)
    WHERE connection_id IS NOT NULL
      AND type IN ('collect_insights', 'evaluate_rules')
      AND status IN ('pending', 'running');

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS uq_jobs_recurring_active;

-- +goose StatementEnd
