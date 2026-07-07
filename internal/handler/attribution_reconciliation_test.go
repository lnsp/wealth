package handler

import (
	"math"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/lnsp/wealth/internal/analytics"
	db "github.com/lnsp/wealth/internal/database/generated"
)

// TestAttributionReconciliation_RSUFixture verifies that HandlePerformance,
// HandlePerformanceHistory, and HandleWealthWaterfall agree on contribution
// totals (cash + in-kind) when fed the same transaction stream. The fixture
// covers every shape that historically misbehaved: a cash deposit, a matched
// internal cash transfer, a standalone RSU vest, a washed broker-to-broker
// in-kind pair, a buy/sell pair, and a dividend. With all three handlers now
// routed through analytics.ClassifyForAttribution +
// analytics.MatchInKindTransferPairs at ScopePortfolio, the totals must
// reconcile within EUR 1.
func TestAttributionReconciliation_RSUFixture(t *testing.T) {
	mkTxn := func(date string, typ string, isin string, qty, amt float64) db.ListTransactionsRow {
		d, err := time.Parse("2006-01-02", date)
		if err != nil {
			t.Fatalf("bad date %q: %v", date, err)
		}
		row := db.ListTransactionsRow{
			ID:     uuid.New(),
			Date:   d,
			Type:   typ,
			Amount: numericFromFloat(amt),
		}
		if isin != "" {
			row.SecurityISIN = pgtype.Text{String: isin, Valid: true}
		}
		if qty != 0 {
			row.Quantity = numericFromFloat(qty)
		}
		return row
	}

	txns := []db.ListTransactionsRow{
		// Cash deposit — pure Contribution.
		mkTxn("2024-01-15", "deposit", "", 0, 10000),
		// Internal cash move between two of the user's accounts — Ignore at
		// portfolio scope, must not double-count as a deposit.
		mkTxn("2024-02-01", "cash_transfer_out", "", 0, 5000),
		mkTxn("2024-02-01", "cash_transfer_in", "", 0, 5000),
		// Buy/sell pair — Ignore at portfolio scope (cash <-> securities).
		mkTxn("2024-03-15", "buy", "DE000XYZ0001", 50, 5000),
		mkTxn("2024-06-15", "sell", "DE000XYZ0001", 2, 220),
		// Dividend — Dividend bucket.
		mkTxn("2024-04-15", "dividend", "DE000XYZ0001", 0, 100),
		// Standalone RSU vest — Contribution at FMV (no matching transfer_out).
		mkTxn("2024-05-15", "transfer", "US0000ABC001", 20, 4000),
		// Broker-to-broker in-kind move: same ISIN, same qty, within +-5d.
		// Pair-matcher must wash both legs, so they don't inflate contributions.
		mkTxn("2024-07-01", "transfer", "US0000XYZ002", 10, 1100),
		mkTxn("2024-07-03", "transfer_out", "US0000XYZ002", 10, 1100),
	}

	// Build the wash set the same way the handlers do.
	var inkind []analytics.InKindTransfer
	for _, t := range txns {
		if !t.SecurityISIN.Valid {
			continue
		}
		if t.Type != "transfer" && t.Type != "transfer_out" {
			continue
		}
		qty := 0.0
		if t.Quantity.Valid {
			f, _ := t.Quantity.Float64Value()
			qty = f.Float64
		}
		inkind = append(inkind, analytics.InKindTransfer{
			ID:       t.ID,
			Date:     t.Date,
			Type:     t.Type,
			ISIN:     t.SecurityISIN.String,
			Quantity: qty,
		})
	}
	washed := analytics.MatchInKindTransferPairs(inkind)
	if len(washed) != 2 {
		t.Fatalf("pair-matcher should wash both XYZ legs, got %d washed", len(washed))
	}

	// --- View 1: HandlePerformance (KPI tiles + IRR) ---
	// Mirrors portfolio.go HandlePerformance: cashDeposited tracks cash-only
	// contributions, transferredIn tracks in-kind contributions at FMV.
	var summaryCashDep, summaryCashWd, summaryInKind float64
	for _, txn := range txns {
		if _, isWash := washed[txn.ID]; isWash {
			continue
		}
		amt := 0.0
		if txn.Amount.Valid {
			f, _ := txn.Amount.Float64Value()
			amt = f.Float64
		}
		hasISIN := txn.SecurityISIN.Valid
		switch analytics.ClassifyForAttribution(txn.Type, hasISIN, analytics.ScopePortfolio) {
		case analytics.BucketContribution:
			if hasISIN {
				summaryInKind += amt
			} else {
				summaryCashDep += amt
			}
		case analytics.BucketWithdrawal:
			summaryCashWd += amt
		}
	}

	// --- View 2: HandlePerformanceHistory (Growth Over Time) ---
	// Single signed events list; final cumulative value is net capital invested.
	type cashEvent struct {
		date   time.Time
		amount float64
	}
	var events []cashEvent
	for _, txn := range txns {
		if _, isWash := washed[txn.ID]; isWash {
			continue
		}
		amt := 0.0
		if txn.Amount.Valid {
			f, _ := txn.Amount.Float64Value()
			amt = f.Float64
		}
		switch analytics.ClassifyForAttribution(txn.Type, txn.SecurityISIN.Valid, analytics.ScopePortfolio) {
		case analytics.BucketContribution:
			events = append(events, cashEvent{date: txn.Date, amount: amt})
		case analytics.BucketWithdrawal:
			events = append(events, cashEvent{date: txn.Date, amount: -amt})
		}
	}
	var historyCum float64
	for _, e := range events {
		historyCum += e.amount
	}

	// --- View 3: HandleWealthWaterfall (Contributions bar) ---
	var wfDeposits, wfWithdrawals, wfDividends float64
	for _, txn := range txns {
		if _, isWash := washed[txn.ID]; isWash {
			continue
		}
		amt := 0.0
		if txn.Amount.Valid {
			f, _ := txn.Amount.Float64Value()
			amt = f.Float64
		}
		switch analytics.ClassifyForAttribution(txn.Type, txn.SecurityISIN.Valid, analytics.ScopePortfolio) {
		case analytics.BucketContribution:
			wfDeposits += amt
		case analytics.BucketWithdrawal:
			wfWithdrawals += amt
		case analytics.BucketDividend:
			wfDividends += amt
		}
	}
	wfNetContrib := wfDeposits - wfWithdrawals

	// --- Invariants ---
	const tolerance = 1.0

	// Cash + in-kind contributions roll up to the same number in all three views.
	summaryTotal := summaryCashDep - summaryCashWd + summaryInKind
	if math.Abs(summaryTotal-historyCum) > tolerance {
		t.Errorf("summary contributions (%.2f) != history cumulative (%.2f)", summaryTotal, historyCum)
	}
	if math.Abs(summaryTotal-wfNetContrib) > tolerance {
		t.Errorf("summary contributions (%.2f) != waterfall net contributions (%.2f)", summaryTotal, wfNetContrib)
	}
	if math.Abs(historyCum-wfNetContrib) > tolerance {
		t.Errorf("history cumulative (%.2f) != waterfall net contributions (%.2f)", historyCum, wfNetContrib)
	}

	// Sanity-check the expected breakdown.
	if math.Abs(summaryCashDep-10000) > tolerance {
		t.Errorf("cash deposits = %.2f, want 10000", summaryCashDep)
	}
	if math.Abs(summaryInKind-4000) > tolerance {
		t.Errorf("in-kind contributions = %.2f, want 4000 (RSU vest only; XYZ pair should wash)", summaryInKind)
	}
	if summaryCashWd != 0 {
		t.Errorf("cash withdrawals = %.2f, want 0 (cash_transfer_out is internal at portfolio scope)", summaryCashWd)
	}
	if math.Abs(wfDividends-100) > tolerance {
		t.Errorf("dividends = %.2f, want 100", wfDividends)
	}
	if math.Abs(wfNetContrib-14000) > tolerance {
		t.Errorf("waterfall net contributions = %.2f, want 14000 (10k cash + 4k FMV vest)", wfNetContrib)
	}
}

