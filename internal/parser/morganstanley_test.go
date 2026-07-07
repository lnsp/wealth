package parser

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestMorganStanleyReleasesReport(t *testing.T) {
	csv := `Vest Date,Order Number,Plan,Type,Status,Price,Quantity,Net Cash Proceeds,Net Share Proceeds,Tax Payment Method
25-Apr-2026,RB9FE31803,GSU Class C,Release,Complete,$342.32,7.056,$0.00,3.663,Fractional Shares
25-Apr-2026,RB9FE31850,GSU Class C,Release,Complete,$342.32,6.028,$0.00,3.130,Fractional Shares
25-Dec-2022,RB754390A4,GSU Class C,Release,Complete,$89.81,38.000,$0.00,19.726,Fractional Shares`

	txns, vests, result, err := ParseCSV([]byte(csv), uuid.New())
	if err != nil {
		t.Fatalf("ParseCSV: %v", err)
	}
	if result.Institution != "morgan_stanley" {
		t.Errorf("expected institution morgan_stanley, got %s", result.Institution)
	}
	if len(txns) != 3 {
		t.Fatalf("expected 3 transactions, got %d", len(txns))
	}
	if len(vests) != 3 {
		t.Fatalf("expected 3 rsu_vests, got %d", len(vests))
	}

	// Vests are in-kind transfers (no cash leg); quantity is net shares.
	for _, txn := range txns {
		if txn.Type != "transfer" {
			t.Errorf("expected transfer type, got %s", txn.Type)
		}
		if txn.SecurityISIN != "" {
			t.Errorf("expected blank ISIN (resolved at handler), got %q", txn.SecurityISIN)
		}
		if txn.Currency != "USD" {
			t.Errorf("expected USD currency, got %q", txn.Currency)
		}
	}

	// Find the 25-Dec-2022 row: gross 38.000, net 19.726.
	var dec2022 *RSUVest
	for i, v := range vests {
		if v.VestDate.Format("2006-01-02") == "2022-12-25" {
			dec2022 = &vests[i]
		}
	}
	if dec2022 == nil {
		t.Fatal("missing 25-Dec-2022 vest row")
	}
	if dec2022.GrossQuantity != 38.0 {
		t.Errorf("gross_quantity = %v, want 38.0", dec2022.GrossQuantity)
	}
	if dec2022.NetQuantity != 19.726 {
		t.Errorf("net_quantity = %v, want 19.726", dec2022.NetQuantity)
	}
	if !dec2022.Vested {
		t.Error("vested flag = false, want true")
	}
	if dec2022.GrantNumber != "RB754390A4" {
		t.Errorf("grant_number = %q, want RB754390A4", dec2022.GrantNumber)
	}
}

func TestMorganStanleyReleasesAndNetSharesProduceMatchingHashes(t *testing.T) {
	// Both files describe the same vests. The Releases Report (with gross) and the
	// Net Shares Report (net only) must produce identical buy-transaction hashes
	// so re-importing the second file is a no-op.
	releases := `Vest Date,Order Number,Plan,Type,Status,Price,Quantity,Net Cash Proceeds,Net Share Proceeds,Tax Payment Method
25-Apr-2026,RB1,GSU Class C,Release,Complete,$342.32,7.056,$0.00,3.663,Fractional Shares
25-Apr-2026,RB2,GSU Class C,Release,Complete,$342.32,6.028,$0.00,3.130,Fractional Shares
25-Nov-2025,RB3,GSU Class C,Release,Complete,$318.47,7.047,$0.00,3.659,Fractional Shares
25-Nov-2025,RB4,GSU Class C,Release,Complete,$318.47,7.047,$0.00,3.659,Fractional Shares`

	// Same vests, listed in a different file order (Net Shares Report is reverse
	// chronological in the real export) — sorting in the parser should reconcile.
	netShares := `Date,Order Number,Plan,Type,Order Status,Price,Quantity,Net Share Proceeds,Net Share Proceeds,Tax Payment Method
25-Nov-2025,N/A,GSU Class C,Released Shares,Completed,$318.47,3.659,$0.00,0.000,N/A
25-Nov-2025,N/A,GSU Class C,Released Shares,Completed,$318.47,3.659,$0.00,0.000,N/A
25-Apr-2026,N/A,GSU Class C,Released Shares,Completed,$342.32,3.663,$0.00,0.000,N/A
25-Apr-2026,N/A,GSU Class C,Released Shares,Completed,$342.32,3.130,$0.00,0.000,N/A`

	accountID := uuid.New()
	relTxns, _, _, err := ParseCSV([]byte(releases), accountID)
	if err != nil {
		t.Fatalf("releases ParseCSV: %v", err)
	}
	nsTxns, _, _, err := ParseCSV([]byte(netShares), accountID)
	if err != nil {
		t.Fatalf("net shares ParseCSV: %v", err)
	}

	relHashes := map[string]bool{}
	for _, txn := range relTxns {
		relHashes[txn.ImportHash] = true
	}
	for _, txn := range nsTxns {
		if !relHashes[txn.ImportHash] {
			t.Errorf("net-shares txn hash %q not found in releases hashes; sort/key drift?", txn.ImportHash)
		}
	}
	if len(relTxns) != len(nsTxns) {
		t.Errorf("transaction count mismatch: releases=%d net-shares=%d", len(relTxns), len(nsTxns))
	}
}

