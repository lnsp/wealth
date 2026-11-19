package analytics

import "math"

// Payroll constants beyond the retirement block in retirement_tax.go. Review
// them each January with the other legal values.
const (
	// § 9a S. 1 Nr. 1a EStG Arbeitnehmer-Pauschbetrag.
	ArbeitnehmerPauschbetrag = 1230.0
	// § 10 Abs. 4 S. 2 EStG: cap on sonstige Vorsorgeaufwendungen for employees.
	SonstigeVorsorgeCap = 1900.0
	// § 10 Abs. 1 Nr. 3 a S. 4 EStG: the KV share that funds Krankengeld isn't deductible.
	KVKrankengeldAbschlag = 0.04
	// § 341 SGB III Arbeitslosenversicherung, employee and employer half each.
	AVBeitragssatz2026 = 0.026
	// § 243 SGB V ermäßigter Beitragssatz: voluntary members without Krankengeld.
	KVErmaessigterBeitragssatz = 0.14
	// § 18 SGB IV Bezugsgröße 2026 per month; § 240 Abs. 4 SGB V: voluntary
	// members pay on at least a third of it.
	Bezugsgroesse2026 = 3955.0
	// § 55 Abs. 3 SGB XI: −0.25 pp for the second to the fifth child.
	PVKinderAbschlag = 0.0025
)

// Contribution ceilings (BBG RV/AV and KV/PV) per year. 2027:
// Sozialversicherungs-Rechengrößenverordnung 2027, BGBl. of 29 Oct 2026.
var enactedCeilings = map[int]struct{ RV, KV float64 }{
	2026: {101400, 69750},
	2027: {106200, 76500},
}

const (
	lastEnactedTariffYear  = 2026
	lastEnactedCeilingYear = 2027
	// § 32a Abs. 1 Nr. 5 EStG: the 45 % zone starts above 277,825 €; it has
	// not been indexed in recent years, so it stays flat.
	topRateThreshold = 277825.0
)

// LawAssumptions extrapolate the law past its last enacted year.
type LawAssumptions struct {
	Inflation  float64 // tariff zones and the Soli threshold
	WageGrowth float64 // contribution ceilings and the Bezugsgröße
}

// PayrollLaw is the law that applies to one calendar year.
type PayrollLaw struct {
	Year             int
	TariffIndex      float64 // § 32a zones scaled by this factor; 1 for enacted years
	SoliFreigrenze   float64
	RVCap, KVCap     float64 // yearly contribution ceilings
	MinVoluntaryBase float64 // yearly minimum base for voluntary GKV members
	TariffEnacted    bool
	CeilingsEnacted  bool
}

// PayrollLawFor returns the enacted law for a year, or extrapolates it.
func PayrollLawFor(year int, a LawAssumptions) PayrollLaw {
	l := PayrollLaw{Year: year, TariffIndex: 1, TariffEnacted: year <= lastEnactedTariffYear}
	if year > lastEnactedTariffYear {
		l.TariffIndex = math.Pow(1+a.Inflation, float64(year-lastEnactedTariffYear))
	}
	c, ok := enactedCeilings[year]
	l.CeilingsEnacted = ok
	switch {
	case ok:
	case year < lastEnactedTariffYear:
		c = enactedCeilings[lastEnactedTariffYear]
	default:
		base := enactedCeilings[lastEnactedCeilingYear]
		f := math.Pow(1+a.WageGrowth, float64(year-lastEnactedCeilingYear))
		c.RV, c.KV = base.RV*f, base.KV*f
	}
	l.RVCap, l.KVCap = c.RV, c.KV
	l.SoliFreigrenze = SoliFreigrenze2026 * l.TariffIndex
	wage := 1.0
	if year > 2026 {
		wage = math.Pow(1+a.WageGrowth, float64(year-2026))
	}
	l.MinVoluntaryBase = Bezugsgroesse2026 / 3 * 12 * wage
	return l
}

// incomeTaxIndexed is the 2026 § 32a tariff with every zone boundary scaled by
// k, except the 45 % threshold. At k = 1 it equals incomeTaxTariff2026.
func incomeTaxIndexed(zvE, k float64) float64 {
	x := math.Floor(zvE)
	if x > topRateThreshold {
		if k == 1 {
			return math.Floor(0.45*x - 19470.38)
		}
		return math.Floor(0.42*topRateThreshold - 11135.63*k + 0.45*(x-topRateThreshold))
	}
	y := x / k
	var t float64
	switch {
	case y <= Grundfreibetrag2026:
		return 0
	case y <= 17799:
		a := (y - Grundfreibetrag2026) / 10000
		t = (914.51*a + 1400) * a
	case y <= 69878:
		b := (y - 17799) / 10000
		t = (173.10*b+2397)*b + 1034.87
	default:
		t = 0.42*y - 11135.63
	}
	return math.Floor(k * t)
}

