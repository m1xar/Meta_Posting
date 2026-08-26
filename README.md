# Raze Posting

Multi-tenant service for connecting Meta users, discovering their
advertising inventory, publishing the same campaign hierarchy to many ad
accounts, collecting Insights, merging per-campaign registration/deposit
statistics from a Keitaro tracker, and pausing underperforming campaigns
with spend-checkpoint guards.

Operators create an account with a login and password and use the browser
workspace at `/app`. Every Meta connection and all resources below it are
isolated by the owning user. The administrator bearer API remains available
for operations and compatibility. Meta users are connected through official
Facebook Login for Business; Meta access tokens are never returned by this API
and are encrypted at rest.

## Scope

The service covers:

- official Meta OAuth and multiple Meta user connections;
- discovery of businesses, ad accounts, Pages, Instagram accounts,
  pixels/datasets, custom conversions, audiences, and Meta apps;
- the six ODAX objectives:
  `OUTCOME_APP_PROMOTION`, `OUTCOME_AWARENESS`, `OUTCOME_ENGAGEMENT`,
  `OUTCOME_LEADS`, `OUTCOME_SALES`, and `OUTCOME_TRAFFIC`;
- website and mobile-app destinations;
- single-image, video, carousel, flexible, and caller-supplied existing-post
  creatives;
- complete `Campaign → Ad Set → Creative → Ad` publishing;
- per-account overrides, account-currency budgets, partial batch success, and
  idempotent batch submission;
- `ONLINE_GAMBLING_AND_GAMING` special-ad-category payloads;
- indefinitely stored Insights and account-wide daily metrics;
- Keitaro tracker integration: per-campaign clicks, registrations (leads),
  deposits (sales) and revenue, matched by `sub_id_7` (campaign id) with a
  `sub_id_3` (campaign name) fallback;
- guard automation: a ladder of lifetime-spend checkpoints per batch or
  campaign - when spend crosses a rung, minimum clicks/impressions/tracker
  metrics are verified and the campaign is paused if they are not met.
  Facebook's native automated rules are not used.

Instant Forms/lead retrieval, click-to-message destinations, and catalogs are
intentionally out of scope for this version.

There is no automatic Insights-retention cutoff in this release; records remain
until an operator explicitly removes them.

## Stack and layout

- Go 1.26, Fiber, GORM
- PostgreSQL 18
- one API process and one database-backed worker process with a bounded
  concurrent runner pool
- Goose SQL migrations
- local persistent upload directory

Important paths:

| Path | Purpose |
|---|---|
| `cmd/api` | HTTP API entrypoint |
| `cmd/worker` | asynchronous job worker |
| `internal/domain` | persisted domain model |
| `internal/meta` | Graph API, OAuth, discovery, and publisher |
| `internal/rules` | rule validation and evaluation |
| `migrations` | PostgreSQL migrations |
| `openapi/openapi.yaml` | OpenAPI 3.1 contract |
| `docs/api-guide.md` | end-to-end API examples |
| `docs/guards.md` | checkpoint-guard behavior and tracker matching |
| `internal/keitaro` | Keitaro Admin API report client |
| `docs/operations.md` | configuration and deployment notes |

## Local start

1. Copy `.env.example` to `.env`.
2. Replace every placeholder and set the Meta app values.
3. Start the API, worker, and PostgreSQL:

```bash
docker compose up -d --build
docker compose ps
curl --fail http://127.0.0.1:8080/readyz
```

Do not commit `.env`. Generate independent random values for the database
password, internal API token, and token-encryption key. One way to generate the
32-byte encryption key in base64 form is:

```bash
openssl rand -base64 32
```

Run the Go checks before deployment:

```bash
go test ./...
go vet ./...
```

## Authentication

The browser workspace uses a server-side, HttpOnly session cookie and a
per-session CSRF token. Registration is available at `GET /register`, sign-in
at `GET /login`, and the authenticated workspace at `GET /app`.

Every `/v1` endpoint requires:

```http
Authorization: Bearer <INTERNAL_API_TOKEN>
```

`GET /healthz`, `GET /readyz`, `GET /docs`, `GET /swagger`,
`GET /openapi.yaml`, and
`GET /oauth/facebook/callback` are public. The callback is public because Meta
redirects the user's browser to it, but it is protected by the short-lived,
one-time OAuth `state` created by `POST /v1/oauth/sessions`.

Example:

```bash
export BASE_URL=http://127.0.0.1:8080
export ADMIN_TOKEN='set-locally-do-not-commit'

curl --fail-with-body \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  "$BASE_URL/v1/connections"
```

## Asynchronous behavior

Connection synchronization and batch publishing use the PostgreSQL-backed job
queue. An accepted response means the work was persisted, not completed. Poll
the returned job, connection, batch, or per-account results until it reaches a
terminal status. Local media is uploaded to each Meta account as part of its
batch job.

A batch is deliberately not all-or-nothing. Every selected ad account has its
own result. Successful accounts remain published when another account fails,
and the error payload is retained for inspection and retry planning.

During publishing, objects are created paused and then activated bottom-up only
after the complete hierarchy for that account exists. Set `leave_paused: true`
when a caller explicitly wants a non-spending launch.

## Documentation

Start with [docs/api-guide.md](docs/api-guide.md). The machine-readable contract
is [openapi/openapi.yaml](openapi/openapi.yaml), and rule behavior is described
in [docs/guards.md](docs/guards.md). A running API also serves interactive
Swagger UI at `GET /docs` (with `GET /swagger` as an alias).
