package parser

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
)

func TestParseRevolutSavings(t *testing.T) {
	csv := `Date,Description,Gross interest,Money in,Money out,Balance

Date,Description,Gross interest,Money in,Money out,Balance
"Apr 25, 2025","Deposit to ""Instant Access Savings""",,"€2,000.00",,"€2,000.00"
"Apr 25, 2025","Deposit to ""Instant Access Savings""",,€826.00,,"€2,826.00"
"Apr 25, 2025","Withdrawal from ""Instant Access Savings""",,,"€1,000.00","€1,826.00"
"Apr 25, 2025","Deposit to ""Instant Access Savings""",,"€4,542.63",,"€6,368.63"
"Apr 26, 2025","Net interest paid to ""Instant Access Savings"" for Apr 26, 2025",2.25%,€0.30,,"€6,368.93"
`

	accountID := uuid.New()
	txns, _, result, err := ParseCSV([]byte(csv), accountID)
	if err != nil {
		t.Fatalf("ParseCSV: %v", err)
	}
	if result.Institution != "revolut" {
		t.Errorf("institution: expected revolut, got %s", result.Institution)
	}
	if result.AccountType != "savings" {
		t.Errorf("account type: expected savings, got %s", result.AccountType)
	}
	if len(txns) != 5 {
		t.Fatalf("expected 5 transactions, got %d", len(txns))
	}

	// First three: deposits
	for _, i := range []int{0, 1, 3} {
		if txns[i].Type != "deposit" {
			t.Errorf("txn[%d] type: expected deposit, got %s", i, txns[i].Type)
		}
		if txns[i].Currency != "EUR" {
			t.Errorf("txn[%d] currency: expected EUR, got %s", i, txns[i].Currency)
		}
	}
	if txns[0].Amount != 2000.00 {
		t.Errorf("txn[0] amount: expected 2000.00, got %f", txns[0].Amount)
	}
	if txns[1].Amount != 826.00 {
		t.Errorf("txn[1] amount: expected 826.00, got %f", txns[1].Amount)
	}
	if txns[3].Amount != 4542.63 {
		t.Errorf("txn[3] amount: expected 4542.63, got %f", txns[3].Amount)
	}

	// Withdrawal
	if txns[2].Type != "withdrawal" {
		t.Errorf("txn[2] type: expected withdrawal, got %s", txns[2].Type)
	}
	if txns[2].Amount != 1000.00 {
		t.Errorf("txn[2] amount: expected 1000.00, got %f", txns[2].Amount)
	}

	// Interest
	if txns[4].Type != "interest" {
		t.Errorf("txn[4] type: expected interest, got %s", txns[4].Type)
	}
	if txns[4].Amount != 0.30 {
		t.Errorf("txn[4] amount: expected 0.30, got %f", txns[4].Amount)
	}

	// Hashes unique
	seen := make(map[string]bool)
	for i, txn := range txns {
		if txn.ImportHash == "" {
			t.Errorf("txn[%d]: empty import hash", i)
		}
		if seen[txn.ImportHash] {
			t.Errorf("txn[%d]: duplicate import hash", i)
		}
		seen[txn.ImportHash] = true
	}
}

func TestParseRevolutCurrent(t *testing.T) {
	csv := `Type,Product,Started Date,Completed Date,Description,Amount,Fee,Currency,State,Balance
Card Payment,Current,2026-05-16 12:42:21,2026-05-17 10:37:40,Lost Weekend,-3.20,0.00,EUR,COMPLETED,2896.03
Card Payment,Current,2026-05-17 14:57:55,,Lost Weekend,-3.20,0.00,EUR,PENDING,
Transfer,Current,2026-05-17 18:52:49,2026-05-17 18:52:50,To Max Mustermann,-11.90,0.00,EUR,COMPLETED,2884.13
Transfer,Current,2026-05-17 19:33:27,2026-05-17 19:33:29,To Max Mustermann,-20.50,0.00,EUR,COMPLETED,2863.63
Card Payment,Current,2026-05-17 20:18:03,,Fräulein Grüneis,-4.50,0.00,EUR,PENDING,
Card Refund,Current,2026-05-17 20:41:59,2026-05-17 20:42:00,Fräulein Grüneis,0.50,0.00,EUR,COMPLETED,2864.13
`

	accountID := uuid.New()
	txns, _, result, err := ParseCSV([]byte(csv), accountID)
	if err != nil {
		t.Fatalf("ParseCSV: %v", err)
	}
	if result.Institution != "revolut" {
		t.Errorf("institution: expected revolut, got %s", result.Institution)
	}
	if result.AccountType != "checking" {
		t.Errorf("account type: expected checking, got %s", result.AccountType)
	}
	// 6 input rows, 2 PENDING skipped → 4 transactions
	if len(txns) != 4 {
		t.Fatalf("expected 4 transactions (pending skipped), got %d", len(txns))
	}

	// First: card payment → withdrawal, uses completed date
	if txns[0].Type != "withdrawal" {
		t.Errorf("txn[0] type: expected withdrawal, got %s", txns[0].Type)
	}
	if txns[0].Amount != 3.20 {
		t.Errorf("txn[0] amount: expected 3.20, got %f", txns[0].Amount)
	}
	if txns[0].Counterparty != "Lost Weekend" {
		t.Errorf("txn[0] counterparty: expected Lost Weekend, got %s", txns[0].Counterparty)
	}
	if txns[0].Date.Format("2006-01-02") != "2026-05-17" {
		t.Errorf("txn[0] date: expected 2026-05-17, got %s", txns[0].Date.Format("2006-01-02"))
	}

	// Transfer (negative) → withdrawal
	if txns[1].Type != "withdrawal" {
		t.Errorf("txn[1] type: expected withdrawal, got %s", txns[1].Type)
	}
	if txns[1].Counterparty != "To Max Mustermann" {
		t.Errorf("txn[1] counterparty: expected To Max Mustermann, got %s", txns[1].Counterparty)
	}

	// Card refund → deposit
	if txns[3].Type != "deposit" {
		t.Errorf("txn[3] type: expected deposit, got %s", txns[3].Type)
	}
	if txns[3].Amount != 0.50 {
		t.Errorf("txn[3] amount: expected 0.50, got %f", txns[3].Amount)
	}

	// Two same-merchant transfers on the same day with different times
	// must produce distinct import hashes thanks to the timestamp-aware hash.
	if txns[1].ImportHash == txns[2].ImportHash {
		t.Error("expected distinct hashes for two same-day transfers, got collision")
	}
}

