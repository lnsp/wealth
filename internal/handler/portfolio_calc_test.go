package handler

// Regression tests for the 2026-11-18 calculation audit, Portfolio page.
// Each test drives the function the handler uses with a small fixture whose
// correct answer is worked out by hand in the comments.

import (
	"math"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/lnsp/wealth/internal/analytics"
	db "github.com/lnsp/wealth/internal/database/generated"
	"github.com/lnsp/wealth/internal/market"
)

func ymd(s string) time.Time {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(err)
	}
	return t
}

func near(a, b, tol float64) bool { return math.Abs(a-b) <= tol }

type txSpec struct {
	acct     uuid.UUID
	date     string
	typ      string
	isin     string
	qty      float64
	amount   float64
	fee, tax float64
	currency string
}

func mkTxns(specs ...txSpec) []db.ListTransactionsRow {
	out := make([]db.ListTransactionsRow, 0, len(specs))
	for _, s := range specs {
		cur := s.currency
		if cur == "" {
			cur = "EUR"
		}
		row := db.ListTransactionsRow{
			ID:        uuid.New(),
			AccountID: s.acct,
			Date:      ymd(s.date),
			Type:      s.typ,
			Amount:    numericFromFloat(s.amount),
			Fee:       numericFromFloat(s.fee),
			Tax:       numericFromFloat(s.tax),
			Currency:  cur,
		}
		if s.isin != "" {
			row.SecurityISIN = pgtype.Text{String: s.isin, Valid: true}
		}
		if s.qty != 0 {
			row.Quantity = numericFromFloat(s.qty)
		}
		out = append(out, row)
	}
	return out
}

// usdFX: 1 EUR = 1.10 USD from 2024-01-01, 1.05 from 2025-01-01; the latest
// rate is 1.15.
func usdFX() *fxTable {
	return newFXTable([]market.HistoricalFXRate{
		{Date: ymd("2024-01-01"), Currency: "USD", Rate: 1.10},
		{Date: ymd("2025-01-01"), Currency: "USD", Rate: 1.05},
	}, map[string]float64{"EUR": 1, "USD": 1.15})
}

// KNOWN: cost basis must use each purchase date's ECB rate, not today's.
func TestReplayPositionCosts_PurchaseDateFX(t *testing.T) {
	acct := uuid.New()
	costs := replayPositionCosts(mkTxns(
		// RSU vest: 10 × 100 USD at 1.10 → 909.09 €
		txSpec{acct: acct, date: "2024-03-01", typ: "transfer", isin: "US1", qty: 10, amount: 1000, currency: "USD"},
		// buy 10 × 120 USD at 1.05 → 1,142.86 €
		txSpec{acct: acct, date: "2025-02-01", typ: "buy", isin: "US1", qty: 10, amount: 1200, currency: "USD"},
		// sell 10: moving average keeps half of 2,051.95 € = 1,025.97 €
		txSpec{acct: acct, date: "2025-06-01", typ: "sell", isin: "US1", qty: 10, amount: 1500, currency: "USD"},
		// a $50 dividend at 1.05 → 47.62 €
		txSpec{acct: acct, date: "2025-07-01", typ: "dividend", isin: "US1", amount: 50, tax: 7.5, currency: "USD"},
	), usdFX())
	pc := costs[positionKey{AccountID: acct, ISIN: "US1"}]
	if pc == nil {
		t.Fatal("no position")
	}
	if !near(pc.Qty, 10, 1e-9) {
		t.Errorf("qty = %v, want 10", pc.Qty)
	}
	want := (1000/1.10 + 1200/1.05) / 2
	if !near(pc.CostEUR, want, 0.01) {
		t.Errorf("CostEUR = %.2f, want %.2f (latest-FX would be %.2f)", pc.CostEUR, want, 1100/1.15)
	}
	// FIFO keeps the 2025 lot.
	if !near(pc.FIFOCostEUR(), 1200/1.05, 0.01) {
		t.Errorf("FIFO cost = %.2f, want %.2f", pc.FIFOCostEUR(), 1200/1.05)
	}
	avg, cur, ok := pc.AvgCostNative()
	if !ok || cur != "USD" || !near(avg, 110, 1e-9) {
		t.Errorf("AvgCostNative = %v %s %v, want 110 USD", avg, cur, ok)
	}
	if !near(pc.DividendsEUR, 50/1.05, 0.01) || !near(pc.DividendsEUR-pc.DividendTaxEUR, 42.5/1.05, 0.01) {
		t.Errorf("dividends gross/net = %.2f/%.2f", pc.DividendsEUR, pc.DividendsEUR-pc.DividendTaxEUR)
	}
}

