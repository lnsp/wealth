package scheduler

import (
	"testing"

	"github.com/lnsp/wealth/internal/market"
)

// Audit W2: fund data was fetched for asset_class = 'etf' only.
func TestFundDataTarget(t *testing.T) {
	cases := map[string][2]bool{ // class -> {fetch, probe}
		"etf":        {true, false},
		"bond":       {true, false},
		"fund":       {true, false},
		"stock":      {false, true},
		"crypto":     {false, false},
		"commodity":  {false, false},
		"derivative": {false, false},
	}
	for class, want := range cases {
		fetch, probe := fundDataTarget(class)
		if fetch != want[0] || probe != want[1] {
			t.Errorf("fundDataTarget(%q) = %v/%v, want %v/%v", class, fetch, probe, want[0], want[1])
		}
	}
}

func TestMetadataProvesEquityFund(t *testing.T) {
	equity := &market.ETFMetadata{TER: 0.18, SectorWeights: map[string]float64{"Technology": 24}}
	etc := &market.ETFMetadata{TER: 0.12}                                                    // gold ETC: no breakdown
	bond := &market.ETFMetadata{TER: 0.07, CountryWeights: map[string]float64{"France": 20}} // bond fund: countries only
	noTER := &market.ETFMetadata{SectorWeights: map[string]float64{"Technology": 24}}
	if !metadataProvesEquityFund(equity) {
		t.Error("an equity fund profile must allow reclassification")
	}
	for name, meta := range map[string]*market.ETFMetadata{"etc": etc, "bond": bond, "no TER": noTER, "nil": nil} {
		if metadataProvesEquityFund(meta) {
			t.Errorf("%s must not reclassify a stock as an equity ETF", name)
		}
	}
}
