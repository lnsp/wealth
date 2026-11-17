package handler

import (
	"context"
	"math"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/lnsp/wealth/internal/analytics"
	db "github.com/lnsp/wealth/internal/database/generated"
	"github.com/lnsp/wealth/internal/market"
)

// isSecuritiesOnlyAccountType returns true for account types where transactions
// may consist solely of security buys/sells without explicit cash funding legs.
func isSecuritiesOnlyAccountType(acctType string) bool {
	switch acctType {
	case "brokerage", "crypto", "pension", "precious_metals":
		return true
	default:
		return false
	}
}

// isExplicitCashTxnType returns true when a transaction represents an explicit
// external or internal cash movement on an account.
func isExplicitCashTxnType(txType string) bool {
	switch txType {
	case "deposit", "withdrawal", "cash_transfer_in", "cash_transfer_out":
		return true
	default:
		return false
	}
}

// normalizeCurrencyAndScale maps sub-unit currencies like UK pence ("GBp", "GBX")
// to their base ISO currency ("GBP") and returns the unit scale factor.
func normalizeCurrencyAndScale(currency string) (string, float64) {
	c := strings.TrimSpace(currency)
	if c == "" || strings.EqualFold(c, "EUR") {
		return "EUR", 1.0
	}
	if c == "GBp" || c == "GBX" {
		return "GBP", 0.01
	}
	return strings.ToUpper(c), 1.0
}

// convertAmountToEUR converts an amount in `currency` to EUR using units-per-EUR
// ECB reference rates (`amountEUR = amountForeign / rate`), handling GBp/GBX pence.
func convertAmountToEUR(amount float64, currency string, fxRates map[string]float64) float64 {
	if math.IsNaN(amount) || math.IsInf(amount, 0) || amount == 0 {
		return 0
	}
	norm, scale := normalizeCurrencyAndScale(currency)
	scaled := amount * scale
	if norm == "EUR" {
		return scaled
	}
	if fxRates != nil {
		if rate := fxRates[norm]; rate > 0 && !math.IsNaN(rate) && !math.IsInf(rate, 0) {
			return scaled / rate
		}
	}
	return scaled
}

// convertAmountToEURAtDate converts `amount` in `currency` to EUR as of `date`
// using historical ECB rates (with weekend/holiday fallback to the prior business
// day), falling back to latest `fxFallback` if the date predates stored FX history.
func convertAmountToEURAtDate(amount float64, currency string, date time.Time, histRates []market.HistoricalFXRate, fxFallback map[string]float64) float64 {
	if math.IsNaN(amount) || math.IsInf(amount, 0) || amount == 0 {
		return 0
	}
	norm, scale := normalizeCurrencyAndScale(currency)
	scaled := amount * scale
	if norm == "EUR" {
		return scaled
	}
	if len(histRates) > 0 {
		if rate := market.LookupRateForDate(histRates, norm, date); rate > 0 {
			return scaled / rate
		}
	}
	if fxFallback != nil {
		if rate := fxFallback[norm]; rate > 0 {
			return scaled / rate
		}
	}
	return scaled
}

// loadFXRatesMap loads the latest ECB exchange rates (units per EUR) into a map.
func loadFXRatesMap(ctx context.Context, q *db.Queries) map[string]float64 {
	fx := map[string]float64{"EUR": 1.0}
	rows, err := q.ListLatestExchangeRates(ctx)
	if err != nil {
		return fx
	}
	for _, r := range rows {
		rate := numericToFloat(r.Rate)
		if rate > 0 {
			fx[strings.ToUpper(strings.TrimSpace(r.Currency))] = rate
		}
	}
	return fx
}

// loadHistoricalFXRates loads all stored historical ECB exchange rates sorted by date ASC.
func loadHistoricalFXRates(ctx context.Context, q *db.Queries) []market.HistoricalFXRate {
	rows, err := q.ListAllExchangeRates(ctx)
	if err != nil {
		return nil
	}
	out := make([]market.HistoricalFXRate, 0, len(rows))
	for _, r := range rows {
		rate := numericToFloat(r.Rate)
		if rate > 0 {
			out = append(out, market.HistoricalFXRate{
				Date:     time.Date(r.Date.Year(), r.Date.Month(), r.Date.Day(), 0, 0, 0, 0, time.UTC),
				Currency: strings.ToUpper(strings.TrimSpace(r.Currency)),
				Rate:     rate,
			})
		}
	}
	return out
}

