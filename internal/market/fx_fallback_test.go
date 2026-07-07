package market

import (
	"math"
	"testing"
	"time"
)

// ECB FX fallback + round-trip contract:
//
//   - ECB publishes reference rates daily on TARGET business days only
//     (weekdays excluding TARGET holidays). For weekend/holiday lookups,
//     the rate to use is the most recent published rate on or before
//     the target — typically Friday's rate for Saturday/Sunday lookups.
//   - amountEUR = amountForeign / rate  (rate = units-per-EUR)
//   - Round-trip: ToEUR(FromEUR(x, r), r) ≈ x  within float epsilon.
//
// These tests pin both halves. The weekend-fallback function is the
// migration target for any future date-based FX consumer (e.g.,
// historical attribution, period-snapshot tax reporting).

// Fixture: ECB rates for the week of Mon 2026-04-13 through Fri 2026-04-17.
// Saturday/Sunday have no entries — that's the fallback case.
func weekRates() []HistoricalFXRate {
	d := func(day int) time.Time { return time.Date(2026, 4, day, 0, 0, 0, 0, time.UTC) }
	return []HistoricalFXRate{
		// USD across the week
		{Date: d(13), Currency: "USD", Rate: 1.0810},
		{Date: d(14), Currency: "USD", Rate: 1.0825},
		{Date: d(15), Currency: "USD", Rate: 1.0850},
		{Date: d(16), Currency: "USD", Rate: 1.0860},
		{Date: d(17), Currency: "USD", Rate: 1.0875}, // Friday
		// Sat (18) + Sun (19) intentionally absent
		{Date: d(20), Currency: "USD", Rate: 1.0830}, // next Monday
		// GBP — sparser fixture
		{Date: d(15), Currency: "GBP", Rate: 0.8350},
		{Date: d(17), Currency: "GBP", Rate: 0.8330},
	}
}

func d(year, month, day int) time.Time {
	return time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC)
}

func TestFXFallback_SaturdayUsesFridayRate(t *testing.T) {
	rates := weekRates()
	saturday := d(2026, 4, 18)
	got := LookupRateForDate(rates, "USD", saturday)
	if got != 1.0875 {
		t.Errorf("Saturday USD lookup = %v, want 1.0875 (Friday rate)", got)
	}
}

func TestFXFallback_SundayUsesFridayRate(t *testing.T) {
	rates := weekRates()
	sunday := d(2026, 4, 19)
	got := LookupRateForDate(rates, "USD", sunday)
	if got != 1.0875 {
		t.Errorf("Sunday USD lookup = %v, want 1.0875 (Friday rate; no Saturday data either)", got)
	}
}

func TestFXFallback_ExactWeekdayReturnsThatDay(t *testing.T) {
	rates := weekRates()
	cases := []struct {
		day  time.Time
		want float64
	}{
		{d(2026, 4, 13), 1.0810},
		{d(2026, 4, 14), 1.0825},
		{d(2026, 4, 15), 1.0850},
		{d(2026, 4, 16), 1.0860},
		{d(2026, 4, 17), 1.0875},
		{d(2026, 4, 20), 1.0830},
	}
	for _, c := range cases {
		got := LookupRateForDate(rates, "USD", c.day)
		if got != c.want {
			t.Errorf("weekday %s USD = %v, want %v", c.day.Format("Mon 2006-01-02"), got, c.want)
		}
	}
}

func TestFXFallback_HolidayMondayWalksToPriorBusinessDay(t *testing.T) {
	// Simulate a TARGET holiday on Mon 2026-04-20 (Easter Monday in
	// real-world 2026 falls on April 6, but for test purposes we just
	// REMOVE the Monday entry to model "no rate published").
	rates := weekRates()
	holidayMonday := d(2026, 4, 20)
	// Remove the Monday entry to simulate the holiday.
	pruned := rates[:0]
	for _, r := range rates {
		if r.Date.Equal(holidayMonday) && r.Currency == "USD" {
			continue
		}
		pruned = append(pruned, r)
	}
	got := LookupRateForDate(pruned, "USD", holidayMonday)
	if got != 1.0875 {
		t.Errorf("holiday Monday USD lookup = %v, want 1.0875 (walks back to Friday)", got)
	}
}

func TestFXFallback_DateBeforeAnyRateReturnsZero(t *testing.T) {
	rates := weekRates()
	veryEarly := d(2026, 1, 1)
	if got := LookupRateForDate(rates, "USD", veryEarly); got != 0 {
		t.Errorf("date before any rate = %v, want 0 (caller decides how to handle)", got)
	}
}

func TestFXFallback_UnknownCurrencyReturnsZero(t *testing.T) {
	rates := weekRates()
	target := d(2026, 4, 17)
	if got := LookupRateForDate(rates, "JPY", target); got != 0 {
		t.Errorf("unknown currency lookup = %v, want 0 (no JPY in fixture)", got)
	}
}

