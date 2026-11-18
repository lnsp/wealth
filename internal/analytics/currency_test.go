package analytics

import (
	"math"
	"testing"
)

func TestComputeCurrencyExposure_Empty(t *testing.T) {
	result := ComputeCurrencyExposure(nil)
	if result != nil {
		t.Errorf("expected nil for empty input, got %v", result)
	}
}

func TestComputeCurrencyExposure_ZeroValue(t *testing.T) {
	result := ComputeCurrencyExposure([]HoldingForCurrency{
		{ISIN: "TEST", MarketValue: 0},
	})
	if result != nil {
		t.Errorf("expected nil for zero-value holdings, got %v", result)
	}
}

func TestComputeCurrencyExposure_SingleHoldingNoCW(t *testing.T) {
	result := ComputeCurrencyExposure([]HoldingForCurrency{
		{ISIN: "TEST", Currency: "USD", MarketValue: 10000},
	})
	if len(result) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(result))
	}
	if result[0].Currency != "USD" {
		t.Errorf("expected USD, got %s", result[0].Currency)
	}
	if math.Abs(result[0].Pct-100) > 0.01 {
		t.Errorf("expected 100%%, got %f%%", result[0].Pct)
	}
}

func TestComputeCurrencyExposure_DefaultsToEUR(t *testing.T) {
	result := ComputeCurrencyExposure([]HoldingForCurrency{
		{ISIN: "TEST", Currency: "", MarketValue: 5000},
	})
	if len(result) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(result))
	}
	if result[0].Currency != "EUR" {
		t.Errorf("expected EUR for empty currency, got %s", result[0].Currency)
	}
}

func TestComputeCurrencyExposure_WithCountryWeights(t *testing.T) {
	result := ComputeCurrencyExposure([]HoldingForCurrency{
		{
			ISIN:        "VWCE",
			Currency:    "EUR",
			MarketValue: 100000,
			CountryWeights: map[string]float64{
				"United States":  60,
				"Japan":          10,
				"United Kingdom": 5,
				"Germany":        5,
				"France":         5,
				"Other":          15,
			},
		},
	})

	// Build a map for easier lookup
	byCode := make(map[string]CurrencyExposureEntry)
	for _, e := range result {
		byCode[e.Currency] = e
	}

	// USD should be 60%
	if usd, ok := byCode["USD"]; !ok || math.Abs(usd.Pct-60) > 0.1 {
		t.Errorf("expected USD ~60%%, got %+v", byCode["USD"])
	}
	// JPY should be 10%
	if jpy, ok := byCode["JPY"]; !ok || math.Abs(jpy.Pct-10) > 0.1 {
		t.Errorf("expected JPY ~10%%, got %+v", byCode["JPY"])
	}
	// EUR should be ~10% (Germany + France)
	if eur, ok := byCode["EUR"]; !ok || math.Abs(eur.Pct-10) > 0.1 {
		t.Errorf("expected EUR ~10%%, got %+v", byCode["EUR"])
	}
	// GBP should be 5%
	if gbp, ok := byCode["GBP"]; !ok || math.Abs(gbp.Pct-5) > 0.1 {
		t.Errorf("expected GBP ~5%%, got %+v", byCode["GBP"])
	}
	// "Other" country maps to "OTHER" currency
	if other, ok := byCode["OTHER"]; !ok || math.Abs(other.Pct-15) > 0.1 {
		t.Errorf("expected OTHER ~15%%, got %+v", byCode["OTHER"])
	}
}

func TestComputeCurrencyExposure_MultipleHoldings(t *testing.T) {
	result := ComputeCurrencyExposure([]HoldingForCurrency{
		{
			ISIN:        "SXR8",
			Currency:    "EUR",
			MarketValue: 50000,
			CountryWeights: map[string]float64{
				"United States": 100,
			},
		},
		{
			ISIN:        "EUNL",
			Currency:    "EUR",
			MarketValue: 50000,
			CountryWeights: map[string]float64{
				"Germany":     30,
				"France":      25,
				"Netherlands": 15,
				"Switzerland": 10,
				"Sweden":      10,
				"Other":       10,
			},
		},
	})

	byCode := make(map[string]CurrencyExposureEntry)
	for _, e := range result {
		byCode[e.Currency] = e
	}

	// SXR8 is 50% of portfolio, 100% US → 50% USD
	// EUNL is 50% of portfolio, 70% EUR, 10% CHF, 10% SEK, 10% OTHER
	if usd, ok := byCode["USD"]; !ok || math.Abs(usd.Pct-50) > 0.5 {
		t.Errorf("expected USD ~50%%, got %+v", byCode["USD"])
	}
	// EUR = 30+25+15 = 70% of 50% = 35%
	if eur, ok := byCode["EUR"]; !ok || math.Abs(eur.Pct-35) > 0.5 {
		t.Errorf("expected EUR ~35%%, got %+v", byCode["EUR"])
	}
	// CHF = 10% of 50% = 5%
	if chf, ok := byCode["CHF"]; !ok || math.Abs(chf.Pct-5) > 0.5 {
		t.Errorf("expected CHF ~5%%, got %+v", byCode["CHF"])
	}
}

