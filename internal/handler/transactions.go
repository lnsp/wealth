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

func resolveTxnCategoryBucket(override, txType, cp, ref string) string {
	return string(cashflow.BucketOf(cashflow.ResolveCategory(override, txType, cp, ref)))
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
				cat := resolveTxnCategoryBucket(ov, t.Type, cp, ref)
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

	writeJSON(w, http.StatusOK, map[string]any{
		"transactions": txns,
		"total":        count,
		"limit":        limit,
		"offset":       offset,
	})
}
