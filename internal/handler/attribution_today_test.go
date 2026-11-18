package handler

import (
	"math"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/lnsp/wealth/internal/database/generated"
	"github.com/lnsp/wealth/internal/market"
)

// Regression for Net Worth F9: "Dragged by A (-120 EUR) and B (--40 EUR)".
func TestAttributionSummary_ExplicitSigns(t *testing.T) {
	c := func(name string, v float64) attributionContribution {
		return attributionContribution{Name: name, Change: v}
	}
	cases := []struct {
		in   []attributionContribution
		want string
	}{
		{[]attributionContribution{c("A", -120), c("B", 40)}, "Dragged by A (-120 EUR), offset by B (+40 EUR)"},
		{[]attributionContribution{c("A", -120), c("B", -40)}, "Dragged by A (-120 EUR) and B (-40 EUR)"},
		{[]attributionContribution{c("A", 1234), c("B", -40)}, "Driven by A (+1.234 EUR), offset by B (-40 EUR)"},
		{[]attributionContribution{c("A", 120), c("B", 40)}, "Driven by A (+120 EUR) and B (+40 EUR)"},
		{[]attributionContribution{c("A", -55)}, "Dragged by A (-55 EUR)"},
		{[]attributionContribution{c("A", 55), c("B", 0.2)}, "Driven by A (+55 EUR)"},
		{[]attributionContribution{c("A", 0)}, "No notable price moves on your holdings."},
		{nil, ""},
	}
	for _, tc := range cases {
		if got := attributionSummary(tc.in); got != tc.want {
			t.Errorf("attributionSummary(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// Regression for Net Worth F4: the move was the last two price rows,
// whatever their dates, both at today's FX. Snapshots on 17 and 18 Nov with
// no new USD price since the 17th must show no price move; the FX change
// between the two snapshot dates is part of the market move.
func TestHoldingMoveEUR_PricesEachSnapshotDateAtItsFX(t *testing.T) {
	d := func(s string) time.Time { t, _ := time.Parse("2006-01-02", s); return t }
	rows := []db.ListPriceHistoryRow{
		{Date: d("2026-11-16"), Close: numericFromFloat(281.26), Currency: "USD"},
		{Date: d("2026-11-17"), Close: numericFromFloat(277.60), Currency: "USD"},
	}
	fx := []market.HistoricalFXRate{
		{Date: d("2026-11-16"), Currency: "USD", Rate: 1.1014},
		{Date: d("2026-11-17"), Currency: "USD", Rate: 1.0989},
	}
	latest := map[string]float64{"USD": 1.0989}

	got, ok := holdingMoveEUR(rows, 46.49, d("2026-11-17"), d("2026-11-18"), fx, latest)
	if !ok || got != 0 {
		t.Errorf("17→18 Nov move = %.2f (ok=%v), want 0: no price after the 17th", got, ok)
	}

	got, _ = holdingMoveEUR(rows, 46.49, d("2026-11-16"), d("2026-11-17"), fx, latest)
	want := 46.49 * (277.60/1.0989 - 281.26/1.1014)
	if math.Abs(got-want) > 0.005 {
		t.Errorf("16→17 Nov move = %.2f, want %.2f (each day at its own FX)", got, want)
	}

	if _, ok := holdingMoveEUR(rows, 1, d("2026-11-01"), d("2026-11-17"), fx, latest); ok {
		t.Error("a date before the first price must not be priced")
	}
}

func TestNetWorthFlows_OnlyRealFlowsInTheWindow(t *testing.T) {
	d := func(s string) time.Time { t, _ := time.Parse("2006-01-02", s); return t }
	txn := func(date, typ, isin string, amount, fee float64) db.ListTransactionsRow {
		r := db.ListTransactionsRow{ID: uuid.New(), Date: d(date), Type: typ, Amount: numericFromFloat(amount), Fee: numericFromFloat(fee), Currency: "EUR"}
		if isin != "" {
			r.SecurityISIN = pgtype.Text{String: isin, Valid: true}
			r.Quantity = numericFromFloat(1)
		}
		return r
	}
	loanPayment := txn("2026-11-18", "withdrawal", "", 1200, 0)
	txns := []db.ListTransactionsRow{
		txn("2026-11-18", "deposit", "", 500, 0),
		txn("2026-11-18", "withdrawal", "", -200, 0), // stored negative: still an outflow
		txn("2026-11-18", "buy", "IE00B4L5Y983", 300, 1),
		txn("2026-11-18", "dividend", "IE00B4L5Y983", 12, 0),
		txn("2026-11-18", "cash_transfer_out", "", 1000, 0),
		txn("2026-11-18", "cash_transfer_in", "", 1000, 0),
		loanPayment,
		txn("2026-11-17", "deposit", "", 9999, 0), // the "from" day is already in that snapshot
	}
	linked := map[uuid.UUID]bool{loanPayment.ID: true}
	implicit := []implicitCashFlowEvent{{Date: d("2026-11-18"), DeltaEUR: 50}}

	contrib, income := netWorthFlows(txns, inKindWashSet(txns), linked, implicit, d("2026-11-17"), d("2026-11-18"), nil, nil)
	if math.Abs(contrib-350) > 1e-9 {
		t.Errorf("contributions = %.2f, want 500 − 200 + 50 implicit = 350", contrib)
	}
	if math.Abs(income-11) > 1e-9 {
		t.Errorf("income = %.2f, want dividend 12 − buy fee 1 = 11", income)
	}
}
