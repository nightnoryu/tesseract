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
