# Decisions

## Milestone 1: workspace and local services

Follow Lumio's separate `backend/` Go module, `web/` pnpm app, root
`compose.yml`, and namespaced mise tasks. Keep frontend output in `web/dist`
until deployment requirements establish whether Go should embed it.

Run Go and Vite on the host through mise; Compose manages persistent development
databases. PostgreSQL uses port 5433 to coexist with Lumio's port 5432. SQL Server
is opt-in because it requires more resources and an amd64 runtime. Named volumes
survive `dev:down`. SQL Server's SA credential is for local administration only;
the application's future DWH connection must use separate read-only credentials.

Match Lumio's exact Go, Node, pnpm, React, Vite, Tailwind, and OpenAPI generator
versions. Pin ogen and openapi-typescript now; add generation tasks when the
canonical contract is introduced in milestone 3. Do not add unused module trees,
database libraries, analytics dependencies, or placeholder API contracts.

## Milestone 2: operational backend

Pin go-kita v1.9.0 after inspecting its current connector, migrator, and JSON logger
implementation. Run embedded migrations before listening; use the application's
own advisory lock ID and a bounded database pool. Create `tesseract_app` now;
add domain tables with their milestones. Avoid a schema named after the default
`tesseract` database role: PostgreSQL's default `$user` search path would move
unqualified migration history into that schema on the next startup.

The go-kita DSN does not support URL options. Reject them rather than dropping
TLS settings; use pgx's standard PGSSLMODE/PGSSLROOTCERT environment configuration.
Migration and connection errors become fixed codes to avoid leaking credentials
or SQL. Request logs use go-kita JSON logging and server-generated correlation
IDs; request paths, headers, panic values, and raw errors stay out of logs.

Use a dedicated Prometheus registry with Go/process, HTTP, and LLM metrics only.
LLM metrics have a fixed unconfigured series until milestone 14 introduces
reviewed provider/model IDs. Readiness depends on PostgreSQL; DWH configuration
is reported separately and will not make the application process unready.
Operational handlers remain handwritten until the milestone 3 API contract.

## Milestone 3: canonical HTTP contract

Use OpenAPI 3.0.3, the already pinned ogen/openapi-typescript generators, and
openapi-fetch bound to generated paths. Generate operational JSON handlers now
so the React service indicator exercises a real typed request. Keep Prometheus
text output outside OpenAPI. Generate into a temporary directory for consistency
checks so stale, missing, and extra files are detected without mutating the tree.

Reserve endpoint names for later milestones with explicit failure responses;
do not invent domain DTOs before catalog, QuerySpec, and dashboard semantics
exist. Protected operations declare a session cookie but fail closed for every
cookie until milestone 9. Common errors, routing failures, and recovered panics
use generated envelope types with safe messages and a server-generated request
ID. Route labels come from the generated router's templates.

## Milestone 4: semantic catalog

Version 1 uses strict TOML with one sources file and one dataset per file under
`datasets/`. Only MSSQL is supported. Logical IDs use lowercase ASCII; physical
identifiers use a conservative ASCII subset (no qualification or SQL expressions).
Case-insensitive duplicate physical columns/tables are rejected to remain safe on
SQL Server's common case-insensitive collations. Each table has one definition.

Exposure is mandatory on every column. Hidden columns may back explicitly named
measures but cannot be dimensions or direct filters. Filters belong to dimensions
and permit only their listed operators; an empty list permits no filtering.
Temporal dimensions must list grains. Count/count_distinct count non-null column
values; sum/avg require numbers, min/max numbers or dates. No formulas, joins,
implicit count-star, or inferred fields exist. Descriptions/synonyms are metadata,
never executable definitions. Synonyms do not grant access or resolve ambiguity.

SHA-256 covers the validated catalog, including descriptions and exposure, using
canonical JSON with sorted definition and set lists. File names, comments and
TOML declaration order do not affect it. Validation does not resolve credentials,
connect to a warehouse, establish compatibility, or grant user entitlements.
The demo describes synthetic fixture data only; production definitions require
human review. API publication and DWH checks remain in subsequent milestones.

## Milestone 5: DWH boundary

Pin Microsoft's go-mssqldb v1.9.8. Connectors own a validated catalog snapshot
and accept logical dataset IDs for inspection. Metadata lookups bind schema,
table and approved column names; they return no uncataloged columns or rows.
Missing or metadata-invisible tables report `Present=false`; reconciliation will
assign availability reasons in milestone 6. No application API exposes this probe.

