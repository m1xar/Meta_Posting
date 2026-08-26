# Raze Posting

Multi-tenant service for connecting Meta users, publishing the same campaign
hierarchy to many ad accounts, collecting lifetime Insights, merging
per-campaign registration/deposit statistics from a Keitaro tracker, and
pausing underperforming campaigns with spend-checkpoint guards.

Operators create an account with a login and password and work in the browser
workspace at `/app`. Every Meta connection and all resources below it are
isolated by the owning user. Meta users are connected through official
Facebook Login for Business; Meta access tokens are never returned by the API
and are encrypted at rest.

## Scope

- official Meta OAuth and multiple Meta user connections per operator;
- discovery of businesses, ad accounts, Pages, Instagram accounts,
  pixels/datasets, custom conversions, audiences, and Meta apps;
- the six ODAX objectives and website/mobile-app destinations;
- single-image, video, carousel, flexible, and existing-post creatives;
- complete `Campaign → Ad Set → Creative → Ad` publishing with per-account
  overrides, account-currency budgets, partial batch success, and idempotent
  batch submission;
- lifetime Insights snapshots per published object;
- Keitaro tracker integration: per-campaign clicks, registrations (leads),
  deposits (sales), and revenue matched by `sub_id_7` (campaign id) with a
  `sub_id_3` (campaign name) fallback;
- guard automation: a ladder of spend checkpoints per batch or campaign — when
  lifetime spend crosses a checkpoint, minimum clicks/impressions/tracker
  metrics are verified and the campaign is paused if they are not met.

Facebook's native automated rules are not used; all automation runs inside the
worker. Instant Forms, click-to-message destinations, and catalogs are out of
scope.

## The four workspace pages

| Page | Purpose |
|---|---|
| `/app` | Dashboard: aggregate spend/revenue/regs/deposits, live campaigns, connection management |
| `/app/launch` | Launcher: publish one hierarchy to many accounts with a checkpoint ladder |
| `/app/campaigns` | All campaigns with statuses, metrics, guard progress; pause/resume and live rule editing |
| `/app/accounts/{id}` | One ad account: aggregate totals and its campaigns |

## Stack and layout

- Go 1.26, Fiber, GORM; PostgreSQL 18
- one API process and one database-backed worker with a bounded runner pool
- Goose SQL migrations; local persistent upload directory

| Path | Purpose |
|---|---|
| `cmd/api` | HTTP API + workspace UI entrypoint |
| `cmd/worker` | job runner and scheduler (sync, publish, insights, guards, tracker) |
| `internal/domain` | persisted domain model |
| `internal/meta` | Graph API, OAuth, discovery, publisher |
| `internal/keitaro` | Keitaro Admin API report client |
| `internal/application` | use cases: batches, insights, guards, tracker sync |
| `internal/httpapi` | HTTP handlers and the embedded workspace UI (`webui/`) |
| `migrations` | PostgreSQL migrations |
| `openapi/openapi.yaml` | OpenAPI 3.1 contract |
| `docs/` | API guide, guard behavior, operations notes |

## Local start

1. Copy `.env.example` to `.env`.
2. Replace every placeholder; set the Meta app values and (optionally) the
   Keitaro base URL + API key. Leaving Keitaro empty disables tracker syncing.
3. Start everything:

```bash
docker compose up -d --build
docker compose ps
curl --fail http://127.0.0.1:8080/readyz
```

Do not commit `.env`. Generate independent random values for the database
password and the 32-byte token-encryption key:

```bash
openssl rand -base64 32
```

Run the Go checks before deployment:

```bash
go test ./...
go vet ./...
```

## Authentication

The browser workspace uses a server-side, HttpOnly session cookie plus a
per-session CSRF token (`raze_csrf` cookie echoed in the `X-CSRF-Token` header
on mutating requests). Registration is at `GET /register`, sign-in at
`GET /login`.

`GET /healthz`, `GET /readyz`, `GET /docs`, `GET /openapi.yaml`, the legal
pages, and `GET /oauth/facebook/callback` are public. The callback is public
because Meta redirects the user's browser to it, but it is protected by the
short-lived, one-time OAuth state bound to the initiating user.

## Asynchronous behavior

Connection synchronization and batch publishing use the PostgreSQL-backed job
queue. An accepted response means the work was persisted, not completed. A
batch is deliberately not all-or-nothing: every selected ad account has its
own result, and successful accounts remain published when another fails.
During publishing, objects are created paused and activated bottom-up after
the full hierarchy exists; set `leave_paused: true` for a non-spending launch.

The worker also runs three recurring loops:

- **Insights** (`INSIGHTS_POLL_INTERVAL`): lifetime metrics per published object;
- **Guards** (`GUARD_EVALUATION_INTERVAL`): checkpoint evaluation and pausing;
- **Tracker** (`KEITARO_POLL_INTERVAL`): Keitaro report sync when configured.

## Documentation

Start with [docs/api-guide.md](docs/api-guide.md). Guard behavior is described
in [docs/guards.md](docs/guards.md), configuration and deployment in
[docs/operations.md](docs/operations.md). The machine-readable contract is
[openapi/openapi.yaml](openapi/openapi.yaml); a running API serves Swagger UI
at `GET /docs`.
