package handler

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/lnsp/wealth/internal/analytics"
	db "github.com/lnsp/wealth/internal/database/generated"
)

// Regression tests for the 2026-11-18 calculation audit, Analysis page
// Risk and Performance tabs (docs/calc-audit-2026-11-18.md).

func rpDay(s string) time.Time {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(err)
	}
	return t
}

func rpSnap(date string, cash, invest, physical, debt float64) db.NetWorthSnapshot {
	return db.NetWorthSnapshot{
		Date:                rpDay(date),
		Total:               numericFromFloat(cash + invest + physical - debt),
		CashComponent:       numericFromFloat(cash),
		InvestmentComponent: numericFromFloat(invest),
		PhysicalComponent:   numericFromFloat(physical),
		DebtComponent:       numericFromFloat(debt),
	}
}

func rpTxn(date, typ string, amount float64, isin string) db.ListTransactionsRow {
	t := db.ListTransactionsRow{
		ID:        uuid.New(),
		AccountID: uuid.Nil,
		Date:      rpDay(date),
		Type:      typ,
		Amount:    numericFromFloat(amount),
		Currency:  "EUR",
	}
	if isin != "" {
		t.SecurityISIN = pgtype.Text{String: isin, Valid: true}
		t.Quantity = numericFromFloat(1)
	}
	return t
}

// F2: buying the apartment (+320k property, +260k mortgage) and the car
// showed as +1,400 % and +40.7 % "returns". The risk series is now cash plus
// securities, so neither appears.
func TestInvestableRiskSeries_ExcludesPropertyAndLoans(t *testing.T) {
	// Newest first, as ListNetWorthSnapshots returns them.
	snaps := []db.NetWorthSnapshot{
		rpSnap("2025-04-02", 0, 4300, 320000, 260000),
		rpSnap("2025-04-01", 0, 4290, 320000, 260000),
		rpSnap("2025-03-31", 0, 4285, 0, 0),
		rpSnap("2016-08-25", 0, 0, 0, 0),
	}
	pts := investableRiskSeries(snaps)
	if len(pts) != 3 {
		t.Fatalf("got %d points, want 3 (leading zero snapshot dropped)", len(pts))
	}
	if !pts[0].Date.Equal(rpDay("2025-03-31")) || pts[1].Value != 4290 || pts[2].Value != 4300 {
		t.Fatalf("series = %+v", pts)
	}
	adj := analytics.BuildCashflowAdjustedSeries(pts, nil)
	m := analytics.ComputeRiskMetrics(adj, 0, pts[2].Value)
	if m.AnnualizedVolatility > 10 {
		t.Errorf("volatility = %.2f %%: the property purchase leaked into the risk series", m.AnnualizedVolatility)
	}
}

// F9 + F2: amounts are magnitudes with the sign taken from the type (a
// withdrawal stored as −1,300 is an outflow, not a deposit), vests count at
// market value, an unmatched outgoing depot transfer is an outflow, and the
// investable scope keeps asset/loan payments that the net-worth scope skips.
func TestExternalRiskFlows_SignsAndScope(t *testing.T) {
	assetPayment := rpTxn("2026-08-01", "withdrawal", 44900, "")
	txns := []db.ListTransactionsRow{
		rpTxn("2026-01-01", "deposit", 5200, ""),
		rpTxn("2026-01-03", "withdrawal", -1300, ""),
		rpTxn("2026-01-25", "transfer", 2923, "US02079K1079"),
		rpTxn("2026-02-01", "transfer_out", 500, "IE00B4L5Y983"),
		rpTxn("2026-02-02", "buy", 1000, "IE00B4L5Y983"),
		assetPayment,
	}
	want := map[string]float64{
		"2026-01-01": 5200, "2026-01-03": -1300, "2026-01-25": 2923, "2026-02-01": -500, "2026-08-01": -44900,
	}
	flows := externalRiskFlows(txns, nil, nil, map[string]float64{"EUR": 1}, nil)
	if len(flows) != len(want) {
		t.Fatalf("got %d flows %+v, want %d", len(flows), flows, len(want))
	}
	for _, f := range flows {
		if w, ok := want[f.Date.Format("2006-01-02")]; !ok || math.Abs(f.Amount-w) > 1e-6 {
			t.Errorf("flow %s = %.2f, want %.2f", f.Date.Format("2006-01-02"), f.Amount, w)
		}
	}
	netWorthScope := externalRiskFlows(txns, nil, nil, map[string]float64{"EUR": 1}, map[uuid.UUID]bool{assetPayment.ID: true})
	if len(netWorthScope) != len(want)-1 {
		t.Errorf("net-worth scope should skip the asset payment, got %d flows", len(netWorthScope))
	}
}

