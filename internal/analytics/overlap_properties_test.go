package analytics

import (
	"math"
	"testing"
)

// Property tests for BuildOverlapMatrix. The existing _Table test covers
// known fixtures; these lock the two structural invariants the spec
// explicitly requires:
//
//   1. The matrix is symmetric — matrix[i][j] == matrix[j][i] for all i, j.
//      Overlap is a symmetric relation (min-of-pairwise weights), so a
//      bug that computes one half differently from the other would silently
//      mislead a user reading the upper vs. lower triangle.
//   2. The diagonal is 100.0 — an ETF fully overlaps with itself.
//      In the chart this renders as the deepest cell on the diagonal;
//      drifting away from 100 would imply an ETF doesn't fully contain
//      its own constituents (a contradiction).

func assertMatrixSymmetric(t *testing.T, label string, m [][]float64) {
	t.Helper()
	n := len(m)
	for i := 0; i < n; i++ {
		if len(m[i]) != n {
			t.Errorf("%s: row %d has length %d, want %d (matrix should be square)", label, i, len(m[i]), n)
			return
		}
		for j := 0; j < n; j++ {
			if math.Abs(m[i][j]-m[j][i]) > 1e-9 {
				t.Errorf("%s: matrix[%d][%d]=%.6f != matrix[%d][%d]=%.6f (symmetry broken)",
					label, i, j, m[i][j], j, i, m[j][i])
			}
		}
	}
}

func assertDiagonal100(t *testing.T, label string, m [][]float64) {
	t.Helper()
	for i, row := range m {
		if math.Abs(row[i]-100.0) > 1e-9 {
			t.Errorf("%s: matrix[%d][%d]=%.6f, want 100.0 (diagonal must be full self-overlap)",
				label, i, i, row[i])
		}
	}
}

func TestBuildOverlapMatrix_SymmetryAndDiagonal_TwoETFs(t *testing.T) {
	etfs := []ETFWithHoldings{
		{ISIN: "A", Holdings: map[string]float64{"X": 60, "Y": 40}},
		{ISIN: "B", Holdings: map[string]float64{"X": 30, "Z": 70}},
	}
	m := BuildOverlapMatrix(etfs)
	assertMatrixSymmetric(t, "two ETFs", m)
	assertDiagonal100(t, "two ETFs", m)
}

func TestBuildOverlapMatrix_SymmetryAndDiagonal_FiveETFsAsymmetricMix(t *testing.T) {
	// Deliberately uneven overlap shape (some pairs share nothing, others a
	// lot) — would catch a copy-paste bug where matrix[j][i] was forgotten.
	etfs := []ETFWithHoldings{
		{ISIN: "WORLD", Holdings: map[string]float64{"AAPL": 5, "MSFT": 4, "GOOG": 3, "NESN": 1}},
		{ISIN: "USA", Holdings: map[string]float64{"AAPL": 7, "MSFT": 6, "GOOG": 5}},
		{ISIN: "EUR", Holdings: map[string]float64{"NESN": 4, "SAP": 3, "ASML": 3}},
		{ISIN: "TECH", Holdings: map[string]float64{"AAPL": 10, "MSFT": 9, "GOOG": 8}},
		{ISIN: "BOND", Holdings: map[string]float64{"DE10Y": 50, "US10Y": 50}}, // zero overlap with all stocks
	}
	m := BuildOverlapMatrix(etfs)
	assertMatrixSymmetric(t, "five ETFs", m)
	assertDiagonal100(t, "five ETFs", m)
	// Bond ETF has no overlap with any equity ETF — row and column should
	// all be 0 off-diagonal.
	for i := 0; i < 4; i++ {
		if math.Abs(m[4][i]) > 1e-9 {
			t.Errorf("matrix[4][%d]=%.4f, want 0 (BOND has no equity overlap)", i, m[4][i])
		}
		if math.Abs(m[i][4]) > 1e-9 {
			t.Errorf("matrix[%d][4]=%.4f, want 0 (BOND has no equity overlap)", i, m[i][4])
		}
	}
}

func TestBuildOverlapMatrix_SymmetryWithIdenticalHoldings(t *testing.T) {
	// Two ETFs with identical constituents → off-diagonal should be 100 too.
	// (Symmetric self-overlap edge case.)
	weights := map[string]float64{"AAPL": 40, "MSFT": 35, "GOOG": 25}
	etfs := []ETFWithHoldings{
		{ISIN: "A", Holdings: weights},
		{ISIN: "B", Holdings: weights},
	}
	m := BuildOverlapMatrix(etfs)
	assertMatrixSymmetric(t, "identical holdings", m)
	assertDiagonal100(t, "identical holdings", m)
	if math.Abs(m[0][1]-100) > 0.5 {
		t.Errorf("identical holdings should overlap 100%%, got matrix[0][1]=%.4f", m[0][1])
	}
}

func TestBuildOverlapMatrix_SingleETF(t *testing.T) {
	// Edge: 1×1 matrix is trivially symmetric, diagonal must still hit 100.
	m := BuildOverlapMatrix([]ETFWithHoldings{
		{ISIN: "A", Holdings: map[string]float64{"X": 100}},
	})
	assertMatrixSymmetric(t, "single ETF", m)
	assertDiagonal100(t, "single ETF", m)
}

func TestBuildOverlapMatrix_Empty(t *testing.T) {
	// Edge: empty input yields a 0-length matrix — both invariants hold
	// vacuously, but the assertion helpers should not panic.
	m := BuildOverlapMatrix(nil)
	assertMatrixSymmetric(t, "empty", m)
	assertDiagonal100(t, "empty", m)
	if len(m) != 0 {
		t.Errorf("empty input → matrix len %d, want 0", len(m))
	}
}
