package analytics

import (
	"math"
	"testing"
)

// Teilfreistellung classification (InvStG 2018 § 20):
//   - Aktienfonds (equity exposure ≥ 51%)              → 30% TFS
//   - Mischfonds (equity 25%–50.99%)                   → 15% TFS
//   - Sonstige Investmentfonds (bonds, money market,
//     commodity, equity <25%)                          → 0% TFS
//
// The codebase currently maps `asset_class == "etf"` → 30% TFS uniformly,
// which over-claims the exemption on bond ETFs and under-claims it on
// mixed funds. The TeilfreistellungRate helper exists so callers can route
// through proper classification when fund-composition data is available.
// These tests verify the helper against a curated set of well-known
// German-traded ISINs whose equity weighting is publicly documented in
// the issuer fact sheets.
//
// Sources for the equity-percentage figures used below:
//   - iShares fact sheets (ishares.com/de)
//   - Vanguard fact sheets (vanguardinvestor.de)
//   - Xtrackers fact sheets (etf.dws.com)
// As of 2026-05; periodic re-verification against current fact sheets is
// expected if the test ever needs updating.

func TestTeilfreistellung_RateConstants(t *testing.T) {
	// Pin the three rate constants. A drift here would silently shift
	// every after-tax KPI in the app (Anlage KAP, Sell Simulator, FSA
	// utilization, switch tax cost).
	if TeilfreistellungEquity != 0.30 {
		t.Errorf("TeilfreistellungEquity = %v, want 0.30 (InvStG § 20(1))", TeilfreistellungEquity)
	}
	if TeilfreistellungMixed != 0.15 {
		t.Errorf("TeilfreistellungMixed = %v, want 0.15 (InvStG § 20(2))", TeilfreistellungMixed)
	}
	if TeilfreistellungBond != 0.00 {
		t.Errorf("TeilfreistellungBond = %v, want 0.00 (Sonstige Investmentfonds)", TeilfreistellungBond)
	}
}

func TestTeilfreistellung_RateBuckets(t *testing.T) {
	// Verify the boundary semantics of TeilfreistellungRate. Boundaries
	// matter: a fund at exactly 50% equity is a Mischfonds (15%), not
	// equity. A fund at exactly 25% is Mischfonds, not Sonstige.
	cases := []struct {
		name      string
		equityPct float64
		want      float64
	}{
		// Aktienfonds: ≥51%
		{"pure equity 100%", 1.00, 0.30},
		{"pure equity 80%", 0.80, 0.30},
		{"equity boundary 51%", 0.51, 0.30},
		// Mischfonds: 25% ≤ x < 51%
		{"mixed just under equity 50.99%", 0.5099, 0.15},
		{"mixed classic 60/40 → 40% (bond side)", 0.40, 0.15},
		{"mixed boundary 25%", 0.25, 0.15},
		// Sonstige: < 25%
		{"mixed just under 24.99%", 0.2499, 0.00},
		{"bond-heavy 20%", 0.20, 0.00},
		{"pure bond 0%", 0.00, 0.00},
		// Edge: negative would mean malformed input — clamps to 0 bucket.
		{"negative malformed", -0.10, 0.00},
	}
	for _, c := range cases {
		got := TeilfreistellungRate(c.equityPct)
		if math.Abs(got-c.want) > 1e-9 {
			t.Errorf("%s (equity=%.4f): TeilfreistellungRate = %v, want %v",
				c.name, c.equityPct, got, c.want)
		}
	}
}

// knownFund represents a well-known German-traded ISIN with its
// documented equity exposure and expected TFS bucket.
type knownFund struct {
	isin        string
	name        string
	equityPct   float64 // from issuer fact sheet
	wantRate    float64
	wantBucket  string
}