// F3: ECB rates are units per EUR; "1 € = 1,0989 USD" must not be inverted.
func TestFxRatePoints_NotInverted(t *testing.T) {
	rows := []db.ListExchangeRateHistoryRow{{Date: rpDay("2026-11-17"), Rate: numericFromFloat(1.0989)}}
	pts := fxRatePoints(rows)
	if len(pts) != 1 || pts[0].Rate != 1.0989 {
		t.Fatalf("points = %+v, want rate 1.0989", pts)
	}
}

// I5: FX currencies come from what is held.
func TestForeignCurrencyList(t *testing.T) {
	got := foreignCurrencyList([]string{"EUR", "USD", "GBp", "usd", "", "CHF", "GBP"})
	if strings.Join(got, ",") != "CHF,GBP,USD" {
		t.Errorf("currencies = %v, want CHF,GBP,USD", got)
	}
}

// F5: the price index is chained month by month (no jump at a year
// boundary) and matches the published level ratio; F4: "Lost to inflation"
// is no longer 0 by construction; I7: leading zeros are not plotted.
func TestComputeInflation_ChainedIndexAndErosion(t *testing.T) {
	var raw []analytics.DailyValuation
	for d := rpDay("2022-12-01"); !d.After(rpDay("2026-11-18")); d = d.AddDate(0, 0, 1) {
		raw = append(raw, analytics.DailyValuation{Date: d, Value: 10000})
	}
	s := computeInflation(raw, raw, germanVPI)
	cpi := map[string]float64{}
	for _, p := range s.History {
		cpi[p.Date[:7]] = p.CpiIndex
	}
	// Dec 2023 → Jan 2024 used to jump from 84.8 to 93.9.
	if jump := cpi["2024-01"] / cpi["2023-12"]; jump < 0.99 || jump > 1.01 {
		t.Errorf("Dec 2023 → Jan 2024 index step = %.4f, want a monthly step", jump)
	}
	// Destatis: Oct 2026 = 126.5; Dec 2022 ≈ 113.2 (2020 = 100).
	if got, want := cpi["2022-12"]/cpi["2026-10"], 113.2/126.5; math.Abs(got-want) > 0.01 {
		t.Errorf("Dec 2022 / Oct 2026 = %.4f, want ≈ %.4f", got, want)
	}
	if cpi["2026-11"] != 100 {
		t.Errorf("latest month index = %.1f, want 100", cpi["2026-11"])
	}
	if !s.HasWindow || s.WindowStart.Format("2006-01-02") != "2025-11-18" || s.WindowEnd.Format("2006-01-02") != "2026-11-18" {
		t.Fatalf("window = %v..%v", s.WindowStart, s.WindowEnd)
	}
	if s.InflationPct < 2 || s.InflationPct > 4 {
		t.Errorf("12-month inflation = %.2f %%, want 2-4 %%", s.InflationPct)
	}
	if want := 10000 * s.InflationPct / 100; s.PurchasingPowerLost < 100 || math.Abs(s.PurchasingPowerLost-want) > 1e-6 {
		t.Errorf("lost to inflation = %.2f, want %.2f", s.PurchasingPowerLost, want)
	}
	if math.Abs(s.NominalReturn) > 1e-9 || s.RealReturn >= 0 {
		t.Errorf("flat value: nominal %.2f %%, real %.2f %%; want 0 and negative", s.NominalReturn, s.RealReturn)
	}
	// Month-end points, the current month at the latest snapshot.
	if last := s.History[len(s.History)-1]; last.Date != "2026-11-18" {
		t.Errorf("last point = %s, want the latest snapshot 2026-11-18", last.Date)
	}
	if s.History[0].Date != "2022-12-31" {
		t.Errorf("first point = %s, want month end 2022-12-31", s.History[0].Date)
	}
}

// F15: the window return excludes deposits.
func TestComputeInflation_ReturnExcludesDeposits(t *testing.T) {
	raw := []analytics.DailyValuation{
		{Date: rpDay("2025-11-18"), Value: 10000},
		{Date: rpDay("2026-05-01"), Value: 20000}, // +10,000 deposit
		{Date: rpDay("2026-11-18"), Value: 20200}, // +1 % market
	}
	adj := analytics.BuildCashflowAdjustedSeries(raw, []analytics.CashFlow{{Date: rpDay("2026-05-01"), Amount: 10000}})
	s := computeInflation(raw, adj, germanVPI)
	if math.Abs(s.NominalReturn-1) > 1e-6 {
		t.Errorf("nominal return = %.4f %%, want 1 %% (the deposit is not growth)", s.NominalReturn)
	}
}

