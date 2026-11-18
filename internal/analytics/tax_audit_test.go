package analytics

import (
	"math"
	"testing"
	"time"
)

// Regression tests for the 2026-11-18 calculation audit (docs/calc-audit-2026-11-18.md,
// Tax section). Each test reproduces the audit's worked example against the
// real engine.

func near(t *testing.T, label string, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 0.011 {
		t.Errorf("%s = %.4f, want %.2f", label, got, want)
	}
}

func etfTxn(date, typ, isin string, qty, amount float64) TaxTransaction {
	d, _ := time.Parse("2006-01-02", date)
	return TaxTransaction{AccountID: "depot", Date: date, Year: d.Year(), Type: typ, ISIN: isin, Quantity: qty, Amount: amount,
		AssetClass: "etf", IsEquityFund: true, TFS: 0.30, HasExplicitTFS: true}
}

func stockTxn(acct, date, typ, isin string, qty, amount float64) TaxTransaction {
	d, _ := time.Parse("2006-01-02", date)
	return TaxTransaction{AccountID: acct, Date: date, Year: d.Year(), Type: typ, ISIN: isin, Quantity: qty, Amount: amount,
		AssetClass: "stock", HasExplicitTFS: true}
}

// C2: share losses stay in the Aktientopf and cannot offset fund gains or interest.
func TestAudit_C2_SummaryUsesLossPots(t *testing.T) {
	txns := []TaxTransaction{
		stockTxn("d", "2025-02-01", "buy", "STK", 10, 10000),
		stockTxn("d", "2025-03-01", "sell", "STK", 10, 5000),
		etfTxn("2025-02-01", "buy", "ETF", 10, 10000),
		etfTxn("2025-04-01", "sell", "ETF", 10, 15000),
		{AccountID: "d", Date: "2025-12-31", Year: 2025, Type: "interest", Amount: 600},
	}
	s := ComputeTaxSummary(txns, 2025, nil)
	// 3,500 (ETF gain after TFS) + 600 interest − 1,000 allowance = 3,100 × 26.375 %
	near(t, "estimated tax", s.EstimatedTax, 817.63)
	near(t, "taxable", s.TaxableGain, 4100)
	near(t, "share carry-forward", s.LossCarryOutShare, -5000)
	near(t, "interest", s.InterestIncome, 600)
}

// C3: Teilfreistellung also reduces fund losses (§20 InvStG).
func TestAudit_C3_TeilfreistellungOnFundLosses(t *testing.T) {
	txns := []TaxTransaction{
		etfTxn("2025-02-01", "buy", "ETF", 10, 10000),
		etfTxn("2025-04-01", "sell", "ETF", 10, 5000),
		{AccountID: "depot", Date: "2025-05-01", Year: 2025, Type: "dividend", ISIN: "STK", Amount: 6000, AssetClass: "stock", HasExplicitTFS: true},
	}
	s := ComputeTaxSummary(txns, 2025, nil)
	// −3,500 + 6,000 − 1,000 = 1,500 × 26.375 %
	near(t, "estimated tax", s.EstimatedTax, 395.63)
	near(t, "teilfreistellung (negative on a loss)", s.TeilfreistellungAmt, -1500)
	if len(s.BySecurity) != 1 || math.Abs(s.BySecurity[0].TaxablePL-(-3500)) > 0.01 || s.BySecurity[0].Pot != "general" {
		t.Errorf("by_security = %+v, want one general-pot row with taxable_pl -3500", s.BySecurity)
	}
	// The Loss-pots tab agrees.
	p := ComputeLossPots(txns)[0]
	near(t, "loss pots general losses", p.GeneralLosses, -3500)
}

// C4: interest is capital income.
func TestAudit_C4_InterestIncluded(t *testing.T) {
	txns := []TaxTransaction{
		stockTxn("d", "2025-01-10", "buy", "STK", 10, 1000),
		stockTxn("d", "2025-06-10", "sell", "STK", 10, 2000),
		{AccountID: "d", Date: "2025-12-31", Year: 2025, Type: "interest", Amount: 600},
		{AccountID: "d", Date: "2025-12-31", Year: 2025, Type: "interest", Amount: -4.12}, // interest paid: not deductible
	}
	s := ComputeTaxSummary(txns, 2025, nil)
	near(t, "estimated tax", s.EstimatedTax, 158.25)
	near(t, "interest income", s.InterestIncome, 600)
}

