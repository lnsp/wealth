package analytics

import (
	"math"
	"testing"
)

func TestIncomeTax2026_Tariff(t *testing.T) {
	cases := []struct {
		zvE   float64
		joint bool
		want  float64
	}{
		{12348, false, 0},      // Grundfreibetrag 2026
		{12349, false, 0},      // first euro above it: 0.14 € rounds down
		{17799, false, 1034},   // end of zone 2
		{20000, false, 1570},   // zone 3
		{69878, false, 18213},  // end of zone 3
		{100000, false, 30864}, // 42 % zone
		{300000, false, 115529},
		{40000, true, 3140}, // splitting: 2 × tariff(20,000)
	}
	for _, c := range cases {
		if got := IncomeTax2026(c.zvE, c.joint); got != c.want {
			t.Errorf("IncomeTax2026(%v, joint=%v) = %v, want %v", c.zvE, c.joint, got, c.want)
		}
	}
}

func TestSolidarityLevy2026(t *testing.T) {
	if got := SolidarityLevy2026(20350, false); got != 0 {
		t.Errorf("at the Freigrenze: %v, want 0", got)
	}
	// Milderungszone: 11.9 % of the excess is lower than 5.5 % of the tax.
	if got := SolidarityLevy2026(25000, false); math.Abs(got-553.35) > 0.01 {
		t.Errorf("Soli(25,000) = %v, want 553.35", got)
	}
	if got := SolidarityLevy2026(40000, false); math.Abs(got-2200) > 0.01 {
		t.Errorf("Soli(40,000) = %v, want 2,200", got)
	}
	if got := SolidarityLevy2026(40000, true); got != 0 {
		t.Errorf("joint Soli(40,000) = %v, want 0", got)
	}
}

// Audit C5(c): the old formula 50 + (year − 2005) was wrong; the statutory
// table (Wachstumschancengesetz) rises 0.5 pp a year from 82.5 % in 2023.
func TestPensionBesteuerungsanteil_StatutoryTable(t *testing.T) {
	cases := map[int]float64{
		2005: 50, 2010: 60, 2020: 80, 2021: 81, 2022: 82, 2023: 82.5,
		2026: 84, 2030: 86, 2040: 91, 2057: 99.5, 2058: 100, 2070: 100,
	}
	for year, want := range cases {
		if got := PensionBesteuerungsanteil(year) * 100; math.Abs(got-want) > 1e-9 {
			t.Errorf("Besteuerungsanteil(%d) = %v %%, want %v %%", year, got, want)
		}
	}
}

func TestErtragsanteilPrivateRente(t *testing.T) {
	cases := map[int]float64{60: 22, 62: 21, 63: 20, 65: 18, 66: 18, 67: 17, 70: 15}
	for age, want := range cases {
		if got := ErtragsanteilPrivateRente(age) * 100; math.Abs(got-want) > 1e-9 {
			t.Errorf("Ertragsanteil(%d) = %v %%, want %v %%", age, got, want)
		}
	}
}

// Audit W2: early retirement can't take the statutory pension before 63 and
// costs at most 14.4 %.
func TestStatutoryPensionAgeFactor(t *testing.T) {
	cases := map[int]float64{55: 0.856, 60: 0.856, 63: 0.856, 65: 0.928, 67: 1, 68: 1.06, 70: 1.18}
	for age, want := range cases {
		if got := StatutoryPensionAgeFactor(age); math.Abs(got-want) > 1e-9 {
			t.Errorf("factor(%d) = %v, want %v", age, got, want)
		}
	}
}

// Audit C5(d): a flat 7.7 % on every source was wrong.
func TestRetireeHealthContributions(t *testing.T) {
	// Statutory: 7.3 % + 1.45 % + PV 3.6 % = 12.35 %.
	if got := StatutoryPensionHealthContribution(1000, false); math.Abs(got-123.5) > 1e-9 {
		t.Errorf("statutory KV/PV on 1,000 = %v, want 123.50", got)
	}
	if got := StatutoryPensionHealthContribution(1000, true); math.Abs(got-129.5) > 1e-9 {
		t.Errorf("childless statutory KV/PV on 1,000 = %v, want 129.50", got)
	}
	// bAV: full 17.5 % above the 197.75 € Freibetrag, PV on the whole amount.
	want := (500-197.75)*0.175 + 500*0.036
	if got := OccupationalPensionHealthContribution(500, false); math.Abs(got-want) > 1e-9 {
		t.Errorf("bAV KV/PV on 500 = %v, want %v", got, want)
	}
	if got := OccupationalPensionHealthContribution(150, false); got != 0 {
		t.Errorf("bAV below the Freigrenze = %v, want 0", got)
	}
}

// Audit C7: a 24,000 € pension, 83 % taxable, was taxed at (taxable − 11,604)
// × 42 % = 3,493 €. Under § 32a with the pension allowances and the deductible
// KVdR contributions (12.35 %) it is 808 €.
func TestPensionIncomeTax_TariffNotMarginalRate(t *testing.T) {
	taxable := 24000 * 0.83
	health := 24000 * 0.1235
	got := PensionIncomeTax(taxable, health, false, 0)
	if got != 808 {
		t.Errorf("PensionIncomeTax = %v, want 808", got)
	}
	// Without the health deduction the tariff gives the auditor's ~1,551.
	if got := PensionIncomeTax(taxable+138, 0, false, 0); got != 1551 {
		t.Errorf("PensionIncomeTax without deductions = %v, want 1,551", got)
	}
}

func TestCapitalGainsTaxOnWithdrawal_GainShareOnly(t *testing.T) {
	rate := CapitalIncomeTaxRate(0)
	// 10,000 € sold, 40 % gain, equity fund: 4,000 × 0.7 − 1,000 = 1,800 taxable.
	got := CapitalGainsTaxOnWithdrawal(10000, 0.4, 0.3, rate, 1000)
	if math.Abs(got-1800*rate) > 1e-9 {
		t.Errorf("tax = %v, want %v", got, 1800*rate)
	}
	if got := CapitalGainsTaxOnWithdrawal(10000, 0, 0.3, rate, 0); got != 0 {
		t.Errorf("no gain → %v, want 0", got)
	}
	// GrossWithdrawalForNet inverts it.
	gross := GrossWithdrawalForNet(30000, 0.6, 0.3, rate, 1000)
	if net := gross - CapitalGainsTaxOnWithdrawal(gross, 0.6, 0.3, rate, 1000); math.Abs(net-30000) > 1e-6 {
		t.Errorf("gross %v leaves %v net, want 30,000", gross, net)
	}
	if got := GrossWithdrawalForNet(1000, 0.5, 0.3, rate, 1000); got != 1000 {
		t.Errorf("within the allowance gross = %v, want 1,000", got)
	}
}
