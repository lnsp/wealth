package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/lnsp/wealth/internal/cashflow"
	db "github.com/lnsp/wealth/internal/database/generated"
)

type CashflowHandler struct {
	queries *db.Queries
}

func NewCashflowHandler(q *db.Queries) *CashflowHandler {
	return &CashflowHandler{queries: q}
}

// monthlyBucket is one month × one category cell.
type monthlyBucket struct {
	Month    string  `json:"month"` // YYYY-MM
	Category string  `json:"category"`
	Bucket   string  `json:"bucket"`
	Amount   float64 `json:"amount"` // signed: income positive, spend negative
	Count    int     `json:"count"`
}

type monthlyTotals struct {
	Month    string  `json:"month"`
	Income   float64 `json:"income"`
	Fixed    float64 `json:"fixed"`
	Variable float64 `json:"variable"`
	Transfer float64 `json:"transfer"`
	Net      float64 `json:"net"` // Income - Fixed - Variable
}

type cashflowResponse struct {
	From     string          `json:"from"`
	To       string          `json:"to"`
	Months   []monthlyTotals `json:"months"`   // newest first
	Buckets  []monthlyBucket `json:"buckets"`  // detail per (month, category)
	Medians  medianSummary   `json:"medians"`  // for "Apply to Planning"
}

type medianSummary struct {
	MonthlyIncome   float64 `json:"monthly_income"`
	MonthlySpend    float64 `json:"monthly_spend"`    // Fixed + Variable
	MonthlySurplus  float64 `json:"monthly_surplus"`  // Income − Spend
	AnnualGrossIncome float64 `json:"annual_gross_income"` // sum of trailing 12 months income
}

