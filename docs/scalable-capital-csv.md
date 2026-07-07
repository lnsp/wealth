# Scalable Capital CSV Import Format

The parser supports two CSV formats from Scalable Capital:

## Format 1: Bookmarklet / API Export (primary)

**Encoding:** UTF-8 (with or without BOM)
**Delimiter:** Semicolon (`;`)
**Date format:** ISO 8601 (`YYYY-MM-DD`) or German (`DD.MM.YYYY`)

### Columns

| Column | Required | Description |
|--------|----------|-------------|
| `date` | Yes | Transaction date (`2026-01-15` or `15.01.2026`) |
| `status` | No | `SETTLED`, `CANCELLED`, or `REJECTED`. Only `SETTLED` rows are imported. |
| `type` | No | Transaction category (see below) |
| `sub_type` | No | Sub-classification (see below) |
| `side` | No | `BUY` or `SELL` for security transactions |
| `isin` | Yes | ISIN of the security (e.g. `IE00BK5BQT80`). Empty for cash transactions. |
| `description` | No | Human-readable name (e.g. `Vanguard FTSE All-World`) |
| `quantity` | No | Number of shares/units. Positive for buys, can be negative. |
| `amount` | No | EUR amount. Negative = money out (buy/withdrawal), positive = money in (sell/deposit/dividend). |
| `currency` | No | 3-letter code (defaults to `EUR` if missing) |
| `is_cancellation` | No | `true` if this row reverses a prior transaction |
| `account` | No | `broker` or `savings` — used to auto-detect account type |

### Transaction Types

| `type` | `sub_type` | `side` | Maps to |
|--------|-----------|--------|---------|
| `SECURITY_TRANSACTION` | `SINGLE` | `BUY` | `buy` |
| `SECURITY_TRANSACTION` | `SINGLE` | `SELL` | `sell` |
| `SECURITY_TRANSACTION` | `SAVINGS_PLAN` | `BUY` | `savings_plan` |
| `CASH_TRANSACTION` | `DEPOSIT` | | `deposit` |
| `CASH_TRANSACTION` | `WITHDRAWAL` | | `withdrawal` |
| `CASH_TRANSACTION` | `DISTRIBUTION` | | `dividend` |
| `CASH_TRANSACTION` | `INTEREST` | | `interest` |
| `CASH_TRANSACTION` | `TAX` | | `fee` |
| `CASH_TRANSACTION` | `CASH_TRANSFER_OUT` | | `cash_transfer_out` |
| `CASH_TRANSACTION` | `CASH_TRANSFER_IN` | | `cash_transfer_in` |
| `NON_TRADE_SECURITY_TRANSACTION` | `TRANSFER_OUT` | | `transfer_out` |
| `NON_TRADE_SECURITY_TRANSACTION` | `TRANSFER_IN` | | `transfer` |

### Example

```csv
date;status;type;sub_type;side;isin;description;quantity;amount;currency;is_cancellation
2026-01-15;SETTLED;SECURITY_TRANSACTION;SINGLE;BUY;IE00BK5BQT80;Vanguard FTSE All-World;10;-1200;EUR;false
2026-01-20;SETTLED;CASH_TRANSACTION;DEPOSIT;;;Sparplan;;5000;EUR;false
2026-01-25;SETTLED;CASH_TRANSACTION;DISTRIBUTION;;IE00B4WXJJ64;iShares Bond;;50.25;EUR;false
2026-02-01;SETTLED;CASH_TRANSACTION;TAX;;;Vorabpauschale;;-12.50;EUR;false
2026-02-05;SETTLED;CASH_TRANSACTION;INTEREST;;;Zinsen;;3.75;EUR;false
2026-03-05;SETTLED;SECURITY_TRANSACTION;SAVINGS_PLAN;BUY;IE00BK5BQT80;Vanguard;5;-600;EUR;false
2026-03-10;SETTLED;SECURITY_TRANSACTION;SINGLE;SELL;IE00BK5BQT80;Vanguard;3;360;EUR;false
```

---

## Format 2: PRIME+ Native Export

**Encoding:** UTF-8
**Delimiter:** Semicolon (`;`)

### Columns

| Column | Required | Description |
|--------|----------|-------------|
| `date` / `datum` | Yes | Transaction date |
| `isin` | Yes | Security ISIN |
| `name` / `titel` | No | Security name |
| `shares` / `stück` | No | Number of shares |
| `amount` / `betrag` | No | Transaction amount |
| `currency` / `währung` | No | Currency code |
| `side` | No | Transaction type (see below) |

### Side values (German or English)

| Value | Maps to |
|-------|---------|
| `buy` / `kauf` | `buy` |
| `sell` / `verkauf` | `sell` |
| `dividend` / `dividende` / `distribution` / `ausschüttung` | `dividend` |
| `deposit` / `einzahlung` | `deposit` |
| `withdrawal` / `auszahlung` | `withdrawal` |
| `savings_plan` / `sparplan` | `savings_plan` |
| `interest` / `zinsen` | `interest` |
| `fee` / `gebühr` / `tax` / `steuer` | `fee` |

---

## Behavior Notes

- **Deduplication:** Each row gets a SHA-256 hash from `accountID + date + amount + ISIN + description + quantity`. Re-importing the same CSV is safe.
- **Cancellations:** Rows with `is_cancellation=true` have their type reversed (buy becomes sell, deposit becomes withdrawal, etc.) so they net to zero against the original.
- **Skipped rows:** `CANCELLED` and `REJECTED` status rows are ignored with a warning.
- **Price derivation:** Price per share is computed as `|amount| / |quantity|` when both are present.
- **Amount sign:** Negative = money leaving account (buys, withdrawals), positive = money entering (sells, deposits, dividends).
