package handler

import (
	"context"
	"math"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/lnsp/wealth/internal/analytics"
	"github.com/lnsp/wealth/internal/assets"
	db "github.com/lnsp/wealth/internal/database/generated"
	"github.com/lnsp/wealth/internal/market"
)

// Pure planning math shared by the projection (Net Worth + Planning), FIRE,
// Pension Gap, Life Events and Money Left on the Table handlers. The
// constants they rely on live in internal/analytics/retirement_tax.go.

// investableAccountType reports whether an account's balance counts towards
// investable wealth (cash + securities), the scope every projection uses.
// Property, loans and credit cards are outside it.
func investableAccountType(t string) bool {
	switch t {
	case "checking", "savings", "brokerage", "crypto", "pension", "precious_metals":
		return true
	}
	return false
}

// trailingContribution is the default monthly contribution of every
// projection: net external money added to investable accounts.
type trailingContribution struct {
	Monthly       float64 // (Deposits + InKind − Withdrawals) / MonthsCovered
	MonthsCovered float64 // 12, or fewer when the history is shorter
	Deposits      float64
	Withdrawals   float64
	InKind        float64 // RSU vests and other unmatched in-kind transfers, at FMV
}

// trailingNetContribution returns the average monthly net external
// contribution over the 12 months up to asOf, at investable scope:
// Σ deposits − Σ withdrawals on checking, savings, brokerage, pension, crypto
// and precious-metal accounts, plus in-kind grants (RSU vests) at their value
// on the vest date. Moves between own accounts (cash_transfer_*, matched
// transfer/transfer_out pairs) cancel and are ignored. With less than 12
// months of history the total is divided by the months actually covered.
// Amounts are taken as absolute values because some importers store
// withdrawals with a negative sign.
func trailingNetContribution(
	txns []db.ListTransactionsRow,
	acctTypes map[uuid.UUID]string,
	asOf time.Time,
	histFX []market.HistoricalFXRate,
	fxRates map[string]float64,
) trailingContribution {
	asOfDay := assets.CivilDate(asOf)
	cutoff := asOfDay.AddDate(-1, 0, 0)

	var inkind []analytics.InKindTransfer
	for _, t := range txns {
		if !investableAccountType(acctTypes[t.AccountID]) || !t.SecurityISIN.Valid {
			continue
		}
		if t.Type != "transfer" && t.Type != "transfer_out" {
			continue
		}
		inkind = append(inkind, analytics.InKindTransfer{
			ID: t.ID, Date: t.Date, Type: t.Type, ISIN: t.SecurityISIN.String,
			Quantity: numericToFloat(t.Quantity),
		})
	}
	washed := analytics.MatchInKindTransferPairs(inkind)

	var out trailingContribution
	var earliest time.Time
	for _, t := range txns {
		if !investableAccountType(acctTypes[t.AccountID]) {
			continue
		}
		d := assets.CivilDate(t.Date)
		if d.After(asOfDay) {
			continue
		}
		if earliest.IsZero() || d.Before(earliest) {
			earliest = d
		}
		if !d.After(cutoff) {
			continue
		}
		if _, ok := washed[t.ID]; ok {
			continue
		}
		hasISIN := t.SecurityISIN.Valid
		bucket := analytics.ClassifyForAttribution(t.Type, hasISIN, analytics.ScopePortfolio)
		if bucket != analytics.BucketContribution && bucket != analytics.BucketWithdrawal {
			continue
		}
		amt := math.Abs(convertAmountToEURAtDate(numericToFloat(t.Amount), t.Currency, t.Date, histFX, fxRates))
		switch {
		case bucket == analytics.BucketWithdrawal:
			out.Withdrawals += amt
		case hasISIN:
			out.InKind += amt
		default:
			out.Deposits += amt
		}
	}
	if earliest.IsZero() {
		return out
	}
	out.MonthsCovered = 12
	if earliest.After(cutoff) {
		out.MonthsCovered = math.Max(1, asOfDay.Sub(earliest).Hours()/24/30.4375)
	}
	out.Monthly = (out.Deposits + out.InKind - out.Withdrawals) / out.MonthsCovered
	return out
}

// contribForMonth returns the contribution paid at the end of month m
// (1-based). The first 12 months pay `base`; every later year grows by
// `growth`. The chart, sensitivity, milestones and FIRE date all use it so
// their timings agree.
func contribForMonth(base, growth float64, m int) float64 {
	if growth <= 0 || m <= 12 {
		return base
	}
	return base * math.Pow(1+growth, float64((m-1)/12))
}

