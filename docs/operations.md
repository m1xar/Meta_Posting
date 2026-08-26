# Operations

## Processes and persistence

Raze Posting runs as three long-lived Docker Compose services plus one
one-shot initializer:

- `api`: session-authenticated workspace and JSON API plus the public OAuth callback;
- `worker`: claims asynchronous jobs, evaluates campaign guards, and syncs Keitaro tracker statistics;
- `postgres`: PostgreSQL 18;
- `uploads-init`: prepares bind-mount ownership, then exits successfully.

Uploads are bind-mounted from `./uploads` into
`/var/lib/raze-posting/uploads`. PostgreSQL uses the named `postgres_data`
volume. Back up both; a database-only backup does not contain uploaded media.
Insights have no scheduled retention deletion and therefore remain in
PostgreSQL indefinitely unless an operator removes them.

The API is bound to `127.0.0.1:8080` by default and should be exposed only
through the existing TLS reverse proxy.

The containers run as UID/GID `10001:10001`. The `uploads-init` Compose
service prepares the bind-mount root with mode `0750` before either application
service starts. It changes only the mount root, not ownership recursively, so
it does not rewrite a restored media tree. On a fresh host it is also safe to
pre-create the directory explicitly:

```bash
sudo install -d -m 0750 -o 10001 -g 10001 /opt/raze-posting/uploads
```

When restoring files created under another UID/GID, audit and correct their
ownership as a separate, deliberate migration step. Do not add a recursive
`chown` to normal startup.

## Required configuration

Create `.env` from `.env.example`. Required secret or deployment-specific
values:

| Variable | Purpose |
|---|---|
| `DATABASE_URL` | PostgreSQL DSN used by API and worker; its password must match `POSTGRES_PASSWORD` (percent-encode it in the URL when needed) |
| `POSTGRES_PASSWORD` | database container password; use the same value in `DATABASE_URL` |
| `HOST_HTTP_PORT` | loopback host port published by Compose (default `8080`) |
| `TOKEN_ENCRYPTION_KEY` | base64-encoded 32-byte key for Meta tokens |
| `META_APP_ID` | Raze Meta app ID |
| `META_APP_SECRET` | Raze Meta app secret |
| `META_OAUTH_REDIRECT_URI` | exact callback registered in Meta |
| `META_LOGIN_CONFIG_ID` | Facebook Login for Business configuration |
| `WORKER_CONCURRENCY` | maximum number of jobs processed concurrently |
| `KEITARO_BASE_URL` | Keitaro tracker origin (empty disables tracker sync) |
| `KEITARO_API_KEY` | Keitaro Admin API key |

Default requested permissions are:

```text
ads_management,ads_read,business_management,pages_show_list,pages_read_engagement,pages_manage_ads
```

Add only permissions required by implemented scopes. This version does not need
`leads_retrieval`, messaging permissions, or `catalog_management`.

`TOKEN_ENCRYPTION_KEY` must remain stable while encrypted connections exist.
Rotating it requires a controlled token re-encryption procedure; simply
replacing it makes existing tokens unreadable.

`META_REQUEST_TIMEOUT` defaults to `30s` for ordinary Graph API calls.
Multipart image and video uploads use a separate HTTP client and
`META_UPLOAD_TIMEOUT`, which defaults to `30m`; increasing the upload timeout
does not make ordinary Graph calls wait longer.

## Shared callback cutover

The configured callback is:

```text
https://api.terahash.win/oauth/facebook/callback
```

The existing tracking service previously owned the same public route. As agreed
for this deployment, stop that project before routing port `8080` to Raze
Posting. Do not run both services behind the same callback path: OAuth `state`
is stored by the service that starts the login and cannot be consumed by the
other one.

Before the cutover:

1. record how the old service is started and how to restore it;
2. stop it cleanly;
3. confirm `127.0.0.1:8080` is free;
4. start Raze Posting;
5. verify `/readyz` locally and through HTTPS;
6. create a new OAuth session and test one connection.

No Meta dashboard callback change is needed when the exact URL remains the
same.

## Deployment

From the deployment directory:

```bash
docker compose config
docker compose up -d --build
docker compose ps
docker compose logs --tail=200 api worker postgres
```

`uploads-init` should exit successfully and remain in the `Exited (0)` state;
that is expected. Both `api` and `worker` wait for it before starting.

Verification:

```bash
curl --fail http://127.0.0.1:8080/readyz
curl --fail https://api.terahash.win/readyz
```

Authenticated check: sign in at `https://api.terahash.win/login` and confirm
the dashboard loads and `GET /app/api/overview` returns data for your user.

