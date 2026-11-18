package handler

import (
	"math"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/lnsp/wealth/internal/analytics"
	"github.com/lnsp/wealth/internal/assets"
	db "github.com/lnsp/wealth/internal/database/generated"
)

var planNow = time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)

func planTxn(acct uuid.UUID, date string, typ string, amount float64, isin string, qty float64) db.ListTransactionsRow {
	d, _ := time.Parse("2006-01-02", date)
	row := db.ListTransactionsRow{
		ID: uuid.New(), AccountID: acct, Date: d, Type: typ,
		Amount: numericFromFloat(amount), Currency: "EUR",
	}
	if isin != "" {
		row.SecurityISIN = pgtype.Text{String: isin, Valid: true}
		row.Quantity = numericFromFloat(qty)
	}
	return row
}

// Root cause 2 / audit C3: the default contribution was Σ deposits / 12. It is
// now deposits − withdrawals (whatever their stored sign) + RSU vests, at
// investable scope, over the trailing 12 months.
func TestTrailingNetContribution_NetOfWithdrawalsPlusVests(t *testing.T) {
	checking, broker, otherBroker, house := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	types := map[uuid.UUID]string{checking: "checking", broker: "brokerage", otherBroker: "brokerage", house: "real_estate"}
	txns := []db.ListTransactionsRow{
		planTxn(checking, "2022-12-01", "deposit", 5000, "", 0), // history starts long ago
		planTxn(checking, "2025-11-10", "deposit", 9999, "", 0), // outside the window
		planTxn(checking, "2026-01-01", "deposit", 5200, "", 0),
		planTxn(checking, "2026-02-01", "deposit", 5200, "", 0),
		planTxn(checking, "2026-03-01", "deposit", 5200, "", 0),
		planTxn(checking, "2026-04-01", "deposit", 5200, "", 0),
		planTxn(checking, "2026-05-01", "deposit", 5200, "", 0),
		planTxn(checking, "2026-05-18", "withdrawal", -7328.97, "", 0),       // stored negative
		planTxn(broker, "2026-04-25", "transfer", 1200, "US02079K1079", 3.5), // RSU vest at FMV
		// Depot move between own brokers: matched pair, not a contribution.
		planTxn(otherBroker, "2026-06-01", "transfer_out", 4000, "IE00B4L5Y983", 40),
		planTxn(broker, "2026-06-03", "transfer", 4000, "IE00B4L5Y983", 40),
		// Own-account cash moves cancel.
		planTxn(checking, "2026-07-01", "cash_transfer_out", 1000, "", 0),
		planTxn(broker, "2026-07-01", "cash_transfer_in", 1000, "", 0),
		// Outside investable scope.
		planTxn(house, "2026-08-01", "deposit", 50000, "", 0),
	}
	got := trailingNetContribution(txns, types, planNow, nil, nil, assets.Holdings{})
	want := (26000 - 7328.97 + 1200) / 12
	if math.Abs(got.Monthly-want) > 1e-6 || got.MonthsCovered != 12 {
		t.Errorf("monthly %.4f over %.1f months, want %.4f over 12", got.Monthly, got.MonthsCovered, want)
	}
	if got.Deposits != 26000 || math.Abs(got.Withdrawals-7328.97) > 1e-9 || got.InKind != 1200 {
		t.Errorf("deposits %.2f withdrawals %.2f in-kind %.2f", got.Deposits, got.Withdrawals, got.InKind)
	}
	// The old gross-deposit figure would have been 26,000 / 12 = 2,166.67.
	if got.Monthly >= 26000.0/12 {
		t.Errorf("net contribution %.2f must be below gross deposits / 12", got.Monthly)
	}
}

// INFO: with less than 12 months of history, divide by the months covered.
func TestTrailingNetContribution_ShortHistory(t *testing.T) {
	acct := uuid.New()
	types := map[uuid.UUID]string{acct: "savings"}
	txns := []db.ListTransactionsRow{
		planTxn(acct, "2026-08-18", "deposit", 3000, "", 0),
		planTxn(acct, "2026-09-18", "deposit", 3000, "", 0),
		planTxn(acct, "2026-10-18", "deposit", 3000, "", 0),
	}
	got := trailingNetContribution(txns, types, planNow, nil, nil, assets.Holdings{})
	months := planNow.Truncate(24*time.Hour).Sub(time.Date(2026, 8, 18, 0, 0, 0, 0, time.UTC)).Hours() / 24 / 30.4375
	if math.Abs(got.MonthsCovered-months) > 1e-9 || math.Abs(got.Monthly-9000/months) > 1e-6 {
		t.Errorf("monthly %.2f over %.2f months, want %.2f over %.2f", got.Monthly, got.MonthsCovered, 9000/months, months)
	}
}

