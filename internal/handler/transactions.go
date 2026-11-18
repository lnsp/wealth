package handler

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lnsp/wealth/internal/cashflow"
	db "github.com/lnsp/wealth/internal/database/generated"
)

// resolveTxnCategoryBucket buckets a row the way the Cashflow page does
// (account-aware classification, then the sign rule for money in/out), so a
// drill-through from a Cashflow bucket lists exactly the rows behind it.
func resolveTxnCategoryBucket(env cashflow.Env, override, txType, cp, ref string, cashDelta float64) string {
	cat := cashflow.ResolveCategoryWith(env, override, txType, cp, ref)
	return string(cashflow.BucketOf(cashflow.ForCashDelta(cat, cashDelta, override != "")))
}

type TransactionsHandler struct {
	queries *db.Queries
}

func NewTransactionsHandler(q *db.Queries) *TransactionsHandler {
	return &TransactionsHandler{queries: q}
}

// filterTxnsByUser filters transactions to only those belonging to the user's accounts.
func (h *TransactionsHandler) filterTxnsByUser(ctx context.Context, txns []db.ListTransactionsRow) []db.ListTransactionsRow {
	allowedIDs := userAccountIDs(ctx, h.queries.DB())
	if allowedIDs == nil {
		return txns // no auth — return all
	}
	allowed := make(map[uuid.UUID]bool, len(allowedIDs))
	for _, id := range allowedIDs {
		allowed[id] = true
	}
	filtered := txns[:0]
	for _, t := range txns {
		if allowed[t.AccountID] {
			filtered = append(filtered, t)
		}
	}
	return filtered
}

