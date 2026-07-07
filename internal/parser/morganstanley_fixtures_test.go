package parser

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
)

// TestMorganStanleyAgainstRealExports validates the parser against the actual
// CSV files in exports/morganstanley/. Skips silently if the fixtures are
// absent (e.g. CI without the personal data).
func TestMorganStanleyAgainstRealExports(t *testing.T) {
	cases := []struct {
		file              string
		minTxns, maxTxns  int
		minVests          int
		anyVested         bool
		anyUnvested       bool
		expectInstitution string
	}{
		{"Releases Report.csv", 80, 100, 80, true, false, "morgan_stanley"},
		{"Releases Net Shares Report.csv", 80, 100, 0, false, false, "morgan_stanley"},
		{"Withdrawals Report.csv", 10, 30, 0, false, false, "morgan_stanley"},
		{"Unvested GSUs As At Date.csv", 0, 0, 100, false, true, "morgan_stanley"},
	}
	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			path := filepath.Join("..", "..", "exports", "morganstanley", tc.file)
			data, err := os.ReadFile(path)
			if err != nil {
				t.Skipf("fixture not available: %v", err)
			}
			txns, vests, result, err := ParseCSV(data, uuid.New())
			if err != nil {
				t.Fatalf("ParseCSV(%s): %v", tc.file, err)
			}
			if result.Institution != tc.expectInstitution {
				t.Errorf("institution = %s, want %s", result.Institution, tc.expectInstitution)
			}
			if len(txns) < tc.minTxns || len(txns) > tc.maxTxns {
				t.Errorf("transactions count = %d, want [%d, %d]", len(txns), tc.minTxns, tc.maxTxns)
			}
			if len(vests) < tc.minVests {
				t.Errorf("vests count = %d, want >= %d", len(vests), tc.minVests)
			}
			gotVested, gotUnvested := false, false
			for _, v := range vests {
				if v.Vested {
					gotVested = true
				} else {
					gotUnvested = true
				}
			}
			if tc.anyVested && !gotVested {
				t.Error("expected at least one vested row")
			}
			if tc.anyUnvested && !gotUnvested {
				t.Error("expected at least one unvested row")
			}
		})
	}
}