func TestMorganStanleyWithdrawalsReport(t *testing.T) {
	csv := `Execution Date,Order Number,Plan,Type,Order Status,Price,Quantity,Net Amount,Net Share Proceeds,Tax Payment Method
08-Mar-2023,WRC77055FC9-1EE,GSU Class C,Sale,Complete,$96.01,-39.972,"$3,830.18",0,N/A
30-Jul-2024,WBC87297C67-1EE,Cash,Sale,Complete,$1.00,-2.290,$2.29,0,N/A
31-Dec-2025,WBC9AD856A2-1EE,Cash,Sale,Complete,$1.00,"-31,945.690","$31,945.69",0,N/A
Please note that any Alphabet share sales, transfers, or deposits...`

	txns, vests, _, err := ParseCSV([]byte(csv), uuid.New())
	if err != nil {
		t.Fatalf("ParseCSV: %v", err)
	}
	if len(vests) != 0 {
		t.Errorf("expected 0 rsu_vests, got %d", len(vests))
	}
	// 1 GSU sale -> sell + auto-wire withdrawal; 2 explicit cash rows are skipped.
	if len(txns) != 2 {
		t.Fatalf("expected 2 transactions (1 sell + 1 auto-wire withdrawal), got %d", len(txns))
	}

	var sells, withdrawals int
	var sellAmount, withdrawalAmount float64
	for _, txn := range txns {
		switch txn.Type {
		case "sell":
			sells++
			sellAmount = txn.Amount
			if txn.Quantity != 39.972 {
				t.Errorf("sell quantity = %v, want 39.972", txn.Quantity)
			}
			if txn.Amount < 3830.17 || txn.Amount > 3830.19 {
				t.Errorf("sell amount = %v, want ~3830.18", txn.Amount)
			}
		case "withdrawal":
			withdrawals++
			withdrawalAmount = txn.Amount
		}
	}
	if sells != 1 || withdrawals != 1 {
		t.Errorf("type breakdown: sell=%d withdrawal=%d, want 1/1", sells, withdrawals)
	}
	if sellAmount != withdrawalAmount {
		t.Errorf("sell (%v) and auto-wire withdrawal (%v) must cancel out", sellAmount, withdrawalAmount)
	}
}

// TestMorganStanleyWithdrawalsPlanDriftDedup locks in that re-importing the
// Withdrawals Report after a cosmetic export change (MS exports the plan as
// "GSU Class C" sometimes and "(GSU Class C)" other times) produces identical
// ImportHashes per row — so the second import is a no-op rather than producing
// duplicate auto-wire withdrawals.
func TestMorganStanleyWithdrawalsPlanDriftDedup(t *testing.T) {
	bare := `Execution Date,Order Number,Plan,Type,Order Status,Price,Quantity,Net Amount,Net Share Proceeds,Tax Payment Method
08-Mar-2023,WRC77055FC9-1EE,GSU Class C,Sale,Complete,$96.01,-39.972,"$3,830.18",0,N/A
29-Apr-2024,WRC83C590B4-1EE,GSU Class C,Sale,Complete,$168.28,-67.486,"$11,348.99",0,N/A`

	parens := `Execution Date,Order Number,Plan,Type,Order Status,Price,Quantity,Net Amount,Net Share Proceeds,Tax Payment Method
08-Mar-2023,WRC77055FC9-1EE,(GSU Class C),Sale,Complete,$96.01,-39.972,"$3,830.18",0,N/A
29-Apr-2024,WRC83C590B4-1EE,(GSU Class C),Sale,Complete,$168.28,-67.486,"$11,348.99",0,N/A`

	accountID := uuid.New()
	bareTxns, _, _, err := ParseCSV([]byte(bare), accountID)
	if err != nil {
		t.Fatalf("bare ParseCSV: %v", err)
	}
	parensTxns, _, _, err := ParseCSV([]byte(parens), accountID)
	if err != nil {
		t.Fatalf("parens ParseCSV: %v", err)
	}

	if len(bareTxns) != len(parensTxns) {
		t.Fatalf("txn count mismatch: bare=%d parens=%d", len(bareTxns), len(parensTxns))
	}
	bareHashes := map[string]bool{}
	for _, txn := range bareTxns {
		bareHashes[txn.ImportHash] = true
	}
	for _, txn := range parensTxns {
		if !bareHashes[txn.ImportHash] {
			t.Errorf("parens-variant hash %q (%s) not present in bare hashes — dedup would fail on re-import", txn.ImportHash, txn.Type)
		}
	}
	for _, txn := range parensTxns {
		if strings.Contains(txn.Counterparty, "((") || strings.Contains(txn.Counterparty, "))") {
			t.Errorf("counterparty %q contains nested parens; normalizePlan didn't strip them", txn.Counterparty)
		}
	}
}