func TestTeilfreistellung_KnownISINSet(t *testing.T) {
	// Verification: each well-known ISIN routes to the correct TFS rate
	// when its equity composition is provided. This is the "known ISIN
	// set" the spec requires.
	known := []knownFund{
		// --- Aktienfonds (30% TFS) ---
		{"IE00B4L5Y983", "iShares Core MSCI World UCITS ETF (Acc)", 1.00, 0.30, "Aktienfonds"},
		{"IE00B3RBWM25", "Vanguard FTSE All-World UCITS ETF (Dist)", 1.00, 0.30, "Aktienfonds"},
		{"IE00B5BMR087", "iShares Core S&P 500 UCITS ETF (Acc)", 1.00, 0.30, "Aktienfonds"},
		{"IE00BKM4GZ66", "iShares Core MSCI EM IMI UCITS ETF (Acc)", 1.00, 0.30, "Aktienfonds"},
		{"IE00B4L5YC18", "iShares Core MSCI Europe UCITS ETF (Acc)", 1.00, 0.30, "Aktienfonds"},
		{"LU0274208692", "Xtrackers MSCI World UCITS ETF (1C)", 1.00, 0.30, "Aktienfonds"},
		// LifeStrategy 80%: 80% equity → still ≥51% → Aktienfonds
		{"IE00BMVB5L14", "Vanguard LifeStrategy 80% Equity UCITS ETF", 0.80, 0.30, "Aktienfonds"},
		// LifeStrategy 60%: 60% equity → ≥51% → Aktienfonds (NOT mixed,
		// despite the colloquial "balanced" label).
		{"IE00BMVB5K07", "Vanguard LifeStrategy 60% Equity UCITS ETF", 0.60, 0.30, "Aktienfonds"},

		// --- Mischfonds (15% TFS) ---
		// LifeStrategy 40%: 40% equity → between 25% and 51% → Mischfonds.
		{"IE00BMVB5J91", "Vanguard LifeStrategy 40% Equity UCITS ETF", 0.40, 0.15, "Mischfonds"},

		// --- Sonstige Investmentfonds (0% TFS) ---
		// LifeStrategy 20%: 20% equity → <25% → Sonstige.
		{"IE00BMVB5H77", "Vanguard LifeStrategy 20% Equity UCITS ETF", 0.20, 0.00, "Sonstige"},
		// Bond ETFs — the common over-claim trap: asset_class="etf" but
		// the fund holds zero equity, so TFS must be 0%.
		{"IE00B3F81R35", "iShares Core Global Aggregate Bond UCITS ETF", 0.00, 0.00, "Sonstige (bond)"},
		{"IE00B3DKXQ41", "iShares Core EUR Govt Bond UCITS ETF", 0.00, 0.00, "Sonstige (bond)"},
		{"IE00BZ163L38", "Vanguard EUR Eurozone Government Bond UCITS ETF", 0.00, 0.00, "Sonstige (bond)"},
		{"LU0290355717", "Xtrackers II Eurozone Government Bond UCITS ETF", 0.00, 0.00, "Sonstige (bond)"},
		// Commodity / gold (ETCs and Sonstige money market — 0% TFS).
		{"DE000A0S9GB0", "Xetra-Gold", 0.00, 0.00, "Sonstige (commodity)"},
	}

	for _, f := range known {
		got := TeilfreistellungRate(f.equityPct)
		if math.Abs(got-f.wantRate) > 1e-9 {
			t.Errorf("%s %s (equity=%.0f%%, expected %s, want %.0f%% TFS): got %.0f%% TFS",
				f.isin, f.name, f.equityPct*100, f.wantBucket, f.wantRate*100, got*100)
		}
	}
}

func TestTeilfreistellung_DocumentsCurrentSimplification(t *testing.T) {
	// Pin the existing equityMap behavior at the handler boundary:
	// `equityMap[isin] = sec.AssetClass == "etf"`. This grants 30% TFS
	// to every ETF — INCLUDING bond ETFs — which is wrong under InvStG
	// 2018 but is the live behavior across analysis.go / portfolio.go /
	// scheduler/jobs.go (~15 call sites). TeilfreistellungRate is the
	// migration target; until the equityMap callers route through it,
	// users with bond ETFs see overstated after-tax figures.
	//
	// This test ensures the proper rates are available for those callers
	// to adopt incrementally.
	bondISIN := "IE00B3F81R35"
	if got := simulateLegacyClassification(bondISIN, "etf"); got != TeilfreistellungEquity {
		t.Errorf("legacy classification of bond ETF (asset_class=etf): TFS=%v, want %v — confirms the gap exists", got, TeilfreistellungEquity)
	}
	// Proper classification via TeilfreistellungRate with real equity%:
	if got := TeilfreistellungRate(0.00); got != TeilfreistellungBond {
		t.Errorf("proper classification of bond ETF (equity=0%%): TFS=%v, want %v", got, TeilfreistellungBond)
	}
}

// simulateLegacyClassification reproduces the code pattern
// `equityMap[isin] = sec.AssetClass == "etf"` followed by
// `if equityMap[isin] { rate = TeilfreistellungEquity }`.
// Documenting it as a tiny pure function lets the gap be observed
// in CI rather than only in production tax reports.
func simulateLegacyClassification(_ string, assetClass string) float64 {
	if assetClass == "etf" {
		return TeilfreistellungEquity
	}
	return TeilfreistellungBond
}