// C6: an in-kind depot transfer keeps the original cost basis and date.
func TestAudit_C6_TransferKeepsCostBasisAndDate(t *testing.T) {
	for _, recorded := range []float64{2000, 0} {
		txns := []TaxTransaction{
			stockTxn("A", "2020-03-02", "buy", "STK", 10, 1000),
			stockTxn("A", "2024-05-10", "transfer_out", "STK", 10, 0),
			stockTxn("B", "2024-05-12", "transfer", "STK", 10, recorded),
			stockTxn("B", "2025-06-01", "sell", "STK", 10, 2500),
		}
		s := ComputeTaxSummary(txns, 2025, nil)
		near(t, "realised gain", s.RealizedGains, 1500)
		near(t, "estimated tax", s.EstimatedTax, 131.88)

		lots := ComputeTaxLots(txns[:3], map[string]float64{"STK": 250}, nil, nil)
		if len(lots) != 1 || lots[0].AccountID != "B" || lots[0].BuyDate != "2020-03-02" || math.Abs(lots[0].CostBasis-1000) > 0.01 {
			t.Errorf("recorded %.0f: lots after transfer = %+v, want one lot in B bought 2020-03-02 at 1000", recorded, lots)
		}
	}
}

// C6: an unmatched inbound transfer (RSU vest) is still an acquisition at its recorded value.
func TestAudit_C6_UnmatchedTransferIsAcquisition(t *testing.T) {
	txns := []TaxTransaction{
		stockTxn("MS", "2024-01-10", "transfer", "GOOG", 10, 1500),
		stockTxn("MS", "2025-01-10", "sell", "GOOG", 10, 2500),
	}
	s := ComputeTaxSummary(txns, 2025, nil)
	near(t, "realised gain", s.RealizedGains, 1000)
}

// C6: Vorabpauschale pro-rating follows the original acquisition date.
func TestAudit_C6_VorabpauschaleUsesOriginalAcquisitionDate(t *testing.T) {
	txns := []TaxTransaction{
		etfTxn("2023-03-01", "buy", "ETF", 100, 10000),
		etfTxn("2024-07-01", "transfer_out", "ETF", 100, 0),
		etfTxn("2024-07-02", "transfer", "ETF", 100, 0),
	}
	txns[2].AccountID = "other-depot"
	events := []TaxTransaction{{Type: "vorabpauschale", Year: 2024, ISIN: "ETF", AssetClass: "etf", TFS: 0.30, HasExplicitTFS: true, VPJan1Price: 100, VPYearEndPrice: 110}}
	vp := VorabpauschaleForYear(MergeVorabpauschaleEvents(txns, events), 2024, TaxOptions{})
	if len(vp) != 1 {
		t.Fatalf("expected 1 entry, got %+v", vp)
	}
	// Held all year (since 2023), so no 6/12 reduction: 100 × 100 × 2.29 % × 0.7.
	near(t, "vorabpauschale", vp[0].Vorabpauschale, 160.30)
}

