package parser

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestScalableCapitalFullCSV(t *testing.T) {
	csv := "date;status;type;sub_type;side;isin;description;quantity;amount;currency;is_cancellation\n" +
		"2026-01-15;SETTLED;SECURITY_TRANSACTION;SINGLE;BUY;IE00BK5BQT80;Vanguard FTSE All-World;10;-1200;EUR;false\n" +
		"2026-01-20;SETTLED;CASH_TRANSACTION;DEPOSIT;;;Sparplan;;5000;EUR;false\n" +
		"2026-01-25;SETTLED;CASH_TRANSACTION;DISTRIBUTION;;IE00B4WXJJ64;iShares Bond;;50.25;EUR;false\n" +
		"2026-02-01;SETTLED;CASH_TRANSACTION;TAX;;;Vorabpauschale;;-12.50;EUR;false\n" +
		"2026-02-05;SETTLED;CASH_TRANSACTION;INTEREST;;;Zinsen;;3.75;EUR;false\n" +
		"2026-02-10;SETTLED;CASH_TRANSACTION;WITHDRAWAL;;;Auszahlung;;-500;EUR;false\n" +
		"2026-02-15;SETTLED;CASH_TRANSACTION;CASH_TRANSFER_OUT;;;Umbuchung;;-1000;EUR;false\n" +
		"2026-02-16;SETTLED;CASH_TRANSACTION;CASH_TRANSFER_IN;;;Eingang Tagesgeld;;1000;EUR;false\n" +
		"2026-02-28;SETTLED;NON_TRADE_SECURITY_TRANSACTION;TRANSFER_OUT;;IE00B5BMR087;iShares S&P 500;50;-25000;EUR;false\n" +
		"2026-03-01;SETTLED;NON_TRADE_SECURITY_TRANSACTION;TRANSFER_IN;;IE00B5BMR087;iShares S&P 500;50;25000;EUR;false\n" +
		"2026-03-05;SETTLED;SECURITY_TRANSACTION;SAVINGS_PLAN;BUY;IE00BK5BQT80;Vanguard;5;-600;EUR;false\n" +
		"2026-03-10;SETTLED;SECURITY_TRANSACTION;SINGLE;SELL;IE00BK5BQT80;Vanguard;3;360;EUR;false\n" +
		"2026-03-15;CANCELLED;SECURITY_TRANSACTION;SINGLE;BUY;IE00BK5BQT80;Vanguard;10;0;EUR;false\n" +
		"2026-03-20;REJECTED;CASH_TRANSACTION;DEPOSIT;;;Failed deposit;;0;EUR;false\n" +
		// Cancellation pair: original buy + its cancellation
		"2026-03-25;SETTLED;SECURITY_TRANSACTION;SINGLE;BUY;IE00BK5BQT80;Vanguard;2;-250;EUR;false\n" +
		"2026-03-25;SETTLED;SECURITY_TRANSACTION;SINGLE;BUY;IE00BK5BQT80;Vanguard;2;250;EUR;true\n"

	accountID := uuid.New()
	txns, _, result, err := ParseCSV([]byte(csv), accountID)
	if err != nil {
		t.Fatalf("ParseCSV: %v", err)
	}

	if result.Institution != "scalable_capital" {
		t.Errorf("institution: want scalable_capital, got %s", result.Institution)
	}

	// 16 rows total, minus 2 filtered (CANCELLED + REJECTED) = 14 transactions
	if len(txns) != 14 {
		t.Fatalf("expected 14 transactions, got %d", len(txns))
	}

	// Verify each transaction type
	tests := []struct {
		idx      int
		wantType string
		wantISIN string
		wantQty  float64
		wantAmt  float64
	}{
		{0, "buy", "IE00BK5BQT80", 10, 1200},
		{1, "deposit", "", 0, 5000},
		{2, "dividend", "IE00B4WXJJ64", 0, 50.25},
		{3, "fee", "", 0, 12.50}, // TAX → fee
		{4, "interest", "", 0, 3.75},
		{5, "withdrawal", "", 0, 500},
		{6, "cash_transfer_out", "", 0, 1000},           // CASH_TRANSFER_OUT
		{7, "cash_transfer_in", "", 0, 1000},            // CASH_TRANSFER_IN
		{8, "transfer_out", "IE00B5BMR087", 50, 25000}, // NON_TRADE TRANSFER_OUT
		{9, "transfer", "IE00B5BMR087", 50, 25000},     // NON_TRADE TRANSFER_IN
		{10, "savings_plan", "IE00BK5BQT80", 5, 600},
		{11, "sell", "IE00BK5BQT80", 3, 360},
		{12, "buy", "IE00BK5BQT80", 2, 250},  // original buy
		{13, "sell", "IE00BK5BQT80", 2, 250}, // cancellation → reversed to sell
	}

	for _, tt := range tests {
		txn := txns[tt.idx]
		if txn.Type != tt.wantType {
			t.Errorf("txn[%d] type: want %s, got %s", tt.idx, tt.wantType, txn.Type)
		}
		if txn.SecurityISIN != tt.wantISIN {
			t.Errorf("txn[%d] isin: want %s, got %s", tt.idx, tt.wantISIN, txn.SecurityISIN)
		}
		if txn.Quantity != tt.wantQty {
			t.Errorf("txn[%d] qty: want %.3f, got %.3f", tt.idx, tt.wantQty, txn.Quantity)
		}
		if txn.Amount != tt.wantAmt {
			t.Errorf("txn[%d] amt: want %.2f, got %.2f", tt.idx, tt.wantAmt, txn.Amount)
		}
	}

	// Verify warnings for skipped rows (CANCELLED + REJECTED)
	if result.Errors == nil || len(result.Errors) == 0 {
		t.Error("expected parsing warnings for CANCELLED/REJECTED rows")
	} else {
		if len(result.Errors) != 2 {
			t.Errorf("expected 2 warnings, got %d: %v", len(result.Errors), result.Errors)
		}
		// Check that warnings mention the specific statuses
		foundCancelled := false
		foundRejected := false
		for _, w := range result.Errors {
			if strings.Contains(w, "CANCELLED") {
				foundCancelled = true
			}
			if strings.Contains(w, "REJECTED") {
				foundRejected = true
			}
		}
		if !foundCancelled {
			t.Error("expected warning about CANCELLED row")
		}
		if !foundRejected {
			t.Error("expected warning about REJECTED row")
		}
	}
}

