package handler

import (
	"encoding/json"
	"math"
	"testing"
	"time"

	"github.com/lnsp/wealth/data"
	db "github.com/lnsp/wealth/internal/database/generated"
)

// Audit W1: fee drag was linear (10,000 € at 0.20 % for 30 years = 600 €).
// The cost includes the growth the fees would have earned.
func TestFeeDragFV_IncludesLostGrowth(t *testing.T) {
	got := feeDragFV(10000, 0.20, 5, 30)
	if math.Abs(got-2519.6) > 1 {
		t.Errorf("fee drag = %.2f, want ≈ 2,519.6 (10,000·1.05^30 − 10,000·(1.05·0.998)^30)", got)
	}
	if got := feeDragFV(10000, 0.20, 0, 1); math.Abs(got-20) > 1e-9 {
		t.Errorf("one year without growth = %.4f, want 20", got)
	}
	if feeDragFV(0, 0.2, 5, 10) != 0 || feeDragFV(1000, 0, 5, 10) != 0 {
		t.Error("no value or no charge must cost nothing")
	}
}

// Audit C6: the grade and saving used the TER diluted by stocks and funds
// without a charge. GOOG (11,744 €, no TER) plus a 5,000 € ETF at 0.22 %
// showed "A+ … 0.07 %, saving 53 €"; over the funds it is B+ and 8 €.
func TestCostBenchmark_UsesFundsWithKnownCharge(t *testing.T) {
	googValue, etfValue, etfTER := 11744.13, 5000.0, 0.22
	annualCost := etfValue * etfTER / 100
	covered := annualCost / etfValue * 100               // 0.22
	diluted := annualCost / (googValue + etfValue) * 100 // ≈ 0.066
	if math.Abs(covered-0.22) > 1e-9 || diluted > 0.07 {
		t.Fatalf("setup: covered %.4f diluted %.4f", covered, diluted)
	}

	b := costBenchmark(covered, etfValue)
	if b["grade"] != "B+" {
		t.Errorf("grade = %v, want B+", b["grade"])
	}
	if b["your_ter"] != 0.22 {
		t.Errorf("your_ter = %v, want 0.22", b["your_ter"])
	}
	if saving := b["annual_saving"].(float64); math.Abs(saving-8) > 0.005 {
		t.Errorf("annual saving = %.2f, want 8.00 ((0.38 − 0.22) %% × 5,000)", saving)
	}
	// 5,000·1.05^10 · (0.9978^10 − 0.9962^10) ≈ 126.8 €, not 10 × 8 €.
	wantTen := 5000 * math.Pow(1.05, 10) * (math.Pow(0.9978, 10) - math.Pow(0.9962, 10))
	if ten := b["ten_year_saving"].(float64); math.Abs(ten-wantTen) > 0.01 {
		t.Errorf("ten-year saving = %.2f, want %.2f (compounded at 5 %%)", ten, wantTen)
	}
	if g, _ := costGrade(diluted); g != "A+" {
		t.Fatalf("setup: the diluted TER grades %s", g)
	}
}

// Audit W11: custody fee rows were left out and the per-year divisor was the
// span between the first and last transaction of any type.
func TestTradingCosts_FeeRowsAndSpanToToday(t *testing.T) {
	now := day("2026-11-18")
	txns := []tradingCostInput{
		{Date: day("2024-11-18"), Type: "buy", AccountType: "brokerage", AmountEUR: 1000, FeeEUR: 1},
		{Date: day("2025-06-01"), Type: "fee", AccountType: "brokerage", AmountEUR: -20},        // custody
		{Date: day("2025-07-01"), Type: "fee", AccountType: "checking", AmountEUR: -5},          // bank fee, not investing
		{Date: day("2025-01-01"), Type: "deposit", AccountType: "brokerage", AmountEUR: 500},    // not a cost
		{Date: day("2023-01-01"), Type: "withdrawal", AccountType: "checking", AmountEUR: -100}, // earlier, other type
	}
	volume, fees, trades, years, ok := tradingCosts(txns, now)
	if !ok || trades != 1 || volume != 1000 {
		t.Fatalf("ok %v trades %d volume %.2f, want 1 trade on 1,000", ok, trades, volume)
	}
	if math.Abs(fees-21) > 1e-9 {
		t.Errorf("fees = %.2f, want 21 (trade fee + custody, not the bank fee)", fees)
	}
	if math.Abs(years-2) > 0.01 {
		t.Errorf("years = %.3f, want 2 (first trade to today)", years)
	}
	_, _, _, years, _ = tradingCosts([]tradingCostInput{
		{Date: day("2026-09-01"), Type: "buy", AccountType: "brokerage", AmountEUR: 1000, FeeEUR: 1},
	}, now)
	if years != 1 {
		t.Errorf("a recent first trade averages over %.2f years, want at least 1", years)
	}
	if _, _, _, _, ok := tradingCosts(nil, now); ok {
		t.Error("no trades must report no trading history (TCO empty state)")
	}
}