## Migrations

SQL migrations live in `migrations/` and are applied in order by Goose through
the application database layer. Check startup logs before accepting traffic;
an API process that cannot complete migrations must not be considered healthy.

Take a database backup before applying a new production migration. Rollback
procedures must be tested against the exact release; do not assume an
application downgrade is compatible with a newer schema.

## Health and monitoring

`GET /healthz` is a process-liveness check. `GET /readyz` also verifies the
database connection and is the correct check for Compose health, cutovers, and
traffic readiness. Operational monitoring should additionally watch:

- API and worker restart counts;
- PostgreSQL availability and volume space;
- uploads filesystem usage and inode count;
- jobs stuck in `running` beyond their lease;
- growth of `dead` jobs;
- connection `last_error`, token expiry, and last sync age;
- batches in `running` for unexpectedly long periods;
- Meta throttling and permission errors;
- guard evaluation lag and campaigns paused by guards;
- Keitaro tracker sync failures (job type `sync_tracker`).

The normal worker execution settings are controlled by:

- `WORKER_CONCURRENCY`;
- `WORKER_POLL_INTERVAL`;
- `INSIGHTS_POLL_INTERVAL`;
- `GUARD_EVALUATION_INTERVAL`;
- `KEITARO_POLL_INTERVAL`;
- `JOB_LEASE_DURATION`;
- `JOB_MAX_ATTEMPTS`.

Compose allows two minutes for the API and 35 minutes for the worker to stop
after `SIGTERM`. The longer worker window is a final safety margin for
in-flight Graph calls, multipart uploads, lease cleanup, and job
checkpointing; operators should use `docker compose stop` rather than killing
containers directly.

The supplied Nginx configuration applies a 16 MiB body limit by default and
raises it to `1032m` only on exact `POST /app/api/media`: the 1 GiB application file
limit plus the API's 8 MiB multipart-envelope allowance. Request buffering is
disabled only for that media route, preventing Nginx from first copying the
full upload to its temporary directory. Keep the upstream on HTTP/1.1 and
retain the 30-minute body/send/read timeouts when installing the configuration.

The same exact media location permits at most four concurrent uploads and 60
new upload requests per minute (with a 20-request burst) per source IP. These
limits do not throttle the transfer rate or shorten the 30-minute timeout of an
accepted upload. Clients should keep upload concurrency at four or below and
retry an HTTP `429` with backoff. If many trusted operators share one NAT
address, adjust the values deliberately while retaining both controls.

At the API layer, Fiber streams request bodies and defers multipart parsing
until after session authentication. Only one byte of a fixed-length request is
prefetched before middleware runs; chunked bodies are routed from their
headers. Authenticated multipart parsing remains bounded by `1032m`, and JSON
parsing is separately bounded at 16 MiB. Do not disable
`StreamRequestBody` or enable Fiber's automatic multipart pre-parser: either
change would allow an unauthenticated sender to consume the upload budget
before the session check.

The exact OAuth callback location has access logging disabled because its query
string contains a short-lived authorization code and opaque state; preserve
that location when merging with an existing virtual host.

## Backups

At minimum, back up:

- the PostgreSQL database;
- the uploads directory;
- deployment configuration excluding plaintext export to source control;
- the token-encryption key in a separate protected secret backup.

Restore testing matters: a database restored without the matching encryption
key cannot use existing Meta connections, and media records restored without
their files cannot publish those creatives.

## Security

- Operator sessions are HttpOnly cookies with CSRF tokens; serve the workspace
  over TLS only.
- Keep TLS termination enabled at Nginx.
- Do not log Authorization headers, Meta authorization codes, app secrets, or
  decrypted access tokens.
- Limit filesystem access to `.env`, PostgreSQL data, uploads, and backups.
- Use Meta's official OAuth flow only; never collect Meta passwords.
- Treat raw Graph responses and Insights as internal business data.

## Incident notes

For a failed batch, inspect in this order:

1. batch status and per-account results;
2. the associated job and `last_error`;
3. connection status/scopes and last successful sync;
4. account status, currency, capabilities, and selected assets;
5. Meta error code/subcode and request ID.

For a campaign a guard paused unexpectedly, open its checks on the campaigns
page (or `GET /app/api/campaigns`): each check stores the observed metrics and
the thresholds it was judged against. Resuming the campaign overrides the
failed check; later checkpoints still apply. If tracker metrics look empty,
verify the Keitaro sync job is running and the tracking links carry
`sub_id_7={{campaign.id}}` / `sub_id_3={{campaign.name}}`.
