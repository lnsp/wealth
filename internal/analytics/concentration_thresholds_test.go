package analytics

import (
	"testing"
)

// Concentration alert thresholds locked to the TASKS spec:
//   ≥10% effective single-stock exposure → "critical" (renders claret)
//   ≥5%  but <10%                          → "warning"  (renders amber)
//   <5%                                    → no alert
//
// Edge cases at the exact threshold values 5.0 and 10.0 are inclusive
// (`>=`), matching the typical German risk-threshold convention. These
// tests pin those boundaries so a refactor can't silently shift the
// cutoff to strict `>` and quietly stop warning a user at exactly 10.0%.

func alertWithConcentration(pct float64) *Alert {
	holdings := []HoldingWithETF{
		{ISIN: "ETF1", MarketValue: 100, ETFHoldings: map[string]float64{"AAPL": pct}},
	}
	alerts := ComputeConcentrationAlerts(nil, holdings, map[string]string{"AAPL": "Apple"})
	for i, a := range alerts {
		if a.Type == "concentration" {
			return &alerts[i]
		}
	}
	return nil
}

func TestConcentrationThreshold_BelowWarning(t *testing.T) {
	// 4.99% — below the 5% warning floor, no alert.
	if a := alertWithConcentration(4.99); a != nil {
		t.Errorf("4.99%% concentration → expected no alert, got level=%s value=%.2f", a.Level, a.Value)
	}
}

func TestConcentrationThreshold_AtWarningFloor(t *testing.T) {
	// Exactly 5.0% — warning floor, inclusive.
	a := alertWithConcentration(5.0)
	if a == nil {
		t.Fatal("5.0%% concentration → expected an alert (boundary, inclusive)")
	}
	if a.Level != "warning" {
		t.Errorf("5.0%% → level=%s, want warning", a.Level)
	}
}

func TestConcentrationThreshold_InWarningBand(t *testing.T) {
	// 7.5% — squarely in the warning band (5..<10%).
	a := alertWithConcentration(7.5)
	if a == nil {
		t.Fatal("7.5%% concentration → expected a warning alert")
	}
	if a.Level != "warning" {
		t.Errorf("7.5%% → level=%s, want warning", a.Level)
	}
}

func TestConcentrationThreshold_JustBelowCritical(t *testing.T) {
	// 9.99% — still in the warning band, NOT critical yet.
	a := alertWithConcentration(9.99)
	if a == nil {
		t.Fatal("9.99%% concentration → expected an alert")
	}
	if a.Level != "warning" {
		t.Errorf("9.99%% → level=%s, want warning (critical only at >=10)", a.Level)
	}
}

func TestConcentrationThreshold_AtCriticalFloor(t *testing.T) {
	// Exactly 10.0% — critical floor, inclusive.
	a := alertWithConcentration(10.0)
	if a == nil {
		t.Fatal("10.0%% concentration → expected an alert")
	}
	if a.Level != "critical" {
		t.Errorf("10.0%% → level=%s, want critical (boundary, inclusive)", a.Level)
	}
}

func TestConcentrationThreshold_DeepInCritical(t *testing.T) {
	// 35% — deep in critical band, no doubt about it.
	a := alertWithConcentration(35.0)
	if a == nil || a.Level != "critical" {
		t.Errorf("35%% concentration should be critical, got %+v", a)
	}
}

func TestConcentrationThreshold_ConstantsAlignWithSpec(t *testing.T) {
	// Hard-code the contract: the warning threshold is 5% and the spec
	// wants the critical band to start at 10%. If anyone tweaks the
	// constant in portfolio.go below 5 or above 10, this test fails to
	// remind them the frontend chip colours assume these exact cutoffs.
	if ConcentrationWarningThreshold != 5.0 {
		t.Errorf("ConcentrationWarningThreshold = %.2f, want 5.0 (>5%% amber per TASKS spec)",
			ConcentrationWarningThreshold)
	}
}