// Average cost is reported in the currency it was paid: a USD-listed share
// bought in EUR has an EUR average cost, never "EUR amount labelled USD".
func TestReplayPositionCosts_PaidCurrency(t *testing.T) {
	acct := uuid.New()
	costs := replayPositionCosts(mkTxns(
		txSpec{acct: acct, date: "2025-02-01", typ: "buy", isin: "US1", qty: 2, amount: 300, fee: 1, currency: "EUR"},
	), usdFX())
	avg, cur, ok := costs[positionKey{acct, "US1"}].AvgCostNative()
	if !ok || cur != "EUR" || !near(avg, 150.5, 1e-9) {
		t.Errorf("EUR purchase: avg %v %s %v, want 150.50 EUR", avg, cur, ok)
	}
	mixed := replayPositionCosts(mkTxns(
		txSpec{acct: acct, date: "2025-02-01", typ: "buy", isin: "US1", qty: 2, amount: 300, currency: "EUR"},
		txSpec{acct: acct, date: "2025-03-01", typ: "buy", isin: "US1", qty: 2, amount: 330, currency: "USD"},
	), usdFX())
	if _, _, ok := mixed[positionKey{acct, "US1"}].AvgCostNative(); ok {
		t.Error("mixed EUR/USD purchases must not report a single paid currency")
	}
}

// C2 toy case: buy 100 @ 10, sell 50 @ 20, 50 left worth 1,000. The FIFO gain
// is 1,000 − 500 = 500; the old "value − (buys − sell proceeds)" said 1,000.
func TestSwitchCompare_GainFromFIFOLots(t *testing.T) {
	acct := uuid.New()
	pc := replayPositionCosts(mkTxns(
		txSpec{acct: acct, date: "2025-01-02", typ: "buy", isin: "X", qty: 100, amount: 1000},
		txSpec{acct: acct, date: "2025-06-02", typ: "sell", isin: "X", qty: 50, amount: 1000},
	), usdFX())[positionKey{acct, "X"}]
	value := 50 * 20.0
	if gain := value - pc.FIFOCostEUR(); !near(gain, 500, 1e-6) {
		t.Errorf("gain = %.2f, want 500", gain)
	}
	// 500 gain on a share, nothing else this year: 1,000 allowance covers it.
	if r := switchTax(switchTaxInput{Gain: 500, IsStock: true, Position: yearTaxPosition{AllowanceLeft: 1000}, Rate: 0.26375}); r.Tax != 0 {
		t.Errorf("tax = %.2f, want 0", r.Tax)
	}
}

// C2 seed: an RSU-only position (transfers, no buys) underwater by 864 € must
// show its real value and no tax — not "invested −75,960, tax 19,771".
func TestSwitchCompare_RSUVestsAreLots(t *testing.T) {
	acct := uuid.New()
	pc := replayPositionCosts(mkTxns(
		txSpec{acct: acct, date: "2025-01-10", typ: "transfer", isin: "G", qty: 10, amount: 3150, currency: "USD"},
		txSpec{acct: acct, date: "2025-02-10", typ: "transfer", isin: "G", qty: 10, amount: 3150, currency: "USD"},
	), usdFX())[positionKey{acct, "G"}]
	cost := pc.FIFOCostEUR() // 6,300 / 1.05 = 6,000 €
	if !near(cost, 6000, 0.01) {
		t.Fatalf("FIFO cost = %.2f, want 6000", cost)
	}
	gain := 5136.0 - cost
	r := switchTax(switchTaxInput{Gain: gain, IsStock: true, Position: yearTaxPosition{AllowanceLeft: 1000, Allowance: 1000}, Rate: 0.26375})
	if r.Tax != 0 || !near(r.LossAfterTFS, 864, 0.01) {
		t.Errorf("tax %.2f loss %.2f, want 0 and 864", r.Tax, r.LossAfterTFS)
	}
}

