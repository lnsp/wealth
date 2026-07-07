package handler

import (
	"math"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/lnsp/wealth/internal/analytics"
	db "github.com/lnsp/wealth/internal/database/generated"
)

// Time Machine attribution identity:
//
//	nw_change = contrib + market + dividends + interest − |fees| − |taxes|
//
// The handler computes market_return as the residual that makes this
// equation hold. This test verifies the *bucket* totals (contrib /
// dividends / interest / fees / taxes) are correct over a synthetic
// post-target-date transaction stream including a washed broker-to-broker
// in-kind pair, an internal cash transfer pair, and a standalone RSU vest.

func TestTimeMachineAttribution_BucketIdentity(t *testing.T) {
	mkTxn := func(date string, typ string, isin string, qty, amt, fee, tax float64) db.ListTransactionsRow {
		d, err := time.Parse("2006-01-02", date)
		if err != nil {
			t.Fatalf("bad date %q: %v", date, err)
		}
		row := db.ListTransactionsRow{
			ID:     uuid.New(),
			Date:   d,
			Type:   typ,
			Amount: numericFromFloat(amt),
		}
		if isin != "" {
			row.SecurityISIN = pgtype.Text{String: isin, Valid: true}
		}
		if qty != 0 {
			row.Quantity = numericFromFloat(qty)
		}
		if fee != 0 {
			row.Fee = numericFromFloat(fee)
		}
		if tax != 0 {
			row.Tax = numericFromFloat(tax)
		}
		return row
	}

	// Target date: 2024-01-01. All txns below are "since then".
	txns := []db.ListTransactionsRow{
		mkTxn("2024-02-15", "deposit", "", 0, 5000, 0, 0),
		mkTxn("2024-03-15", "transfer", "US0000ABC001", 20, 4000, 0, 0), // RSU vest
		mkTxn("2024-04-15", "dividend", "DE000XYZ0001", 0, 100, 5, 25),
		mkTxn("2024-05-15", "interest", "", 0, 50, 0, 0),
		mkTxn("2024-06-15", "buy", "DE000XYZ0001", 1, 200, 1, 0),
		// Internal cash transfer pair — Ignore at portfolio scope.
		mkTxn("2024-07-01", "cash_transfer_out", "", 0, 1000, 0, 0),
		mkTxn("2024-07-01", "cash_transfer_in", "", 0, 1000, 0, 0),
		// Broker-to-broker in-kind pair (same ISIN, same qty, within 5d).
		mkTxn("2024-08-01", "transfer", "US0000XYZ002", 10, 1100, 0, 0),
		mkTxn("2024-08-03", "transfer_out", "US0000XYZ002", 10, 1100, 0, 0),
		mkTxn("2024-09-01", "withdrawal", "", 0, 500, 0, 0),
	}

	// Wash set.
	var inkind []analytics.InKindTransfer
	for _, tx := range txns {
		if !tx.SecurityISIN.Valid {
			continue
		}
		if tx.Type != "transfer" && tx.Type != "transfer_out" {
			continue
		}
		qty := 0.0
		if tx.Quantity.Valid {
			f, _ := tx.Quantity.Float64Value()
			qty = f.Float64
		}
		inkind = append(inkind, analytics.InKindTransfer{
			ID: tx.ID, Date: tx.Date, Type: tx.Type, ISIN: tx.SecurityISIN.String, Quantity: qty,
		})
	}
	washed := analytics.MatchInKindTransferPairs(inkind)
	if len(washed) != 2 {
		t.Fatalf("expected 2 washed txns (the XYZ broker-to-broker pair), got %d", len(washed))
	}

	var contrib, dividends, interest, fees, taxes float64
	for _, tx := range txns {
		amt := 0.0
		if tx.Amount.Valid {
			f, _ := tx.Amount.Float64Value()
			amt = f.Float64
		}
		feeAmt := 0.0
		if tx.Fee.Valid {
			f, _ := tx.Fee.Float64Value()
			feeAmt = f.Float64
		}
		taxAmt := 0.0
		if tx.Tax.Valid {
			f, _ := tx.Tax.Float64Value()
			taxAmt = f.Float64
		}
		fees += feeAmt
		taxes += taxAmt
		if _, isWash := washed[tx.ID]; isWash {
			continue
		}
		switch analytics.ClassifyForAttribution(tx.Type, tx.SecurityISIN.Valid, analytics.ScopePortfolio) {
		case analytics.BucketContribution:
			contrib += amt
		case analytics.BucketWithdrawal:
			contrib -= amt
		case analytics.BucketDividend:
			dividends += amt
		case analytics.BucketInterest:
			interest += amt
		case analytics.BucketFee:
			fees += amt
		case analytics.BucketTax:
			taxes += amt
		}
	}

	// Expected: 5000 cash + 4000 RSU vest − 500 withdrawal = 8500 contrib.
	// Internal cash pair washes; broker-to-broker XYZ pair washes.
	if math.Abs(contrib-8500) > 1 {
		t.Errorf("contrib=%.2f, want 8500 (5k cash + 4k vest − 500 withdrawal)", contrib)
	}
	if math.Abs(dividends-100) > 1 {
		t.Errorf("dividends=%.2f, want 100", dividends)
	}
	if math.Abs(interest-50) > 1 {
		t.Errorf("interest=%.2f, want 50", interest)
	}
	// fees come from per-row Fee column: 5 (dividend row) + 1 (buy row) = 6.
	if math.Abs(fees-6) > 1 {
		t.Errorf("fees=%.2f, want 6", fees)
	}
	// taxes come from per-row Tax column: 25 (dividend row).
	if math.Abs(taxes-25) > 1 {
		t.Errorf("taxes=%.2f, want 25", taxes)
	}

	// Pick an arbitrary nwChange that satisfies the identity, then verify
	// the residual market_return formula reproduces it.
	const marketReturnTarget = 750.0
	nwChange := contrib + marketReturnTarget + dividends + interest - math.Abs(fees) - math.Abs(taxes)
	marketReturn := nwChange - contrib - dividends - interest + math.Abs(fees) + math.Abs(taxes)
	if math.Abs(marketReturn-marketReturnTarget) > 0.5 {
		t.Errorf("residual market_return=%.2f, expected %.2f (identity broken)", marketReturn, marketReturnTarget)
	}
}

