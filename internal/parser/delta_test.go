package parser

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
)

func TestParseDelta(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "delta.csv"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	accountID := uuid.New()
	txns, _, result, err := ParseCSV(data, accountID)
	if err != nil {
		t.Fatalf("ParseCSV: %v", err)
	}
	if result.Institution != "delta" {
		t.Errorf("institution: expected delta, got %s", result.Institution)
	}
	if result.AccountType != "brokerage" {
		t.Errorf("account type: expected brokerage, got %s", result.AccountType)
	}

	// 5 rows: BTC DEPOSIT, EUR WITHDRAW (sync leg → skipped), ASME.DE BUY,
	// ETH BUY, ASME.DE SELL. We expect 4 transactions.
	if len(txns) != 4 {
		t.Fatalf("expected 4 transactions (sync leg skipped), got %d", len(txns))
	}

	btc := findByISIN(txns, "CRYPTO:BTC")
	if btc == nil {
		t.Fatal("BTC deposit missing")
	}
	if btc.Type != "transfer" {
		t.Errorf("BTC type: expected transfer (no cost basis), got %s", btc.Type)
	}
	if btc.Quantity != 0.67420698 {
		t.Errorf("BTC quantity: expected 0.67420698, got %f", btc.Quantity)
	}
	if btc.Amount != 0 {
		t.Errorf("BTC amount: expected 0 (no cost basis), got %f", btc.Amount)
	}
	if btc.Counterparty != "Bitcoin" {
		t.Errorf("BTC counterparty: expected Bitcoin, got %s", btc.Counterparty)
	}

	asml := findByISIN(txns, "ASME.DE")
	if asml == nil {
		t.Fatal("ASME.DE buy missing")
	}
	if asml.Type != "buy" {
		t.Errorf("ASML type: expected buy, got %s", asml.Type)
	}
	if asml.Quantity != 67 {
		t.Errorf("ASML quantity: expected 67, got %f", asml.Quantity)
	}
	if asml.Amount != 6769 {
		t.Errorf("ASML amount: expected 6769, got %f", asml.Amount)
	}
	if asml.Fee != 11 {
		t.Errorf("ASML fee: expected 11, got %f", asml.Fee)
	}
	if asml.Currency != "EUR" {
		t.Errorf("ASML currency: expected EUR, got %s", asml.Currency)
	}

	eth := findByISIN(txns, "CRYPTO:ETH")
	if eth == nil {
		t.Fatal("ETH buy missing")
	}
	if eth.Type != "buy" {
		t.Errorf("ETH type: expected buy, got %s", eth.Type)
	}
	if eth.Quantity != 1.5 {
		t.Errorf("ETH quantity: expected 1.5, got %f", eth.Quantity)
	}

	// The SELL row produces a second ASME.DE transaction — verify by counting.
	asmlCount := 0
	for _, tx := range txns {
		if tx.SecurityISIN == "ASME.DE" {
			asmlCount++
		}
	}
	if asmlCount != 2 {
		t.Errorf("expected 2 ASME.DE rows (buy + sell), got %d", asmlCount)
	}

	// Hashes unique.
	seen := make(map[string]bool)
	for i, tx := range txns {
		if tx.ImportHash == "" {
			t.Errorf("txn[%d]: empty import hash", i)
		}
		if seen[tx.ImportHash] {
			t.Errorf("txn[%d]: duplicate import hash", i)
		}
		seen[tx.ImportHash] = true
	}
}

func TestDeltaSyntheticISINClassifiedAsCrypto(t *testing.T) {
	if got := ClassifyAsset("CRYPTO:BTC", "Bitcoin"); got != "crypto" {
		t.Errorf("CRYPTO:BTC: expected crypto, got %s", got)
	}
	if got := ClassifyAsset("CRYPTO:ETH", "Ethereum"); got != "crypto" {
		t.Errorf("CRYPTO:ETH: expected crypto, got %s", got)
	}
	// Stock tickers (no CRYPTO: prefix) fall through to name-based heuristics.
	if got := ClassifyAsset("ASME.DE", "ASML Holding NV"); got != "stock" {
		t.Errorf("ASME.DE: expected stock, got %s", got)
	}
}

func TestSplitDeltaSymbol(t *testing.T) {
	cases := []struct {
		in, sym, name string
	}{
		{"BTC (Bitcoin)", "BTC", "Bitcoin"},
		{"ASME.DE (ASML Holding NV)", "ASME.DE", "ASML Holding NV"},
		{"EUR (Euro)", "EUR", "Euro"},
		{"BTC", "BTC", ""},
		{"", "", ""},
	}
	for _, c := range cases {
		sym, name := splitDeltaSymbol(c.in)
		if sym != c.sym || name != c.name {
			t.Errorf("splitDeltaSymbol(%q) = (%q, %q), want (%q, %q)", c.in, sym, name, c.sym, c.name)
		}
	}
}