// INFO I2: the chart loop and planProjectFV must agree when contributions grow.
func TestPlanProjectFV_MatchesChartTiming(t *testing.T) {
	mr := planMonthlyRate(7)
	v := 10000.0
	for m := 1; m <= 60; m++ {
		v = v*(1+mr) + contribForMonth(500, 0.05, m)
	}
	if fv := planProjectFV(10000, 500, 7, 0.05, 60); math.Abs(fv-v) > 1e-6 {
		t.Errorf("planProjectFV %.4f, chart %.4f", fv, v)
	}
	// Months 1–12 pay the base amount, month 13 the first increase.
	if contribForMonth(500, 0.05, 12) != 500 || contribForMonth(500, 0.05, 13) != 525 {
		t.Error("contribution growth must start in month 13")
	}
}

// Audit C10: within a depot the oldest lot must be sold first (§ 20 Abs. 4
// S. 7 EStG). 100 shares bought 2015 @ 50 and 100 @ 120 in 2025, price 110,
// 11,000 € needed: the old loss-first sort sold the 2025 lot tax-free.
func TestPlanLiquidation_FIFOWithinDepot(t *testing.T) {
	acct := uuid.New().String()
	lots := []analytics.TaxLot{
		{AccountID: acct, ISIN: "IE00B4L5Y983", BuyDate: "2025-03-01", CostBasis: 12000, CurrentValue: 11000, UnrealizedPL: -1000, TFSRate: 0.3},
		{AccountID: acct, ISIN: "IE00B4L5Y983", BuyDate: "2015-03-01", CostBasis: 5000, CurrentValue: 11000, UnrealizedPL: 6000, TFSRate: 0.3},
	}
	rate := analytics.CapitalIncomeTaxRate(0)
	plan := planLiquidation(lots, map[string]string{"IE00B4L5Y983": "etf"}, 11000, rate, planNow)
	if len(plan.Steps) < 1 || plan.Steps[0].BuyDate != "2015-03-01" {
		t.Fatalf("first sale %+v, want the 2015 lot", plan.Steps)
	}
	if math.Abs(plan.Steps[0].TaxDue-1107.75) > 0.01 || plan.Steps[0].SellAmount != 11000 {
		t.Errorf("2015 lot: sell %.2f tax %.2f, want 11,000 / 1,107.75", plan.Steps[0].SellAmount, plan.Steps[0].TaxDue)
	}
	// The rest comes from the 2025 lot; its small loss offsets part of the gain.
	if len(plan.Steps) != 2 || plan.Steps[1].BuyDate != "2025-03-01" {
		t.Fatalf("steps %+v", plan.Steps)
	}
	lossTaxable := -1000 * (1107.75 / 11000) * 0.7
	wantTax := (4200 + lossTaxable) * rate
	if math.Abs(plan.TotalTax-wantTax) > 0.01 {
		t.Errorf("total tax %.2f, want %.2f", plan.TotalTax, wantTax)
	}
	if plan.TotalProceeds < 11000 {
		t.Errorf("proceeds %.2f below the cash needed", plan.TotalProceeds)
	}
}

// Across depots and ISINs the cheapest head lot (tax per euro raised) goes first.
func TestPlanLiquidation_RanksDepotsByTaxPerEuro(t *testing.T) {
	a, b := uuid.New().String(), uuid.New().String()
	lots := []analytics.TaxLot{
		{AccountID: a, ISIN: "AAA", BuyDate: "2010-01-01", CostBasis: 1000, CurrentValue: 10000, UnrealizedPL: 9000, TFSRate: 0.3},
		{AccountID: b, ISIN: "BBB", BuyDate: "2024-01-01", CostBasis: 9500, CurrentValue: 10000, UnrealizedPL: 500, TFSRate: 0.3},
	}
	plan := planLiquidation(lots, map[string]string{"AAA": "etf", "BBB": "etf"}, 5000, analytics.CapitalIncomeTaxRate(0), planNow)
	if len(plan.Steps) != 1 || plan.Steps[0].ISIN != "BBB" {
		t.Errorf("steps %+v, want one sale of BBB", plan.Steps)
	}
}

