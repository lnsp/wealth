package parser

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestSparkasseParser_FeeAndTransferMagnitudesPositiveAndIntraFileUniqueness(t *testing.T) {
	csvData := strings.Join([]string{
		"Auftragskonto;Buchungstag;Valutadatum;Buchungstext;Verwendungszweck;Beguenstigter/Zahlungspflichtiger;Kontonummer;BLZ;Betrag;Waehrung;Info",
		"DE1234567890;15.03.2025;15.03.2025;ENTGELTABSCHLUSS;Kontofuehrung Maerz;Sparkasse;;;-4,90;EUR;Umsatz gebucht",
		"DE1234567890;16.03.2025;16.03.2025;UMBUCHUNG;Umbuchung Tagesgeld;Eigenes Konto;;;-500,00;EUR;Umsatz gebucht",
		"DE1234567890;16.03.2025;16.03.2025;KARTENZAHLUNG;Kaffee;Baeckerei;;;-3,50;EUR;Umsatz gebucht",
		"DE1234567890;16.03.2025;16.03.2025;KARTENZAHLUNG;Kaffee;Baeckerei;;;-3,50;EUR;Umsatz gebucht",
	}, "\n")

	acctID := uuid.New()
	txns, _, _, err := ParseCSV([]byte(csvData), acctID)
	if err != nil {
		t.Fatalf("ParseCSV Sparkasse failed: %v", err)
	}
	if len(txns) != 4 {
		t.Fatalf("expected 4 transactions, got %d", len(txns))
	}
	for i, tx := range txns {
		if tx.Amount <= 0 {
			t.Fatalf("txn[%d] (%s) Amount = %.2f, want positive magnitude > 0", i, tx.Type, tx.Amount)
		}
		if tx.Currency != "EUR" {
			t.Fatalf("txn[%d] Currency = %q, want EUR", i, tx.Currency)
		}
	}
	// Two identical coffee charges on the same day must receive distinct import hashes (intra-file occurrence suffix).
	if txns[2].ImportHash == txns[3].ImportHash {
		t.Fatalf("expected distinct ImportHash for duplicate same-day Sparkasse charges, got %s", txns[2].ImportHash)
	}
}

func TestDeltaParser_ThirdAssetCryptoFeeOmittedWithWarning(t *testing.T) {
	csvData := strings.Join([]string{
		`"Date","Way","Base amount","Base currency (name)","Base type","Quote amount","Quote currency","Exchange","Sent/Received from","Sent to","Fee amount","Fee currency (name)","Broker","Notes","Sync Base Holding","Leverage Metadata"`,
		`"2025-02-01T10:00:00.000Z","BUY",0.1,"BTC (Bitcoin)","CRYPTO",9000,"EUR","Binance",,,0.05,"BNB (BNB)",,"spot buy",true,`,
		`"2025-02-02T10:00:00.000Z","BUY",0.1,"BTC (Bitcoin)","CRYPTO",9000,"EUR","Binance",,,0.0001,"BTC (Bitcoin)",,"spot buy base fee",true,`,
	}, "\n")

	txns, _, res, err := ParseCSV([]byte(csvData), uuid.New())
	if err != nil {
		t.Fatalf("ParseCSV Delta failed: %v", err)
	}
	if len(txns) != 2 {
		t.Fatalf("expected 2 transactions, got %d", len(txns))
	}
	// Row 1: BNB fee cannot be valued in EUR without BNB quote -> omitted (0) + warning emitted
	if txns[0].Fee != 0 {
		t.Fatalf("expected BNB fee to be omitted rather than stored as raw EUR, got %.4f", txns[0].Fee)
	}
	if res == nil || len(res.Errors) == 0 {
		t.Fatal("expected warning in ImportResult.Errors when omitting third-currency crypto fee")
	}
	// Row 2: 0.0001 BTC fee at price 90,000 EUR/BTC -> 9.00 EUR
	if txns[1].Fee < 8.99 || txns[1].Fee > 9.01 {
		t.Fatalf("expected base-asset BTC fee converted to 9.00 EUR, got %.4f", txns[1].Fee)
	}
}