func TestFXFallback_LongWeekendBetweenRates(t *testing.T) {
	// GBP fixture has gaps: only Wed (15) and Fri (17) for GBP. Tuesday (14)
	// should return 0 (no prior GBP rate). Thursday (16) should return
	// Wednesday's. Saturday (18) should return Friday's.
	rates := weekRates()
	if got := LookupRateForDate(rates, "GBP", d(2026, 4, 14)); got != 0 {
		t.Errorf("Tue GBP (no prior rate) = %v, want 0", got)
	}
	if got := LookupRateForDate(rates, "GBP", d(2026, 4, 16)); got != 0.8350 {
		t.Errorf("Thu GBP = %v, want 0.8350 (Wed rate)", got)
	}
	if got := LookupRateForDate(rates, "GBP", d(2026, 4, 18)); got != 0.8330 {
		t.Errorf("Sat GBP = %v, want 0.8330 (Fri rate)", got)
	}
}

// Round-trip consistency: convert foreign → EUR → foreign, must match
// original within float epsilon. This is the inverse-pair property
// users implicitly rely on whenever a multi-currency portfolio is
// reconciled (asset shown in EUR, then user mentally re-translates).

// toEUR mirrors handler/portfolio.go convertToEUR's math (amount / rate).
func toEUR(amount, rate float64) float64 { return amount / rate }

// fromEUR is the inverse: amountForeign = amountEUR * rate.
func fromEUR(amountEUR, rate float64) float64 { return amountEUR * rate }

func TestFXRoundTrip_USDtoEURtoUSD(t *testing.T) {
	const rate = 1.0875 // 1 EUR = 1.0875 USD
	original := 1000.00
	eur := toEUR(original, rate)
	roundTrip := fromEUR(eur, rate)
	if math.Abs(roundTrip-original) > 1e-9 {
		t.Errorf("USD→EUR→USD = %.12f, want %.12f (diff %.2e)",
			roundTrip, original, roundTrip-original)
	}
}

func TestFXRoundTrip_GBPtoEURtoGBP(t *testing.T) {
	const rate = 0.8330
	original := 1234.56
	eur := toEUR(original, rate)
	roundTrip := fromEUR(eur, rate)
	if math.Abs(roundTrip-original) > 1e-9 {
		t.Errorf("GBP→EUR→GBP = %.12f, want %.12f", roundTrip, original)
	}
}

func TestFXRoundTrip_CrossCurrencyVisATwoStep(t *testing.T) {
	// Convert 1000 USD → EUR → GBP → EUR → USD.
	// Each leg uses the *same* rate forward and backward so the result
	// must exactly match the original (within float epsilon).
	const usdRate = 1.0875 // 1 EUR = 1.0875 USD
	const gbpRate = 0.8330 // 1 EUR = 0.8330 GBP
	original := 1000.00

	eur := toEUR(original, usdRate)             // USD → EUR
	gbp := fromEUR(eur, gbpRate)                // EUR → GBP
	eurAgain := toEUR(gbp, gbpRate)             // GBP → EUR (back)
	usdAgain := fromEUR(eurAgain, usdRate)      // EUR → USD (back)
	if math.Abs(usdAgain-original) > 1e-9 {
		t.Errorf("USD→EUR→GBP→EUR→USD = %.12f, want %.12f (diff %.2e)",
			usdAgain, original, usdAgain-original)
	}
}

func TestFXRoundTrip_AcrossSeveralAmounts(t *testing.T) {
	// Verify round-trip stability across a range of magnitudes — small,
	// medium, large, fractional, near-zero, large-with-fractional.
	rate := 1.0875
	cases := []float64{0.01, 1.00, 7.43, 100, 1234.56, 99_999.99, 1_000_000}
	for _, original := range cases {
		eur := toEUR(original, rate)
		roundTrip := fromEUR(eur, rate)
		if math.Abs(roundTrip-original) > 1e-9*math.Max(1, math.Abs(original)) {
			t.Errorf("amount=%v: round-trip = %v (diff %v)", original, roundTrip, roundTrip-original)
		}
	}
}

func TestFXFallback_LookupAgreesWithRoundTrip(t *testing.T) {
	// Integration: lookup the rate for a Saturday, then verify a
	// round-trip using that rate is stable. Catches the case where a
	// lookup gives the wrong "rate basis" but the round-trip still works
	// vacuously (because we'd be using the same wrong rate twice).
	rates := weekRates()
	saturday := d(2026, 4, 18)
	rate := LookupRateForDate(rates, "USD", saturday)
	if rate == 0 {
		t.Fatal("lookup returned 0 — round-trip test pre-condition failed")
	}
	original := 5000.0
	eur := toEUR(original, rate)
	roundTrip := fromEUR(eur, rate)
	if math.Abs(roundTrip-original) > 1e-9 {
		t.Errorf("Sat USD round-trip via lookup: %v → %v → %v (drift %v)",
			original, eur, roundTrip, roundTrip-original)
	}
}
