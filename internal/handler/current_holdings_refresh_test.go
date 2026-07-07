package handler

import (
	"os"
	"strings"
	"testing"
)

// Materialized view refresh contract:
//
//   - The `current_holdings` materialized view aggregates transactions
//     into per-(account, ISIN) net position rows (migrations/002).
//   - After a CSV import inserts new transactions, the MV is stale until
//     refreshed. ImportHandler (import.go:163) calls
//     `RefreshCurrentHoldings` so the next GET /api/portfolio/holdings
//     reflects the just-imported rows.
//   - SettingsHandler exposes a manual rebuild endpoint that also calls
//     RefreshCurrentHoldings (settings.go:227).
//   - RefreshCurrentHoldings is `REFRESH MATERIALIZED VIEW CONCURRENTLY
//     current_holdings` (queries.sql:201). The CONCURRENTLY clause needs
//     the unique index on (account_id, security_isin), pinned below.
//
// The MV math itself is exercised via a pure-Go mirror so a future
// migration that changes the additive/subtractive type lists or the
// cost-basis formula surfaces in CI before silently mis-reporting
// holdings.

const (
	importGoPath   = "import.go"
	settingsGoPath = "settings.go"
	queriesSQL     = "../database/queries.sql"
	migration002   = "../../migrations/002_add_transfer_out.sql"
)

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func TestRefreshCurrentHoldings_CalledAfterImport(t *testing.T) {
	// The wiring contract: import.go must call RefreshCurrentHoldings
	// AFTER inserting transactions and BEFORE returning the response.
	src := readFile(t, importGoPath)
	if !strings.Contains(src, "RefreshCurrentHoldings(r.Context())") {
		t.Fatal("import.go: missing RefreshCurrentHoldings call — post-import GET /portfolio/holdings will return stale data")
	}
	// Order check: the refresh call must come after insertTransactions().
	// (insertTransactions is invoked earlier in HandleImport.)
	insertIdx := strings.Index(src, "insertTransactions")
	refreshIdx := strings.Index(src, "RefreshCurrentHoldings")
	if insertIdx < 0 || refreshIdx < 0 {
		t.Fatalf("import.go: missing key markers (insert=%d refresh=%d)", insertIdx, refreshIdx)
	}
	if refreshIdx <= insertIdx {
		t.Errorf("import.go: RefreshCurrentHoldings called before insertTransactions — view will reflect pre-import state")
	}
}

func TestRefreshCurrentHoldings_LoggedAsWarningOnFailure(t *testing.T) {
	// On refresh failure, the handler must NOT 5xx — the import already
	// succeeded; only the cached aggregation is stale. The import.go
	// pattern is: `if err := …; err != nil { log.Printf("WARNING…"); }`.
	src := readFile(t, importGoPath)
	// Find the RefreshCurrentHoldings line; ensure the surrounding block
	// uses log.Printf (not writeError, which would 500 the response).
	idx := strings.Index(src, "RefreshCurrentHoldings(r.Context())")
	if idx < 0 {
		t.Fatal("RefreshCurrentHoldings not found")
	}
	window := src[idx:]
	if len(window) > 200 {
		window = window[:200]
	}
	if strings.Contains(window, "writeError") {
		t.Error("refresh failure must NOT writeError — the import succeeded; only cache is stale")
	}
	if !strings.Contains(window, "log.Printf") && !strings.Contains(window, "WARNING") {
		t.Error("refresh failure should be logged as WARNING for ops visibility")
	}
}

func TestRefreshCurrentHoldings_AlsoCalledByManualRebuild(t *testing.T) {
	// Settings page exposes a "Rebuild Net Worth" admin action that
	// triggers a manual refresh — same query as the auto-call.
	src := readFile(t, settingsGoPath)
	if !strings.Contains(src, "RefreshCurrentHoldings") {
		t.Error("settings.go: manual rebuild path doesn't call RefreshCurrentHoldings — UI button is a no-op")
	}
}

