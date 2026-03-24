package handler

import (
	"net/http"
	"strconv"

	db "github.com/lnsp/wealth/internal/database/generated"
)

type PortfolioHandler struct {
	queries *db.Queries
}

func NewPortfolioHandler(q *db.Queries) *PortfolioHandler {
	return &PortfolioHandler{queries: q}
}

func (h *PortfolioHandler) HandleHoldings(w http.ResponseWriter, r *http.Request) {
	holdings, err := h.queries.ListCurrentHoldings(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list holdings: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"holdings": holdings})
}

func (h *PortfolioHandler) HandleNetWorth(w http.ResponseWriter, r *http.Request) {
	limit := int32(365)
	if l := r.URL.Query().Get("days"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 {
			limit = int32(n)
		}
	}

	snapshots, err := h.queries.ListNetWorthSnapshots(r.Context(), limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list snapshots: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"snapshots": snapshots})
}

func (h *PortfolioHandler) HandleAccounts(w http.ResponseWriter, r *http.Request) {
	accounts, err := h.queries.ListAccounts(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list accounts: "+err.Error())
		return
	}

	// Compute cash balance for each account
	type accountWithBalance struct {
		db.Account
		Balance float64 `json:"balance"`
	}
	var result []accountWithBalance
	for _, acc := range accounts {
		bal, err := h.queries.GetCashBalance(r.Context(), acc.ID)
		if err != nil {
			bal.Valid = false
		}
		balFloat := 0.0
		if bal.Valid {
			f, _ := bal.Float64Value()
			balFloat = f.Float64
		}
		result = append(result, accountWithBalance{Account: acc, Balance: balFloat})
	}

	writeJSON(w, http.StatusOK, map[string]any{"accounts": result})
}
