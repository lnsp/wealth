package parser

import (
	"testing"

	"github.com/google/uuid"
)

func TestParseSparkasseCSV(t *testing.T) {
	csv := `Auftragskonto;Buchungstag;Valutadatum;Buchungstext;Verwendungszweck;Begünstigter/Zahlungspflichtiger;Kontonummer;BLZ;Betrag;Währung
DE123456789;01.03.2026;01.03.2026;GEHALT;Gehalt März 2026;Arbeitgeber GmbH;987654321;10050000;2.500,00;EUR
DE123456789;02.03.2026;02.03.2026;LASTSCHRIFT;Miete März;Vermieter;111222333;10050000;-850,00;EUR
DE123456789;03.03.2026;03.03.2026;ZINSEN;Zinsgutschrift Q1;Sparkasse;000000000;10050000;12,50;EUR`

	accountID := uuid.New()
	txns, result, err := ParseCSV([]byte(csv), accountID)
	if err != nil {
		t.Fatalf("ParseCSV: %v", err)
	}

	if result.Institution != "sparkasse" {
		t.Errorf("expected institution sparkasse, got %s", result.Institution)
	}

	if len(txns) != 3 {
		t.Fatalf("expected 3 transactions, got %d", len(txns))
	}

	// Check deposit (salary)
	if txns[0].Type != "deposit" {
		t.Errorf("txn[0] type: expected deposit, got %s", txns[0].Type)
	}
	if txns[0].Amount != 2500.00 {
		t.Errorf("txn[0] amount: expected 2500.00, got %f", txns[0].Amount)
	}
	if txns[0].Category != "income" {
		t.Errorf("txn[0] category: expected income, got %s", txns[0].Category)
	}

	// Check withdrawal (rent)
	if txns[1].Type != "withdrawal" {
		t.Errorf("txn[1] type: expected withdrawal, got %s", txns[1].Type)
	}
	if txns[1].Amount != 850.00 {
		t.Errorf("txn[1] amount: expected 850.00, got %f", txns[1].Amount)
	}

	// Check interest
	if txns[2].Type != "interest" {
		t.Errorf("txn[2] type: expected interest, got %s", txns[2].Type)
	}
	if txns[2].Amount != 12.50 {
		t.Errorf("txn[2] amount: expected 12.50, got %f", txns[2].Amount)
	}

	// Check dedup hashes are unique
	hashes := make(map[string]bool)
	for _, txn := range txns {
		if txn.ImportHash == "" {
			t.Error("empty import hash")
		}
		if hashes[txn.ImportHash] {
			t.Error("duplicate import hash")
		}
		hashes[txn.ImportHash] = true
	}
}

func TestParseN26CSV(t *testing.T) {
	csv := `"Date","Payee","Account number","Transaction type","Payment reference","Amount (EUR)","Amount (Foreign Currency)","Type Foreign Currency","Exchange Rate"
"2026-03-01","Employer GmbH","DE123","Income","Salary March","2500.00","","",""
"2026-03-05","Rewe","","MasterCard Payment","Groceries","-45.67","","",""`

	accountID := uuid.New()
	txns, result, err := ParseCSV([]byte(csv), accountID)
	if err != nil {
		t.Fatalf("ParseCSV: %v", err)
	}

	if result.Institution != "n26" {
		t.Errorf("expected institution n26, got %s", result.Institution)
	}

	if len(txns) != 2 {
		t.Fatalf("expected 2 transactions, got %d", len(txns))
	}

	if txns[0].Type != "deposit" {
		t.Errorf("txn[0] type: expected deposit, got %s", txns[0].Type)
	}
	if txns[0].Amount != 2500.00 {
		t.Errorf("txn[0] amount: expected 2500.00, got %f", txns[0].Amount)
	}

	if txns[1].Type != "withdrawal" {
		t.Errorf("txn[1] type: expected withdrawal, got %s", txns[1].Type)
	}
	if txns[1].Amount != 45.67 {
		t.Errorf("txn[1] amount: expected 45.67, got %f", txns[1].Amount)
	}
}

