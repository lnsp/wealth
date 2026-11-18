package handler

import (
	"math"
	"testing"
	"time"

	"github.com/lnsp/wealth/internal/analytics"
	db "github.com/lnsp/wealth/internal/database/generated"
)

// These tests run the helpers HandleTaxCalendar builds its events from.

func isoDay(s string) time.Time {
	t, _ := time.Parse("2006-01-02", s)
	return t
}

// I2: § 149 AO deadlines with the Art. 97 § 36 EGAO transition, rolled past
// weekends per § 108 Abs. 3 AO.
func TestIncomeTaxReturnDeadlines(t *testing.T) {
	cases := []struct {
		year               int
		unadvised, advised string
	}{
		{2023, "2024-09-02", "2025-06-02"}, // 31.08.2024 Sat; 31.05.2025 Sat
		{2024, "2025-07-31", "2026-04-30"},
		{2025, "2026-07-31", "2027-03-01"}, // 28.02.2027 is a Sunday
		{2026, "2027-08-02", "2028-02-29"}, // 31.07.2027 is a Saturday; 2028 is a leap year
	}
	for _, c := range cases {
		u, a := incomeTaxReturnDeadlines(c.year)
		if !u.Equal(isoDay(c.unadvised)) || !a.Equal(isoDay(c.advised)) {
			t.Errorf("%d: got %s / %s, want %s / %s", c.year, u.Format("2006-01-02"), a.Format("2006-01-02"), c.unadvised, c.advised)
		}
	}
}

// The Vorabpauschale is debited on the first working day of the year.
func TestFirstWorkingDayOfYear(t *testing.T) {
	for year, want := range map[int]string{2026: "2026-01-02", 2027: "2027-01-04", 2028: "2028-01-03"} {
		if got := firstWorkingDayOfYear(year); !got.Equal(isoDay(want)) {
			t.Errorf("%d: got %s, want %s", year, got.Format("2006-01-02"), want)
		}
	}
}

// harvestFor builds the lots from the ledger with the engine, as the calendar
// does, and runs harvestAnalysis at today's prices.
func harvestFor(t *testing.T, txns []analytics.TaxTransaction, prices map[string]float64, excluded map[string]bool) harvestResult {
	t.Helper()
	lots := analytics.ComputeTaxLots(txns, prices, nil, nil)
	return harvestAnalysis(txns, lots, excluded, 2026, analytics.TaxOptions{}, isoDay("2026-11-18"))
}

// W2: a sell-all realises every FIFO lot of the depot, winners included. Two
// GOOG vests at 100 and 180 with the price at 130 are −200 net, not the −500
// of the losing lot alone; with no share gains this year selling saves
// nothing now and the loss carries forward in the Aktienverlusttopf.
func TestHarvestAnalysis_SellAllPerDepotAndNoGainsToOffset(t *testing.T) {
	ms := testAccount("MS", "morgan_stanley")
	txns := []analytics.TaxTransaction{
		ttx(ms, "2026-01-10", "transfer", goog, "stock", 10, 1000, 0),
		ttx(ms, "2026-02-10", "transfer", goog, "stock", 10, 1800, 0),
	}
	res := harvestFor(t, txns, map[string]float64{goog: 130}, nil)
	if res.Positions != 1 || !approx(res.GrossLoss, 200) || !approx(res.ShareLoss, 200) {
		t.Errorf("loss = %.2f (share %.2f) over %d positions, want 200 over 1", res.GrossLoss, res.ShareLoss, res.Positions)
	}
	if res.Saving != 0 {
		t.Errorf("saving = %.2f, want 0: there are no share gains this year", res.Saving)
	}
	if !approx(res.CarryForward, 200) {
		t.Errorf("carry forward = %.2f, want 200", res.CarryForward)
	}
}