// planMonthlyRate converts a nominal annual return in percent to a monthly rate.
func planMonthlyRate(annualPct float64) float64 {
	if annualPct <= -99.99 {
		annualPct = -99.99
	}
	return math.Pow(1+annualPct/100, 1.0/12.0) - 1
}

// planProjectFV compounds `cv` monthly at `annualPct` with contributions from
// contribForMonth for `months` months.
func planProjectFV(cv, mc, annualPct, growth float64, months int) float64 {
	mr := planMonthlyRate(annualPct)
	v := cv
	for m := 1; m <= months; m++ {
		v = v*(1+mr) + contribForMonth(mc, growth, m)
	}
	return v
}

// applyMonth grows value for one month and then adds a contribution (which
// raises the cost basis) or takes a withdrawal (which removes basis pro rata).
func applyMonth(value, basis, mr, flow float64) (float64, float64) {
	value *= 1 + mr
	if flow >= 0 {
		return value + flow, basis + flow
	}
	take := math.Min(-flow, math.Max(value, 0))
	if value > 0 {
		basis *= 1 - take/value
	}
	return value - take, basis
}

// planGainShare is the fraction of a portfolio's value that is unrealised gain.
func planGainShare(value, basis float64) float64 {
	if value <= 0 || basis >= value {
		return 0
	}
	if basis <= 0 {
		return 1
	}
	return 1 - basis/value
}

// fireParams describe one FIRE plan. Money amounts are in today's euros
// unless they say otherwise; rates are fractions.
type fireParams struct {
	CurrentValue   float64 // investable wealth today; also the starting cost basis
	MonthlyContrib float64 // nominal, months 1–12
	ContribGrowth  float64 // p.a., applied each 12 months
	AnnualReturn   float64 // nominal p.a.
	Inflation      float64 // p.a.
	Expenses       float64 // yearly spending the portfolio and the pension must cover
	Pension        float64 // yearly pension after income tax, paid from PensionAge
	CurrentAge     int     // 0: unknown, the pension is counted from the independence date
	PensionAge     int
	SWR            float64 // withdrawal rate
	TaxRate        float64 // analytics.CapitalIncomeTaxRate
	TFS            float64 // Teilfreistellung of the portfolio
	Allowance      float64 // Sparer-Pauschbetrag per year, nominal (not indexed)
	MaxMonths      int
}

func (p fireParams) bridgeModelled() bool {
	return p.CurrentAge > 0 && p.PensionAge > 0 && p.Pension > 0
}

// longRunGap is the yearly spending the portfolio covers once the pension runs.
func (p fireParams) longRunGap() float64 {
	return math.Max(0, p.Expenses-p.Pension)
}

// bridgeYearsAt returns the years between independence at month m and the
// pension start, during which the portfolio also replaces the pension.
func (p fireParams) bridgeYearsAt(m int) float64 {
	if !p.bridgeModelled() {
		return 0
	}
	return math.Max(0, float64(p.PensionAge)-(float64(p.CurrentAge)+float64(m)/12))
}

func (p fireParams) inflator(months float64) float64 {
	return math.Pow(1+p.Inflation, months/12)
}

// netSpendNominal is the spending the portfolio must fund in retirement year y
// after independence at month m, in euros of that year.
func (p fireParams) netSpendNominal(m, y int) float64 {
	infl := p.inflator(float64(m + 12*y))
	if p.bridgeModelled() && float64(p.CurrentAge)+float64(m)/12+float64(y) < float64(p.PensionAge) {
		return p.Expenses * infl
	}
	return p.longRunGap() * infl
}

// targetAt returns the portfolio needed for independence at month m, in euros
// of that month: the long-run gap grossed up for tax on the gain share and
// divided by the withdrawal rate, plus a reserve that replaces the pension
// until it starts (discounted at the expected return). The gain share comes
// from the projected value and basis at m.
func (p fireParams) targetAt(m int, value, basis float64) (target, bridge float64) {
	if p.SWR <= 0 {
		return math.Inf(1), 0
	}
	g := planGainShare(value, basis)
	infl := p.inflator(float64(m))
	gap := p.longRunGap()
	gross := func(net float64) float64 {
		return analytics.GrossWithdrawalForNet(net, g, p.TFS, p.TaxRate, p.Allowance)
	}
	longRun := gross(gap*infl) / p.SWR
	years := p.bridgeYearsAt(m)
	for k := 0; float64(k) < years; k++ {
		weight := math.Min(1, years-float64(k))
		f := infl * math.Pow(1+p.Inflation, float64(k))
		extra := gross(p.Expenses*f) - gross(gap*f)
		bridge += weight * extra / math.Pow(1+p.AnnualReturn, float64(k))
	}
	return longRun + bridge, bridge
}

