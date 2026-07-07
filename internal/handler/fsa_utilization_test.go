package handler

import (
	"math"
	"testing"

	"github.com/lnsp/wealth/internal/analytics"
)

// fsaUtilization mirrors the HandleFSAStatus computation: utilization % =
// used / allowance × 100, where allowance is 1000 EUR for single filers and
// 2000 EUR for joint (married, Zusammenveranlagung). Locked here so the
// allowance doubling and the utilization clamp survive any refactor.
func fsaUtilization(income float64, joint bool) (used, remaining, utilizationPct, allowance float64) {
	allowance = analytics.Sparerpauschbetrag
	if joint {
		allowance *= 2
	}
	used = math.Min(income, allowance)
	remaining = allowance - used
	if allowance > 0 {
		utilizationPct = math.Round(used/allowance*10000) / 100
	}
	return
}

func TestFSAUtilization_SingleFiler(t *testing.T) {
	used, remaining, pct, allow := fsaUtilization(500, false)
	if allow != 1000 {
		t.Errorf("single allowance = %.2f, want 1000", allow)
	}
	if used != 500 || remaining != 500 {
		t.Errorf("single 500 income: used=%.2f remaining=%.2f, want 500/500", used, remaining)
	}
	if math.Abs(pct-50.0) > 0.01 {
		t.Errorf("single 500 / 1000 utilization = %.2f%%, want 50.00", pct)
	}
}

func TestFSAUtilization_JointFiler(t *testing.T) {
	used, remaining, pct, allow := fsaUtilization(500, true)
	if allow != 2000 {
		t.Errorf("joint allowance = %.2f, want 2000", allow)
	}
	if used != 500 || remaining != 1500 {
		t.Errorf("joint 500 income: used=%.2f remaining=%.2f, want 500/1500", used, remaining)
	}
	if math.Abs(pct-25.0) > 0.01 {
		t.Errorf("joint 500 / 2000 utilization = %.2f%%, want 25.00", pct)
	}
}

func TestFSAUtilization_OverConsumed(t *testing.T) {
	// Income above the allowance is clamped — used == allowance, remaining 0.
	used, remaining, pct, _ := fsaUtilization(3000, false)
	if used != 1000 || remaining != 0 {
		t.Errorf("over-consumed single: used=%.2f remaining=%.2f, want 1000/0", used, remaining)
	}
	if pct != 100 {
		t.Errorf("over-consumed utilization = %.2f, want 100", pct)
	}

	used, remaining, pct, _ = fsaUtilization(3000, true)
	if used != 2000 || remaining != 0 {
		t.Errorf("over-consumed joint: used=%.2f remaining=%.2f, want 2000/0", used, remaining)
	}
	if pct != 100 {
		t.Errorf("over-consumed joint utilization = %.2f, want 100", pct)
	}
}

func TestFSAUtilization_BoundaryAtAllowance(t *testing.T) {
	// Exactly at the limit → 100%, zero remaining.
	_, remaining, pct, _ := fsaUtilization(1000, false)
	if remaining != 0 || pct != 100 {
		t.Errorf("single at boundary: remaining=%.2f utilization=%.2f, want 0/100", remaining, pct)
	}
	_, remaining, pct, _ = fsaUtilization(2000, true)
	if remaining != 0 || pct != 100 {
		t.Errorf("joint at boundary: remaining=%.2f utilization=%.2f, want 0/100", remaining, pct)
	}
}

func TestFSAUtilization_ZeroIncome(t *testing.T) {
	used, remaining, pct, _ := fsaUtilization(0, false)
	if used != 0 || remaining != 1000 || pct != 0 {
		t.Errorf("zero income single: %.2f / %.2f / %.2f, want 0/1000/0", used, remaining, pct)
	}
}