func TestSwitchAlternativeValue_ShiftsLotsBeforePriceHistory(t *testing.T) {
	from := ymd("2026-05-15")
	cur := func(d time.Time) float64 { return 300 }
	alt := func(d time.Time) float64 {
		if d.Before(from) {
			return 0
		}
		if d.Equal(from) {
			return 100
		}
		return 120
	}
	lots := []switchLot{
		{Date: ymd("2026-01-25"), Qty: 10, CostEUR: 2800}, // shifted: 10 × 300 = 3,000 € → 30 alt units
		{Date: ymd("2026-06-01"), Qty: 5, CostEUR: 1200},  // 1,200 € / 120 = 10 alt units
	}
	v, shifted, start := switchAlternativeValue(lots, cur, alt, from, 130)
	if shifted != 1 || !start.Equal(from) || !near(v, (30+10)*130, 1e-6) {
		t.Errorf("value %.2f shifted %d from %s, want 5200, 1, 2026-05-15", v, shifted, start.Format("2006-01-02"))
	}
}

// W11: fund losses count after Teilfreistellung; share losses only offset
// share gains; realised losses this year shrink the taxable gain.
func TestSwitchTax_GermanLossRules(t *testing.T) {
	rate := analytics.CapitalIncomeTaxRate(0)
	// Equity ETF at −1,000: only 700 € counts; it offsets 700 of the 2,000 €
	// already taxed above the allowance this year.
	r := switchTax(switchTaxInput{Gain: -1000, TFS: 0.30, Position: yearTaxPosition{IncomeAboveAllowance: 2000}, Rate: rate})
	if !near(r.LossAfterTFS, 700, 1e-9) || !near(r.TaxSavedNow, 700*rate, 1e-9) {
		t.Errorf("fund loss: %+v", r)
	}
	// A share loss with no share gains this year saves nothing now.
	r = switchTax(switchTaxInput{Gain: -1000, IsStock: true, Position: yearTaxPosition{IncomeAboveAllowance: 2000, StockIncome: 0}, Rate: rate})
	if r.TaxSavedNow != 0 || !near(r.LossAfterTFS, 1000, 1e-9) {
		t.Errorf("share loss without share gains: %+v", r)
	}
	// A 1,000 € share gain with 400 € unused share losses and 300 € unused
	// general losses: 300 taxable after the losses, inside the allowance.
	r = switchTax(switchTaxInput{Gain: 1000, IsStock: true, Position: yearTaxPosition{StockLossLeft: 400, GeneralLossLeft: 300, AllowanceLeft: 200}, Rate: rate})
	if !near(r.LossOffset, 700, 1e-9) || !near(r.AllowanceUsed, 200, 1e-9) || !near(r.Tax, 100*rate, 1e-9) {
		t.Errorf("gain with loss pots: %+v", r)
	}
	// A fund gain cannot use share losses.
	r = switchTax(switchTaxInput{Gain: 1000, TFS: 0.30, Position: yearTaxPosition{StockLossLeft: 400}, Rate: rate})
	if r.LossOffset != 0 || !near(r.Tax, 700*rate, 1e-9) {
		t.Errorf("fund gain vs share losses: %+v", r)
	}
	// Church tax 9 % → 27.995 %.
	if !near(analytics.CapitalIncomeTaxRate(0.09), 0.279951, 1e-5) {
		t.Errorf("church-tax rate %.6f", analytics.CapitalIncomeTaxRate(0.09))
	}
}