// Audit W12: alternatives ignored the tax on switching.
func TestSwitchOutcome_AccountsForSwitchTax(t *testing.T) {
	// 50,000 € with a 20,000 € gain: selling costs 20,000 × 0.7 × 26.375 %
	// = 3,692.50 € now. A 0.15-point cheaper fund takes decades to make
	// that back at 5 % growth.
	if tax := switchTaxCost(20000, 0.30, 0); math.Abs(tax-3692.5) > 0.01 {
		t.Fatalf("switch tax = %.2f, want 3,692.50", tax)
	}
	net, breakeven := switchOutcome(50000, 30000, 0.22, 0.07, 0.30)
	if net >= 0 || (breakeven != 0 && breakeven < 30) {
		t.Errorf("small TER gap: 10-year net %.2f, break-even %d; want negative and 30+ years", net, breakeven)
	}
	// From a 1.80 % fund to 0.20 % it pays off within a few years.
	net, breakeven = switchOutcome(50000, 30000, 1.80, 0.20, 0.30)
	if net <= 0 || breakeven < 1 || breakeven > 10 {
		t.Errorf("large TER gap: 10-year net %.2f, break-even %d; want positive within 10 years", net, breakeven)
	}
	// Without a gain there is no switch tax: the cheaper fund is ahead at once.
	if _, breakeven = switchOutcome(50000, 50000, 0.22, 0.07, 0.30); breakeven != 1 {
		t.Errorf("no gain: break-even %d, want 1", breakeven)
	}
}

func TestETFAlternativesData(t *testing.T) {
	var alts map[string]struct {
		Name         string  `json:"name"`
		Index        string  `json:"index"`
		TER          float64 `json:"ter"`
		Alternatives []struct {
			ISIN  string  `json:"isin"`
			Name  string  `json:"name"`
			TER   float64 `json:"ter"`
			Index string  `json:"index"`
		} `json:"alternatives"`
	}
	if err := json.Unmarshal(data.ETFAlternativesJSON, &alts); err != nil {
		t.Fatal(err)
	}
	for isin, base := range alts {
		if !validISIN(isin) {
			t.Errorf("base %s: invalid ISIN check digit", isin)
		}
		if base.Index == "" || base.TER <= 0 {
			t.Errorf("base %s: index %q, TER %.2f", isin, base.Index, base.TER)
		}
		for _, a := range base.Alternatives {
			if !validISIN(a.ISIN) {
				t.Errorf("%s → %s: invalid ISIN check digit", isin, a.ISIN)
			}
			if a.ISIN == isin || a.TER >= base.TER {
				t.Errorf("%s → %s (%.2f %%) is not a cheaper alternative to %.2f %%", isin, a.ISIN, a.TER, base.TER)
			}
			if a.Index == "" {
				t.Errorf("%s → %s: index missing", isin, a.ISIN)
			}
		}
	}
	// Audit W12: IE00BJ0KDQ92 is Xtrackers MSCI World 1C, not an ACWI fund.
	for _, base := range alts {
		for _, a := range base.Alternatives {
			if a.ISIN == "IE00BJ0KDQ92" && a.Index != "MSCI World" {
				t.Errorf("IE00BJ0KDQ92 listed as tracking %q", a.Index)
			}
		}
	}
}

// validISIN checks the ISIN check digit (Luhn over the letter-expanded code).
func validISIN(isin string) bool {
	if len(isin) != 12 {
		return false
	}
	digits := ""
	for _, r := range isin[:11] {
		switch {
		case r >= '0' && r <= '9':
			digits += string(r)
		case r >= 'A' && r <= 'Z':
			digits += itoa(int(r-'A') + 10)
		default:
			return false
		}
	}
	sum := 0
	double := true
	for i := len(digits) - 1; i >= 0; i-- {
		d := int(digits[i] - '0')
		if double {
			d *= 2
			if d > 9 {
				d -= 9
			}
		}
		sum += d
		double = !double
	}
	return int(isin[11]-'0') == (10-sum%10)%10
}

func itoa(n int) string {
	if n < 10 {
		return string(rune('0' + n))
	}
	return string(rune('0'+n/10)) + string(rune('0'+n%10))
}

