-- +goose Up
-- +goose StatementBegin

CREATE TABLE asset_ad_accounts (
    asset_id uuid NOT NULL REFERENCES assets(id) ON DELETE CASCADE,
    ad_account_id uuid NOT NULL REFERENCES ad_accounts(id) ON DELETE CASCADE,
    connection_id uuid NOT NULL REFERENCES meta_connections(id) ON DELETE CASCADE,
    last_synced_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (asset_id, ad_account_id)
);

CREATE INDEX idx_asset_ad_accounts_account_asset
    ON asset_ad_accounts (ad_account_id, asset_id);
CREATE INDEX idx_asset_ad_accounts_connection
    ON asset_ad_accounts (connection_id, ad_account_id);

INSERT INTO asset_ad_accounts (asset_id, ad_account_id, connection_id, last_synced_at)
SELECT id, ad_account_id, connection_id, last_synced_at
FROM assets
WHERE ad_account_id IS NOT NULL
ON CONFLICT DO NOTHING;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS asset_ad_accounts;

-- +goose StatementEnd