func TestComputeCurrencyExposure_SortedByValueDesc(t *testing.T) {
	result := ComputeCurrencyExposure([]HoldingForCurrency{
		{ISIN: "A", Currency: "GBP", MarketValue: 1000},
		{ISIN: "B", Currency: "USD", MarketValue: 5000},
		{ISIN: "C", Currency: "EUR", MarketValue: 3000},
	})

	if len(result) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(result))
	}
	if result[0].Currency != "USD" {
		t.Errorf("expected first currency to be USD (largest), got %s", result[0].Currency)
	}
	if result[1].Currency != "EUR" {
		t.Errorf("expected second currency to be EUR, got %s", result[1].Currency)
	}
	if result[2].Currency != "GBP" {
		t.Errorf("expected third currency to be GBP (smallest), got %s", result[2].Currency)
	}
}

// Audit W9: partial country weights used to be scaled up to 100 %, so a fund
// with only 50 % of its countries on record showed 60 % USD. The unlisted
// half is now "Unknown".
func TestComputeCurrencyExposure_PartialWeightsLeaveUnknown(t *testing.T) {
	result := ComputeCurrencyExposure([]HoldingForCurrency{
		{
			ISIN:        "TEST",
			Currency:    "EUR",
			MarketValue: 10000,
			IsFund:      true,
			CountryWeights: map[string]float64{
				"United States": 30,
				"Japan":         20,
			},
		},
	})

	byCode := make(map[string]CurrencyExposureEntry)
	for _, e := range result {
		byCode[e.Currency] = e
	}

	if usd := byCode["USD"]; math.Abs(usd.Pct-30) > 0.01 {
		t.Errorf("expected USD 30%%, got %f%%", usd.Pct)
	}
	if jpy := byCode["JPY"]; math.Abs(jpy.Pct-20) > 0.01 {
		t.Errorf("expected JPY 20%%, got %f%%", jpy.Pct)
	}
	if unk := byCode[CurrencyUnknown]; math.Abs(unk.Pct-50) > 0.01 {
		t.Errorf("expected Unknown 50%%, got %f%%", unk.Pct)
	}
	if _, ok := byCode["EUR"]; ok {
		t.Error("a fund's trading currency must not absorb the unlisted weight")
	}
}

func TestComputeCurrencyExposure_RoundingAbove100IsNormalised(t *testing.T) {
	result := ComputeCurrencyExposure([]HoldingForCurrency{
		{ISIN: "TEST", Currency: "EUR", MarketValue: 1000, IsFund: true,
			CountryWeights: map[string]float64{"United States": 70.2, "Japan": 30}},
	})
	sum := 0.0
	for _, e := range result {
		if e.Currency == CurrencyUnknown {
			t.Errorf("no Unknown bucket expected for weights summing to 100.2, got %+v", e)
		}
		sum += e.Pct
	}
	if math.Abs(sum-100) > 1e-9 {
		t.Errorf("percentages sum to %f, want 100", sum)
	}
}

// Audit W9: an ETF without country data was counted 100 % in its trading
// currency (a EUR-listed world ETF showed as all EUR).
func TestComputeCurrencyExposure_FundWithoutCountriesIsUnknown(t *testing.T) {
	result := ComputeCurrencyExposure([]HoldingForCurrency{
		{ISIN: "IE00BK5BQT80", Currency: "EUR", MarketValue: 10000, IsFund: true},
		{ISIN: "US02079K1079", Currency: "USD", MarketValue: 5000},
	})
	byCode := make(map[string]CurrencyExposureEntry)
	for _, e := range result {
		byCode[e.Currency] = e
	}
	if unk := byCode[CurrencyUnknown]; math.Abs(unk.Value-10000) > 0.01 {
		t.Errorf("fund without countries: Unknown = %f, want 10000", unk.Value)
	}
	if usd := byCode["USD"]; math.Abs(usd.Value-5000) > 0.01 {
		t.Errorf("stock: USD = %f, want 5000 (trading currency)", usd.Value)
	}
	if _, ok := byCode["EUR"]; ok {
		t.Error("EUR must not appear: neither holding is exposed to EUR on record")
	}
}

func TestCountryToCurrencyMapping(t *testing.T) {
	// Verify key mappings exist
	tests := map[string]string{
		"United States":  "USD",
		"Japan":          "JPY",
		"United Kingdom": "GBP",
		"Germany":        "EUR",
		"France":         "EUR",
		"Switzerland":    "CHF",
		"Canada":         "CAD",
		"Australia":      "AUD",
		"Sweden":         "SEK",
		"Denmark":        "DKK",
		"Norway":         "NOK",
		"China":          "CNY",
		"India":          "INR",
		"Brazil":         "BRL",
		"South Korea":    "KRW",
	}
	for country, expected := range tests {
		got := CountryToCurrency[country]
		if got != expected {
			t.Errorf("CountryToCurrency[%q] = %q, want %q", country, got, expected)
		}
	}
}