// computeAllAccountCashEUR returns per-account EUR cash balances across active
// accounts, converting each currency bucket via fxRates and clamping synthetic
// negative cash to 0 on securities-only accounts without explicit cash transactions.
func computeAllAccountCashEUR(ctx context.Context, q *db.Queries, accounts []db.Account, fxRates map[string]float64) (map[uuid.UUID]float64, error) {
	acctType := make(map[uuid.UUID]string, len(accounts))
	for _, a := range accounts {
		acctType[a.ID] = a.Type
	}
	rows, err := q.ListAllAccountCashByCurrency(ctx)
	if err != nil {
		return nil, err
	}
	rawEUR := make(map[uuid.UUID]float64, len(accounts))
	hasExplicit := make(map[uuid.UUID]bool, len(accounts))
	for _, r := range rows {
		bal := numericToFloat(r.Balance)
		rawEUR[r.AccountID] += convertAmountToEUR(bal, r.Currency, fxRates)
		if r.HasExplicitCash {
			hasExplicit[r.AccountID] = true
		}
	}
	out := make(map[uuid.UUID]float64, len(accounts))
	for _, a := range accounts {
		if !a.IsActive {
			continue
		}
		cash := rawEUR[a.ID]
		if cash < 0 && !hasExplicit[a.ID] && isSecuritiesOnlyAccountType(acctType[a.ID]) {
			cash = 0
		}
		out[a.ID] = cash
	}
	return out, nil
}

// computeSingleAccountCashEUR computes a single account's EUR cash balance across
// all currency buckets, clamping synthetic negative cash when appropriate.
func computeSingleAccountCashEUR(ctx context.Context, q *db.Queries, acct db.Account, fxRates map[string]float64) (float64, error) {
	rows, err := q.GetAccountCashByCurrency(ctx, acct.ID)
	if err != nil {
		return 0, err
	}
	var cashEUR float64
	hasExplicit := false
	for _, r := range rows {
		bal := numericToFloat(r.Balance)
		cashEUR += convertAmountToEUR(bal, r.Currency, fxRates)
		if r.HasExplicitCash {
			hasExplicit = true
		}
	}
	if cashEUR < 0 && !hasExplicit && isSecuritiesOnlyAccountType(acct.Type) {
		cashEUR = 0
	}
	return cashEUR, nil
}

// txnSignedCashDelta returns the signed cash delta of a transaction in its native
// currency (positive for cash inflows, negative for cash outflows, 0 for in-kind
// security transfers). Assumes parser convention Amount >= 0 (with fallback if
// legacy rows stored signed negative amounts).
func txnSignedCashDelta(txType string, hasSecurityISIN bool, amount, fee, tax float64) float64 {
	mag := math.Abs(amount)
	f := math.Abs(fee)
	tx := math.Abs(tax)
	switch txType {
	case "deposit", "interest", "sell", "dividend", "cash_transfer_in":
		return mag - f - tx
	case "buy", "savings_plan", "withdrawal", "fee", "tax", "cash_transfer_out":
		return -mag - f - tx
	case "transfer":
		if !hasSecurityISIN {
			return -mag - f - tx
		}
		return 0
	default:
		return 0
	}
}

// addMonthsFirstOfMonth normalizes `t` to day 1 of its month in UTC before adding
// `months`, avoiding Go time.AddDate's March-31 -> March-3 overflow bug.
func addMonthsFirstOfMonth(t time.Time, months int) time.Time {
	return time.Date(t.Year(), t.Month()+time.Month(months), 1, 0, 0, 0, 0, time.UTC)
}

// addMonthsClamped adds `months` calendar months to `t`, clamping day-of-month
// to the last valid day of the target month (e.g. Jan 31 + 1 month -> Feb 28/29
// instead of overflowing into March).
func addMonthsClamped(t time.Time, months int) time.Time {
	first := time.Date(t.Year(), t.Month()+time.Month(months), 1, t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), t.Location())
	lastDay := time.Date(first.Year(), first.Month()+1, 0, 0, 0, 0, 0, time.UTC).Day()
	day := t.Day()
	if day > lastDay {
		day = lastDay
	}
	return time.Date(first.Year(), first.Month(), day, t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), t.Location())
}