// W11: remaining allowance subtracts realised losses (gain 900, loss 600 →
// 300 used, not 900) and uses the year's allowance.
func TestComputeYearTaxPosition_NetsLossesAndPots(t *testing.T) {
	acct := uuid.New()
	secs := map[string]db.Security{
		"ETF": {ISIN: "ETF", AssetClass: "bond"}, // 0 % Teilfreistellung keeps the arithmetic plain
		"STK": {ISIN: "STK", AssetClass: "stock"},
	}
	txns := mkTxns(
		txSpec{acct: acct, date: "2026-01-02", typ: "buy", isin: "ETF", qty: 10, amount: 1000},
		txSpec{acct: acct, date: "2026-02-02", typ: "buy", isin: "STK", qty: 10, amount: 1000},
		txSpec{acct: acct, date: "2026-03-02", typ: "sell", isin: "ETF", qty: 10, amount: 1900}, // +900
		txSpec{acct: acct, date: "2026-04-02", typ: "sell", isin: "STK", qty: 10, amount: 400},  // −600 share loss
	)
	pos := computeYearTaxPosition(txns, secs, usdFX(), 2026, false)
	// The share loss cannot offset the fund gain: 900 used, 600 share loss left.
	if !near(pos.AllowanceLeft, 100, 1e-6) || !near(pos.StockLossLeft, 600, 1e-6) {
		t.Errorf("share loss vs fund gain: %+v", pos)
	}
	secs["STK"] = db.Security{ISIN: "STK", AssetClass: "bond"}
	pos = computeYearTaxPosition(txns, secs, usdFX(), 2026, true)
	// Both general: 900 − 600 = 300 used of the joint 2,000.
	if !near(pos.AllowanceLeft, 1700, 1e-6) || pos.Allowance != 2000 {
		t.Errorf("general losses, joint: %+v", pos)
	}
	if pos2022 := computeYearTaxPosition(nil, secs, usdFX(), 2022, false); pos2022.Allowance != 801 {
		t.Errorf("2022 allowance = %v, want 801", pos2022.Allowance)
	}
}

// C3: the monthly total is the current run rate of plans still running.
func TestAnalyseSavingsPlan_RunRateAndLumpSums(t *testing.T) {
	now := ymd("2026-11-18")
	var stopped, running []planBuy
	for m := 1; m <= 12; m++ { // plan A: 200 €/month through 2023, then stopped
		stopped = append(stopped, planBuy{Date: time.Date(2023, time.Month(m), 2, 0, 0, 0, 0, time.UTC), Amount: 200, Qty: 2})
	}
	for m := 1; m <= 11; m++ { // plan B: 300 €/month since January 2026
		running = append(running, planBuy{Date: time.Date(2026, time.Month(m), 2, 0, 0, 0, 0, time.UTC), Amount: 300, Qty: 3})
	}
	running = append(running, planBuy{Date: ymd("2026-06-15"), Amount: 6000, Qty: 60}) // one-off

	a, ok := analyseSavingsPlan(stopped, now)
	if !ok || a.Active {
		t.Errorf("plan A: ok=%v active=%v, want detected and inactive", ok, a.Active)
	}
	b, ok := analyseSavingsPlan(running, now)
	if !ok || !b.Active {
		t.Fatalf("plan B: ok=%v active=%v", ok, b.Active)
	}
	if b.MonthlyAmount != 300 || !near(b.Trailing12mMonthly, 275, 1e-9) {
		t.Errorf("plan B: run rate %.2f (want 300), T12M/12 %.2f (want 275)", b.MonthlyAmount, b.Trailing12mMonthly)
	}
	if b.LumpSums != 1 || b.LumpSumAmount != 6000 || b.Invested != 3300 {
		t.Errorf("plan B lump sums: %d / %.0f, invested %.0f", b.LumpSums, b.LumpSumAmount, b.Invested)
	}
	if b.MonthsSpan != 11 || b.MonthsWithPurchase != 11 {
		t.Errorf("plan B months: span %d, with purchase %d", b.MonthsSpan, b.MonthsWithPurchase)
	}
}

