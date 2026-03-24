package parser

import "testing"

func TestClassifySparkasseTransaction(t *testing.T) {
	tests := []struct {
		name         string
		amount       float64
		buchungstext string
		want         string
	}{
		// Interest
		{name: "interest by Zinsen", amount: 12.50, buchungstext: "Zinsen/Zinsgutschrift", want: "interest"},
		{name: "interest lowercase", amount: 5.00, buchungstext: "zinsen q1", want: "interest"},
		{name: "interest negative", amount: -1.00, buchungstext: "ZINSEN", want: "interest"},

		// Fees
		{name: "fee by Gebühr", amount: -3.50, buchungstext: "Kontogebühr", want: "fee"},
		{name: "fee by Gebuehr", amount: -3.50, buchungstext: "Kontogebuehr", want: "fee"},
		{name: "fee by Entgelt", amount: -2.00, buchungstext: "Kartenentgelt", want: "fee"},

		// Transfers
		{name: "transfer by Umbuchung", amount: -500.00, buchungstext: "Umbuchung", want: "transfer"},
		{name: "transfer by Übertrag", amount: 1000.00, buchungstext: "Übertrag Depot", want: "transfer"},
		{name: "transfer by Uebertrag", amount: -100.00, buchungstext: "Uebertrag", want: "transfer"},

		// Deposits (positive amount, no special keyword)
		{name: "deposit salary", amount: 2500.00, buchungstext: "GEHALT", want: "deposit"},
		{name: "deposit generic positive", amount: 100.00, buchungstext: "GUTSCHRIFT", want: "deposit"},

		// Withdrawals (negative amount, no special keyword)
		{name: "withdrawal rent", amount: -850.00, buchungstext: "LASTSCHRIFT", want: "withdrawal"},
		{name: "withdrawal generic", amount: -10.00, buchungstext: "KARTENZAHLUNG", want: "withdrawal"},
		{name: "withdrawal zero amount", amount: 0, buchungstext: "", want: "withdrawal"},

		// Priority: keyword takes precedence over amount sign
		{name: "fee positive amount", amount: 3.50, buchungstext: "Entgelt Erstattung", want: "fee"},
		{name: "interest negative", amount: -0.01, buchungstext: "Negativzinsen", want: "interest"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifySparkasseTransaction(tt.amount, tt.buchungstext)
			if got != tt.want {
				t.Errorf("classifySparkasseTransaction(%f, %q) = %q, want %q", tt.amount, tt.buchungstext, got, tt.want)
			}
		})
	}
}

func TestCategorizeByBuchungstext(t *testing.T) {
	tests := []struct {
		name         string
		buchungstext string
		want         string
	}{
		// Income
		{name: "salary by Gehalt", buchungstext: "GEHALT", want: "income"},
		{name: "salary by Lohn", buchungstext: "Lohnzahlung", want: "income"},
		{name: "gehalt lowercase", buchungstext: "gehalt maerz", want: "income"},

		// Rent
		{name: "rent by Miete", buchungstext: "Miete März", want: "rent"},
		{name: "rent lowercase", buchungstext: "miete wohnung", want: "rent"},

		// Insurance
		{name: "insurance", buchungstext: "Haftpflichtversicherung", want: "insurance"},
		{name: "insurance prefix", buchungstext: "Versicherung XYZ", want: "insurance"},

		// Utilities
		{name: "electricity", buchungstext: "Strom Abschlag", want: "utilities"},
		{name: "gas", buchungstext: "Gas Rechnung", want: "utilities"},
		{name: "water", buchungstext: "Wasser Abschlag", want: "utilities"},

		// No category
		{name: "generic lastschrift", buchungstext: "LASTSCHRIFT", want: ""},
		{name: "empty", buchungstext: "", want: ""},
		{name: "kartenzahlung", buchungstext: "KARTENZAHLUNG REWE", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := categorizeByBuchungstext(tt.buchungstext)
			if got != tt.want {
				t.Errorf("categorizeByBuchungstext(%q) = %q, want %q", tt.buchungstext, got, tt.want)
			}
		})
	}
}

func TestClassifyN26Transaction(t *testing.T) {
	tests := []struct {
		name    string
		amount  float64
		txnType string
		want    string
	}{
		{name: "interest english", amount: 5.00, txnType: "Interest", want: "interest"},
		{name: "interest german", amount: 5.00, txnType: "Zinsen", want: "interest"},
		{name: "interest in compound", amount: 5.00, txnType: "Savings Interest", want: "interest"},
		{name: "deposit positive", amount: 2500.00, txnType: "Income", want: "deposit"},
		{name: "deposit any positive", amount: 100.00, txnType: "CT", want: "deposit"},
		{name: "withdrawal negative", amount: -45.67, txnType: "MasterCard Payment", want: "withdrawal"},
		{name: "withdrawal zero", amount: 0, txnType: "Direct Debit", want: "withdrawal"},
		{name: "withdrawal empty type", amount: -10.00, txnType: "", want: "withdrawal"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifyN26Transaction(tt.amount, tt.txnType)
			if got != tt.want {
				t.Errorf("classifyN26Transaction(%f, %q) = %q, want %q", tt.amount, tt.txnType, got, tt.want)
			}
		})
	}
}

func TestClassifyScalableTransaction(t *testing.T) {
	tests := []struct {
		name    string
		txnType string
		want    string
	}{
		// Buy
		{name: "buy english", txnType: "buy", want: "buy"},
		{name: "buy german", txnType: "kauf", want: "buy"},
		{name: "buy compound", txnType: "market buy", want: "buy"},

		// Sell
		{name: "sell english", txnType: "sell", want: "sell"},
		{name: "sell german", txnType: "verkauf", want: "sell"},

		// Dividend
		{name: "dividend english", txnType: "dividend", want: "dividend"},
		{name: "dividend german", txnType: "dividende", want: "dividend"},
		{name: "distribution german", txnType: "ausschüttung", want: "dividend"},

		// Savings plan
		{name: "savings english", txnType: "savings", want: "savings_plan"},
		{name: "savings german", txnType: "sparplan", want: "savings_plan"},
		{name: "savings plan compound", txnType: "savings plan", want: "savings_plan"},

		// Fee
		{name: "fee english", txnType: "fee", want: "fee"},
		{name: "fee german umlaut", txnType: "gebühr", want: "fee"},
		{name: "fee german ascii", txnType: "gebuehr", want: "fee"},

		// Default
		{name: "unknown defaults to buy", txnType: "unknown", want: "buy"},
		{name: "empty defaults to buy", txnType: "", want: "buy"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifyScalableTransaction(tt.txnType)
			if got != tt.want {
				t.Errorf("classifyScalableTransaction(%q) = %q, want %q", tt.txnType, got, tt.want)
			}
		})
	}
}

func TestAbs(t *testing.T) {
	tests := []struct {
		name  string
		input float64
		want  float64
	}{
		{name: "positive", input: 5.5, want: 5.5},
		{name: "negative", input: -3.14, want: 3.14},
		{name: "zero", input: 0, want: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := abs(tt.input)
			if got != tt.want {
				t.Errorf("abs(%f) = %f, want %f", tt.input, got, tt.want)
			}
		})
	}
}
