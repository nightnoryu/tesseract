# Tesseract

AI-assisted self-service BI for marketing and sales. PostgreSQL will hold
application state and schema metadata; analytical queries will run directly in
the configured warehouse. See [the roadmap](tesseract_roadmap.md).

## Local development

Install mise and Docker with Compose. From the repository root:

```sh
mise trust
mise install
cp .env.example .env
mise run build
mise run check
mise run dev
```

`dev` starts PostgreSQL and runs Go and Vite in parallel. Open
http://localhost:5173 for the React preview; http://localhost:8080 returns a plain
text backend startup response. Ctrl-C stops the local processes; PostgreSQL
continues running until `mise run dev:down`. Restart `backend:dev` after Go edits;
Vite applies frontend edits automatically.

```sh
mise run dev:mssql   # optional local SQL Server (accepts Microsoft's EULA)
mise run dev:ps
mise run dev:logs
mise run dev:down    # keeps database volumes
```

SQL Server Developer edition requires an amd64 Docker runtime and at least 2 GB
of memory. Its SA password in `.env` is for local administration. Configure
`TESSERACT_DWH_CRM_DSN` with a separate read-only account. PostgreSQL is exposed on localhost:5433 and SQL Server on
localhost:1433; all credentials and port overrides are in `.env.example`.
Changing database credentials after initialization requires updating the database
account too; editing `.env` alone does not change persisted passwords.

## Workspace

- `backend/cmd/tesseract`: Go API with PostgreSQL startup migrations; output in `backend/bin/`.
- `web/`: React, TypeScript, Vite, Tailwind; output in `web/dist/`.
- `mise.toml`: pinned toolchain and build, check, and development tasks.
- `compose.yml`: PostgreSQL and optional SQL Server with persistent volumes.
- [Decisions](docs/decisions.md): layout adaptation from Lumio and scope choices.

## Backend operations

The API connects to PostgreSQL and applies embedded go-kita migrations before
opening its HTTP listener. Startup fails with a safe error code if configuration,
connectivity, or migrations fail. The pool allows 10 open and 2 idle connections;
startup has a 30-second deadline, with a 5-second initial connection timeout.
SIGINT/SIGTERM drains HTTP requests for up to 10 seconds before closing the pool.

`TESSERACT_DATABASE_URL` accepts a PostgreSQL URL without query options. For
existing milestone 1 `.env` files, remove `?sslmode=disable` from the URL and add
`PGSSLMODE=disable` for local development. For remote deployments, set
`PGSSLMODE=verify-full` and `PGSSLROOTCERT` to the CA certificate path. go-kita's
DSN type cannot preserve URL query parameters, so these are rejected explicitly.

```sh
curl -i http://localhost:8080/healthz
curl -i http://localhost:8080/readyz
curl http://localhost:8080/metrics
mise run backend:check
mise run backend:integration  # requires Docker; isolated PostgreSQL container
```

`/healthz` reports process liveness. `/readyz` performs a database ping with a
one-second deadline and returns 503 on failure or shutdown. Its separate `dwh`
field is `not_configured` until milestone 5. `/metrics` exposes basic Go/process,
HTTP count/duration/in-flight, and LLM count/duration/error metrics. LLM series
remain zero with fixed `unconfigured` labels until the provider is implemented.
Keep operational endpoints on a trusted network when deploying.

JSON logs include server-generated `request_id` (also returned as `X-Request-ID`),
route template, status, duration, and safe error code. Request IDs are available
in handler context for downstream correlation. Headers, query strings, request
bodies, raw errors, and unmatched paths are not logged. The API deliberately
replaces caller-supplied request IDs. No user-controlled values become metric
labels.

Embedded migrations create `tesseract_app`; future application tables should use
that schema explicitly. go-kita manages its migration history and advisory lock.
The integration test verifies migrations across two startups and readiness after
PostgreSQL stops. Unit tests cover log/metric privacy, panic sanitization,
readiness, draining, and rejected connection options.

## License

[MIT](LICENSE).

## API contract

`api/openapi.yaml` is the canonical contract. ogen 1.24.0 generates the Go
server, client, and DTOs in `backend/internal/generated/api`; openapi-typescript
7.13.0 generates `web/src/api/schema.d.ts`. The frontend uses those generated
paths with openapi-fetch. Generated bindings are ignored by Git; build, check,
and dev tasks generate them automatically. Never edit generated files manually.