// listAllTransactionsRows loads all active-account transactions (newest-first,
// with same-day buys ordered after sells so reverse iteration processes buys
// before sells) without any 10,000-row limit.
func listAllTransactionsRows(ctx context.Context, q *db.Queries) ([]db.ListTransactionsRow, error) {
	rows, err := q.ListAllTransactions(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]db.ListTransactionsRow, len(rows))
	for i, r := range rows {
		out[i] = db.ListTransactionsRow(r)
	}
	return out, nil
}

// implicitCashFlowEvent represents an implicit external funding/sweep delta on a
// securities-only account that has no explicit cash transactions (so its negative
// synthetic cash balance is clamped to 0).
type implicitCashFlowEvent struct {
	Date      time.Time
	AccountID uuid.UUID
	DeltaEUR  float64 // > 0 implicit contribution, < 0 implicit withdrawal/sweep
}

// computeImplicitCashClampEvents walks chronologically ordered transactions and
// emits telescoping implicit contribution/withdrawal events for securities-only
// accounts without explicit cash transactions so clamped cash reconciles cleanly
// across HandlePerformance, HandlePerformanceHistory, HandleWealthWaterfall, and HandleTimeMachine.
func computeImplicitCashClampEvents(accounts []db.Account, txnsChrono []db.ListTransactionsRow, histRates []market.HistoricalFXRate, fxFallback map[string]float64) []implicitCashFlowEvent {
	if len(accounts) == 0 || len(txnsChrono) == 0 {
		return nil
	}
	hasExplicit := make(map[uuid.UUID]bool, len(accounts))
	for _, t := range txnsChrono {
		if isExplicitCashTxnType(t.Type) {
			hasExplicit[t.AccountID] = true
		}
	}
	eligible := make(map[uuid.UUID]bool, len(accounts))
	for _, a := range accounts {
		if a.IsActive && isSecuritiesOnlyAccountType(a.Type) && !hasExplicit[a.ID] {
			eligible[a.ID] = true
		}
	}
	if len(eligible) == 0 {
		return nil
	}
	rawCash := make(map[uuid.UUID]float64, len(eligible))
	var out []implicitCashFlowEvent
	for _, t := range txnsChrono {
		if !eligible[t.AccountID] {
			continue
		}
		amtEUR := convertAmountToEURAtDate(numericToFloat(t.Amount), t.Currency, t.Date, histRates, fxFallback)
		feeEUR := convertAmountToEURAtDate(numericToFloat(t.Fee), t.Currency, t.Date, histRates, fxFallback)
		taxEUR := convertAmountToEURAtDate(numericToFloat(t.Tax), t.Currency, t.Date, histRates, fxFallback)
		delta := txnSignedCashDelta(t.Type, t.SecurityISIN.Valid, amtEUR, feeEUR, taxEUR)
		if delta == 0 {
			continue
		}
		prevRaw := rawCash[t.AccountID]
		newRaw := prevRaw + delta
		rawCash[t.AccountID] = newRaw
		prevOffset := math.Max(0, -prevRaw)
		newOffset := math.Max(0, -newRaw)
		imp := newOffset - prevOffset
		if math.Abs(imp) > 1e-9 {
			out = append(out, implicitCashFlowEvent{
				Date:      t.Date,
				AccountID: t.AccountID,
				DeltaEUR:  imp,
			})
		}
	}
	return out
}

// securityTFSRate resolves a security's InvStG Teilfreistellung rate (0.30 equity,
// 0.15 mixed, 0.00 bond/money-market/stock/crypto). Uses s.EquityPct when populated
// (accepting either 0..1 fraction or >1 percentage), and falls back to fund-name
// classification for bond/money-market/mixed funds when equity_pct is NULL.
func securityTFSRate(s db.Security) float64 {
	var eqPtr *float64
	if s.EquityPct.Valid {
		v := numericToFloat(s.EquityPct)
		eqPtr = &v
	}
	return analytics.ResolveSecurityTFSRate(s.AssetClass, s.Name, eqPtr)
}
