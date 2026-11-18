package scheduler

import (
	"math"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/lnsp/wealth/internal/assets"
)

// backfillStartDate is the first day net worth can be non-zero: the first
// transaction, or an earlier property/vehicle purchase or loan payout.
func backfillStartDate(firstTxn time.Time, physical assets.Holdings) time.Time {
	start := firstTxn
	for _, a := range physical.Assets {
		if !a.PurchaseDate.IsZero() && a.PurchaseDate.Before(start) {
			start = a.PurchaseDate
		}
	}
	for _, l := range physical.Loans {
		if !l.PayoutDate.IsZero() && l.PayoutDate.Before(start) {
			start = l.PayoutDate
		}
	}
	return start
}

// snapshotCashDelta is a transaction's effect on its account's cash, in the
// transaction's currency: positive for inflows, negative for outflows, 0 for
// in-kind security moves. Amounts are taken as magnitudes signed by type, as
// in handler.txnSignedCashDelta, so a withdrawal stored with a negative
// amount still lowers cash.
func snapshotCashDelta(txType string, hasISIN bool, amount, fee, tax float64) float64 {
	mag, f, t := math.Abs(amount), math.Abs(fee), math.Abs(tax)
	switch txType {
	case "deposit", "interest", "sell", "dividend", "cash_transfer_in":
		return mag - f - t
	case "buy", "savings_plan", "withdrawal", "fee", "tax", "cash_transfer_out":
		return -mag - f - t
	case "transfer":
		if !hasISIN {
			return -mag - f - t
		}
	}
	return 0
}

// cashLedger holds each account's cash per currency, in that currency. The
// snapshot backfill converts it at each day's FX — as the live snapshot
// converts the per-currency balances at the latest FX — instead of freezing
// every transaction at its own date's rate, which made the first live day
// after a rebuild jump by the FX move on all foreign cash.
type cashLedger map[uuid.UUID]map[string]float64

func (l cashLedger) add(acct uuid.UUID, currency string, delta float64) {
	if delta == 0 {
		return
	}
	cur := strings.TrimSpace(currency)
	if cur == "" {
		cur = "EUR"
	}
	if l[acct] == nil {
		l[acct] = make(map[string]float64)
	}
	l[acct][cur] += delta
}

// eur is the ledger's total in EUR, each balance converted by toEUR.
// clamp(acct) reports accounts whose negative EUR cash is synthetic (a
// securities-only account without explicit cash transactions) and counts
// as 0, as in the live snapshot.
func (l cashLedger) eur(toEUR func(amount float64, currency string) float64, clamp func(uuid.UUID) bool) float64 {
	total := 0.0
	for acct, byCur := range l {
		bal := 0.0
		for cur, amt := range byCur {
			bal += toEUR(amt, cur)
		}
		if bal < 0 && clamp(acct) {
			bal = 0
		}
		total += bal
	}
	return total
}