```sh
mise run api:generate   # after editing the contract
mise run api:check      # regenerate into a temporary directory and compare
mise run build
mise run check         # types, Go tests, formatting, vet, generation
```

CI runs the consistency check and builds/checks. Comparison includes extra and
missing generated files and verifies reproducibility after generation. The React app
requests `/api/v1/catalog` through the typed client and shows catalog availability. Vite proxies `/api`, `/healthz`, and `/readyz` to port 8080; production
hosting must route these paths to Go on the same origin.

Mise tracks sources and outputs for dependency installation, API generation,
builds, and local checks, skipping tasks whose inputs have not changed. Backend
tasks generate only Go bindings; web tasks generate only TypeScript bindings.
`build` and `check` share a single `web:typecheck` task. Use
`mise run --force <task>` to rerun a task and its dependencies regardless of
freshness. Development processes, Compose operations, and integration tests
always run because they depend on live services or environment settings.

Health and readiness use generated handlers. `/metrics` remains a Prometheus
endpoint outside the JSON contract. Errors use
`{"error":{"code":"not_implemented","message":"..."},"request_id":"..."}`,
with the same server-generated ID in `X-Request-ID`.

Password authentication is implemented. Dashboard, sharing and chat
handlers remain reserved and return 501 after authentication; unauthenticated
requests return 401. No raw SQL request interface is exposed.

## Semantic catalog

```sh
mise run catalog:validate
# Or from backend/:
go run ./cmd/catalog validate -dir ../catalog
go build -o bin/catalog ./cmd/catalog
```

The validator exits nonzero for invalid definitions and prints source/dataset
counts and a deterministic SHA-256 on success. It needs no database or secrets.
Edit `catalog/sources.toml` (required `version = 1`) and `catalog/datasets/*.toml`.
The leads example documents the schema: explicit column exposure, named measures,
dimensions, filter operators, time grains, descriptions and optional synonyms.
Unknown keys, SQL formulas, unknown references, unsupported types/operations and
unsafe identifiers are rejected. Omitted operators allow no filters. See
[the catalog decisions](docs/decisions.md) for the supported vocabulary.

`infra/fixtures/leads.sql` contains matching synthetic SQL Server data. Run it
explicitly with a SQL Server client supporting `GO`, against an empty local
DWH as its administrator; it is not an application migration. Catalog validation
alone does not mean a table exists, is compatible, or is authorized for a user.

### DWH metadata probe (milestone 5)

The SQL Server adapter uses the catalog source kind and resolves its `secret_env`.
Provision a dedicated SQL Server login/user in the DWH, add it to
`db_denydatawriter`, and grant `SELECT` only on catalog-approved tables. Do not
use `sa`, `db_owner`, write-capable roles, schema administration, or procedure
grants. For example, after creating the login and database user:

```sql
ALTER ROLE db_denydatawriter ADD MEMBER tesseract_reader;
GRANT SELECT ON OBJECT::mart.MarketingLeads TO tesseract_reader;
```

Set `TESSERACT_DWH_CRM_DSN` to a driver URL such as
`sqlserver://tesseract_reader:<URL-escaped-password>@localhost:1433?database=warehouse&encrypt=true`.
Use a trusted server certificate in production. For the local self-signed server,
add `TrustServerCertificate=true`. Apply `infra/fixtures/leads.sql` with an admin
account in an empty development database first.

```sh
cd backend
go run ./cmd/dwh-probe -catalog ../catalog -source crm_dwh -dataset leads
# Must fail with dwh_object_not_cataloged:
go run ./cmd/dwh-probe -catalog ../catalog -source crm_dwh -dataset unlisted
# Real SQL Server test, Docker required:
go test -tags=integration ./internal/source/... -count=1
```

The probe prints approved column names/types and table presence, never business
rows. Defaults: 10s connect, 30s operation, 4 open/2 idle connections, 4 concurrent
operations, 1,000 result rows/groups. Use `-help` for bounded overrides. Unsupported
source kinds fail explicitly. Analytical requests remain disabled until milestone
8 adds schema compatibility and entitlement validation.

### Metadata ingestion

