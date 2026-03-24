package parser

import (
	"crypto/sha256"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestTryDecode(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		want string
	}{
		{
			name: "valid UTF-8",
			data: []byte("Hello, World!"),
			want: "Hello, World!",
		},
		{
			name: "UTF-8 with BOM",
			data: append([]byte{0xEF, 0xBB, 0xBF}, []byte("Hello")...),
			want: "Hello",
		},
		{
			name: "UTF-8 with German umlauts",
			data: []byte("Bücher über Ärzte"),
			want: "Bücher über Ärzte",
		},
		{
			name: "Latin-1 encoded umlaut",
			data: []byte{0xFC}, // ü in Latin-1
			want: "ü",
		},
		{
			name: "Latin-1 with German text",
			data: []byte{0x42, 0xFC, 0x63, 0x68, 0x65, 0x72}, // Bücher in Latin-1
			want: "Bücher",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tryDecode(tt.data)
			if got != tt.want {
				t.Errorf("tryDecode() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDetectDelimiterTableDriven(t *testing.T) {
	tests := []struct {
		name string
		text string
		want rune
	}{
		{name: "semicolons dominate", text: "a;b;c;d\n1;2;3;4", want: ';'},
		{name: "commas dominate", text: "a,b,c,d\n1,2,3,4", want: ','},
		{name: "equal count prefers comma", text: "a;b,c\n1;2,3", want: ','},
		{name: "no delimiters defaults to comma", text: "abcdef\n123456", want: ','},
		{name: "only first line matters", text: "a;b;c\na,b,c,d,e,f,g", want: ';'},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := detectDelimiter(tt.text)
			if got != tt.want {
				t.Errorf("detectDelimiter(%q) = %c, want %c", tt.text, got, tt.want)
			}
		})
	}
}

func TestComputeHash(t *testing.T) {
	accountID := uuid.MustParse("550e8400-e29b-41d4-a716-446655440000")
	date := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)

	t.Run("deterministic", func(t *testing.T) {
		txn := Transaction{
			AccountID:    accountID,
			Date:         date,
			Amount:       2500.0,
			Reference:    "Gehalt März",
			Counterparty: "Arbeitgeber GmbH",
		}
		hash1 := computeHash(txn)
		hash2 := computeHash(txn)
		if hash1 != hash2 {
			t.Errorf("computeHash not deterministic: %q != %q", hash1, hash2)
		}
	})

	t.Run("format matches expected SHA-256", func(t *testing.T) {
		txn := Transaction{
			AccountID:    accountID,
			Date:         date,
			Amount:       100.0,
			Reference:    "test",
			Counterparty: "peer",
		}
		data := fmt.Sprintf("%s|%s|%.4f|%s|%s",
			accountID.String(), "2026-03-01", 100.0, "test", "peer")
		expected := fmt.Sprintf("%x", sha256.Sum256([]byte(data)))

		got := computeHash(txn)
		if got != expected {
			t.Errorf("computeHash() = %q, want %q", got, expected)
		}
	})

	t.Run("different amounts produce different hashes", func(t *testing.T) {
		txn1 := Transaction{AccountID: accountID, Date: date, Amount: 100.0}
		txn2 := Transaction{AccountID: accountID, Date: date, Amount: 200.0}
		if computeHash(txn1) == computeHash(txn2) {
			t.Error("different amounts should produce different hashes")
		}
	})

	t.Run("different dates produce different hashes", func(t *testing.T) {
		txn1 := Transaction{AccountID: accountID, Date: date, Amount: 100.0}
		txn2 := Transaction{AccountID: accountID, Date: date.AddDate(0, 0, 1), Amount: 100.0}
		if computeHash(txn1) == computeHash(txn2) {
			t.Error("different dates should produce different hashes")
		}
	})

	t.Run("different accounts produce different hashes", func(t *testing.T) {
		txn1 := Transaction{AccountID: accountID, Date: date, Amount: 100.0}
		txn2 := Transaction{AccountID: uuid.New(), Date: date, Amount: 100.0}
		if computeHash(txn1) == computeHash(txn2) {
			t.Error("different account IDs should produce different hashes")
		}
	})
}

func TestParseCSVEdgeCases(t *testing.T) {
	t.Run("empty CSV", func(t *testing.T) {
		_, _, err := ParseCSV([]byte(""), uuid.New())
		if err == nil {
			t.Error("expected error for empty CSV")
		}
	})

	t.Run("header only no data", func(t *testing.T) {
		csv := "Auftragskonto;Buchungstag;Betrag"
		_, _, err := ParseCSV([]byte(csv), uuid.New())
		if err == nil {
			t.Error("expected error for header-only CSV")
		}
	})

	t.Run("all transactions get import hashes", func(t *testing.T) {
		csv := `"Date","Payee","Amount (EUR)"
"2026-03-01","Alice","100.00"
"2026-03-02","Bob","-50.00"`

		txns, _, err := ParseCSV([]byte(csv), uuid.New())
		if err != nil {
			t.Fatalf("ParseCSV: %v", err)
		}
		for i, txn := range txns {
			if txn.ImportHash == "" {
				t.Errorf("txn[%d] has empty import hash", i)
			}
		}
	})
}

func TestSparkasseDetect(t *testing.T) {
	p := &SparkasseParser{}
	tests := []struct {
		name   string
		header []string
		want   bool
	}{
		{
			name:   "valid sparkasse header",
			header: []string{"Auftragskonto", "Buchungstag", "Betrag", "Währung"},
			want:   true,
		},
		{
			name:   "missing buchungstag",
			header: []string{"Auftragskonto", "Betrag"},
			want:   false,
		},
		{
			name:   "n26 header rejected",
			header: []string{"Date", "Payee", "Amount (EUR)"},
			want:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := p.Detect(tt.header)
			if got != tt.want {
				t.Errorf("SparkasseParser.Detect(%v) = %v, want %v", tt.header, got, tt.want)
			}
		})
	}
}

