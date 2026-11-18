package parser

import (
	"crypto/sha256"
	"fmt"
	"math"
	"math/big"
	"testing"

	"github.com/google/uuid"
)

// Regression tests for the 2026-11-18 calculation audit, Transactions C4 and
// W1 (docs/calc-audit-2026-11-18.md). Import hashes are pinned with the
// formulas the parsers used before the fixes, so already-imported rows keep
// deduplicating.

func n26Hash(acct uuid.UUID, date string, amount float64, ref, payee, typ string) string {
	data := fmt.Sprintf("%s|%s|%.4f|%s|%s|%s|%s|%.8f|%s", acct, date, amount, ref, payee, typ, "", 0.0, "EUR")
	return fmt.Sprintf("%x", sha256.Sum256([]byte(data)))
}

func scalableHash(acct uuid.UUID, date string, signed float64, isin, desc string, qty float64) string {
	data := fmt.Sprintf("%s|%s|%.4f|%s|%s|%.8f", acct, date, signed, isin, desc, qty)
	return fmt.Sprintf("%x", sha256.Sum256([]byte(data)))
}

// C4: N26 overdraft interest (−4.12) is a fee of 4.12, not +4.12 interest.
func TestN26NegativeInterestIsFeeWithUnchangedHash(t *testing.T) {
	csv := `"Date","Payee","Account number","Transaction type","Payment reference","Amount (EUR)","Amount (Foreign Currency)","Type Foreign Currency","Exchange Rate"
"2026-03-31","N26","","Interest","Overdraft interest","-4.12","","",""
"2026-03-31","N26","","Interest","Savings interest","1.05","","",""`
	acct := uuid.New()
	txns, _, _, err := ParseCSV([]byte(csv), acct)
	if err != nil {
		t.Fatalf("ParseCSV: %v", err)
	}
	if len(txns) != 2 {
		t.Fatalf("got %d txns, want 2", len(txns))
	}
	charge, credit := txns[0], txns[1]
	if charge.Type != "fee" || charge.Amount != 4.12 {
		t.Errorf("overdraft interest = %s %.2f, want fee 4.12", charge.Type, charge.Amount)
	}
	if want := n26Hash(acct, "2026-03-31", -4.12, "Overdraft interest", "N26", "interest"); charge.ImportHash != want {
		t.Errorf("overdraft interest hash changed: %s, want %s", charge.ImportHash, want)
	}
	if credit.Type != "interest" || credit.Amount != 1.05 {
		t.Errorf("credit interest = %s %.2f, want interest 1.05", credit.Type, credit.Amount)
	}
	if want := n26Hash(acct, "2026-03-31", 1.05, "Savings interest", "N26", "interest"); credit.ImportHash != want {
		t.Errorf("credit interest hash changed: %s, want %s", credit.ImportHash, want)
	}
}

// C4: Scalable rows whose direction only the sign carries.
func TestScalableTypeFollowsSign(t *testing.T) {
	csv := scalableHeader +
		"2026-02-01;SETTLED;CASH_TRANSACTION;TAX;;;Steuererstattung;;45.10;EUR;false\n" +
		"2026-02-02;SETTLED;CASH_TRANSACTION;TAX;;;Kapitalertragsteuer;;-12.50;EUR;false\n" +
		"2026-02-03;SETTLED;CASH_TRANSACTION;SOMETHING_NEW;;;Unbekannt;;-30;EUR;false\n" +
		"2026-02-04;SETTLED;CASH_TRANSACTION;SOMETHING_NEW;;;Unbekannt;;30;EUR;false\n" +
		"2026-02-05;SETTLED;CASH_TRANSACTION;INTEREST;;;Negativzins;;-1.20;EUR;false\n" +
		"2026-02-06;SETTLED;CASH_TRANSACTION;TAX;;;Kapitalertragsteuer;;12.50;EUR;true\n"
	acct := uuid.New()
	txns, _, _, err := ParseCSV([]byte(csv), acct)
	if err != nil {
		t.Fatalf("ParseCSV: %v", err)
	}
	want := []struct {
		date, typ, desc string
		signed          float64
		cash            float64 // cash effect: magnitude with the type's direction
	}{
		{"2026-02-01", "deposit", "Steuererstattung", 45.10, 45.10},
		{"2026-02-02", "tax", "Kapitalertragsteuer", -12.50, -12.50},
		{"2026-02-03", "withdrawal", "Unbekannt", -30, -30},
		{"2026-02-04", "deposit", "Unbekannt", 30, 30},
		{"2026-02-05", "fee", "Negativzins", -1.20, -1.20},
		{"2026-02-06", "deposit", "Kapitalertragsteuer", 12.50, 12.50}, // cancelled tax: refunded
	}
	if len(txns) != len(want) {
		t.Fatalf("got %d txns, want %d", len(txns), len(want))
	}
	for i, w := range want {
		got := txns[i]
		if got.Type != w.typ || got.Amount != math.Abs(w.signed) {
			t.Errorf("%s %s: got %s %.2f, want %s %.2f", w.date, w.desc, got.Type, got.Amount, w.typ, math.Abs(w.signed))
		}
		if c := cashImpactOf(got); math.Abs(c-w.cash) > 1e-9 {
			t.Errorf("%s %s: cash %.2f, want %.2f", w.date, w.desc, c, w.cash)
		}
		if h := scalableHash(acct, w.date, w.signed, "", w.desc, 0); got.ImportHash != h {
			t.Errorf("%s %s: import hash changed", w.date, w.desc)
		}
	}
}

