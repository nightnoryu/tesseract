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
`TESSERACT_DWH_CRM_DSN` with a separate read-only account when DWH connectivity is
implemented. PostgreSQL is exposed on localhost:5433 and SQL Server on
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
paths with openapi-fetch. Never edit generated files manually.

```sh
mise run api:generate   # after editing the contract
mise run api:check      # regenerate into a temporary directory and compare
mise run build
mise run check         # types, Go tests, formatting, vet, generation
```

CI runs the consistency check and builds/checks. Comparison includes extra and
missing generated files and works before files are committed. The React preview
requests `/healthz` through the typed client and shows the service connection
state. Vite proxies `/api`, `/healthz`, and `/readyz` to port 8080; production
hosting must route these paths to Go on the same origin.

Health and readiness use generated handlers. `/metrics` remains a Prometheus
endpoint outside the JSON contract. Errors use
`{"error":{"code":"not_implemented","message":"..."},"request_id":"..."}`,
with the same server-generated ID in `X-Request-ID`.

Auth, catalog, dashboard, query, sharing, and chat routes are reserved contracts;
they return 501 without accessing data. Protected routes return 401 without a
session cookie and reject every supplied cookie with 501 until authentication
is implemented. Request bodies and success schemas will be added in their
respective milestones, including the strictly typed QuerySpec. No raw SQL
request interface is exposed.
