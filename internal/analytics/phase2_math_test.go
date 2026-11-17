package analytics

import (
	"math"
	"testing"
	"time"
)

func TestSmoothOutliers_PermanentStepDoesNotFlatline(t *testing.T) {
	start := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	// Day 0..2: 10k, Day 3: transient 0 glitch, Day 4: 10.1k, Day 5..9: permanent +150% step to 25k+
	vals := []float64{10000, 10050, 10100, 0, 10150, 25000, 25200, 24800, 25500, 26000}
	snaps := make([]DailyValuation, len(vals))
	for i, v := range vals {
		snaps[i] = DailyValuation{Date: start.AddDate(0, 0, i), Value: v}
	}

	smoothed := smoothOutliers(snaps)
	// Transient zero glitch at index 3 must be smoothed to previous 10100.
	if smoothed[3].Value != 10100 {
		t.Fatalf("transient zero glitch smoothed[3] = %.2f, want 10100", smoothed[3].Value)
	}
	// Permanent step at index 5..9 must NOT be flatlined at ~10k!
	for i := 5; i < len(smoothed); i++ {
		if smoothed[i].Value != vals[i] {
			t.Fatalf("permanent step smoothed[%d] = %.2f, want %.2f (must not flatline)", i, smoothed[i].Value, vals[i])
		}
	}
}

func TestBuildCashflowAdjustedSeries_NeutralizesDeposits(t *testing.T) {
	start := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	// Portfolio starts at 10,000 EUR, receives +40,000 EUR deposit on day 10, zero market movement.
	var snaps []DailyValuation
	for i := 0; i < 30; i++ {
		val := 10000.0
		if i >= 10 {
			val = 50000.0
		}
		snaps = append(snaps, DailyValuation{Date: start.AddDate(0, 0, i), Value: val})
	}
	flows := []CashFlow{{Date: start.AddDate(0, 0, 10), Amount: 40000}}

	adj := BuildCashflowAdjustedSeries(snaps, flows)
	if len(adj) != len(snaps) {
		t.Fatalf("len(adj) = %d, want %d", len(adj), len(snaps))
	}
	metrics := ComputeRiskMetrics(adj, 0, 50000)
	if metrics.AnnualizedVolatility > 0.01 {
		t.Fatalf("expected ~0%% volatility after neutralizing deposit, got %.4f%%", metrics.AnnualizedVolatility)
	}
	if metrics.MaxDrawdown > 0.01 {
		t.Fatalf("expected ~0%% drawdown after neutralizing deposit, got %.4f%%", metrics.MaxDrawdown)
	}
}

func TestComputeTWR_IntradayDepositNoSignFlip(t *testing.T) {
	d0 := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	d1 := time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC)
	// Start 1,000 EUR; morning deposit +50,000 EUR; market drops 5% intraday -> end value = 51,000 * 0.95 = 48,450 EUR.
	// Old end-of-day formula produced (48450 - 1000 - 50000)/1000 = -2.55 -> 1+r = -1.55 < 0 (flipped sign!).
	vals := []DailyValuation{
		{Date: d0, Value: 1000},
		{Date: d1, Value: 48450},
	}
	flows := []CashFlow{{Date: d1, Amount: 50000}}
	twr := CalculateTWR(vals, flows)
	if math.Abs(twr-(-0.05)) > 0.005 {
		t.Fatalf("CalculateTWR with large intraday deposit = %.4f, want ~-0.05 (-5%%)", twr)
	}
}

func TestComputeVorabpauschaleDetailed_MidYearProRataAndDistributions(t *testing.T) {
	// 2024 Basiszins = 2.29%.
	// Fund bought in July (month 7) for 12,000 EUR, year-end 13,000 EUR, distributions 50 EUR, equity TFS 30%.
	// Full-year Basisertrag on 12,000 = 12000 * 0.0229 * 0.7 = 192.36 EUR.
	// Pro-rated for July acquisition ((13 - 7)/12 = 6/12 = 0.5): 96.18 EUR.
	// Minus 50 EUR distribution -> Vorabpauschale = 46.18 EUR.
	// Tax after 30% TFS: 46.18 * 0.70 * 0.26375 = 8.53 EUR.
	entries := ComputeVorabpauschaleDetailed(2024, []VorabpauschaleInput{
		{
			ISIN:          "IE00BK5BQT80",
			Name:          "FTSE All-World",
			Jan1Value:     0,
			YearEndValue:  13000,
			Distributions: 50,
			MidYearBuys:   []VorabpauschaleLotInput{{BuyMonth: 7, ValueJan: 12000}},
			TFSRate:       TeilfreistellungEquity,
		},
	})
	if len(entries) != 1 {
		t.Fatalf("expected 1 Vorabpauschale entry, got %d", len(entries))
	}
	if math.Abs(entries[0].Vorabpauschale-46.18) > 0.05 {
		t.Fatalf("Vorabpauschale = %.2f, want 46.18", entries[0].Vorabpauschale)
	}
	if math.Abs(entries[0].TaxOnVP-8.53) > 0.05 {
		t.Fatalf("TaxOnVP = %.2f, want 8.53", entries[0].TaxOnVP)
	}
}

