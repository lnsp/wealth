package analytics

import (
	"math"
	"testing"
)

func testApartment() PropertyValuation {
	return PropertyValuation{
		PurchaseDate:  day("2025-04-01"),
		PurchasePrice: 320000,
		LandValue:     75000,
		BuildingEnd:   1998 + DefaultBuildingUsefulLife,
		Drift:         DefaultPropertyDrift,
	}
}

func TestProperty_ValueAt(t *testing.T) {
	p := testApartment()
	approx(t, "before purchase", p.ValueAt(day("2025-03-31")), 0, 0)
	approx(t, "at purchase", p.ValueAt(p.PurchaseDate), 320000, 0.01)

	// 1.61 y later: land 75k · 1.02^1.61, building 245k · 1.02^1.61 · 51.14/52.75.
	land, building := p.Components(day("2026-11-11"))
	approx(t, "land", land, 77433, 5)
	approx(t, "building", building, 245211, 60)

	// Land alone keeps growing; the building tends to 0 at the end of its life.
	_, b2078 := p.Components(day("2078-06-01"))
	approx(t, "building at end of life", b2078, 0, 0.01)
}

func TestProperty_LandAndOther(t *testing.T) {
	land := PropertyValuation{PurchaseDate: day("2025-01-01"), PurchasePrice: 100000, Drift: 0.02}
	approx(t, "land after 10y", land.ValueAt(day("2035-01-01")), 100000*math.Pow(1.02, 3652.0/365.25), 1)

	watch := PropertyValuation{PurchaseDate: day("2025-01-01"), PurchasePrice: 8000}
	approx(t, "drift 0 stays flat", watch.ValueAt(day("2030-01-01")), 8000, 0)
	watch.Appraisals = []AssetObservation{{Date: day("2027-01-01"), AppraisedValue: ptr(9500)}}
	approx(t, "appraisal holds", watch.ValueAt(day("2030-01-01")), 9500, 0)
	approx(t, "before appraisal", watch.ValueAt(day("2026-12-31")), 8000, 0)
}

func TestProperty_AppraisalKeepsSplit(t *testing.T) {
	p := testApartment()
	p.Appraisals = []AssetObservation{{Date: day("2026-04-01"), AppraisedValue: ptr(340000)}}
	land, building := p.Components(day("2026-04-01"))
	approx(t, "appraised total", land+building, 340000, 0.01)
	if share := land / (land + building); share < 0.23 || share > 0.25 {
		t.Errorf("land share after appraisal = %.3f, want ≈0.24", share)
	}
	if p.ValueAt(day("2027-04-01")) <= 340000*0.99 || p.ValueAt(day("2027-04-01")) >= 340000*1.02 {
		t.Errorf("one year after appraisal = %.0f, want slightly above 340k minus aging", p.ValueAt(day("2027-04-01")))
	}
}

func TestProperty_ImprovementAddsAndAges(t *testing.T) {
	base := testApartment()
	improved := testApartment()
	improved.Improvements = []Improvement{{Date: day("2026-01-01"), Amount: 18000}}
	diffAt := improved.ValueAt(day("2026-01-01")) - base.ValueAt(day("2026-01-01"))
	approx(t, "added on its date", diffAt, 18000, 0.01)
	diffLater := improved.ValueAt(day("2046-01-01")) - base.ValueAt(day("2046-01-01"))
	if diffLater <= 0 || diffLater >= 18000*math.Pow(1.02, 20) {
		t.Errorf("improvement after 20y = %.0f, want positive but aged", diffLater)
	}
}

func testMortgage() Loan {
	return Loan{
		Principal:        260000,
		PayoutDate:       day("2025-04-01"),
		FirstPaymentDate: day("2025-05-01"),
		Rate:             0.0365,
		MonthlyPayment:   MonthlyPaymentFor(260000, 0.0365, 0.02),
		FixedUntil:       day("2040-04-01"),
		FollowupRate:     -1,
	}
}

func TestLoan_Schedule(t *testing.T) {
	l := testMortgage()
	approx(t, "annuity", l.MonthlyPayment, 1224.17, 0)
	s := l.Schedule()
	first := s[0]
	approx(t, "first interest", first.Interest, 790.83, 0.01) // 260000 · 3.65 % / 12
	approx(t, "first principal", first.Principal, 433.34, 0.01)
	approx(t, "balance after 19 payments", s[18].Balance, 251537, 2)
	approx(t, "left when fixed rate ends", l.BalanceAt(day("2040-04-01")), 156357, 5)
	if last := s[len(s)-1]; last.Balance != 0 || last.Due.Year() != 2053 {
		t.Errorf("payoff = %s balance %.2f, want 2053 and 0", last.Due.Format("2006-01"), last.Balance)
	}
}

func TestLoan_BalanceAt(t *testing.T) {
	l := testMortgage()
	approx(t, "before payout", l.BalanceAt(day("2025-03-31")), 0, 0)
	approx(t, "after payout", l.BalanceAt(day("2025-04-15")), 260000, 0)
	l.Extras = []ExtraRepayment{{Date: day("2025-06-15"), Amount: 10000}}
	before := testMortgage().BalanceAt(day("2025-06-20"))
	approx(t, "extra between due dates", l.BalanceAt(day("2025-06-20")), before-10000, 0.01)
	if got, plain := l.BalanceAt(day("2030-01-01")), testMortgage().BalanceAt(day("2030-01-01")); plain-got < 10000 {
		t.Errorf("extra should save more than its amount by 2030: diff %.0f", plain-got)
	}
}

func TestLoan_DueDateClampsMonthEnd(t *testing.T) {
	l := Loan{FirstPaymentDate: day("2025-01-31")}
	if d := l.dueDate(1); d.Format("2006-01-02") != "2025-02-28" {
		t.Errorf("due 1 = %s, want 2025-02-28", d.Format("2006-01-02"))
	}
	if d := l.dueDate(2); d.Format("2006-01-02") != "2025-03-31" {
		t.Errorf("due 2 = %s, want 2025-03-31", d.Format("2006-01-02"))
	}
}

func TestLoan_Scenario(t *testing.T) {
	l := testMortgage()
	from := day("2026-11-11")
	base := l.Scenario(from, 0, -1)
	extra := l.Scenario(from, 5000, -1)
	if extra.RemainingAtFixedEnd >= base.RemainingAtFixedEnd-5000*13 {
		t.Errorf("5k/yr for 14 Decembers should cut more than 65k: %.0f → %.0f", base.RemainingAtFixedEnd, extra.RemainingAtFixedEnd)
	}
	if !extra.PayoffDate.Before(base.PayoffDate) || extra.TotalInterest >= base.TotalInterest {
		t.Error("extra repayments must pay off earlier with less interest")
	}
	higher := l.Scenario(from, 0, 0.05)
	if !higher.PayoffDate.After(base.PayoffDate) || higher.InterestAfterFixed <= base.InterestAfterFixed {
		t.Error("a higher follow-up rate must take longer and cost more")
	}
	approx(t, "same rate keeps remaining", higher.RemainingAtFixedEnd, base.RemainingAtFixedEnd, 0.01)
}
