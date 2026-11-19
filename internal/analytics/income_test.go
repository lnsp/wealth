package analytics

import (
	"math"
	"testing"
	"time"
)

func date(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }

func nearly(t *testing.T, name string, got, want, tol float64) {
	t.Helper()
	if math.Abs(got-want) > tol {
		t.Errorf("%s = %.2f, want %.2f ± %.2f", name, got, want, tol)
	}
}

var gkvProfile = PayrollProfile{Health: "gkv", Zusatzbeitrag: 0.029}

func TestIncomeTaxIndexedMatches2026Tariff(t *testing.T) {
	for x := 0.0; x <= 400000; x += 137.37 {
		if got, want := incomeTaxIndexed(x, 1), incomeTaxTariff2026(x); got != want {
			t.Fatalf("zvE %.2f: indexed %.0f, 2026 tariff %.0f", x, got, want)
		}
	}
}

func TestIncomeTaxIndexedContinuousAtTopThreshold(t *testing.T) {
	k := math.Pow(1.02, 10)
	below, above := incomeTaxIndexed(topRateThreshold, k), incomeTaxIndexed(topRateThreshold+1, k)
	if d := above - below; d < 0 || d > 1 {
		t.Errorf("tax jumps by %.0f at the 45 %% threshold", d)
	}
}

func TestPayrollLawFor(t *testing.T) {
	a := LawAssumptions{Inflation: 0.02, WageGrowth: 0.03}
	l26, l27, l28 := PayrollLawFor(2026, a), PayrollLawFor(2027, a), PayrollLawFor(2028, a)
	if l26.RVCap != 101400 || l26.KVCap != 69750 || !l26.TariffEnacted || l26.TariffIndex != 1 {
		t.Errorf("2026 law %+v", l26)
	}
	if l27.RVCap != 106200 || l27.KVCap != 76500 || !l27.CeilingsEnacted || l27.TariffEnacted {
		t.Errorf("2027 law %+v", l27)
	}
	nearly(t, "2027 tariff index", l27.TariffIndex, 1.02, 1e-12)
	nearly(t, "2028 RV cap", l28.RVCap, 106200*1.03, 1e-6)
	nearly(t, "2028 KV cap", l28.KVCap, 76500*1.03, 1e-6)
	if l28.CeilingsEnacted {
		t.Error("2028 ceilings are an assumption")
	}
	nearly(t, "2026 minimum voluntary base", l26.MinVoluntaryBase, 15820, 0.01)
}

// The adversarial review recomputed the mockup's 2027 gross with the final
// 2027 ceilings: 102,524 €.
func TestPayroll2027Golden(t *testing.T) {
	r := Payroll(182669, PayrollLawFor(2027, LawAssumptions{Inflation: 0.02, WageGrowth: 0.03}), gkvProfile)
	nearly(t, "net", r.Net, 102524, 1.5)
	nearly(t, "RV", r.RV, 0.093*106200, 0.01)
	nearly(t, "KV", r.KV, 0.0875*76500, 0.01)
	nearly(t, "PV childless", r.PV, 0.024*76500, 0.01)
}

// The first mockup indexed the 2026 ceilings by 2 % and got 103,043 €.
func TestPayrollReproducesFirstMockup(t *testing.T) {
	law := PayrollLaw{Year: 2027, TariffIndex: 1.02, SoliFreigrenze: 20350 * 1.02, RVCap: 103428, KVCap: 71145}
	nearly(t, "net", Payroll(182669, law, gkvProfile).Net, 103043, 1.5)
}

func TestPayrollPrivateInsuranceSubsidyCap(t *testing.T) {
	law := PayrollLawFor(2027, LawAssumptions{})
	low := Payroll(150000, law, PayrollProfile{Health: "pkv", PKVPremiumMonthly: 600, PKVBasicShare: 0.85})
	nearly(t, "half premium", low.EmployerSubsidy, 3600, 0.01)
	high := Payroll(150000, law, PayrollProfile{Health: "pkv", PKVPremiumMonthly: 2000, PKVBasicShare: 0.85})
	nearly(t, "capped subsidy", high.EmployerSubsidy, high.MaxSubsidy, 0.01)
	nearly(t, "max subsidy", high.MaxSubsidy, (0.0875+0.018)*76500, 0.01)
	if high.PV != 0 || high.KV != 24000-high.EmployerSubsidy {
		t.Errorf("private: KV %.2f PV %.2f", high.KV, high.PV)
	}
}