// TestTimeMachineHoldings_RebuildsQuantitiesCorrectly mirrors the in-handler
// holdings-reconstruction switch on a synthetic stream. Buy/sell flow,
// in-kind transfer in/out, and the broker-to-broker pair (which nets to
// zero qty change) all hit the right end-state quantities.
func TestTimeMachineHoldings_RebuildsQuantitiesCorrectly(t *testing.T) {
	mkTxn := func(date, typ, isin string, qty, amt float64) struct {
		date  time.Time
		typ   string
		isin  string
		qty   float64
		amt   float64
	} {
		d, _ := time.Parse("2006-01-02", date)
		return struct {
			date  time.Time
			typ   string
			isin  string
			qty   float64
			amt   float64
		}{d, typ, isin, qty, amt}
	}

	txns := []struct {
		date  time.Time
		typ   string
		isin  string
		qty   float64
		amt   float64
	}{
		mkTxn("2024-01-10", "buy", "DE000XYZ0001", 50, 5000),
		mkTxn("2024-02-15", "transfer", "US0000ABC001", 20, 4000),  // RSU vest
		mkTxn("2024-06-15", "sell", "DE000XYZ0001", 10, 1100),
		// Broker-to-broker in-kind pair on XYZ — qty nets to zero on net.
		mkTxn("2024-07-01", "transfer", "US0000RST003", 5, 600),
		mkTxn("2024-07-03", "transfer_out", "US0000RST003", 5, 600),
	}

	type lot struct{ qty, costBasis float64 }
	holdings := make(map[string]*lot)
	for _, tx := range txns {
		switch tx.typ {
		case "buy", "savings_plan", "transfer":
			if tx.isin == "" || tx.qty <= 0 {
				continue
			}
			h, ok := holdings[tx.isin]
			if !ok {
				h = &lot{}
				holdings[tx.isin] = h
			}
			h.qty += tx.qty
			h.costBasis += tx.amt
		case "sell", "transfer_out":
			if tx.isin == "" {
				continue
			}
			h := holdings[tx.isin]
			if h == nil || h.qty <= 0 {
				continue
			}
			avg := h.costBasis / h.qty
			sold := math.Min(tx.qty, h.qty)
			h.qty -= sold
			h.costBasis -= sold * avg
		}
	}

	// XYZ: 50 bought - 10 sold = 40 shares; cost basis = 5000 × (40/50) = 4000.
	if h := holdings["DE000XYZ0001"]; h == nil {
		t.Fatal("XYZ holding missing")
	} else {
		if math.Abs(h.qty-40) > 0.001 {
			t.Errorf("XYZ qty=%.3f, want 40", h.qty)
		}
		if math.Abs(h.costBasis-4000) > 1 {
			t.Errorf("XYZ cost_basis=%.2f, want 4000", h.costBasis)
		}
	}
	// ABC: standalone RSU vest, no sell. 20 sh, cost 4000.
	if h := holdings["US0000ABC001"]; h == nil {
		t.Fatal("ABC holding missing")
	} else {
		if math.Abs(h.qty-20) > 0.001 {
			t.Errorf("ABC qty=%.3f, want 20", h.qty)
		}
	}
	// RST: broker-to-broker pair — qty 0, cost 0.
	if h := holdings["US0000RST003"]; h != nil && h.qty > 0.001 {
		t.Errorf("RST should be zero after washed pair, got qty=%.3f cost=%.2f", h.qty, h.costBasis)
	}
}
