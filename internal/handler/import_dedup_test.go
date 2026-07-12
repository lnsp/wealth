package handler

import (
	"fmt"
	"testing"

	"github.com/google/uuid"

	db "github.com/lnsp/wealth/internal/database/generated"
)

// These tests pin the similarity-adoption dedup contract in insertTransactions:
// when an importer version changes its ImportHash format, re-importing the same
// file must not duplicate rows. Incoming rows whose hash is unknown adopt an
// economically identical existing row (same account, date, type, ISIN,
// quantity, amount, fee, tax, currency) instead of inserting — unless that row
// is already accounted for by the batch. This is the regression guard for the
// 2026-07-12 prod incident where the Morgan Stanley withdrawals hash scheme
// changed (sha256 → ms-sell-/ms-wire-) and a reimport double-counted 9 sales.

func row(hash string) db.ListSimilarTransactionsRow {
	return db.ListSimilarTransactionsRow{ID: uuid.New(), ImportHash: hash}
}

func TestPickAdoptable_LegacyRowIsAdopted(t *testing.T) {
	legacy := row("a1b2c3d4-legacy-sha256")
	batch := map[string]bool{"ms-sell-acct-ORDER1": true}

	id, ok := pickAdoptable([]db.ListSimilarTransactionsRow{legacy}, batch, map[uuid.UUID]bool{})
	if !ok {
		t.Fatal("expected legacy row to be adoptable")
	}
	if id != legacy.ID {
		t.Errorf("adopted %s, want %s", id, legacy.ID)
	}
}

func TestPickAdoptable_NoCandidates(t *testing.T) {
	if _, ok := pickAdoptable(nil, map[string]bool{"h": true}, map[uuid.UUID]bool{}); ok {
		t.Error("no candidates must not adopt")
	}
}

func TestPickAdoptable_BatchOwnedRowIsNotAdopted(t *testing.T) {
	// The existing row's hash matches another incoming row in this batch: that
	// row is its rightful owner (it will dedup by hash), so a second identical
	// incoming transaction must insert, not adopt.
	owned := row("ms-vest-acct-2026-05-25-1")
	batch := map[string]bool{
		"ms-vest-acct-2026-05-25-1": true,
		"ms-vest-acct-2026-05-25-2": true,
	}

	if _, ok := pickAdoptable([]db.ListSimilarTransactionsRow{owned}, batch, map[uuid.UUID]bool{}); ok {
		t.Error("row whose hash belongs to the batch must not be adopted")
	}
}

func TestPickAdoptable_ClaimedRowIsNotAdoptedTwice(t *testing.T) {
	legacy := row("legacy-1")
	claimed := map[uuid.UUID]bool{legacy.ID: true}

	if _, ok := pickAdoptable([]db.ListSimilarTransactionsRow{legacy}, map[string]bool{}, claimed); ok {
		t.Error("already-claimed row must not be adopted again")
	}
}

func TestPickAdoptable_SkipsClaimedAndPicksNext(t *testing.T) {
	first, second := row("legacy-1"), row("legacy-2")
	claimed := map[uuid.UUID]bool{first.ID: true}

	id, ok := pickAdoptable([]db.ListSimilarTransactionsRow{first, second}, map[string]bool{}, claimed)
	if !ok || id != second.ID {
		t.Errorf("expected second candidate %s, got %s (ok=%v)", second.ID, id, ok)
	}
}

// Multiset semantics: a file with two identical transactions against a DB
// holding two identical legacy rows adopts both, one each — while a DB holding
// only one legacy row yields one adoption and leaves the second incoming row
// to insert.
func TestPickAdoptable_MultisetPairing(t *testing.T) {
	legacyA, legacyB := row("legacy-a"), row("legacy-b")
	candidates := []db.ListSimilarTransactionsRow{legacyA, legacyB}
	batch := map[string]bool{"new-1": true, "new-2": true}
	claimed := map[uuid.UUID]bool{}

	id1, ok1 := pickAdoptable(candidates, batch, claimed)
	if !ok1 {
		t.Fatal("first incoming row should adopt")
	}
	claimed[id1] = true

	id2, ok2 := pickAdoptable(candidates, batch, claimed)
	if !ok2 {
		t.Fatal("second incoming row should adopt the remaining legacy row")
	}
	if id1 == id2 {
		t.Error("both incoming rows adopted the same legacy row")
	}
	claimed[id2] = true

	if _, ok3 := pickAdoptable(candidates, batch, claimed); ok3 {
		t.Error("a third identical incoming row has nothing left to adopt and must insert")
	}
}

// Regression shape of the prod incident: 9 sells re-imported under a new hash
// scheme against 9 legacy sha256 rows — every incoming row adopts a distinct
// legacy row and nothing is left to duplicate.
func TestPickAdoptable_HashSchemeMigration(t *testing.T) {
	var legacy []db.ListSimilarTransactionsRow
	batch := map[string]bool{}
	for i := 0; i < 9; i++ {
		legacy = append(legacy, row(fmt.Sprintf("%064x", i))) // old content-sha256 style
		batch[fmt.Sprintf("ms-sell-acct-ORDER%d", i)] = true  // new structured style
	}

	claimed := map[uuid.UUID]bool{}
	for i := 0; i < 9; i++ {
		id, ok := pickAdoptable(legacy, batch, claimed)
		if !ok {
			t.Fatalf("incoming row %d found nothing to adopt", i)
		}
		if claimed[id] {
			t.Fatalf("incoming row %d re-adopted an already claimed row", i)
		}
		claimed[id] = true
	}
	if len(claimed) != 9 {
		t.Errorf("adopted %d distinct rows, want 9", len(claimed))
	}
	if _, ok := pickAdoptable(legacy, batch, claimed); ok {
		t.Error("all legacy rows claimed — a 10th row must insert, not adopt")
	}
}
