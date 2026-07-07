package parser

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
)

func TestParseINGOrdermanager(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "ing_ordermanager.csv"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	accountID := uuid.New()
	txns, _, result, err := ParseCSV(data, accountID)
	if err != nil {
		t.Fatalf("ParseCSV: %v", err)
	}
	if result.Institution != "ing" {
		t.Errorf("institution: expected ing, got %s", result.Institution)
	}
	if result.AccountType != "brokerage" {
		t.Errorf("account type: expected brokerage, got %s", result.AccountType)
	}

	// 4 order rows: 3 Ausgeführt + 1 Storniert (must be skipped with warning).
	if len(txns) != 3 {
		t.Fatalf("expected 3 executed orders, got %d", len(txns))
	}

	// First buy: iShares EM, German-format quantity and price.
	if txns[0].Type != "buy" {
		t.Errorf("txn[0] type: expected buy, got %s", txns[0].Type)
	}
	if txns[0].SecurityISIN != "IE00B4L5YC18" {
		t.Errorf("txn[0] ISIN: expected IE00B4L5YC18, got %s", txns[0].SecurityISIN)
	}
	if txns[0].Quantity != 6.76942 {
		t.Errorf("txn[0] quantity: expected 6.76942, got %f", txns[0].Quantity)
	}
	if txns[0].Price != 42.692 {
		t.Errorf("txn[0] price: expected 42.692, got %f", txns[0].Price)
	}
	wantAmount := 6.76942 * 42.692
	if diff := txns[0].Amount - wantAmount; diff > 1e-6 || diff < -1e-6 {
		t.Errorf("txn[0] amount: expected ~%f, got %f", wantAmount, txns[0].Amount)
	}
	if txns[0].Reference != "466391180" {
		t.Errorf("txn[0] reference: expected order number 466391180, got %s", txns[0].Reference)
	}

	// Third row is a Verkauf → sell.
	if txns[2].Type != "sell" {
		t.Errorf("txn[2] type: expected sell, got %s", txns[2].Type)
	}
	if txns[2].SecurityISIN != "DE000ENAG999" {
		t.Errorf("txn[2] ISIN: expected DE000ENAG999, got %s", txns[2].SecurityISIN)
	}

	// Hashes must be unique.
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

func TestParseINGDepotuebersicht(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "ing_depotuebersicht.csv"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	accountID := uuid.New()
	txns, _, result, err := ParseCSV(data, accountID)
	if err != nil {
		t.Fatalf("ParseCSV: %v", err)
	}
	if result.Institution != "ing" {
		t.Errorf("institution: expected ing, got %s", result.Institution)
	}
	if result.AccountType != "brokerage" {
		t.Errorf("account type: expected brokerage, got %s", result.AccountType)
	}

	// 3 positions + 1 footer; E.ON has n.a. cost basis and gets skipped → 2 left.
	if len(txns) != 2 {
		t.Fatalf("expected 2 holdings transactions, got %d", len(txns))
	}

	// ARM HLDGS: 60 shares at 99.63 cost basis = 5977.80 Einstandswert
	arm := findByISIN(txns, "US0420682058")
	if arm == nil {
		t.Fatal("ARM HLDGS row missing")
	}
	if arm.Type != "buy" {
		t.Errorf("ARM type: expected buy, got %s", arm.Type)
	}
	if arm.Quantity != 60 {
		t.Errorf("ARM quantity: expected 60, got %f", arm.Quantity)
	}
	if arm.Price != 99.63 {
		t.Errorf("ARM price: expected 99.63, got %f", arm.Price)
	}
	if arm.Amount != 5977.80 {
		t.Errorf("ARM amount: expected 5977.80 (Einstandswert with thousands separator), got %f", arm.Amount)
	}
	if arm.Date.Format("2006-01-02") != "2026-05-15" {
		t.Errorf("ARM date: expected 2026-05-15, got %s", arm.Date.Format("2006-01-02"))
	}
}

func findByISIN(txns []Transaction, isin string) *Transaction {
	for i := range txns {
		if txns[i].SecurityISIN == isin {
			return &txns[i]
		}
	}
	return nil
}