type fireYear struct {
	Year           int     `json:"year"`
	GrossWithdraw  float64 `json:"gross_withdrawal"`
	EstimatedTax   float64 `json:"estimated_tax"`
	NetReceived    float64 `json:"net_received"`
	EffectiveRate  float64 `json:"effective_rate_pct"`
	RemainingValue float64 `json:"remaining_value"`
}

type firePoint struct {
	Date  string  `json:"date"`
	Value float64 `json:"value"`
}

// fireResult is the outcome of simulateFIRE. Amounts are in today's euros
// unless the field name says nominal.
type fireResult struct {
	Reached         bool
	Month           int // months from now until independence; -1 if not reached
	Target          float64
	TargetNominal   float64
	Bridge          float64
	BridgeYears     float64
	ValueNominal    float64
	BasisNominal    float64
	GainShare       float64
	FirstYearGross  float64
	FirstYearTax    float64
	LongevityMonths int // months the portfolio lasts after independence, capped at 480
	CumulativeTax   float64
	Years           []fireYear
	Series          []firePoint
}

// simulateFIRE projects accumulation month by month (same contributions as the
// chart) until the portfolio reaches targetAt, then draws an inflation-indexed,
// tax-grossed-up withdrawal for up to 40 years. Basis = starting value + every
// contribution; each withdrawal removes basis pro rata.
func simulateFIRE(p fireParams, start time.Time) fireResult {
	res := fireResult{Month: -1}
	mr := planMonthlyRate(p.AnnualReturn * 100)
	v, basis := p.CurrentValue, math.Max(p.CurrentValue, 0)
	for m := 0; m <= p.MaxMonths; m++ {
		if m > 0 {
			v, basis = applyMonth(v, basis, mr, contribForMonth(p.MonthlyContrib, p.ContribGrowth, m))
		}
		t, bridge := p.targetAt(m, v, basis)
		res.TargetNominal = t
		res.Target = t / p.inflator(float64(m))
		res.Bridge = bridge / p.inflator(float64(m))
		res.BridgeYears = p.bridgeYearsAt(m)
		res.ValueNominal, res.BasisNominal = v, basis
		res.GainShare = planGainShare(v, basis)
		if v >= t {
			res.Reached = true
			res.Month = m
			break
		}
	}
	if !res.Reached {
		return res
	}

	m := res.Month
	fiDate := addMonthsFirstOfMonth(start, m)
	dv, db := res.ValueNominal, res.BasisNominal
	res.Series = append(res.Series, firePoint{Date: fiDate.Format("2006-01"), Value: math.Round(dv / p.inflator(float64(m)))})
	months := 0
	depleted := false
	for y := 0; y < 40 && !depleted; y++ {
		need := p.netSpendNominal(m, y)
		g := planGainShare(dv, db)
		gross := analytics.GrossWithdrawalForNet(need, g, p.TFS, p.TaxRate, p.Allowance)
		tax := gross - need
		monthly := gross / 12
		withdrawn := 0.0
		for k := 1; k <= 12; k++ {
			dv *= 1 + mr
			take := math.Min(monthly, dv)
			if dv > 0 {
				db *= 1 - take/dv
			}
			dv -= take
			withdrawn += take
			months++
			if take < monthly-1e-6 || dv <= 1e-6 {
				dv, depleted = 0, true
			}
			if depleted || months%3 == 0 {
				res.Series = append(res.Series, firePoint{
					Date:  addMonthsFirstOfMonth(fiDate, months).Format("2006-01"),
					Value: math.Round(dv / p.inflator(float64(m+months))),
				})
			}
			if depleted {
				break
			}
		}
		frac := 1.0
		if gross > 0 {
			frac = withdrawn / gross
		}
		defl := p.inflator(float64(m + 12*y))
		row := fireYear{
			Year:           fiDate.Year() + y,
			GrossWithdraw:  math.Round(withdrawn / defl),
			EstimatedTax:   math.Round(tax*frac/defl*100) / 100,
			NetReceived:    math.Round(need*frac/defl*100) / 100,
			RemainingValue: math.Round(dv / p.inflator(float64(m+months))),
		}
		if gross > 0 {
			row.EffectiveRate = math.Round(tax/gross*10000) / 100
		}
		if y == 0 {
			res.FirstYearGross = gross / defl
			res.FirstYearTax = tax / defl
		}
		res.CumulativeTax += tax * frac / defl
		res.Years = append(res.Years, row)
	}
	res.LongevityMonths = months
	return res
}

