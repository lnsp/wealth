package scheduler

import (
	"math"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/lnsp/wealth/internal/assets"
	db "github.com/lnsp/wealth/internal/database/generated"
)

// Regression for Net Worth F8: the backfill converted each cash transaction
// at its own date's FX and kept that EUR amount, while the live snapshot
// converts the balance at the latest rate — a fake move on the first live
// day after every rebuild.
func TestCashLedger_RevaluesForeignCashAtEachDaysFX(t *testing.T) {
	ms, n26 := uuid.New(), uuid.New()
	l := make(cashLedger)
	l.add(ms, "USD", snapshotCashDelta("sell", true, 1000, 0, 0))       // $1,000 sale proceeds
	l.add(ms, "USD", snapshotCashDelta("withdrawal", false, 400, 0, 0)) // $400 wired out
	l.add(n26, "", snapshotCashDelta("deposit", false, 250, 0, 0))      // blank currency = EUR

	rate := 1.10
	toEUR := func(a float64, cur string) float64 {
		if cur == "USD" {
			return a / rate
		}
		return a
	}
	noClamp := func(uuid.UUID) bool { return false }

	if got, want := l.eur(toEUR, noClamp), 600/1.10+250; math.Abs(got-want) > 1e-9 {
		t.Errorf("day 1 cash = %.4f, want %.4f", got, want)
	}
	rate = 1.0989 // next day: the same $600 is worth more euros
	if got, want := l.eur(toEUR, noClamp), 600/1.0989+250; math.Abs(got-want) > 1e-9 {
		t.Errorf("day 2 cash = %.4f, want %.4f (balance revalued at the day's FX)", got, want)
	}
}

func TestCashLedger_ClampsSyntheticNegativeCash(t *testing.T) {
	broker := uuid.New()
	l := make(cashLedger)
	l.add(broker, "EUR", snapshotCashDelta("buy", true, 500, 1, 0))
	id := func(a float64, _ string) float64 { return a }
	if got := l.eur(id, func(uuid.UUID) bool { return true }); got != 0 {
		t.Errorf("clamped = %v, want 0", got)
	}
	if got := l.eur(id, func(uuid.UUID) bool { return false }); got != -501 {
		t.Errorf("unclamped = %v, want -501", got)
	}
}

func TestSnapshotCashDelta_SignsByType(t *testing.T) {
	cases := []struct {
		typ    string
		isin   bool
		amount float64
		want   float64
	}{
		{"withdrawal", false, -200, -200}, // stored negative: still an outflow
		{"withdrawal", false, 200, -200},
		{"deposit", false, 300, 300},
		{"dividend", true, 10, 10},
		{"transfer", true, 5000, 0}, // RSU vest: shares, no cash
		{"transfer", false, 50, -50},
		{"transfer_out", true, 5000, 0},
	}
	for _, c := range cases {
		if got := snapshotCashDelta(c.typ, c.isin, c.amount, 0, 0); got != c.want {
			t.Errorf("snapshotCashDelta(%s, %v, %v) = %v, want %v", c.typ, c.isin, c.amount, got, c.want)
		}
	}
	if got := snapshotCashDelta("sell", true, 1000, 2, 30); got != 968 {
		t.Errorf("sell net of fee and tax = %v, want 968", got)
	}
}

// Info I5: the rebuild deletes snapshots before this date.
func TestBackfillStartDate(t *testing.T) {
	d := func(s string) time.Time { t, _ := time.Parse("2006-01-02", s); return t }
	first := d("2022-12-25")
	if got := backfillStartDate(first, assets.Holdings{}); !got.Equal(first) {
		t.Errorf("no assets: start = %s, want the first transaction", got.Format("2006-01-02"))
	}
	h := assets.Holdings{
		Assets: []assets.Asset{{Asset: db.Asset{PurchaseDate: d("2025-04-01")}}},
		Loans:  []assets.Loan{{Loan: db.Loan{PayoutDate: d("2021-06-30")}}},
	}
	if got := backfillStartDate(first, h); !got.Equal(d("2021-06-30")) {
		t.Errorf("start = %s, want the earlier loan payout 2021-06-30", got.Format("2006-01-02"))
	}
}
