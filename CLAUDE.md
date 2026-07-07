# CLAUDE.md

## Loop Mode
This project uses a continuous iteration workflow. You will receive the same prompt repeatedly. Each time:
- Treat it as a completely new task
- Never say "I already did this" or "as I mentioned before"
- Execute fully every time
- Read TASKS.md for current state — that's your only source of truth

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

# E2E tests (requires running backend on :8443)
cd frontend && npm test

# Frontend dev server (proxies API to :8443)
cd frontend && npm run dev

# Build frontend (output goes to frontend/dist/)
cd frontend && npm run build

# Regenerate sqlc after editing queries.sql
sqlc generate
```

## Development workflow

1. Edit `internal/database/queries.sql` for new queries, then run `sqlc generate`
2. Never edit files in `internal/database/generated/` — they are auto-generated
3. **Do NOT run `npm run build` or `go build` locally** — use `docker compose up -d --build` instead. The Dockerfile handles frontend build, embed.go creation, and Go compilation in the correct order. This avoids the `embed.go` issue where Vite deletes the dist/ directory.
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