func TestComputeTaxLots_PerDepotFIFOAndFractionalCrypto(t *testing.T) {
	txns := []TaxTransaction{
		// Depot A: buy 10 @ 100, then buy 10 @ 200, then sell 10 (consumes 10 @ 100 in Depot A only)
		{AccountID: "depot-A", Date: "2023-06-15", Year: 2023, Type: "buy", ISIN: "IE00TEST0001", Quantity: 10, Amount: 1000, TFS: 0.30, HasExplicitTFS: true},
		{AccountID: "depot-B", Date: "2022-03-10", Year: 2022, Type: "buy", ISIN: "IE00TEST0001", Quantity: 10, Amount: 500, TFS: 0.30, HasExplicitTFS: true},
		{AccountID: "depot-A", Date: "2024-02-20", Year: 2024, Type: "buy", ISIN: "IE00TEST0001", Quantity: 10, Amount: 2000, TFS: 0.30, HasExplicitTFS: true},
		{AccountID: "depot-A", Date: "2024-08-01", Year: 2024, Type: "sell", ISIN: "IE00TEST0001", Quantity: 10, Amount: 2200, TFS: 0.30, HasExplicitTFS: true},
		// Fractional crypto position 0.0005 BTC (< old 0.001 cutoff!)
		{AccountID: "crypto-1", Date: "2024-11-05", Year: 2024, Type: "buy", ISIN: "CRYPTO-BTC", Quantity: 0.0005, Amount: 40, TFS: 0, HasExplicitTFS: true},
	}
	prices := map[string]float64{"IE00TEST0001": 250, "CRYPTO-BTC": 90000}
	names := map[string]float64{}
	_ = names
	lots := ComputeTaxLots(txns, prices, map[string]string{"IE00TEST0001": "Test ETF", "CRYPTO-BTC": "Bitcoin"}, map[string]bool{"IE00TEST0001": true})
	if len(lots) != 3 {
		t.Fatalf("expected 3 remaining lots (depot-B 2022 lot, depot-A 2024 lot, crypto 0.0005 BTC), got %d", len(lots))
	}

	foundDepotB := false
	foundCrypto := false
	for _, l := range lots {
		if l.AccountID == "depot-B" {
			foundDepotB = true
			if l.BuyDate != "2022-03-10" || math.Abs(l.CostBasis-500) > 0.01 {
				t.Fatalf("depot-B lot should be untouched by depot-A sell: date=%s cost=%.2f", l.BuyDate, l.CostBasis)
			}
		}
		if l.ISIN == "CRYPTO-BTC" {
			foundCrypto = true
			if math.Abs(l.Quantity-0.0005) > 1e-9 {
				t.Fatalf("fractional BTC lot quantity = %.8f, want 0.0005", l.Quantity)
			}
		}
	}
	if !foundDepotB || !foundCrypto {
		t.Fatalf("missing expected lots: foundDepotB=%v foundCrypto=%v", foundDepotB, foundCrypto)
	}
}