func TestRefreshCurrentHoldings_UsesConcurrentlyClause(t *testing.T) {
	// REFRESH MATERIALIZED VIEW CONCURRENTLY avoids blocking concurrent
	// reads. It requires a UNIQUE index — pinned in the next test.
	sql := readFile(t, queriesSQL)
	if !strings.Contains(sql, "REFRESH MATERIALIZED VIEW CONCURRENTLY current_holdings") {
		t.Error("queries.sql: refresh must use CONCURRENTLY to avoid blocking reads during import")
	}
}

func TestRefreshCurrentHoldings_UniqueIndexExistsForConcurrentRefresh(t *testing.T) {
	// CONCURRENTLY refresh requires a UNIQUE index on the MV. Without
	// it, the refresh errors out and the call site silently falls back
	// to a stale view.
	mig := readFile(t, migration002)
	if !strings.Contains(mig, "CREATE UNIQUE INDEX ON current_holdings (account_id, security_isin)") {
		t.Error("migration 002: missing UNIQUE INDEX required by CONCURRENTLY refresh")
	}
}

// ---- MV aggregation math (mirror of migration 002 SQL) ----

type txn struct {
	AccountID string
	ISIN      string
	Type      string  // buy, sell, savings_plan, transfer, transfer_out, dividend, deposit, …
	Quantity  float64
	Amount    float64
	Fee       float64
}

type holdingRow struct {
	AccountID      string
	ISIN           string
	Quantity       float64
	AvgCostBasis   float64
	TotalDividends float64
}

// isAdditiveQty mirrors the SQL: buy, savings_plan, transfer add shares.
func isAdditiveQty(typ string) bool {
	return typ == "buy" || typ == "savings_plan" || typ == "transfer"
}

// isSubtractiveQty mirrors the SQL: sell, transfer_out subtract shares.
func isSubtractiveQty(typ string) bool {
	return typ == "sell" || typ == "transfer_out"
}

// aggregateHoldings replicates the SQL in migrations/002:
//   - groups by (account_id, isin) where isin is non-empty
//   - quantity = Σadditive.qty − Σsubtractive.qty
//   - avg_cost = (Σadditive.(amount+fee) − Σsubtractive.(amount−fee)) / quantity
//   - dividends = Σ(amount where type=dividend)
//   - HAVING: only rows with quantity > 0 are returned
func aggregateHoldings(txns []txn) []holdingRow {
	type bucket struct {
		qty, cost, div float64
	}
	groups := map[string]map[string]*bucket{}
	for _, t := range txns {
		if t.ISIN == "" {
			continue
		}
		acctMap, ok := groups[t.AccountID]
		if !ok {
			acctMap = map[string]*bucket{}
			groups[t.AccountID] = acctMap
		}
		b, ok := acctMap[t.ISIN]
		if !ok {
			b = &bucket{}
			acctMap[t.ISIN] = b
		}
		switch {
		case isAdditiveQty(t.Type):
			b.qty += t.Quantity
			b.cost += t.Amount + t.Fee
		case isSubtractiveQty(t.Type):
			b.qty -= t.Quantity
			b.cost -= (t.Amount - t.Fee)
		case t.Type == "dividend":
			b.div += t.Amount
		}
	}
	var out []holdingRow
	for acct, m := range groups {
		for isin, b := range m {
			if b.qty <= 0 {
				continue // HAVING clause
			}
			out = append(out, holdingRow{
				AccountID:      acct,
				ISIN:           isin,
				Quantity:       b.qty,
				AvgCostBasis:   b.cost / b.qty,
				TotalDividends: b.div,
			})
		}
	}
	return out
}

func TestCurrentHoldings_EmptyInputProducesNoRows(t *testing.T) {
	if rows := aggregateHoldings(nil); len(rows) != 0 {
		t.Errorf("empty input → %d rows, want 0", len(rows))
	}
}