// liquidationStep is one sale in a "what to sell" plan.
type liquidationStep struct {
	AccountID   string  `json:"account_id,omitempty"`
	ISIN        string  `json:"isin"`
	Name        string  `json:"name"`
	BuyDate     string  `json:"buy_date"`
	SellAmount  float64 `json:"sell_amount"`
	TaxDue      float64 `json:"tax_due"`
	NetProceeds float64 `json:"net_proceeds"`
	EffRate     float64 `json:"effective_rate_pct"`
}

type liquidationPlan struct {
	Steps         []liquidationStep
	TotalTax      float64
	TotalProceeds float64
}

// planLiquidation picks lots to sell for `cashNeeded` net of tax. Within one
// depot and ISIN the oldest lot must go first (FIFO, § 20 Abs. 4 S. 7 EStG),
// so only the head lot of each (account, ISIN) queue is a candidate; among the
// candidates the one with the lowest tax per euro raised is sold next (losses
// first). The plan's tax nets gains and losses by pot: share losses offset
// only share gains (§ 20 Abs. 6 S. 4), other losses offset anything.
// Crypto and physical gold held over a year are tax-free (§ 23); within a year
// they are approximated at the flat rate.
func planLiquidation(lots []analytics.TaxLot, assetClass map[string]string, cashNeeded, rate float64, asOf time.Time) liquidationPlan {
	type queue struct {
		lots []analytics.TaxLot
		sold []float64 // fraction of each lot already sold
	}
	queues := map[string]*queue{}
	var order []string
	for _, l := range lots {
		if l.CurrentValue <= 0 {
			continue
		}
		k := l.AccountID + "|" + l.ISIN
		q, ok := queues[k]
		if !ok {
			q = &queue{}
			queues[k] = q
			order = append(order, k)
		}
		q.lots = append(q.lots, l)
		q.sold = append(q.sold, 0)
	}
	for _, k := range order {
		q := queues[k]
		idx := make([]int, len(q.lots))
		for i := range idx {
			idx[i] = i
		}
		sort.SliceStable(idx, func(a, b int) bool { return q.lots[idx[a]].BuyDate < q.lots[idx[b]].BuyDate })
		sorted := make([]analytics.TaxLot, len(q.lots))
		for i, j := range idx {
			sorted[i] = q.lots[j]
		}
		q.lots = sorted
	}

	taxable := func(l analytics.TaxLot) float64 { // taxable P/L of the whole lot
		if analytics.IsPrivateSaleAssetClass(assetClass[l.ISIN]) {
			if bought, err := time.Parse("2006-01-02", l.BuyDate); err == nil && !bought.AddDate(1, 0, 0).After(asOf) {
				return 0
			}
			return math.Max(0, l.UnrealizedPL) // § 23 losses never offset § 20 income
		}
		return l.UnrealizedPL * (1 - l.TFSRate)
	}

	var plan liquidationPlan
	var sharePot, otherPot, gross float64
	remaining := cashNeeded
	for remaining > 0.005 {
		bestKey := ""
		bestRate := math.Inf(1)
		bestDate := ""
		for _, k := range order {
			q := queues[k]
			if len(q.lots) == 0 {
				continue
			}
			l := q.lots[0]
			r := taxable(l) * rate / l.CurrentValue
			if r < bestRate-1e-12 || (math.Abs(r-bestRate) <= 1e-12 && l.BuyDate < bestDate) {
				bestKey, bestRate, bestDate = k, r, l.BuyDate
			}
		}
		if bestKey == "" {
			break
		}
		q := queues[bestKey]
		l := q.lots[0]
		avail := l.CurrentValue * (1 - q.sold[0])
		perEuroTax := math.Max(0, bestRate)
		sell := math.Min(avail, remaining/(1-perEuroTax))
		frac := sell / l.CurrentValue
		lotTaxable := taxable(l) * frac
		stepTax := math.Max(0, lotTaxable*rate)
		if analytics.IsShareAssetClass(assetClass[l.ISIN]) {
			sharePot += lotTaxable
		} else {
			otherPot += lotTaxable
		}
		effRate := 0.0
		if l.UnrealizedPL*frac > 0 {
			effRate = stepTax / (l.UnrealizedPL * frac) * 100
		}
		plan.Steps = append(plan.Steps, liquidationStep{
			AccountID: l.AccountID, ISIN: l.ISIN, Name: l.Name, BuyDate: l.BuyDate,
			SellAmount:  math.Round(sell*100) / 100,
			TaxDue:      math.Round(stepTax*100) / 100,
			NetProceeds: math.Round((sell-stepTax)*100) / 100,
			EffRate:     math.Round(effRate*100) / 100,
		})
		gross += sell
		remaining -= sell - stepTax
		q.sold[0] += frac
		if q.sold[0] >= 1-1e-9 {
			q.lots, q.sold = q.lots[1:], q.sold[1:]
		}
	}
	base := math.Max(0, sharePot) + otherPot
	plan.TotalTax = math.Max(0, base) * rate
	plan.TotalProceeds = gross - plan.TotalTax
	return plan
}

