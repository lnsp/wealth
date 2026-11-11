package analytics

import (
	"math"
	"time"
)

// ExtraRepayment is an unscheduled repayment (Sondertilgung).
type ExtraRepayment struct {
	Date   time.Time
	Amount float64
}

// Loan is a German annuity loan (Annuitätendarlehen): a constant monthly
// payment of which interest = balance · rate / 12 and the rest repays
// principal. After the fixed-rate period the follow-up rate applies (the
// nominal rate when unknown).
type Loan struct {
	Principal        float64
	PayoutDate       time.Time
	FirstPaymentDate time.Time
	Rate             float64 // nominal, per year
	MonthlyPayment   float64
	FixedUntil       time.Time // zero = fixed for the whole term
	FollowupRate     float64   // < 0 = same as Rate
	Extras           []ExtraRepayment
}

// LoanPayment is one scheduled monthly payment.
type LoanPayment struct {
	Due       time.Time
	Interest  float64
	Principal float64
	Balance   float64 // after this payment
}

// maxLoanMonths caps schedules whose payment never covers the interest.
const maxLoanMonths = 12 * 60

// MonthlyPaymentFor returns the annuity for an initial repayment rate, e.g.
// 260,000 at 3.65 % + 2 % repayment → 1,224.17 €/month.
func MonthlyPaymentFor(principal, rate, initialRepayment float64) float64 {
	return math.Round(principal*(rate+initialRepayment)/12*100) / 100
}

// dueDate returns the n-th (0-based) due date, keeping the day of month and
// clamping it to the month's last day.
func (l Loan) dueDate(n int) time.Time {
	f := l.FirstPaymentDate
	first := time.Date(f.Year(), f.Month()+time.Month(n), 1, 0, 0, 0, 0, f.Location())
	last := first.AddDate(0, 1, -1).Day()
	day := f.Day()
	if day > last {
		day = last
	}
	return time.Date(first.Year(), first.Month(), day, 0, 0, 0, 0, f.Location())
}

func (l Loan) rateOn(due time.Time) float64 {
	if !l.FixedUntil.IsZero() && due.After(l.FixedUntil) && l.FollowupRate >= 0 {
		return l.FollowupRate
	}
	return l.Rate
}

// Schedule returns the payments until the loan is repaid. Extra repayments
// lower the balance on their date.
func (l Loan) Schedule() []LoanPayment {
	var out []LoanPayment
	balance := l.Principal
	extraIdx := 0
	extras := sortedExtras(l.Extras)
	for n := 0; n < maxLoanMonths && balance > 0.005; n++ {
		due := l.dueDate(n)
		for extraIdx < len(extras) && !extras[extraIdx].Date.After(due) {
			balance = math.Max(0, balance-extras[extraIdx].Amount)
			extraIdx++
		}
		if balance <= 0.005 {
			break
		}
		interest := round2(balance * l.rateOn(due) / 12)
		principal := math.Min(balance, l.MonthlyPayment-interest)
		balance = round2(balance - principal)
		out = append(out, LoanPayment{Due: due, Interest: interest, Principal: principal, Balance: balance})
	}
	return out
}

func round2(v float64) float64 { return math.Round(v*100) / 100 }

func sortedExtras(in []ExtraRepayment) []ExtraRepayment {
	out := append([]ExtraRepayment(nil), in...)
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].Date.Before(out[j-1].Date); j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// BalanceAt returns what is owed at the end of day t (0 before the payout).
func (l Loan) BalanceAt(t time.Time) float64 {
	return balanceAt(l, l.Schedule(), t)
}

func balanceAt(l Loan, schedule []LoanPayment, t time.Time) float64 {
	if t.Before(l.PayoutDate) {
		return 0
	}
	balance := l.Principal
	for _, p := range schedule {
		if p.Due.After(t) {
			break
		}
		balance = p.Balance
	}
	// Extras between the last due date and t already happened.
	var lastDue time.Time
	for _, p := range schedule {
		if p.Due.After(t) {
			break
		}
		lastDue = p.Due
	}
	for _, e := range l.Extras {
		if e.Date.After(lastDue) && !e.Date.After(t) {
			balance -= e.Amount
		}
	}
	return math.Max(0, balance)
}

// LoanScenario summarises a what-if run of a loan.
type LoanScenario struct {
	RemainingAtFixedEnd float64   `json:"remaining_at_fixed_end"`
	PayoffDate          time.Time `json:"payoff_date"`
	TotalInterest       float64   `json:"total_interest"`
	InterestAfterFixed  float64   `json:"interest_after_fixed"`
}

// Scenario simulates extra repayments of extraPerYear each December from
// `from` until the fixed-rate period ends, and a follow-up rate (< 0 keeps the
// current assumption).
func (l Loan) Scenario(from time.Time, extraPerYear, followupRate float64) LoanScenario {
	sim := l
	sim.Extras = append([]ExtraRepayment(nil), l.Extras...)
	if followupRate >= 0 {
		sim.FollowupRate = followupRate
	}
	if extraPerYear > 0 {
		end := l.FixedUntil
		if end.IsZero() {
			end = from.AddDate(60, 0, 0)
		}
		for y := from.Year(); ; y++ {
			d := time.Date(y, time.December, 1, 0, 0, 0, 0, from.Location())
			if d.After(end) {
				break
			}
			if d.After(from) {
				sim.Extras = append(sim.Extras, ExtraRepayment{Date: d, Amount: extraPerYear})
			}
		}
	}
	schedule := sim.Schedule()
	var out LoanScenario
	for _, p := range schedule {
		out.TotalInterest += p.Interest
		if !l.FixedUntil.IsZero() && p.Due.After(l.FixedUntil) {
			out.InterestAfterFixed += p.Interest
		}
	}
	if len(schedule) > 0 {
		out.PayoffDate = schedule[len(schedule)-1].Due
	}
	if !l.FixedUntil.IsZero() {
		out.RemainingAtFixedEnd = balanceAt(sim, schedule, l.FixedUntil)
	}
	out.TotalInterest = round2(out.TotalInterest)
	out.InterestAfterFixed = round2(out.InterestAfterFixed)
	return out
}