func (h *TransactionsHandler) HandleList(w http.ResponseWriter, r *http.Request) {
	limit := int32(50)
	offset := int32(0)

	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 && n <= 500 {
			limit = int32(n)
		}
	}
	if o := r.URL.Query().Get("offset"); o != "" {
		if n, err := strconv.Atoi(o); err == nil && n >= 0 {
			offset = int32(n)
		}
	}

	// Filter parameters
	typeFilter := r.URL.Query().Get("type")
	accountFilter := r.URL.Query().Get("account_id")
	categoryFilter := r.URL.Query().Get("category")
	categoryGroup := r.URL.Query().Get("category_group")
	searchFilter := strings.ToLower(r.URL.Query().Get("search"))
	dateFrom := r.URL.Query().Get("from") // YYYY-MM-DD
	dateTo := r.URL.Query().Get("to")     // YYYY-MM-DD

	var accountUUID uuid.UUID
	hasAccountFilter := false
	if accountFilter != "" {
		if parsed, err := uuid.Parse(accountFilter); err == nil {
			accountUUID = parsed
			hasAccountFilter = true
		}
	}

	var fromDate, toDate time.Time
	if dateFrom != "" {
		if t, err := time.Parse("2006-01-02", dateFrom); err == nil {
			fromDate = t
		}
	}
	if dateTo != "" {
		if t, err := time.Parse("2006-01-02", dateTo); err == nil {
			toDate = t.Add(24*time.Hour - time.Nanosecond) // end of day
		}
	}

	// If filters are active, fetch all (untruncated!) and filter consistently before pagination
	if typeFilter != "" || hasAccountFilter || categoryFilter != "" || categoryGroup != "" || searchFilter != "" || !fromDate.IsZero() || !toDate.IsZero() {
		exportRows, err := h.queries.ListAllTransactionsForExport(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, "list transactions: "+err.Error())
			return
		}
		allTxns := make([]db.ListTransactionsRow, len(exportRows))
		for i, er := range exportRows {
			allTxns[i] = db.ListTransactionsRow(er)
		}
		allTxns = h.filterTxnsByUser(r.Context(), allTxns)

		var accountTypes map[uuid.UUID]string
		var ownIBANs []string
		if categoryGroup != "" {
			accounts, err := h.queries.ListAllAccounts(r.Context())
			if err != nil {
				writeError(w, http.StatusInternalServerError, "list accounts: "+err.Error())
				return
			}
			accountTypes = make(map[uuid.UUID]string, len(accounts))
			for _, a := range accounts {
				accountTypes[a.ID] = a.Type
				if a.IBAN.Valid && a.IBAN.String != "" {
					ownIBANs = append(ownIBANs, a.IBAN.String)
				}
			}
		}

		var filtered []db.ListTransactionsRow
		for _, t := range allTxns {
			if hasAccountFilter && t.AccountID != accountUUID {
				continue
			}
			if typeFilter != "" && t.Type != typeFilter {
				continue
			}
			if categoryFilter != "" && (!t.Category.Valid || t.Category.String != categoryFilter) {
				continue
			}
			if categoryGroup != "" {
				cp, ref, ov := "", "", ""
				if t.Counterparty.Valid {
					cp = t.Counterparty.String
				}
				if t.Reference.Valid {
					ref = t.Reference.String
				}
				if t.Category.Valid {
					ov = t.Category.String
				}
				env := cashflow.Env{AccountType: accountTypes[t.AccountID], OwnIBANs: ownIBANs}
				delta := txnSignedCashDelta(t.Type, t.SecurityISIN.Valid && t.SecurityISIN.String != "",
					numericToFloat(t.Amount), numericToFloat(t.Fee), numericToFloat(t.Tax))
				cat := resolveTxnCategoryBucket(env, ov, t.Type, cp, ref, delta)
				if cat != categoryGroup {
					continue
				}
			}
			if !fromDate.IsZero() && t.Date.Before(fromDate) {
				continue
			}
			if !toDate.IsZero() && t.Date.After(toDate) {
				continue
			}
			if searchFilter != "" {
				match := false
				if t.Counterparty.Valid && strings.Contains(strings.ToLower(t.Counterparty.String), searchFilter) {
					match = true
				}
				if t.Reference.Valid && strings.Contains(strings.ToLower(t.Reference.String), searchFilter) {
					match = true
				}
				if strings.Contains(strings.ToLower(t.AccountName), searchFilter) {
					match = true
				}
				if t.SecurityISIN.Valid && strings.Contains(strings.ToLower(t.SecurityISIN.String), searchFilter) {
					match = true
				}
				if !match {
					continue
				}
			}
			filtered = append(filtered, t)
		}

		total := int32(len(filtered))

		// Apply pagination
		start := offset
		if start > total {
			start = total
		}
		end := start + limit
		if end > total {
			end = total
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"transactions": filtered[start:end],
			"total":        total,
			"limit":        limit,
			"offset":       offset,
			"first_date":   earliestTxnDate(allTxns),
		})
		return
	}

	// No filters: use the standard paginated query
	// If auth enabled, filter by user accounts
	allowedIDs := userAccountIDs(r.Context(), h.queries.DB())
	if allowedIDs != nil {
		// Must fetch all and filter in memory for user isolation
		exportRows, err := h.queries.ListAllTransactionsForExport(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, "list transactions: "+err.Error())
			return
		}
		allTxns := make([]db.ListTransactionsRow, len(exportRows))
		for i, er := range exportRows {
			allTxns[i] = db.ListTransactionsRow(er)
		}
		allTxns = h.filterTxnsByUser(r.Context(), allTxns)
		total := int32(len(allTxns))
		start := offset
		if start > total { start = total }
		end := start + limit
		if end > total { end = total }
		writeJSON(w, http.StatusOK, map[string]any{
			"transactions": allTxns[start:end], "total": total, "limit": limit, "offset": offset,
			"first_date": earliestTxnDate(allTxns),
		})
		return
	}

	txns, err := h.queries.ListTransactions(r.Context(), db.ListTransactionsParams{Limit: limit, Offset: offset})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list transactions: "+err.Error())
		return
	}

	count, err := h.queries.CountTransactions(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "count transactions: "+err.Error())
		return
	}

	firstDate := ""
	if d, err := h.queries.GetFirstTransactionDate(r.Context()); err == nil {
		firstDate = d.Format("2006-01-02")
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"transactions": txns,
		"total":        count,
		"limit":        limit,
		"offset":       offset,
		"first_date":   firstDate,
	})
}

// earliestTxnDate is the date of the oldest row ("" when there is none), so
// the month picker can reach back to the first transaction whatever the
// current filters.
func earliestTxnDate(rows []db.ListTransactionsRow) string {
	var first time.Time
	for _, t := range rows {
		if first.IsZero() || t.Date.Before(first) {
			first = t.Date
		}
	}
	if first.IsZero() {
		return ""
	}
	return first.Format("2006-01-02")
}