func TestMorganStanleyUnvestedSchedule(t *testing.T) {
	csv := `"Alphabet, Inc. - Restricted Shares Vesting (report run on 17-May-2026)"
Plan Name,Employee Grant Number,Vesting Date,Total Quantity,Quantity from Original Grant,DEUs,Grant Date
2021 Stock Plan,C1022750,25-May-2026,7.056000,7,0.056000,02-Nov-2022
2021 Stock Plan,C1224939,25-May-2026,7.056000,7,0.056000,06-Mar-2024
2021 Stock Plan,C1535939,25-May-2026,6.028000,6,0.028000,05-Mar-2025
The numbers on this statement reflect post-split values from stock split on 7/15/2022`

	txns, vests, result, err := ParseCSV([]byte(csv), uuid.New())
	if err != nil {
		t.Fatalf("ParseCSV: %v", err)
	}
	if result.Institution != "morgan_stanley" {
		t.Errorf("expected morgan_stanley, got %s", result.Institution)
	}
	if len(txns) != 0 {
		t.Errorf("expected 0 transactions for unvested, got %d", len(txns))
	}
	if len(vests) != 3 {
		t.Fatalf("expected 3 unvested vests, got %d", len(vests))
	}
	for _, v := range vests {
		if v.Vested {
			t.Errorf("vested=true on unvested row")
		}
		if v.NetQuantity != 0 {
			t.Errorf("net_quantity should be 0 for unvested, got %v", v.NetQuantity)
		}
		if v.ImportHash == "" {
			t.Error("missing import_hash on unvested row")
		}
	}
	// Verify the C1022750 row's gross.
	for _, v := range vests {
		if v.GrantNumber == "C1022750" {
			if v.GrossQuantity != 7.056 {
				t.Errorf("gross_quantity = %v, want 7.056", v.GrossQuantity)
			}
		}
	}
}

func TestMorganStanleyDetectsAllFour(t *testing.T) {
	p := &MorganStanleyParser{}
	cases := []struct {
		name   string
		header []string
		want   bool
	}{
		{"releases", strings.Split("Vest Date,Order Number,Plan,Type,Status,Price,Quantity,Net Cash Proceeds,Net Share Proceeds,Tax Payment Method", ","), true},
		{"net shares", strings.Split("Date,Order Number,Plan,Type,Order Status,Price,Quantity,Net Share Proceeds,Net Share Proceeds,Tax Payment Method", ","), true},
		{"withdrawals", strings.Split("Execution Date,Order Number,Plan,Type,Order Status,Price,Quantity,Net Amount,Net Share Proceeds,Tax Payment Method", ","), true},
		{"unvested", strings.Split("Plan Name,Employee Grant Number,Vesting Date,Total Quantity,Quantity from Original Grant,DEUs,Grant Date", ","), true},
		{"sparkasse-like", strings.Split("Auftragskonto;Buchungstag;Betrag", ";"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := p.Detect(tc.header); got != tc.want {
				t.Errorf("Detect(%v) = %v, want %v", tc.header, got, tc.want)
			}
		})
	}
}

func TestMorganStanleyParseMSDate(t *testing.T) {
	d, err := parseMSDate("25-Apr-2026")
	if err != nil {
		t.Fatalf("parseMSDate: %v", err)
	}
	if d.Format("2006-01-02") != "2026-04-25" {
		t.Errorf("got %s, want 2026-04-25", d.Format("2006-01-02"))
	}
}

