package handler

import (
	"net/http"
	"strconv"

	db "github.com/lnsp/wealth/internal/database/generated"
)

type TransactionsHandler struct {
	queries *db.Queries
}

func NewTransactionsHandler(q *db.Queries) *TransactionsHandler {
	return &TransactionsHandler{queries: q}
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

	txns, err := h.queries.ListTransactions(r.Context(), limit, offset)
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
