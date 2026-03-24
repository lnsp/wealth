# Finance Tracker

A self-hosted personal finance tracker for **Sparkasse**, **N26**, and **Scalable Capital**. Built entirely on periodic CSV snapshots — no FinTS, no PSD2, no browser automation.

A Go backend compiles to a single binary, runs in a ~30MB container using ~30–50MB of RAM, and has zero runtime dependency management. A React frontend is embedded directly into the binary via `go:embed`.

## Features

- **CSV import** with auto-detection for Sparkasse (Latin-1/semicolon), N26 (UTF-8/comma), and Scalable Capital (UTF-8/semicolon)
- **Deduplication** via SHA-256 hashing — safe to re-import overlapping date ranges
- **Portfolio valuation** from transaction history (no point-in-time snapshots needed)
- **Market data** from Yahoo Finance (prices) and ECB (FX rates), fetched on a daily schedule
- **ETF decomposition** — sector and country allocation weighted across your portfolio
- **ETF overlap analysis** — pairwise overlap matrix showing redundancy between funds
- **Performance tracking** — Time-Weighted Return (TWR) and Internal Rate of Return (IRR)
- **Net worth tracking** with historical snapshots broken down by cash vs. investments
- **Holdings template** for manual position entry (e.g., Sparkasse depot without CSV export)

## Quick start

```bash
cp .env.example .env
# Edit .env — at minimum change SESSION_SECRET

docker compose up -d
```

The app is available at `http://localhost:8443`.

### First steps

1. Go to **Settings** and create an account (e.g., "Sparkasse Girokonto", institution: sparkasse, type: checking)
2. Export a CSV from your bank's web portal
3. On the **Net Worth** page, select the account and drag-and-drop your CSV file
4. Repeat for each account/institution

## CSV export guides

### Sparkasse

Online banking → Umsätze → Export → CSV. Typical encoding is ISO-8859-1 with semicolon delimiter and German number format (1.234,56). Date format: DD.MM.YYYY.

### N26

WebApp → Home → Downloads → Account Activity → select date range → Download CSV. UTF-8 encoding, comma delimiter, standard numbers, YYYY-MM-DD dates.

### Scalable Capital

If you have PRIME+, use Broker → Transactions → Export CSV. Otherwise, see the bookmarklet approach in the design document. The app reconstructs current holdings by replaying all buy/sell transactions from the history.

## Architecture

```
┌──────────────────────────────────────────────────┐
│              Docker Host                         │
│                                                  │
│  ┌────────────────────────────────────────────┐  │
│  │   App Container (~30MB)                    │  │
│  │   Go binary: finance-tracker               │  │
│  │     ├── HTTP API (Chi)                     │  │
│  │     ├── Embedded React SPA (go:embed)      │  │
│  │     ├── CSV parsers + import pipeline      │  │
│  │     ├── Portfolio analytics engine         │  │
│  │     └── Cron scheduler                     │  │
│  │           ├── Daily 18:30: prices          │  │
│  │           ├── Daily 16:00: FX rates        │  │
│  │           ├── Sunday 03:00: ETF holdings   │  │
│  │           └── Daily 02:00: backup          │  │
│  └──────────────────┬─────────────────────────┘  │
│                      │                            │
│  ┌──────────────────┴─────────────────────────┐  │
│  │   PostgreSQL 16 (~80MB)                    │  │
│  └────────────────────────────────────────────┘  │
└──────────────────────────────────────────────────┘
```

## Tech stack

| Layer | Technology |
|-------|-----------|
| Backend | Go 1.22+, Chi router, pgx v5, sqlc, goose migrations, robfig/cron |
| Frontend | React 18, TypeScript, Vite, Apache ECharts, Tailwind CSS |
| Database | PostgreSQL 16 |
| Container | Alpine Linux (~30MB app image) |

### Go dependencies

```
github.com/go-chi/chi/v5       # HTTP routing
github.com/jackc/pgx/v5        # PostgreSQL driver + pool
github.com/pressly/goose/v3    # Migrations
github.com/robfig/cron/v3      # Scheduler
github.com/PuerkitoBio/goquery # HTML scraping (justETF)
golang.org/x/text              # Latin-1 encoding support
```

Everything else (CSV parsing, JSON, XML, HTTP client, SHA-256, static file serving) is Go stdlib.

## Development

### Backend

```bash
# Run with a local PostgreSQL
export DATABASE_URL=postgres://finance:finance@localhost:5432/finance?sslmode=disable
go run ./cmd/server
```

### Frontend

```bash
cd frontend
npm install
npm run dev    # Vite dev server with API proxy to :8443
```

### Tests

```bash
go test ./...
```

## API endpoints

| Method | Path | Description |
|--------|------|-------------|
| POST | `/api/import` | Upload CSV (multipart, `file` + `account_id`) |
| GET | `/api/transactions?limit=50&offset=0` | List transactions |
| GET | `/api/portfolio/holdings` | Current holdings (materialized view) |
| GET | `/api/portfolio/networth?days=365` | Net worth snapshots |
| GET | `/api/portfolio/accounts` | Accounts with cash balances |
| GET | `/api/analysis/sectors` | Weighted sector allocation |
| GET | `/api/analysis/countries` | Weighted country allocation |
| GET | `/api/analysis/overlap` | ETF overlap matrix |
| POST | `/api/settings/accounts` | Create account |
| PUT | `/api/settings/accounts/{id}` | Update account |
| GET | `/api/settings/securities` | List securities |
| PUT | `/api/settings/securities/{isin}/symbol` | Set Yahoo Finance ticker |
| GET | `/api/settings/template` | Download holdings CSV template |

## Database schema

Six tables and one materialized view:

- **accounts** — bank accounts (Sparkasse, N26, Scalable Capital)
- **securities** — asset master data keyed by ISIN, with sector/country JSONB
- **transactions** — unified transaction log with SHA-256 dedup hash
- **market_data** — daily closing prices per security
- **etf_holdings** — ETF constituent weights for overlap analysis
- **exchange_rates** — ECB daily FX rates (base EUR)
- **net_worth_snapshots** — daily net worth with cash/investment breakdown
- **current_holdings** (materialized view) — derived from transaction log

## Configuration

Environment variables (see `.env.example`):

| Variable | Default | Description |
|----------|---------|-------------|
| `DATABASE_URL` | — | PostgreSQL connection string |
| `PORT` | `8443` | HTTP listen port |
| `SESSION_SECRET` | — | Cookie signing secret |
| `BACKUP_PATH` | `/backups` | Directory for nightly pg_dump |
| `TZ` | `Europe/Berlin` | Timezone for cron schedules |

## Security

- **Zero bank credentials** stored anywhere — CSV-only means the app never touches your bank login
- Cookie-based session auth with bcrypt-hashed password
- Container hardening: `cap_drop: ALL`, `no-new-privileges`, non-root user
- PostgreSQL only accessible via Docker internal network
- Nightly `pg_dump` backups to mounted volume

## License

MIT