// F6 + F16: the benchmark gets the same starting value and the same flows
// (vests at market value, withdrawals of any stored sign), units are not
// floored at 0, and both lines are valued on the same month-end dates up to
// the latest snapshot.
func TestReplayBenchmark_LikeForLike(t *testing.T) {
	var actual []analytics.DailyValuation
	for d := rpDay("2026-01-10"); !d.After(rpDay("2026-04-15")); d = d.AddDate(0, 0, 1) {
		actual = append(actual, analytics.DailyValuation{Date: d, Value: 1000})
	}
	prices := []benchmarkPrice{
		{date: rpDay("2026-01-02"), price: 100},
		{date: rpDay("2026-02-02"), price: 110},
		{date: rpDay("2026-03-02"), price: 120},
	}
	flows := []analytics.CashFlow{
		{Date: rpDay("2026-01-05"), Amount: 999},   // before the start: in the start value
		{Date: rpDay("2026-02-10"), Amount: 1100},  // vest at market value: +10 units
		{Date: rpDay("2026-03-10"), Amount: -3600}, // withdrawal: −30 units
	}
	rp := replayBenchmark(actual, flows, prices)
	if !rp.Start.Equal(rpDay("2026-01-10")) {
		t.Fatalf("start = %v", rp.Start)
	}
	// 10 units at the start, +10, −30 → −10 units at 120.
	if math.Abs(rp.BenchmarkValue-(-1200)) > 1e-6 {
		t.Errorf("benchmark value = %.2f, want −1,200 (no floor at 0)", rp.BenchmarkValue)
	}
	if rp.ActualValue != 1000 {
		t.Errorf("actual value = %.2f", rp.ActualValue)
	}
	var dates []string
	for _, p := range rp.Points {
		dates = append(dates, p.Date)
	}
	if got := strings.Join(dates, ","); got != "2026-01-10,2026-01-31,2026-02-28,2026-03-31,2026-04-15" {
		t.Errorf("points = %s", got)
	}
	if rp.Points[0].Actual != rp.Points[0].Benchmark {
		t.Errorf("both lines must start equal: %+v", rp.Points[0])
	}
	if rp.Points[2].Benchmark != 20*110 {
		t.Errorf("Feb month-end benchmark = %.0f, want 2,200", rp.Points[2].Benchmark)
	}
}

// F7: the benchmark is measured over the portfolio's window only.
func TestBenchmarkRiskWindow(t *testing.T) {
	prices := []benchmarkPrice{{rpDay("2022-01-03"), 50}, {rpDay("2023-01-02"), 60}, {rpDay("2024-01-01"), 70}, {rpDay("2025-01-06"), 0}}
	pts := benchmarkRiskWindow(prices, rpDay("2023-01-01"), rpDay("2025-12-31"))
	if len(pts) != 2 || pts[0].Value != 60 || pts[1].Value != 70 {
		t.Errorf("window = %+v", pts)
	}
}

// F8: 90k € of Alphabet held directly next to a 10k € MSCI World ETF holding
// 4 % Alphabet showed "4 %" and no alert. Exposure is a share of all
// securities, with direct stocks counted at 100 %.
func TestConcentrationExposureInputs_IncludesDirectStocks(t *testing.T) {
	const googl = "US02079K1079"
	enriched := []enrichedHolding{
		{security: db.Security{ISIN: googl, Name: "Alphabet Inc. Class C", AssetClass: "stock"}, value: 90000},
		{security: db.Security{ISIN: "IE00B4L5Y983", Name: "iShares Core MSCI World", AssetClass: "etf"}, value: 10000},
		{security: db.Security{ISIN: "LU0000000001", Name: "Fund without data", AssetClass: "fund"}, value: 0},
	}
	constituents := map[string]map[string]float64{"IE00B4L5Y983": {googl: 4, "US0378331005": 5}}
	names := map[string]string{"US0378331005": "Apple"}
	inputs := concentrationExposureInputs(enriched, constituents, names)
	exposure := analytics.ComputeEffectiveExposure(inputs)
	if got := exposure[googl] * 100; math.Abs(got-90.4) > 1e-9 {
		t.Errorf("Alphabet exposure = %.2f %%, want 90.4 %%", got)
	}
	alerts := analytics.ComputeConcentrationAlertsWithThresholds(nil, inputs, names, 70, 5)
	found := false
	for _, a := range alerts {
		if a.Type == "concentration" && strings.HasPrefix(a.Message, "Alphabet") {
			found = true
			if a.Level != "critical" {
				t.Errorf("Alphabet alert level = %s, want critical", a.Level)
			}
		}
	}
	if !found {
		t.Errorf("no Alphabet concentration alert in %+v", alerts)
	}
}

