package analytics

import "math"

// Retirement-planning constants. Every legal or statistical value carries its
// source and the year it is valid for; review them each January and July.
// Planning assumptions (not law) are grouped at the end of the block.
const (
	// § 32a Abs. 1 EStG for VZ 2026 (Steuerfortentwicklungsgesetz): Grundfreibetrag
	// 12,348 €. Zone formulas are in IncomeTax2026.
	Grundfreibetrag2026 = 12348.0

	// SolzG 1995 § 3 Abs. 3 / § 4: no Soli up to 20,350 € income tax (40,700 €
	// joint) in 2026; above it, the levy is capped at 11.9 % of the excess.
	SoliFreigrenze2026     = 20350.0
	SoliMilderungszoneRate = 0.119

	// § 68 SGB VI, Rentenwertbestimmungsverordnung 2026: 42.52 € per
	// Entgeltpunkt per month from 1 July 2026 (40.79 € from 1 July 2025).
	AktuellerRentenwert = 42.52

	// § 35 SGB VI Regelaltersgrenze for anyone born 1964 or later.
	Regelaltersgrenze = 67
	// §§ 36, 236 SGB VI: the earliest old-age pension (langjährig Versicherte,
	// 35 years) starts at 63. § 77 Abs. 2 SGB VI: −0.3 % per month claimed early
	// (at most 48 months = −14.4 %), +0.5 % per month claimed late.
	EarliestStatutoryPensionAge   = 63
	StatutoryPensionEarlyPerMonth = 0.003
	StatutoryPensionLatePerMonth  = 0.005

	// § 241 SGB V general health-insurance rate; KVdR members pay half of it and
	// half of the Zusatzbeitrag on the statutory pension (§ 249a SGB V), and the
	// full rate on occupational pensions (§ 248 SGB V).
	KVAllgemeinerBeitragssatz = 0.146
	// § 242a SGB V average Zusatzbeitrag 2026, set by the BMG at 2.9 %.
	KVZusatzbeitrag2026 = 0.029
	// § 55 SGB XI Pflegeversicherung 2026: 3.6 %, 4.2 % for the childless.
	// Retirees pay it in full (§ 59 Abs. 1 SGB XI).
	PVBeitragssatz2026          = 0.036
	PVBeitragssatzKinderlos2026 = 0.042
	// § 226 Abs. 2 SGB V: occupational pensions up to 1/20 of the monthly
	// Bezugsgröße (3,955 € in 2026) are exempt — a Freibetrag for KV, a
	// Freigrenze for PV — 197.75 € a month in 2026.
	VersorgungsbezugFreibetrag2026 = 197.75

	// § 9a S. 1 Nr. 3 EStG Werbungskosten-Pauschbetrag on § 22 pension income,
	// § 10c EStG Sonderausgaben-Pauschbetrag.
	WerbungskostenPauschbetragRente = 102.0
	SonderausgabenPauschbetrag      = 36.0

	// § 3 Nr. 63 EStG: bAV contributions are tax-free up to 8 % of the BBG RV
	// (101,400 € in 2026 → 8,112 €), social-security-free up to 4 % (4,056 €).
	BBGRentenversicherung2026 = 101400.0
	BAVTaxFreeMax2026         = 8112.0
	BAVSocialSecurityFree2026 = 4056.0
	// § 1a Abs. 1a BetrAVG: the employer must pass on at least 15 % of converted
	// salary as far as it saves social-security contributions.
	BAVMinEmployerSubsidy = 0.15
	// § 158 SGB VI: statutory pension contribution 18.6 % (employee + employer).
	RVBeitragssatz2026 = 0.186
	// § 10 Abs. 3 EStG: Basisvorsorge cap = Höchstbeitrag knappschaftliche RV
	// (24.7 % × 124,800 €) = 30,826 € in 2026, 100 % deductible since 2023.
	// The cap is shared with the statutory pension contributions (employee and
	// employer share).
	RuerupCap2026 = 30826.0

	// Planning assumptions (not law).
	PlanningDefaultReturnPct    = 7.0 // nominal p.a., the projection default
	PlanningDefaultInflationPct = 2.0 // ECB medium-term target
	// ECB deposit facility rate: 2.50 % since September 2026 (unchanged on
	// 29 Oct 2026); used as the yield idle cash could earn in a savings account.
	PlanningCashRatePct = 2.5
	// Emergency reserve: months of expenses kept in cash (also in the frontend
	// planning constants).
	PlanningReserveMonths = 6
)

