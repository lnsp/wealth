package analytics

import (
	"math"
	"sort"
	"strings"
	"time"
)

// Income stream kinds.
const (
	IncomeSalary     = "salary"
	IncomeBonus      = "bonus"
	IncomeRSURefresh = "rsu_refresh"
	IncomeOneOff     = "one_off"
)

// IncomeStep is a dated change of a salary: a promotion, or 0 for unpaid leave.
type IncomeStep struct {
	Date   time.Time
	Amount float64
}

// IncomeStream is one expected source of pay. Amount means: salary, gross a
// year as of Start; bonus, the target share of last year's base salary;
// rsu_refresh, the grant value; one_off, the payment in Start's month.
type IncomeStream struct {
	Kind         string
	Currency     string
	Amount       float64
	Growth       float64 // salary: yearly raise; refresh: grant growth
	Month        int     // salary: raise month; bonus: payout month; refresh: grant month
	PayoutFactor float64
	VestMonths   int
	VestEvery    int
	CliffMonths  int
	FirstYear    int // refresh: first modelled grant; 0 = first grant month from the schedule start
	Start, End   time.Time
	Created      time.Time
	Steps        []IncomeStep
}

// HeldVest is a future tranche of a grant already held, valued at today's price.
type HeldVest struct {
	Date     time.Time
	Amount   float64
	Currency string
}

// SeenGrant is a real grant from an import, used to retire modelled refreshes.
type SeenGrant struct {
	FirstSeen, FirstVest time.Time
}

// ScheduleAssumptions drive everything the law and the streams don't fix.
type ScheduleAssumptions struct {
	Inflation  float64
	WageGrowth float64
	ShareDrift float64            // share price change a year until vest
	FX         map[string]float64 // units per EUR
}

// SavingsRule turns net income into savings: "costs" saves net income minus
// running costs (today's money, inflated); "rate" saves a share of salary and
// bonus after tax, and vests in full when RSUFull.
type SavingsRule struct {
	Mode                string
	RunningCostsMonthly float64
	Rate                float64
	RSUFull             bool
}

// ScheduleInput describes the months to build. Employment income stops in
// Stop's month (FIRE or retirement); unvested tranches are forfeited then.
type ScheduleInput struct {
	Start   time.Time
	Months  int
	Stop    time.Time
	Streams []IncomeStream
	Held    []HeldVest
	Grants  []SeenGrant
	Profile PayrollProfile
	Assume  ScheduleAssumptions
	Savings SavingsRule
}

// ScheduleMonth is one month of expected income, nominal euros.
type ScheduleMonth struct {
	Month        time.Time `json:"month"`
	BaseGross    float64   `json:"base_gross"`
	BonusGross   float64   `json:"bonus_gross"`
	OneOffGross  float64   `json:"one_off_gross"`
	HeldGross    float64   `json:"held_gross"`
	RefreshGross float64   `json:"refresh_gross"`
	BaseNet      float64   `json:"base_net"`
	BonusNet     float64   `json:"bonus_net"`
	OneOffNet    float64   `json:"one_off_net"`
	HeldNet      float64   `json:"held_net"`
	RefreshNet   float64   `json:"refresh_net"`
	Net          float64   `json:"net"`
	RunningCosts float64   `json:"running_costs"`
	Savings      float64   `json:"savings"`
	CashSavings  float64   `json:"cash_savings"`
	Stopped      bool      `json:"stopped"`
}

// Gross is the month's pay before tax.
func (m ScheduleMonth) Gross() float64 {
	return m.BaseGross + m.BonusGross + m.OneOffGross + m.HeldGross + m.RefreshGross
}

func (m ScheduleMonth) variableGross() float64 {
	return m.BonusGross + m.OneOffGross + m.HeldGross + m.RefreshGross
}