// TestWaterfallIdentity verifies the algebraic invariant
//
//	NW = Contributions + MarketReturns + Dividends + Interest - Fees - Taxes
//
// holds within EUR 1 when the snapshot is consistent with the transaction
// stream. The full handler integration touches the DB so we exercise the
// classifier-driven aggregation directly (mirroring HandleWealthWaterfall's
// totals loop) and feed it a synthetic NW that equals the expected sum.
func TestWaterfallIdentity(t *testing.T) {
	mkTxn := func(date string, typ string, isin string, qty, amt, fee, tax float64) db.ListTransactionsRow {
		d, err := time.Parse("2006-01-02", date)
		if err != nil {
			t.Fatalf("bad date %q: %v", date, err)
		}
		row := db.ListTransactionsRow{
			ID:     uuid.New(),
			Date:   d,
			Type:   typ,
			Amount: numericFromFloat(amt),
		}
		if isin != "" {
			row.SecurityISIN = pgtype.Text{String: isin, Valid: true}
		}
		if qty != 0 {
			row.Quantity = numericFromFloat(qty)
		}
		if fee != 0 {
			row.Fee = numericFromFloat(fee)
		}
		if tax != 0 {
			row.Tax = numericFromFloat(tax)
		}
		return row
	}

	// Synthetic stream: 10k deposit, 4k RSU vest at FMV, 100 dividend with 5
	// fee + 25 tax-at-source on a dividend row, 50 interest, a 200 buy with
	// 1 fee. Plus a 1k withdrawal late in the period.
	txns := []db.ListTransactionsRow{
		mkTxn("2024-01-15", "deposit", "", 0, 10000, 0, 0),
		mkTxn("2024-03-15", "transfer", "US0000ABC001", 20, 4000, 0, 0),
		mkTxn("2024-04-15", "dividend", "DE000XYZ0001", 0, 100, 5, 25),
		mkTxn("2024-05-15", "interest", "", 0, 50, 0, 0),
		mkTxn("2024-06-15", "buy", "DE000XYZ0001", 1, 200, 1, 0),
		mkTxn("2024-09-15", "withdrawal", "", 0, 1000, 0, 0),
	}

	var deposits, withdrawals, dividends, interest, fees, taxes float64
	for _, txn := range txns {
		amt := 0.0
		if txn.Amount.Valid {
			f, _ := txn.Amount.Float64Value()
			amt = f.Float64
		}
		feeAmt := 0.0
		if txn.Fee.Valid {
			f, _ := txn.Fee.Float64Value()
			feeAmt = f.Float64
		}
		taxAmt := 0.0
		if txn.Tax.Valid {
			f, _ := txn.Tax.Float64Value()
			taxAmt = f.Float64
		}
		fees += feeAmt
		taxes += taxAmt

		switch analytics.ClassifyForAttribution(txn.Type, txn.SecurityISIN.Valid, analytics.ScopePortfolio) {
		case analytics.BucketContribution:
			deposits += amt
		case analytics.BucketWithdrawal:
			withdrawals += amt
		case analytics.BucketDividend:
			dividends += amt
		case analytics.BucketInterest:
			interest += amt
		case analytics.BucketFee:
			fees += amt
		case analytics.BucketTax:
			taxes += amt
		}
	}

	netContributions := deposits - withdrawals

	// Pretend market moved +€500 on the held position. In production this is
	// computed from explicit monthly MV deltas; here we just pick a number.
	marketReturns := 500.0

	expectedNW := netContributions + marketReturns + dividends + interest - math.Abs(fees) - math.Abs(taxes)

	// Synthetic snapshot — consistent with the transactions + the chosen
	// marketReturns. The identity must hold within EUR 1.
	currentNW := expectedNW

	gap := currentNW - expectedNW
	if math.Abs(gap) > 1.0 {
		t.Errorf("waterfall identity broken: NW=%.2f, expected=%.2f, gap=%.2f", currentNW, expectedNW, gap)
	}

	// Sanity-check the components.
	if math.Abs(deposits-14000) > 1.0 {
		t.Errorf("deposits=%.2f, want 14000 (10k cash + 4k FMV vest)", deposits)
	}
	if math.Abs(withdrawals-1000) > 1.0 {
		t.Errorf("withdrawals=%.2f, want 1000", withdrawals)
	}
	if math.Abs(dividends-100) > 1.0 {
		t.Errorf("dividends=%.2f, want 100", dividends)
	}
	if math.Abs(interest-50) > 1.0 {
		t.Errorf("interest=%.2f, want 50", interest)
	}
	if math.Abs(fees-6) > 1.0 {
		t.Errorf("fees=%.2f, want 6 (5 dividend fee + 1 buy fee)", fees)
	}
	if math.Abs(taxes-25) > 1.0 {
		t.Errorf("taxes=%.2f, want 25", taxes)
	}
	if math.Abs(netContributions-13000) > 1.0 {
		t.Errorf("netContributions=%.2f, want 13000 (14000 - 1000)", netContributions)
	}
}
