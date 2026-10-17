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

- `backend/cmd/tesseract`: minimal Go HTTP process; output in `backend/bin/`.
- `web/`: React, TypeScript, Vite, Tailwind; output in `web/dist/`.
- `mise.toml`: pinned toolchain and build, check, and development tasks.
- `compose.yml`: PostgreSQL and optional SQL Server with persistent volumes.
- [Decisions](docs/decisions.md): layout adaptation from Lumio and scope choices.

Milestone 1 provides the runnable workspace. Database connections, migrations,
health/readiness probes, structured logging, and metrics arrive in milestone 2;
the shared OpenAPI contract and generation arrive in milestone 3. The generator
versions are already pinned. There are no application tests yet; `check` runs Go
format/vet/test, TypeScript checking, and Compose validation.

## License

[MIT](LICENSE).