// C7: crypto and physical gold are private sales (§23), not capital income.
func TestAudit_C7_CryptoIsPrivateSale(t *testing.T) {
	btc := func(date, typ string, qty, amount float64) TaxTransaction {
		d, _ := time.Parse("2006-01-02", date)
		return TaxTransaction{AccountID: "wallet", Date: date, Year: d.Year(), Type: typ, ISIN: "CRYPTO:BTC", Name: "Bitcoin", Quantity: qty, Amount: amount, AssetClass: "crypto", HasExplicitTFS: true}
	}
	txns := []TaxTransaction{
		btc("2023-02-01", "buy", 1, 10000),
		btc("2025-03-01", "sell", 1, 20000),
	}
	s := ComputeTaxSummary(txns, 2025, nil)
	if s.EstimatedTax != 0 || s.RealizedGains != 0 || len(s.BySecurity) != 0 {
		t.Errorf("crypto must not be taxed under §20: tax=%.2f gains=%.2f by_security=%d", s.EstimatedTax, s.RealizedGains, len(s.BySecurity))
	}
	if s.PrivateSales == nil {
		t.Fatal("expected a §23 private-sales section")
	}
	near(t, "tax-free P/L (held > 1 year)", s.PrivateSales.TaxFreePL, 10000)
	near(t, "taxable private-sale gain", s.PrivateSales.NetTaxable, 0)

	// Held under a year: taxable at the personal rate, but below the Freigrenze.
	short := []TaxTransaction{btc("2025-01-10", "buy", 1, 1000), btc("2025-06-10", "sell", 1, 1500)}
	ps := ComputeTaxSummary(short, 2025, nil).PrivateSales
	if ps == nil || math.Abs(ps.TaxableGains-500) > 0.01 || !ps.BelowFreigrenze || ps.TaxableAmount != 0 {
		t.Errorf("500 short-term gain: %+v, want taxable gains 500 below the 1,000 Freigrenze", ps)
	}
	// At or above the Freigrenze the whole gain is taxable.
	over := []TaxTransaction{btc("2025-01-10", "buy", 1, 1000), btc("2025-06-10", "sell", 1, 2200)}
	ps = ComputeTaxSummary(over, 2025, nil).PrivateSales
	if ps == nil || ps.BelowFreigrenze || math.Abs(ps.TaxableAmount-1200) > 0.01 {
		t.Errorf("1,200 short-term gain: %+v, want the whole 1,200 taxable", ps)
	}
	// 2023 Freigrenze was 600.
	if PrivateSaleFreigrenze(2023) != 600 {
		t.Errorf("Freigrenze 2023 = %.0f, want 600", PrivateSaleFreigrenze(2023))
	}

	// Lots and the sell simulator keep crypto out of Abgeltungsteuer too.
	lots := ComputeTaxLots(txns[:1], map[string]float64{"CRYPTO:BTC": 20000}, nil, nil)
	if len(lots) != 1 || !lots[0].PrivateSale || lots[0].TaxIfSold != 0 || lots[0].FIFOTaxIfSold != 0 || lots[0].TaxFreeFrom != "2024-02-02" {
		t.Errorf("crypto lot = %+v, want private_sale with no §20 tax, tax-free from 2024-02-02", lots)
	}
	res, total, _ := SimulateSellAt(lots, []SellRequest{{ISIN: "CRYPTO:BTC", AmountEUR: 20000}}, 0, time.Date(2025, 3, 1, 0, 0, 0, 0, time.UTC))
	if len(res) != 1 || !res[0].PrivateSale || res[0].EstimatedTax != 0 || total != 0 || math.Abs(res[0].PrivateSaleTaxFreePL-10000) > 0.01 {
		t.Errorf("crypto sell simulation = %+v total %.2f, want tax-free private sale", res, total)
	}
}

func TestAudit_C7_HoldingPeriodBoundaries(t *testing.T) {
	d := func(s string) time.Time { v, _ := time.Parse("2006-01-02", s); return v }
	cases := []struct {
		acq, sale string
		over      bool
	}{
		{"2024-03-15", "2025-03-15", false}, // the anniversary is still inside the year
		{"2024-03-15", "2025-03-16", true},
		{"2024-02-29", "2025-02-28", false},
		{"2024-02-29", "2025-03-01", true},
	}
	for _, c := range cases {
		if got := HeldOverOneYear(d(c.acq), d(c.sale)); got != c.over {
			t.Errorf("HeldOverOneYear(%s, %s) = %v, want %v", c.acq, c.sale, got, c.over)
		}
	}
}