func TestScalablePriceDerivation(t *testing.T) {
	csv := "date;status;type;sub_type;side;isin;description;quantity;amount;currency;is_cancellation\n" +
		"2026-01-01;SETTLED;SECURITY_TRANSACTION;SINGLE;BUY;IE00BK5BQT80;Vanguard;10;-1200;EUR;false\n"

	txns, _, _, err := ParseCSV([]byte(csv), uuid.New())
	if err != nil {
		t.Fatalf("ParseCSV: %v", err)
	}

	// Price should be derived: 1200 / 10 = 120
	if txns[0].Price != 120 {
		t.Errorf("derived price: want 120, got %.2f", txns[0].Price)
	}
}

func TestScalableDeduplication(t *testing.T) {
	csv := "date;status;type;sub_type;side;isin;description;quantity;amount;currency;is_cancellation\n" +
		"2026-01-01;SETTLED;SECURITY_TRANSACTION;SINGLE;BUY;IE00BK5BQT80;Vanguard;10;-1200;EUR;false\n" +
		// Same buy but cancellation (positive amount) should get different hash
		"2026-01-01;SETTLED;SECURITY_TRANSACTION;SINGLE;BUY;IE00BK5BQT80;Vanguard;10;1200;EUR;true\n"

	txns, _, _, err := ParseCSV([]byte(csv), uuid.New())
	if err != nil {
		t.Fatalf("ParseCSV: %v", err)
	}

	if len(txns) != 2 {
		t.Fatalf("expected 2 transactions, got %d", len(txns))
	}

	if txns[0].ImportHash == txns[1].ImportHash {
		t.Error("cancellation should have different hash than original")
	}
}

func TestScalableEmptyRows(t *testing.T) {
	csv := "date;status;type;sub_type;side;isin;description;quantity;amount;currency;is_cancellation\n" +
		"\n" +
		"2026-01-01;SETTLED;CASH_TRANSACTION;DEPOSIT;;;Deposit;;1000;EUR;false\n" +
		"\n"

	txns, _, _, err := ParseCSV([]byte(csv), uuid.New())
	if err != nil {
		t.Fatalf("ParseCSV: %v", err)
	}

	if len(txns) != 1 {
		t.Errorf("expected 1 transaction (empty rows skipped), got %d", len(txns))
	}
}

func TestScalablePrimePlusNativeFormat(t *testing.T) {
	// PRIME+ native export uses different columns: isin + shares (no sub_type/is_cancellation)
	csv := "date;isin;name;shares;side;amount;currency\n" +
		"2026-01-15;IE00BK5BQT80;Vanguard FTSE All-World;10;buy;-1200;EUR\n" +
		"2026-01-20;;;0;deposit;5000;EUR\n" +
		"2026-01-25;IE00B4WXJJ64;iShares Bond;0;dividend;50.25;EUR\n" +
		"2026-02-01;IE00BK5BQT80;Vanguard;3;sell;360;EUR\n"

	accountID := uuid.New()
	txns, _, result, err := ParseCSV([]byte(csv), accountID)
	if err != nil {
		t.Fatalf("ParseCSV: %v", err)
	}

	if result.Institution != "scalable_capital" {
		t.Errorf("institution: want scalable_capital, got %s", result.Institution)
	}

	if len(txns) != 4 {
		t.Fatalf("expected 4 transactions, got %d", len(txns))
	}

	// Check types classified from side column
	tests := []struct {
		idx      int
		wantType string
		wantISIN string
	}{
		{0, "buy", "IE00BK5BQT80"},
		{1, "deposit", ""},
		{2, "dividend", "IE00B4WXJJ64"},
		{3, "sell", "IE00BK5BQT80"},
	}

	for _, tt := range tests {
		txn := txns[tt.idx]
		if txn.Type != tt.wantType {
			t.Errorf("txn[%d] type: want %s, got %s", tt.idx, tt.wantType, txn.Type)
		}
		if txn.SecurityISIN != tt.wantISIN {
			t.Errorf("txn[%d] isin: want %s, got %s", tt.idx, tt.wantISIN, txn.SecurityISIN)
		}
	}
}

func TestScalablePrimePlusDetection(t *testing.T) {
	// PRIME+ native: has isin + shares but NOT sub_type
	p := &ScalableCapitalParser{}
	if !p.Detect([]string{"date", "isin", "name", "shares", "side", "amount", "currency"}) {
		t.Error("should detect PRIME+ native format (isin + shares)")
	}
	// German variant
	if !p.Detect([]string{"Datum", "ISIN", "Name", "Stück", "Seite", "Betrag", "Währung"}) {
		t.Error("should detect PRIME+ native format with German headers")
	}
	// Should NOT detect random CSV
	if p.Detect([]string{"date", "description", "amount"}) {
		t.Error("should not detect unrelated CSV")
	}
}
