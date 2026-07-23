# API guide

This guide uses placeholders and environment variables only. Never paste a Meta
access token into an API request: the service obtains it through OAuth and
stores it encrypted.

```bash
export BASE_URL=https://api.terahash.win
export ADMIN_TOKEN='internal-admin-token'
```

All collection endpoints use offset pagination:

- `limit`: default `50`, maximum `500`;
- `offset`: default `0`;
- response shape: `{"items": [...], "total": N, "limit": 50, "offset": 0}`.

Filters may be combined. Repeated enum filters use comma-separated query values
where documented in OpenAPI.

JSON request bodies are strict: unknown fields, trailing JSON values, and an
incorrect `Content-Type` are rejected. Errors use:

```json
{
  "error": {
    "code": "invalid_request",
    "message": "human-readable summary",
    "request_id": "request-correlation-id",
    "details": {"field": "field_name"}
  }
}
```

Send `X-Request-ID` when correlating an internal workflow; otherwise the API
generates one and returns it in the response header.

## 1. Connect a Meta user

Create a short-lived OAuth session:

```bash
curl --fail-with-body \
  -X POST "$BASE_URL/v1/oauth/sessions" \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{}'
```

The response contains `authorization_url`. It expires after 30 minutes. Open
that URL in the browser where the intended Meta user is signed in. Meta
redirects to the configured `/oauth/facebook/callback`; the callback consumes
the one-time `state`, exchanges the code, records the connection, and queues the
first inventory sync. It does not expose the Meta token.

List connections:

```bash
curl --fail-with-body \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  "$BASE_URL/v1/connections?limit=50&offset=0"
```

Request a fresh inventory synchronization:

```bash
curl --fail-with-body \
  -X POST "$BASE_URL/v1/connections/CONNECTION_UUID/sync" \
  -H "Authorization: Bearer $ADMIN_TOKEN"
```

Synchronization is asynchronous. Poll the returned job and then inspect
`last_synced_at` and `last_error` on the connection.

## 2. Select ad accounts and assets

Find active USD accounts belonging to a connection:

```bash
curl --fail-with-body \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  "$BASE_URL/v1/ad-accounts?connection_id=CONNECTION_UUID&account_status=1&currency=USD&active_only=true&search=casino&limit=100"
```

`active_only` defaults to `true`; use `false` only for historical or
diagnostic views of accounts that Meta no longer returns for the connection.

Assets available to a selected account:

```bash
curl --fail-with-body \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  "$BASE_URL/v1/assets?connection_id=CONNECTION_UUID&ad_account_id=AD_ACCOUNT_UUID&types=page,instagram_account,pixel,dataset,custom_conversion,custom_audience,lookalike_audience,meta_app&active_only=true"
```

The `id` fields are local UUIDs. Fields named `meta_*_id` are Meta identifiers.
Batch account selection uses local ad-account UUIDs; creative and promoted
object payloads use the relevant Meta identifiers.

## 3. Upload media

Upload one image or video to the persistent local upload directory:

```bash
curl --fail-with-body \
  -X POST "$BASE_URL/v1/media" \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  -F "file=@/absolute/path/creative.mp4" \
  -F "kind=video" \
  -F "connection_id=CONNECTION_UUID" \
  -F "ad_account_id=AD_ACCOUNT_UUID"
```

Inspect the stored record:

```bash
curl --fail-with-body \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  "$BASE_URL/v1/media/MEDIA_UUID"
```

The upload is local and becomes `ready`; it is not yet uploaded to Meta. Bind
the local media UUID in the batch request. The worker uploads it independently
to every selected account and writes the account-specific image hash or video
ID into the hierarchy:

```json
{
  "media_bindings": [
    {
      "media_id": "MEDIA_UUID",
      "target": "/creative/object_story_spec/video_data/video_id"
    }
  ]
}
```

For an image, a typical target is
`/creative/object_story_spec/link_data/image_hash`. Targets are RFC 6901 JSON
pointers and must be below `/creative`.

## 4. Inspect campaign capabilities

The capabilities endpoint exposes the API's typed vocabulary and important
combinations without requiring callers to hard-code enum lists:

```bash
curl --fail-with-body \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  "$BASE_URL/v1/capabilities"
```

Meta may still reject a combination because capabilities, policies, billing,
country availability, and permissions differ by account. Treat this endpoint
as the service contract, not as a promise that every account is eligible for
every option.

## 5. Publish to multiple accounts

The example below creates a website-sales hierarchy with one image and marks it
as online gambling/gaming. Budget integers are minor units in each account's
currency. There is no currency conversion.

```bash
curl --fail-with-body \
  -X POST "$BASE_URL/v1/batches" \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "connection_id": "CONNECTION_UUID",
    "name": "UAE web registration test",
    "idempotency_key": "launch-2026-07-23-a",
    "ad_account_ids": [
      "AD_ACCOUNT_UUID_1",
      "AD_ACCOUNT_UUID_2"
    ],
    "leave_paused": false,
    "media_bindings": [
      {
        "media_id": "MEDIA_UUID",
        "target": "/creative/object_story_spec/link_data/image_hash"
      }
    ],
    "hierarchy": {
      "campaign": {
        "name": "UAE | Web | Registration",
        "objective": "OUTCOME_SALES",
        "special_ad_categories": ["ONLINE_GAMBLING_AND_GAMING"]
      },
      "ad_set": {
        "name": "AE | Broad | 21+",
        "billing_event": "IMPRESSIONS",
        "optimization_goal": "OFFSITE_CONVERSIONS",
        "destination_type": "WEBSITE",
        "daily_budget": 10000,
        "promoted_object": {
          "pixel_id": "META_PIXEL_ID",
          "custom_event_type": "COMPLETE_REGISTRATION"
        },
        "targeting": {
          "age_min": 21,
          "age_max": 65,
          "geo_locations": {"countries": ["AE"]},
          "publisher_platforms": ["facebook", "instagram"]
        }
      },
      "creative": {
        "name": "Image A",
        "object_story_spec": {
          "page_id": "META_PAGE_ID",
          "instagram_user_id": "META_INSTAGRAM_ACCOUNT_ID",
          "link_data": {
            "link": "https://example.invalid/landing",
            "message": "Primary text",
            "name": "Headline",
            "call_to_action": {
              "type": "SIGN_UP",
              "value": {"link": "https://example.invalid/landing"}
            }
          }
        },
        "url_tags": "utm_source=meta&utm_campaign={{campaign.id}}"
      },
      "ad": {
        "name": "Image A",
        "conversion_domain": "example.invalid"
      }
    },
    "account_overrides": {
      "AD_ACCOUNT_UUID_2": {
        "ad_set": {"daily_budget": 25000}
      }
    }
  }'
```

Reusing the same connection and `idempotency_key` returns the existing batch
instead of creating a duplicate launch.

The API accepts the batch once it and its per-account work are durable. Poll:

```bash
curl --fail-with-body \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  "$BASE_URL/v1/batches/BATCH_UUID"

curl --fail-with-body \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  "$BASE_URL/v1/batches/BATCH_UUID/results?limit=100"

curl --fail-with-body \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  "$BASE_URL/v1/published-objects?batch_id=BATCH_UUID&object_types=campaign,ad_set,creative,ad&limit=100"
```

Terminal batch statuses are `succeeded`, `partially_succeeded`, `failed`, and
`cancelled`. Per-account error codes and messages are retained. Do not resubmit
with a new idempotency key merely because a poll timed out.

### App destination

For app promotion, use the advertised app's Meta App ID and store URL:

```json
{
  "campaign": {
    "name": "Android app acquisition",
    "objective": "OUTCOME_APP_PROMOTION",
    "special_ad_categories": ["ONLINE_GAMBLING_AND_GAMING"]
  },
  "ad_set": {
    "name": "Android installs",
    "destination_type": "APP",
    "optimization_goal": "APP_INSTALLS",
    "billing_event": "IMPRESSIONS",
    "daily_budget": 10000,
    "promoted_object": {
      "application_id": "ADVERTISED_META_APP_ID",
      "object_store_url": "https://play.google.com/store/apps/details?id=example"
    },
    "targeting": {
      "geo_locations": {"countries": ["AE"]},
      "user_os": ["Android"]
    }
  }
}
```