// --- Pension gap ---

type pensionSourceInput struct {
	Type         string  `json:"type"` // gesetzliche, bav, riester, ruerup, private, other
	Label        string  `json:"label"`
	MonthlyGross float64 `json:"monthly_gross"`   // today's euros
	Rentenpunkte float64 `json:"rentenpunkte"`    // statutory pension only
	StartAge     int     `json:"start_age"`       // 0: the retirement age
	TaxPortion   float64 `json:"tax_portion_pct"` // ≤ 0: derived from type and start year
}

type pensionSourceResult struct {
	Type            string  `json:"type"`
	Label           string  `json:"label"`
	MonthlyGross    float64 `json:"monthly_gross"`
	MonthlyTax      float64 `json:"monthly_tax"` // income tax + health insurance, as before
	MonthlyIncomeTx float64 `json:"monthly_income_tax"`
	MonthlyHealth   float64 `json:"monthly_health"`
	MonthlyNet      float64 `json:"monthly_net"`
	AnnualGross     float64 `json:"annual_gross"`
	AnnualNet       float64 `json:"annual_net"`
	StartAge        int     `json:"start_age"`
	TaxableSharePct float64 `json:"taxable_share_pct"`
	AgeFactor       float64 `json:"age_factor,omitempty"`
	StartsLater     bool    `json:"starts_after_retirement"`
}

type pensionContext struct {
	CurrentAge int
	NowYear    int
	Joint      bool
	ChurchRate float64
	Childless  bool
}

func pensionSourceLabel(t, label string) string {
	if label != "" {
		return label
	}
	switch t {
	case "gesetzliche":
		return "Gesetzliche Rente"
	case "bav":
		return "Betriebliche Altersvorsorge"
	case "riester":
		return "Riester-Rente"
	case "ruerup":
		return "Rürup / Basisrente"
	case "private":
		return "Private Rentenversicherung"
	}
	return "Sonstige Einkünfte"
}

