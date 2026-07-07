package handler

import (
	"math"
	"testing"
)

// computeAllocationBucket mirrors HandleAllocationSummary's per-holding loop
// for one allocation dimension (sector or country). Given the holding's
// market value and a map of {bucket → weight%} that may sum to less than
// 100, route the missing fraction into "Unknown" so the bucket totals
// reflect the holding's full value.
func computeAllocationBucket(value float64, weights map[string]float64, out map[string]float64) {
	if value <= 0 {
		return
	}
	sum := 0.0
	for bucket, pct := range weights {
		out[bucket] += value * pct / 100
		sum += pct
	}
	if sum < 100 {
		out["Unknown"] += value * (100 - sum) / 100
	}
}

// Each test feeds a portfolio of holdings into the bucketing helper and
// asserts: (a) the bucket totals sum to the portfolio's total value; (b)
// an "Unknown" bucket appears when any holding lacks metadata.

func TestAllocationBucket_FullCoverage(t *testing.T) {
	sectors := map[string]float64{}
	computeAllocationBucket(10000, map[string]float64{"Tech": 60, "Health": 40}, sectors)
	computeAllocationBucket(5000, map[string]float64{"Tech": 100}, sectors)

	totalValue := 10000 + 5000
	sum := 0.0
	for _, v := range sectors {
		sum += v
	}
	if math.Abs(sum-float64(totalValue)) > 1.0 {
		t.Errorf("bucket sum = %.2f, want %.2f (full coverage)", sum, float64(totalValue))
	}
	if _, ok := sectors["Unknown"]; ok {
		t.Error("Unknown should not be present when all holdings have full coverage")
	}
}

func TestAllocationBucket_OneHoldingNoMetadata(t *testing.T) {
	countries := map[string]float64{}
	computeAllocationBucket(10000, map[string]float64{"US": 70, "DE": 30}, countries)
	computeAllocationBucket(5000, map[string]float64{}, countries) // missing metadata

	totalValue := 15000.0
	sum := 0.0
	for _, v := range countries {
		sum += v
	}
	if math.Abs(sum-totalValue) > 1.0 {
		t.Errorf("bucket sum = %.2f, want 15000 (missing metadata routes to Unknown)", sum)
	}
	if math.Abs(countries["Unknown"]-5000) > 1.0 {
		t.Errorf("Unknown bucket = %.2f, want 5000 (the entire unattributed holding)", countries["Unknown"])
	}
}

func TestAllocationBucket_PartialCoverage(t *testing.T) {
	// Holding worth 10000 with sectors summing to only 80% (20% missing).
	sectors := map[string]float64{}
	computeAllocationBucket(10000, map[string]float64{"Tech": 50, "Health": 30}, sectors)

	if math.Abs(sectors["Tech"]-5000) > 1.0 {
		t.Errorf("Tech = %.2f, want 5000", sectors["Tech"])
	}
	if math.Abs(sectors["Health"]-3000) > 1.0 {
		t.Errorf("Health = %.2f, want 3000", sectors["Health"])
	}
	if math.Abs(sectors["Unknown"]-2000) > 1.0 {
		t.Errorf("Unknown = %.2f, want 2000 (20%% × 10000)", sectors["Unknown"])
	}

	sum := sectors["Tech"] + sectors["Health"] + sectors["Unknown"]
	if math.Abs(sum-10000) > 1.0 {
		t.Errorf("total = %.2f, want 10000 (partial coverage routes residual to Unknown)", sum)
	}
}

func TestAllocationBucket_AllUnknown(t *testing.T) {
	// Portfolio where NO holding has metadata — entire value lands in Unknown.
	sectors := map[string]float64{}
	computeAllocationBucket(5000, nil, sectors)
	computeAllocationBucket(3000, map[string]float64{}, sectors)

	if math.Abs(sectors["Unknown"]-8000) > 1.0 {
		t.Errorf("Unknown = %.2f, want 8000 (no metadata anywhere)", sectors["Unknown"])
	}
	// And no other key should exist.
	for k := range sectors {
		if k != "Unknown" {
			t.Errorf("unexpected bucket %q with value %.2f — should only have Unknown", k, sectors[k])
		}
	}
}

func TestAllocationBucket_NegativeOrZeroValueSkipped(t *testing.T) {
	// Defensive: a holding with zero or negative value shouldn't poison the
	// totals (e.g., a stale row with no current price).
	sectors := map[string]float64{}
	computeAllocationBucket(0, map[string]float64{"Tech": 100}, sectors)
	computeAllocationBucket(-500, map[string]float64{"Tech": 100}, sectors)
	computeAllocationBucket(10000, map[string]float64{"Tech": 100}, sectors)

	if math.Abs(sectors["Tech"]-10000) > 1.0 {
		t.Errorf("Tech = %.2f, want 10000 (zero/negative-value holdings skipped)", sectors["Tech"])
	}
	if _, ok := sectors["Unknown"]; ok {
		t.Error("Unknown should not appear when the only contributing holding has full coverage")
	}
}