The Raze Meta app authorizes ad management. It does not replace the Meta App ID
of the mobile app being advertised.

### Other creative shapes

- Video: `object_story_spec.video_data.video_id`.
- Carousel: `object_story_spec.link_data.child_attachments` with at least two
  cards.
- Flexible: `asset_feed_spec` with images/videos, bodies, titles, link URLs,
  formats, and optional customization rules.
- Existing post: `object_story_id`; do not combine it with
  `object_story_spec` or `asset_feed_spec`.

Existing-post IDs are supplied by the caller; the current inventory sync does
not enumerate every Page/Instagram post.

The full schemas are in `openapi/openapi.yaml`.

### Forward-compatible `raw` fields

Campaign, ad-set, targeting, promoted-object, creative, asset-feed, and ad
specs expose a `raw` object for Meta fields not yet represented by a typed
property. The publisher deep-merges `raw` over typed values. Use it only after
validating the field against the configured Graph API version and a test
account.

The service still controls dependency IDs, the uniquely tagged Meta object
names, and status. It creates the hierarchy in `PAUSED`, so `raw` cannot bypass
the safe create-then-activate sequence. It also cannot bypass Meta permissions,
account capabilities, policy review, or payload validation.

### Validation-only scope

`validate_only=true` performs Meta Graph `validate_only` calls for the campaign
and creative plus complete local schema validation for the ad set and ad. Meta
requires real dependency IDs to Graph-validate the latter two; the service does
not create temporary billable hierarchy objects during a dry run. The result
stages explicitly distinguish `validated` from `locally_validated`.

## 6. Create a pause rule

This rule pauses a campaign when spend is at least 100 account-currency units
and registrations remain below one in a rolling 24-hour window, after a
one-hour grace period:

```bash
curl --fail-with-body \
  -X POST "$BASE_URL/v1/rules" \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "connection_id": "CONNECTION_UUID",
    "batch_id": "BATCH_UUID",
    "name": "Stop no registrations after spend",
    "status": "disabled",
    "scope_level": "campaign",
    "action": "pause",
    "lookback_seconds": 86400,
    "evaluation_interval_seconds": 900,
    "grace_period_seconds": 3600,
    "minimum_spend": 100,
    "minimum_impressions": 1000,
    "timezone": "Asia/Dubai",
    "conditions": {
      "logic": "all",
      "conditions": [
        {"metric": "spend", "operator": "gte", "threshold": 100},
        {
          "metric": "actions.complete_registration",
          "operator": "lt",
          "threshold": 1,
          "missing_as_zero": true
        }
      ]
    }
  }'
```

See [rule-dsl.md](rule-dsl.md) before using action or conversion metrics.

Review the configuration, then enable its scheduled evaluation:

```bash
curl --fail-with-body \
  -X POST "$BASE_URL/v1/rules/RULE_UUID/enable" \
  -H "Authorization: Bearer $ADMIN_TOKEN"
```

Scheduled active rules execute their configured action when all eligibility
checks and conditions pass. Inspect the audit records:

```bash
curl --fail-with-body \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  "$BASE_URL/v1/rules/RULE_UUID/evaluations?limit=100"
```

## 7. Query stored Insights

```bash
curl --fail-with-body \
  -G "$BASE_URL/v1/insights" \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  --data-urlencode "connection_id=CONNECTION_UUID" \
  --data-urlencode "ad_account_id=AD_ACCOUNT_UUID_1" \
  --data-urlencode "level=campaign" \
  --data-urlencode "window_start=2026-07-01T00:00:00Z" \
  --data-urlencode "window_end=2026-07-23T23:59:59Z" \
  --data-urlencode "limit=500"
```

Insights are returned from persisted snapshots. Their freshness is visible in
`fetched_at`; querying does not imply that Meta has just been called.

## 8. Inspect jobs

```bash
curl --fail-with-body \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  "$BASE_URL/v1/jobs?statuses=dead&limit=100"
```

Inspect `last_error`, connection state, and the owning batch or media item when
a job reaches `dead`. Automatic retries stop after `max_attempts`; this API
contract does not expose a manual job retry operation.