// IncomeTax2026 returns the § 32a EStG income tax for VZ 2026 on a taxable
// income (zu versteuerndes Einkommen). joint applies the splitting method
// (§ 32a Abs. 5). Both the income and the result are rounded down to full euros.
func IncomeTax2026(zvE float64, joint bool) float64 {
	if joint {
		return 2 * incomeTaxTariff2026(math.Floor(zvE/2))
	}
	return incomeTaxTariff2026(zvE)
}

func incomeTaxTariff2026(zvE float64) float64 {
	x := math.Floor(zvE)
	switch {
	case x <= Grundfreibetrag2026:
		return 0
	case x <= 17799:
		y := (x - Grundfreibetrag2026) / 10000
		return math.Floor((914.51*y + 1400) * y)
	case x <= 69878:
		z := (x - 17799) / 10000
		return math.Floor((173.10*z+2397)*z + 1034.87)
	case x <= 277825:
		return math.Floor(0.42*x - 11135.63)
	default:
		return math.Floor(0.45*x - 19470.38)
	}
}

// SolidarityLevy2026 returns the Solidaritätszuschlag on an income tax amount:
// zero up to the Freigrenze, then the lower of 5.5 % and 11.9 % of the excess.
func SolidarityLevy2026(incomeTax float64, joint bool) float64 {
	limit := SoliFreigrenze2026
	if joint {
		limit *= 2
	}
	if incomeTax <= limit {
		return 0
	}
	return math.Min(incomeTax*Solidaritaetszuschlag, (incomeTax-limit)*SoliMilderungszoneRate)
}

// PensionBesteuerungsanteil returns the taxable share of a statutory or Rürup
// (Basis-) pension by the year payments start, per the table in § 22 Nr. 1
// S. 3 a aa EStG as amended by the Wachstumschancengesetz (2024): 50 % up to
// 2005, +2 pp a year to 80 % in 2020, +1 pp to 82 % in 2022, then +0.5 pp a
// year from 82.5 % in 2023 to 100 % in 2058. The share is fixed for life.
func PensionBesteuerungsanteil(startYear int) float64 {
	switch {
	case startYear <= 2005:
		return 0.50
	case startYear <= 2020:
		return 0.50 + 0.02*float64(startYear-2005)
	case startYear <= 2022:
		return 0.80 + 0.01*float64(startYear-2020)
	case startYear >= 2058:
		return 1.0
	default:
		return 0.825 + 0.005*float64(startYear-2023)
	}
}

// ErtragsanteilPrivateRente returns the taxable Ertragsanteil of a private
// pension annuity by the age at which payments start (§ 22 Nr. 1 S. 3 a bb
// EStG table). Only the ages a pension plausibly starts at are tabulated
// exactly; younger ages use the table's value for 50.
func ErtragsanteilPrivateRente(startAge int) float64 {
	table := []struct{ maxAge, pct int }{
		{50, 30}, {52, 29}, {53, 28}, {54, 27}, {56, 26}, {57, 25}, {58, 24},
		{59, 23}, {61, 22}, {62, 21}, {63, 20}, {64, 19}, {66, 18}, {67, 17},
		{68, 16}, {70, 15}, {71, 14}, {73, 13}, {74, 12}, {75, 11}, {77, 10},
		{79, 9}, {80, 8}, {82, 7}, {84, 6}, {87, 5}, {91, 4}, {93, 3}, {96, 2},
	}
	for _, row := range table {
		if startAge <= row.maxAge {
			return float64(row.pct) / 100
		}
	}
	return 0.01
}

