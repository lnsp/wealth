# Self-Hosted Personal Finance Tracker — CSV-Only Architecture

**A simplified, privacy-first wealth tracker for Sparkasse, N26, and Scalable Capital built entirely on periodic CSV snapshots — no FinTS, no PSD2, no browser automation.**

By accepting manual CSV uploads as the sole data ingestion method, the architecture eliminates FinTS protocol handling, TAN flows, credential encryption, PSD2 aggregators, and Selenium scraping — while preserving every feature that matters: consolidated net worth, portfolio valuation, ETF decomposition, and overlap analysis. A Go backend compiles to a single ~15MB binary, runs in a scratch container using ~30–50MB of RAM, and has zero runtime dependency management.

---

## Design principles

Three decisions shaped this architecture:

**CSV-only ingestion** eliminates the largest source of complexity. The original plan specified FinTS for Sparkasse, PSD2 for N26, and Selenium for Scalable Capital — three protocols, each with its own authentication flows, failure modes, and maintenance burden. Sparkasse's mandatory SCA meant every FinTS session still required manual TAN approval, making "automated sync" a fiction anyway. CSV export from each bank's web app, uploaded to the dashboard, is the same workflow Portfolio Performance uses — perfectly adequate for a personal wealth tracker.

**Minimal dependencies** were enforced by an explicit redundancy audit. The original plan included `pdfplumber` (PDF parsing — contradicts CSV-only), `chardet` (encoding detection — three known answers don't need a library), `etf-scraper` (thin wrapper over provider CSVs already fetched directly), OpenFIGI API (ISIN→ticker mapping for 10–30 positions — a seed file is simpler), and three charting libraries (Recharts + ECharts + Tremor — ECharts alone covers every visualization). All removed.

**Go over Python** became viable once `python-fints` and `pandas` were no longer needed. The remaining Python libraries (`yfinance`, `justetf-scraping`) are thin HTTP/HTML scraping wrappers trivially replicated in Go. What Go provides in return: a single statically-linked binary (no pip, no virtualenvs, no `--break-system-packages`), a ~15MB Docker image vs. 500–800MB for Python+pandas+numpy, ~30–50MB runtime RAM vs. ~150–200MB, native concurrency for parallel price fetching, and a type system that catches data model errors at compile time.

---

## CSV export sources and formats

### Sparkasse

Export from the Sparkasse online banking portal (varies slightly by regional Sparkasse, but the general path is consistent):

**Checking account transactions**: Umsätze → Export → CSV (MT940 or CAMT.053 text format). Typical columns: Auftragskonto, Buchungstag, Valutadatum, Buchungstext, Verwendungszweck, Begünstigter/Zahlungspflichtiger, Kontonummer, BLZ, Betrag, Währung. Historical depth: 90–180 days per export depending on the Sparkasse.

**Securities depot (Deka/S-Broker)**: The depot overview page typically offers a CSV export of current holdings. If it doesn't, manually enter 5–15 positions into the holdings CSV template the app provides (see Import Pipeline section). This is a one-time effort per rebalance, not a recurring chore.

**Encoding**: ISO-8859-1 (Latin-1). Delimiter: semicolon. Number format: German (comma decimal, period thousands). Date format: DD.MM.YYYY.

### N26

Export from N26 WebApp: Home → Downloads → Account Activity → select date range → Download CSV.

Typical columns (post-2024 format): Date, Payee, Account number, Transaction type, Payment reference, Amount (EUR), Amount (Foreign Currency), Type Foreign Currency, Exchange Rate. The Category column was removed in a 2024 format change.

**Encoding**: UTF-8. Delimiter: comma. Number format: standard (dot decimal). Date format: YYYY-MM-DD. Headers may be English or German depending on account language — the parser detects language from the header row.

### Scalable Capital

**Important**: Scalable Capital's native CSV transaction export (Broker → Transactions → Export CSV) is a **PRIME+ subscription feature**. Free-plan users cannot access it directly.

Third-party Tampermonkey scripts and Chrome extensions exist (e.g., `Scalable-Capital-Transactions-Exporter`, `Scalable-Capital-Depot-CSV-Export`, the "Scalable Capital Transaction Exporter" Chrome extension), but **none are recommended here**. They run JavaScript inside your authenticated Scalable Capital session with full access to session cookies, bearer tokens, and API responses. The repos have very small communities (4 stars each), single authors, no security audits, and no automated tests. The Chrome extension is additionally a marketing funnel for a third-party portfolio service and can auto-update with new code at any time. A compromised author account or malicious update could silently exfiltrate your account data.

**Recommended approach: a self-authored bookmarklet.** Scalable Capital's web app uses a GraphQL-style BFF endpoint at `POST /broker/api/data`. The response is an array of operation results with `__typename`-discriminated union types. Authentication is cookie-based via an encrypted `appSession` JWE token — `fetch()` from the same origin includes it automatically, no token extraction needed.

The API returns two transaction types: `BrokerSecurityTransactionSummary` (buys, sells — contains `isin`, `quantity`, `side`, `amount`) and `BrokerCashTransactionSummary` (deposits, dividends — contains `cashTransactionType`, `relatedIsin` for distributions). Amounts are signed: negative for buys, positive for sells and distributions. Pagination is cursor-based via `moreTransactions.cursor`.

**Important limitation**: The summary endpoint does not return `fee` or `tax` fields — only the net `amount`. To get fee/tax breakdown, you would need to query individual transaction detail endpoints (inspect DevTools when clicking into a single transaction). For portfolio tracking purposes, the net amount is typically sufficient since fees are already deducted.

Run this bookmarklet while logged in on `de.scalable.capital/broker/transactions`:

```javascript
javascript:void(async function(){
  const portfolioId = new URLSearchParams(location.search).get('portfolioId')
    || prompt('Enter your portfolioId from the URL:');
  if (!portfolioId) return alert('No portfolioId found');

  // Reconstruct the GraphQL query from the known response shape.
  // NOTE: The exact query string must match what the web app sends.
  // Capture it from DevTools → Network → Payload tab on any
  // /broker/api/data request, then paste it here verbatim.
  // The structure below is inferred from the response and may need
  // minor adjustments to field names or argument syntax.
  const query = `query MoreTransactions($portfolioId: String!, $cursor: String, $limit: Int) {
    account {
      id
      brokerPortfolio(id: $portfolioId) {
        id
        moreTransactions(cursor: $cursor, limit: $limit) {
          cursor
          total
          transactions {
            id currency type status isCancellation lastEventDateTime description
            ... on BrokerSecurityTransactionSummary {
              securityTransactionType quantity amount side isin
            }
            ... on BrokerCashTransactionSummary {
              cashTransactionType amount relatedIsin
            }
          }
        }
      }
    }
  }`;

  const allTxns = [];
  let cursor = null;

  // Paginate through all transactions
  while (true) {
    const resp = await fetch('/broker/api/data', {
      method: 'POST',
      credentials: 'same-origin',
      headers: {
        'Content-Type': 'application/json',
        'x-scacap-features-enabled': 'CRYPTO_MULTI_ETP,UNIQUE_SECURITY_ID',
      },
      body: JSON.stringify([{
        query,
        variables: { portfolioId, cursor, limit: 50 },
      }]),
    });
    const json = await resp.json();
    const page = json[0]?.data?.account?.brokerPortfolio?.moreTransactions;
    if (!page?.transactions?.length) break;
    allTxns.push(...page.transactions);
    cursor = page.cursor;
    if (!cursor || allTxns.length >= page.total) break;
  }

  // Build CSV — separate columns for security vs cash fields
  const header = [
    'date','status','tx_type','sub_type','side','isin',
    'description','quantity','amount','currency'
  ].join(';');

  const rows = allTxns
    .filter(t => t.status === 'SETTLED')
    .map(t => {
      const date = t.lastEventDateTime?.split('T')[0] || '';
      const isSecurity = t.__typename === 'BrokerSecurityTransactionSummary';
      return [
        date,
        t.status,
        t.type,                                          // SECURITY_TRANSACTION or CASH_TRANSACTION
        isSecurity ? t.securityTransactionType : t.cashTransactionType,  // SINGLE, DEPOSIT, DISTRIBUTION, etc.
        isSecurity ? t.side : '',                        // BUY, SELL, or empty for cash
        (isSecurity ? t.isin : t.relatedIsin) || '',     // ISIN (relatedIsin for dividends)
        (t.description || '').replace(/;/g, ','),        // sanitize semicolons
        isSecurity ? t.quantity : '',
        t.amount,
        t.currency,
      ].join(';');
    });

  const csv = [header, ...rows].join('\n');
  const blob = new Blob(['\uFEFF' + csv], {type: 'text/csv;charset=utf-8'});
  const a = document.createElement('a');
  a.href = URL.createObjectURL(blob);
  a.download = 'scalable-transactions-' + new Date().toISOString().slice(0,10) + '.csv';
  a.click();
  alert('Exported ' + rows.length + ' settled transactions');
}())
```

**One thing you must verify before running**: The GraphQL `query` string above is reconstructed from the response shape, not captured from the actual request payload. Scalable Capital's server may reject it if the field names or argument syntax differ from what their schema expects. To get the exact query:

1. Open DevTools → Network → filter `/broker/api/data`
2. Click any request → Payload tab
3. Copy the `query` string from the request body
4. Replace the `query` constant in the bookmarklet with your captured version

Everything else in the script — pagination loop, response path, field mappings, CSV generation — is based on the verified response structure and should work without modification.

If you have a PRIME+ subscription, simply use the native Broker → Transactions → Export CSV button instead — none of this is needed.

**Transaction CSV format**: The bookmarklet outputs semicolon-delimited UTF-8 with BOM. Columns: date, status, tx_type (SECURITY_TRANSACTION / CASH_TRANSACTION), sub_type (SINGLE / DEPOSIT / DISTRIBUTION), side (BUY / SELL), isin, description, quantity, amount (signed — negative for buys), currency. This covers all transaction types: equity trades, ETF purchases, savings plan deposits, and dividend distributions.

**Current holdings**: No direct export exists. Two approaches:
- **Preferred**: The transaction CSV contains everything needed — the app reconstructs current holdings by replaying buy/sell quantities per ISIN. This also captures cost basis.
- **Alternative**: Adapt the bookmarklet to query the portfolio holdings operation instead (inspect DevTools on the depot overview page — it will be a different GraphQL query to the same `/broker/api/data` endpoint).

**Encoding**: UTF-8 with BOM (`\uFEFF` prefix ensures Excel opens correctly). Delimiter: semicolon. Number format: dot decimal (raw API values).

---

## System architecture

### Stack

| Layer | Technology | Rationale |
|-------|-----------|-----------|
| **Language** | Go 1.22+ | Single binary, tiny container, native concurrency, zero runtime deps. |
| **HTTP router** | `net/http` (stdlib) + Chi | Chi adds middleware and route grouping without framework overhead. Compatible with stdlib `http.Handler`. |
| **Database driver** | pgx v5 | The de facto PostgreSQL driver for Go. Connection pooling built in. |
| **Query layer** | sqlc | Generates type-safe Go code from SQL queries. No ORM magic — you write SQL, sqlc gives you typed functions. |
| **Migrations** | goose | Simple, file-based SQL migrations. Embeddable in the binary via `embed`. |
| **Scheduler** | robfig/cron v3 | In-process cron scheduler. Goroutine-based, no external broker. |
| **HTML scraping** | goquery | jQuery-like selectors for justETF scraping. Only external dependency for data ingestion. |
| **CSV parsing** | `encoding/csv` (stdlib) | Standard library. Combined with `golang.org/x/text/encoding` for Latin-1 → UTF-8 transcoding. |
| **Frontend** | React 18 + Vite | Unchanged — built to static files, embedded in the Go binary. |
| **Charts** | Apache ECharts (`echarts-for-react`) | Single library for all visualizations: area, bar, line, pie/donut, heatmap, treemap. |
| **UI components** | shadcn/ui + Tailwind CSS | Unstyled primitives (cards, tables, dialogs) — no charting engine bundled. |

### Dependency inventory

The Go module has exactly these direct dependencies — nothing else:

```
github.com/go-chi/chi/v5          # HTTP routing
github.com/jackc/pgx/v5           # PostgreSQL driver + pool
github.com/pressly/goose/v3       # Migrations
github.com/robfig/cron/v3         # Scheduler
github.com/PuerkitoBio/goquery    # HTML scraping (justETF)
golang.org/x/text                 # Latin-1 encoding support
```

Everything else — CSV parsing, JSON handling, XML parsing (ECB rates), HTTP client (Yahoo Finance, provider CSVs), SHA-256 hashing (dedup), static file serving, `embed` (SPA + migrations) — is Go stdlib.

### Deployment: two containers

```
┌──────────────────────────────────────────────────────┐
│            Docker Host (Synology / QNAP NAS)         │
│                                                        │
│  ┌──────────────────────────────────────────────────┐ │
│  │   App Container (FROM alpine, ~30MB)              │ │
│  │                                                    │ │
│  │   Single Go binary: finance-tracker                │ │
│  │     ├── HTTP API (Chi router)                      │ │
│  │     ├── Serves React SPA (embedded via go:embed)   │ │
│  │     ├── CSV upload + parsing                       │ │
│  │     ├── Portfolio valuation + analytics            │ │
│  │     ├── Cron scheduler (robfig/cron)               │ │
│  │     │     ├── Weekdays 18:30: price update          │ │
│  │     │     ├── Weekdays 16:00: ECB FX rates         │ │
│  │     │     ├── Sunday 03:00: ETF holdings refresh   │ │
│  │     │     ├── Daily 19:00: net worth snapshot       │ │
│  │     │     ├── Daily 19:05: price alert evaluation   │ │
│  │     │     └── Daily 02:00: pg_dump backup          │ │
│  │     └── Embedded DB migrations (goose)             │ │
│  └────────────────────┬─────────────────────────────┘ │
│                        │ (Docker internal network)     │
│  ┌────────────────────┴─────────────────────────────┐ │
│  │          PostgreSQL 16 (Alpine, ~80MB image)      │ │
│  │          Volume: /nas/docker/finance-db            │ │
│  └──────────────────────────────────────────────────┘ │
└──────────────────────────────────────────────────────┘

Image sizes:   App ~30MB  |  PostgreSQL ~80MB  |  Total ~110MB
Runtime RAM:   App ~30–50MB  |  PostgreSQL ~150–250MB  |  Total ~200–300MB
```

The React SPA builds to static files (~2–5MB) embedded directly into the Go binary via `go:embed`. At startup, the binary runs pending goose migrations, starts the cron scheduler, and begins serving HTTP. No process manager, no init system, no sidecar.

For Synology DSM 7.2+, deploy via Container Manager on a non-standard port (e.g., 8443) since DSM occupies 80/443. Optionally add Caddy for HTTPS.

### Dockerfile

```dockerfile
# --- Build stage ---
FROM node:20-alpine AS frontend
WORKDIR /app/frontend
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci
COPY frontend/ ./
RUN npm run build

FROM golang:1.22-alpine AS backend
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=frontend /app/frontend/dist ./frontend/dist
RUN CGO_ENABLED=0 GOOS=linux go build -o /finance-tracker ./cmd/server

# --- Runtime stage ---
FROM alpine:3.19
RUN apk add --no-cache postgresql16-client tzdata ca-certificates
COPY --from=backend /finance-tracker /usr/local/bin/finance-tracker
EXPOSE 8443
ENTRYPOINT ["finance-tracker"]
```

Alpine is used for the runtime stage because `pg_dump` is needed for the backup cron job and `ca-certificates` for HTTPS calls to Yahoo Finance and ECB. Total image: ~30MB.

---

## Data model

Thirteen tables (accounts, securities, transactions, market_data, etf_holdings, exchange_rates, net_worth_snapshots, import_history, target_allocations, financial_goals, price_alerts, notifications, wealth_reports), one materialized view (current_holdings). The schema is expressed as goose SQL migrations and compiled into the binary via `embed`.

### accounts

| Column | Type | Notes |
|--------|------|-------|
| id | UUID | Primary key, `gen_random_uuid()` |
| name | TEXT | e.g., "Sparkasse Girokonto", "Scalable Capital Depot" |
| institution | TEXT | `sparkasse`, `n26`, `scalable_capital` |
| type | TEXT | `checking`, `savings`, `brokerage` |
| currency | TEXT | `EUR` default |
| iban | TEXT | Nullable (brokerages may not have one) |
| is_active | BOOLEAN | Soft delete, default `true` |
| created_at | TIMESTAMPTZ | |

### securities

Asset master data, keyed by ISIN.

| Column | Type | Notes |
|--------|------|-------|
| isin | TEXT | Primary key (e.g., `IE00B3RBWM25`) |
| wkn | TEXT | Nullable |
| symbol | TEXT | Yahoo Finance ticker (e.g., `EUNL.DE`). Set from seed file or manually in settings. |
| name | TEXT | Display name |
| asset_class | TEXT | `etf`, `stock`, `bond`, `fund`, `commodity` |
| currency | TEXT | Denomination currency |
| ter | NUMERIC(6,4) | Total Expense Ratio (ETFs only) |
| sector_weights | JSONB | `{"Technology": 22.1, "Healthcare": 13.5, ...}` |
| country_weights | JSONB | `{"US": 62.3, "JP": 6.1, ...}` |
| metadata_updated_at | TIMESTAMPTZ | Last sector/country refresh |

### transactions

Unified table for all financial movements.

| Column | Type | Notes |
|--------|------|-------|
| id | UUID | Primary key |
| account_id | UUID | FK → accounts |
| date | DATE | Transaction date |
| type | TEXT | `buy`, `sell`, `dividend`, `interest`, `deposit`, `withdrawal`, `fee`, `transfer`, `savings_plan` |
| security_isin | TEXT | FK → securities, nullable (null for cash transactions) |
| quantity | NUMERIC(18,8) | Shares/units, nullable |
| price | NUMERIC(18,8) | Price per unit at execution, nullable |
| amount | NUMERIC(18,4) | Total amount in account currency |
| fee | NUMERIC(18,4) | Transaction fees, default 0 |
| tax | NUMERIC(18,4) | Withholding tax, default 0 |
| currency | TEXT | Transaction currency |
| counterparty | TEXT | Payee/payer for bank transactions |
| reference | TEXT | Verwendungszweck / payment reference |
| category | TEXT | Nullable, for spending categorization |
| import_hash | TEXT | UNIQUE. SHA-256 of key fields for deduplication |

**Deduplication**: `import_hash` = SHA-256(account_id + date + amount + reference + counterparty). On CSV re-import, `INSERT ... ON CONFLICT (import_hash) DO NOTHING` skips existing rows. Safe for overlapping date range re-uploads.

### market_data

| Column | Type | Notes |
|--------|------|-------|
| security_isin | TEXT | FK → securities, composite PK |
| date | DATE | Trading day, composite PK |
| close | NUMERIC(18,8) | Closing price |
| currency | TEXT | Price currency |

### etf_holdings

| Column | Type | Notes |
|--------|------|-------|
| etf_isin | TEXT | FK → securities, composite PK |
| holding_isin | TEXT | Constituent ISIN, composite PK |
| holding_name | TEXT | e.g., "Apple Inc." |
| weight_pct | NUMERIC(8,4) | Allocation percentage |
| sector | TEXT | Holding's sector |
| country | TEXT | Holding's domicile |
| as_of_date | DATE | Snapshot date |

### exchange_rates

| Column | Type | Notes |
|--------|------|-------|
| date | DATE | Composite PK |
| currency | TEXT | Composite PK (target currency, base = EUR) |
| rate | NUMERIC(18,8) | EUR → currency rate |

### Materialized view: current_holdings

Computed from the transaction log by summing buy/sell quantities per (account, security) pair:

```sql
CREATE MATERIALIZED VIEW current_holdings AS
SELECT
    t.account_id,
    t.security_isin,
    SUM(CASE WHEN t.type IN ('buy', 'savings_plan') THEN t.quantity
             WHEN t.type = 'sell' THEN -t.quantity
             ELSE 0 END) AS quantity,
    SUM(CASE WHEN t.type IN ('buy', 'savings_plan') THEN t.amount + t.fee
             WHEN t.type = 'sell' THEN -(t.amount - t.fee)
             ELSE 0 END) /
    NULLIF(SUM(CASE WHEN t.type IN ('buy', 'savings_plan') THEN t.quantity
                     WHEN t.type = 'sell' THEN -t.quantity
                     ELSE 0 END), 0) AS avg_cost_basis,
    SUM(CASE WHEN t.type = 'dividend' THEN t.amount ELSE 0 END) AS total_dividends
FROM transactions t
WHERE t.security_isin IS NOT NULL
GROUP BY t.account_id, t.security_isin
HAVING SUM(CASE WHEN t.type IN ('buy', 'savings_plan') THEN t.quantity
                 WHEN t.type = 'sell' THEN -t.quantity
                 ELSE 0 END) > 0;

CREATE UNIQUE INDEX ON current_holdings (account_id, security_isin);
```

Refresh after every CSV import and after daily price updates: `REFRESH MATERIALIZED VIEW CONCURRENTLY current_holdings`.

---

## CSV import pipeline

### Upload flow

```
User uploads CSV ──→ POST /api/import (multipart/form-data)
                         │
                         ├── Read file bytes
                         ├── Try UTF-8 decode, fallback to Latin-1
                         │   (golang.org/x/text/encoding/charmap)
                         ├── Detect institution from header row
                         │     ├── "Auftragskonto" + "Buchungstag" → Sparkasse
                         │     ├── "Payee" or "Empfänger"          → N26
                         │     └── "tx_type" + "sub_type" + "isin"   → Scalable Capital (bookmarklet)
                         │         or "isin"/"ISIN" + "shares"/"Stück" → Scalable Capital (PRIME+ native)
                         │
                         ├── Route to institution-specific parser
                         ├── Normalize to []Transaction structs
                         ├── Compute import_hash per row (crypto/sha256)
                         ├── Batch INSERT ... ON CONFLICT DO NOTHING
                         ├── Auto-create securities for new ISINs
                         │     (name from CSV, symbol left blank → user sets in settings)
                         │
                         ├── REFRESH MATERIALIZED VIEW CONCURRENTLY current_holdings
                         └── Return JSON: {imported: N, skipped: M, new_securities: K}
```

### Parser interface

```go
type Parser interface {
    // Detect returns true if the header matches this institution.
    Detect(header []string) bool
    // Parse transforms raw CSV records into normalized transactions.
    Parse(records [][]string, accountID uuid.UUID) ([]Transaction, error)
}

// Registered parsers, tried in order:
var parsers = []Parser{
    &SparkasseParser{},
    &N26Parser{},
    &ScalableCapitalParser{},
}
```

**German number parsing**: A shared utility handles `1.234,56` → `1234.56`:

```go
func parseGermanDecimal(s string) (float64, error) {
    s = strings.ReplaceAll(s, ".", "")  // remove thousands separator
    s = strings.Replace(s, ",", ".", 1) // swap decimal separator
    return strconv.ParseFloat(strings.TrimSpace(s), 64)
}
```

**Holdings CSV template**: For Sparkasse depot positions or any institution without clean CSV export, the app provides a downloadable template:

```csv
isin,name,quantity,market_value,currency,date
IE00B3RBWM25,Vanguard FTSE All-World,150.000,17850.00,EUR,2026-03-24
DE0005933931,iShares Core DAX,50.000,7250.00,EUR,2026-03-24
```

Uploaded to the same `/api/import` endpoint — auto-detected by the presence of `isin` + `quantity` + `market_value` in the header. Generates synthetic `buy` transactions to establish positions.

---

## Market data pipeline

### ISIN → ticker mapping

No API. A seed file embedded in the binary maps common German ETFs and stocks:

```go
//go:embed data/ticker_map.json
var tickerMapJSON []byte

// ticker_map.json:
// {
//   "IE00B3RBWM25": "VWRL.AS",
//   "IE00B4L5Y983": "IWDA.AS",
//   "DE0005933931": "EXS1.DE",
//   "IE00BKM4GZ66": "EIMI.AS",
//   ...
// }
```

Ships with ~50 common European ETFs/stocks. Unknown ISINs are flagged in the UI for manual ticker entry in settings. The mapping is stored in the `securities.symbol` column and persists in the database.

### Price feeds (daily, automated)

Yahoo Finance v8 API called directly — no library needed:

```go
func fetchPrices(symbols []string) (map[string]float64, error) {
    // GET https://query1.finance.yahoo.com/v8/finance/chart/{symbol}?range=5d&interval=1d
    // Parse JSON response for regularMarketPrice or chart close array
    // Concurrent fetches via errgroup, ~10 at a time
}
```

For a portfolio of 10–30 positions, all prices arrive in 1–2 seconds via concurrent goroutines. Rate limiting: Yahoo allows ~2,000 calls/hour for unauthenticated access — negligible concern at this scale.

**Fallback**: If Yahoo Finance becomes unreliable, EODHD (€19.99/month) offers an official API with ISIN-based lookups.

### FX rates (daily, automated)

ECB XML feed, parsed with `encoding/xml`:

```go
func fetchECBRates() (map[string]float64, error) {
    // GET https://www.ecb.europa.eu/stats/eurofxref/eurofxref-daily.xml
    // Parse <Cube currency="USD" rate="1.0823"/> elements
    // Returns {"USD": 1.0823, "GBP": 0.8612, ...}
}
```

One HTTP request per day. Historical backfill: `eurofxref-hist.xml` (full history since 1999).

### ETF holdings (weekly, automated)

Two data paths, both using Go's `net/http` + `goquery`:

**Provider CSV downloads** (full holdings): iShares/BlackRock publish CSV files on every product page. The URL pattern is predictable given a fund identifier. Same for Xtrackers/DWS and Amundi. These CSVs contain holding name, ISIN, weight, sector, and country — everything needed for decomposition and overlap. Parsed with `encoding/csv`.

**justETF scraping** (sector/country aggregates + metadata): Fetches the ETF profile page, extracts pre-computed sector and country allocation percentages, TER, fund size, and replication method using goquery selectors. Useful for ETFs where full holdings CSVs aren't easily available. Writes to `securities.sector_weights` and `securities.country_weights` JSONB columns.

**Refresh cadence**: Weekly for holdings (index ETFs rebalance monthly, reconstitute quarterly). Quarterly for metadata. Daily for prices and FX.

### Cron scheduler

```go
c := cron.New(cron.WithSeconds())

c.AddFunc("0 30 18 * * 1-5", updatePrices)      // weekdays 18:30, after EU close
c.AddFunc("0 0 16 * * 1-5",  updateFXRates)      // weekdays 16:00
c.AddFunc("0 0 3 * * 0",     updateETFHoldings)   // Sundays 03:00
c.AddFunc("0 0 19 * * *",    snapshotNetWorth)    // daily 19:00
c.AddFunc("0 5 19 * * *",    evaluatePriceAlerts) // daily 19:05
c.AddFunc("0 0 2 * * *",     backupDatabase)       // daily 02:00

c.Start()
```

Each job runs in its own goroutine. `updatePrices` fetches all tickers concurrently with an `errgroup` (bounded to 10 concurrent requests). Failed jobs log errors and retry on next schedule — no dead letter queue needed at this scale.

---

## Portfolio analytics engine

### Net worth computation

```
Net Worth = Cash Balances + Investment Portfolio Value + Other Assets

Cash Balances:
  = running total from bank transaction imports per checking/savings account
  = Σ(deposits) - Σ(withdrawals) + Σ(interest) - Σ(fees) per account

Investment Portfolio Value:
  = Σ (current_holdings.quantity × latest market_data.close × fx_rate_to_EUR)
  across all brokerage accounts

Other Assets (optional):
  = manually entered via settings (property, vehicles, etc.)
```

**Historical net worth**: The daily cron job computes and stores a snapshot in a `net_worth_snapshots` table (date, total, cash_component, investment_component). For dates before installation, back-computation from the transaction log + historical prices fills the gap. This powers the primary dashboard chart.

### Performance calculations

**Time-Weighted Return (TWR)**: Standard for benchmark comparison, independent of cash flow timing.

```go
func CalculateTWR(valuations []DailyValuation, cashflows []CashFlow) float64 {
    periods := splitByCashflows(valuations, cashflows)
    twr := 1.0
    for _, p := range periods {
        r := (p.EndValue - p.StartValue - p.NetFlow) / p.StartValue
        twr *= (1 + r)
    }
    return twr - 1.0
}
```

**Money-Weighted Return (MWR/IRR)**: Accounts for contribution timing — useful for evaluating savings plan performance. Computed via Newton-Raphson with step damping and bisection fallback to prevent divergence on complex cashflow patterns:

```go
func CalculateIRR(cashflows []CashFlow, guess float64) float64 {
    rate := guess
    for i := 0; i < 100; i++ {
        npv, dnpv := 0.0, 0.0
        for _, cf := range cashflows {
            t := cf.Date.Sub(cashflows[0].Date).Hours() / (365.25 * 24)
            npv += cf.Amount / math.Pow(1+rate, t)
            dnpv += -t * cf.Amount / math.Pow(1+rate, t+1)
        }
        if math.Abs(npv) < 1e-8 { break }
        rate -= npv / dnpv
    }
    return rate
}
```

**Per-position P&L**: Unrealized = (quantity × current_price) - (quantity × avg_cost_basis). Realized = computed from matched sell transactions using FIFO or average cost method.

### ETF decomposition

**Sector allocation** (weighted aggregation):

```go
func ComputeSectorExposure(holdings []Holding) map[string]float64 {
    totalValue := 0.0
    for _, h := range holdings { totalValue += h.MarketValue }

    exposure := make(map[string]float64)
    for _, h := range holdings {
        weight := h.MarketValue / totalValue
        for sector, pct := range h.Security.SectorWeights {
            exposure[sector] += weight * pct / 100
        }
    }
    return exposure // {"Technology": 0.261, "Healthcare": 0.134, ...}
}
```

Country allocation uses the identical pattern with `CountryWeights`.

### Overlap computation

**Pairwise overlap**: Overlap(A, B) = Σ min(w_A_i, w_B_i) for each holding present in both ETFs.

```go
func ComputeOverlap(a, b map[string]float64) float64 {
    overlap := 0.0
    for isin, weightA := range a {
        if weightB, ok := b[isin]; ok {
            overlap += math.Min(weightA, weightB)
        }
    }
    return overlap // 0–100 percentage
}

func BuildOverlapMatrix(etfs []ETFWithHoldings) [][]float64 {
    n := len(etfs)
    matrix := make([][]float64, n)
    for i := range matrix {
        matrix[i] = make([]float64, n)
        matrix[i][i] = 100.0
        for j := i + 1; j < n; j++ {
            o := ComputeOverlap(etfs[i].Holdings, etfs[j].Holdings)
            matrix[i][j] = o
            matrix[j][i] = o
        }
    }
    return matrix
}
```

**Effective per-holding exposure** reveals concentration across ETFs:

```go
func ComputeEffectiveExposure(holdings []Holding) map[string]float64 {
    totalValue := 0.0
    for _, h := range holdings { totalValue += h.MarketValue }

    exposure := make(map[string]float64)
    for _, h := range holdings {
        portfolioWeight := h.MarketValue / totalValue
        for isin, holdingWeight := range h.ETFHoldings {
            exposure[isin] += portfolioWeight * holdingWeight / 100
        }
    }
    return exposure // {"US0378331005": 8.2, ...} → "8.2% of portfolio is Apple"
}
```

Overlap above 70% between two ETFs signals high redundancy. Single-stock exposure above 5% signals concentration risk.

---

## Frontend dashboard

### Page structure

**1. Net Worth Overview** (landing page)
- Hero KPI: total net worth in EUR with change (absolute + percentage) over selected period. Shows Liquid Net Worth alongside Total when illiquid assets exist. Data freshness indicator shows snapshot age with color-coded urgency (green=today, yellow=1-3d, orange=3-7d, red=stale).
- Stacked area chart: net worth over time, broken down by account or asset class
- Asset allocation donut chart showing portfolio composition by asset class (Equities, Cash, Real Estate, etc.) when multiple accounts exist
- Account cards grouped by asset class, each with latest balance and per-account sparkline
- Admin-only guards: user management endpoints (create/delete/toggle) require admin role, returning 403 for non-admin users. Role checked from session user_id via database lookup.
- Notification bell icon with unread count badge (desktop sidebar + mobile top header). Dropdown panel shows recent notifications with timestamps, marks as read on open. Polls every 60s.
- Quick import: drag-and-drop zone for CSV upload
- Empty state: welcome screen with onboarding guidance when no data is imported yet

**2. Portfolio**
- Performance attribution: per-holding daily contribution analysis with natural-language summary in the daily digest card (e.g., "Driven by SXR8 (+840 EUR), offset by IS3N (-60 EUR)"). Computed from last two price points × quantity.
- Allocation summary: combined `/api/analysis/summary` endpoint returns sectors, countries, and currency exposure in a single request (reduces Analysis page from 17 to 14 API calls on load).
- Portfolio health score: composite 0-100 score from 5 weighted dimensions — Diversification (25%), Cost Efficiency (15%), Risk Balance (20%), Allocation Discipline (25%), Income Stability (15%). Color-coded gauge on Portfolio page with per-dimension breakdown.
- Benchmark comparison: "What if I had bought X?" — replays user's actual deposit history through a benchmark ETF (default MSCI World), computing what the portfolio would be worth if all deposits had gone into the benchmark. Shows dual-line chart and EUR difference.
- Purchasing power: nominal vs inflation-adjusted (real) net worth chart using German HVPI rates (2020-2026). Shows real return, purchasing power lost to inflation, and erosion area between nominal and real lines.
- Estate calculator: German inheritance tax (Erbschaftsteuer) estimator. Add heirs by relationship (spouse, child, sibling, other) with auto-applied Freibeträge (500K/400K/200K/20K). Computes progressive tax per heir and total effective rate.
- Cash flow: monthly income vs expenses stacked bar chart with 12-month forward projection based on trailing averages. Projected bars shown with reduced opacity.
- Allocation history: stacked area chart showing how each holding's portfolio weight evolved month-by-month over time. Computed from transaction replay.
- Holdings table: ISIN, name, account_name, quantity, avg cost, current price, market value, unrealized P&L (absolute + %), weight. Supports account filter dropdown for multi-broker views.
- Performance chart: TWR and MWR over time, with benchmark overlay (e.g., MSCI World)
- Target allocation & rebalancing: editable target weights per holding with actual vs target comparison bars, drift indicators (on_target/underweight/overweight with color-coded badges), max drift summary. Rebalancing suggestions compute minimum trades to restore targets, with deposit-only mode ("allocate 2,000 EUR to underweight positions"). Shows buy/sell actions with share counts and EUR amounts.
- Dividends dashboard: monthly/yearly/cumulative views with KPI cards (trailing 12-month income, yield on cost, dividend growth YoY), cumulative line chart overlay, per-security breakdown, forward-looking dividend calendar projecting expected payments for the next 12 months based on historical payment patterns per security

**3. Analysis**
- Risk analytics dashboard: annualized volatility, Sharpe ratio, Sortino ratio, max drawdown, Value-at-Risk (95%, 1-day), with MSCI World benchmark comparisons. Drawdown-over-time chart. Computed from daily net worth snapshots with outlier smoothing for data quality. Sharpe/Sortino use mean-daily-return-based annualization to avoid inflating returns from cash deposits.
- Currency exposure: donut chart + horizontal bar breakdown showing underlying currency exposure (USD, EUR, GBP, JPY, etc.) derived from country-to-currency mapping of ETF geographic allocations.
- Tax overview (Germany): realized gains/losses per tax year using average cost method, Sparerpauschbetrag (1,000 EUR) usage progress bar, Teilfreistellung (30% for equity ETFs), estimated Abgeltungssteuer (26.375%), effective tax rate, dividend income, tax-loss harvesting hints with potential savings. Year selector for historical tax years.
- FIFO tax lot inventory: per-security expandable view showing individual FIFO lots with buy date, quantity, cost basis, current value, unrealized P&L, estimated tax if sold (after Teilfreistellung), net proceeds, and effective tax rate. Grouped by ISIN with aggregate totals.
- What-if sell simulator: enter EUR amounts per security, preview FIFO-based tax impact including cost basis, realized gain, Teilfreistellung, taxable gain, estimated tax, net proceeds, and number of FIFO lots consumed. Shows total tax and effective rate across all simulated sells. Includes "Harvest Losses" button that auto-fills losing positions and simulates tax-loss harvesting, showing per-security P&L indicators (green/red) to identify harvest candidates.
- Portfolio costs: weighted average TER, annual/daily cost in EUR, per-holding cost breakdown sorted by fee impact, cumulative fee drag bar chart at 1/5/10/20/30 years, 10-year drag KPI card. Cost benchmarking card with efficiency grade (A+ to D) comparing weighted TER against average German ETF investor (0.38%), showing annual and 10-year savings.
- Sector allocation: donut chart (current) + stacked area (drift over time)
- Country allocation: horizontal bar chart sorted by weight
- ETF overlap matrix: N×N heatmap with overlap percentages
- Top shared holdings: bar chart showing individual stocks with highest aggregate exposure
- Concentration alerts: flagged positions exceeding thresholds

**4. Transactions**
- Unified transaction list across all accounts, filterable by institution/type/date
- Import history: log of all CSV imports with row counts and timestamps

**5. Settings**
- Cheaper ETF alternatives: embedded static JSON of lower-cost ETFs tracking similar indices. Shows annual savings and 10-year projected savings at current position size. Covers 5 common ETF families (All-World, S&P 500, EM, Europe, EUR Govt Bond).
- Caddy HTTPS: optional Caddy reverse proxy via `docker-compose.caddy.yml` overlay. Provides automatic HTTPS (Let's Encrypt in production, self-signed for localhost). Usage: `docker compose -f docker-compose.yml -f docker-compose.caddy.yml up -d`.
- Structured logging: slog JSON handler set as default at startup, standard `log` output redirected through slog via writer adapter. All log output is now JSON-structured for production observability.
- Liability amortization: for accounts of type "liability", shows monthly payment, total interest, and payoff timeline computed from standard mortgage amortization formula. Displayed on Net Worth page when liability accounts exist.
- Encrypted backups: when `BACKUP_AGE_RECIPIENT` env var is set, daily backups are piped through `age -r <recipient>` after gzip, producing `.sql.gz.age` files. Without the key, backups remain plain gzip.
- TOTP/2FA: per-user TOTP setup via /settings/users/{id}/totp/setup (returns secret + provisioning URL), verification via /verify, disable via DELETE. Login requires TOTP code when enabled (returns `totp_required: true` if code missing). Uses pquerna/otp library.
- PDF wealth reports: downloadable A4 PDF generated via go-pdf/fpdf with summary KPIs (net worth start/end, change, dividends, transactions), top gainer/loser, and full holdings table with value/weight/return columns.
- Multi-user sessions: session cookie embeds user_id (format: `user_id|expiry:hmac`). Auth system extracts user_id via `UserIDFromRequest()`. Login stores authenticated user's ID in session. Legacy password-only login maps to first admin user. Backward compatible with existing sessions.
- Multi-user data model: user_id column on accounts, financial_goals, price_alerts, and wealth_reports tables (migration 010). On first startup, creates default admin user and assigns all orphaned data. Foundation for per-user query scoping.
- User management: admin panel for creating, disabling, and deleting users with username/password + role (admin/member). Users table with bcrypt-hashed passwords. Foundation for multi-user data isolation (Journey 16 Step 1).
- Account management (add/edit/deactivate) with 8 account types: checking, savings, brokerage, credit, real_estate, pension, precious_metals, liability. 9 institutions supported. Net Worth page groups accounts by asset class (Investments, Cash, Real Estate, Pension, etc.).
- Securities management (set ticker symbols for new ISINs, override metadata)
- Financial goals: create goals with target amount, target date, monthly contribution, and assumed return rate. Goals show progress bars on Net Worth page with on-track/behind/ahead status and projected future value using compound growth formula.
- Wealth projection: interactive chart showing historical net worth + projected future growth with real-time what-if sliders (300ms debounced) for monthly contribution and expected return rate. Shows target line when goals are set. Save up to 3 named scenarios (localStorage) and toggle them as colored dashed overlays on the chart for comparison. Milestone table below chart shows projected value, cumulative contributions, growth, and inflation-adjusted (2%) real value at 1/5/10/15/20/25/30 year horizons.
- FIRE calculator: computes Financial Independence number from annual expenses and safe withdrawal rate (default 3.5%). Shows progress bar, years to FIRE, and remaining amount. Updates dynamically with user inputs. Includes retirement drawdown phase: when expenses are set, the projection API simulates portfolio depletion after FIRE date, showing longevity (years portfolio lasts), success rate, monthly withdrawal, and a drawdown chart with area fill.
- Rolling risk metrics: 30/90/365-day rolling annualized volatility and Sharpe ratio displayed as line charts. Shows how portfolio risk evolves over time vs single-point lifetime metrics.
- Savings rate: trailing 12-month savings rate KPI, lifetime net deposits, total deposited/withdrawn. Computed from deposit and withdrawal transactions.
- Confidence cone: P10/P90 percentile bands on wealth projection chart, computed from historical portfolio volatility using parametric model (1.28 z-score × monthly vol × sqrt(months)).
- Correlation matrix: pairwise Pearson correlation heatmap between all held securities based on daily price returns. Red-to-green color scale (-1 to +1). Helps identify diversification quality.
- Sensitivity analysis: tornado chart showing how varying return rate (±3%), monthly contribution (±500 EUR), and starting value (±20%) affects the projected final portfolio value. Projection defaults to historical average monthly deposit when no explicit contribution is set.
- FX impact per holding: decomposes each holding's return into asset return (local currency performance) and FX impact (EUR exchange rate effect). Computed from country→currency weight mapping and 1-year EUR/X rate changes. Displayed as additional column in holdings table.
- Historical FX rates chart: EUR/USD, EUR/GBP, EUR/JPY, EUR/CHF time series from ECB data (weekly sampled from ~1,400 daily rates since 2020).
- Daily digest card: dismissible summary at the top of Net Worth page showing daily P&L, unread alert count, and scheduler health status. Reappears on next page load.
- Auto-generated reports: scheduler creates monthly wealth reports on the 1st of each month at 06:00 and annual reports on January 2nd at 06:00. Reports aggregate net worth delta, dividends, and transaction counts for the period.
- Vorabpauschale tracking: per-ETF table showing Jan 1 value, Basiszins (from Bundesbank), Basisertrag, Vorabpauschale amount, and tax owed after Teilfreistellung. Uses historical Basiszins rates (2022-2026).
- Tax report CSV export: downloadable German-language Steuerreport with summary (Gewinne, Verluste, Teilfreistellung, Sparerpauschbetrag, geschätzte Steuer) and per-security breakdown. Suitable for Steuerberater or Anlage KAP filing.
- Price alerts: create threshold alerts (price above/below for securities, net worth milestone). Daily evaluation at 19:05 triggers notifications. Alert management with pause/resume/delete. Notification center with read/unread tracking.
- Wealth reports: generate monthly/annual portfolio summaries with net worth delta, top gainer/loser, dividends received, transaction count, and holdings snapshot. Reports stored as JSON, viewable in-browser with KPI cards and detail panel. Manual generation button for any past month.
- Scheduler status (last run, next run, errors)
- Data export (database dump, CSV export of all transactions)

### Visualization mapping (ECharts only)

| Visualization | ECharts series type | Config notes |
|---------------|-------------------|-------------|
| Net worth over time | `line` with `areaStyle` | Stacked areas, `stack: 'total'` |
| Account sparklines | `line` | Minimal config, no axis labels |
| Holdings weight | `bar` | Horizontal, sorted descending |
| Sector allocation | `pie` | `radius: ['40%', '70%']` for donut |
| Country allocation | `bar` | Horizontal, sorted descending |
| Overlap heatmap | `heatmap` | Cartesian grid, `visualMap` for color scale |
| Allocation treemap | `treemap` | Hierarchy: portfolio → ETF → sector → stock |
| Dividend income | `bar` | Grouped by month, stacked by security |
| Performance chart | `line` | Dual Y-axis (TWR left, MWR right) |
| Top shared holdings | `bar` | Horizontal, aggregate exposure per stock |
| Drawdown over time | `line` with `areaStyle` | Red-shaded area below 0%, from daily snapshots |
| Currency exposure | `pie` | Donut chart with horizontal bar breakdown by currency |

All charts use a single `<ReactECharts>` wrapper component with different `option` props. A shared theme object enforces consistent colors and typography across all visualizations.

### Responsive design (desktop + mobile)

The application targets two primary form factors: desktop browsers (1024px+) and mobile phones (320–480px). The layout adapts at the `md` breakpoint (768px) using Tailwind CSS responsive prefixes.

**Navigation**

| Viewport | Navigation | Behavior |
|----------|-----------|----------|
| Desktop (≥768px) | Fixed left sidebar (224px / `w-56`) | Always visible, vertical link list with icons + labels |
| Mobile (<768px) | Fixed bottom tab bar | 5-tab iOS-style bar with icons + small labels, safe-area padding for notched phones |

The sidebar is hidden on mobile (`hidden md:flex`). The bottom tab bar is hidden on desktop (`md:hidden`). A mobile top header (`md:hidden`) shows the app logo and notification bell. Main content area uses `md:ml-56` to offset for the sidebar on desktop, `pt-12 md:pt-0` for the mobile header, and `pb-[72px]` on mobile to clear the bottom tab bar.

**Content layout adaptations**

| Component | Desktop | Mobile |
|-----------|---------|--------|
| Page padding | `px-8 py-8` | `px-4 py-5` |
| KPI cards (Portfolio) | 4-column grid | 2-column grid (`grid-cols-2 md:grid-cols-4`) |
| Holdings table | Full table with 8 columns | Card layout with stacked info, weight bar, P&L |
| Transactions list | Full table with 6 columns | Compact cards showing counterparty, type badge, amount |
| ETF selector (Analysis) | Wrapped button row | Horizontally scrollable row (`flex-nowrap overflow-x-auto`) |
| ETF holdings drill-down | Full table with ISIN, sector, country | Compact list with rank, name, weight |
| Charts | Full height (400px default) | Reduced height (300–320px), tighter grid margins |
| Account cards | 3-column grid | Single column |
| Performance stats | 3-column row | 3-column with smaller text (`text-apple-caption2 md:text-apple-caption1`) |

**Chart responsiveness**: All charts render at `width: 100%` via the `<ReactECharts>` wrapper. Grid margins are tightened on mobile (e.g., `left: 55` instead of `80`) to maximize chart area. Axis labels use smaller font sizes. The overlap heatmap wraps in a `min-w-[350px]` container with `overflow-x-auto` for horizontal scrolling on narrow screens.

**Touch targets**: Bottom tab bar items use `flex-1` for equal-width tap targets. Interactive buttons have minimum `py-[6px] px-3` padding. The CSV upload zone has `p-6 md:p-10` for comfortable drag-and-drop interaction on both form factors.

**Safe area support**: The viewport meta tag includes `viewport-fit=cover`. The bottom tab bar uses `padding-bottom: env(safe-area-inset-bottom)` for notched phones (iPhone X+). The HTML element applies `env(safe-area-inset-*)` padding.

**Stacked allocation bar (Holdings)**: On both desktop and mobile, a full-width horizontal stacked bar appears above the holdings list showing portfolio composition at a glance. Each segment is color-coded with a legend below. Holdings are sorted by weight descending. On desktop, the table includes a Weight column with color-coded percentage and progress bar. On mobile, cards show the weight bar and percentage inline.

---

## Security

The CSV-only approach means **zero bank credentials exist anywhere in the system**. The user authenticates with their bank in their own browser, exports a CSV file, and uploads it. The security surface is minimal:

**Application authentication**: Cookie-based session auth with bcrypt-hashed password. Even for single-user deployment, this prevents unauthorized access from other devices on the LAN. Optionally add TOTP 2FA (~50 lines with a Go TOTP library).

**Container hardening**: Run containers with `cap_drop: ALL`, `security_opt: no-new-privileges`, non-root user. PostgreSQL listens only on the Docker internal network (not bound to host).

**Network**: LAN-only by default. For remote access, use WireGuard VPN to the NAS — never expose the app directly. If HTTPS is needed locally, Caddy handles automatic certificates.

**Backups**: Nightly `pg_dump` (executed from the app container, which includes `postgresql16-client`) piped to a mounted NAS backup volume. Optionally encrypt with `age` for offsite copies.

---

## Implementation roadmap (~8 weeks)

### Phase 1: Foundation + CSV import (weeks 1–3)

- Set up Go module, Chi router, pgx connection pool
- Write goose migrations for all six tables + materialized view
- Implement three CSV parsers (Sparkasse, N26, Scalable Capital) + auto-detection
- Build deduplication with import_hash (SHA-256 + ON CONFLICT DO NOTHING)
- Build `/api/import` endpoint with multipart file upload
- Scaffold React app (Vite + Tailwind + shadcn/ui)
- Build CSV upload UI (drag-and-drop, auto-detection feedback, import report)
- Basic transaction list page with filtering
- Set up Dockerfile with multi-stage build (frontend → Go → alpine)

**Deliverable**: Working import pipeline with transaction browser.

### Phase 2: Market data + portfolio valuation (weeks 4–5)

- Embed ticker seed file, build settings UI for manual ticker mapping
- Implement Yahoo Finance price fetcher (concurrent goroutines via errgroup)
- Implement ECB FX rate fetcher (stdlib XML parser)
- Set up robfig/cron with daily price/FX jobs
- Build portfolio valuation: holdings × prices × FX = market value
- Implement TWR and IRR calculations
- Build net worth computation + daily snapshot job
- Create net worth dashboard page (hero KPI, ECharts area chart, account cards)

**Deliverable**: Live portfolio valuation and net worth tracking.

### Phase 3: ETF decomposition + overlap (weeks 6–7)

- Build provider CSV downloader (iShares, Xtrackers) for ETF holdings
- Build justETF scraper with goquery for sector/country metadata
- Implement sector and country weighted aggregation
- Implement pairwise overlap computation + overlap matrix
- Build effective per-holding exposure calculation
- Create analysis page: ECharts donut, bar, heatmap, treemap components
- Add concentration risk alerts (>70% overlap, >5% single-stock)

**Deliverable**: Full ETF decomposition and overlap analysis.

### Phase 4: Polish + hardening (week 8)

- Holdings CSV template (downloadable + uploadable)
- Performance chart with MSCI World benchmark overlay
- Risk analytics dashboard (volatility, Sharpe, Sortino, max drawdown, VaR) with benchmark comparison
- Dividend income tracking and bar chart
- Session auth with bcrypt + optional TOTP
- Automated encrypted backups (pg_dump + age)
- Container security hardening (non-root, cap_drop, read-only FS)
- Settings page (accounts, securities, scheduler status, data export)
- Docker Compose file + deployment documentation

**Deliverable**: Production-ready self-hosted deployment.

---

## Project structure

```
finance-tracker/
├── docker-compose.yml
├── Dockerfile
├── go.mod
├── go.sum
├── sqlc.yaml
├── .env.example
│
├── cmd/
│   └── server/
│       └── main.go                    # Entrypoint: migrations, cron, HTTP server
│
├── internal/
│   ├── config/
│   │   └── config.go                  # Env-based config (DATABASE_URL, etc.)
│   │
│   ├── database/
│   │   ├── pool.go                    # pgx connection pool setup
│   │   ├── queries.sql                # sqlc source queries
│   │   └── generated/                 # sqlc output (DO NOT EDIT)
│   │       ├── db.go
│   │       ├── models.go
│   │       └── queries.sql.go
│   │
│   ├── parser/
│   │   ├── parser.go                  # Parser interface + auto-detection
│   │   ├── german.go                  # Shared: number parsing, date parsing, encoding
│   │   ├── sparkasse.go
│   │   ├── n26.go
│   │   └── scalable.go
│   │
│   ├── market/
│   │   ├── yahoo.go                   # Yahoo Finance price fetcher
│   │   ├── ecb.go                     # ECB FX rate fetcher
│   │   └── etf.go                     # Provider CSV + justETF holdings fetcher
│   │
│   ├── analytics/
│   │   ├── portfolio.go               # Valuation, net worth, P&L
│   │   ├── performance.go             # TWR, IRR calculations
│   │   ├── risk.go                    # Volatility, Sharpe, Sortino, max drawdown, VaR
│   │   ├── currency.go                # Country-to-currency mapping, currency exposure
│   │   ├── tax.go                     # German tax calculations (Sparerpauschbetrag, Teilfreistellung, FIFO lots)
│   │   └── decomposition.go           # Sector, country, overlap
│   │
│   ├── handler/
│   │   ├── import.go                  # POST /api/import
│   │   ├── portfolio.go               # GET /api/portfolio, /api/networth
│   │   ├── analysis.go                # GET /api/analysis/sectors, /overlap, /risk, /currency, etc.
│   │   ├── transactions.go            # GET /api/transactions
│   │   ├── alerts.go                  # Price alert CRUD + notification endpoints
│   │   ├── reports.go                 # Wealth report generation + retrieval
│   │   └── settings.go                # CRUD for accounts, securities
│   │
│   └── scheduler/
│       └── jobs.go                    # Cron job definitions
│
├── migrations/
│   ├── 001_initial_schema.sql
│   ├── 002_add_transfer_out.sql
│   ├── 003_import_history.sql
│   ├── 004_target_allocations.sql
│   ├── 005_financial_goals.sql
│   ├── 006_price_alerts.sql
│   ├── 007_wealth_reports.sql
│   ├── 008_extended_account_types.sql
│   └── embed.go                       # go:embed for goose
│
├── data/
│   ├── ticker_map.json                # Seed: ISIN → Yahoo ticker for ~50 ETFs
│   ├── holdings_template.csv          # Downloadable template
│   └── embed.go                       # go:embed
│
├── frontend/
│   ├── package.json
│   ├── vite.config.ts
│   ├── src/
│   │   ├── App.tsx
│   │   ├── pages/
│   │   │   ├── NetWorth.tsx
│   │   │   ├── Portfolio.tsx
│   │   │   ├── Analysis.tsx
│   │   │   ├── Transactions.tsx
│   │   │   └── Settings.tsx
│   │   ├── components/
│   │   │   ├── charts/
│   │   │   │   ├── EChartWrapper.tsx  # Single wrapper for all charts
│   │   │   │   └── theme.ts          # Shared ECharts theme
│   │   │   ├── CsvUploader.tsx
│   │   │   ├── HoldingsTable.tsx
│   │   │   └── AccountCard.tsx
│   │   └── api/
│   │       └── client.ts             # Typed API client
│   └── dist/                          # Build output, embedded in Go binary
│       └── embed.go                   # go:embed
│
└── scripts/
    └── backfill_prices.sh             # One-time historical price backfill
```

---

## Key design decisions summary

| Decision | Choice | Why |
|----------|--------|-----|
| **Ingestion** | CSV-only | Eliminates FinTS, PSD2, Selenium — zero credential storage, zero protocol complexity |
| **Backend** | Go | Single binary, ~30MB image, ~30–50MB RAM, zero runtime deps, native concurrency |
| **Database** | PostgreSQL | Concurrent access, window functions, NUMERIC precision, JSONB for metadata |
| **Query layer** | sqlc | Type-safe generated code from SQL — no ORM overhead, compile-time query validation |
| **Scheduler** | robfig/cron (in-process) | Goroutine-based — no Redis, no broker, no extra container |
| **Charts** | Apache ECharts (sole library) | Covers all visualization types: area, bar, pie, heatmap, treemap |
| **UI components** | shadcn/ui + Tailwind | Unstyled primitives — no bundled charting engine, no framework lock-in |
| **ISIN→ticker** | Embedded seed file + manual settings | No API dependency for 10–30 positions |
| **Deduplication** | SHA-256 + ON CONFLICT DO NOTHING | Safe CSV re-imports, single SQL statement |
| **Holdings tracking** | Derived from transactions | Single source of truth via materialized view |
| **ETF data** | Direct provider CSVs + justETF scraping (goquery) | Most reliable free sources, no wrapper libraries |
| **Price data** | Yahoo Finance v8 (direct HTTP) | No wrapper library, concurrent goroutine fetches |
| **Security** | No credentials stored | CSV-only means nothing to protect beyond the database itself |

---

## Codebase assessment

### Strengths

**1. Type-safe database layer via sqlc**
All SQL lives in `internal/database/queries.sql` as the single source of truth. `sqlc generate` produces type-safe Go code in `internal/database/generated/` — every query is validated at compile time, eliminating SQL injection and type mismatch bugs entirely. The use of `pgtype.*` wrappers (Numeric, Text) for nullable fields is consistent throughout the handler layer.

**2. Comprehensive test coverage with table-driven tests**
30 test files span all major packages: analytics, parser, handler, market, auth. Critical financial calculations (IRR, TWR, overlap detection) use table-driven tests with tolerance-based assertions (`internal/analytics/portfolio_test.go`, `performance_test.go`). Parser tests validate multiple institution formats. This catches silent regressions in financial math before they reach users.

**3. Strong frontend type contract**
`frontend/src/api/client.ts` defines TypeScript interfaces for every API response (Account, HoldingRow, PerformanceData, etc.) and serves as the single contract between frontend and backend. Components follow single responsibility (`HoldingsTable.tsx`, `CsvUploader.tsx`, `ErrorBoundary.tsx`). Changes to the API shape are caught at compile time on both sides.

**4. Correct financial calculations with proper edge-case handling**
`internal/handler/portfolio.go` HandlePerformance (lines 214–404) correctly separates cash-based returns from in-kind transfers, computes IRR on cash flows only (excluding transferred securities), uses average cost for realized P&L, and applies currency conversion via ECB rates. IRR/TWR implementations in `internal/analytics/` use Newton's method with convergence tolerance. This avoids common mistakes like counting transfers as investments.

**5. Batch query optimization in hot paths**
`internal/handler/portfolio.go` loadPriceMap() (lines 47–57) batch-loads all latest prices in a single query and stores them in a map. The holdings loop then reads from the map instead of issuing per-holding queries. The same pattern is used in HandleAccounts and HandlePerformance, keeping the most-visited pages fast regardless of portfolio size.

### Weaknesses

**1. ~~N+1 query problems in analysis handlers~~ — RESOLVED**
All analysis handlers (HandleSectors, HandleCountries, HandleCurrency, HandleTreemap, HandleTopHoldings, HandleAlerts, HandleOverlap) now use `loadEnrichedHoldings()` which batch-loads holdings, securities, and prices in 3 queries total instead of 2N+1 per handler.

**2. ~~Missing input validation on critical endpoints~~ — RESOLVED**
`HandleNetWorth` days parameter is capped at 5000. CSV upload validates file size ≤ 5MB before parsing. `HandleCreateAccount` validates account type against allowed enum (checking, savings, brokerage, credit) and currency code format (3 letters). Transaction list limit already capped at 500.

**3. ~~Import path account mismatch~~ — RESOLVED**
CSVs with an `account` column (e.g., Scalable Capital's `broker`/`savings`) are now validated against the target account type during import. Importing brokerage transactions into a savings account is blocked with a clear error. Cross-account file reuse warns but doesn't block. Remaining: insert errors are still silently skipped for duplicate vs real failure distinction.

**4. ~~Hardcoded magic numbers~~ — RESOLVED**
Configurable values moved to `internal/config/config.go` with environment variable overrides: `BENCHMARK_ISIN`, `BENCHMARK_TICKER`, `PRICE_FETCH_CONCURRENCY`, `REQUEST_TIMEOUT_SECONDS`, `OVERLAP_WARNING_PCT`, `CONCENTRATION_WARNING_PCT`. All have sensible defaults matching the original hardcoded values.

**5. ~~Poor observability~~ — mostly RESOLVED**
Frontend error states on all main pages. All 6 scheduler jobs (prices, FX rates, ETF metadata, net worth, backup, alerts) now report status (ok/error/running) with timestamps and result messages via the scheduler-status endpoint. Remaining: backend uses basic `log.Println` instead of structured `slog`.
