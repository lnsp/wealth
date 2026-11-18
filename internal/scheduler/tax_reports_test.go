package scheduler

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/lnsp/wealth/internal/analytics"
	db "github.com/lnsp/wealth/internal/database/generated"
	"github.com/lnsp/wealth/internal/handler"
)

func taxTxn(acct, date, typ, isin, class string, qty, amount, tfs float64) analytics.TaxTransaction {
	d, _ := time.Parse("2006-01-02", date)
	return analytics.TaxTransaction{
		AccountID: acct, Date: date, Year: d.Year(), Type: typ, ISIN: isin, Quantity: qty, Amount: amount,
		AssetClass: class, TFS: tfs, HasExplicitTFS: true, IsEquityFund: tfs >= analytics.TeilfreistellungEquity,
	}
}

// An in-kind depot transfer must not restart the Vorabpauschale clock: 100
// units bought in 2023 and moved to another depot in June 2025 owe the full
// 2025 Vorabpauschale on the 1 January price (100 € × 2.53 % × 0.7 per unit),
// not a 7/12 share on the transfer value as ReconstructVorabpauschaleYear
// booked it.
func TestVorabpauschaleTaxFromLedger_TransferKeepsHoldingPeriod(t *testing.T) {
	etf := "IE00B4L5Y983"
	txns := []analytics.TaxTransaction{
		taxTxn("ing", "2023-02-01", "buy", etf, "etf", 100, 8000, 0.30),
		taxTxn("ing", "2025-06-10", "transfer_out", etf, "etf", 100, 12000, 0.30),
		taxTxn("scalable", "2025-06-11", "transfer", etf, "etf", 100, 12000, 0.30),
	}
	vp := analytics.TaxTransaction{
		Date: "2025-12-31", Year: 2025, Type: "vorabpauschale", ISIN: etf, AssetClass: "etf",
		TFS: 0.30, HasExplicitTFS: true, IsEquityFund: true, VPJan1Price: 100, VPYearEndPrice: 110,
	}
	got := vorabpauschaleTaxFromLedger(analytics.MergeVorabpauschaleEvents(txns, []analytics.TaxTransaction{vp}), 2025)
	// 177.10 € Vorabpauschale × 0.70 × 26.375 % = 32.70 €.
	if math.Abs(got-32.70) > 0.01 {
		t.Errorf("Vorabpauschale tax = %.2f, want 32.70", got)
	}
}

// The harvest alert's saving goes through the engine: a share loss saves tax
// only against share gains above the allowance.
func TestHarvestSavingFromLedger(t *testing.T) {
	goog := "US02079K1079"
	gains := []analytics.TaxTransaction{
		taxTxn("ing", "2025-01-10", "buy", "DE0007164600", "stock", 10, 1000, 0),
		taxTxn("ing", "2026-03-01", "sell", "DE0007164600", "stock", 10, 3000, 0), // +2,000
	}
	lossPos := taxTxn("ing", "2026-04-01", "buy", goog, "stock", 10, 1000, 0)
	now := time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC)
	prices := map[string]float64{goog: 50} // −500

	saving, ok := harvestSavingFromLedger(&handler.TaxLedger{Txns: append(gains, lossPos), Prices: prices}, goog, now)
	if !ok || math.Abs(saving-500*analytics.EffectiveTaxRate) > 0.01 {
		t.Errorf("saving = %.2f (ok %v), want %.2f", saving, ok, 500*analytics.EffectiveTaxRate)
	}
	// No share gains this year: nothing saved now, the loss carries forward.
	saving, ok = harvestSavingFromLedger(&handler.TaxLedger{Txns: []analytics.TaxTransaction{lossPos}, Prices: prices}, goog, now)
	if !ok || saving != 0 {
		t.Errorf("without gains: saving = %.2f (ok %v), want 0", saving, ok)
	}
	// Not held in any taxable depot (e.g. only in a pension wrapper): no alert.
	if _, ok := harvestSavingFromLedger(&handler.TaxLedger{Txns: gains, Prices: prices}, goog, now); ok {
		t.Error("a security without taxable lots must not produce a harvest alert")
	}
}

func snap(date string, total string) db.NetWorthSnapshot {
	d, _ := time.Parse("2006-01-02", date)
	var n pgtype.Numeric
	_ = n.Scan(total)
	return db.NetWorthSnapshot{Date: d, Total: n}
}

// Report backfill starts at the first non-zero snapshot, not at the zero
// placeholders written before any data existed.
func TestFirstNonZeroSnapshot(t *testing.T) {
	snaps := []db.NetWorthSnapshot{snap("2026-05-01", "157000"), snap("2022-12-01", "1500"), snap("2016-01-01", "0"), snap("2018-01-01", "0")}
	got, ok := firstNonZeroSnapshot(snaps)
	if !ok || got.Format("2006-01-02") != "2022-12-01" {
		t.Errorf("first non-zero = %v (%v), want 2022-12-01", got, ok)
	}
	if _, ok := firstNonZeroSnapshot([]db.NetWorthSnapshot{snap("2016-01-01", "0")}); ok {
		t.Error("all-zero history has no first non-zero snapshot")
	}
}

// The digest shows the report pages' figures: change split into deposits and
// market, dividends net with gross and withheld.
func TestDigestLines(t *testing.T) {
	lines := strings.Join(digestLines(handler.ReportData{
		HasNetWorth: true, NetWorthStart: 157060, NetWorthEnd: 157246, NetWorthChange: 186, NetWorthChangePct: 0.1,
		NetDeposits: 500, MarketChange: -314,
		TotalDividends: 73.62, DividendsGross: 100, DividendsWithheld: 26.38,
	}), "\n")
	for _, want := range []string{"Net Worth: 157.246 EUR", "Monthly Change: +186 EUR (+0.1%)", "Deposits: +500 EUR · Market: -314 EUR", "Dividend Income: 74 EUR net (100 EUR gross, 26 EUR withheld)"} {
		if !strings.Contains(lines, want) {
			t.Errorf("digest is missing %q:\n%s", want, lines)
		}
	}
	empty := strings.Join(digestLines(handler.ReportData{}), "\n")
	if !strings.Contains(empty, "No net worth snapshot") || strings.Contains(empty, "Net Worth: 0") {
		t.Errorf("a period without snapshots must not report a net worth of 0:\n%s", empty)
	}
}