// HandleCashflow returns aggregated monthly cashflow for checking/savings
// accounts over the requested window (default 12 months). The response is
// pre-computed in two shapes: a per-month totals series for the table, and
// (month × category) buckets for the donut breakdown.
func (h *CashflowHandler) HandleCashflow(w http.ResponseWriter, r *http.Request) {
	months := 12
	if m := r.URL.Query().Get("months"); m != "" {
		if n, err := strconv.Atoi(m); err == nil && n >= 1 && n <= 60 {
			months = n
		}
	}

	now := time.Now()
	to := time.Date(now.Year(), now.Month()+1, 0, 0, 0, 0, 0, time.UTC) // last day of current month
	from := time.Date(now.Year(), now.Month()-time.Month(months-1), 1, 0, 0, 0, 0, time.UTC)

	rows, err := h.queries.ListCashTransactions(r.Context(), db.ListCashTransactionsParams{
		Date:   from,
		Date_2: to,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list cash transactions: "+err.Error())
		return
	}

	// Aggregate by (month, category). Internal transfers and investment moves
	// are still surfaced (under their own bucket) so the user can see them,
	// but they don't roll up into Income / Spend.
	type key struct {
		month    string
		category cashflow.Category
	}
	agg := map[key]*monthlyBucket{}
	totals := map[string]*monthlyTotals{}

	for _, row := range rows {
		amount := numericToFloat(row.Amount)
		ref := ""
		if row.Reference.Valid {
			ref = row.Reference.String
		}
		cp := ""
		if row.Counterparty.Valid {
			cp = row.Counterparty.String
		}
		override := ""
		if row.Category.Valid {
			override = row.Category.String
		}
		cat := cashflow.ResolveCategory(override, row.Type, cp, ref)
		bucket := cashflow.BucketOf(cat)

		month := row.Date.Format("2006-01")
		k := key{month: month, category: cat}
		if agg[k] == nil {
			agg[k] = &monthlyBucket{Month: month, Category: string(cat), Bucket: string(bucket)}
		}
		agg[k].Amount += amount
		agg[k].Count++

		if totals[month] == nil {
			totals[month] = &monthlyTotals{Month: month}
		}
		t := totals[month]
		// Buckets use absolute magnitudes — income is naturally positive in
		// the source data, spend is negative, so we abs() before summing into
		// the spend buckets so the table reads "spent 1.234,56 €" cleanly.
		switch bucket {
		case cashflow.BucketIncome:
			t.Income += amount
		case cashflow.BucketFixed:
			t.Fixed += -amount
		case cashflow.BucketVariable:
			t.Variable += -amount
		case cashflow.BucketTransfer:
			t.Transfer += amount
		}
	}

	// Build month list, newest first, including months with zero activity.
	var monthsList []monthlyTotals
	cursor := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < months; i++ {
		mkey := cursor.Format("2006-01")
		if t, ok := totals[mkey]; ok {
			t.Net = t.Income - t.Fixed - t.Variable
			monthsList = append(monthsList, *t)
		} else {
			monthsList = append(monthsList, monthlyTotals{Month: mkey})
		}
		cursor = cursor.AddDate(0, -1, 0)
	}

	// Flatten bucket map to slice, newest first, then by category.
	buckets := make([]monthlyBucket, 0, len(agg))
	for _, b := range agg {
		// Buckets store the signed amount; convert spend buckets to absolute
		// for the frontend's donut math (it wants magnitudes).
		if b.Bucket == string(cashflow.BucketFixed) || b.Bucket == string(cashflow.BucketVariable) {
			b.Amount = -b.Amount
		}
		buckets = append(buckets, *b)
	}

	// Medians — trailing 12 months only, ignoring months with zero income
	// (which are usually months before the first salary was imported).
	medians := computeMedians(monthsList)

	writeJSON(w, http.StatusOK, cashflowResponse{
		From:    from.Format("2006-01-02"),
		To:      to.Format("2006-01-02"),
		Months:  monthsList,
		Buckets: buckets,
		Medians: medians,
	})
}

func computeMedians(months []monthlyTotals) medianSummary {
	var incomes, spends []float64
	for _, m := range months {
		if m.Income > 0 {
			incomes = append(incomes, m.Income)
			spends = append(spends, m.Fixed+m.Variable)
		}
	}
	med := func(xs []float64) float64 {
		if len(xs) == 0 {
			return 0
		}
		// shallow sort
		for i := 1; i < len(xs); i++ {
			for j := i; j > 0 && xs[j-1] > xs[j]; j-- {
				xs[j-1], xs[j] = xs[j], xs[j-1]
			}
		}
		mid := len(xs) / 2
		if len(xs)%2 == 0 {
			return (xs[mid-1] + xs[mid]) / 2
		}
		return xs[mid]
	}
	mi, ms := med(incomes), med(spends)
	var annual float64
	for _, v := range incomes {
		annual += v
	}
	return medianSummary{
		MonthlyIncome:     mi,
		MonthlySpend:      ms,
		MonthlySurplus:    mi - ms,
		AnnualGrossIncome: annual,
	}
}

// HandleUpdateCategory writes a user override into transactions.category.
// Empty string clears the override (heuristic takes back over).
func (h *CashflowHandler) HandleUpdateCategory(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid transaction id")
		return
	}

	var body struct {
		Category string `json:"category"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body: "+err.Error())
		return
	}

	// Validate: empty string is allowed (= clear override); otherwise must be
	// one of the known categories so the heuristic/override switch in the
	// classifier doesn't silently fall through.
	if body.Category != "" && !cashflow.IsKnown(cashflow.Category(body.Category)) {
		writeError(w, http.StatusBadRequest, "unknown category")
		return
	}

	if !h.userOwnsTransaction(r.Context(), id) {
		writeError(w, http.StatusForbidden, "not your transaction")
		return
	}

	err = h.queries.UpdateTransactionCategory(r.Context(), db.UpdateTransactionCategoryParams{
		ID:       id,
		Category: pgtype.Text{String: body.Category, Valid: body.Category != ""},
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "update category: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// userOwnsTransaction returns true when auth is disabled OR the transaction's
// account belongs to the current user.
func (h *CashflowHandler) userOwnsTransaction(ctx context.Context, txID uuid.UUID) bool {
	allowed := userAccountIDs(ctx, h.queries.DB())
	if allowed == nil {
		return true
	}
	var accountID uuid.UUID
	err := h.queries.DB().QueryRow(ctx, "SELECT account_id FROM transactions WHERE id = $1", txID).Scan(&accountID)
	if err != nil {
		return false
	}
	for _, id := range allowed {
		if id == accountID {
			return true
		}
	}
	return false
}
