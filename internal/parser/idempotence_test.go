package parser

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// CSV re-import idempotence: each supported parser must produce identical
// ImportHash values when given the same CSV input twice. The DB's
// import_hash UNIQUE constraint then prevents duplicate rows. Without
// this guarantee, a user re-uploading their monthly statement would
// double-count every transaction.
//
// Each fixture below is a minimum-viable CSV sample for one supported
// institution. The test parses each twice with a fresh account_id (since
// account_id is part of some hashes), then asserts the set of hashes
// matches byte-for-byte across the two parses.

func TestParser_ReimportIsIdempotent(t *testing.T) {
	cases := []struct {
		name string
		csv  string
		// expectedCount is the number of taxable/cash transactions produced
		// per parse. Doubles as a regression guard against the parser
		// silently dropping or duplicating rows.
		expectedCount int
	}{
		{
			name: "sparkasse",
			csv: `Auftragskonto;Buchungstag;Valutadatum;Buchungstext;Verwendungszweck;Begünstigter/Zahlungspflichtiger;Kontonummer;BLZ;Betrag;Währung
DE123456789;01.03.2026;01.03.2026;GEHALT;Gehalt März 2026;Arbeitgeber GmbH;987654321;10050000;2.500,00;EUR
DE123456789;02.03.2026;02.03.2026;LASTSCHRIFT;Miete März;Vermieter;111222333;10050000;-850,00;EUR
DE123456789;03.03.2026;03.03.2026;ZINSEN;Zinsgutschrift Q1;Sparkasse;000000000;10050000;12,50;EUR`,
			expectedCount: 3,
		},
		{
			name: "n26",
			csv: `"Date","Payee","Account number","Transaction type","Payment reference","Amount (EUR)","Amount (Foreign Currency)","Type Foreign Currency","Exchange Rate"
"2026-03-01","Employer GmbH","DE123","Income","Salary March","2500.00","","",""
"2026-03-05","Rewe","","MasterCard Payment","Groceries","-45.67","","",""`,
			expectedCount: 2,
		},
		{
			name: "scalable",
			csv: `date;status;type;sub_type;side;isin;description;quantity;amount;currency;is_cancellation
2026-03-01;SETTLED;SECURITY_TRANSACTION;SINGLE;BUY;IE00B3RBWM25;Vanguard FTSE All-World;10.5;-1254.75;EUR;false
2026-03-15;SETTLED;SECURITY_TRANSACTION;SAVINGS_PLAN;BUY;IE00B4L5Y983;iShares MSCI World;5.0;-411.50;EUR;false
2026-03-20;SETTLED;CASH_TRANSACTION;DEPOSIT;;;Sparplan;;2000;EUR;false`,
			expectedCount: 3,
		},
		{
			name: "revolut current",
			csv: `Type,Product,Started Date,Completed Date,Description,Amount,Fee,Currency,State,Balance
Card Payment,Current,2026-05-16 12:42:21,2026-05-17 10:37:40,Lost Weekend,-3.20,0.00,EUR,COMPLETED,2896.03
Transfer,Current,2026-05-17 18:52:49,2026-05-17 18:52:50,To Max Mustermann,-11.90,0.00,EUR,COMPLETED,2884.13
Card Refund,Current,2026-05-17 20:41:59,2026-05-17 20:42:00,Fräulein Grüneis,0.50,0.00,EUR,COMPLETED,2864.13
`,
			expectedCount: 3,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// Two parse runs with the SAME account_id. Hashes derive from
			// the row contents + account_id, so they must match.
			acct := uuid.New()
			pass1, _, _, err := ParseCSV([]byte(c.csv), acct)
			if err != nil {
				t.Fatalf("first parse: %v", err)
			}
			pass2, _, _, err := ParseCSV([]byte(c.csv), acct)
			if err != nil {
				t.Fatalf("second parse: %v", err)
			}
			if len(pass1) != c.expectedCount {
				t.Errorf("first parse produced %d txns, want %d", len(pass1), c.expectedCount)
			}
			if len(pass2) != c.expectedCount {
				t.Errorf("second parse produced %d txns, want %d", len(pass2), c.expectedCount)
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
				t.Errorf("import hashes differ across re-imports — DB dedup will fail to catch the re-upload\n  pass1: %v\n  pass2: %v", h1, h2)
			}
			// Hashes within a single parse must be unique — otherwise the
			// DB unique constraint blocks rows on first import.
			seen := make(map[string]int)
			for i, h := range h1 {
				if seen[h] > 0 {
					t.Errorf("duplicate hash within single parse (txn %d): %s collides with %d", i, h, seen[h]-1)
				}
				seen[h] = i + 1
			}
		})
	}
}

// File-fixture parsers: ING order manager, ING Depotübersicht, Delta.
// Real CSVs live in testdata/ and are large enough that quoting them inline
// would be noisy — load + re-parse to verify idempotence.
func TestParser_ReimportIsIdempotent_FromFixtures(t *testing.T) {
	fixtures := []string{
		"ing_ordermanager.csv",
		"ing_depotuebersicht.csv",
		"delta.csv",
		"revolut_savings.csv",
	}
	for _, name := range fixtures {
		t.Run(name, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("testdata", name))
			if err != nil {
				t.Skipf("fixture missing: %v", err)
				return
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
				t.Errorf("re-parse count diverged: %d → %d", len(pass1), len(pass2))
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
				t.Errorf("%s: import hashes differ across re-imports", name)
			}
		})
	}
}

// Account ID is part of the hash for parsers that include it (so the same
// CSV imported to two different accounts produces different hashes,
// preventing cross-account collisions).
func TestParser_DifferentAccountsProduceDifferentHashes(t *testing.T) {
	csv := `Auftragskonto;Buchungstag;Valutadatum;Buchungstext;Verwendungszweck;Begünstigter/Zahlungspflichtiger;Kontonummer;BLZ;Betrag;Währung
DE123456789;01.03.2026;01.03.2026;GEHALT;Gehalt;Arbeitgeber;987654321;10050000;2.500,00;EUR`

	acctA := uuid.New()
	acctB := uuid.New()
	passA, _, _, err := ParseCSV([]byte(csv), acctA)
	if err != nil {
		t.Fatalf("parse A: %v", err)
	}
	passB, _, _, err := ParseCSV([]byte(csv), acctB)
	if err != nil {
		t.Fatalf("parse B: %v", err)
	}
	if len(passA) != 1 || len(passB) != 1 {
		t.Fatalf("expected 1 txn per account, got %d/%d", len(passA), len(passB))
	}
	if passA[0].ImportHash == passB[0].ImportHash {
		t.Errorf("same CSV imported into two accounts must produce different hashes (account_id is part of the hash), got %s == %s", passA[0].ImportHash, passB[0].ImportHash)
	}
}