// PayrollProfile holds the personal inputs of the payroll calculation. Only
// single filing is modelled.
type PayrollProfile struct {
	Health            string // "gkv" or "pkv"
	Zusatzbeitrag     float64
	PKVPremiumMonthly float64 // incl. long-term care
	PKVBasicShare     float64 // deductible basic-cover share of the premium
	ChurchRate        float64 // 0, 0.08 or 0.09
	Children          int
}

// PayrollResult is the yearly tax bill (assessment) on a gross wage and the
// employee's social security. In private insurance KV holds the premium after
// the employer subsidy and PV is zero.
type PayrollResult struct {
	Gross           float64    `json:"gross"`
	TaxableIncome   float64    `json:"taxable_income"`
	IncomeTax       float64    `json:"income_tax"`
	Soli            float64    `json:"soli"`
	ChurchTax       float64    `json:"church_tax"`
	RV              float64    `json:"rv"`
	AV              float64    `json:"av"`
	KV              float64    `json:"kv"`
	PV              float64    `json:"pv"`
	EmployerSubsidy float64    `json:"employer_subsidy"`
	MaxSubsidy      float64    `json:"max_subsidy"`
	Net             float64    `json:"net"`
	Law             PayrollLaw `json:"-"`
}

// Deductions is everything between gross and net.
func (r PayrollResult) Deductions() float64 { return r.Gross - r.Net }

func pvEmployeeRate(children int) float64 {
	half := PVBeitragssatz2026 / 2
	if children <= 0 {
		return half + PVBeitragssatzKinderlos2026 - PVBeitragssatz2026
	}
	return half - float64(min(max(children-1, 0), 4))*PVKinderAbschlag
}

func pvFullRate(children int) float64 {
	if children <= 0 {
		return PVBeitragssatzKinderlos2026
	}
	return PVBeitragssatz2026 - float64(min(max(children-1, 0), 4))*PVKinderAbschlag
}

// Payroll computes income tax, Soli, church tax and the employee's RV, AV, KV
// and PV on a yearly gross wage. Social security on one-off payments is
// counted against the yearly ceiling, which is exact while regular pay is
// above the ceilings.
func Payroll(gross float64, law PayrollLaw, p PayrollProfile) PayrollResult {
	r := PayrollResult{Gross: gross, Net: gross, Law: law}
	r.MaxSubsidy = ((KVAllgemeinerBeitragssatz+KVZusatzbeitrag2026)/2 + PVBeitragssatz2026/2) * law.KVCap
	if gross <= 0 {
		return r
	}
	rvBase, kvBase := min(gross, law.RVCap), min(gross, law.KVCap)
	r.RV = RVBeitragssatz2026 / 2 * rvBase
	r.AV = AVBeitragssatz2026 / 2 * rvBase
	var basic float64
	if p.Health == "pkv" {
		premium := p.PKVPremiumMonthly * 12
		r.EmployerSubsidy = min(premium/2, r.MaxSubsidy)
		r.KV = premium - r.EmployerSubsidy
		basic = max(0, p.PKVBasicShare*premium-r.EmployerSubsidy)
	} else {
		r.KV = (KVAllgemeinerBeitragssatz + p.Zusatzbeitrag) / 2 * kvBase
		r.PV = pvEmployeeRate(p.Children) * kvBase
		basic = r.KV*(1-KVKrankengeldAbschlag) + r.PV
	}
	vorsorge := r.RV + max(basic, min(SonstigeVorsorgeCap, r.KV+r.PV+r.AV))
	zv := gross - ArbeitnehmerPauschbetrag - SonderausgabenPauschbetrag - vorsorge
	// Church tax is itself deductible (§ 10 Abs. 1 Nr. 4 EStG): iterate to the fixed point.
	for range 5 {
		r.TaxableIncome = max(0, zv-r.ChurchTax)
		r.IncomeTax = incomeTaxIndexed(r.TaxableIncome, law.TariffIndex)
		r.ChurchTax = p.ChurchRate * r.IncomeTax
	}
	if r.IncomeTax > law.SoliFreigrenze {
		r.Soli = min(r.IncomeTax*Solidaritaetszuschlag, (r.IncomeTax-law.SoliFreigrenze)*SoliMilderungszoneRate)
	}
	r.Net = gross - r.IncomeTax - r.Soli - r.ChurchTax - r.RV - r.AV - r.KV - r.PV
	return r
}

// PostFireHealth returns the yearly health and care insurance once employment
// ends and before the statutory pension: voluntary statutory insurance on the
// capital income (at least the minimum base, at most the ceiling), or the full
// private premium without the employer subsidy.
func PostFireHealth(capitalIncome float64, law PayrollLaw, p PayrollProfile) float64 {
	if p.Health == "pkv" {
		return p.PKVPremiumMonthly * 12
	}
	base := min(max(capitalIncome, law.MinVoluntaryBase), law.KVCap)
	return base * (KVErmaessigterBeitragssatz + p.Zusatzbeitrag + pvFullRate(p.Children))
}