// Audit W5: returns were computed per security and then matched by date, so
// a gap in one series paired a multi-day return with a one-day return.
func TestAlignedReturnCorrelation_AlignsIntervals(t *testing.T) {
	start := day("2026-01-01")
	var a, b []eurPricePoint
	price := 100.0
	for i := 0; i < 60; i++ {
		// Deterministic zig-zag with drift.
		price *= 1 + 0.01*float64((i*7)%5-2)
		d := start.AddDate(0, 0, i)
		a = append(a, eurPricePoint{Date: d, EUR: price})
		if i%9 != 4 { // b is the same series with gaps
			b = append(b, eurPricePoint{Date: d, EUR: price * 2})
		}
	}
	corr, n, ok := alignedReturnCorrelation(a, b, correlationMinObservations)
	if !ok || math.Abs(corr-1) > 1e-9 {
		t.Errorf("identical prices with gaps: corr %.4f (n %d, ok %v), want 1", corr, n, ok)
	}
	if _, _, ok := alignedReturnCorrelation(a[:15], b[:12], correlationMinObservations); ok {
		t.Error("fewer than the minimum shared returns must report no correlation, not 0")
	}
}

// Audit W10: history weighted by cost basis, the last point by market value
// and mislabelled, months without trades skipped, no Unknown series.
func TestBuildSectorHistory_MarketValueEveryMonth(t *testing.T) {
	txns := []sectorHistoryTxn{
		{Date: day("2026-01-10"), Type: "buy", ISIN: "ETF", Qty: 10, CostEUR: 1000},
		{Date: day("2026-03-05"), Type: "buy", ISIN: "STOCK", Qty: 10, CostEUR: 1000},
	}
	prices := map[string][]eurPricePoint{
		"ETF":   {{Date: day("2026-01-01"), EUR: 100}, {Date: day("2026-03-01"), EUR: 300}},
		"STOCK": {{Date: day("2026-01-01"), EUR: 100}},
	}
	priceAt := func(isin string, at time.Time) (float64, bool) {
		var p float64
		found := false
		for _, pt := range prices[isin] {
			if !pt.Date.After(at) {
				p, found = pt.EUR, true
			}
		}
		return p, found
	}
	weights := map[string]map[string]float64{"ETF": {"Technology": 100}}

	got := buildSectorHistory(txns, day("2026-04-15"), priceAt, weights)
	wantDates := []string{"2026-01-31", "2026-02-28", "2026-03-31", "2026-04-15"}
	if len(got) != len(wantDates) {
		t.Fatalf("points = %+v, want %v", got, wantDates)
	}
	for i, d := range wantDates {
		if got[i].Date != d {
			t.Errorf("point %d dated %s, want %s", i, got[i].Date, d)
		}
	}
	// End of March: ETF 10 × 300 = 3,000, stock 1,000 → 75 % / 25 %, not
	// the 50 / 50 that cost basis gives.
	march := got[2].Sectors
	if math.Abs(march["Technology"]-75) > 1e-9 || math.Abs(march["Unknown"]-25) > 1e-9 {
		t.Errorf("March = %v, want Technology 75, Unknown 25", march)
	}
	if feb := got[1].Sectors; math.Abs(feb["Technology"]-100) > 1e-9 {
		t.Errorf("February (no trades) = %v, want Technology 100", feb)
	}
}

func TestStockCountryFromISIN(t *testing.T) {
	cases := map[[2]string]string{
		{"US02079K1079", "Alphabet Inc. Class C"}: "United States",
		{"DE0007030009", "Rheinmetall AG"}:        "Germany",
		{"US0420682058", "ARM HLDGS ADR DL-,001"}: "", // ADR: the ISIN says nothing about ARM
		{"KYG017191142", "Alibaba Group Holding"}: "", // offshore domicile
		{"ASME.DE", "ASML Holding NV"}:            "", // not an ISIN
		{"US0000000000", "Broadstone Net Lease"}:  "United States",
	}
	for in, want := range cases {
		if got := stockCountryFromISIN(in[0], in[1]); got != want {
			t.Errorf("stockCountryFromISIN(%q, %q) = %q, want %q", in[0], in[1], got, want)
		}
	}
}

// Audit W7 / I3: the same fund in two accounts was compared with itself
// (100 % overlap) and listed twice in Costs.
func TestAggregateByISIN_MergesAccounts(t *testing.T) {
	sec := db.Security{ISIN: "IE00BK5BQT80", Name: "VWCE", AssetClass: "etf"}
	other := db.Security{ISIN: "IE00B4L5Y983", Name: "EUNL", AssetClass: "etf"}
	got := aggregateByISIN([]enrichedHolding{
		{security: sec, value: 6000, costValue: 5000},
		{security: other, value: 1000, costValue: 900},
		{security: sec, value: 4000, costValue: 3000},
	})
	if len(got) != 2 || got[0].security.ISIN != sec.ISIN || got[0].value != 10000 || got[0].costValue != 8000 {
		t.Errorf("aggregated = %+v, want VWCE once at 10,000 / 8,000", got)
	}
}