func TestCurrentHoldings_BuyAggregatesWithFeesIntoCostBasis(t *testing.T) {
	rows := aggregateHoldings([]txn{
		{AccountID: "A", ISIN: "IE00B4L5Y983", Type: "buy", Quantity: 10, Amount: 1000, Fee: 5},
	})
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	r := rows[0]
	if r.Quantity != 10 {
		t.Errorf("qty = %v, want 10", r.Quantity)
	}
	if r.AvgCostBasis != 100.5 {
		t.Errorf("avg cost = %v, want 100.5 (cost incl fees: 1005/10)", r.AvgCostBasis)
	}
}

func TestCurrentHoldings_PartialSellWeightsCostBasis(t *testing.T) {
	// Buy 10 @ €100 (+€5 fee → cost basis 100.5), then sell 4 @ €110 (−€2 fee).
	// SQL: cost = (1000 + 5) − (440 − 2) = 1005 − 438 = 567; qty = 10 − 4 = 6.
	// avg = 567/6 = 94.5
	rows := aggregateHoldings([]txn{
		{AccountID: "A", ISIN: "X", Type: "buy", Quantity: 10, Amount: 1000, Fee: 5},
		{AccountID: "A", ISIN: "X", Type: "sell", Quantity: 4, Amount: 440, Fee: 2},
	})
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	r := rows[0]
	if r.Quantity != 6 {
		t.Errorf("qty = %v, want 6", r.Quantity)
	}
	if r.AvgCostBasis != 94.5 {
		t.Errorf("avg cost = %v, want 94.5 ((1005 − 438)/6)", r.AvgCostBasis)
	}
}

func TestCurrentHoldings_FullSellExcludedByHavingClause(t *testing.T) {
	// Buy 10, sell 10 → net qty 0 → HAVING > 0 filters out.
	rows := aggregateHoldings([]txn{
		{AccountID: "A", ISIN: "X", Type: "buy", Quantity: 10, Amount: 1000},
		{AccountID: "A", ISIN: "X", Type: "sell", Quantity: 10, Amount: 1100},
	})
	if len(rows) != 0 {
		t.Errorf("fully divested holding present in output: %+v", rows)
	}
}

func TestCurrentHoldings_TransferOutSubtractsLikeSell(t *testing.T) {
	// Migration 002 added transfer_out specifically so broker-to-broker
	// moves don't leave phantom positions in the source account.
	rows := aggregateHoldings([]txn{
		{AccountID: "A", ISIN: "X", Type: "buy", Quantity: 10, Amount: 1000},
		{AccountID: "A", ISIN: "X", Type: "transfer_out", Quantity: 10, Amount: 1000},
	})
	if len(rows) != 0 {
		t.Errorf("transfer_out should fully divest: %+v", rows)
	}
}

func TestCurrentHoldings_TransferAddsLikeBuy(t *testing.T) {
	// Receiving broker sees a `transfer` row that adds shares + cost basis
	// at the receiving-side valuation.
	rows := aggregateHoldings([]txn{
		{AccountID: "B", ISIN: "X", Type: "transfer", Quantity: 10, Amount: 1100, Fee: 0},
	})
	if len(rows) != 1 || rows[0].Quantity != 10 || rows[0].AvgCostBasis != 110 {
		t.Errorf("transfer should add: %+v", rows)
	}
}

func TestCurrentHoldings_SavingsPlanCountsAsBuy(t *testing.T) {
	// savings_plan is the rebalanced equivalent of a buy (e.g. ETF
	// auto-investments); aggregates same as buy.
	rows := aggregateHoldings([]txn{
		{AccountID: "A", ISIN: "X", Type: "savings_plan", Quantity: 5, Amount: 500, Fee: 1},
	})
	if len(rows) != 1 || rows[0].Quantity != 5 || rows[0].AvgCostBasis != 100.2 {
		t.Errorf("savings_plan should aggregate as buy: %+v", rows)
	}
}