// Share losses only offset share gains (§ 20 Abs. 6 S. 4 EStG).
func TestPlanLiquidation_ShareLossDoesNotOffsetFundGain(t *testing.T) {
	a := uuid.New().String()
	lots := []analytics.TaxLot{
		{AccountID: a, ISIN: "STOCK", BuyDate: "2024-01-01", CostBasis: 6000, CurrentValue: 5000, UnrealizedPL: -1000},
		{AccountID: a, ISIN: "FUND", BuyDate: "2015-01-01", CostBasis: 2000, CurrentValue: 6000, UnrealizedPL: 4000, TFSRate: 0.3},
	}
	rate := analytics.CapitalIncomeTaxRate(0)
	plan := planLiquidation(lots, map[string]string{"STOCK": "stock", "FUND": "etf"}, 10000, rate, planNow)
	if len(plan.Steps) != 2 || plan.Steps[0].ISIN != "STOCK" {
		t.Fatalf("steps %+v, want the stock loss first, then the fund", plan.Steps)
	}
	if fundTax := plan.Steps[1].TaxDue; fundTax <= 0 || math.Abs(plan.TotalTax-fundTax) > 0.01 {
		t.Errorf("total tax %.2f, want the fund's full %.2f (the stock loss stays in the share pot)", plan.TotalTax, fundTax)
	}
}

func TestPlanLiquidation_PrivateSaleTaxFreeAfterOneYear(t *testing.T) {
	a := uuid.New().String()
	lots := []analytics.TaxLot{{AccountID: a, ISIN: "BTC", BuyDate: "2023-01-01", CostBasis: 1000, CurrentValue: 9000, UnrealizedPL: 8000}}
	plan := planLiquidation(lots, map[string]string{"BTC": "crypto"}, 5000, analytics.CapitalIncomeTaxRate(0), planNow)
	if plan.TotalTax != 0 || plan.Steps[0].SellAmount != 5000 {
		t.Errorf("tax %.2f, sell %.2f; § 23 gains after a year are tax-free", plan.TotalTax, plan.Steps[0].SellAmount)
	}
}

func pensionCtx() pensionContext {
	return pensionContext{CurrentAge: 35, NowYear: 2026}
}

// Audit C5: 30 pension points at 42.52 €, retiring 2058 → 100 % taxable,
// 12.35 % KVdR, tax from the § 32a tariff on the year's total.
func TestPensionsAt_StatutoryPension(t *testing.T) {
	src := []pensionSourceInput{{Type: "gesetzliche", Rentenpunkte: 30, StartAge: 67}}
	rows, gross, net := pensionsAt(src, 67, 67, false, pensionCtx())
	if len(rows) != 1 {
		t.Fatalf("rows %+v", rows)
	}
	wantGross := 30 * analytics.AktuellerRentenwert
	health := wantGross * 0.1235
	tax := analytics.PensionIncomeTax(wantGross*12, health*12, false, 0) / 12
	if math.Abs(gross-wantGross) > 1e-9 || math.Abs(net-(wantGross-health-tax)) > 1e-9 {
		t.Errorf("gross %.2f net %.2f, want %.2f / %.2f", gross, net, wantGross, wantGross-health-tax)
	}
	if rows[0].TaxableSharePct != 100 {
		t.Errorf("taxable share %.1f %%, want 100 %% for a 2058 start", rows[0].TaxableSharePct)
	}
}

// Audit C5(a): the Grundfreibetrag applies once to total income, not per source.
func TestPensionsAt_TaxOnTotalIncome(t *testing.T) {
	ctx := pensionCtx()
	stat := pensionSourceInput{Type: "gesetzliche", MonthlyGross: 900, StartAge: 67}
	bav := pensionSourceInput{Type: "bav", MonthlyGross: 900, StartAge: 67}
	r1, _, _ := pensionsAt([]pensionSourceInput{stat}, 67, 67, false, ctx)
	r2, _, _ := pensionsAt([]pensionSourceInput{bav}, 67, 67, false, ctx)
	if r1[0].MonthlyIncomeTx != 0 || r2[0].MonthlyIncomeTx != 0 {
		t.Fatalf("each source alone should be under the Grundfreibetrag: %v / %v", r1[0].MonthlyIncomeTx, r2[0].MonthlyIncomeTx)
	}
	both, _, _ := pensionsAt([]pensionSourceInput{stat, bav}, 67, 67, false, ctx)
	if both[0].MonthlyIncomeTx+both[1].MonthlyIncomeTx <= 0 {
		t.Error("together they must pay income tax")
	}
	// bAV pays the full KV rate above the Freibetrag; statutory pays half.
	if both[1].MonthlyHealth <= both[0].MonthlyHealth {
		t.Errorf("bAV health %.2f should exceed statutory %.2f", both[1].MonthlyHealth, both[0].MonthlyHealth)
	}
}