// I2: whenever the flag is set there is a message, and the 30-day volatility
// threshold is the same for both.
func TestVolatilityContext_FlagAlwaysHasMessage(t *testing.T) {
	start := rpDay("2026-01-01")
	var pts []analytics.DailyValuation
	v := 100.0
	for i := 0; i < 120; i++ {
		if i >= 90 {
			if i%2 == 0 {
				v *= 1.025
			} else {
				v /= 1.025
			}
		}
		pts = append(pts, analytics.DailyValuation{Date: start.AddDate(0, 0, i), Value: v})
	}
	ctx := volatilityContext(pts, pts)
	vol := ctx["vol_30d"].(float64)
	if vol <= volElevatedPct {
		t.Fatalf("test series vol = %.1f, want above %.0f", vol, volElevatedPct)
	}
	if ctx["elevated"] != true || ctx["message"] == "" || ctx["reason"] != "volatility" {
		t.Errorf("context = %+v", ctx)
	}
	calm := volatilityContext(pts[:80], pts[:80])
	if calm["elevated"] != false || calm["message"] != "" {
		t.Errorf("calm context = %+v", calm)
	}
}

// F14: funds are weighted by equity_pct, crypto gets an impact, bonds and
// gold none.
func TestStressExposure(t *testing.T) {
	cases := []struct {
		sec        db.Security
		eq, crypto float64
	}{
		{db.Security{AssetClass: "stock"}, 1, 0},
		{db.Security{AssetClass: "etf", Name: "Vanguard LifeStrategy 60", EquityPct: numericFromFloat(60)}, 0.6, 0},
		{db.Security{AssetClass: "etf", Name: "Some fund", EquityPct: numericFromFloat(0.8)}, 0.8, 0},
		{db.Security{AssetClass: "etf", Name: "VANGUARD FTSE ALL-WORLD"}, 1, 0},
		{db.Security{AssetClass: "etf", Name: "Multi-Asset Balanced"}, 0.5, 0},
		{db.Security{AssetClass: "etf", Name: "iShares Core Euro Govt Bond"}, 0, 0},
		{db.Security{AssetClass: "crypto"}, 0, 1},
		{db.Security{AssetClass: "commodity"}, 0, 0},
	}
	for _, c := range cases {
		eq, cr := stressExposure(c.sec)
		if math.Abs(eq-c.eq) > 1e-9 || cr != c.crypto {
			t.Errorf("%s %q: equity %.2f crypto %.2f, want %.2f %.2f", c.sec.AssetClass, c.sec.Name, eq, cr, c.eq, c.crypto)
		}
	}
	// 2008: −54 % index; 60 % equity + 20 % crypto + 20 % bonds.
	if got, want := crisisDrop(-54, 0.6, 0.2), -54*0.6+(-90)*0.2; math.Abs(got-want) > 1e-9 {
		t.Errorf("crisis drop = %.2f, want %.2f", got, want)
	}
}

// F13: "With monthly investing" used 1 % of the portfolio a month, so the
// value cancelled out. It now simulates the user's trailing-12-month net
// contributions along the crisis path.
func TestDCARecoveryMonths(t *testing.T) {
	// No contributions: the index's own recovery.
	if got := dcaRecoveryMonths(-54, 17, 50, 100000, 0); got != 50 {
		t.Errorf("without contributions = %d, want 50", got)
	}
	small := dcaRecoveryMonths(-54, 17, 50, 100000, 500)
	large := dcaRecoveryMonths(-54, 17, 50, 100000, 5000)
	if !(large < small && small < 50) {
		t.Errorf("recovery with 500 €/mo = %d, with 5,000 €/mo = %d; want both below 50 and more money faster", small, large)
	}
	// Same contribution relative to the portfolio gives the same answer.
	if a, b := dcaRecoveryMonths(-34, 1, 5, 10000, 100), dcaRecoveryMonths(-34, 1, 5, 1000000, 10000); a != b {
		t.Errorf("scale invariance: %d vs %d", a, b)
	}
	if got := trailingMonthlyContribution([]analytics.CashFlow{
		{Date: rpDay("2025-11-01"), Amount: 99999}, // outside the window
		{Date: rpDay("2026-01-01"), Amount: 26000},
		{Date: rpDay("2026-03-01"), Amount: -2000},
	}, rpDay("2026-11-18")); math.Abs(got-2000) > 1e-9 {
		t.Errorf("trailing monthly contribution = %.2f, want 2,000", got)
	}
	if got := scenarioMonths("2007-10-01", "2009-03-09"); got != 17 {
		t.Errorf("GFC decline = %d months, want 17", got)
	}
}

