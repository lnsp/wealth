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
// Callers resolve the rate with ResolveSecurityTFSRate: securities.equity_pct
// when known, otherwise a fund-name fallback (bond / money-market / gold
// keywords → 0 %, "LifeStrategy NN%" → by its equity share, other ETFs →
// 30 %). These tests verify TeilfreistellungRate against a curated set of
// well-known German-traded ISINs whose equity weighting is publicly
// documented in the issuer fact sheets, and the name fallback against the
// fund names that used to be over-claimed.
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

func TestResolveSecurityTFSRate_NameFallback(t *testing.T) {
	// Regression (audit KNOWN, bond-ETF Teilfreistellung): with equity_pct
	// not yet populated, bond and low-equity funds must not get the 30 %
	// equity-fund exemption.
	cases := []struct {
		name string
		want float64
	}{
		{"iShares Core EUR Corp Bond UCITS ETF", 0},
		{"iShares USD High Yield Corp Bond UCITS ETF", 0},
		{"Amundi Prime Euro Govies UCITS ETF", 0},
		{"Xtrackers II Eurozone Government Bond UCITS ETF", 0},
		{"iShares Euro Inflation Linked Govt Bond UCITS ETF", 0},
		{"iShares USD Treasury Bond 1-3yr UCITS ETF", 0},
		{"Xtrackers II EUR Overnight Rate Swap UCITS ETF", 0},
		{"Vanguard LifeStrategy 20% Equity UCITS ETF", 0},
		{"Vanguard LifeStrategy 40% Equity UCITS ETF", 0.15},
		{"Vanguard LifeStrategy 60% Equity UCITS ETF", 0.30},
		{"Vanguard LifeStrategy 80% Equity UCITS ETF", 0.30},
		{"Xtrackers MSCI World UCITS ETF 1C", 0.30},
		{"Vanguard FTSE All-World UCITS ETF", 0.30},
	}
	for _, c := range cases {
		if got := ResolveSecurityTFSRate("etf", c.name, nil); math.Abs(got-c.want) > 1e-9 {
			t.Errorf("ResolveSecurityTFSRate(etf, %q) = %.2f, want %.2f", c.name, got, c.want)
		}
	}
	// A known equity_pct always wins over the name.
	eq := 0.95
	if got := ResolveSecurityTFSRate("etf", "Some Corp Bond Name", &eq); got != TeilfreistellungEquity {
		t.Errorf("equity_pct 95 %% → %.2f, want 0.30", got)
	}
	// Stocks and crypto never get a Teilfreistellung.
	if got := ResolveSecurityTFSRate("stock", "Vanguard LifeStrategy 80%", nil); got != 0 {
		t.Errorf("stock → %.2f, want 0", got)
	}
}
