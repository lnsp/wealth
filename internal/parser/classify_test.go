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

func TestClassifyScalableType(t *testing.T) {
	tests := []struct {
		name    string
		txnType string
		subType string
		side    string
		want    string
	}{
		{name: "buy single", txnType: "security_transaction", subType: "single", side: "buy", want: "buy"},
		{name: "sell single", txnType: "security_transaction", subType: "single", side: "sell", want: "sell"},
		{name: "savings plan", txnType: "security_transaction", subType: "savings_plan", side: "buy", want: "savings_plan"},
		{name: "deposit", txnType: "cash_transaction", subType: "deposit", side: "", want: "deposit"},
		{name: "withdrawal", txnType: "cash_transaction", subType: "withdrawal", side: "", want: "withdrawal"},
		{name: "distribution", txnType: "cash_transaction", subType: "distribution", side: "", want: "dividend"},
		{name: "interest", txnType: "cash_transaction", subType: "interest", side: "", want: "interest"},
		{name: "tax", txnType: "cash_transaction", subType: "tax", side: "", want: "fee"},
		{name: "cash transfer out", txnType: "cash_transaction", subType: "cash_transfer_out", side: "", want: "cash_transfer_out"},
		{name: "cash transfer in", txnType: "cash_transaction", subType: "cash_transfer_in", side: "", want: "cash_transfer_in"},
		{name: "transfer in", txnType: "non_trade_security_transaction", subType: "transfer_in", side: "", want: "transfer"},
		{name: "transfer out", txnType: "non_trade_security_transaction", subType: "transfer_out", side: "", want: "transfer_out"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifyScalableType(tt.txnType, tt.subType, tt.side)
			if got != tt.want {
				t.Errorf("classifyScalableType(%q, %q, %q) = %q, want %q", tt.txnType, tt.subType, tt.side, got, tt.want)
			}
		})
	}
}

func TestReverseTxnType(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"buy", "sell"},
		{"savings_plan", "sell"},
		{"sell", "buy"},
		{"deposit", "withdrawal"},
		{"withdrawal", "deposit"},
		{"dividend", "fee"},
		{"fee", "interest"},
		{"transfer", "transfer_out"},
		{"transfer_out", "transfer"},
		{"cash_transfer_in", "cash_transfer_out"},
		{"cash_transfer_out", "cash_transfer_in"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := reverseTxnType(tt.input)
			if got != tt.want {
				t.Errorf("reverseTxnType(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestClassifyAsset(t *testing.T) {
	tests := []struct {
		name      string
		isin      string
		secName   string
		wantClass string
	}{
		// ETFs
		{name: "vanguard etf", isin: "IE00BK5BQT80", secName: "Vanguard FTSE All-World (Acc)", wantClass: "etf"},
		{name: "ishares etf", isin: "IE00B5BMR087", secName: "iShares Core S&P 500 (Acc)", wantClass: "etf"},
		{name: "xtrackers etf", isin: "IE00BTJRMP35", secName: "Xtrackers MSCI Emerging Markets (Acc)", wantClass: "etf"},
		{name: "amundi etf", isin: "LU0908500753", secName: "Amundi Core Stoxx Europe 600 (Acc)", wantClass: "etf"},
		{name: "ucits keyword", isin: "IE00B4L5Y983", secName: "iShares Core MSCI World UCITS ETF", wantClass: "etf"},

		// Stocks
		{name: "apple stock", isin: "US0378331005", secName: "Apple", wantClass: "stock"},
		{name: "rheinmetall stock", isin: "DE0007030009", secName: "Rheinmetall", wantClass: "stock"},
		{name: "cloudflare stock", isin: "US18915M1071", secName: "Cloudflare A", wantClass: "stock"},

		// Derivatives
		{name: "call option", isin: "DE000HT47771", secName: "Rheinmetall Call 1.900,00 € HSBC", wantClass: "derivative"},
		{name: "factor certificate", isin: "DE000GP7W4P6", secName: "Rheinmetall Long 7x Faktor-Zertifikat GS", wantClass: "derivative"},
		{name: "short certificate", isin: "DE000UG169K1", secName: "Tesla Short -7x Faktor-Zertifikat HVB", wantClass: "derivative"},
		{name: "optionsschein", isin: "DE000GJ0TGT9", secName: "Intel Corp Call 45,00 $ Optionsschein GS", wantClass: "derivative"},
		{name: "leveraged etf", isin: "FR0010755611", secName: "Amundi MSCI USA Daily (2x) Leveraged (Acc)", wantClass: "derivative"},

		// Bonds
		{name: "govt bond", isin: "IE00B4WXJJ64", secName: "iShares Core Euro Govt Bond (Dist)", wantClass: "bond"},

		// Funds
		{name: "deka fund", isin: "DE0008474503", secName: "Deka-ImmobilienEuropa", wantClass: "fund"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ClassifyAsset(tt.isin, tt.secName)
			if got != tt.wantClass {
				t.Errorf("ClassifyAsset(%q, %q) = %q, want %q", tt.isin, tt.secName, got, tt.wantClass)
			}
		})
	}
}
