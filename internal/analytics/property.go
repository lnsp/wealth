package analytics

import (
	"math"
	"sort"
	"time"
)

// Default parameters for real estate.
const (
	DefaultPropertyDrift      = 0.02 // nominal price change per year
	DefaultBuildingUsefulLife = 80   // ImmoWertV Anlage 1: houses and apartments
)

// Improvement is capital spent on an asset after purchase (e.g. a new
// bathroom). It adds to the building value on its date and ages from there.
type Improvement struct {
	Date   time.Time
	Amount float64
}

// PropertyValuation values real estate (and any non-depreciating asset) as
//
//	land     · (1 + drift)^years
//	building · (1 + drift)^years · remaining_life(t) / remaining_life(anchor)
//
// i.e. the land follows prices, and the building follows prices while losing
// value in a straight line over its useful life (ImmoWertV §38). Price indices
// hold building age constant, so the two effects don't double-count.
// Appraisals re-anchor the value, keeping the model's land/building split.
// Land and "other" assets have no building (BuildingEnd = 0); drift 0 makes the
// value stay at the last appraisal.
type PropertyValuation struct {
	PurchaseDate  time.Time
	PurchasePrice float64
	LandValue     float64 // non-ageing part of the purchase price
	BuildingEnd   float64 // fractional year the building is fully depreciated (year built + useful life); 0 = no building
	Drift         float64 // yearly price change; beyond the index when one is set
	Index         *PriceIndex
	Improvements  []Improvement
	Appraisals    []AssetObservation // only AppraisedValue is used
}

// IndexValue is one index level at a point in time.
type IndexValue struct {
	Date  time.Time
	Value float64
}

// PriceIndex is a published price index (e.g. Eurostat's quarterly house
// price index). Each quarterly value is an average, so it sits at the
// quarter's midpoint; levels in between are interpolated linearly.
type PriceIndex struct {
	Points []IndexValue // sorted by Date
}

// NewQuarterlyIndex builds an index from quarter start dates and values.
func NewQuarterlyIndex(periods []time.Time, values []float64) *PriceIndex {
	ix := &PriceIndex{}
	for i, p := range periods {
		mid := p.AddDate(0, 1, 15)
		ix.Points = append(ix.Points, IndexValue{Date: mid, Value: values[i]})
	}
	sort.Slice(ix.Points, func(i, j int) bool { return ix.Points[i].Date.Before(ix.Points[j].Date) })
	return ix
}

// Latest returns the most recent published point.
func (ix *PriceIndex) Latest() (IndexValue, bool) {
	if ix == nil || len(ix.Points) == 0 {
		return IndexValue{}, false
	}
	return ix.Points[len(ix.Points)-1], true
}

// level returns the index at t, extended by drift beyond the published range.
func (ix *PriceIndex) level(t time.Time, drift float64) float64 {
	pts := ix.Points
	first, last := pts[0], pts[len(pts)-1]
	if !t.After(first.Date) {
		return first.Value / math.Pow(1+drift, yearsBetween(t, first.Date))
	}
	if !t.Before(last.Date) {
		return last.Value * math.Pow(1+drift, yearsBetween(last.Date, t))
	}
	i := sort.Search(len(pts), func(i int) bool { return pts[i].Date.After(t) })
	a, b := pts[i-1], pts[i]
	frac := float64(t.Sub(a.Date)) / float64(b.Date.Sub(a.Date))
	return a.Value + frac*(b.Value-a.Value)
}

// YearOverYear is the index change over the year up to its latest point.
func (ix *PriceIndex) YearOverYear() (float64, bool) {
	last, ok := ix.Latest()
	if !ok || last.Date.AddDate(-1, 0, 0).Before(ix.Points[0].Date) {
		return 0, false
	}
	return last.Value/ix.level(last.Date.AddDate(-1, 0, 0), 0) - 1, true
}

// PriceGrowth is the price factor between two dates (index or drift).
func (p PropertyValuation) PriceGrowth(from, to time.Time) float64 { return p.growth(from, to) }

// growth is the price factor between two dates: the index ratio when an
// index is set, else the fixed yearly drift.
func (p PropertyValuation) growth(from, to time.Time) float64 {
	if p.Index != nil && len(p.Index.Points) > 0 {
		return p.Index.level(to, p.Drift) / p.Index.level(from, p.Drift)
	}
	return math.Pow(1+p.Drift, yearsBetween(from, to))
}

func yearFraction(t time.Time) float64 {
	start := time.Date(t.Year(), 1, 1, 0, 0, 0, 0, t.Location())
	end := start.AddDate(1, 0, 0)
	return float64(t.Year()) + float64(t.Sub(start))/float64(end.Sub(start))
}

func (p PropertyValuation) remainingLife(t time.Time) float64 {
	if p.BuildingEnd <= 0 {
		return 0
	}
	return math.Max(0, p.BuildingEnd-yearFraction(t))
}

type propertyState struct {
	date           time.Time
	land, building float64
}

// advance moves the state forward to t: both parts follow prices, the
// building also ages.
func (p PropertyValuation) advance(s propertyState, t time.Time) propertyState {
	if !t.After(s.date) {
		return s
	}
	growth := p.growth(s.date, t)
	s.land *= growth
	if from := p.remainingLife(s.date); from > 0 {
		s.building *= growth * p.remainingLife(t) / from
	} else {
		s.building = 0
	}
	s.date = t
	return s
}

type propertyEvent struct {
	date      time.Time
	improve   float64
	appraisal float64
	isAppr    bool
}

// Components returns the modelled land and building value on date t (both 0
// before purchase).
func (p PropertyValuation) Components(t time.Time) (land, building float64) {
	if t.Before(p.PurchaseDate) {
		return 0, 0
	}
	landValue := math.Min(math.Max(p.LandValue, 0), p.PurchasePrice)
	if p.BuildingEnd <= 0 {
		landValue = p.PurchasePrice
	}
	s := propertyState{date: p.PurchaseDate, land: landValue, building: p.PurchasePrice - landValue}

	var events []propertyEvent
	for _, im := range p.Improvements {
		if im.Date.After(p.PurchaseDate) && !im.Date.After(t) {
			events = append(events, propertyEvent{date: im.Date, improve: im.Amount})
		}
	}
	for _, a := range p.Appraisals {
		if a.AppraisedValue != nil && !a.Date.Before(p.PurchaseDate) && !a.Date.After(t) {
			events = append(events, propertyEvent{date: a.Date, appraisal: *a.AppraisedValue, isAppr: true})
		}
	}
	// Improvements before appraisals on the same day: the appraisal sees them.
	sort.SliceStable(events, func(i, j int) bool {
		if events[i].date.Equal(events[j].date) {
			return !events[i].isAppr && events[j].isAppr
		}
		return events[i].date.Before(events[j].date)
	})
	for _, e := range events {
		s = p.advance(s, e.date)
		if !e.isAppr {
			if p.BuildingEnd > 0 {
				s.building += e.improve
			} else {
				s.land += e.improve
			}
			continue
		}
		if total := s.land + s.building; total > 0 {
			scale := e.appraisal / total
			s.land *= scale
			s.building *= scale
		} else {
			s.land, s.building = e.appraisal, 0
		}
	}
	s = p.advance(s, t)
	return s.land, s.building
}

// ValueAt returns the modelled market value on date t (0 before purchase).
func (p PropertyValuation) ValueAt(t time.Time) float64 {
	land, building := p.Components(t)
	return land + building
}