// KNOWN: the cash buffer used 5 % of securities as monthly spending. It is
// now liquid cash over recorded checking withdrawals per covered month.
func TestCashBufferInputs(t *testing.T) {
	checking := db.Account{ID: uuid.New(), Type: "checking", IsActive: true}
	savings := db.Account{ID: uuid.New(), Type: "savings", IsActive: true}
	pension := db.Account{ID: uuid.New(), Type: "pension", IsActive: true}
	cash := map[uuid.UUID]float64{checking.ID: 3000, savings.ID: 9000, pension.ID: 50000}
	if got := liquidCashEUR([]db.Account{checking, savings, pension}, cash); got != 12000 {
		t.Errorf("liquid cash = %.0f, want 12,000", got)
	}

	carPayment := rpTxn("2026-03-15", "withdrawal", 40000, "")
	carPayment.AccountID = checking.ID
	var txns []db.ListTransactionsRow
	for _, d := range []string{"2026-01-01", "2026-02-01", "2026-03-01", "2026-04-01", "2026-05-01"} {
		w := rpTxn(d, "withdrawal", -1500, "") // stored negative
		w.AccountID = checking.ID
		txns = append(txns, w)
	}
	txns = append(txns, carPayment)
	perMonth, months := recordedMonthlySpending(txns, []db.Account{checking}, map[uuid.UUID]bool{carPayment.ID: true}, nil, map[string]float64{"EUR": 1}, rpDay("2026-11-18"))
	wantMonths := rpDay("2026-05-01").Sub(rpDay("2026-01-01")).Hours() / 24 / 30.44
	if math.Abs(months-wantMonths) > 1e-9 || math.Abs(perMonth-7500/wantMonths) > 1e-6 {
		t.Errorf("spending = %.2f over %.2f months, want %.2f over %.2f", perMonth, months, 7500/wantMonths, wantMonths)
	}
	if p, _ := recordedMonthlySpending(nil, []db.Account{checking}, nil, nil, nil, rpDay("2026-11-18")); p != 0 {
		t.Errorf("no data: %.2f, want 0 (falls back to the stated assumption)", p)
	}
}

// F11: a plan from Jan 2025 to Mar 2026 that then stopped. The range runs to
// the last complete month (the current month joins once it has an
// execution); one-off buys do not count.
func TestComputeSparplanStreak_StoppedPlan(t *testing.T) {
	var txns []db.ListTransactionsRow
	for m := rpDay("2025-01-15"); !m.After(rpDay("2026-03-15")); m = m.AddDate(0, 1, 0) {
		txns = append(txns, rpTxn(m.Format("2006-01-02"), "savings_plan", 200, "IE00B4L5Y983"))
	}
	txns = append(txns, rpTxn("2026-06-10", "buy", 5000, "IE00B4L5Y983"))
	monthly := sparplanMonthlyAmounts(txns, nil, map[string]float64{"EUR": 1})
	if _, ok := monthly["2026-06"]; ok {
		t.Fatal("a one-off buy counted as a Sparplan execution")
	}
	st := computeSparplanStreak(monthly, rpDay("2026-11-18"))
	if st.TotalMonths != 22 || st.ActiveMonths != 15 || st.MissedMonths != 7 {
		t.Errorf("months total/active/missed = %d/%d/%d, want 22/15/7", st.TotalMonths, st.ActiveMonths, st.MissedMonths)
	}
	if st.CurrentStreak != 0 || st.LongestStreak != 15 {
		t.Errorf("streak current/longest = %d/%d, want 0/15", st.CurrentStreak, st.LongestStreak)
	}
	if st.LastMonth != "2026-10" {
		t.Errorf("range ends %s, want 2026-10", st.LastMonth)
	}
	if st.AvgMonthly != 200 || st.MissedCost <= 0 {
		t.Errorf("avg %.2f, missed cost %.2f", st.AvgMonthly, st.MissedCost)
	}

	// Executed this month already: the current month counts.
	monthly["2026-11"] = 200
	if st := computeSparplanStreak(monthly, rpDay("2026-11-18")); st.TotalMonths != 23 || st.CurrentStreak != 1 {
		t.Errorf("with a November execution: total %d, streak %d; want 23, 1", st.TotalMonths, st.CurrentStreak)
	}
}
