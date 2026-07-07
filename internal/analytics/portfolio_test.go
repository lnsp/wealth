package analytics

import (
	"encoding/json"
	"math"
	"sort"
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
				"AAPL": 0.4, // 1.0 * 40/100
				"MSFT": 0.3, // 1.0 * 30/100
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

func TestComputeConcentrationAlerts(t *testing.T) {
	tests := []struct {
		name            string
		etfs            []ETFWithHoldings
		holdingsWithETF []HoldingWithETF
		holdingNames    map[string]string
		wantOverlap     int // number of overlap alerts
		wantConc        int // number of concentration alerts
	}{
		{
			name:         "no holdings, no alerts",
			etfs:         nil,
			wantOverlap:  0,
			wantConc:     0,
			holdingNames: map[string]string{},
		},
		{
			name: "high overlap triggers alert",
			etfs: []ETFWithHoldings{
				{
					ISIN: "ETF1", Name: "S&P 500",
					Holdings: map[string]float64{"AAPL": 7, "MSFT": 6, "GOOG": 4, "AMZN": 3.5, "NVDA": 3, "META": 2.5, "TSLA": 2, "BRK": 1.8, "JPM": 1.5, "V": 1.3, "UNH": 1.2, "JNJ": 1.1, "PG": 1, "HD": 0.9, "MA": 0.85, "XOM": 0.8, "PFE": 0.75, "ABBV": 0.7, "KO": 0.65, "PEP": 0.6, "AVGO": 0.55, "COST": 0.5, "MRK": 0.45, "TMO": 0.4, "ACN": 0.35, "LLY": 0.3, "DHR": 0.25, "NKE": 0.2, "TXN": 0.15, "LOW": 0.1},
				},
				{
					ISIN: "ETF2", Name: "MSCI World",
					Holdings: map[string]float64{"AAPL": 5, "MSFT": 4.5, "GOOG": 3, "AMZN": 2.5, "NVDA": 2, "META": 1.8, "TSLA": 1.5, "BRK": 1.2, "JPM": 1, "V": 0.9, "UNH": 0.85, "JNJ": 0.8, "PG": 0.75, "HD": 0.7, "MA": 0.65, "XOM": 0.6, "PFE": 0.55, "ABBV": 0.5, "KO": 0.45, "PEP": 0.4, "AVGO": 0.35, "COST": 0.3, "MRK": 0.25, "TMO": 0.2, "ACN": 0.15, "LLY": 0.1, "NESN": 5, "ROCHE": 3, "SAP": 2, "ASML": 1.5},
				},
			},
			holdingsWithETF: []HoldingWithETF{},
			holdingNames:    map[string]string{},
			wantOverlap:     0, // overlap is sum of min weights, calculate: 5+4.5+3+2.5+2+1.8+1.5+1.2+1+0.9+0.85+0.8+0.75+0.7+0.65+0.6+0.55+0.5+0.45+0.4+0.35+0.3+0.25+0.2+0.15+0.1 = ~31.1% — not enough
			wantConc:        0,
		},
		{
			name: "very high overlap triggers alert",
			etfs: []ETFWithHoldings{
				{
					ISIN: "ETF1", Name: "S&P 500 ETF",
					Holdings: map[string]float64{"AAPL": 20, "MSFT": 18, "GOOG": 15, "AMZN": 12, "NVDA": 10},
				},
				{
					ISIN: "ETF2", Name: "US Tech ETF",
					Holdings: map[string]float64{"AAPL": 22, "MSFT": 20, "GOOG": 16, "AMZN": 14, "NVDA": 11},
				},
			},
			holdingsWithETF: []HoldingWithETF{},
			holdingNames:    map[string]string{},
			wantOverlap:     1, // min(20,22)+min(18,20)+min(15,16)+min(12,14)+min(10,11) = 20+18+15+12+10 = 75%
			wantConc:        0,
		},
		{
			name: "single stock concentration triggers alert",
			etfs: []ETFWithHoldings{},
			holdingsWithETF: []HoldingWithETF{
				{
					ISIN:        "ETF1",
					MarketValue: 10000,
					ETFHoldings: map[string]float64{"AAPL": 30, "MSFT": 20, "GOOG": 10},
				},
			},
			holdingNames: map[string]string{"AAPL": "Apple Inc.", "MSFT": "Microsoft Corp.", "GOOG": "Alphabet Inc."},
			wantOverlap:  0,
			wantConc:     3, // AAPL=30%, MSFT=20%, GOOG=10% — all above 5%
		},
		{
			name: "no concentration below threshold",
			etfs: []ETFWithHoldings{},
			holdingsWithETF: []HoldingWithETF{
				{
					ISIN:        "ETF1",
					MarketValue: 10000,
					ETFHoldings: map[string]float64{"AAPL": 4, "MSFT": 3, "GOOG": 2},
				},
			},
			holdingNames: map[string]string{},
			wantOverlap:  0,
			wantConc:     0, // all below 5%
		},
		{
			name: "critical level for 90%+ overlap",
			etfs: []ETFWithHoldings{
				{
					ISIN: "ETF1", Name: "Fund A",
					Holdings: map[string]float64{"AAPL": 50, "MSFT": 50},
				},
				{
					ISIN: "ETF2", Name: "Fund B",
					Holdings: map[string]float64{"AAPL": 50, "MSFT": 50},
				},
			},
			holdingsWithETF: []HoldingWithETF{},
			holdingNames:    map[string]string{},
			wantOverlap:     1, // 100% overlap, critical level
			wantConc:        0,
		},
		{
			name: "critical level for 10%+ concentration",
			etfs: []ETFWithHoldings{},
			holdingsWithETF: []HoldingWithETF{
				{
					ISIN:        "ETF1",
					MarketValue: 10000,
					ETFHoldings: map[string]float64{"AAPL": 15},
				},
			},
			holdingNames: map[string]string{"AAPL": "Apple"},
			wantOverlap:  0,
			wantConc:     1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			alerts := ComputeConcentrationAlerts(tt.etfs, tt.holdingsWithETF, tt.holdingNames)

			overlapAlerts := 0
			concAlerts := 0
			for _, a := range alerts {
				switch a.Type {
				case "overlap":
					overlapAlerts++
				case "concentration":
					concAlerts++
				}
			}

			if overlapAlerts != tt.wantOverlap {
				t.Errorf("overlap alerts = %d, want %d (alerts: %+v)", overlapAlerts, tt.wantOverlap, alerts)
			}
			if concAlerts != tt.wantConc {
				t.Errorf("concentration alerts = %d, want %d (alerts: %+v)", concAlerts, tt.wantConc, alerts)
			}
		})
	}
}