func TestPayrollChurchTaxIsDeductible(t *testing.T) {
	law := PayrollLawFor(2026, LawAssumptions{})
	plain := Payroll(120000, law, gkvProfile)
	p := gkvProfile
	p.ChurchRate = 0.09
	church := Payroll(120000, law, p)
	nearly(t, "church tax", church.ChurchTax, 0.09*church.IncomeTax, 0.01)
	if church.TaxableIncome >= plain.TaxableIncome || church.IncomeTax >= plain.IncomeTax {
		t.Errorf("church tax should lower taxable income: %.0f vs %.0f", church.TaxableIncome, plain.TaxableIncome)
	}
}

func TestPayrollChildrenLowerCareRate(t *testing.T) {
	law := PayrollLawFor(2026, LawAssumptions{})
	p := gkvProfile
	p.Children = 3
	nearly(t, "PV with 3 children", Payroll(100000, law, p).PV, (0.018-0.005)*69750, 0.01)
}

func TestPostFireHealth(t *testing.T) {
	law := PayrollLawFor(2026, LawAssumptions{})
	rate := 0.14 + 0.029 + 0.042
	nearly(t, "minimum base", PostFireHealth(0, law, gkvProfile), 15820*rate, 0.01)
	nearly(t, "capital income", PostFireHealth(22764, law, gkvProfile), 22764*rate, 0.01)
	nearly(t, "ceiling", PostFireHealth(1e6, law, gkvProfile), 69750*rate, 0.01)
	nearly(t, "private", PostFireHealth(1e6, law, PayrollProfile{Health: "pkv", PKVPremiumMonthly: 950}), 11400, 0.01)
}

func mockHeld() []HeldVest {
	out := []HeldVest{{Date: date(2026, 12, 15), Amount: 3600, Currency: "USD"}}
	per := map[int]float64{2027: 41200 / 12.0, 2028: 27600 / 12.0, 2029: 14800 / 12.0}
	for y := 2027; y <= 2029; y++ {
		for m := 1; m <= 12; m++ {
			out = append(out, HeldVest{Date: date(y, time.Month(m), 15), Amount: per[y], Currency: "USD"})
		}
	}
	for m := 1; m <= 9; m++ {
		out = append(out, HeldVest{Date: date(2030, time.Month(m), 15), Amount: 4100 / 9.0, Currency: "USD"})
	}
	return out
}

func mockInput() ScheduleInput {
	created := date(2026, 11, 19)
	return ScheduleInput{
		Start:  date(2026, 12, 1),
		Months: 121,
		Streams: []IncomeStream{
			{Kind: IncomeSalary, Currency: "EUR", Amount: 110000, Growth: 0.03, Month: 1, Start: created, Created: created,
				Steps: []IncomeStep{{Date: date(2027, 4, 1), Amount: 125000}}},
			{Kind: IncomeBonus, Amount: 0.15, PayoutFactor: 1, Month: 3, Start: created, Created: created},
			{Kind: IncomeRSURefresh, Currency: "USD", Amount: 50000, Month: 3, VestMonths: 48, VestEvery: 1, FirstYear: 2027, Start: created, Created: created},
		},
		Held:    mockHeld(),
		Profile: gkvProfile,
		Assume:  ScheduleAssumptions{Inflation: 0.02, WageGrowth: 0.03, FX: map[string]float64{"USD": 1.16}},
		Savings: SavingsRule{Mode: "costs", RunningCostsMonthly: 3400},
	}
}

func yearOf(s Schedule, y int) ScheduleYear {
	for _, Y := range s.Years {
		if Y.Year == y {
			return Y
		}
	}
	return ScheduleYear{}
}

// The mockup engine (JavaScript) gives these figures for the same input.
func TestScheduleMatchesMockup(t *testing.T) {
	s := BuildSchedule(mockInput())
	y := yearOf(s, 2027)
	nearly(t, "2027 base", y.BaseGross, 122075, 0.5)
	nearly(t, "2027 bonus", y.BonusGross, 16500, 0.5)
	nearly(t, "2027 held", y.HeldGross, 41200/1.16, 0.5)
	nearly(t, "2027 refresh", y.RefreshGross, 9375/1.16, 0.5)
	nearly(t, "2027 net", y.Payroll.Net, 102248, 1.5)
	nearly(t, "2027 savings", y.Savings, 60632, 1.5)
	nearly(t, "2027 running costs", y.RunningCosts, 3400*1.02*12, 0.01)
	nearly(t, "2031 refresh at steady state", yearOf(s, 2031).RefreshGross, 50000/1.16, 0.5)
}

func TestScheduleMonthsSumToYear(t *testing.T) {
	s := BuildSchedule(mockInput())
	for _, y := range []int{2027, 2028, 2031, 2036} {
		Y := yearOf(s, y)
		var sum float64
		for _, m := range s.Months {
			if m.Month.Year() == y {
				sum += m.Net
			}
		}
		nearly(t, "months vs year net", sum, Y.Payroll.Net, 0.01)
	}
}