// C4: weights are market value at each month-end, every month is present
// and dated at its end. Two positions bought for 1,000 € each, one doubling,
// are 66.7 / 33.3 — cost weights would say 50 / 50.
func TestAllocationHistory_MarketValueAtMonthEnd(t *testing.T) {
	acct := uuid.New()
	accts := []db.Account{{ID: acct, Type: "brokerage", IsActive: true}}
	txns := mkTxns(
		txSpec{acct: acct, date: "2026-01-10", typ: "buy", isin: "A", qty: 10, amount: 1000},
		txSpec{acct: acct, date: "2026-01-10", typ: "buy", isin: "B", qty: 10, amount: 1000},
	)
	prices := priceBook{
		"A": {{date: ymd("2026-01-31"), close: 100, currency: "EUR"}, {date: ymd("2026-02-27"), close: 200, currency: "EUR"}},
		"B": {{date: ymd("2026-01-31"), close: 100, currency: "EUR"}},
	}
	series := buildValuationSeries(valuationRequest{
		Txns: txns, Accounts: accts, Prices: prices, FX: usdFX(), End: ymd("2026-04-10"), ByISIN: true, Emit: isMonthEnd,
	})
	var dates []string
	for _, p := range series {
		dates = append(dates, p.Date.Format("2006-01-02"))
	}
	want := []string{"2026-01-31", "2026-02-28", "2026-03-31", "2026-04-10"}
	if len(dates) != len(want) {
		t.Fatalf("dates %v, want %v", dates, want)
	}
	for i := range want {
		if dates[i] != want[i] {
			t.Errorf("point %d dated %s, want %s", i, dates[i], want[i])
		}
	}
	w := allocationWeights(series[1].ByISIN, func(s string) string { return s })
	if !near(w["A"], 66.7, 1e-9) || !near(w["B"], 33.3, 1e-9) {
		t.Errorf("Feb weights %v, want A 66.7 B 33.3", w)
	}
}

// C5: untargeted holdings are sold; trades net to zero.
func TestPlanRebalance_SellsUntargetedAndNets(t *testing.T) {
	holdings := map[string]rebalanceHolding{
		"A": {ISIN: "A", Value: 6000, Price: 100},
		"B": {ISIN: "B", Value: 3000, Price: 100},
		"C": {ISIN: "C", Value: 1000, Price: 100},
	}
	plan := planRebalance(holdings, map[string]float64{"A": 60, "B": 40}, 10000, 0)
	got := map[string]rebalanceTrade{}
	for _, tr := range plan.Trades {
		got[tr.ISIN] = tr
	}
	if len(plan.Trades) != 2 || got["B"].Action != "buy" || got["B"].Amount != 1000 || got["C"].Action != "sell" || got["C"].Amount != 1000 {
		t.Errorf("trades %+v, want buy B 1000 and sell C 1000", plan.Trades)
	}
	if plan.Unallocated != 0 {
		t.Errorf("net cash %.2f, want 0", plan.Unallocated)
	}
}

func TestPlanRebalance_NormalisesTargets(t *testing.T) {
	holdings := map[string]rebalanceHolding{
		"A": {ISIN: "A", Value: 6000, Price: 100},
		"B": {ISIN: "B", Value: 4000, Price: 100},
	}
	// 60 + 20 = 80 % → scaled to 75 / 25: buy A 1,500, sell B 1,500.
	plan := planRebalance(holdings, map[string]float64{"A": 60, "B": 20}, 10000, 0)
	if !plan.TargetsNormalised || plan.TargetSum != 80 {
		t.Errorf("normalised=%v sum=%v", plan.TargetsNormalised, plan.TargetSum)
	}
	buys, sells := 0.0, 0.0
	for _, tr := range plan.Trades {
		if tr.Action == "buy" {
			buys += tr.Amount
		} else {
			sells += tr.Amount
		}
	}
	if buys != 1500 || sells != 1500 {
		t.Errorf("buys %.0f sells %.0f, want 1500 / 1500 (%+v)", buys, sells, plan.Trades)
	}
}

