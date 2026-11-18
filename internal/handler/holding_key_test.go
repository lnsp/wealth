package handler

import (
	"crypto/sha256"
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/lnsp/wealth/internal/analytics"
	"github.com/lnsp/wealth/internal/market"
)

// legacySynthID reproduces the per-ETF synthetic ID that fe4671e stored
// (sha256 of "<etf ISIN>|<lower-case name>"), so rows already in the DB are
// covered too.
func legacySynthID(etfISIN, name string) string {
	sum := sha256.Sum256([]byte(etfISIN + "|" + strings.ToLower(name)))
	return fmt.Sprintf("SYNTH:%x", sum[:6])
}

// Audit C3: SYNTH:<sha(etf|name)> IDs differ per ETF, and holdingKey only
// name-keyed XX_ IDs, so two MSCI World ETFs sharing five name-only
// constituents showed 0 % overlap instead of 17.4 %, and top holdings listed
// each company twice.
func TestHoldingKey_SyntheticIDsMatchAcrossETFs(t *testing.T) {
	top := []struct {
		name string
		w    float64
	}{{"Apple Inc.", 4.8}, {"Microsoft Corp.", 4.4}, {"NVIDIA Corp.", 4.2}, {"Amazon.com Inc.", 2.6}, {"Alphabet Inc. A", 1.4}}

	for _, scheme := range []struct {
		label string
		id    func(etf, name string) string
	}{
		{"legacy per-ETF SYNTH ids", legacySynthID},
		{"current name-only SYNTH ids", func(_, name string) string { return market.SyntheticConstituentID(name) }},
	} {
		t.Run(scheme.label, func(t *testing.T) {
			var etfs []analytics.ETFWithHoldings
			var positions []analytics.HoldingWithETF
			for i, etf := range []string{"IE00B4L5Y983", "IE00BJ0KDQ92"} {
				weights := map[string]float64{}
				for _, h := range top {
					weights[holdingKey(scheme.id(etf, h.name), h.name)] += h.w
				}
				etfs = append(etfs, analytics.ETFWithHoldings{ISIN: etf, Holdings: weights})
				positions = append(positions, analytics.HoldingWithETF{ISIN: etf, MarketValue: []float64{6000, 4000}[i], ETFHoldings: weights})
			}
			m := analytics.BuildOverlapMatrix(etfs)
			if math.Abs(m[0][1]-17.4) > 1e-9 {
				t.Errorf("overlap = %.2f %%, want 17.40 %%", m[0][1])
			}
			exposure := analytics.ComputeLookThroughExposure(positions, 10000)
			if len(exposure) != 5 {
				t.Errorf("top holdings rows = %d, want 5 merged rows", len(exposure))
			}
			if apple := exposure[holdingKey("", "Apple Inc.")]; math.Abs(apple-0.048) > 1e-9 {
				t.Errorf("Apple exposure = %.4f, want 0.0480 (one merged row)", apple)
			}
		})
	}
}

func TestSyntheticConstituentID_IndependentOfFund(t *testing.T) {
	a := market.SyntheticConstituentID("NVIDIA Corp.")
	b := market.SyntheticConstituentID("  nvidia corporation ")
	if a != b || !strings.HasPrefix(a, "SYNTH:") {
		t.Errorf("ids = %q / %q, want the same SYNTH: id", a, b)
	}
	if !market.IsSyntheticConstituentID(a) || !market.IsSyntheticConstituentID("XX_IE00B5_1") || market.IsSyntheticConstituentID("US0378331005") {
		t.Error("IsSyntheticConstituentID misclassifies ids")
	}
}

func TestHoldingKey(t *testing.T) {
	tests := []struct {
		name    string
		isin    string
		hname   string
		wantKey string
	}{
		{
			name:    "real ISIN is used as-is",
			isin:    "US0378331005",
			hname:   "Apple Inc.",
			wantKey: "US0378331005",
		},
		{
			name:    "synthetic ISIN falls back to name",
			isin:    "XX_IE00B5_0",
			hname:   "Apple Inc.",
			wantKey: "name:apple",
		},
		{
			name:    "empty ISIN falls back to name",
			isin:    "",
			hname:   "Microsoft Corp.",
			wantKey: "name:microsoft",
		},
		{
			name:    "name is trimmed, lowercased and stripped of legal form",
			isin:    "XX_test_1",
			hname:   "  NVIDIA Corp  ",
			wantKey: "name:nvidia",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := holdingKey(tt.isin, tt.hname)
			if got != tt.wantKey {
				t.Errorf("holdingKey(%q, %q) = %q, want %q", tt.isin, tt.hname, got, tt.wantKey)
			}
		})
	}
}

func TestHoldingKey_OverlapMatching(t *testing.T) {
	// Same stock in different ETFs should produce the same key
	// even with different synthetic ISINs
	key1 := holdingKey("XX_IE00B5_1", "Apple")
	key2 := holdingKey("XX_IE00BK_3", "Apple")
	if key1 != key2 {
		t.Errorf("same holding name with different synthetic ISINs should match: %q != %q", key1, key2)
	}

	// Different stocks should produce different keys
	key3 := holdingKey("XX_IE00B5_0", "Microsoft")
	if key1 == key3 {
		t.Error("different holding names should not match")
	}

	// Real ISINs should always be used even if names differ
	key4 := holdingKey("US0378331005", "Apple Inc.")
	key5 := holdingKey("US0378331005", "Apple")
	if key4 != key5 {
		t.Errorf("same real ISIN should match regardless of name: %q != %q", key4, key5)
	}
}
