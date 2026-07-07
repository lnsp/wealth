package handler

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	db "github.com/lnsp/wealth/internal/database/generated"
)

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
	searchFilter := strings.ToLower(r.URL.Query().Get("search"))
	dateFrom := r.URL.Query().Get("from") // YYYY-MM-DD
	dateTo := r.URL.Query().Get("to")     // YYYY-MM-DD

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

	// If filters are active, fetch all and filter in memory
	if typeFilter != "" || searchFilter != "" || !fromDate.IsZero() || !toDate.IsZero() {
		allTxns, err := h.queries.ListTransactions(r.Context(), db.ListTransactionsParams{Limit: 10000, Offset: 0})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "list transactions: "+err.Error())
			return
		}
		allTxns = h.filterTxnsByUser(r.Context(), allTxns)

		var filtered []db.ListTransactionsRow
		for _, t := range allTxns {
			if typeFilter != "" && t.Type != typeFilter {
				continue
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
		allTxns, err := h.queries.ListTransactions(r.Context(), db.ListTransactionsParams{Limit: 10000, Offset: 0})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "list transactions: "+err.Error())
			return
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
