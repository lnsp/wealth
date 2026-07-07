package analytics

import (
	"math"
	"testing"
	"time"
)

func TestComputeOverlap(t *testing.T) {
	a := map[string]float64{"AAPL": 10.0, "MSFT": 8.0, "GOOG": 5.0}
	b := map[string]float64{"AAPL": 7.0, "MSFT": 12.0, "AMZN": 6.0}

	// min(10,7) + min(8,12) = 7 + 8 = 15
	got := ComputeOverlap(a, b)
	if got != 15.0 {
		t.Errorf("ComputeOverlap: expected 15.0, got %f", got)
	}
}

func TestComputeOverlapNoOverlap(t *testing.T) {
	a := map[string]float64{"AAPL": 10.0}
	b := map[string]float64{"GOOG": 10.0}

	got := ComputeOverlap(a, b)
	if got != 0 {
		t.Errorf("expected 0 overlap, got %f", got)
	}
}

func TestBuildOverlapMatrix(t *testing.T) {
	etfs := []ETFWithHoldings{
		{ISIN: "A", Holdings: map[string]float64{"X": 50, "Y": 50}},
		{ISIN: "B", Holdings: map[string]float64{"X": 30, "Z": 70}},
	}

	matrix := BuildOverlapMatrix(etfs)
	if matrix[0][0] != 100 {
		t.Errorf("diagonal should be 100")
	}
	// Overlap: min(50,30) = 30
	if matrix[0][1] != 30 {
		t.Errorf("expected overlap 30, got %f", matrix[0][1])
	}
	if matrix[1][0] != 30 {
		t.Errorf("matrix should be symmetric")
	}
}

func TestComputeSectorExposure(t *testing.T) {
	holdings := []Holding{
		{MarketValue: 1000, SectorWeights: map[string]float64{"Tech": 60, "Health": 40}},
		{MarketValue: 1000, SectorWeights: map[string]float64{"Tech": 20, "Finance": 80}},
	}

	exposure := ComputeSectorExposure(holdings)
	// Tech: (0.5 * 60/100) + (0.5 * 20/100) = 0.3 + 0.1 = 0.4
	if math.Abs(exposure["Tech"]-0.4) > 0.001 {
		t.Errorf("Tech exposure: expected ~0.4, got %f", exposure["Tech"])
	}
}

func TestCalculateIRR(t *testing.T) {
	// Simple case: invest 1000, get back 1100 after 1 year = 10% IRR
	cashflows := []CashFlow{
		{Date: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC), Amount: -1000},
		{Date: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), Amount: 1100},
	}

	irr := CalculateIRR(cashflows, 0.1)
	if math.Abs(irr-0.1) > 0.001 {
		t.Errorf("IRR: expected ~0.1, got %f", irr)
	}
}

func TestCalculateTWR(t *testing.T) {
	valuations := []DailyValuation{
		{Date: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC), Value: 1000},
		{Date: time.Date(2025, 7, 1, 0, 0, 0, 0, time.UTC), Value: 1100},
		{Date: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), Value: 1210},
	}

	twr := CalculateTWR(valuations, nil)
	// (1100/1000) * (1210/1100) - 1 = 1.1 * 1.1 - 1 = 0.21
	if math.Abs(twr-0.21) > 0.01 {
		t.Errorf("TWR: expected ~0.21, got %f", twr)
	}
}