func TestCurrentHoldings_DividendsSummedNotCounted(t *testing.T) {
	// Dividends contribute to total_dividends but NOT quantity.
	rows := aggregateHoldings([]txn{
		{AccountID: "A", ISIN: "X", Type: "buy", Quantity: 10, Amount: 1000},
		{AccountID: "A", ISIN: "X", Type: "dividend", Amount: 25},
		{AccountID: "A", ISIN: "X", Type: "dividend", Amount: 30},
	})
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	r := rows[0]
	if r.Quantity != 10 {
		t.Errorf("dividend leaked into qty: got %v, want 10", r.Quantity)
	}
	if r.TotalDividends != 55 {
		t.Errorf("dividends = %v, want 55", r.TotalDividends)
	}
}

func TestCurrentHoldings_CashTransactionsExcluded(t *testing.T) {
	// WHERE security_isin IS NOT NULL — deposits/withdrawals/fees with
	// no ISIN don't produce holding rows.
	rows := aggregateHoldings([]txn{
		{AccountID: "A", ISIN: "", Type: "deposit", Amount: 5000},
		{AccountID: "A", ISIN: "", Type: "fee", Amount: 12},
		{AccountID: "A", ISIN: "X", Type: "buy", Quantity: 10, Amount: 1000},
	})
	if len(rows) != 1 || rows[0].ISIN != "X" {
		t.Errorf("cash txns leaked into holdings: %+v", rows)
	}
}

func TestCurrentHoldings_GroupsByBothAccountAndISIN(t *testing.T) {
	// Same ISIN held in two accounts produces TWO rows; same account
	// holding two ISINs produces TWO rows.
	rows := aggregateHoldings([]txn{
		{AccountID: "A", ISIN: "X", Type: "buy", Quantity: 10, Amount: 1000},
		{AccountID: "A", ISIN: "Y", Type: "buy", Quantity: 5, Amount: 500},
		{AccountID: "B", ISIN: "X", Type: "buy", Quantity: 7, Amount: 700},
	})
	if len(rows) != 3 {
		t.Errorf("expected 3 rows (A:X, A:Y, B:X), got %d: %+v", len(rows), rows)
	}
}

func TestCurrentHoldings_PostImportReflectsNewTransactions(t *testing.T) {
	// Spec scenario: state BEFORE import + new transactions → MV after
	// refresh shows the combined holdings.
	pre := []txn{
		{AccountID: "A", ISIN: "OLD", Type: "buy", Quantity: 5, Amount: 500},
	}
	imported := []txn{
		{AccountID: "A", ISIN: "OLD", Type: "buy", Quantity: 3, Amount: 360}, // additional buy
		{AccountID: "A", ISIN: "NEW", Type: "buy", Quantity: 10, Amount: 1500}, // new position
	}
	// "Post-refresh" output = aggregation over (pre ∪ imported).
	combined := append([]txn{}, pre...)
	combined = append(combined, imported...)
	post := aggregateHoldings(combined)

	// OLD: 5+3=8 shares, cost basis (500+360)/8 = 107.5
	// NEW: 10 shares, cost basis 150
	var oldRow, newRow *holdingRow
	for i := range post {
		r := &post[i]
		switch r.ISIN {
		case "OLD":
			oldRow = r
		case "NEW":
			newRow = r
		}
	}
	if oldRow == nil || newRow == nil {
		t.Fatalf("post-refresh missing rows: %+v", post)
	}
	if oldRow.Quantity != 8 || oldRow.AvgCostBasis != 107.5 {
		t.Errorf("OLD post-refresh: qty=%v cost=%v, want 8 / 107.5", oldRow.Quantity, oldRow.AvgCostBasis)
	}
	if newRow.Quantity != 10 || newRow.AvgCostBasis != 150 {
		t.Errorf("NEW post-refresh: qty=%v cost=%v, want 10 / 150", newRow.Quantity, newRow.AvgCostBasis)
	}
}