With the database and a restricted DWH principal configured in `.env`, run
`mise run ingest:once` for a manual scan or `mise run ingest:worker` for
background scans. The worker loads the validated catalog at startup; restart it
after catalog changes. Direct CLI usage from `backend`:

```sh
go run ./cmd/ingest -once -catalog ../catalog
go run ./cmd/ingest -interval 15m -scan-timeout 5m -source crm_dwh
```

The CLI reads `TESSERACT_DATABASE_URL`, PostgreSQL TLS settings, and each
catalog source's DSN environment variable. Sources and tables run sequentially
(concurrency one), with no immediate retries; the next scheduled cycle retries.
A PostgreSQL transaction lock prevents overlapping scans of the same source
across processes, including scans with different catalog hashes. Inspection has
a per-source deadline plus 30 seconds for atomic publication.

`tesseract_app.schema_current` points to a complete snapshot for each
source/catalog hash. Join it to `schema_scans`, `schema_tables`, and
`schema_columns` to inspect results. Empty reason means compatible; failures
record `missing_table`, `missing_column`, `incompatible_type`, or
`scan_failed`. A published scan can contain unavailable tables; publication
success does not mean all tables are compatible. Connector errors are sanitized
to `scan_failed`, since the connector does not distinguish access denial from
other connection failures. No business rows are queried or stored.

Snapshots retain history. Consumers must match the current catalog hash and
apply a freshness policy (availability API follows in milestone 7). A cancelled
worker or PostgreSQL outage cannot publish a new snapshot; the previous scan's
timestamp remains unchanged.

### Catalog availability

Provision an account as described below, restart the API, run `mise run ingest:once`, and sign in to the web app to load or refresh source counts and table statuses.

`GET /api/v1/catalog` requires a valid session cookie and returns the current catalog hash, per-source configured/available/unavailable/stale counts, latest and last successful scan times, and approved table statuses. No DWH connection is made by this endpoint. `TESSERACT_CATALOG_DIR` defaults to `../catalog` when running from `backend`. `TESSERACT_METADATA_MAX_AGE` defaults to `15m` and must be positive and at most `24h`; keep the ingestion interval below it.

Expired compatible inspections become unavailable with reason `stale`. Existing failure reasons remain visible even when stale. `not_scanned` means no matching current-catalog inspection exists, including after a catalog change. Failed scans never fall back to an older successful snapshot. `missing_table` can also mean SQL Server hid the object from the inspection account; explicit permission errors use `access_denied`.

Milestone 7 verification:

```sh
cd backend
GOCACHE=/tmp/tesseract-go-cache mise exec -- go test -tags=integration ./... -count=1
GOCACHE=/tmp/tesseract-go-cache mise exec -- go vet ./...
cd ..
mise exec -- pnpm --dir web run build
mise exec -- bash scripts/check-api.sh
```

The integration suite requires Docker and runs real PostgreSQL and SQL Server containers, including ingestion through the authenticated availability API, missing tables/columns, type drift, DWH downtime, catalog hash invalidation, and unlisted-object exclusion.

### Direct DWH aggregate queries

After a successful `mise run ingest:once`, a local operator with configured
`TESSERACT_DATABASE_URL` and DWH credentials can run a strict QuerySpec from stdin:

```sh
cd backend
# Export the database environment variables from your local configuration first.
printf '%s\n' '{"dataset":"leads","metrics":["lead_count"],"dimensions":[{"id":"channel"}],"order":[{"field":"lead_count","direction":"desc"}],"limit":10}' |
  go run ./cmd/query -catalog ../catalog -user YOUR_LOCAL_USER_UUID
```

Output is JSON with semantic `columns` and aggregate `rows`, read directly from
SQL Server. Integer and decimal cells are exact strings to preserve browser precision. The operator command resolves the selected user from the access policy; missing grants, stale/unverified metadata (15 minutes), undocumented fields,
and live schema incompatibility are denied. Add filter-only dimensions to the
field grant. Filters use typed values, for example
`{"dimension":"channel","operator":"eq","values":[{"type":"string","value":"paid"}]}`.
Time dimensions require a catalog-approved `grain`; `date_range` contains a
dimension and typed `start`/`end` values and uses an exclusive end boundary.
See `QuerySpec` in `api/openapi.yaml` for the complete shape. Signed-in users can POST this same JSON to `/api/v1/queries` with the configured Origin header; the backend resolves their data policy from the session identity.