// pensionsAt evaluates every source for someone who stops working at
// `retireAge`. A statutory pension is claimed at the source's start age (or
// the retirement age), never before 63, with the § 77 SGB VI factor. Sources
// whose start age is after `asOfAge` are listed but not paid. Income tax is
// levied on the taxable parts of all paid sources together, with health
// contributions deducted as Sonderausgaben; it is allocated back to sources by
// taxable amount. Amounts are monthly, in today's euros (the tariff is assumed
// to be indexed with inflation).
func pensionsAt(sources []pensionSourceInput, retireAge, asOfAge int, followRetirement bool, pc pensionContext) ([]pensionSourceResult, float64, float64) {
	type row struct {
		res     pensionSourceResult
		taxable float64
		health  float64
		paid    bool
	}
	rows := make([]row, 0, len(sources))
	bavTotal := 0.0
	for _, src := range sources {
		start := src.StartAge
		if start <= 0 || (followRetirement && src.Type == "gesetzliche") {
			start = retireAge
		}
		gross := src.MonthlyGross
		factor := 0.0
		if src.Type == "gesetzliche" {
			if start < analytics.EarliestStatutoryPensionAge {
				start = analytics.EarliestStatutoryPensionAge
			}
			if src.Rentenpunkte > 0 {
				factor = analytics.StatutoryPensionAgeFactor(start)
				gross = src.Rentenpunkte * analytics.AktuellerRentenwert * factor
			}
		}
		if gross <= 0 {
			continue
		}
		startYear := pc.NowYear + (start - pc.CurrentAge)
		share := src.TaxPortion / 100
		if src.TaxPortion <= 0 {
			switch src.Type {
			case "gesetzliche", "ruerup":
				share = analytics.PensionBesteuerungsanteil(startYear)
			case "private":
				share = analytics.ErtragsanteilPrivateRente(start)
			default: // bav, riester and other income are taxed in full
				share = 1
			}
		}
		share = math.Min(1, math.Max(0, share))
		paid := start <= asOfAge
		r := row{
			res: pensionSourceResult{
				Type: src.Type, Label: pensionSourceLabel(src.Type, src.Label),
				MonthlyGross: gross, StartAge: start, TaxableSharePct: math.Round(share*1000) / 10,
				AgeFactor: math.Round(factor*1000) / 1000, StartsLater: !paid,
			},
			taxable: gross * share,
			paid:    paid,
		}
		if paid {
			switch src.Type {
			case "gesetzliche":
				r.health = analytics.StatutoryPensionHealthContribution(gross, pc.Childless)
			case "bav":
				bavTotal += gross
			}
		}
		rows = append(rows, r)
	}
	// Occupational pensions share one § 226 Freibetrag; split the
	// contribution pro rata.
	if bavTotal > 0 {
		bavHealth := analytics.OccupationalPensionHealthContribution(bavTotal, pc.Childless)
		for i := range rows {
			if rows[i].paid && rows[i].res.Type == "bav" {
				rows[i].health = bavHealth * rows[i].res.MonthlyGross / bavTotal
			}
		}
	}
	taxableTotal, healthTotal := 0.0, 0.0
	for _, r := range rows {
		if r.paid {
			taxableTotal += r.taxable
			healthTotal += r.health
		}
	}
	annualTax := analytics.PensionIncomeTax(taxableTotal*12, healthTotal*12, pc.Joint, pc.ChurchRate)
	out := make([]pensionSourceResult, 0, len(rows))
	totalGross, totalNet := 0.0, 0.0
	for _, r := range rows {
		res := r.res
		if r.paid {
			incomeTax := 0.0
			if taxableTotal > 0 {
				incomeTax = annualTax / 12 * r.taxable / taxableTotal
			}
			res.MonthlyIncomeTx = incomeTax
			res.MonthlyHealth = r.health
			res.MonthlyTax = incomeTax + r.health
			res.MonthlyNet = res.MonthlyGross - res.MonthlyTax
			totalGross += res.MonthlyGross
			totalNet += res.MonthlyNet
		}
		res.AnnualGross = math.Round(res.MonthlyGross * 12)
		res.AnnualNet = math.Round(res.MonthlyNet * 12)
		res.MonthlyGross = math.Round(res.MonthlyGross*100) / 100
		res.MonthlyTax = math.Round(res.MonthlyTax*100) / 100
		res.MonthlyIncomeTx = math.Round(res.MonthlyIncomeTx*100) / 100
		res.MonthlyHealth = math.Round(res.MonthlyHealth*100) / 100
		res.MonthlyNet = math.Round(res.MonthlyNet*100) / 100
		out = append(out, res)
	}
	return out, totalGross, totalNet
}

// portfolioAtRetirement projects the portfolio to `months` from now (nominal,
// the same contributions as the projection) and returns the monthly SWR
// withdrawal before and after tax on the gain share, deflated to today's euros.
type portfolioDraw struct {
	ValueNominal float64
	ValueReal    float64
	GrossMonthly float64 // today's euros
	NetMonthly   float64 // today's euros
}

func portfolioAtRetirement(currentValue, monthlyContrib, annualReturn, inflation, swr, taxRate, tfs, allowance float64, months int) portfolioDraw {
	if months < 0 {
		months = 0
	}
	v := math.Max(0, planProjectFV(currentValue, monthlyContrib, annualReturn*100, 0, months))
	basis := math.Max(0, currentValue+monthlyContrib*float64(months))
	defl := math.Pow(1+inflation, float64(months)/12)
	w := v * swr
	tax := analytics.CapitalGainsTaxOnWithdrawal(w, planGainShare(v, basis), tfs, taxRate, allowance)
	return portfolioDraw{
		ValueNominal: v,
		ValueReal:    v / defl,
		GrossMonthly: w / defl / 12,
		NetMonthly:   (w - tax) / defl / 12,
	}
}