func TestComputeConcentrationAlerts_AlertLevels(t *testing.T) {
	// Test critical overlap (>=90%)
	etfs := []ETFWithHoldings{
		{ISIN: "A", Name: "Fund A", Holdings: map[string]float64{"X": 50, "Y": 50}},
		{ISIN: "B", Name: "Fund B", Holdings: map[string]float64{"X": 50, "Y": 50}},
	}
	alerts := ComputeConcentrationAlerts(etfs, nil, map[string]string{})
	if len(alerts) != 1 {
		t.Fatalf("expected 1 alert, got %d", len(alerts))
	}
	if alerts[0].Level != "critical" {
		t.Errorf("expected critical level for 100%% overlap, got %s", alerts[0].Level)
	}

	// Test warning overlap (70-89%)
	etfs2 := []ETFWithHoldings{
		{ISIN: "A", Name: "Fund A", Holdings: map[string]float64{"X": 40, "Y": 35}},
		{ISIN: "B", Name: "Fund B", Holdings: map[string]float64{"X": 40, "Y": 35, "Z": 25}},
	}
	alerts2 := ComputeConcentrationAlerts(etfs2, nil, map[string]string{})
	if len(alerts2) != 1 {
		t.Fatalf("expected 1 alert, got %d", len(alerts2))
	}
	if alerts2[0].Level != "warning" {
		t.Errorf("expected warning level for 75%% overlap, got %s", alerts2[0].Level)
	}

	// Test critical concentration (>=10%)
	holdings := []HoldingWithETF{
		{ISIN: "E1", MarketValue: 1000, ETFHoldings: map[string]float64{"AAPL": 15}},
	}
	alerts3 := ComputeConcentrationAlerts(nil, holdings, map[string]string{"AAPL": "Apple"})
	if len(alerts3) != 1 {
		t.Fatalf("expected 1 alert, got %d", len(alerts3))
	}
	if alerts3[0].Level != "critical" {
		t.Errorf("expected critical level for 15%% concentration, got %s", alerts3[0].Level)
	}

	// Test warning concentration (5-9.9%)
	holdings2 := []HoldingWithETF{
		{ISIN: "E1", MarketValue: 1000, ETFHoldings: map[string]float64{"AAPL": 7}},
	}
	alerts4 := ComputeConcentrationAlerts(nil, holdings2, map[string]string{"AAPL": "Apple"})
	if len(alerts4) != 1 {
		t.Fatalf("expected 1 alert, got %d", len(alerts4))
	}
	if alerts4[0].Level != "warning" {
		t.Errorf("expected warning level for 7%% concentration, got %s", alerts4[0].Level)
	}
}