func TestPlanRebalance_SmallDepositNeverBuysZero(t *testing.T) {
	holdings := map[string]rebalanceHolding{
		"X": {ISIN: "X", Value: 5000, Price: 160},
		"Y": {ISIN: "Y", Value: 5000, Price: 106},
	}
	plan := planRebalance(holdings, map[string]float64{"X": 50, "Y": 50}, 10000, 200)
	placed := 0.0
	for _, tr := range plan.Trades {
		if tr.Action != "buy" || tr.Amount < rebalanceMinTrade {
			t.Errorf("bad trade %+v", tr)
		}
		placed += tr.Amount
	}
	if !near(placed+plan.Unallocated, 200, 0.011) || len(plan.Trades) != 2 {
		t.Errorf("placed %.2f + unallocated %.2f, want 200 over 2 trades", placed, plan.Unallocated)
	}
	// 1,000 deposit across one underweight fund: the whole deposit is placed.
	plan = planRebalance(map[string]rebalanceHolding{"X": {Value: 6000, Price: 160}, "Y": {Value: 4000, Price: 106}}, map[string]float64{"X": 50, "Y": 50}, 10000, 1000)
	if len(plan.Trades) != 1 || plan.Trades[0].Amount != 1000 || plan.Unallocated != 0 {
		t.Errorf("deposit 1000: %+v unallocated %.2f", plan.Trades, plan.Unallocated)
	}
}

// C6 / KNOWN TWR: an RSU account that sells everything, wires the cash out
// and receives the next vest weeks later has no −100 % sub-period; the
// chained TWR equals the price path while shares are held.
func TestValuationSeries_TWRThroughLiquidation(t *testing.T) {
	acct := uuid.New()
	accts := []db.Account{{ID: acct, Type: "brokerage", IsActive: true}}
	txns := mkTxns(
		txSpec{acct: acct, date: "2026-01-05", typ: "transfer", isin: "G", qty: 10, amount: 1000}, // vest @ 100
		txSpec{acct: acct, date: "2026-01-20", typ: "sell", isin: "G", qty: 10, amount: 1100},     // @ 110: +10 %
		txSpec{acct: acct, date: "2026-01-20", typ: "withdrawal", amount: 1100},
		txSpec{acct: acct, date: "2026-02-05", typ: "transfer", isin: "G", qty: 10, amount: 1200}, // vest @ 120
	)
	prices := priceBook{"G": {{date: ymd("2026-02-20"), close: 132, currency: "EUR"}}} // +10 % again
	series := buildValuationSeries(valuationRequest{Txns: txns, Accounts: accts, Prices: prices, FX: usdFX(), End: ymd("2026-02-28")})
	chain := analytics.ChainTWR(seriesTWRSteps(series))
	if !chain.OK || len(chain.Resets) != 0 || !near(chain.Return, 1.1*1.1-1, 1e-9) {
		t.Errorf("TWR %.6f ok=%v resets=%v, want 0.21", chain.Return, chain.OK, chain.Resets)
	}
	in, out := 0.0, 0.0
	for _, p := range series {
		in += p.InKindIn
		out += p.CashOut
	}
	if in != 2200 || out != 1100 {
		t.Errorf("flows in %.0f out %.0f, want 2200 / 1100", in, out)
	}
	if last := series[len(series)-1]; last.Value != 1320 {
		t.Errorf("last value %.2f, want 1320", last.Value)
	}
}

// W1: the scope is investment accounts only; checking cash is not invested.
func TestIsInvestmentAccount(t *testing.T) {
	for typ, want := range map[string]bool{"brokerage": true, "crypto": true, "pension": true, "precious_metals": true, "checking": false, "savings": false} {
		if got := isInvestmentAccount(db.Account{Type: typ, IsActive: true}); got != want {
			t.Errorf("%s: %v, want %v", typ, got, want)
		}
	}
	if isInvestmentAccount(db.Account{Type: "brokerage", IsActive: false}) {
		t.Error("inactive account must be out of scope")
	}
}