func TestTWRAndCashflowAdjustedSeries_LargeDepositSmallDipAndGain(t *testing.T) {
	d0 := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	d1 := time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC)

	// Case 1: Start 1,000 EUR + 50,000 EUR deposit -> 51,000 EUR, small -1.9608% dip to 50,000 EUR (EndValue == NetFlow).
	// Must produce -1.9608% return, NOT a -100% cliff!
	dipVals := []DailyValuation{{Date: d0, Value: 1000}, {Date: d1, Value: 50000}}
	flows := []CashFlow{{Date: d1, Amount: 50000}}
	wantDip := (50000.0 / 51000.0) - 1.0
	if got := CalculateTWR(dipVals, flows); math.Abs(got-wantDip) > 1e-4 {
		t.Fatalf("CalculateTWR at EndValue == NetFlow = %.6f, want %.6f", got, wantDip)
	}
	adjDip := BuildCashflowAdjustedSeries(dipVals, flows)
	gotAdjDip := (adjDip[1].Value / adjDip[0].Value) - 1.0
	if math.Abs(gotAdjDip-wantDip) > 1e-4 {
		t.Fatalf("BuildCashflowAdjustedSeries return at EndValue == NetFlow = %.6f, want %.6f", gotAdjDip, wantDip)
	}

	// Case 2: Start 1,000 EUR + 50,000 EUR deposit -> 51,000 EUR, +2.0% gain to 52,020 EUR.
	// Must produce +2.0% return, NOT +102% leveraged return on the pre-deposit 1,000 EUR!
	gainVals := []DailyValuation{{Date: d0, Value: 1000}, {Date: d1, Value: 52020}}
	if got := CalculateTWR(gainVals, flows); math.Abs(got-0.02) > 1e-4 {
		t.Fatalf("CalculateTWR with +2%% gain on large deposit = %.6f, want 0.02", got)
	}
	adjGain := BuildCashflowAdjustedSeries(gainVals, flows)
	gotAdjGain := (adjGain[1].Value / adjGain[0].Value) - 1.0
	if math.Abs(gotAdjGain-0.02) > 1e-4 {
		t.Fatalf("BuildCashflowAdjustedSeries return with +2%% gain = %.6f, want 0.02", gotAdjGain)
	}
}

func TestLoanPayoffDateAndScenario_ExtraRepaymentPaysOffBalance(t *testing.T) {
	extraDate := time.Date(2025, 6, 15, 0, 0, 0, 0, time.UTC)
	loan := Loan{
		Principal:        100000,
		PayoutDate:       time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
		FirstPaymentDate: time.Date(2025, 2, 1, 0, 0, 0, 0, time.UTC),
		Rate:             0.03,
		MonthlyPayment:   500,
		FixedUntil:       time.Date(2035, 1, 1, 0, 0, 0, 0, time.UTC),
		Extras:           []ExtraRepayment{{Date: extraDate, Amount: 100000}},
	}
	pd := loan.PayoffDate()
	if !pd.Equal(extraDate) {
		t.Fatalf("Loan.PayoffDate() = %v, want %v", pd, extraDate)
	}
	sc := loan.Scenario(time.Date(2025, 3, 1, 0, 0, 0, 0, time.UTC), 0, -1)
	if !sc.PayoffDate.Equal(extraDate) {
		t.Fatalf("Loan.Scenario().PayoffDate = %v, want %v", sc.PayoffDate, extraDate)
	}
}

func TestReconstructVorabpauschaleYear_InYearSellFIFODeductsJan1AndMidYearLots(t *testing.T) {
	// Depot A holds 100 shares pre-2024 (@ 80 EUR), buys 50 shares in June 2024 (@ 100 EUR = 5,000 EUR),
	// and sells 120 shares in October 2024.
	// FIFO consumes all 100 pre-2024 shares + 20 of the June shares -> 30 June shares (3,000 EUR) survive at year-end!
	txns := []VorabpauschaleTxn{
		{AccountID: "depot-A", ISIN: "IE00BK5BQT80", Year: 2023, Month: 5, Type: "buy", Quantity: 100, AmountEUR: 8000},
		{AccountID: "depot-A", ISIN: "IE00BK5BQT80", Year: 2024, Month: 6, Type: "buy", Quantity: 50, AmountEUR: 5000},
		{AccountID: "depot-A", ISIN: "IE00BK5BQT80", Year: 2024, Month: 10, Type: "sell", Quantity: 120, AmountEUR: 13200},
	}
	posMap := ReconstructVorabpauschaleYear(2024, txns)
	pos, ok := posMap["IE00BK5BQT80"]
	if !ok {
		t.Fatalf("expected surviving position for IE00BK5BQT80")
	}
	if pos.Jan1Qty != 0 {
		t.Fatalf("Jan1Qty = %.4f, want 0 (consumed by October sell)", pos.Jan1Qty)
	}
	if math.Abs(pos.YearEndQty-30) > 1e-6 {
		t.Fatalf("YearEndQty = %.4f, want 30", pos.YearEndQty)
	}
	if len(pos.MidYearBuys) != 1 || pos.MidYearBuys[0].BuyMonth != 6 || math.Abs(pos.MidYearBuys[0].ValueJan-3000) > 0.01 {
		t.Fatalf("MidYearBuys = %+v, want [{BuyMonth:6 ValueJan:3000}]", pos.MidYearBuys)
	}
}
