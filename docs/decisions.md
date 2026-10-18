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
