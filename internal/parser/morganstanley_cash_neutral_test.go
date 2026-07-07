package parser

import (
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// Morgan Stanley accounts are modeled as cash-neutral (commit 862620e):
// vests emit transfer (in-kind grant, no cash); sells emit a sell + an
// auto-wire withdrawal of equal amount; explicit "Cash" rows in the
// Withdrawals Report are skipped to avoid double-counting. After importing
// any combination of MS CSVs, the net cash flow within the MS account must
// be zero — otherwise the account would carry phantom cash that the rest
// of the system never spends.
//
// "Cash flow" here = sum over txns of:
//   +amount for type=deposit / dividend / interest / sell / cash_transfer_in
//   −amount for type=withdrawal / fee / tax / buy / savings_plan / cash_transfer_out
//   0       for type=transfer / transfer_out (in-kind, no cash impact)

func cashImpactOf(t Transaction) float64 {
	switch t.Type {
	case "deposit", "dividend", "interest", "sell", "cash_transfer_in":
		return t.Amount
	case "withdrawal", "fee", "tax", "buy", "savings_plan", "cash_transfer_out":
		return -t.Amount
	}
	return 0 // transfer / transfer_out are in-kind
}

func TestMorganStanley_RemainsCashNeutralAcrossAllReports(t *testing.T) {
	files := []string{
		"Releases Report.csv",            // 80+ vests → all transfer (in-kind), zero cash impact
		"Releases Net Shares Report.csv", // 80+ net-share vests → same
		"Withdrawals Report.csv",         // 10-30 sells → each sell + auto-wire = 0 net
		"Withdrawal Wire Report.csv",     // optional, may not exist
	}
	acct := uuid.New()
	var all []Transaction
	loaded := 0
	for _, f := range files {
		path := filepath.Join("..", "..", "exports", "morganstanley", f)
		data, err := os.ReadFile(path)
		if err != nil {
			continue // optional fixture
		}
		txns, _, _, err := ParseCSV(data, acct)
		if err != nil {
			// Some MS exports (e.g. Withdrawal Wire Report) use a header
			// shape the parser doesn't auto-detect — skip rather than fail
			// since the cash-neutral invariant should hold on the recognized
			// reports alone.
			t.Logf("skip %s: %v", f, err)
			continue
		}
		all = append(all, txns...)
		loaded++
	}
	if loaded == 0 {
		t.Skip("no MS fixtures available")
	}

	totalCash := 0.0
	for _, txn := range all {
		totalCash += cashImpactOf(txn)
	}
	// Within 1 cent of zero — FP slop is acceptable on dollar-denominated
	// sums of 100+ rows.
	if math.Abs(totalCash) > 0.01 {
		t.Errorf("MS account net cash flow = %.4f USD, want ~0 (cash-neutral model invariant)", totalCash)
	}

	// Per-sell pairing: every sell must be matched by exactly one withdrawal
	// with the same date + amount (auto-wire). Mismatches indicate either
	// the auto-wire didn't fire or a stray explicit cash row leaked through.
	sells := 0
	wires := 0
	wireByOrder := make(map[string]float64)
	sellByOrder := make(map[string]float64)
	for _, txn := range all {
		switch txn.Type {
		case "sell":
			sells++
			sellByOrder[strings.TrimSuffix(txn.Reference, "-wire")] = txn.Amount
		case "withdrawal":
			wires++
			wireByOrder[strings.TrimSuffix(txn.Reference, "-wire")] = txn.Amount
		}
	}
	if sells != wires {
		t.Errorf("sells (%d) and auto-wires (%d) must pair 1:1", sells, wires)
	}
	for orderRef, sellAmt := range sellByOrder {
		wireAmt, ok := wireByOrder[orderRef]
		if !ok {
			t.Errorf("sell %s has no matching auto-wire withdrawal", orderRef)
			continue
		}
		if math.Abs(sellAmt-wireAmt) > 0.01 {
			t.Errorf("sell %s = %.2f but wire = %.2f — should match exactly", orderRef, sellAmt, wireAmt)
		}
	}
}

func TestMorganStanley_ReimportProducesIdenticalHashes(t *testing.T) {
	// Re-importing each MS CSV must produce identical hash sequences — the
	// DB UNIQUE(import_hash) constraint then guarantees no duplicate rows.
	files := []string{
		"Releases Report.csv",
		"Releases Net Shares Report.csv",
		"Withdrawals Report.csv",
	}
	for _, f := range files {
		t.Run(f, func(t *testing.T) {
			path := filepath.Join("..", "..", "exports", "morganstanley", f)
			data, err := os.ReadFile(path)
			if err != nil {
				t.Skipf("fixture missing: %v", err)
			}
			acct := uuid.New()
			pass1, _, _, err := ParseCSV(data, acct)
			if err != nil {
				t.Fatalf("first parse: %v", err)
			}
			pass2, _, _, err := ParseCSV(data, acct)
			if err != nil {
				t.Fatalf("second parse: %v", err)
			}
			if len(pass1) != len(pass2) {
				t.Errorf("count diverged: %d → %d", len(pass1), len(pass2))
			}
			h1 := make([]string, 0, len(pass1))
			h2 := make([]string, 0, len(pass2))
			for _, t := range pass1 {
				h1 = append(h1, t.ImportHash)
			}
			for _, t := range pass2 {
				h2 = append(h2, t.ImportHash)
			}
			sort.Strings(h1)
			sort.Strings(h2)
			if strings.Join(h1, "|") != strings.Join(h2, "|") {
				t.Errorf("%s: import hashes differ across re-imports — re-upload would duplicate rows", f)
			}
		})
	}
}

func TestMorganStanley_CashRowsSkipped(t *testing.T) {
	// Synthetic test: the Withdrawals Report's "Cash" rows must NOT
	// produce a withdrawal transaction — the proceeds are already covered
	// by the auto-wire on the matching sell row. Importing them would
	// double-debit.
	csv := `Execution Date,Order Number,Plan,Type,Order Status,Price,Quantity,Net Amount,Net Share Proceeds,Tax Payment Method
08-Mar-2024,WRC-A,GSU Class C,Sale,Complete,$100.00,-10.0,"$1,000.00",0,N/A
09-Mar-2024,WRC-B,Cash,Sale,Complete,$1.00,-1000.0,"$1,000.00",0,N/A`

	txns, _, _, err := ParseCSV([]byte(csv), uuid.New())
	if err != nil {
		t.Fatalf("ParseCSV: %v", err)
	}
	// Expect: 1 sell + 1 auto-wire withdrawal from the GSU row; the Cash
	// row is dropped entirely. Net cash = +1000 (sell) − 1000 (wire) = 0.
	if len(txns) != 2 {
		t.Fatalf("expected 2 txns (sell + auto-wire), got %d (Cash row should be skipped)", len(txns))
	}
	totalCash := 0.0
	for _, txn := range txns {
		totalCash += cashImpactOf(txn)
	}
	if math.Abs(totalCash) > 0.01 {
		t.Errorf("synthetic GSU + Cash mix: net cash = %.4f, want 0", totalCash)
	}
}