func TestComputeConcentrationAlerts_MessageContent(t *testing.T) {
	etfs := []ETFWithHoldings{
		{ISIN: "A", Name: "S&P 500", Holdings: map[string]float64{"X": 50, "Y": 30}},
		{ISIN: "B", Name: "MSCI World", Holdings: map[string]float64{"X": 50, "Y": 30}},
	}
	alerts := ComputeConcentrationAlerts(etfs, nil, map[string]string{})
	if len(alerts) != 1 {
		t.Fatalf("expected 1 alert, got %d", len(alerts))
	}
	if alerts[0].Type != "overlap" {
		t.Errorf("expected overlap type, got %s", alerts[0].Type)
	}
	if alerts[0].Value != 80.0 {
		t.Errorf("expected value 80.0, got %f", alerts[0].Value)
	}

	// Test concentration message includes holding name
	holdings := []HoldingWithETF{
		{ISIN: "E1", MarketValue: 1000, ETFHoldings: map[string]float64{"US0378331005": 8}},
	}
	names := map[string]string{"US0378331005": "Apple Inc."}
	alerts2 := ComputeConcentrationAlerts(nil, holdings, names)
	if len(alerts2) != 1 {
		t.Fatalf("expected 1 alert, got %d", len(alerts2))
	}
	if alerts2[0].Message == "" {
		t.Error("expected non-empty message")
	}
}