// extraSavingsToClose returns the additional constant monthly saving (nominal)
// whose portfolio, after `months`, pays `gapMonthly` (today's euros) after tax
// at the withdrawal rate. The new money's gain share is 1 − paid-in / value;
// the saver's allowance is assumed to be used by the existing portfolio.
func extraSavingsToClose(gapMonthly, annualReturn, inflation, swr, taxRate, tfs float64, months int) float64 {
	if gapMonthly <= 0 || months <= 0 || swr <= 0 {
		return 0
	}
	mr := planMonthlyRate(annualReturn * 100)
	factor := float64(months)
	if math.Abs(mr) > 1e-12 {
		factor = (math.Pow(1+mr, float64(months)) - 1) / mr
	}
	if factor <= 0 {
		return 0
	}
	g := planGainShare(factor, float64(months))
	netPerEuro := swr * (1 - g*(1-tfs)*taxRate)
	defl := math.Pow(1+inflation, float64(months)/12)
	return gapMonthly * 12 * defl / (factor * netPerEuro)
}

// --- Money left on the table ---

// idleCashCost is the yearly return forgone on cash above the reserve: the
// spread between the expected portfolio return and what the cash earns in a
// savings account.
func idleCashCost(totalCash, reserve, expectedReturnPct, cashRatePct float64) (excess, cost float64) {
	excess = math.Max(0, totalCash-math.Max(0, reserve))
	spread := math.Max(0, expectedReturnPct-cashRatePct) / 100
	return excess, excess * spread
}

// rebalanceTurnover returns the euros that would have to move to reach the
// target weights: half the sum of absolute drifts (every euro sold is a euro
// bought). Holdings without a target count with target 0 when any target is
// set; targets without a holding count with actual 0.
func rebalanceTurnover(valueByISIN, targetPctByISIN map[string]float64) float64 {
	total := 0.0
	for _, v := range valueByISIN {
		total += v
	}
	if total <= 0 || len(targetPctByISIN) == 0 {
		return 0
	}
	drift := 0.0
	for isin, v := range valueByISIN {
		drift += math.Abs(v/total*100 - targetPctByISIN[isin])
	}
	for isin, t := range targetPctByISIN {
		if _, held := valueByISIN[isin]; !held {
			drift += math.Abs(t)
		}
	}
	return total * drift / 100 / 2
}

// fireDrawdownJSON is the `drawdown` object of the projection response. The
// original fields keep their names; amounts are in today's euros.
func fireDrawdownJSON(fp fireParams, fr fireResult, now time.Time) map[string]any {
	fireDate := ""
	yearsToFire := -1
	if fr.Reached {
		fireDate = addMonthsFirstOfMonth(now, fr.Month).Format("2006-01")
		yearsToFire = fr.Month / 12
	}
	series := fr.Series
	if series == nil {
		series = []firePoint{}
	}
	years := fr.Years
	if years == nil {
		years = []fireYear{}
	}
	pensionFromAge := 0
	if fp.bridgeModelled() {
		pensionFromAge = fp.PensionAge
	}
	coverage := math.Min(float64(fr.LongevityMonths), 480) / 480 * 100
	return map[string]any{
		"reached":             fr.Reached,
		"series":              series,
		"fire_number":         math.Round(fr.Target),
		"fire_number_nominal": math.Round(fr.TargetNominal),
		"fire_date":           fireDate,
		"years_to_fire":       yearsToFire,
		"months_to_fire":      fr.Month,
		"longevity_years":     fr.LongevityMonths / 12,
		// Share of a 40-year retirement this deterministic simulation funds;
		// not a probability of success.
		"success_rate":          math.Round(coverage*10) / 10,
		"tax_breakdown":         years,
		"cumulative_tax":        math.Round(fr.CumulativeTax),
		"first_year_withdrawal": math.Round(fr.FirstYearGross),
		"first_year_tax":        math.Round(fr.FirstYearTax),
		"spending_gap":          math.Round(fp.longRunGap()),
		"bridge_years":          math.Round(fr.BridgeYears*10) / 10,
		"bridge_reserve":        math.Round(fr.Bridge),
		"pension_from_age":      pensionFromAge,
		"gain_share_pct":        math.Round(fr.GainShare*1000) / 10,
		"teilfreistellung_pct":  math.Round(fp.TFS*1000) / 10,
		"tax_rate_pct":          math.Round(fp.TaxRate*100000) / 1000,
		"allowance":             fp.Allowance,
		"inflation_pct":         math.Round(fp.Inflation*10000) / 100,
		"swr_pct":               math.Round(fp.SWR*10000) / 100,
		"in_todays_euros":       true,
	}
}