func TestHarvestAnalysis_SavingLimitedToGainsInSamePot(t *testing.T) {
	ing := testAccount("ING", "ing")
	rate := analytics.EffectiveTaxRate
	// This year: 2,000 share gains → 1,000 taxable after the allowance.
	gains := []analytics.TaxTransaction{
		ttx(ing, "2025-01-10", "buy", "DE0007164600", "stock", 10, 1000, 0),
		ttx(ing, "2026-03-01", "sell", "DE0007164600", "stock", 10, 3000, 0),
	}
	with := func(extra ...analytics.TaxTransaction) []analytics.TaxTransaction {
		return append(append([]analytics.TaxTransaction{}, gains...), extra...)
	}

	// A 500 share loss saves 500 × rate.
	res := harvestFor(t, with(ttx(ing, "2026-04-01", "buy", goog, "stock", 10, 1000, 0)), map[string]float64{goog: 50}, nil)
	if !approx(res.Saving, 500*rate) {
		t.Errorf("share loss saving = %.2f, want %.2f", res.Saving, 500*rate)
	}
	// A 3,000 share loss can only remove the 1,000 above the allowance; the rest carries forward.
	res = harvestFor(t, with(ttx(ing, "2026-04-01", "buy", goog, "stock", 10, 4000, 0)), map[string]float64{goog: 100}, nil)
	if !approx(res.Saving, 1000*rate) || !approx(res.CarryForward, 1000) {
		t.Errorf("large share loss: saving %.2f carry %.2f, want %.2f and 1,000", res.Saving, res.CarryForward, 1000*rate)
	}
	// An equity-ETF loss is a general loss after Teilfreistellung; it offsets share gains too.
	res = harvestFor(t, with(ttx(ing, "2026-04-01", "buy", "ETF", "etf", 10, 2000, 0.30)), map[string]float64{"ETF": 100}, nil)
	if !approx(res.GeneralLoss, 700) || !approx(res.Saving, 700*rate) {
		t.Errorf("ETF loss: general %.2f saving %.2f, want 700 and %.2f", res.GeneralLoss, res.Saving, 700*rate)
	}
	// A share loss cannot offset dividends.
	divs := []analytics.TaxTransaction{
		ttx(ing, "2026-02-01", "dividend", "DE0007164600", "stock", 0, 3000, 0),
		ttx(ing, "2026-04-01", "buy", goog, "stock", 10, 1000, 0),
	}
	res = harvestFor(t, divs, map[string]float64{goog: 50}, nil)
	if res.Saving != 0 {
		t.Errorf("share loss vs dividends: saving %.2f, want 0", res.Saving)
	}
}

func TestHarvestAnalysis_SkipsPrivateSalesAndPensionWrappers(t *testing.T) {
	d := testAccount("Delta", "delta")
	bav := testAccount("bAV", "other")
	txns := []analytics.TaxTransaction{
		ttx(d, "2026-01-10", "buy", "XX_BTC", "crypto", 1, 4000, 0),
		ttx(bav, "2026-01-10", "buy", "ETF", "etf", 10, 4000, 0.30),
	}
	res := harvestFor(t, txns, map[string]float64{"XX_BTC": 2000, "ETF": 200}, map[string]bool{bav.ID.String(): true})
	if res.Positions != 0 || res.GrossLoss != 0 {
		t.Errorf("crypto (§ 23) and pension wrappers must be excluded, got %+v", res)
	}
}

// W4: realised gains count towards the allowance. 3,000 € of gains by July
// means the allowance is used, whatever the dividends look like.
func TestProjectedAllowanceUse_IncludesRealisedGains(t *testing.T) {
	ing := testAccount("ING", "ing")
	txns := []analytics.TaxTransaction{
		ttx(ing, "2026-01-10", "buy", goog, "stock", 10, 1000, 0),
		ttx(ing, "2026-03-01", "sell", goog, "stock", 10, 4000, 0),
		ttx(ing, "2026-04-01", "dividend", "DE0007164600", "stock", 0, 70, 0),
	}
	banks := aggregateCapitalIncome(capitalIncomeInput{Year: 2026, Ledger: txns, Accounts: []db.Account{ing}})
	got := projectedAllowanceUse(banks, 7)
	if want := 3000 + 70*12/7.0; math.Abs(got-want) > 0.01 {
		t.Errorf("projected = %.2f, want %.2f (gains as realised, dividends annualised)", got, want)
	}
}