// Audit W2: start_age is honoured; the statutory pension can't start before 63.
func TestPensionsAt_StartAges(t *testing.T) {
	ctx := pensionCtx()
	src := []pensionSourceInput{
		{Type: "gesetzliche", Rentenpunkte: 30},
		{Type: "private", MonthlyGross: 300, StartAge: 70},
	}
	rows, _, _ := pensionsAt(src, 67, 67, false, ctx)
	if !rows[1].StartsLater || rows[1].MonthlyNet != 0 {
		t.Errorf("private pension from 70 must not count at 67: %+v", rows[1])
	}
	_, _, netAt60 := pensionsAt(src, 60, 60, true, ctx)
	if netAt60 != 0 {
		t.Errorf("at 60 nothing is paid yet, got %.2f", netAt60)
	}
	rows63, _, _ := pensionsAt(src, 63, 63, true, ctx)
	if math.Abs(rows63[0].MonthlyGross-30*analytics.AktuellerRentenwert*0.856) > 0.01 {
		t.Errorf("at 63 the pension is reduced by 14.4 %%: %.2f", rows63[0].MonthlyGross)
	}
}

// Audit C4: the portfolio withdrawal is deflated to today's euros.
func TestPortfolioAtRetirement_TodaysEuros(t *testing.T) {
	rate := analytics.CapitalIncomeTaxRate(0)
	d := portfolioAtRetirement(45073, 1557, 0.07, 0.02, 0.035, rate, 0.3, 1000, 32*12)
	defl := math.Pow(1.02, 32)
	if math.Abs(d.GrossMonthly-d.ValueNominal*0.035/12/defl) > 1e-6 {
		t.Errorf("gross %.2f, want nominal / %.3f", d.GrossMonthly, defl)
	}
	if d.NetMonthly >= d.GrossMonthly {
		t.Errorf("net %.2f should be below gross %.2f", d.NetMonthly, d.GrossMonthly)
	}
	t.Logf("portfolio at 67: %.0f nominal, %.0f today; withdrawal %.0f gross / %.0f net a month in today's €",
		d.ValueNominal, d.ValueReal, d.GrossMonthly, d.NetMonthly)
}

// Audit W2: the extra saving needed accounts for tax on its own gains.
func TestExtraSavingsToClose_ClosesTheGapAfterTax(t *testing.T) {
	rate := analytics.CapitalIncomeTaxRate(0)
	pmt := extraSavingsToClose(500, 0.07, 0.02, 0.035, rate, 0.3, 30*12)
	d := portfolioAtRetirement(0, pmt, 0.07, 0.02, 0.035, rate, 0.3, 0, 30*12)
	if math.Abs(d.NetMonthly-500) > 0.01 {
		t.Errorf("saving %.2f a month yields %.2f net, want 500", pmt, d.NetMonthly)
	}
}

// Audit W4: idle cash is costed at the spread over the cash rate, and
// rebalancing turnover is half the summed drift.
func TestOpportunityCostPieces(t *testing.T) {
	excess, cost := idleCashCost(50000, 18000, 7, 2.5)
	if excess != 32000 || math.Abs(cost-1440) > 1e-9 {
		t.Errorf("excess %.0f cost %.2f, want 32,000 / 1,440", excess, cost)
	}
	turnover := rebalanceTurnover(map[string]float64{"A": 60000, "B": 40000}, map[string]float64{"A": 50, "B": 50})
	if math.Abs(turnover*0.02-200) > 1e-9 {
		t.Errorf("+10/−10 on 100k costs %.2f, want 200", turnover*0.02)
	}
	// An ISIN held in two depots is one position.
	if got := rebalanceTurnover(map[string]float64{"A": 100000}, map[string]float64{"A": 100}); got != 0 {
		t.Errorf("on-target turnover %.2f, want 0", got)
	}
}

