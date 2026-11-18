package analytics

import (
	"math"
	"testing"
)

func TestCapitalIncomeTaxRate(t *testing.T) {
	cases := []struct {
		church, want float64
	}{
		{0, 0.26375},
		{0.08, 0.278186},
		{0.09, 0.279951},
		{-1, 0.26375},
		{0.5, 0.279951},
	}
	for _, c := range cases {
		if got := CapitalIncomeTaxRate(c.church); math.Abs(got-c.want) > 1e-6 {
			t.Errorf("CapitalIncomeTaxRate(%v) = %.6f, want %.6f", c.church, got, c.want)
		}
	}
}

func TestSparerPauschbetragForYear(t *testing.T) {
	cases := []struct {
		year  int
		joint bool
		want  float64
	}{
		{2022, false, 801}, {2022, true, 1602}, {2023, false, 1000}, {2026, true, 2000},
	}
	for _, c := range cases {
		if got := SparerPauschbetragForYear(c.year, c.joint); got != c.want {
			t.Errorf("SparerPauschbetragForYear(%d, %v) = %v, want %v", c.year, c.joint, got, c.want)
		}
	}
}

func TestTaxAssetClassRouting(t *testing.T) {
	if !IsShareAssetClass("stock") || IsShareAssetClass("etf") || IsShareAssetClass("fund") {
		t.Error("only stocks belong in the Aktienverlusttopf (§20 Abs. 6 S. 4 EStG)")
	}
	if !IsPrivateSaleAssetClass("crypto") || !IsPrivateSaleAssetClass("commodity") || IsPrivateSaleAssetClass("etf") {
		t.Error("crypto and physical commodities are §23 private sales")
	}
	if PrivateSaleFreigrenze(2023) != 600 || PrivateSaleFreigrenze(2024) != 1000 {
		t.Error("§23 Freigrenze is 600 € until 2023, 1,000 € from 2024")
	}
}

func TestBasiszins2026(t *testing.T) {
	if BasiszinsByYear[2026] != 3.20 {
		t.Errorf("Basiszins 2026 = %v, want 3.20 (BMF 13.01.2026)", BasiszinsByYear[2026])
	}
}
