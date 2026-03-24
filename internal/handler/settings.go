package handler

import (
	"encoding/json"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/lnsp/wealth/internal/database/generated"
	"github.com/lnsp/wealth/data"
)

type SettingsHandler struct {
	queries *db.Queries
}

func NewSettingsHandler(q *db.Queries) *SettingsHandler {
	return &SettingsHandler{queries: q}
}

type CreateAccountRequest struct {
	Name        string `json:"name"`
	Institution string `json:"institution"`
	Type        string `json:"type"`
	Currency    string `json:"currency"`
	IBAN        string `json:"iban"`
}

func (h *SettingsHandler) HandleCreateAccount(w http.ResponseWriter, r *http.Request) {
	var req CreateAccountRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	if req.Name == "" || req.Institution == "" || req.Type == "" {
		writeError(w, http.StatusBadRequest, "name, institution, and type are required")
		return
	}
	if req.Currency == "" {
		req.Currency = "EUR"
	}

	iban := pgtype.Text{}
	if req.IBAN != "" {
		iban = pgtype.Text{String: req.IBAN, Valid: true}
	}

	acc, err := h.queries.CreateAccount(r.Context(), req.Name, req.Institution, req.Type, req.Currency, iban)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "create account: "+err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, acc)
}

type UpdateAccountRequest struct {
	Name     string `json:"name"`
	IBAN     string `json:"iban"`
	IsActive *bool  `json:"is_active"`
}

func (h *SettingsHandler) HandleUpdateAccount(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid account ID")
		return
	}

	var req UpdateAccountRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	// Get existing account
	acc, err := h.queries.GetAccount(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "account not found")
		return
	}

	name := acc.Name
	if req.Name != "" {
		name = req.Name
	}
	iban := acc.IBAN
	if req.IBAN != "" {
		iban = pgtype.Text{String: req.IBAN, Valid: true}
	}
	isActive := acc.IsActive
	if req.IsActive != nil {
		isActive = *req.IsActive
	}

	if err := h.queries.UpdateAccount(r.Context(), id, name, iban, isActive); err != nil {
		writeError(w, http.StatusInternalServerError, "update account: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})
}

func (h *SettingsHandler) HandleListSecurities(w http.ResponseWriter, r *http.Request) {
	securities, err := h.queries.ListSecurities(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list securities: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"securities": securities})
}

type UpdateSecuritySymbolRequest struct {
	Symbol string `json:"symbol"`
}

func (h *SettingsHandler) HandleUpdateSecuritySymbol(w http.ResponseWriter, r *http.Request) {
	isin := r.PathValue("isin")
	if isin == "" {
		writeError(w, http.StatusBadRequest, "isin is required")
		return
	}

	var req UpdateSecuritySymbolRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	symbol := pgtype.Text{}
	if req.Symbol != "" {
		symbol = pgtype.Text{String: req.Symbol, Valid: true}
	}

	if err := h.queries.UpdateSecuritySymbol(r.Context(), isin, symbol); err != nil {
		writeError(w, http.StatusInternalServerError, "update symbol: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})
}

func (h *SettingsHandler) HandleHoldingsTemplate(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/csv")
	w.Header().Set("Content-Disposition", "attachment; filename=holdings_template.csv")
	w.Write(data.HoldingsTemplateCSV)
}