func TestN26Detect(t *testing.T) {
	p := &N26Parser{}
	tests := []struct {
		name   string
		header []string
		want   bool
	}{
		{name: "english header", header: []string{"Date", "Payee", "Amount (EUR)"}, want: true},
		{name: "german header", header: []string{"Datum", "Empfänger", "Betrag (EUR)"}, want: true},
		{name: "sparkasse header rejected", header: []string{"Auftragskonto", "Buchungstag"}, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := p.Detect(tt.header)
			if got != tt.want {
				t.Errorf("N26Parser.Detect(%v) = %v, want %v", tt.header, got, tt.want)
			}
		})
	}
}

func TestScalableCapitalDetect(t *testing.T) {
	p := &ScalableCapitalParser{}
	tests := []struct {
		name   string
		header []string
		want   bool
	}{
		{name: "english with shares", header: []string{"date", "isin", "shares", "type"}, want: true},
		{name: "german with stück", header: []string{"datum", "isin", "stück", "typ"}, want: true},
		{name: "isin with type only", header: []string{"date", "isin", "type"}, want: true},
		{name: "missing isin", header: []string{"date", "shares", "type"}, want: false},
		{name: "isin only no shares or type", header: []string{"date", "isin", "amount"}, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := p.Detect(tt.header)
			if got != tt.want {
				t.Errorf("ScalableCapitalParser.Detect(%v) = %v, want %v", tt.header, got, tt.want)
			}
		})
	}
}

func TestHoldingsTemplateDetect(t *testing.T) {
	p := &HoldingsTemplateParser{}
	tests := []struct {
		name   string
		header []string
		want   bool
	}{
		{name: "valid template", header: []string{"isin", "name", "quantity", "market_value", "currency"}, want: true},
		{name: "missing market_value", header: []string{"isin", "quantity"}, want: false},
		{name: "missing isin", header: []string{"quantity", "market_value"}, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := p.Detect(tt.header)
			if got != tt.want {
				t.Errorf("HoldingsTemplateParser.Detect(%v) = %v, want %v", tt.header, got, tt.want)
			}
		})
	}
}
