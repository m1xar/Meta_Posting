# API guide

End-to-end flow against a running instance. The workspace UI at `/app` does
all of this for you; the guide shows the same calls for scripting and
debugging. The machine-readable contract is [`openapi/openapi.yaml`](../openapi/openapi.yaml)
(interactive UI at `GET /docs`).

All `/app/api` endpoints use the session cookie from `/auth/login`. Mutating
calls also need the `X-CSRF-Token` header carrying the `raze_csrf` cookie
value.

```bash
export BASE_URL=http://127.0.0.1:8080
JAR=$(mktemp)

# 1. Register (or login) and keep the cookies.
curl --fail-with-body -c "$JAR" -H 'Content-Type: application/json' \
  -d '{"login":"buyer1","password":"correct-horse-battery"}' \
  "$BASE_URL/auth/register"

CSRF=$(awk '$6=="raze_csrf" {print $7}' "$JAR")
```

## 2. Connect a Meta user

Open `GET /app/connect/meta` in the browser — it redirects into Facebook Login
for Business and returns to `/oauth/facebook/callback`, which creates the
connection and queues the first inventory sync automatically.

Re-sync later:

```bash
curl --fail-with-body -b "$JAR" -H "X-CSRF-Token: $CSRF" -X POST \
  "$BASE_URL/app/api/connections/<connection-id>/sync"
```

## 3. Inspect the workspace

```bash
curl --fail-with-body -b "$JAR" "$BASE_URL/app/api/overview"   # dashboard payload
curl --fail-with-body -b "$JAR" "$BASE_URL/app/api/launcher"   # connections, accounts, Pages, capabilities
```

## 4. Upload media (optional)

```bash
curl --fail-with-body -b "$JAR" -H "X-CSRF-Token: $CSRF" \
  -F connection_id=<connection-id> -F kind=image -F file=@creative.jpg \
  "$BASE_URL/app/api/media"
```

## 5. Launch a batch with a checkpoint guard

```bash
curl --fail-with-body -b "$JAR" -H "X-CSRF-Token: $CSRF" \
  -H 'Content-Type: application/json' -X POST "$BASE_URL/app/api/batches" -d '{
  "batch": {
    "connection_id": "<connection-id>",
    "name": "0826_DE_offer_1",
    "ad_account_ids": ["<account-uuid>", "<account-uuid>"],
    "idempotency_key": "0826_DE_offer_1-v1",
    "leave_paused": false,
    "hierarchy": {
      "campaign": {"name": "0826_DE_offer_1", "objective": "OUTCOME_SALES", "special_ad_categories": []},
      "ad_set": {
        "name": "0826_DE_offer_1 / Ad set",
        "billing_event": "IMPRESSIONS",
        "optimization_goal": "OFFSITE_CONVERSIONS",
        "destination_type": "WEBSITE",
        "daily_budget": 2000,
        "targeting": {"age_min": 18, "age_max": 65,
          "geo_locations": {"countries": ["DE"]},
          "publisher_platforms": ["facebook", "instagram"]}
      },
      "creative": {
        "name": "0826_DE_offer_1 / Creative",
        "object_story_spec": {"page_id": "<page-id>", "link_data": {
          "link": "https://tracker.example/?sub_id_3={{campaign.name}}&sub_id_7={{campaign.id}}",
          "message": "Primary text", "name": "Learn more",
          "call_to_action": {"type": "LEARN_MORE", "value": {"link": "https://tracker.example/"}}}}
      },
      "ad": {"name": "0826_DE_offer_1 / Ad"}
    },
    "media_bindings": [{"media_id": "<media-uuid>", "target": "/creative/object_story_spec/link_data/image_hash"}]
  },
  "checkpoints": [
    {"spend": 5,  "min_tracker_clicks": 20},
    {"spend": 15, "min_tracker_leads": 1},
    {"spend": 40, "min_tracker_sales": 1}
  ]
}'
```

`202 Accepted` returns the batch and the created guard. Poll the batch:

```bash
curl --fail-with-body -b "$JAR" "$BASE_URL/app/api/batches/<batch-id>"
```

Keep the tracking link tagged with `sub_id_3={{campaign.name}}` and
`sub_id_7={{campaign.id}}` — that is how Keitaro statistics come back to the
right campaign (see [guards.md](guards.md)).

## 6. Watch campaigns

```bash
curl --fail-with-body -b "$JAR" "$BASE_URL/app/api/campaigns"
curl --fail-with-body -b "$JAR" "$BASE_URL/app/api/accounts/<account-uuid>/stats"
```

Each campaign view carries the published object, the latest lifetime Insights
snapshot, the Keitaro roll-up, the guard that applies, and every checkpoint
outcome.

## 7. Manage live runs

```bash
# Pause / resume one campaign (resume overrides its failed checks):
curl -b "$JAR" -H "X-CSRF-Token: $CSRF" -X POST "$BASE_URL/app/api/campaigns/<id>/pause"
curl -b "$JAR" -H "X-CSRF-Token: $CSRF" -X POST "$BASE_URL/app/api/campaigns/<id>/resume"

# Edit the ladder on a live guard:
curl -b "$JAR" -H "X-CSRF-Token: $CSRF" -H 'Content-Type: application/json' \
  -X PATCH "$BASE_URL/app/api/guards/<guard-id>" \
  -d '{"checkpoints":[{"spend":10,"min_tracker_leads":2},{"spend":30,"min_tracker_sales":1}]}'

# Give one campaign its own rules (overrides the batch guard):
curl -b "$JAR" -H "X-CSRF-Token: $CSRF" -H 'Content-Type: application/json' \
  -X POST "$BASE_URL/app/api/campaigns/<id>/guard" \
  -d '{"checkpoints":[{"spend":20,"min_tracker_sales":1}]}'
```

## Error envelope

Every error is JSON:

```json
{"error": {"code": "invalid_request", "message": "...", "request_id": "...", "details": {"field": "checkpoints"}}}
```

`401 session_expired` means sign in again; `409 conflict` on registration
means the login is taken; `502 meta_api_error` carries the Graph error code
and transience flag in `details`.