// Audit W5: "2028-11" is 24 months after November 2026, not 23.
func TestMonthsBetween(t *testing.T) {
	ev, _ := time.Parse("2006-01", "2028-11")
	if got := monthsBetween(planNow, ev); got != 24 {
		t.Errorf("monthsBetween = %d, want 24", got)
	}
	past, _ := time.Parse("2006-01", "2020-01")
	if got := monthsBetween(planNow, past); got != 0 {
		t.Errorf("past event offset %d, want 0", got)
	}
}

func TestSimulateLifeEvents_RecurringAndBasis(t *testing.T) {
	events := []lifeEventSim{{MonthOffset: 2, CashImpact: -1000, RecurringCost: 100, DurationMonths: 12}}
	vals, bases := simulateLifeEvents(10000, 0, 0, events, 30)
	if math.Abs(vals[30]-(10000-1000-1200)) > 1e-9 {
		t.Errorf("value after the event %.2f, want 7,800", vals[30])
	}
	if math.Abs(bases[30]-vals[30]) > 1e-9 {
		t.Errorf("with no gains basis %.2f should equal value %.2f", bases[30], vals[30])
	}
	// Baseline FIRE month on the event path equals simulateFIRE's month.
	p := germanFire(45073, 1557, 36000)
	bv, bb := simulateLifeEvents(p.CurrentValue, p.MonthlyContrib, planMonthlyRate(7), nil, 600)
	if got, want := fireMonthOnPath(p, bv, bb), simulateFIRE(p, planNow).Month; got != want {
		t.Errorf("life-event FIRE month %d, FIRE calculator %d", got, want)
	}
}

// A car bought with money from a savings account is a one-off move from
// liquid to illiquid wealth (prod, Nov 2026: 46,500 € withdrawn on 8 Nov,
// ID.3 at 44,990 € on 9 Nov). Linked, it must not depress the recurring
// contribution; unlinked, it still counts but is pointed out for linking.
func TestTrailingNetContribution_AssetPurchaseIsOneOff(t *testing.T) {
	savings := uuid.New()
	types := map[uuid.UUID]string{savings: "savings"}
	txns := []db.ListTransactionsRow{planTxn(savings, "2024-01-01", "deposit", 500, "", 0)} // history before the window
	for m := 1; m <= 12; m++ {
		txns = append(txns, planTxn(savings, time.Date(2025, time.Month(11+m-1), 25, 0, 0, 0, 0, time.UTC).Format("2006-01-02"), "deposit", 2000, "", 0))
	}
	payment := planTxn(savings, "2026-11-08", "withdrawal", 46500, "", 0)
	other := planTxn(savings, "2026-10-01", "withdrawal", 1200, "", 0)
	txns = append(txns, payment, other)
	car := assets.Asset{Asset: db.Asset{
		ID: uuid.New(), Name: "ID.3 Neo", Kind: "car",
		PurchaseDate:  time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC),
		PurchasePrice: numericFromFloat(44990),
	}}

	unlinked := trailingNetContribution(txns, types, planNow, nil, nil, assets.Holdings{Assets: []assets.Asset{car}})
	if want := (24000 - 46500 - 1200) / 12.0; math.Abs(unlinked.Monthly-want) > 1e-6 {
		t.Errorf("unlinked: monthly %.2f, want %.2f (an unlinked payment still counts)", unlinked.Monthly, want)
	}
	if len(unlinked.UnlinkedAssetPayments) != 1 || unlinked.UnlinkedAssetPayments[0].TransactionID != payment.ID.String() {
		t.Fatalf("unlinked: hints %+v, want the 46,500 € withdrawal", unlinked.UnlinkedAssetPayments)
	}

	car.Links = []db.AssetTransaction{{TransactionID: payment.ID, AssetID: car.ID, Purpose: "purchase"}}
	linked := trailingNetContribution(txns, types, planNow, nil, nil, assets.Holdings{Assets: []assets.Asset{car}})
	if want := (24000 - 1200) / 12.0; math.Abs(linked.Monthly-want) > 1e-6 {
		t.Errorf("linked: monthly %.2f, want %.2f (the purchase is a swap, not a recurring withdrawal)", linked.Monthly, want)
	}
	if linked.AssetPayments != 46500 || len(linked.UnlinkedAssetPayments) != 0 {
		t.Errorf("linked: asset payments %.2f, hints %d; want 46,500 and none", linked.AssetPayments, len(linked.UnlinkedAssetPayments))
	}
}
