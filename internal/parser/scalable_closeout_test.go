package parser

import (
	"math"
	"testing"

	"github.com/google/uuid"
)

const scalableHeader = "date;status;type;sub_type;side;isin;description;quantity;amount;currency;is_cancellation\n"

// Shapes mirror a real HSBC turbo knock-out (signs recovered from prod import
// hashes): unsigned quantity and a nominal negative amount on the security
// row, the residual payout as a same-day DISTRIBUTION.
func knockoutCSV(subType string) string {
	return scalableHeader +
		"2026-05-08;SETTLED;SECURITY_TRANSACTION;SINGLE;BUY;DE000HM5JQH7;AMD Short Turbo HSBC;235;-264.19;EUR;false\n" +
		"2026-06-02;SETTLED;SECURITY_TRANSACTION;SINGLE;BUY;DE000HM5JQH7;AMD Short Turbo HSBC;266;-80.79;EUR;false\n" +
		"2026-06-05;SETTLED;CASH_TRANSACTION;DISTRIBUTION;;DE000HM5JQH7;AMD Short Turbo HSBC;;91.36;EUR;false\n" +
		"2026-06-05;SETTLED;NON_TRADE_SECURITY_TRANSACTION;" + subType + ";;DE000HM5JQH7;AMD Short Turbo HSBC;501;-0.501;EUR;false\n"
}

func TestScalableKnockoutBecomesSell(t *testing.T) {
	for _, sub := range []string{"KNOCK_OUT", "REDEMPTION", "TRANSFER_IN", ""} {
		t.Run("sub_type="+sub, func(t *testing.T) {
			txns, _, _, err := ParseCSV([]byte(knockoutCSV(sub)), uuid.New())
			if err != nil {
				t.Fatalf("ParseCSV: %v", err)
			}
			if len(txns) != 3 {
				t.Fatalf("got %d txns, want 3 (2 buys + 1 sell): %+v", len(txns), txns)
			}
			var held float64
			for _, tx := range txns {
				switch tx.Type {
				case "buy":
					held += tx.Quantity
				case "sell":
					held -= tx.Quantity
					if tx.Quantity != 501 || tx.Amount != 91.36 {
						t.Errorf("sell = %g units for %.2f, want 501 for 91.36", tx.Quantity, tx.Amount)
					}
					if math.Abs(tx.Price-91.36/501) > 1e-12 {
						t.Errorf("sell price = %g, want %g", tx.Price, 91.36/501)
					}
				default:
					t.Errorf("unexpected %s row: %+v", tx.Type, tx)
				}
			}
			if held != 0 {
				t.Errorf("position after knock-out = %g, want 0", held)
			}
		})
	}
}

// The sell must keep the payout row's hash so re-importing over the old
// misclassified dividend reclassifies it instead of adding a second row.
func TestScalableKnockoutKeepsPayoutHash(t *testing.T) {
	acct := uuid.New()
	payoutOnly := scalableHeader +
		"2026-06-05;SETTLED;CASH_TRANSACTION;DISTRIBUTION;;DE000HM5JQH7;AMD Short Turbo HSBC;;91.36;EUR;false\n"
	alone, _, _, err := ParseCSV([]byte(payoutOnly), acct)
	if err != nil || len(alone) != 1 || alone[0].Type != "dividend" {
		t.Fatalf("payout alone should stay a dividend: %+v, %v", alone, err)
	}
	merged, _, _, err := ParseCSV([]byte(knockoutCSV("KNOCK_OUT")), acct)
	if err != nil {
		t.Fatalf("ParseCSV: %v", err)
	}
	for _, tx := range merged {
		if tx.Type == "sell" && tx.ImportHash != alone[0].ImportHash {
			t.Errorf("sell hash %s != payout hash %s", tx.ImportHash, alone[0].ImportHash)
		}
	}
}

// Real ETF distributions and genuine transfers must be untouched.
func TestScalableCloseoutNeedsSameDayPair(t *testing.T) {
	csv := scalableHeader +
		// distribution with no security booking that day
		"2026-03-02;SETTLED;CASH_TRANSACTION;DISTRIBUTION;;IE00B3RBWM25;Vanguard FTSE All-World;;42.10;EUR;false\n" +
		// incoming transfer of a different ISIN on a distribution day
		"2026-03-02;SETTLED;NON_TRADE_SECURITY_TRANSACTION;TRANSFER_IN;;IE00B4L5Y983;iShares Core MSCI World;10;0;EUR;false\n" +
		// transfer and payout of the same ISIN on different days
		"2026-04-01;SETTLED;NON_TRADE_SECURITY_TRANSACTION;TRANSFER_IN;;IE00B5BMR087;iShares Core S&P 500;5;0;EUR;false\n" +
		"2026-04-02;SETTLED;CASH_TRANSACTION;DISTRIBUTION;;IE00B5BMR087;iShares Core S&P 500;;3.00;EUR;false\n"
	txns, _, _, err := ParseCSV([]byte(csv), uuid.New())
	if err != nil {
		t.Fatalf("ParseCSV: %v", err)
	}
	want := []string{"dividend", "transfer", "transfer", "dividend"}
	if len(txns) != len(want) {
		t.Fatalf("got %d txns, want %d", len(txns), len(want))
	}
	for i, tx := range txns {
		if tx.Type != want[i] {
			t.Errorf("row %d: type %s, want %s", i, tx.Type, want[i])
		}
	}
}
