package analytics

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func day(s string) time.Time {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(err)
	}
	return t
}

func TestMatchInKindTransferPairs_BrokerToBrokerMoveIsWash(t *testing.T) {
	out := uuid.New()
	in := uuid.New()
	txns := []InKindTransfer{
		{ID: out, Date: day("2026-03-10"), Type: "transfer_out", ISIN: "US0378331005", Quantity: 100},
		{ID: in, Date: day("2026-03-12"), Type: "transfer", ISIN: "US0378331005", Quantity: 100},
	}
	excluded := MatchInKindTransferPairs(txns)
	if _, ok := excluded[out]; !ok {
		t.Errorf("expected transfer_out to be excluded")
	}
	if _, ok := excluded[in]; !ok {
		t.Errorf("expected matching transfer to be excluded")
	}
}

func TestMatchInKindTransferPairs_RSUVestStaysAsContribution(t *testing.T) {
	// Lone in-kind transfer with no matching transfer_out — e.g. an RSU vest.
	in := uuid.New()
	txns := []InKindTransfer{
		{ID: in, Date: day("2026-03-15"), Type: "transfer", ISIN: "US02079K3059", Quantity: 12},
	}
	excluded := MatchInKindTransferPairs(txns)
	if len(excluded) != 0 {
		t.Errorf("RSU vest must not be excluded; got %d exclusions", len(excluded))
	}
}

func TestMatchInKindTransferPairs_DifferentQuantitiesDoNotMatch(t *testing.T) {
	// Vest of 100, then a transfer_out of 50 — partial moves are not matched.
	// The full vest stays as a Contribution; the transfer_out is already
	// Ignore in the classifier.
	out := uuid.New()
	in := uuid.New()
	txns := []InKindTransfer{
		{ID: in, Date: day("2026-03-01"), Type: "transfer", ISIN: "X", Quantity: 100},
		{ID: out, Date: day("2026-03-03"), Type: "transfer_out", ISIN: "X", Quantity: 50},
	}
	excluded := MatchInKindTransferPairs(txns)
	if len(excluded) != 0 {
		t.Errorf("partial qty must not match; got %d exclusions", len(excluded))
	}
}

func TestMatchInKindTransferPairs_OutsideDateWindowDoesNotMatch(t *testing.T) {
	// 7 days apart — outside the 5-day window.
	out := uuid.New()
	in := uuid.New()
	txns := []InKindTransfer{
		{ID: out, Date: day("2026-03-01"), Type: "transfer_out", ISIN: "X", Quantity: 50},
		{ID: in, Date: day("2026-03-08"), Type: "transfer", ISIN: "X", Quantity: 50},
	}
	excluded := MatchInKindTransferPairs(txns)
	if len(excluded) != 0 {
		t.Errorf("rows >5 days apart must not match; got %d exclusions", len(excluded))
	}
}

func TestMatchInKindTransferPairs_GreedyPicksNearestInTime(t *testing.T) {
	// Two candidates for one out — greedy must pick the closer date.
	out := uuid.New()
	far := uuid.New()
	near := uuid.New()
	txns := []InKindTransfer{
		{ID: far, Date: day("2026-03-15"), Type: "transfer", ISIN: "X", Quantity: 50},
		{ID: out, Date: day("2026-03-10"), Type: "transfer_out", ISIN: "X", Quantity: 50},
		{ID: near, Date: day("2026-03-11"), Type: "transfer", ISIN: "X", Quantity: 50},
	}
	excluded := MatchInKindTransferPairs(txns)
	if _, ok := excluded[near]; !ok {
		t.Errorf("expected nearest transfer to be matched")
	}
	if _, ok := excluded[far]; ok {
		t.Errorf("far transfer should remain unmatched (would-be RSU vest)")
	}
	if _, ok := excluded[out]; !ok {
		t.Errorf("expected transfer_out to be excluded")
	}
}

func TestMatchInKindTransferPairs_DifferentISINsDoNotMatch(t *testing.T) {
	out := uuid.New()
	in := uuid.New()
	txns := []InKindTransfer{
		{ID: out, Date: day("2026-03-10"), Type: "transfer_out", ISIN: "X", Quantity: 100},
		{ID: in, Date: day("2026-03-11"), Type: "transfer", ISIN: "Y", Quantity: 100},
	}
	excluded := MatchInKindTransferPairs(txns)
	if len(excluded) != 0 {
		t.Errorf("different ISINs must not match; got %d exclusions", len(excluded))
	}
}

func TestMatchInKindTransferPairs_MultiplePairsAllMatch(t *testing.T) {
	// Two independent broker-to-broker moves of the same ISIN, separated by
	// months. Each pair should match independently.
	o1, i1 := uuid.New(), uuid.New()
	o2, i2 := uuid.New(), uuid.New()
	txns := []InKindTransfer{
		{ID: o1, Date: day("2026-01-10"), Type: "transfer_out", ISIN: "X", Quantity: 25},
		{ID: i1, Date: day("2026-01-12"), Type: "transfer", ISIN: "X", Quantity: 25},
		{ID: o2, Date: day("2026-04-10"), Type: "transfer_out", ISIN: "X", Quantity: 25},
		{ID: i2, Date: day("2026-04-11"), Type: "transfer", ISIN: "X", Quantity: 25},
	}
	excluded := MatchInKindTransferPairs(txns)
	if len(excluded) != 4 {
		t.Errorf("expected 4 exclusions (2 pairs), got %d", len(excluded))
	}
}

func TestMatchInKindTransferPairs_CashTransfersIgnored(t *testing.T) {
	// Rows without ISIN don't participate.
	a := uuid.New()
	b := uuid.New()
	txns := []InKindTransfer{
		{ID: a, Date: day("2026-03-10"), Type: "transfer", ISIN: "", Quantity: 0},
		{ID: b, Date: day("2026-03-11"), Type: "transfer_out", ISIN: "", Quantity: 0},
	}
	excluded := MatchInKindTransferPairs(txns)
	if len(excluded) != 0 {
		t.Errorf("cash transfers (no ISIN) must not match; got %d", len(excluded))
	}
}