// ScheduleYear sums a calendar year and carries its tax bill. A partial year
// is taxed as if its months ran all year.
type ScheduleYear struct {
	Year         int           `json:"year"`
	Months       int           `json:"months"`
	BaseGross    float64       `json:"base_gross"`
	BonusGross   float64       `json:"bonus_gross"`
	OneOffGross  float64       `json:"one_off_gross"`
	HeldGross    float64       `json:"held_gross"`
	RefreshGross float64       `json:"refresh_gross"`
	Net          float64       `json:"net"`
	Savings      float64       `json:"savings"`
	RunningCosts float64       `json:"running_costs"`
	Payroll      PayrollResult `json:"payroll"`
	BaseKeep     float64       `json:"base_keep"`
	VariableKeep float64       `json:"variable_keep"`
	MarginalKeep float64       `json:"marginal_keep"`
	MarginalTax  float64       `json:"marginal_tax"`
}

// Schedule is the month-by-month result.
type Schedule struct {
	Months []ScheduleMonth
	Years  []ScheduleYear
}

// Contributions returns the savings of each month, the input of every
// Planning projection.
func (s Schedule) Contributions() []float64 {
	out := make([]float64, len(s.Months))
	for i, m := range s.Months {
		out[i] = m.Savings
	}
	return out
}

func firstOfMonth(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
}

func monthsFrom(a, b time.Time) int {
	return (b.Year()-a.Year())*12 + int(b.Month()) - int(a.Month())
}

func toEUR(amount float64, currency string, fx map[string]float64) float64 {
	c := strings.ToUpper(strings.TrimSpace(currency))
	if c == "" || c == "EUR" {
		return amount
	}
	if r := fx[c]; r > 0 {
		return amount / r
	}
	return amount
}

func activeIn(s IncomeStream, m time.Time) bool {
	if m.Before(firstOfMonth(s.Start)) {
		return false
	}
	return s.End.IsZero() || !m.After(firstOfMonth(s.End))
}

// salaryRates returns the yearly salary in effect for each month from `from`
// on. Before the stream's start the start amount is assumed when the job was
// already running at `running`, else nothing is paid.
func salaryRates(s IncomeStream, from, running time.Time, n int) []float64 {
	start := firstOfMonth(s.Start)
	steps := append([]IncomeStep(nil), s.Steps...)
	sort.Slice(steps, func(i, j int) bool { return steps[i].Date.Before(steps[j].Date) })
	raiseMonth := s.Month
	if raiseMonth < 1 || raiseMonth > 12 {
		raiseMonth = 1
	}
	out := make([]float64, n)
	rate := s.Amount
	for i := range n {
		m := from.AddDate(0, i, 0)
		if m.Before(start) {
			if !start.After(running) {
				out[i] = s.Amount
			}
			continue
		}
		if m.After(start) && int(m.Month()) == raiseMonth {
			rate *= 1 + s.Growth
		}
		for _, st := range steps {
			if firstOfMonth(st.Date).Equal(m) {
				rate = st.Amount
			}
		}
		if !s.End.IsZero() && m.After(firstOfMonth(s.End)) {
			continue
		}
		out[i] = rate
	}
	return out
}

