package analytics

import (
	"math"
	"sort"
	"time"
)

// Default depreciation parameters for cars. Calibrated on 296 used VW ID.3
// Pro S 77/79 kWh listings (AutoScout24, Nov 2026) via a log-linear fit of
// asking price on age and mileage (R² 0.88): −7.8 %/year of age and −3.0 %
// per 10,000 km, i.e. ≈ −11.9 %/year at 15,000 km/year.
const (
	DefaultCarAnnualDepreciation = 0.078
	DefaultCarKmDepreciation     = 0.030
	DefaultCarKmPerYear          = 15000
)

const daysPerYear = 365.25

// AssetObservation is a dated odometer reading and/or market appraisal.
// Either pointer may be nil, but not both.
type AssetObservation struct {
	Date           time.Time
	OdometerKm     *float64
	AppraisedValue *float64
}

// DepreciatingAsset values a physical asset with an exponential model that
// separates calendar age from usage:
//
//	V(t) = V_anchor · (1 − AnnualRate)^years · (1 − KmRate)^(km / 10,000)
//
// The anchor is the latest appraisal on or before t, or the purchase if none.
// Because the model is memoryless, re-anchoring on an appraisal simply
// restarts the same curve from the observed market value.
type DepreciatingAsset struct {
	PurchaseDate       time.Time
	PurchasePrice      float64
	PurchaseOdometerKm float64
	AnnualRate         float64 // fractional value loss per year of age
	KmRate             float64 // fractional value loss per 10,000 km
	ExpectedKmPerYear  float64 // used to extrapolate mileage without readings
	Observations       []AssetObservation
}

// minRateSpan is the shortest odometer history used to infer the actual
// driving rate; shorter spans fall back to ExpectedKmPerYear.
const minRateSpan = 90 * 24 * time.Hour

func yearsBetween(from, to time.Time) float64 {
	return to.Sub(from).Hours() / 24 / daysPerYear
}

type odoPoint struct {
	date time.Time
	km   float64
}

func (a DepreciatingAsset) odometerPoints() []odoPoint {
	pts := []odoPoint{{a.PurchaseDate, a.PurchaseOdometerKm}}
	for _, o := range a.Observations {
		if o.OdometerKm != nil && !o.Date.Before(a.PurchaseDate) {
			pts = append(pts, odoPoint{o.Date, *o.OdometerKm})
		}
	}
	sort.SliceStable(pts, func(i, j int) bool { return pts[i].date.Before(pts[j].date) })
	return pts
}

// OdometerAt estimates the odometer on date t: linear interpolation between
// readings, then extrapolation at the observed driving rate (or the expected
// rate while the reading history is shorter than 90 days).
func (a DepreciatingAsset) OdometerAt(t time.Time) float64 {
	pts := a.odometerPoints()
	if !t.After(pts[0].date) {
		return pts[0].km
	}
	for i := 1; i < len(pts); i++ {
		if !t.After(pts[i].date) {
			prev, next := pts[i-1], pts[i]
			span := next.date.Sub(prev.date)
			if span <= 0 {
				return next.km
			}
			frac := float64(t.Sub(prev.date)) / float64(span)
			return prev.km + frac*(next.km-prev.km)
		}
	}
	first, last := pts[0], pts[len(pts)-1]
	rate := a.ExpectedKmPerYear
	if span := last.date.Sub(first.date); span >= minRateSpan {
		rate = math.Max(0, (last.km-first.km)/yearsBetween(first.date, last.date))
	}
	return last.km + rate*yearsBetween(last.date, t)
}

// ValueAt returns the modelled market value on date t (0 before purchase).
func (a DepreciatingAsset) ValueAt(t time.Time) float64 {
	if t.Before(a.PurchaseDate) {
		return 0
	}
	anchorDate, anchorValue := a.PurchaseDate, a.PurchasePrice
	for _, o := range a.Observations {
		if o.AppraisedValue == nil || o.Date.Before(a.PurchaseDate) || o.Date.After(t) {
			continue
		}
		if !o.Date.Before(anchorDate) {
			anchorDate, anchorValue = o.Date, *o.AppraisedValue
		}
	}
	years := yearsBetween(anchorDate, t)
	km := math.Max(0, a.OdometerAt(t)-a.OdometerAt(anchorDate))
	return anchorValue * math.Pow(1-a.AnnualRate, years) * math.Pow(1-a.KmRate, km/10000)
}

// EffectiveAnnualRate is the combined yearly value loss at the expected
// mileage, e.g. 11.9 % for the car defaults.
func (a DepreciatingAsset) EffectiveAnnualRate() float64 {
	return 1 - (1-a.AnnualRate)*math.Pow(1-a.KmRate, a.ExpectedKmPerYear/10000)
}