// StatutoryPensionAgeFactor returns the Zugangsfaktor for a statutory pension
// claimed at claimAge: −0.3 % per month before the Regelaltersgrenze (claims
// before 63 are not possible, so at most −14.4 %), +0.5 % per month after it.
func StatutoryPensionAgeFactor(claimAge int) float64 {
	if claimAge < EarliestStatutoryPensionAge {
		claimAge = EarliestStatutoryPensionAge
	}
	months := float64((claimAge - Regelaltersgrenze) * 12)
	if months < 0 {
		return 1 + months*StatutoryPensionEarlyPerMonth
	}
	return 1 + months*StatutoryPensionLatePerMonth
}

// StatutoryPensionHealthContribution returns the monthly KV + PV a KVdR member
// pays on a statutory pension: half the general rate and half the average
// Zusatzbeitrag, plus the full Pflegeversicherung rate.
func StatutoryPensionHealthContribution(monthlyGross float64, childless bool) float64 {
	if monthlyGross <= 0 {
		return 0
	}
	pv := PVBeitragssatz2026
	if childless {
		pv = PVBeitragssatzKinderlos2026
	}
	return monthlyGross * ((KVAllgemeinerBeitragssatz+KVZusatzbeitrag2026)/2 + pv)
}

// OccupationalPensionHealthContribution returns the monthly KV + PV on the sum
// of all occupational pensions (Versorgungsbezüge, § 229 SGB V): the full KV
// rate and Zusatzbeitrag on the part above the § 226 Abs. 2 Freibetrag, and the
// full PV rate on the whole amount once it exceeds the same Freigrenze.
// Private, Riester and Rürup pensions carry no contributions for KVdR members.
func OccupationalPensionHealthContribution(totalMonthly float64, childless bool) float64 {
	if totalMonthly <= 0 {
		return 0
	}
	kv := math.Max(0, totalMonthly-VersorgungsbezugFreibetrag2026) * (KVAllgemeinerBeitragssatz + KVZusatzbeitrag2026)
	pv := 0.0
	if totalMonthly > VersorgungsbezugFreibetrag2026 {
		rate := PVBeitragssatz2026
		if childless {
			rate = PVBeitragssatzKinderlos2026
		}
		pv = totalMonthly * rate
	}
	return kv + pv
}

// PensionIncomeTax returns the yearly income tax, Soli and church tax on the
// taxable part of all pension income together (§ 2 EStG: the tariff applies
// to total income, never per source). deductibleHealth is the year's KV/PV
// contributions, deductible as Sonderausgaben under § 10 Abs. 1 Nr. 3 EStG.
// churchRate is 0, 0.08 or 0.09 of the income tax.
func PensionIncomeTax(annualTaxable, deductibleHealth float64, joint bool, churchRate float64) float64 {
	if annualTaxable <= 0 {
		return 0
	}
	zvE := annualTaxable - WerbungskostenPauschbetragRente - SonderausgabenPauschbetrag - math.Max(0, deductibleHealth)
	if zvE <= 0 {
		return 0
	}
	est := IncomeTax2026(zvE, joint)
	return est + SolidarityLevy2026(est, joint) + est*math.Max(0, churchRate)
}

// CapitalGainsTaxOnWithdrawal returns the flat tax on selling `gross` euros of
// a portfolio whose gains make up gainShare of its value: only the gain is
// taxed (§ 20 Abs. 4 EStG), after the fund partial exemption tfs (§ 20 InvStG),
// with the saver's allowance deducted first. rate is CapitalIncomeTaxRate.
func CapitalGainsTaxOnWithdrawal(gross, gainShare, tfs, rate, allowance float64) float64 {
	if gross <= 0 || gainShare <= 0 {
		return 0
	}
	taxable := gross*math.Min(gainShare, 1)*(1-tfs) - math.Max(0, allowance)
	if taxable <= 0 {
		return 0
	}
	return taxable * rate
}

// GrossWithdrawalForNet returns the amount to sell so that `net` euros are left
// after CapitalGainsTaxOnWithdrawal.
func GrossWithdrawalForNet(net, gainShare, tfs, rate, allowance float64) float64 {
	if net <= 0 {
		return 0
	}
	k := math.Min(math.Max(gainShare, 0), 1) * (1 - tfs) // taxable euros per euro sold
	if k <= 0 || rate <= 0 || net*k <= allowance {
		return net
	}
	return (net - allowance*rate) / (1 - k*rate)
}