Verification: `cd backend && go test ./... && go vet ./...`; real PostgreSQL +
SQL Server aggregation and drift checks: `go test -tags=integration ./... -count=1`.
Run `mise run api:check` and `mise run web:check` for generated contract consistency
and frontend type checking.

## Password accounts

Accounts are administrator-provisioned; there is no public signup or email reset.
Set `TESSERACT_PUBLIC_ORIGIN` to the exact browser origin (no trailing slash).
Production requires HTTPS, with the frontend and API on that origin. Secure,
HttpOnly, SameSite=Strict cookies expire after 12 hours. For local development only,
`.env.example` enables HTTP cookies at `http://127.0.0.1:5173` using
`TESSERACT_AUTH_INSECURE_DEVELOPMENT=true`; remove that override in production.

Run account commands from `backend` with `TESSERACT_DATABASE_URL` and PostgreSQL TLS
settings loaded as for the API. Supply a password of at least 12 characters (maximum
1024 bytes) through stdin, never an argument. For example, in Bash:

```sh
read -rs -p 'New password: ' account_password
printf '%s' "$account_password" | go run ./cmd/account -action create -email analyst@example.com
unset account_password
```

Sign in in the browser, then select **Load / refresh** to see protected catalog
metadata. **Change password** verifies the current password and revokes every
session. **Sign out** revokes the current session. A successful login rotates the
browser's previous session. Sessions and account status are checked on every
protected request; browser refresh restores a valid session without browser storage.

Recovery: an administrator verifies the person's identity outside Tesseract and
uses the same stdin procedure with `-action reset-password`. Deliver the replacement
password through an approved private channel; the user can change it after login.
Disable an account with `go run ./cmd/account -action disable -email analyst@example.com`;
this immediately revokes sessions. `-action enable` restores login but never old
sessions. Reset disabled accounts only after explicit re-enablement. Accounts are
retained for stable identity and future ownership records; deletion is not exposed.

Schedule `go run ./cmd/account -action cleanup` hourly to remove expired sessions and
rate-limit records. Login limits are shared across API replicas: 10 attempts per
normalized email and 50 per peer IP per 15-minute window, counting successes too.
Password changes allow 10 attempts per account/window. Forwarded IP headers are
ignored; deployments behind a proxy share its IP limit and should additionally
limit traffic at the proxy. Each process allows four concurrent password KDFs.
Non-GET API requests require an `Origin` matching the configured public origin,
including login; scripted clients must send that header. Cookies and responses
must not be cached by the proxy. The old catalog bearer token is no longer accepted.

Signed-in accounts can see catalog metadata. Analytical HTTP execution remains
unimplemented pending milestone 11's data entitlements; authentication alone does
not grant DWH access. OIDC and dashboard access rules are later milestones.

Authentication verification:

```sh
cd backend
GOCACHE=/tmp/tesseract-go-build go test ./...
GOCACHE=/tmp/tesseract-go-build go test -tags=integration ./internal/auth/infrastructure/postgres -count=1
cd ..
mise exec -- pnpm --dir web run build
mise exec -- bash scripts/check-api.sh
```

### Organizational sign-in (OIDC)

Password login remains available with SSO disabled (the default). To enable SSO,
register a confidential web application at your OIDC provider with authorization
code flow, S256 PKCE, and client authentication. Set:

```dotenv
TESSERACT_OIDC_ENABLED=true
TESSERACT_OIDC_ISSUER=https://identity.example.com/tenant
TESSERACT_OIDC_CLIENT_ID=tesseract
TESSERACT_OIDC_CLIENT_SECRET=<secret from your deployment secret store>
TESSERACT_OIDC_REDIRECT_URI=https://tesseract.example.com/api/v1/auth/oidc/callback
TESSERACT_OIDC_SCOPES="openid email profile"
```

The redirect URI must exactly equal `TESSERACT_PUBLIC_ORIGIN` plus
`/api/v1/auth/oidc/callback`; register that exact URI with the provider. The API
must be reachable at that path on the browser origin (the Vite development proxy
also supports it). Providers must return callbacks as top-level GET redirects
with query parameters. `openid` is always included. Discovery normally uses the
issuer's `/.well-known/openid-configuration`; set
`TESSERACT_OIDC_DISCOVERY_URL` only for a custom metadata location. Its published
issuer must still match the configured issuer exactly. HTTPS is required, except
loopback HTTP under the existing explicit insecure development setting. Invalid
enabled configuration or failed discovery prevents startup; explicitly disable
SSO to run password-only during a provider outage.