// C7 / W4 / W5 / INFO: dividends in EUR, growth null without a base,
// calendar per month total scaled to today's quantity, zero months filled,
// cumulative is the running net sum.
func TestSummarizeDividends(t *testing.T) {
	now := ymd("2026-11-18")
	pays := []dividendPayment{
		// Two payouts in the same month (e.g. a correction): the month total
		// is 30 € on 10 units = 3 €/unit.
		{Date: ymd("2026-03-10"), ISIN: "D", Gross: 20, Net: 15, QtyHeld: 10},
		{Date: ymd("2026-03-20"), ISIN: "D", Gross: 10, Net: 7, QtyHeld: 10},
		{Date: ymd("2026-05-10"), ISIN: "D", Gross: -5, Net: -5, QtyHeld: 10}, // reversal
	}
	s := summarizeDividends(pays, map[string]float64{"D": 20}, map[string]string{"D": "Div Fund"}, now)
	if s.Growth != nil {
		t.Errorf("growth = %v, want nil (nothing paid in the prior 12 months)", *s.Growth)
	}
	if len(s.Monthly) != 9 || s.Monthly[1].Amount != 0 || s.Monthly[0].Month != "2026-03" || s.Monthly[8].Month != "2026-11" {
		t.Errorf("monthly %+v, want 2026-03 … 2026-11 with zero months", s.Monthly)
	}
	if c := s.Cumulative[len(s.Cumulative)-1].Cumulative; c != 25 {
		t.Errorf("cumulative %.2f, want 25 (reversal included)", c)
	}
	var march *dividendCalendarEntry
	for i := range s.Calendar {
		if s.Calendar[i].Month == "2027-03" {
			march = &s.Calendar[i]
		}
	}
	if march == nil || march.Expected != 60 {
		t.Errorf("calendar March = %+v, want 3 €/unit × 20 units = 60", march)
	}
	if s.Trailing12mHeld != 25 || s.TotalNet != 17 {
		t.Errorf("T12M held %.2f net total %.2f", s.Trailing12mHeld, s.TotalNet)
	}
}

// W6: one drift definition; health diversification counts funds; composite
// rounds; income never scores below "no payouts".
func TestHealthScoreAndDrift(t *testing.T) {
	vals := map[string]float64{"A": 6000, "B": 3000, "C": 1000}
	if d := allocationMaxDrift(vals, map[string]float64{"A": 60, "B": 40}); !near(d, -10, 1e-9) && !near(d, 10, 1e-9) {
		t.Errorf("max drift %.2f, want ±10 (C untargeted at 10 %%)", d)
	}
	score, subs := computeHealthScore(healthInputs{
		ValueByISIN: map[string]float64{"IE00B3RBWM25": 10000},
		AssetClass:  map[string]string{"IE00B3RBWM25": "etf"},
	})
	if subs[0].Score != 100 || subs[0].Status != "good" {
		t.Errorf("one all-world ETF: diversification %+v", subs[0])
	}
	// (100·25 + 90·15 + 50·20 + 75·25 + 70·15) / 100 = 77.75 → 78
	if score != 78 {
		t.Errorf("composite %d, want 78 (rounded, not truncated)", score)
	}
	_, single := computeHealthScore(healthInputs{ValueByISIN: map[string]float64{"G": 1}, AssetClass: map[string]string{"G": "stock"}})
	if single[0].Status != "poor" {
		t.Errorf("one share: %+v", single[0])
	}
	prev := -1
	for _, months := range []int{0, 1, 5, 6, 12, 24} {
		_, s := computeHealthScore(healthInputs{ValueByISIN: vals, DividendMonths: months})
		if s[4].Score < prev {
			t.Errorf("income score drops at %d months: %d < %d", months, s[4].Score, prev)
		}
		prev = s[4].Score
	}
}

// W12: the listed rows add up exactly to "Change since".
func TestTimeMachineAttribution_RowsSum(t *testing.T) {
	r := timeMachineAttribution(timeMachineParts{
		HistoricalNW: 67318.4, CurrentNW: 159527.3, Contributions: 100796.4, Dividends: 12.6, Interest: 30.4,
		Fees: 4.6, Taxes: 9.5, Assets: -787.2, PurchaseCosts: 0, LoanInterest: 13216.4,
	})
	sum := r.Contributions + r.MarketReturn + r.Dividends + r.Interest - r.Fees - r.Taxes + r.Assets + r.PurchaseCosts + r.LoanInterest
	if sum != r.NetWorthChange || r.NetWorthChange != 159527-67318 {
		t.Errorf("rows sum %.0f, change %.0f", sum, r.NetWorthChange)
	}
	if math.Signbit(r.PurchaseCosts) {
		t.Error("zero purchase costs must not serialise as -0")
	}
}