Require membership in SQL Server's db_denydatawriter role, reject administrative
and broad database permissions, and reject non-read permissions on approved
objects at connection startup. Provision a dedicated principal with SELECT only
on approved objects, no extra roles/schema grants, and no executable procedures.
These checks supplement DBA-managed least privilege; they cannot prove absence
of every possible server permission or prevent a DBA changing grants later.
ApplicationIntent=ReadOnly alone does not enforce read-only access.

Connection and operation deadlines, pool size, concurrency and row/group ceilings
are explicit bounded configuration. Queue wait counts toward the operation deadline.
Errors omit driver text to avoid disclosing DSNs or server details. The connector
has a separate aggregate execution path with adapter-local identifier quoting and
bound limits. Its opaque ValidatedQuery has no constructor yet: milestone 8 must
add catalog/schema/entitlement validation before any analytics can execute. The
zero value fails closed. No HTTP query path is enabled by this milestone.

## Metadata snapshots (milestone 6)

Use one PostgreSQL transaction per source, holding a transaction advisory lock
through DWH inspection and atomic snapshot publication. Lock identity excludes
catalog hash to prevent overlap across catalog revisions. Transaction rollback
on process failure releases the lock; completed failures replace earlier green
snapshots. Source/table concurrency is one and retries occur only on the next
scheduled cycle. Startup loads a fixed catalog snapshot; restart for changes.

Compatibility is conservative: integers map to SQL Server integer types;
decimals to exact decimal/numeric/money types; strings to char/varchar/nchar/
nvarchar/uniqueidentifier; booleans to bit; dates to date; datetimes to
datetime/datetime2/smalldatetime. Approximate numbers, timezone-bearing values,
legacy text, binary and unknown types fail closed. All catalog columns,
including hidden measure inputs, are required. Persist only approved metadata,
fixed reason codes, and scan timestamps; never connector error text or rows.

## Catalog availability (milestone 7)

Availability reads only the published catalog's source IDs and exact catalog hash, then projects stored tables onto its dataset/schema/table allowlist. A failed latest scan replaces availability immediately; last successful scan is historical context for the same catalog version, never a fallback. A compatible inspection expires after `TESSERACT_METADATA_MAX_AGE` (default 15 minutes, maximum 24 hours). Stale tables count as unavailable; stale is also a separate count, and an existing incompatibility reason is preserved. Distinct physical table counts follow the catalog validator's prohibition on duplicate physical tables.

Until account authentication and entitlements arrive in milestones 9–11, the catalog endpoint has an explicit operator-only bearer credential, configured with `TESSERACT_CATALOG_TOKEN` (minimum 32 bytes). Empty disables access; a session cookie cannot bypass it. Use a randomly generated token and HTTPS outside localhost. The UI retains the token only in its password input, never browser storage or URLs; responses use `Cache-Control: no-store`. This grants catalog-wide operator visibility, not analytical query access or user sessions. Replace this gate with session authentication and entitlements in those milestones.

SQL Server may hide absent and inaccessible objects identically in system metadata; those cases report `missing_table` (the UI explains both possibilities). Explicit permission/login errors report `access_denied`; other connection/inspection failures report `scan_failed`. Raw driver errors and connection details are never included in availability responses.

Schema inspection also executes `SELECT TOP (0)` over approved columns once all are confirmed present. It returns no business rows and verifies effective table/column read permissions, since schema visibility alone does not imply analytical access.

### Milestone 08: direct warehouse queries

- `QuerySpec` uses semantic metric/dimension IDs and typed scalar strings, never SQL. Catalog operators and time grains are enforced; all predicates use bound parameters. Date ranges are half-open, dates are Gregorian, datetime inputs normalize to UTC, and week buckets start Monday independently of SQL Server `DATEFIRST`. Warehouse datetimes are interpreted as UTC.
- The application engine requires an identified caller with explicit source, dataset, and semantic field grants (including filter-only fields), plus an explicit unrestricted-row grant. Row-scoped callers fail closed until milestone 11 supplies that policy; ownership and the catalog metadata token confer no query grants. PostgreSQL is read only for fresh metadata during queries; analytical results remain in memory.
- Each execution checks the exact catalog hash and recent successful table compatibility, then inspects the allowlisted physical columns again to catch drift between worker scans. Execution rechecks policy after waiting for capacity. DDL racing the final inspection is still a warehouse operational concern; SQL failures return safe errors. No cached results or fallback datasets.
- Limits: 64 KiB QuerySpec, 16 metrics, 8 dimensions, 16 filters, 100 values/filter, 1–1000 returned groups (also bounded by connector settings), and a 30-second engine deadline including policy checks. Source concurrency and timeouts apply to inspection and execution; cancellation propagates through database calls.
- NULL semantics are SQL's: aggregates ignore NULL, counts return zero for empty input, and sum/avg/min/max return NULL for empty/all-NULL input. Filters use explicit `is_null`/`is_not_null`; `ne` and `not_in` exclude NULL. No ratios or division are supported by the catalog, so zero-denominator formulas are rejected rather than invented. Integer sum/avg widen to decimal to avoid int overflow and integer-average truncation; integer and decimal results serialize as exact strings. Decimal filter inputs are limited to 20 integral/18 fractional digits; no float rounding. Numeric overflow fails explicitly.
- The generated HTTP contract now describes queries, but its session gate remains closed until milestone 09. `cmd/query` provides the executable milestone deliverable to a local operator who already possesses database credentials, with explicit grant flags. It does not turn the metadata bearer token into analytical access. Password/OIDC identity integration and persistent entitlements remain milestones 09–11.