Accounts and links are provisioned by an administrator with database access.
Create the local account using the existing account command, then independently
verify the provider's stable subject and exact issuer before linking:

```sh
cd backend
go run ./cmd/account -action link-oidc -email alice@example.com \
  -issuer https://identity.example.com/tenant -subject '<verified subject>'
```

Each provider identity belongs to one local account; an account has at most one
subject per issuer. Conflicting links fail rather than moving identities.
There is no self-registration, email-based linking, or claim-driven permission
assignment. Email/name/group changes at the provider never change the local
account or its permissions. A disabled local account cannot sign in through
SSO, and disabling it revokes all local sessions. Link removal/reassignment is
not exposed by this milestone; perform any exceptional correction through an
audited administrative database operation and revoke that user's sessions.

The sign-in page shows **Sign in with your organization** when enabled. Login
attempts expire after five minutes and are single-use, with state, nonce, PKCE,
and a browser-bound HttpOnly cookie. Only identity mappings and temporary flow
secrets are stored; provider tokens are not persisted. Run the existing account
`cleanup` command regularly to remove expired attempts as well as sessions and
rate-limit records. Sign out revokes the Tesseract session; it does not sign the
user out of the identity provider or other applications. Password change still
requires the local password. The local provisioning process supplies it even
when users normally choose SSO.


## Data access policy

Set `TESSERACT_ACCESS_POLICY` to an operator-managed JSON file, readable by the API.
Without a file, signed-in users see an empty catalog and all analytical queries
are denied. A configured invalid file fails startup. Obtain the catalog hash
with `mise run catalog:validate` and the local user UUID from `cmd/account` or
the authenticated session response. Example (replace both placeholders):

```json
{
  "version": 1,
  "catalog_hash": "CATALOG_SHA256",
  "grants": [{
    "user_id": "LOCAL_USER_UUID",
    "source": "crm_dwh",
    "dataset": "leads",
    "fields": ["lead_count", "created_at"],
    "row_filters": [{
      "dimension": "channel",
      "operator": "eq",
      "values": [{"type": "string", "value": "paid"}]
    }]
  }]
}
```

Each user/dataset pair has exactly one grant, with an explicit source and
metric/dimension IDs. Specify either nonempty `row_filters` or
`"unrestricted_rows": true`, never both. Omitted row access denies the grant.
Policy fields and scope filters must exist in the catalog; filters obey the same
typed values and allowed operators as QuerySpec. Scope dimensions need not be
granted for user selection. All scope predicates and user filters are ANDed
before aggregation. Unsupported scopes must receive no grant.

Policies use stable local UUIDs, so password and OIDC sessions have identical
access. Group/region membership is provisioned by operators as explicit per-user
grants and typed scope values; no email matching or OIDC claim grants are inferred.
Policy changes and revocations take effect on API restart. Drain all old instances
when revoking access. A catalog change requires a newly reviewed matching policy.

The same query service is the entry point for future previews and widget queries:
always pass the authenticated viewer ID, even for another user's dashboard.
Dashboard ACLs do not create data grants. No query results are cached, and API
responses carry `Cache-Control: no-store`. Row restrictions use bound SQL
parameters, with no SQL Server session context to leak across pooled connections.
Missing DWH credentials leave that source unavailable. Configured pools connect
on demand with bounded query deadlines, so a warehouse outage does not block API
startup. Read-only privileges are checked before each catalog inspection/query.

The CLI is an operator tool requiring direct database credentials; `-user` selects
whose policy to evaluate, and is not a user authentication mechanism.

### Dashboard persistence API

Authenticated owners can create and reopen dashboards through the generated API.
All mutations require the configured `Origin` header and a session cookie:

