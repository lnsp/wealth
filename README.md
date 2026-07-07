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
- **Multi-user support** with per-user authentication (password + optional TOTP 2FA)

## Quick start

```bash
cp .env.example .env
# Edit .env — set ADMIN_PASSWORD and SESSION_SECRET at minimum

docker compose up -d
```

The app is available at `http://localhost:8443`.

### First steps

1. Go to **Settings** and create an account (e.g., "Sparkasse Girokonto", institution: sparkasse, type: checking)
2. Export a CSV from your bank's web portal
3. On the **Transactions** page, click **Import CSV**, select the account, and upload your file
4. Repeat for each account/institution

### Production deployment (HTTPS)

Use the Caddy overlay for automatic TLS:

```bash
# Set your domain
echo 'DOMAIN=wealth.example.com' >> .env
echo 'BASE_URL=https://wealth.example.com' >> .env

docker compose -f docker-compose.yml -f docker-compose.caddy.yml up -d
```

## Authentication

### Password + TOTP

The app creates a default `admin` user on first start using the `ADMIN_PASSWORD` env var. To enable TOTP (2FA):

1. Log in as admin
2. Go to **Settings > Users**
3. Click **Setup TOTP** on your user
4. Scan the QR code with an authenticator app (e.g., Aegis, Google Authenticator)
5. Enter the verification code to confirm

### Passkeys (WebAuthn)

Passkey support requires `BASE_URL` to be set to your actual domain (e.g., `https://wealth.example.com`). The backend endpoints exist (`/api/auth/webauthn/*`) but the frontend UI is not yet implemented — passkey registration and login must currently be done via the API directly.

## CSV export guides

### Sparkasse

Online banking > Umsaetze > Export > CSV. Typical encoding is ISO-8859-1 with semicolon delimiter and German number format (1.234,56). Date format: DD.MM.YYYY.

### N26

WebApp > Home > Downloads > Account Activity > select date range > Download CSV. UTF-8 encoding, comma delimiter, standard numbers, YYYY-MM-DD dates.

### Scalable Capital

If you have PRIME+, use Broker > Transactions > Export CSV. Otherwise, see the bookmarklet approach in the design document. The app reconstructs current holdings by replaying all buy/sell transactions from the history.

## Architecture

```
┌──────────────────────────────────────────────────┐
│              Container Host                      │
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

## Configuration

Environment variables (see `.env.example`):

| Variable | Default | Description |
|----------|---------|-------------|
| `ADMIN_PASSWORD` | **(required)** | Password for the default admin user |
| `SESSION_SECRET` | **(required)** | Cookie signing secret (min 32 bytes) |
| `DATABASE_URL` | `postgres://...` | PostgreSQL connection string |
| `PORT` | `8443` | HTTP listen port |
| `BASE_URL` | `http://localhost:8443` | Public URL (used for WebAuthn RP origin) |
| `BACKUP_PATH` | `/backups` | Directory for nightly pg_dump |
| `BACKUP_AGE_RECIPIENT` | — | age public key for encrypted backups |
| `TZ` | `Europe/Berlin` | Timezone for cron schedules |
| `RATE_LIMIT_API` | `600` | API requests per minute per IP |
| `RATE_LIMIT_UPLOAD` | `30` | Upload requests per hour per IP |
| `RATE_LIMIT_REPORT` | `20` | Report generation requests per hour per IP |

## Development

```bash
# Start services
make up

# Run Go tests
make test

# Frontend dev server (proxies API to :8443)
cd frontend && npm run dev

# Regenerate sqlc after editing queries.sql
make sqlc
```

See `make help` for all available targets.

## CI/CD

GitHub Actions runs on every push to `main` and on PRs:

- **test** — Go tests (with Postgres service) + frontend build/lint
- **build** — Multi-arch Docker image (amd64 + arm64) pushed to `ghcr.io/lnsp/wealth`

Pushing a `v*` tag (e.g., `git tag v1.0.0 && git push origin v1.0.0`) produces semver-tagged images (`1.0.0`, `1.0`).

## Security

- **Zero bank credentials** stored anywhere — CSV-only means the app never touches your bank login
- Cookie-based session auth with bcrypt-hashed passwords
- Optional TOTP (2FA) with AES-256-GCM encrypted secrets
- CSRF protection via Origin/Referer validation
- Container hardening: `cap_drop: ALL`, `no-new-privileges`, read-only root filesystem, non-root user
- PostgreSQL only accessible via internal container network
- Nightly `pg_dump` backups to mounted volume (optionally age-encrypted)
- Per-IP rate limiting on API, upload, and report endpoints
- Security headers: CSP, X-Frame-Options DENY, HSTS (via Caddy)

## License

MIT