func TestMorganStanleyMoneyParsing(t *testing.T) {
	cases := []struct {
		in   string
		want float64
	}{
		{"$96.01", 96.01},
		{"$3,830.18", 3830.18},
		{"-39.972", -39.972},
		{"-31,945.690", -31945.69},
		{"(1,234.56)", -1234.56}, // accountant-style negative
		{"$0.00", 0},
		{"  $1,000.00  ", 1000.0}, // surrounding whitespace
		{"0", 0},
		{"", 0},
	}
	for _, c := range cases {
		got := parseMoneyOrFloat(c.in)
		if got != c.want {
			t.Errorf("parseMoneyOrFloat(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestMorganStanleyVestLinksToTransactionHash(t *testing.T) {
	// The Releases Report produces both a buy transaction and an rsu_vest row
	// for the same vest. The vest's LinkTransactionHash must end up as the
	// import_hash of its sibling buy transaction so the handler can resolve the
	// transaction id when persisting.
	csv := `Vest Date,Order Number,Plan,Type,Status,Price,Quantity,Net Cash Proceeds,Net Share Proceeds,Tax Payment Method
25-Dec-2022,RB754390A4,GSU Class C,Release,Complete,$89.81,38.000,$0.00,19.726,Fractional Shares`

	txns, vests, _, err := ParseCSV([]byte(csv), uuid.New())
	if err != nil {
		t.Fatalf("ParseCSV: %v", err)
	}
	if len(txns) != 1 || len(vests) != 1 {
		t.Fatalf("expected 1 txn + 1 vest, got %d txns / %d vests", len(txns), len(vests))
	}
	if vests[0].LinkTransactionHash == "" {
		t.Fatal("vest LinkTransactionHash is empty after ParseCSV")
	}
	if vests[0].LinkTransactionHash != txns[0].ImportHash {
		t.Errorf("vest LinkTransactionHash %q != txn ImportHash %q",
			vests[0].LinkTransactionHash, txns[0].ImportHash)
	}
}

func TestMorganStanleyStatusFilter(t *testing.T) {
	// Pending releases must be excluded so we don't import shares the user
	// hasn't actually received yet.
	csv := `Vest Date,Order Number,Plan,Type,Status,Price,Quantity,Net Cash Proceeds,Net Share Proceeds,Tax Payment Method
25-Dec-2022,RB001,GSU Class C,Release,Complete,$89.81,38.000,$0.00,19.726,Fractional Shares
25-Dec-2022,RB002,GSU Class C,Release,Pending,$89.81,38.000,$0.00,19.726,Fractional Shares`

	txns, vests, _, err := ParseCSV([]byte(csv), uuid.New())
	if err != nil {
		t.Fatalf("ParseCSV: %v", err)
	}
	if len(txns) != 1 {
		t.Errorf("expected 1 completed txn (pending filtered), got %d", len(txns))
	}
	if len(vests) != 1 {
		t.Errorf("expected 1 completed vest (pending filtered), got %d", len(vests))
	}
}

func TestIsBlankOrFooter(t *testing.T) {
	cases := []struct {
		name string
		row  []string
		want bool
	}{
		{"empty row", []string{}, true},
		{"all blanks", []string{"", "  ", ""}, true},
		{"normal data row", []string{"25-Dec-2025", "RB1", "GSU Class C"}, false},
		{
			"footer that csv split on commas (Please note...)",
			[]string{"Please note that any Alphabet share sales", " transfers", " or deposits..."},
			true,
		},
		{
			"footer 'The numbers on this statement...'",
			[]string{"The numbers on this statement reflect post-split values from stock split on 7/15/2022"},
			true,
		},
		{"single field that isn't a footer", []string{"random"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isBlankOrFooter(tc.row); got != tc.want {
				t.Errorf("isBlankOrFooter(%v) = %v, want %v", tc.row, got, tc.want)
			}
		})
	}
}

func TestMorganStanleyUnvestedHashIncludesAccountID(t *testing.T) {
	// Two accounts importing the same unvested file must produce distinct
	// import_hash values so a single rsu_vests row doesn't block both.
	csv := `Plan Name,Employee Grant Number,Vesting Date,Total Quantity,Quantity from Original Grant,DEUs,Grant Date
2021 Stock Plan,C1022750,25-May-2026,7.056000,7,0.056000,02-Nov-2022`

	_, vestsA, _, err := ParseCSV([]byte(csv), uuid.New())
	if err != nil {
		t.Fatalf("account A: %v", err)
	}
	_, vestsB, _, err := ParseCSV([]byte(csv), uuid.New())
	if err != nil {
		t.Fatalf("account B: %v", err)
	}
	if len(vestsA) != 1 || len(vestsB) != 1 {
		t.Fatalf("expected 1 vest each, got %d/%d", len(vestsA), len(vestsB))
	}
	if vestsA[0].ImportHash == vestsB[0].ImportHash {
		t.Errorf("unvested import_hash collides across accounts: %s", vestsA[0].ImportHash)
	}
}