// C8: the Vorabpauschale is income in the following January and reduces the
// later sale gain (§18 Abs. 3, §19 Abs. 1 S. 3 InvStG).
func TestAudit_C8_VorabpauschaleTaxedAndDeducted(t *testing.T) {
	txns := []TaxTransaction{
		etfTxn("2024-01-15", "buy", "ETF", 100, 10000),
		etfTxn("2026-03-01", "sell", "ETF", 100, 12000),
	}
	events := []TaxTransaction{
		{Type: "vorabpauschale", Year: 2024, ISIN: "ETF", AssetClass: "etf", TFS: 0.30, HasExplicitTFS: true, VPJan1Price: 100, VPYearEndPrice: 110},
		{Type: "vorabpauschale", Year: 2025, ISIN: "ETF", AssetClass: "etf", TFS: 0.30, HasExplicitTFS: true, VPJan1Price: 110, VPYearEndPrice: 115},
	}
	all := MergeVorabpauschaleEvents(txns, events)

	vp2024 := VorabpauschaleForYear(all, 2024, TaxOptions{})
	if len(vp2024) != 1 {
		t.Fatalf("2024 VP entries = %+v", vp2024)
	}
	near(t, "VP 2024", vp2024[0].Vorabpauschale, 160.30)
	near(t, "VP 2024 tax", vp2024[0].TaxOnVP, 29.60)

	s2025 := ComputeTaxSummary(all, 2025, nil)
	near(t, "2025 Vorabpauschale income (for 2024)", s2025.VorabpauschaleIncome, 160.30)
	if s2025.VorabpauschaleYear != 2024 {
		t.Errorf("2025 summary VorabpauschaleYear = %d, want 2024", s2025.VorabpauschaleYear)
	}

	s2026 := ComputeTaxSummary(all, 2026, nil)
	near(t, "2026 Vorabpauschale income (for 2025)", s2026.VorabpauschaleIncome, 194.81)
	near(t, "2026 realised gain after VP deduction", s2026.RealizedGains, 1644.89)
	if len(s2026.BySecurity) != 1 {
		t.Fatalf("by_security = %+v", s2026.BySecurity)
	}
	near(t, "VP deducted", s2026.BySecurity[0].VorabpauschaleDeducted, 355.11)
	// (1,644.89 + 194.81) × 0.7 − 1,000 = 287.79 × 26.375 %; without the
	// deduction it would be 65.56 € more.
	near(t, "2026 tax", s2026.EstimatedTax, 75.90)
}

// W1: pro-rate after the cap, on the start-of-year value.
func TestAudit_W1_VorabpauschaleProRataAfterCap(t *testing.T) {
	entries := ComputeVorabpauschaleDetailed(2025, []VorabpauschaleInput{{
		ISIN: "X", YearEndValue: 10100, TFSRate: 0.30,
		MidYearBuys: []VorabpauschaleLotInput{{BuyMonth: 7, ValueJan: 10000}},
	}})
	if len(entries) != 1 {
		t.Fatalf("entries = %+v", entries)
	}
	// min(177.10, 100) × 6/12 = 50.00 → 50 × 0.7 × 26.375 % = 9.23
	near(t, "vorabpauschale", entries[0].Vorabpauschale, 50)
	near(t, "tax", entries[0].TaxOnVP, 9.23)

	// The ledger path: 100 units bought in July, 1 Jan price 100, year-end 101.
	txns := MergeVorabpauschaleEvents(
		[]TaxTransaction{etfTxn("2025-07-10", "buy", "ETF", 100, 9900)},
		[]TaxTransaction{{Type: "vorabpauschale", Year: 2025, ISIN: "ETF", AssetClass: "etf", TFS: 0.30, HasExplicitTFS: true, VPJan1Price: 100, VPYearEndPrice: 101}},
	)
	vp := VorabpauschaleForYear(txns, 2025, TaxOptions{})
	if len(vp) != 1 {
		t.Fatalf("ledger VP = %+v", vp)
	}
	near(t, "ledger vorabpauschale", vp[0].Vorabpauschale, 50)
	near(t, "ledger tax", vp[0].TaxOnVP, 9.23)
	near(t, "value at 1 Jan price", vp[0].Jan1Value, 10000)
}

// C11: selling everything nets gains and losses; per-lot tax follows FIFO.
func TestAudit_C11_SellAllNetsLots(t *testing.T) {
	txns := []TaxTransaction{
		stockTxn("d", "2024-01-02", "buy", "S", 10, 1000),
		stockTxn("d", "2025-01-02", "buy", "S", 10, 3000),
	}
	lots := ComputeTaxLots(txns, map[string]float64{"S": 200}, nil, nil)
	if len(lots) != 2 {
		t.Fatalf("lots = %+v", lots)
	}
	near(t, "oldest lot FIFO tax", lots[0].FIFOTaxIfSold, 263.75)
	near(t, "selling through the newer lot (FIFO)", lots[1].FIFOTaxIfSold, 0)
	sa := SummarizeSellAll(lots, time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC), 0)
	near(t, "gross tax of selling everything", sa.GrossTax, 0)
	without, with := MarginalSaleTax(txns, HypotheticalSellTxns(lots, time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)), 2026, TaxOptions{})
	near(t, "marginal tax", with-without, 0)
}