## Milestone 09: local identities and sessions

- Administrator-only provisioning via `cmd/account`; no open registration or email reset. Recovery uses administrator identity verification and a stdin-supplied replacement password. Disable/enable preserve stable UUIDs; disable and password replacement revoke every session. Existing accounts are never silently upserted by provisioning.
- Password hashes encode Argon2id v19, 64 MiB, three iterations, one lane, a unique random 16-byte salt, and a 32-byte digest. The verifier accepts only supported parameters, preventing corrupted hashes from requesting unbounded work. Passwords require 12 Unicode characters and at most 1024 bytes. No password normalization/truncation occurs in HTTP handling.
- Sessions contain 256 bits of random entropy, stored only as SHA-256 digests in PostgreSQL, with an absolute 12-hour expiry. Login rotates the previous browser session. Issuance serializes against credential changes and account disabling. Session lookup checks current account state; no user/token cache.
- Strict SameSite/HttpOnly/Secure cookies plus exact configured Origin validation on unsafe API methods cover login CSRF and authenticated mutations. Production origin must be HTTPS. The explicit development override permits only loopback HTTP. No cross-origin API mode or proxy-header trust is introduced.
- PostgreSQL rate counters apply before password work and are shared across replicas. A four-slot local KDF cap bounds memory pressure; unknown accounts still run a dummy KDF and return the same credential error. Expired records are removed by the scheduled account cleanup command.
- This supersedes milestone 07's temporary bearer gate: authenticated users may inspect catalog metadata. Query execution remains closed until persistent data entitlements arrive; later milestones add OIDC and authorization without replacing local IDs.

### Milestone 10: OIDC identity linkage

Use coreos/go-oidc v3 with golang.org/x/oauth2 for discovery, signature/issuer/
audience/expiry validation and authorization code exchange. Explicitly check
nonce and use S256 PKCE. Discovery may use a configured URL without relaxing
issuer verification. Only loopback development may use HTTP.

An administrator links `(issuer, subject)` to an existing local user; email and
other provider claims never merge users or grant permissions. Unique constraints
reject conflicting links. Both methods issue the same twelve-hour local session;
issuance locks the enabled user, and session lookup checks disabled status.

PostgreSQL holds five-minute, single-use attempts bound to a random browser
cookie, with hashes of state/browser/session values and temporary nonce/PKCE
secrets. The start endpoint requires the existing Origin CSRF check and rate
limit. The callback accepts GET query mode, uses a Lax transaction cookie, and
returns only fixed same-origin success/failure redirects. Provider tokens are
neither logged nor retained. Logout is local; provider-wide logout is deferred.

## Milestone 11: data entitlements

Use a strict, immutable operator-managed JSON policy tied to the semantic catalog
hash and local user UUID. A single grant per user/dataset names its source,
permitted metric/dimension IDs, and either explicit unrestricted rows or mandatory
typed filters. Missing grants deny access. Group/region policies are materialized
as per-user grants; claim-based membership and a policy administration API are
deferred. Restart/drain API instances to publish or revoke a policy.

Authorize caller fields before adding policy-only scope fields, then validate
the complete query again. Scope and caller predicates are ANDed and compiled to
bound T-SQL parameters before aggregation. This uses no pooled connection session
context. Unsupported row policies get no grant. Catalog discovery filters tables
and recomputes counts under the same policy. Query responses are never cached.

The authenticated query route and operator CLI resolve grants through the same
policy service. Future chat previews and widgets must use this service with the
viewer's local identity; dashboard ownership/ACLs are independent. Sharing routes
remain reserved for milestone 16; viewer isolation is tested now at the service
and HTTP query boundaries.

API DWH pools connect on demand so an offline warehouse cannot block authentication
or catalog availability. Each inspection/query checks read-only privileges;
query deadlines and concurrency bounds also cover connection attempts.

## Milestone 12: dashboard specifications and revisions

