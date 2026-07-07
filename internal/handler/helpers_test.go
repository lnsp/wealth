package handler

import (
	"math"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/lnsp/wealth/internal/analytics"
)

func TestNumericFromFloat(t *testing.T) {
	tests := []struct {
		name  string
		input float64
	}{
		{"zero", 0},
		{"positive", 123.45},
		{"negative", -99.99},
		{"large", 156376.98},
		{"tiny", 0.001},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n := numericFromFloat(tt.input)
			if !n.Valid {
				t.Fatal("expected Valid numeric")
			}
			f, err := n.Float64Value()
			if err != nil {
				t.Fatalf("Float64Value error: %v", err)
			}
			if math.Abs(f.Float64-tt.input) > 0.01 {
				t.Errorf("numericFromFloat(%f) roundtrip = %f", tt.input, f.Float64)
			}
		})
	}
}

func TestNumericToFloat(t *testing.T) {
	// Valid numeric
	n := numericFromFloat(42.5)
	got := numericToFloat(n)
	if math.Abs(got-42.5) > 0.01 {
		t.Errorf("numericToFloat = %f, want 42.5", got)
	}

	// Invalid numeric returns 0
	var invalid pgtype.Numeric
	if numericToFloat(invalid) != 0 {
		t.Error("invalid numeric should return 0")
	}
}

func TestSortStrings(t *testing.T) {
	tests := []struct {
		name  string
		input []string
		want  []string
	}{
		{"already sorted", []string{"a", "b", "c"}, []string{"a", "b", "c"}},
		{"reversed", []string{"c", "b", "a"}, []string{"a", "b", "c"}},
		{"dates", []string{"2025-03", "2024-01", "2024-12"}, []string{"2024-01", "2024-12", "2025-03"}},
		{"single", []string{"x"}, []string{"x"}},
		{"empty", []string{}, []string{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := make([]string, len(tt.input))
			copy(s, tt.input)
			sortStrings(s)
			for i := range tt.want {
				if s[i] != tt.want[i] {
					t.Errorf("index %d: got %q, want %q", i, s[i], tt.want[i])
				}
			}
		})
	}
}

func TestSortCashFlows(t *testing.T) {
	cfs := []analytics.CashFlow{
		{Date: time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC), Amount: -500},
		{Date: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC), Amount: -1000},
		{Date: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC), Amount: -2000},
	}

	sortCashFlows(cfs)

	if !cfs[0].Date.Equal(time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("first should be 2024-01-01, got %v", cfs[0].Date)
	}
	if !cfs[1].Date.Equal(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("second should be 2025-01-01, got %v", cfs[1].Date)
	}
	if !cfs[2].Date.Equal(time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("third should be 2025-06-01, got %v", cfs[2].Date)
	}
}

func TestSortCashFlows_AlreadySorted(t *testing.T) {
	cfs := []analytics.CashFlow{
		{Date: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC), Amount: -1000},
		{Date: time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC), Amount: -500},
	}
	sortCashFlows(cfs)
	if cfs[0].Amount != -1000 {
		t.Error("already sorted should remain unchanged")
	}
}

func TestSortCashFlows_Empty(t *testing.T) {
	var cfs []analytics.CashFlow
	sortCashFlows(cfs) // should not panic
}