// accountTypesByID maps account IDs to their type for scope filtering.
func (h *PortfolioHandler) accountTypesByID(ctx context.Context) map[uuid.UUID]string {
	accts, _ := h.queries.ListAccounts(ctx)
	out := make(map[uuid.UUID]string, len(accts))
	for _, a := range accts {
		out[a.ID] = a.Type
	}
	return out
}

// portfolioTFS returns the value-weighted Teilfreistellung of the current
// securities (30 % equity funds, 15 % mixed, 0 % stocks and bonds), or the
// equity-fund rate when nothing is held yet.
func (h *PortfolioHandler) portfolioTFS(ctx context.Context, fxRates map[string]float64) float64 {
	holdings, err := h.queries.ListCurrentHoldings(ctx)
	if err != nil || len(holdings) == 0 {
		return analytics.TeilfreistellungEquity
	}
	secs, _ := h.queries.ListSecurities(ctx)
	tfs := make(map[string]float64, len(secs))
	for _, s := range secs {
		tfs[s.ISIN] = securityTFSRate(s)
	}
	pm := h.loadPriceMap(ctx)
	total, weighted := 0.0, 0.0
	for _, hld := range holdings {
		p := pm[hld.SecurityISIN]
		v := numericToFloat(hld.Quantity) * convertAmountToEUR(numericToFloat(p.Close), p.Currency, fxRates)
		if v <= 0 {
			continue
		}
		total += v
		weighted += v * tfs[hld.SecurityISIN]
	}
	if total <= 0 {
		return analytics.TeilfreistellungEquity
	}
	return weighted / total
}

// --- Life events ---

// monthsBetween returns the whole calendar months from `from`'s month to
// `to`'s month ("2028-11" is 24 months after any day in November 2026),
// never negative.
func monthsBetween(from, to time.Time) int {
	n := (to.Year()-from.Year())*12 + int(to.Month()) - int(from.Month())
	if n < 0 {
		return 0
	}
	return n
}

// lifeEventSim is one event placed on the month grid.
type lifeEventSim struct {
	MonthOffset    int
	CashImpact     float64
	TaxImpact      float64
	ContribChange  float64
	RecurringCost  float64
	DurationMonths int // 0 = permanent
}

// simulateLifeEvents returns investable value and cost basis after each month
// 0..months. In its month an event's one-off amount (minus tax) is booked,
// its contribution change starts and its recurring cost starts; a recurring
// cost with duration n is paid n times.
func simulateLifeEvents(cv, contrib, mr float64, events []lifeEventSim, months int) (values, bases []float64) {
	v, basis := math.Max(cv, 0), math.Max(cv, 0)
	active := contrib
	left := map[int]int{}
	values = make([]float64, 0, months+1)
	bases = make([]float64, 0, months+1)
	for m := 0; m <= months; m++ {
		for i, e := range events {
			if e.MonthOffset != m {
				continue
			}
			v, basis = applyMonth(v, basis, 0, e.CashImpact-e.TaxImpact)
			active += e.ContribChange
			if e.RecurringCost != 0 {
				dur := e.DurationMonths
				if dur <= 0 {
					dur = -1 // permanent
				}
				left[i] = dur
			}
		}
		recurring := 0.0
		for i, n := range left {
			recurring += events[i].RecurringCost
			if n > 0 {
				left[i]--
				if left[i] == 0 {
					delete(left, i)
				}
			}
		}
		rate := mr
		flow := active - recurring
		if m == 0 {
			rate, flow = 0, -recurring
		}
		v, basis = applyMonth(v, basis, rate, flow)
		values = append(values, v)
		bases = append(bases, basis)
	}
	return values, bases
}

// fireMonthOnPath returns the first month whose value reaches the FIRE target
// for that month, or -1.
func fireMonthOnPath(p fireParams, values, bases []float64) int {
	for m := range values {
		if t, _ := p.targetAt(m, values[m], bases[m]); values[m] >= t {
			return m
		}
	}
	return -1
}