func TestFormatPct(t *testing.T) {
	tests := []struct {
		input float64
		want  string
	}{
		{70.0, "70%"},
		{75.5, "75.5%"},
		{100.0, "100%"},
		{5.12, "5.1%"},
		{8.99, "8.9%"},
	}
	for _, tt := range tests {
		got := formatPct(tt.input)
		if got != tt.want {
			t.Errorf("formatPct(%f) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestComputeEffectiveExposure_RealisticPortfolio(t *testing.T) {
	// Simulate a portfolio with VWCE (all-world) and SXR8 (S&P 500)
	// Both hold Apple/Microsoft, so aggregate exposure should be summed
	holdings := []HoldingWithETF{
		{
			ISIN:        "VWCE",
			MarketValue: 60000, // 60% of portfolio
			ETFHoldings: map[string]float64{
				"AAPL": 4.5,
				"MSFT": 3.8,
				"NVDA": 3.0,
				"AMZN": 2.5,
				"GOOG": 2.0,
			},
		},
		{
			ISIN:        "SXR8",
			MarketValue: 40000, // 40% of portfolio
			ETFHoldings: map[string]float64{
				"AAPL": 7.0,
				"MSFT": 6.5,
				"NVDA": 5.0,
				"AMZN": 3.5,
				"META": 2.0,
			},
		},
	}

	exposure := ComputeEffectiveExposure(holdings)

	// AAPL: 0.6*4.5/100 + 0.4*7.0/100 = 0.027 + 0.028 = 0.055 (5.5%)
	expectedAAPL := 0.6*4.5/100 + 0.4*7.0/100
	if math.Abs(exposure["AAPL"]-expectedAAPL) > 0.001 {
		t.Errorf("AAPL exposure = %f, want %f", exposure["AAPL"], expectedAAPL)
	}

	// MSFT: 0.6*3.8/100 + 0.4*6.5/100 = 0.0228 + 0.026 = 0.0488 (4.88%)
	expectedMSFT := 0.6*3.8/100 + 0.4*6.5/100
	if math.Abs(exposure["MSFT"]-expectedMSFT) > 0.001 {
		t.Errorf("MSFT exposure = %f, want %f", exposure["MSFT"], expectedMSFT)
	}

	// META only in SXR8: 0.4*2.0/100 = 0.008 (0.8%)
	expectedMETA := 0.4 * 2.0 / 100
	if math.Abs(exposure["META"]-expectedMETA) > 0.001 {
		t.Errorf("META exposure = %f, want %f", exposure["META"], expectedMETA)
	}

	// GOOG only in VWCE: 0.6*2.0/100 = 0.012 (1.2%)
	expectedGOOG := 0.6 * 2.0 / 100
	if math.Abs(exposure["GOOG"]-expectedGOOG) > 0.001 {
		t.Errorf("GOOG exposure = %f, want %f", exposure["GOOG"], expectedGOOG)
	}

	// Total should sum to less than 100%
	total := 0.0
	for _, v := range exposure {
		total += v
	}
	if total > 1.0 {
		t.Errorf("total exposure = %f, should be <= 1.0", total)
	}
}

func TestComputeSectorAllocation(t *testing.T) {
	tests := []struct {
		name     string
		holdings []HoldingValue
		wantNil  bool
		want     map[string]float64
	}{
		{
			name:    "nil holdings",
			wantNil: true,
		},
		{
			name:     "zero total value",
			holdings: []HoldingValue{{ISIN: "A", MarketValue: 0, SectorWeights: map[string]float64{"Tech": 50}}},
			wantNil:  true,
		},
		{
			name: "single holding full sector weights",
			holdings: []HoldingValue{
				{ISIN: "ETF1", MarketValue: 10000, SectorWeights: map[string]float64{"Technology": 60, "Healthcare": 25, "Finance": 15}},
			},
			want: map[string]float64{"Technology": 60, "Healthcare": 25, "Finance": 15},
		},
		{
			name: "two equal holdings different sectors",
			holdings: []HoldingValue{
				{ISIN: "ETF1", MarketValue: 5000, SectorWeights: map[string]float64{"Technology": 80, "Healthcare": 20}},
				{ISIN: "ETF2", MarketValue: 5000, SectorWeights: map[string]float64{"Finance": 60, "Energy": 40}},
			},
			want: map[string]float64{
				"Technology": 40, // 0.5 * 80
				"Healthcare": 10, // 0.5 * 20
				"Finance":    30, // 0.5 * 60
				"Energy":     20, // 0.5 * 40
			},
		},
		{
			name: "weighted by market value",
			holdings: []HoldingValue{
				{ISIN: "ETF1", MarketValue: 9000, SectorWeights: map[string]float64{"Technology": 50}},
				{ISIN: "ETF2", MarketValue: 1000, SectorWeights: map[string]float64{"Technology": 10}},
			},
			want: map[string]float64{
				"Technology": 46, // 0.9*50 + 0.1*10 = 45+1
			},
		},
		{
			name: "holding with nil sector weights is ignored",
			holdings: []HoldingValue{
				{ISIN: "ETF1", MarketValue: 5000, SectorWeights: map[string]float64{"Technology": 60}},
				{ISIN: "ETF2", MarketValue: 5000, SectorWeights: nil},
			},
			want: map[string]float64{
				"Technology": 30, // 0.5 * 60
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ComputeSectorAllocation(tt.holdings)
			if tt.wantNil {
				if got != nil {
					t.Errorf("expected nil, got %v", got)
				}
				return
			}
			for k, v := range tt.want {
				if math.Abs(got[k]-v) > 0.01 {
					t.Errorf("sector[%q] = %f, want %f", k, got[k], v)
				}
			}
			// Check no extra sectors
			for k := range got {
				if _, ok := tt.want[k]; !ok {
					t.Errorf("unexpected sector %q = %f", k, got[k])
				}
			}
		})
	}
}

func TestComputeEffectiveExposure_TopNSorted(t *testing.T) {
	// Verify that sorting by exposure descending works correctly for top-N
	holdings := []HoldingWithETF{
		{
			ISIN:        "ETF1",
			MarketValue: 10000,
			ETFHoldings: map[string]float64{
				"A": 1, "B": 5, "C": 10, "D": 3, "E": 8,
			},
		},
	}

	exposure := ComputeEffectiveExposure(holdings)

	// Convert to sorted slice (same logic as handler)
	type entry struct {
		ISIN string
		Pct  float64
	}
	sorted := make([]entry, 0, len(exposure))
	for isin, pct := range exposure {
		sorted = append(sorted, entry{ISIN: isin, Pct: pct * 100})
	}
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Pct > sorted[j].Pct
	})

	// Top should be C (10%), then E (8%), then B (5%)
	if sorted[0].ISIN != "C" || sorted[1].ISIN != "E" || sorted[2].ISIN != "B" {
		t.Errorf("unexpected sort order: %+v", sorted)
	}
}

func TestCalculateRealizedPL(t *testing.T) {
	tests := []struct {
		name     string
		txns     []TransactionForPL
		wantPL   float64
		wantISIN map[string]float64
	}{
		{
			name:   "no transactions",
			txns:   nil,
			wantPL: 0,
		},
		{
			name: "buy only, no realized PL",
			txns: []TransactionForPL{
				{Type: "buy", ISIN: "A", Qty: 10, Amt: 1000},
			},
			wantPL: 0,
		},
		{
			name: "buy then sell at profit",
			txns: []TransactionForPL{
				{Type: "buy", ISIN: "A", Qty: 10, Amt: 1000},  // avg cost = 100
				{Type: "sell", ISIN: "A", Qty: 5, Amt: 600},   // sell 5 @ 120, cost = 500, gain = 100
			},
			wantPL:   100,
			wantISIN: map[string]float64{"A": 100},
		},
		{
			name: "buy then sell at loss",
			txns: []TransactionForPL{
				{Type: "buy", ISIN: "A", Qty: 10, Amt: 1000},  // avg cost = 100
				{Type: "sell", ISIN: "A", Qty: 10, Amt: 800},  // sell all @ 80, cost = 1000, loss = -200
			},
			wantPL:   -200,
			wantISIN: map[string]float64{"A": -200},
		},
		{
			name: "multiple buys then partial sell (avg cost)",
			txns: []TransactionForPL{
				{Type: "buy", ISIN: "A", Qty: 10, Amt: 1000},           // 10 @ 100
				{Type: "savings_plan", ISIN: "A", Qty: 10, Amt: 1200},  // 10 @ 120, total 20 @ avg 110
				{Type: "sell", ISIN: "A", Qty: 5, Amt: 700},            // sell 5 @ 140, cost = 5*110=550, gain = 150
			},
			wantPL:   150,
			wantISIN: map[string]float64{"A": 150},
		},
		{
			name: "two securities, mixed gains",
			txns: []TransactionForPL{
				{Type: "buy", ISIN: "A", Qty: 10, Amt: 1000},
				{Type: "buy", ISIN: "B", Qty: 5, Amt: 500},
				{Type: "sell", ISIN: "A", Qty: 10, Amt: 1200},  // +200
				{Type: "sell", ISIN: "B", Qty: 5, Amt: 400},    // -100
			},
			wantPL:   100, // 200 - 100
			wantISIN: map[string]float64{"A": 200, "B": -100},
		},
		{
			name: "transfer in then sell",
			txns: []TransactionForPL{
				{Type: "transfer", ISIN: "A", Qty: 10, Amt: 1000},  // transferred in at cost 1000
				{Type: "sell", ISIN: "A", Qty: 10, Amt: 1500},       // sell for 1500, gain = 500
			},
			wantPL:   500,
			wantISIN: map[string]float64{"A": 500},
		},
		{
			name: "sell without prior buy is ignored",
			txns: []TransactionForPL{
				{Type: "sell", ISIN: "A", Qty: 10, Amt: 1000},
			},
			wantPL: 0,
		},
		{
			name: "empty ISIN transactions are skipped",
			txns: []TransactionForPL{
				{Type: "buy", ISIN: "", Qty: 10, Amt: 1000},
			},
			wantPL: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := CalculateRealizedPL(tt.txns)
			if math.Abs(result.TotalRealizedPL-tt.wantPL) > 0.01 {
				t.Errorf("TotalRealizedPL = %f, want %f", result.TotalRealizedPL, tt.wantPL)
			}
			for isin, want := range tt.wantISIN {
				got := result.BySecurity[isin]
				if math.Abs(got-want) > 0.01 {
					t.Errorf("BySecurity[%q] = %f, want %f", isin, got, want)
				}
			}
		})
	}
}
