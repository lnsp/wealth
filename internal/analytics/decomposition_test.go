package analytics

import (
	"math"
	"testing"
)

func TestComputeCountryExposure(t *testing.T) {
	tests := []struct {
		name     string
		holdings []Holding
		wantNil  bool
		want     map[string]float64
	}{
		{
			name:    "nil holdings returns nil",
			wantNil: true,
		},
		{
			name: "zero total value returns nil",
			holdings: []Holding{
				{MarketValue: 0, CountryWeights: map[string]float64{"US": 100}},
			},
			wantNil: true,
		},
		{
			name: "single holding",
			holdings: []Holding{
				{MarketValue: 1000, CountryWeights: map[string]float64{"US": 60, "DE": 40}},
			},
			want: map[string]float64{"US": 0.6, "DE": 0.4},
		},
		{
			name: "two equal weight holdings",
			holdings: []Holding{
				{MarketValue: 1000, CountryWeights: map[string]float64{"US": 60, "DE": 40}},
				{MarketValue: 1000, CountryWeights: map[string]float64{"US": 20, "JP": 80}},
			},
			want: map[string]float64{
				"US": 0.4, // 0.5*60/100 + 0.5*20/100 = 0.3+0.1
				"DE": 0.2, // 0.5*40/100
				"JP": 0.4, // 0.5*80/100
			},
		},
		{
			name: "unequal weights",
			holdings: []Holding{
				{MarketValue: 3000, CountryWeights: map[string]float64{"US": 100}},
				{MarketValue: 1000, CountryWeights: map[string]float64{"DE": 100}},
			},
			want: map[string]float64{
				"US": 0.75, // 0.75*100/100
				"DE": 0.25, // 0.25*100/100
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ComputeCountryExposure(tt.holdings)
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

func TestComputeSectorExposure_Table(t *testing.T) {
	tests := []struct {
		name     string
		holdings []Holding
		wantNil  bool
		want     map[string]float64
	}{
		{
			name:    "nil holdings returns nil",
			wantNil: true,
		},
		{
			name: "single holding all tech",
			holdings: []Holding{
				{MarketValue: 1000, SectorWeights: map[string]float64{"Tech": 100}},
			},
			want: map[string]float64{"Tech": 1.0},
		},
		{
			name: "three holdings",
			holdings: []Holding{
				{MarketValue: 500, SectorWeights: map[string]float64{"Tech": 80, "Health": 20}},
				{MarketValue: 300, SectorWeights: map[string]float64{"Finance": 100}},
				{MarketValue: 200, SectorWeights: map[string]float64{"Tech": 50, "Energy": 50}},
			},
			want: map[string]float64{
				"Tech":    0.50, // 0.5*80/100 + 0.2*50/100 = 0.4+0.1
				"Health":  0.10, // 0.5*20/100
				"Finance": 0.30, // 0.3*100/100
				"Energy":  0.10, // 0.2*50/100
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ComputeSectorExposure(tt.holdings)
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

func TestBuildOverlapMatrix_Table(t *testing.T) {
	tests := []struct {
		name string
		etfs []ETFWithHoldings
		want [][]float64
	}{
		{
			name: "empty",
			etfs: nil,
			want: nil,
		},
		{
			name: "single ETF",
			etfs: []ETFWithHoldings{
				{ISIN: "A", Holdings: map[string]float64{"X": 50, "Y": 50}},
			},
			want: [][]float64{{100}},
		},
		{
			name: "two ETFs with partial overlap",
			etfs: []ETFWithHoldings{
				{ISIN: "A", Holdings: map[string]float64{"X": 50, "Y": 50}},
				{ISIN: "B", Holdings: map[string]float64{"X": 30, "Z": 70}},
			},
			want: [][]float64{
				{100, 30},
				{30, 100},
			},
		},
		{
			name: "three ETFs",
			etfs: []ETFWithHoldings{
				{ISIN: "A", Holdings: map[string]float64{"X": 40, "Y": 60}},
				{ISIN: "B", Holdings: map[string]float64{"Y": 30, "Z": 70}},
				{ISIN: "C", Holdings: map[string]float64{"X": 20, "Z": 80}},
			},
			want: [][]float64{
				{100, 30, 20},  // A-B: min(60,30)=30, A-C: min(40,20)=20
				{30, 100, 70},  // B-C: min(70,80)=70
				{20, 70, 100},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := BuildOverlapMatrix(tt.etfs)
			if tt.want == nil {
				if len(got) != 0 {
					t.Errorf("expected empty matrix, got %v", got)
				}
				return
			}
			if len(got) != len(tt.want) {
				t.Fatalf("matrix size = %d, want %d", len(got), len(tt.want))
			}
			for i := range tt.want {
				for j := range tt.want[i] {
					if got[i][j] != tt.want[i][j] {
						t.Errorf("matrix[%d][%d] = %f, want %f", i, j, got[i][j], tt.want[i][j])
					}
				}
			}
		})
	}
}