- `POST /api/v1/dashboards`: save a DashboardSpec v1 as revision 1.
- `GET /api/v1/dashboards?limit=10&offset=0`: list the caller's dashboards.
- `GET /api/v1/dashboards/{id}`: reopen the current draft with live validation states.
- `PUT /api/v1/dashboards/{id}/draft`: send `{ "expected_revision": 1, "spec": ... }`.
- `POST /api/v1/dashboards/{id}/publish`: send `{ "expected_revision": 2 }`.
- `GET /api/v1/dashboards/{id}/revisions?limit=10&offset=0`: recover saved specifications, newest first.
- `DELETE /api/v1/dashboards/{id}?expected_revision=3`: delete the dashboard and history.

A minimal create body (requires `leads.lead_count` entitlement and a compatible scan):

```json
{
  "version": 1,
  "title": "Lead overview",
  "filters": [],
  "widgets": [{
    "id": "lead_count",
    "title": "Leads",
    "kind": "kpi",
    "layout": { "x": 0, "y": 0, "width": 3, "height": 2 },
    "query": { "dataset": "leads", "metrics": ["lead_count"], "limit": 1 },
    "filter_mappings": []
  }]
}
```

Each successful save or publish advances `revision`. A stale revision returns 409;
reload before retrying. Draft edits retain the previous `published_revision`.
Restore a historical spec by saving it with the current revision. Schema drift
appears as a per-widget `unavailable` state and blocks publication until resolved.
### Dashboard workspace

After signing in, open **Dashboards**, select a saved dashboard, or choose **New dashboard** and paste a DashboardSpec (such as the example above). The initial
editor accepts JSON specifications using catalog-approved semantic IDs; the API
validates queries and the bounded, non-overlapping layout before saving. **Edit**
saves a new draft; **Publish** publishes the current saved revision. A revision
conflict keeps your edits so you can copy them before cancelling and reloading.

KPI, table, line, and bar widgets load live aggregates through `/api/v1/queries`.
Global filter inputs preserve their declared type and operator; **Apply filters**
updates explicitly mapped widgets without saving the filter changes. **Reload data** reopens the saved spec, rechecks availability, and resets viewer filters.
Denied, invalid, unavailable, empty, and failed queries are displayed separately.
Charts include an exact-value table; NULL is displayed as `—`. The catalog tab
shows metadata availability and remediation guidance. Results never enter browser
persistent storage or application PostgreSQL.

Use `mise exec -- pnpm --dir web test` for dashboard component tests. The initial editor is a specification editor. Chat authoring is available when an
LLM endpoint is configured; sharing remains a later milestone.

### Chat dashboard creation

Set `TESSERACT_LLM_ENDPOINT` to the full OpenAI-compatible chat completions URL
(for example `https://llm.example/v1/chat/completions`), `TESSERACT_LLM_MODEL` to
the served model, and `TESSERACT_LLM_TOKEN` to its authorization token. The backend
sends `Authorization: Bearer <token>`. The model must support function tools.
Chat is disabled when these settings are empty. Set
`TESSERACT_LLM_INSECURE_SKIP_VERIFY=true` to skip certificate and hostname
verification for this LLM connection only; the default is `false`. Redirects are
never followed, and upstream errors and credentials are not exposed to clients.

Sign in, open Dashboards, and describe the charts under **Create with chat**.
The assistant searches the current authorized, schema-compatible semantic catalog,
asks clarifying questions when needed, and creates a validated draft. Existing
chart components fetch live results using the signed-in user's permissions.
The tools accept semantic QuerySpec objects and a typed `create_dashboard`
operation; SQL, arbitrary formulas, joins and unknown fields are rejected.
Semantic ambiguity is handled through the assistant’s clarification instructions;
backend validation guarantees catalog and query validity, not interpretation of
natural-language intent. Preview tools execute at most ten result rows and return
only success status to the model. No warehouse rows, physical schema or service credentials are sent.
User-entered chat text is sent to the configured provider.

Each request permits six model turns, twelve tool calls, 20 seconds per provider
request, ten seconds per tool, and 90 seconds overall. Model responses and tool
results are size bounded. Eight chat requests may run concurrently. Sessions are
private to a user, held in server memory (maximum 1000), limited to 20 messages,
and expire after one idle hour or server restart. A successful creation closes
that conversation; use **New conversation** to create another draft. Iterative
editing and durable draft-linked chat history belong to milestone 15. On a lost
connection, check the dashboard list before retrying. LLM count, duration and
error metrics use fixed `openai_compatible` / `configured` labels on `/metrics`.
