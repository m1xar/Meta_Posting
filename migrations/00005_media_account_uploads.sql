-- +goose Up
-- +goose StatementBegin

CREATE TABLE media_account_uploads (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    media_id uuid NOT NULL REFERENCES media(id) ON DELETE CASCADE,
    ad_account_id uuid NOT NULL REFERENCES ad_accounts(id) ON DELETE CASCADE,
    status text NOT NULL,
    meta_image_hash text NOT NULL DEFAULT '',
    meta_video_id text NOT NULL DEFAULT '',
    response_json jsonb NOT NULL DEFAULT '{}'::jsonb,
    last_error text NOT NULL DEFAULT '',
    last_checked_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT uq_media_account_uploads_media_account UNIQUE (media_id, ad_account_id),
    CONSTRAINT ck_media_account_uploads_status CHECK (status IN ('processing', 'ready', 'failed')),
    CONSTRAINT ck_media_account_uploads_identifier CHECK (
        (meta_image_hash <> '' AND meta_video_id = '')
        OR (meta_image_hash = '' AND meta_video_id <> '')
    ),
    CONSTRAINT ck_media_account_uploads_response CHECK (jsonb_typeof(response_json) = 'object')
);

CREATE INDEX idx_media_account_uploads_account_status
    ON media_account_uploads (ad_account_id, status, updated_at);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS media_account_uploads;

-- +goose StatementEnd