func TestParseRevolutCurrentUpperSnakeType(t *testing.T) {
	// The Business API export uses upper snake case for Type — make sure
	// classification works on both forms.
	csv := `Type,Product,Started Date,Completed Date,Description,Amount,Fee,Currency,State,Balance
TOPUP,Current,2026-05-01 09:14:22,2026-05-01 09:14:23,Apple Pay Top-Up,500.00,0.00,EUR,COMPLETED,500.00
CARD_PAYMENT,Current,2026-05-02 12:42:21,2026-05-03 10:37:40,Lost Weekend,-3.20,0.00,EUR,COMPLETED,496.80
ATM,Current,2026-05-08 11:02:10,2026-05-08 11:02:12,Sparkasse ATM,-100.00,1.99,EUR,COMPLETED,396.80
`
	txns, _, _, err := ParseCSV([]byte(csv), uuid.New())
	if err != nil {
		t.Fatalf("ParseCSV: %v", err)
	}
	if len(txns) != 3 {
		t.Fatalf("expected 3 transactions, got %d", len(txns))
	}
	if txns[0].Type != "deposit" {
		t.Errorf("TOPUP: expected deposit, got %s", txns[0].Type)
	}
	if txns[1].Type != "withdrawal" {
		t.Errorf("CARD_PAYMENT: expected withdrawal, got %s", txns[1].Type)
	}
	if txns[2].Type != "withdrawal" {
		t.Errorf("ATM: expected withdrawal, got %s", txns[2].Type)
	}
	if txns[2].Fee != 1.99 {
		t.Errorf("ATM fee: expected 1.99, got %f", txns[2].Fee)
	}
}

func TestRevolutFixtures(t *testing.T) {
	// Verify the shipped E2E fixtures parse cleanly so the E2E test has
	// a stable contract about row counts.
	cases := []struct {
		path        string
		institution string
		acctType    string
		minTxns     int
	}{
		{filepath.Join("testdata", "revolut_checking.csv"), "revolut", "checking", 9},
		{filepath.Join("testdata", "revolut_savings.csv"), "revolut", "savings", 7},
	}
	for _, c := range cases {
		data, err := os.ReadFile(c.path)
		if err != nil {
			t.Fatalf("read %s: %v", c.path, err)
		}
		txns, _, result, err := ParseCSV(data, uuid.New())
		if err != nil {
			t.Fatalf("ParseCSV(%s): %v", c.path, err)
		}
		if result.Institution != c.institution {
			t.Errorf("%s: institution = %s, want %s", c.path, result.Institution, c.institution)
		}
		if result.AccountType != c.acctType {
			t.Errorf("%s: account type = %s, want %s", c.path, result.AccountType, c.acctType)
		}
		if len(txns) < c.minTxns {
			t.Errorf("%s: got %d transactions, want >= %d", c.path, len(txns), c.minTxns)
		}
	}
}

func TestParseRevolutAmount(t *testing.T) {
	cases := []struct {
		in          string
		wantValue   float64
		wantCurrency string
	}{
		{"€2,000.00", 2000.00, "EUR"},
		{"€0.30", 0.30, "EUR"},
		{"$1,234.56", 1234.56, "USD"},
		{"£12.50", 12.50, "GBP"},
		{"", 0, ""},
		{"42.00", 42.00, ""},
	}
	for _, c := range cases {
		v, cur, err := parseRevolutAmount(c.in)
		if err != nil {
			t.Errorf("parseRevolutAmount(%q): %v", c.in, err)
			continue
		}
		if v != c.wantValue {
			t.Errorf("parseRevolutAmount(%q) value = %f, want %f", c.in, v, c.wantValue)
		}
		if cur != c.wantCurrency {
			t.Errorf("parseRevolutAmount(%q) currency = %q, want %q", c.in, cur, c.wantCurrency)
		}
	}
}