// W1: a Morgan Stanley sale stores gross proceeds (quantity × price) and the
// deducted fee; the auto-wire withdraws the net amount, so the account stays
// cash-neutral and the import hashes are unchanged.
func TestMorganStanleySaleStoresGrossAndFee(t *testing.T) {
	csv := `Execution Date,Order Number,Plan,Type,Order Status,Price,Quantity,Net Amount,Net Share Proceeds,Tax Payment Method
08-Mar-2023,WRC77055FC9-1EE,GSU Class C,Sale,Complete,$96.01,-39.972,"$3,830.18",0,N/A
22-Dec-2025,WRC9A31F591-1EE,GSU Class C,Sale,Complete,$310.32,-92.371,"$28,664.57",0,N/A
30-Dec-2025,WRC9AD320A8-1EE,GSU Class C,Sale,Complete,$313.56,-10.450,"$3,276.70",0,N/A`
	acct := uuid.New()
	txns, _, _, err := ParseCSV([]byte(csv), acct)
	if err != nil {
		t.Fatalf("ParseCSV: %v", err)
	}
	if len(txns) != 6 {
		t.Fatalf("got %d txns, want 3 sells + 3 wires", len(txns))
	}
	cases := []struct {
		order           string
		gross, fee, net float64
	}{
		{"WRC77055FC9-1EE", 3837.71, 7.53, 3830.18}, // 39.972 × 96.01 = 3837.70572
		{"WRC9A31F591-1EE", 28664.57, 0, 28664.57},  // gross 28664.56872 < net: no fee
		{"WRC9AD320A8-1EE", 3276.70, 0, 3276.70},    // 3276.702 rounds to the net amount
	}
	for i, c := range cases {
		sell, wire := txns[2*i], txns[2*i+1]
		if sell.Type != "sell" || wire.Type != "withdrawal" {
			t.Fatalf("%s: types %s/%s", c.order, sell.Type, wire.Type)
		}
		if math.Abs(sell.Amount-c.gross) > 1e-9 || math.Abs(sell.Fee-c.fee) > 1e-9 {
			t.Errorf("%s: sell %.4f fee %.4f, want %.2f fee %.2f", c.order, sell.Amount, sell.Fee, c.gross, c.fee)
		}
		if math.Abs(wire.Amount-c.net) > 1e-9 || wire.Quantity != 0 || wire.Price != 0 {
			t.Errorf("%s: wire %.4f qty %v price %v, want %.2f without quantity/price", c.order, wire.Amount, wire.Quantity, wire.Price, c.net)
		}
		if net := cashImpactOf(sell) + cashImpactOf(wire); math.Abs(net) > 1e-9 {
			t.Errorf("%s: sale + wire cash = %.4f, want 0", c.order, net)
		}
		if sell.ImportHash != fmt.Sprintf("ms-sell-%s-%s", acct, c.order) || wire.ImportHash != fmt.Sprintf("ms-wire-%s-%s", acct, c.order) {
			t.Errorf("%s: import hashes changed: %s / %s", c.order, sell.ImportHash, wire.ImportHash)
		}
	}
}

// roundRatHalfAway must round like PostgreSQL's ROUND(numeric, n), which
// migration 032 uses to rewrite stored sales.
func TestRoundRatHalfAway(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"0.125", "0.13"}, {"-0.125", "-0.13"}, {"3837.70572", "3837.71"},
		{"28664.56872", "28664.57"}, {"3276.702", "3276.70"}, {"0.004", "0.00"},
	} {
		r, _ := new(big.Rat).SetString(c.in)
		if got := roundRatHalfAway(r, 2).FloatString(2); got != c.want {
			t.Errorf("round(%s, 2) = %s, want %s", c.in, got, c.want)
		}
	}
}
