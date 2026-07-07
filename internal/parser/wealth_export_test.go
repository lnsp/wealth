package parser

import (
	"math"
	"sort"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// Spec: "Export Transactions" downloads valid CSV that re-imports cleanly.
// The export format from internal/handler/settings.go HandleExportTransactions
// is round-tripped here through the parser to verify:
//
//  1. The parser recognizes the export header (Detect → true).
//  2. Every row parses to a Transaction with matching field values.
//  3. Re-importing the same CSV produces identical ImportHash sequences
//     (so the DB UNIQUE constraint catches re-uploads).

// BOM + the actual header matches what HandleExportTransactions emits.
const wealthExportSample = "\xEF\xBB\xBFdate;type;account;counterparty;security_isin;quantity;price;amount;fee;tax;currency;reference\n" +
	"2026-01-15;deposit;Checking;Employer GmbH;;0.00000000;0.00000000;2500.0000;0.0000;0.0000;EUR;Salary March\n" +
	"2026-02-01;buy;Brokerage;Trade Republic;IE00B3RBWM25;10.50000000;119.50000000;-1254.7500;0.9900;0.0000;EUR;TR-OR-001\n" +
	"2026-03-15;dividend;Brokerage;iShares;IE00B3RBWM25;0.00000000;0.00000000;120.0000;0.0000;31.6500;EUR;\n" +
	"2026-04-15;sell;Brokerage;Trade Republic;IE00B3RBWM25;5.00000000;121.00000000;605.0000;0.9900;0.0000;EUR;TR-OR-002\n" +
	"2026-05-01;withdrawal;Checking;Rent;;0.00000000;0.00000000;-850.0000;0.0000;0.0000;EUR;\n"

func TestWealthExport_RoundTrip(t *testing.T) {
	acct := uuid.New()
	txns, _, result, err := ParseCSV([]byte(wealthExportSample), acct)
	if err != nil {
		t.Fatalf("ParseCSV: %v", err)
	}
	if result.Institution != "wealth_export" {
		t.Errorf("institution = %q, want wealth_export", result.Institution)
	}
	if len(txns) != 5 {
		t.Fatalf("expected 5 txns, got %d", len(txns))
	}

	// Spot-check field round-tripping.
	deposit := txns[0]
	if deposit.Type != "deposit" {
		t.Errorf("txn[0] type = %s, want deposit", deposit.Type)
	}
	if math.Abs(deposit.Amount-2500) > 0.0001 {
		t.Errorf("txn[0] amount = %.4f, want 2500", deposit.Amount)
	}
	if deposit.Counterparty != "Employer GmbH" {
		t.Errorf("txn[0] counterparty = %q, want Employer GmbH", deposit.Counterparty)
	}
	if deposit.SecurityISIN != "" {
		t.Errorf("txn[0] ISIN = %q, want empty (cash txn)", deposit.SecurityISIN)
	}

	buy := txns[1]
	if buy.Type != "buy" {
		t.Errorf("txn[1] type = %s, want buy", buy.Type)
	}
	if buy.SecurityISIN != "IE00B3RBWM25" {
		t.Errorf("txn[1] ISIN = %q, want IE00B3RBWM25", buy.SecurityISIN)
	}
	if math.Abs(buy.Quantity-10.5) > 0.0001 {
		t.Errorf("txn[1] quantity = %.4f, want 10.5", buy.Quantity)
	}
	if math.Abs(buy.Price-119.5) > 0.0001 {
		t.Errorf("txn[1] price = %.4f, want 119.5", buy.Price)
	}
	if math.Abs(buy.Amount-(-1254.75)) > 0.0001 {
		t.Errorf("txn[1] amount = %.4f, want -1254.75 (signed)", buy.Amount)
	}
	if math.Abs(buy.Fee-0.99) > 0.0001 {
		t.Errorf("txn[1] fee = %.4f, want 0.99", buy.Fee)
	}

	div := txns[2]
	if div.Type != "dividend" {
		t.Errorf("txn[2] type = %s, want dividend", div.Type)
	}
	if math.Abs(div.Tax-31.65) > 0.0001 {
		t.Errorf("txn[2] tax = %.4f, want 31.65", div.Tax)
	}

	withdrawal := txns[4]
	if withdrawal.Type != "withdrawal" {
		t.Errorf("txn[4] type = %s, want withdrawal", withdrawal.Type)
	}
	if math.Abs(withdrawal.Amount-(-850)) > 0.0001 {
		t.Errorf("txn[4] amount = %.4f, want -850 (signed)", withdrawal.Amount)
	}

	// Every row should have a non-empty ImportHash and they must be unique.
	seen := make(map[string]bool)
	for _, txn := range txns {
		if txn.ImportHash == "" {
			t.Error("empty ImportHash on a parsed row")
		}
		if seen[txn.ImportHash] {
			t.Errorf("duplicate ImportHash %s within single parse", txn.ImportHash)
		}
		seen[txn.ImportHash] = true
	}
}

func TestWealthExport_ReimportIdempotent(t *testing.T) {
	// Re-importing the same CSV into the same account must produce the
	// same hash sequence so the DB UNIQUE(import_hash) constraint catches
	// re-uploads.
	acct := uuid.New()
	pass1, _, _, err := ParseCSV([]byte(wealthExportSample), acct)
	if err != nil {
		t.Fatalf("first parse: %v", err)
	}
	pass2, _, _, err := ParseCSV([]byte(wealthExportSample), acct)
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
		t.Errorf("hash sequences diverged across re-imports — re-upload would duplicate rows")
	}
}

func TestWealthExport_DifferentAccountsProduceDifferentHashes(t *testing.T) {
	// Importing the export into account B must NOT collide with hashes
	// from account A — account_id is part of the hash.
	acctA := uuid.New()
	acctB := uuid.New()
	passA, _, _, _ := ParseCSV([]byte(wealthExportSample), acctA)
	passB, _, _, _ := ParseCSV([]byte(wealthExportSample), acctB)
	if len(passA) != len(passB) || len(passA) == 0 {
		t.Fatalf("expected matching non-zero counts, got %d / %d", len(passA), len(passB))
	}
	for i := range passA {
		if passA[i].ImportHash == passB[i].ImportHash {
			t.Errorf("txn %d hash collision across accounts: %s", i, passA[i].ImportHash)
		}
	}
}

func TestWealthExport_DetectIgnoresOtherFormats(t *testing.T) {
	// The export-format Detect must NOT trigger on Sparkasse, N26, etc.
	// Sample headers from other parsers:
	cases := []struct {
		name string
		csv  string
	}{
		{"sparkasse", "Auftragskonto;Buchungstag;Betrag;Währung\nDE1;01.01.2026;100,00;EUR"},
		{"n26", `"Date","Payee","Amount (EUR)"\n"2026-01-01","Test","100"`},
	}
	p := &WealthExportParser{}
	for _, c := range cases {
		// We only need to check Detect on the first header line.
		header := strings.Split(strings.SplitN(c.csv, "\n", 2)[0], ";")
		if p.Detect(header) {
			t.Errorf("%s header should NOT match wealth_export parser, but did", c.name)
		}
	}
}
