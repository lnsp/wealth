# CLAUDE.md

## Project overview

Personal finance tracker — Go backend with React (TypeScript) frontend, backed by PostgreSQL. Deployed via Docker Compose.

## Architecture

- **Backend**: Go with chi router, pgx connection pool, sqlc for type-safe queries, goose for migrations
- **Frontend**: React 18 + TypeScript + Vite, Tailwind CSS, ECharts for visualizations
- **Database**: PostgreSQL 16, materialized views for current holdings

## Key directories

- `cmd/server/main.go` — HTTP server entrypoint, route registration
- `internal/handler/` — HTTP handlers (import, transactions, portfolio, analysis, settings)
- `internal/database/queries.sql` — SQL queries (sqlc source of truth)
- `internal/database/generated/` — sqlc-generated code (**do not edit manually**)
- `internal/analytics/` — Overlap computation, weight parsing
- `internal/parser/` — CSV parsers for bank imports
- `internal/market/` — Yahoo Finance + ECB exchange rate clients
- `internal/scheduler/` — Background jobs (price updates, metadata fetch)
- `frontend/src/` — React SPA source
- `frontend/dist/` — Built frontend assets (embedded into Go binary via `embed.go`)
- `migrations/` — Goose SQL migrations

## Common commands

```bash
# Run everything locally
docker compose up -d

# Go tests
go test ./...

# Frontend dev server (proxies API to :8443)
cd frontend && npm run dev

# Build frontend (output goes to frontend/dist/)
cd frontend && npx vite build

# Regenerate sqlc after editing queries.sql
sqlc generate
```

## Development workflow

1. Edit `internal/database/queries.sql` for new queries, then run `sqlc generate`
2. Never edit files in `internal/database/generated/` — they are auto-generated
3. After building the frontend, ensure `frontend/dist/embed.go` exists (vite build may delete it):
   ```go
   package dist

   import "embed"

   //go:embed all:*
   var FS embed.FS
   ```
4. Database migrations go in `migrations/` using goose format
5. Routes are registered in `cmd/server/main.go` under the `/api` prefix

## Code conventions

- Handler files: one file per domain (e.g., `analysis.go`, `portfolio.go`)
- JSON responses use `writeJSON(w, status, body)` and `writeError(w, status, msg)`
- pgtype wrappers (pgtype.Numeric, pgtype.Text) are used for nullable DB fields
- Frontend API client is in `frontend/src/api/client.ts` — add new endpoints there
- Tests use standard `testing` package with `httptest` for handler tests

## Environment

- Requires `DATABASE_URL` pointing to PostgreSQL (see `.env.example`)
- Default port: 8443
- Config loaded via `internal/config/` from environment variables