// C12 + W3: allowance by year and filing status; church tax via §32d.
func TestAudit_C12_W3_AllowanceAndChurchTax(t *testing.T) {
	gain := func(year int, amount float64) []TaxTransaction {
		y := time.Date(year, 1, 1, 0, 0, 0, 0, time.UTC).Format("2006")
		return []TaxTransaction{
			stockTxn("d", y+"-01-10", "buy", "S", 1, 1000),
			stockTxn("d", y+"-06-10", "sell", "S", 1, 1000+amount),
		}
	}
	s := ComputeTaxSummary(gain(2022, 1000), 2022, nil)
	near(t, "2022 allowance", s.Allowance, 801)
	near(t, "2022 tax", s.EstimatedTax, 52.49)

	s = ComputeTaxSummaryWithOptions(gain(2025, 3000), 2025, nil, TaxOptions{Joint: true})
	near(t, "joint allowance", s.Allowance, 2000)
	near(t, "joint tax", s.EstimatedTax, 263.75)

	s = ComputeTaxSummaryWithOptions(gain(2025, 6177.88), 2025, nil, TaxOptions{ChurchTaxRate: 0.09})
	near(t, "2025 tax with 9 % church tax", s.EstimatedTax, 1449.55)
	near(t, "tax rate", s.TaxRate, 0.279951)
}

// I1: the effective rate keeps two decimals.
func TestAudit_I1_EffectiveRatePrecision(t *testing.T) {
	s := ComputeTaxSummary([]TaxTransaction{
		stockTxn("d", "2025-01-10", "buy", "S", 1, 1000),
		stockTxn("d", "2025-06-10", "sell", "S", 1, 3000),
	}, 2025, nil)
	if s.EffectiveRate != 13.19 { // 263.75 / 2,000
		t.Errorf("effective rate = %v, want 13.19", s.EffectiveRate)
	}
}

// I6: the simulator nets losses against gains per pot and applies TFS to losses.
func TestAudit_I6_SimulateSellNetsPots(t *testing.T) {
	lots := []TaxLot{
		{AccountID: "d", ISIN: "STK", Name: "Stock", Quantity: 10, CostBasis: 1000, CurrentValue: 2000, AssetClass: "stock", Pot: "share"},
		{AccountID: "d", ISIN: "BND", Name: "Bond ETF", Quantity: 10, CostBasis: 1000, CurrentValue: 400, AssetClass: "etf", Pot: "general"},
		{AccountID: "d", ISIN: "ETF", Name: "Equity ETF", Quantity: 10, CostBasis: 1000, CurrentValue: 0.01, AssetClass: "etf", Pot: "general", TFSRate: 0.30, IsEquityFund: true},
	}
	res, total, _ := SimulateSell(lots, []SellRequest{{ISIN: "STK", AmountEUR: 2000}, {ISIN: "BND", AmountEUR: 400}}, 0)
	if len(res) != 2 {
		t.Fatalf("results = %+v", res)
	}
	near(t, "stock row tax (alone)", res[0].EstimatedTax, 263.75)
	near(t, "bond row taxable", res[1].TaxableGain, -600)
	// General loss offsets the share gain: (1,000 − 600) × 26.375 %.
	near(t, "total tax", total, 105.50)

	res, _, _ = SimulateSell(lots, []SellRequest{{ISIN: "ETF", AmountEUR: 0.01}}, 0)
	near(t, "ETF loss after TFS", res[0].TaxableGain, -700)
	near(t, "ETF Teilfreistellung on a loss", res[0].Teilfreistellung, -300)
}

func TestMergeVorabpauschaleEvents_AfterSameDayTransactions(t *testing.T) {
	txns := []TaxTransaction{
		{Date: "2024-12-31", Year: 2024, Type: "sell"},
		{Date: "2025-01-02", Year: 2025, Type: "buy"},
	}
	out := MergeVorabpauschaleEvents(txns, []TaxTransaction{{Type: "vorabpauschale", Year: 2024}})
	if len(out) != 3 || out[1].Type != "vorabpauschale" || out[1].Date != "2024-12-31" {
		t.Errorf("merged order = %+v", out)
	}
}
