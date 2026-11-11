package analytics

import (
	"math"
	"testing"
	"time"
)

func ptr(f float64) *float64 { return &f }

func testCar() DepreciatingAsset {
	return DepreciatingAsset{
		PurchaseDate:       day("2026-08-01"),
		PurchasePrice:      44900,
		PurchaseOdometerKm: 2700,
		AnnualRate:         DefaultCarAnnualDepreciation,
		KmRate:             DefaultCarKmDepreciation,
		ExpectedKmPerYear:  DefaultCarKmPerYear,
	}
}

func approx(t *testing.T, name string, got, want, tol float64) {
	t.Helper()
	if math.Abs(got-want) > tol {
		t.Errorf("%s = %.2f, want %.2f (±%.2f)", name, got, want, tol)
	}
}

func TestDepreciation_EffectiveAnnualRate(t *testing.T) {
	// 0.922 · 0.970^1.5 ≈ 0.8809 → 11.9 %/yr at 15k km/yr.
	approx(t, "rate", testCar().EffectiveAnnualRate(), 0.1191, 0.0005)
}

func TestDepreciation_ValueAt(t *testing.T) {
	a := testCar()
	approx(t, "before purchase", a.ValueAt(day("2026-07-31")), 0, 0)
	approx(t, "at purchase", a.ValueAt(a.PurchaseDate), 44900, 0.01)
	// One year at the expected 15k km: 44,900 · 0.8809.
	oneYear := a.PurchaseDate.Add(time.Duration(daysPerYear * 24 * float64(time.Hour)))
	approx(t, "after 1y", a.ValueAt(oneYear), 39552, 5)
	if a.ValueAt(oneYear.AddDate(1, 0, 0)) >= a.ValueAt(oneYear) {
		t.Error("value must keep declining")
	}
}

func TestDepreciation_AppraisalReanchors(t *testing.T) {
	a := testCar()
	a.Observations = []AssetObservation{{Date: day("2026-11-11"), AppraisedValue: ptr(47500)}}

	approx(t, "on appraisal date", a.ValueAt(day("2026-11-11")), 47500, 0.01)
	if v := a.ValueAt(day("2026-11-10")); v >= 44900 || v < 43000 {
		t.Errorf("day before appraisal should follow the purchase curve, got %.0f", v)
	}
	// From the appraisal on, the curve decays like a fresh purchase at 47.5k.
	fresh := testCar()
	fresh.PurchaseDate, fresh.PurchasePrice = day("2026-11-11"), 47500
	fresh.PurchaseOdometerKm = a.OdometerAt(day("2026-11-11"))
	approx(t, "2y after appraisal", a.ValueAt(day("2028-11-11")), fresh.ValueAt(day("2028-11-11")), 1)
}

func TestDepreciation_OdometerReadings(t *testing.T) {
	a := testCar()
	// Before 90 days of history the expected rate (15k/yr) is used.
	approx(t, "expected extrapolation", a.OdometerAt(day("2027-08-01")), 2700+15000, 20)

	// Heavy driver: 15,000 km in the first 6 months → 30k/yr extrapolated.
	a.Observations = []AssetObservation{{Date: day("2027-01-30"), OdometerKm: ptr(17700)}}
	approx(t, "interpolated", a.OdometerAt(day("2026-10-31")), 2700+7500, 100)
	approx(t, "observed rate", a.OdometerAt(day("2027-07-31")), 17700+15000, 150)

	light := testCar()
	light.Observations = []AssetObservation{{Date: day("2027-01-30"), OdometerKm: ptr(5200)}}
	if a.ValueAt(day("2027-08-01")) >= light.ValueAt(day("2027-08-01")) {
		t.Error("more kilometres must mean a lower value")
	}
}

func TestDepreciation_ObservationBeforePurchaseIgnored(t *testing.T) {
	a := testCar()
	a.Observations = []AssetObservation{{Date: day("2026-07-01"), AppraisedValue: ptr(60000), OdometerKm: ptr(0)}}
	approx(t, "at purchase", a.ValueAt(a.PurchaseDate), 44900, 0.01)
	approx(t, "odometer at purchase", a.OdometerAt(a.PurchaseDate), 2700, 0)
}