DashboardSpec v1 contains only titles, a 12-column by 100-row bounded grid,
up to 24 KPI/line/bar/table widgets, typed QuerySpecs, and up to 16 global filters.
Each widget explicitly maps filter IDs to its dataset's dimension IDs. Unmapped
filters do not affect that widget; each global filter must map to at least one
widget. Mapped predicates are ANDed with widget predicates, then validated with
current catalog, metadata, and caller entitlements including mandatory row scope.
KPI widgets select one metric without grouping; line widgets use one time-grained
dimension; bars use one dimension. Layouts cannot overlap. Requests retain the
64 KiB HTTP limit; stored specs have a 60 KiB bound.

PostgreSQL stores dashboard identity/owner and immutable specification revisions,
never query results. Creation saves revision 1. Each draft save and publish
increments the dashboard revision; publication also advances the published pointer
and records a publication revision. Later draft edits preserve the published
revision. Saves, publishes, and deletion require the current revision, with a row
lock and atomic transaction preventing lost updates. History is paginated, newest
first; restoring an old specification means saving it as a new draft revision.
Deletion removes the dashboard and its history.

All dashboard routes require a session and owner access; unknown and other-owner
IDs both return 404. Sharing remains milestone 16. Saves and publishes reject
invalid, denied, or unavailable widget queries. Reads and history return the
stored spec with freshly evaluated per-widget `ready`, `invalid`, `denied`, or
`unavailable` states, so schema drift does not hide the recoverable dashboard.
`ready` means validation passed, not a live DWH connectivity probe. No query runs
while saving/reading a dashboard. Renderers must use the query engine again at
execution time. Operations have a ten-second deadline; list/history pages default
to 10 and cap at 20. The renderer and widget data fetching remain milestone 13.

## Milestone 13: live dashboard rendering

The authenticated React workspace lists and reopens saved drafts, edits a versioned
JSON specification, and saves/publishes with the current revision. This initial
editor deliberately exposes the existing bounded spec; chat authoring follows in
milestone 14. Viewer filters preserve typed string values and apply only explicit
widget mappings, ANDed with the original query filters. They are transient; editing
starts from the saved spec. Invalid changes are rejected by the existing API.

Each ready widget calls the generated direct-query API under the current session.
Requests are aborted on replacement/unmount and late responses are ignored. No
results are persisted in browser storage or application PostgreSQL. Schema and
permission failures remain visible per widget; missing validation fails closed.
Refreshing the dashboard revalidates the saved spec and reloads its data. Mobile
layouts stack in grid order; desktop uses the validated 12-column grid.

ECharts options are constructed locally from the widget kind and returned columns;
no backend chart options, HTML formatters, or executable callbacks are accepted.
Tooltips use rich text, charts resize/dispose with their containers, and an exact
value table accompanies each chart. Charts use floating-point plotting; KPI/table
values retain the API representation, including exact decimal/int64 strings. NULL
is shown as an em dash rather than fabricated zero. Reaching the query limit warns
that additional groups may exist. No automatic query retries or result caching.

Chart initialization follows the [ECharts container lifecycle guidance](https://echarts.apache.org/handbook/en/concepts/chart-size/)
and fixed options follow its [security guidance](https://echarts.apache.org/handbook/en/best-practices/security/).
A chart-level error boundary keeps the dashboard and exact table usable if a chart
chunk or renderer fails. An expired dashboard/query session returns to sign-in.

Vite splits the chart's ZRender dependency into a separate renderer chunk. Both
chart chunks remain behind the lazy chart import, and each stays below Vite's
default size warning threshold.


## Milestone 15: iterative draft operations and durable chat

An edit is a bounded batch of typed operations against an explicit dashboard
revision. Operations modify a deep copy and validate the final result, permitting
related changes (for example, adding a filter and its widget mappings) atomically.
There is no replace-dashboard tool during editing. The PostgreSQL row lock checks
the revision again when committing the new spec and its chat/operation history;
failed history writes roll back the revision. Clarification messages lock/check
the same revision but do not create a new spec revision. History is owner-scoped,
paginated and deleted with the dashboard. Pre-creation sessions remain ephemeral;
their conversation becomes durable in the creation transaction.

Chat edits automatically save a draft. Publish remains an explicit revision-checked
UI action. Discard appends a revision restoring the last published spec, or revision
1 if unpublished, and records a discard event. Historical revisions and chat are
retained. Discard can restore a stored spec after drift; render/publish enforce
current compatibility. Metadata decoration errors after an edit commits return
the committed draft with unavailable widget states, avoiding misleading retry
signals. The synchronous HTTP flow uses the existing turn/tool/time budgets.
The model sees the current spec plus the latest 18 messages; persisted older
messages are available to the user through pagination.