// BuildSchedule expands the streams into months, taxes each calendar year and
// applies the savings rule. Base salary is taxed first; bonus, one-offs and
// vests share the remaining tax in proportion, so stream order never matters.
func BuildSchedule(in ScheduleInput) Schedule {
	start := firstOfMonth(in.Start)
	n := max(in.Months, 0)
	stop := time.Time{}
	if !in.Stop.IsZero() {
		stop = firstOfMonth(in.Stop)
	}
	stopped := func(m time.Time) bool { return !stop.IsZero() && !m.Before(stop) }
	months := make([]ScheduleMonth, n)
	for i := range months {
		months[i].Month = start.AddDate(0, i, 0)
		months[i].Stopped = stopped(months[i].Month)
	}
	monthIndex := func(t time.Time) int {
		i := monthsFrom(start, firstOfMonth(t))
		if i < 0 || i >= n {
			return -1
		}
		return i
	}

	// Base salary, with a year of history for the first bonus.
	histFrom := time.Date(start.Year()-1, 1, 1, 0, 0, 0, 0, time.UTC)
	offset := monthsFrom(histFrom, start)
	baseByMonth := make([]float64, offset+n)
	for _, s := range in.Streams {
		if s.Kind != IncomeSalary {
			continue
		}
		for i, r := range salaryRates(s, histFrom, start, offset+n) {
			baseByMonth[i] += toEUR(r, s.Currency, in.Assume.FX) / 12
		}
	}
	earned := map[int]float64{}
	for i, v := range baseByMonth {
		m := histFrom.AddDate(0, i, 0)
		if stopped(m) {
			continue
		}
		earned[m.Year()] += v
		if j := i - offset; j >= 0 {
			months[j].BaseGross = v
		}
	}

	drift := func(from, to time.Time) float64 {
		return math.Pow(1+in.Assume.ShareDrift, float64(monthsFrom(from, to))/12)
	}
	for _, s := range in.Streams {
		switch s.Kind {
		case IncomeBonus:
			for i := range months {
				m := months[i].Month
				if months[i].Stopped || int(m.Month()) != s.Month || !activeIn(s, m) {
					continue
				}
				months[i].BonusGross += s.Amount * s.PayoutFactor * earned[m.Year()-1]
			}
		case IncomeOneOff:
			if i := monthIndex(s.Start); i >= 0 && !months[i].Stopped {
				months[i].OneOffGross += toEUR(s.Amount, s.Currency, in.Assume.FX)
			}
		case IncomeRSURefresh:
			addRefresh(months, s, in, start, stopped, drift)
		}
	}
	for _, v := range in.Held {
		if i := monthIndex(v.Date); i >= 0 && !months[i].Stopped {
			months[i].HeldGross += toEUR(v.Amount, v.Currency, in.Assume.FX) * drift(start, months[i].Month)
		}
	}

	years := taxYears(months, in)
	applySavings(months, years, in, start)
	return Schedule{Months: months, Years: years}
}

func addRefresh(months []ScheduleMonth, s IncomeStream, in ScheduleInput, start time.Time, stopped func(time.Time) bool, drift func(a, b time.Time) float64) {
	every := s.VestEvery
	if every <= 0 {
		every = 1
	}
	tranches := s.VestMonths / every
	if tranches <= 0 || s.Month < 1 || s.Month > 12 {
		return
	}
	first := s.FirstYear
	if first == 0 {
		first = start.Year()
		if time.Date(first, time.Month(s.Month), 1, 0, 0, 0, 0, time.UTC).Before(start) {
			first++
		}
	}
	end := start.AddDate(0, len(months), 0)
	for g := first; ; g++ {
		grant := time.Date(g, time.Month(s.Month), 1, 0, 0, 0, 0, time.UTC)
		if !grant.Before(end) || stopped(grant) || (!s.End.IsZero() && grant.After(firstOfMonth(s.End))) {
			return
		}
		if grant.Before(firstOfMonth(s.Start)) || suppressedByRealGrant(grant, s.Created, in.Grants) {
			continue
		}
		value := toEUR(s.Amount, s.Currency, in.Assume.FX) * math.Pow(1+s.Growth, float64(g-first))
		for k := 1; k <= tranches; k++ {
			vest := grant.AddDate(0, max(k*every, s.CliffMonths), 0)
			i := monthsFrom(start, vest)
			if i < 0 || i >= len(months) || months[i].Stopped {
				continue
			}
			months[i].RefreshGross += value / float64(tranches) * drift(grant, vest)
		}
	}
}

// suppressedByRealGrant reports whether an import has shown a grant, new since
// the stream was created, whose first vest falls in the year after the
// modelled grant date: the real grant then takes that year's place.
func suppressedByRealGrant(grant, created time.Time, grants []SeenGrant) bool {
	for _, g := range grants {
		if !created.IsZero() && !g.FirstSeen.After(created) {
			continue
		}
		fv := firstOfMonth(g.FirstVest)
		if fv.After(grant) && !fv.After(grant.AddDate(1, 0, 0)) {
			return true
		}
	}
	return false
}

