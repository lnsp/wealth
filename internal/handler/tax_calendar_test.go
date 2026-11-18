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

func lotsFor(acct db.Account, isin string, pls ...float64) []analytics.TaxLot {
	var out []analytics.TaxLot
	for _, pl := range pls {
		out = append(out, analytics.TaxLot{AccountID: acct.ID.String(), ISIN: isin, UnrealizedPL: pl})
	}
	return out
}

// W2: a sell-all realises every FIFO lot of the depot, winners included. On
// the seed GOOG is at −864.19 net (not the −903.47 of the losing lots alone),
// and with no share gains this year selling saves nothing now.
func TestHarvestAnalysis_SellAllPerDepotAndNoGainsToOffset(t *testing.T) {
	ms := testAccount("MS", "morgan_stanley")
	lots := lotsFor(ms, goog, 39.28, -500, -403.47)
	res := harvestAnalysis(lots, map[string]string{goog: "stock"}, nil, nil, 1000, analytics.EffectiveTaxRate)
	if res.Positions != 1 || !approx(res.GrossLoss, 864.19) {
		t.Errorf("gross loss = %.2f over %d positions, want 864.19 over 1", res.GrossLoss, res.Positions)
	}
	if res.Saving != 0 {
		t.Errorf("saving = %.2f, want 0: there are no share gains this year", res.Saving)
	}
	if !approx(res.CarryForward, 864.19) {
		t.Errorf("carry forward = %.2f, want 864.19 in the Aktienverlusttopf", res.CarryForward)
	}
}

func TestHarvestAnalysis_SavingLimitedToGainsInSamePot(t *testing.T) {
	ing := testAccount("ING", "ing")
	classes := map[string]string{goog: "stock", "ETF": "etf"}
	// This year: 2,000 share gains at ING → 1,000 taxable after the allowance.
	ytd := []*bankIncome{{taxBank: accountTaxBank(ing), ShareGains: 2000}}

	// A 500 share loss saves 500 × rate.
	res := harvestAnalysis(lotsFor(ing, goog, -500), classes, nil, ytd, 1000, analytics.EffectiveTaxRate)
	if !approx(res.Saving, 500*analytics.EffectiveTaxRate) {
		t.Errorf("share loss saving = %.2f, want %.2f", res.Saving, 500*analytics.EffectiveTaxRate)
	}
	// A 3,000 share loss can only remove the 1,000 above the allowance.
	res = harvestAnalysis(lotsFor(ing, goog, -3000), classes, nil, ytd, 1000, analytics.EffectiveTaxRate)
	if !approx(res.Saving, 1000*analytics.EffectiveTaxRate) || !approx(res.CarryForward, 1000) {
		t.Errorf("large share loss: saving %.2f carry %.2f, want %.2f and 1,000", res.Saving, res.CarryForward, 1000*analytics.EffectiveTaxRate)
	}
	// An equity-ETF loss is a general loss after Teilfreistellung; it offsets share gains too.
	etfLots := []analytics.TaxLot{{AccountID: ing.ID.String(), ISIN: "ETF", UnrealizedPL: -1000, TFSRate: 0.30}}
	res = harvestAnalysis(etfLots, classes, nil, ytd, 1000, analytics.EffectiveTaxRate)
	if !approx(res.GeneralLoss, 700) || !approx(res.Saving, 700*analytics.EffectiveTaxRate) {
		t.Errorf("ETF loss: general %.2f saving %.2f, want 700 and %.2f", res.GeneralLoss, res.Saving, 700*analytics.EffectiveTaxRate)
	}
	// A share loss cannot offset dividends.
	divs := []*bankIncome{{taxBank: accountTaxBank(ing), Dividends: 3000}}
	res = harvestAnalysis(lotsFor(ing, goog, -500), classes, nil, divs, 1000, analytics.EffectiveTaxRate)
	if res.Saving != 0 {
		t.Errorf("share loss vs dividends: saving %.2f, want 0", res.Saving)
	}
}

func TestHarvestAnalysis_SkipsPrivateSalesAndPensionWrappers(t *testing.T) {
	d := testAccount("Delta", "delta")
	bav := testAccount("bAV", "other")
	lots := append(lotsFor(d, "XX_BTC", -2000), lotsFor(bav, "ETF", -2000)...)
	res := harvestAnalysis(lots, map[string]string{"XX_BTC": "crypto", "ETF": "etf"}, map[string]bool{bav.ID.String(): true}, nil, 1000, analytics.EffectiveTaxRate)
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
	banks := aggregateCapitalIncome(capitalIncomeInput{Year: 2026, Events: realiseCapitalEvents(txns), Accounts: []db.Account{ing}})
	got := projectedAllowanceUse(banks, 7)
	if want := 3000 + 70*12/7.0; math.Abs(got-want) > 0.01 {
		t.Errorf("projected = %.2f, want %.2f (gains as realised, dividends annualised)", got, want)
	}
}
