package analytics

import (
	"encoding/json"
	"math"
	"testing"
)

func TestParseWeights(t *testing.T) {
	tests := []struct {
		name string
		raw  json.RawMessage
		want map[string]float64
	}{
		{
			name: "valid weights",
			raw:  json.RawMessage(`{"Tech": 60.0, "Health": 25.5, "Finance": 14.5}`),
			want: map[string]float64{"Tech": 60.0, "Health": 25.5, "Finance": 14.5},
		},
		{
			name: "empty JSON",
			raw:  json.RawMessage(``),
			want: nil,
		},
		{
			name: "null JSON",
			raw:  json.RawMessage(`null`),
			want: nil,
		},
		{
			name: "invalid JSON returns nil",
			raw:  json.RawMessage(`{not json}`),
			want: nil,
		},
		{
			name: "empty object",
			raw:  json.RawMessage(`{}`),
			want: map[string]float64{},
		},
		{
			name: "single entry",
			raw:  json.RawMessage(`{"US": 100}`),
			want: map[string]float64{"US": 100},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseWeights(tt.raw)
			if tt.want == nil {
				if got != nil {
					t.Errorf("ParseWeights() = %v, want nil", got)
				}
				return
			}
			if len(got) != len(tt.want) {
				t.Fatalf("ParseWeights() len = %d, want %d", len(got), len(tt.want))
			}
			for k, v := range tt.want {
				if got[k] != v {
					t.Errorf("ParseWeights()[%q] = %f, want %f", k, got[k], v)
				}
			}
		})
	}
}

func TestComputeOverlap_Table(t *testing.T) {
	tests := []struct {
		name string
		a    map[string]float64
		b    map[string]float64
		want float64
	}{
		{
			name: "partial overlap",
			a:    map[string]float64{"AAPL": 10, "MSFT": 8, "GOOG": 5},
			b:    map[string]float64{"AAPL": 7, "MSFT": 12, "AMZN": 6},
			want: 15, // min(10,7) + min(8,12) = 7+8
		},
		{
			name: "no overlap",
			a:    map[string]float64{"AAPL": 10},
			b:    map[string]float64{"GOOG": 10},
			want: 0,
		},
		{
			name: "complete overlap same weights",
			a:    map[string]float64{"AAPL": 50, "MSFT": 50},
			b:    map[string]float64{"AAPL": 50, "MSFT": 50},
			want: 100,
		},
		{
			name: "empty a",
			a:    map[string]float64{},
			b:    map[string]float64{"AAPL": 50},
			want: 0,
		},
		{
			name: "nil maps",
			a:    nil,
			b:    nil,
			want: 0,
		},
		{
			name: "single holding overlap",
			a:    map[string]float64{"AAPL": 30},
			b:    map[string]float64{"AAPL": 20},
			want: 20,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ComputeOverlap(tt.a, tt.b)
			if got != tt.want {
				t.Errorf("ComputeOverlap() = %f, want %f", got, tt.want)
			}
		})
	}
}

func TestComputeEffectiveExposure(t *testing.T) {
	tests := []struct {
		name     string
		holdings []HoldingWithETF
		wantNil  bool
		want     map[string]float64
	}{
		{
			name:    "nil holdings",
			wantNil: true,
		},
		{
			name: "zero total value returns nil",
			holdings: []HoldingWithETF{
				{ISIN: "A", MarketValue: 0, ETFHoldings: map[string]float64{"X": 50}},
			},
			wantNil: true,
		},
		{
			name: "single holding",
			holdings: []HoldingWithETF{
				{
					ISIN:        "ETF1",
					MarketValue: 1000,
					ETFHoldings: map[string]float64{"AAPL": 40, "MSFT": 30},
				},
			},
			want: map[string]float64{
				"AAPL": 0.4,  // 1.0 * 40/100
				"MSFT": 0.3,  // 1.0 * 30/100
			},
		},
		{
			name: "two equal holdings",
			holdings: []HoldingWithETF{
				{
					ISIN:        "ETF1",
					MarketValue: 500,
					ETFHoldings: map[string]float64{"AAPL": 60},
				},
				{
					ISIN:        "ETF2",
					MarketValue: 500,
					ETFHoldings: map[string]float64{"AAPL": 20, "GOOG": 40},
				},
			},
			want: map[string]float64{
				"AAPL": 0.4, // 0.5*60/100 + 0.5*20/100 = 0.3+0.1
				"GOOG": 0.2, // 0.5*40/100
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ComputeEffectiveExposure(tt.holdings)
			if tt.wantNil {
				if got != nil {
					t.Errorf("expected nil, got %v", got)
				}
				return
			}
			for k, v := range tt.want {
				if math.Abs(got[k]-v) > 0.001 {
					t.Errorf("exposure[%q] = %f, want %f", k, got[k], v)
				}
			}
		})
	}
}
