package handler

import (
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/lnsp/wealth/internal/database/generated"
)

// filterTxns mirrors HandleList's inline filter (type / search / date range).
// dateTo input is the YYYY-MM-DD upper bound; the handler internally bumps
// it to end-of-day before comparing, so the same adjustment is applied here.
func filterTxns(txns []db.ListTransactionsRow, typeFilter, search string, fromDate, toDate time.Time) []db.ListTransactionsRow {
	var endOfTo time.Time
	if !toDate.IsZero() {
		endOfTo = toDate.Add(24*time.Hour - time.Nanosecond)
	}
	var out []db.ListTransactionsRow
	for _, t := range txns {
		if typeFilter != "" && t.Type != typeFilter {
			continue
		}
		if !fromDate.IsZero() && t.Date.Before(fromDate) {
			continue
		}
		if !endOfTo.IsZero() && t.Date.After(endOfTo) {
			continue
		}
		if search != "" {
			match := false
			s := strings.ToLower(search)
			if t.Counterparty.Valid && strings.Contains(strings.ToLower(t.Counterparty.String), s) {
				match = true
			}
			if t.Reference.Valid && strings.Contains(strings.ToLower(t.Reference.String), s) {
				match = true
			}
			if strings.Contains(strings.ToLower(t.AccountName), s) {
				match = true
			}
			if t.SecurityISIN.Valid && strings.Contains(strings.ToLower(t.SecurityISIN.String), s) {
				match = true
			}
			if !match {
				continue
			}
		}
		out = append(out, t)
	}
	return out
}

func mkTxnRow(date string, txType string) db.ListTransactionsRow {
	d, _ := time.Parse("2006-01-02", date)
	return db.ListTransactionsRow{Date: d, Type: txType}
}

func mkTxnWithCounterparty(date, txType, cp string) db.ListTransactionsRow {
	r := mkTxnRow(date, txType)
	r.Counterparty = pgtype.Text{String: cp, Valid: true}
	return r
}

func TestTransactionsFilter_TypeIsExactMatch(t *testing.T) {
	txns := []db.ListTransactionsRow{
		mkTxnRow("2026-01-01", "deposit"),
		mkTxnRow("2026-01-02", "withdrawal"),
		mkTxnRow("2026-01-03", "buy"),
		mkTxnRow("2026-01-04", "savings_plan"), // looks like "savings"+"_plan", must NOT match "savings"
	}
	got := filterTxns(txns, "deposit", "", time.Time{}, time.Time{})
	if len(got) != 1 || got[0].Type != "deposit" {
		t.Errorf("type=deposit returned %d rows, want 1 (deposit only)", len(got))
	}
	// Confirm non-existent type filter returns nothing (no fuzzy/prefix).
	got = filterTxns(txns, "savings", "", time.Time{}, time.Time{})
	if len(got) != 0 {
		t.Errorf("type=savings (no exact match) returned %d rows, want 0 (prefix matching would wrongly hit savings_plan)", len(got))
	}
}

func TestTransactionsFilter_DateLowerBoundInclusive(t *testing.T) {
	txns := []db.ListTransactionsRow{
		mkTxnRow("2026-01-14", "deposit"), // day before from
		mkTxnRow("2026-01-15", "deposit"), // exact from
		mkTxnRow("2026-01-16", "deposit"), // day after from
	}
	from, _ := time.Parse("2006-01-02", "2026-01-15")
	got := filterTxns(txns, "", "", from, time.Time{})
	if len(got) != 2 {
		t.Fatalf("from=2026-01-15 returned %d rows, want 2 (inclusive of 01-15)", len(got))
	}
	if !got[0].Date.Equal(time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("first row date = %v, want 2026-01-15 (from-date row must be included)", got[0].Date)
	}
}

func TestTransactionsFilter_DateUpperBoundInclusive(t *testing.T) {
	txns := []db.ListTransactionsRow{
		mkTxnRow("2026-01-14", "deposit"), // day before to
		mkTxnRow("2026-01-15", "deposit"), // exact to
		mkTxnRow("2026-01-16", "deposit"), // day after to
	}
	to, _ := time.Parse("2006-01-02", "2026-01-15")
	got := filterTxns(txns, "", "", time.Time{}, to)
	if len(got) != 2 {
		t.Fatalf("to=2026-01-15 returned %d rows, want 2 (inclusive of 01-15)", len(got))
	}
	// Last kept row must be 2026-01-15, not 2026-01-16.
	if !got[len(got)-1].Date.Equal(time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("last row date = %v, want 2026-01-15 (to-date row must be included)", got[len(got)-1].Date)
	}
}

func TestTransactionsFilter_DateRangeBothBoundsInclusive(t *testing.T) {
	txns := []db.ListTransactionsRow{
		mkTxnRow("2026-01-14", "deposit"),
		mkTxnRow("2026-01-15", "deposit"), // from
		mkTxnRow("2026-01-20", "deposit"), // middle
		mkTxnRow("2026-01-25", "deposit"), // to
		mkTxnRow("2026-01-26", "deposit"),
	}
	from, _ := time.Parse("2006-01-02", "2026-01-15")
	to, _ := time.Parse("2006-01-02", "2026-01-25")
	got := filterTxns(txns, "", "", from, to)
	if len(got) != 3 {
		t.Errorf("range [01-15, 01-25] returned %d rows, want 3 (both bounds inclusive)", len(got))
	}
}

func TestTransactionsFilter_DateOnSameDayWithTimeComponent(t *testing.T) {
	// Txns stored with a time component (e.g. "2026-01-15T14:30:00") must
	// still be matched by from=2026-01-15 / to=2026-01-15. The handler
	// expands to=end-of-day for this reason.
	d, _ := time.Parse(time.RFC3339, "2026-01-15T14:30:00Z")
	txns := []db.ListTransactionsRow{{Date: d, Type: "deposit"}}
	from, _ := time.Parse("2006-01-02", "2026-01-15")
	to, _ := time.Parse("2006-01-02", "2026-01-15")
	got := filterTxns(txns, "", "", from, to)
	if len(got) != 1 {
		t.Errorf("same-day [from, to] with afternoon txn returned %d, want 1", len(got))
	}
}

func TestTransactionsFilter_TypeAndDateCombine(t *testing.T) {
	// Both filters apply (AND semantics), not OR.
	txns := []db.ListTransactionsRow{
		mkTxnRow("2026-01-15", "deposit"),
		mkTxnRow("2026-01-15", "withdrawal"),
		mkTxnRow("2026-02-01", "deposit"),
	}
	from, _ := time.Parse("2006-01-02", "2026-01-01")
	to, _ := time.Parse("2006-01-02", "2026-01-31")
	got := filterTxns(txns, "deposit", "", from, to)
	if len(got) != 1 {
		t.Errorf("type=deposit + range Jan 2026 returned %d, want 1 (only Jan deposit)", len(got))
	}
	if got[0].Type != "deposit" || got[0].Date.Month() != time.January {
		t.Errorf("filtered row = %+v, want deposit in January", got[0])
	}
}

func TestTransactionsFilter_SearchCaseInsensitive(t *testing.T) {
	txns := []db.ListTransactionsRow{
		mkTxnWithCounterparty("2026-01-01", "deposit", "Amazon EU SARL"),
		mkTxnWithCounterparty("2026-01-02", "deposit", "Rewe Markt"),
	}
	got := filterTxns(txns, "", "amazon", time.Time{}, time.Time{})
	if len(got) != 1 {
		t.Errorf("search=amazon returned %d, want 1 (case-insensitive match on Counterparty)", len(got))
	}
}