func taxYears(months []ScheduleMonth, in ScheduleInput) []ScheduleYear {
	var years []ScheduleYear
	byYear := map[int]int{}
	for _, m := range months {
		y := m.Month.Year()
		idx, ok := byYear[y]
		if !ok {
			idx = len(years)
			byYear[y] = idx
			years = append(years, ScheduleYear{Year: y})
		}
		Y := &years[idx]
		Y.Months++
		Y.BaseGross += m.BaseGross
		Y.BonusGross += m.BonusGross
		Y.OneOffGross += m.OneOffGross
		Y.HeldGross += m.HeldGross
		Y.RefreshGross += m.RefreshGross
	}
	la := LawAssumptions{Inflation: in.Assume.Inflation, WageGrowth: in.Assume.WageGrowth}
	for i := range years {
		Y := &years[i]
		law := PayrollLawFor(Y.Year, la)
		scale := 12 / float64(Y.Months)
		base := Y.BaseGross * scale
		variable := (Y.BonusGross + Y.OneOffGross + Y.HeldGross + Y.RefreshGross) * scale
		onlyBase := Payroll(base, law, in.Profile)
		all := Payroll(base+variable, law, in.Profile)
		probe := Payroll(base+variable+1000, law, in.Profile)
		Y.Payroll = all
		if base > 0 {
			Y.BaseKeep = onlyBase.Net / base
		}
		Y.VariableKeep = Y.BaseKeep
		if variable > 0 {
			Y.VariableKeep = 1 - (all.Deductions()-onlyBase.Deductions())/variable
		}
		Y.MarginalKeep = 1 - (probe.Deductions()-all.Deductions())/1000
		Y.MarginalTax = (probe.IncomeTax + probe.Soli + probe.ChurchTax - all.IncomeTax - all.Soli - all.ChurchTax) / 1000
	}
	for i := range months {
		m := &months[i]
		Y := years[byYear[m.Month.Year()]]
		m.BaseNet = m.BaseGross * Y.BaseKeep
		m.BonusNet = m.BonusGross * Y.VariableKeep
		m.OneOffNet = m.OneOffGross * Y.VariableKeep
		m.HeldNet = m.HeldGross * Y.VariableKeep
		m.RefreshNet = m.RefreshGross * Y.VariableKeep
		m.Net = m.BaseNet + m.BonusNet + m.OneOffNet + m.HeldNet + m.RefreshNet
	}
	return years
}

func applySavings(months []ScheduleMonth, years []ScheduleYear, in ScheduleInput, start time.Time) {
	byYear := map[int]int{}
	for i, y := range years {
		byYear[y.Year] = i
	}
	rule := in.Savings
	for i := range months {
		m := &months[i]
		if m.Stopped {
			continue
		}
		cashNet := m.BaseNet + m.BonusNet + m.OneOffNet
		vestNet := m.HeldNet + m.RefreshNet
		if rule.Mode == "rate" {
			vestShare := rule.Rate
			if rule.RSUFull {
				vestShare = 1
			}
			m.CashSavings = rule.Rate * cashNet
			m.Savings = m.CashSavings + vestShare*vestNet
			m.RunningCosts = m.Net - m.Savings
		} else {
			m.RunningCosts = rule.RunningCostsMonthly * math.Pow(1+in.Assume.Inflation, float64(m.Month.Year()-start.Year()))
			m.CashSavings = cashNet - m.RunningCosts
			m.Savings = m.Net - m.RunningCosts
		}
		Y := &years[byYear[m.Month.Year()]]
		Y.Net += m.Net
		Y.Savings += m.Savings
		Y.RunningCosts += m.RunningCosts
	}
}