func TestParseScalableCapitalCSV(t *testing.T) {
	csv := `date;time;status;type;isin;name;shares;price;amount;fee;tax;currency
2026-03-01;10:30:00;EXECUTED;buy;IE00B3RBWM25;Vanguard FTSE All-World;10.5;119.50;1254.75;0;0;EUR
2026-03-15;09:00:00;EXECUTED;savings_plan;IE00B4L5Y983;iShares MSCI World;5.0;82.30;411.50;1.50;0;EUR
2026-03-10;00:00:00;CANCELLED;buy;DE0005933931;iShares Core DAX;20;150.00;3000.00;0;0;EUR`

	accountID := uuid.New()
	txns, result, err := ParseCSV([]byte(csv), accountID)
	if err != nil {
		t.Fatalf("ParseCSV: %v", err)
	}

	if result.Institution != "scalable_capital" {
		t.Errorf("expected institution scalable_capital, got %s", result.Institution)
	}

	// Should only have 2 (CANCELLED row filtered)
	if len(txns) != 2 {
		t.Fatalf("expected 2 transactions (cancelled filtered), got %d", len(txns))
	}

	if txns[0].SecurityISIN != "IE00B3RBWM25" {
		t.Errorf("txn[0] ISIN: expected IE00B3RBWM25, got %s", txns[0].SecurityISIN)
	}
	if txns[0].Quantity != 10.5 {
		t.Errorf("txn[0] quantity: expected 10.5, got %f", txns[0].Quantity)
	}
	if txns[1].Fee != 1.50 {
		t.Errorf("txn[1] fee: expected 1.50, got %f", txns[1].Fee)
	}
	if txns[1].Type != "savings_plan" {
		t.Errorf("txn[1] type: expected savings_plan, got %s", txns[1].Type)
	}
}

func TestParseHoldingsTemplate(t *testing.T) {
	csv := `isin,name,quantity,market_value,currency,date
IE00B3RBWM25,Vanguard FTSE All-World,150.000,17850.00,EUR,2026-03-24
DE0005933931,iShares Core DAX,50.000,7250.00,EUR,2026-03-24`

	accountID := uuid.New()
	txns, result, err := ParseCSV([]byte(csv), accountID)
	if err != nil {
		t.Fatalf("ParseCSV: %v", err)
	}

	if result.Institution != "holdings_template" {
		t.Errorf("expected institution holdings_template, got %s", result.Institution)
	}

	if len(txns) != 2 {
		t.Fatalf("expected 2 transactions, got %d", len(txns))
	}

	if txns[0].Type != "buy" {
		t.Errorf("expected buy type, got %s", txns[0].Type)
	}
	if txns[0].Quantity != 150.0 {
		t.Errorf("expected 150 quantity, got %f", txns[0].Quantity)
	}
	// Price should be 17850/150 = 119
	if txns[0].Price != 119.0 {
		t.Errorf("expected price 119.0, got %f", txns[0].Price)
	}
}

func TestParseGermanDecimal(t *testing.T) {
	tests := []struct {
		input string
		want  float64
	}{
		{"1.234,56", 1234.56},
		{"-850,00", -850.00},
		{"12,50", 12.50},
		{"2.500,00", 2500.00},
		{"0,01", 0.01},
		{"", 0},
	}

	for _, tt := range tests {
		got, err := parseGermanDecimal(tt.input)
		if err != nil {
			t.Errorf("parseGermanDecimal(%q): %v", tt.input, err)
			continue
		}
		if got != tt.want {
			t.Errorf("parseGermanDecimal(%q) = %f, want %f", tt.input, got, tt.want)
		}
	}
}

func TestDetectDelimiter(t *testing.T) {
	if d := detectDelimiter("a;b;c;d\n1;2;3;4"); d != ';' {
		t.Errorf("expected semicolon, got %c", d)
	}
	if d := detectDelimiter("a,b,c,d\n1,2,3,4"); d != ',' {
		t.Errorf("expected comma, got %c", d)
	}
}

func TestUnrecognizedCSV(t *testing.T) {
	csv := `foo,bar,baz
1,2,3`

	_, _, err := ParseCSV([]byte(csv), uuid.New())
	if err == nil {
		t.Error("expected error for unrecognized CSV format")
	}
}