func TestScheduleStreamOrderDoesNotMatter(t *testing.T) {
	in := mockInput()
	a := BuildSchedule(in)
	in.Streams = []IncomeStream{in.Streams[2], in.Streams[0], in.Streams[1]}
	b := BuildSchedule(in)
	for i := range a.Months {
		if math.Abs(a.Months[i].Savings-b.Months[i].Savings) > 1e-9 {
			t.Fatalf("month %d: %.2f vs %.2f", i, a.Months[i].Savings, b.Months[i].Savings)
		}
	}
}

func TestScheduleKeepsNegativeMonths(t *testing.T) {
	in := mockInput()
	in.Savings.RunningCostsMonthly = 6000
	s := BuildSchedule(in)
	neg := 0
	for _, m := range s.Months[:12] {
		if m.CashSavings < 0 {
			neg++
		}
	}
	if neg == 0 {
		t.Error("running costs above salary should give negative cash months")
	}
}

func TestScheduleStopsAndForfeitsAtStop(t *testing.T) {
	in := mockInput()
	in.Stop = date(2030, 7, 1)
	s := BuildSchedule(in)
	for _, m := range s.Months {
		if !m.Month.Before(in.Stop) && (m.Gross() != 0 || m.Savings != 0) {
			t.Fatalf("%s: income %.2f after the stop", m.Month.Format("2006-01"), m.Gross())
		}
	}
	if got := yearOf(s, 2030).RefreshGross; got >= yearOf(BuildSchedule(mockInput()), 2030).RefreshGross {
		t.Error("tranches after the stop should be forfeited")
	}
}

func TestScheduleRealGrantReplacesModelledRefresh(t *testing.T) {
	in := mockInput()
	base := yearOf(BuildSchedule(in), 2028).RefreshGross
	in.Grants = []SeenGrant{{FirstSeen: date(2028, 3, 20), FirstVest: date(2028, 4, 25)}}
	got := yearOf(BuildSchedule(in), 2028).RefreshGross
	nearly(t, "2028 refresh without the 2028 grant", got, base-9*50000/48.0/1.16, 0.5)
	// A grant that was already known when the stream was created changes nothing.
	in.Grants = []SeenGrant{{FirstSeen: date(2026, 3, 20), FirstVest: date(2028, 4, 25)}}
	nearly(t, "old grant ignored", yearOf(BuildSchedule(in), 2028).RefreshGross, base, 0.01)
}

func TestScheduleSavingsRate(t *testing.T) {
	in := mockInput()
	in.Savings = SavingsRule{Mode: "rate", Rate: 0.4, RSUFull: true}
	for _, m := range BuildSchedule(in).Months[:24] {
		want := 0.4*(m.BaseNet+m.BonusNet+m.OneOffNet) + m.HeldNet + m.RefreshNet
		nearly(t, "rate savings", m.Savings, want, 1e-9)
	}
}

func TestScheduleFrontLoadedVesting(t *testing.T) {
	frontLoaded := []float64{1.0 / 3, 1.0 / 3, 1.0 / 6, 1.0 / 6}
	in := mockInput()
	in.Held = nil
	in.Streams[2].VestWeights = frontLoaded
	in.Streams[2].End = date(2027, 3, 31) // one grant only
	s := BuildSchedule(in)
	perMonth := []float64{50000 / 3.0 / 12, 50000 / 3.0 / 12, 50000 / 6.0 / 12, 50000 / 6.0 / 12}
	nearly(t, "2027 (Apr–Dec of year 1)", yearOf(s, 2027).RefreshGross*1.16, 9*perMonth[0], 0.01)
	nearly(t, "2028 (end of year 1, start of year 2)", yearOf(s, 2028).RefreshGross*1.16, 3*perMonth[0]+9*perMonth[1], 0.01)
	nearly(t, "2029 (end of year 2, start of year 3)", yearOf(s, 2029).RefreshGross*1.16, 3*perMonth[1]+9*perMonth[2], 0.01)
	var total float64
	for _, y := range s.Years {
		total += y.RefreshGross
	}
	nearly(t, "the whole grant vests", total*1.16, 50000, 0.01)

	// With a grant every year, the shape only moves vests earlier: the steady state is still one grant a year.
	in = mockInput()
	in.Streams[2].VestWeights = frontLoaded
	nearly(t, "steady state", yearOf(BuildSchedule(in), 2031).RefreshGross*1.16, 50000, 0.01)
	if even, front := yearOf(BuildSchedule(mockInput()), 2028).RefreshGross, yearOf(BuildSchedule(in), 2028).RefreshGross; front <= even {
		t.Errorf("front-loading should raise early vests: %.0f vs %.0f", front, even)
	}
}
