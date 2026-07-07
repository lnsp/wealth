package handler

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/lnsp/wealth/internal/database/generated"
)

type AlertsHandler struct {
	queries *db.Queries
}

func NewAlertsHandler(q *db.Queries) *AlertsHandler {
	return &AlertsHandler{queries: q}
}

func (h *AlertsHandler) HandleListAlerts(w http.ResponseWriter, r *http.Request) {
	alerts, err := h.queries.ListPriceAlerts(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list alerts: "+err.Error())
		return
	}

	type alertResponse struct {
		ID           string  `json:"id"`
		AlertType    string  `json:"alert_type"`
		SecurityISIN string  `json:"security_isin,omitempty"`
		SecurityName string  `json:"security_name,omitempty"`
		Threshold    float64 `json:"threshold"`
		IsActive     bool    `json:"is_active"`
		CreatedAt    string  `json:"created_at"`
	}

	var items []alertResponse
	for _, a := range alerts {
		isin := ""
		if a.SecurityISIN.Valid {
			isin = a.SecurityISIN.String
		}
		name := ""
		if a.SecurityName.Valid {
			name = a.SecurityName.String
		}
		items = append(items, alertResponse{
			ID:           a.ID.String(),
			AlertType:    a.AlertType,
			SecurityISIN: isin,
			SecurityName: name,
			Threshold:    numericToFloat(a.Threshold),
			IsActive:     a.IsActive,
			CreatedAt:    a.CreatedAt.Format("2006-01-02T15:04:05Z"),
		})
	}
	if items == nil {
		items = []alertResponse{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"alerts": items})
}

func (h *AlertsHandler) HandleCreateAlert(w http.ResponseWriter, r *http.Request) {
	var req struct {
		AlertType    string  `json:"alert_type"`
		SecurityISIN string  `json:"security_isin"`
		Threshold    float64 `json:"threshold"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	validTypes := map[string]bool{"price_above": true, "price_below": true, "daily_change": true, "portfolio_milestone": true}
	if !validTypes[req.AlertType] {
		writeError(w, http.StatusBadRequest, "invalid alert_type")
		return
	}
	if req.Threshold <= 0 {
		writeError(w, http.StatusBadRequest, "threshold must be positive")
		return
	}

	isin := pgtype.Text{}
	if req.SecurityISIN != "" {
		isin = pgtype.Text{String: req.SecurityISIN, Valid: true}
	}
	var threshold pgtype.Numeric
	threshold.Scan(fmt.Sprintf("%.2f", req.Threshold))

	alert, err := h.queries.CreatePriceAlert(r.Context(), db.CreatePriceAlertParams{
		AlertType: req.AlertType, SecurityISIN: isin, Threshold: threshold,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "create alert: "+err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"id":         alert.ID.String(),
		"alert_type": alert.AlertType,
	})
}

func (h *AlertsHandler) HandleDeleteAlert(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid alert id")
		return
	}
	if err := h.queries.DeletePriceAlert(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, "delete alert: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *AlertsHandler) HandleToggleAlert(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid alert id")
		return
	}
	if err := h.queries.TogglePriceAlert(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, "toggle alert: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *AlertsHandler) HandleListNotifications(w http.ResponseWriter, r *http.Request) {
	notifications, err := h.queries.ListNotifications(r.Context(), 50)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list notifications: "+err.Error())
		return
	}

	unread, _ := h.queries.CountUnreadNotifications(r.Context())

	type notifResponse struct {
		ID          string  `json:"id"`
		AlertType   string  `json:"alert_type"`
		Message     string  `json:"message"`
		Value       float64 `json:"value"`
		IsRead      bool    `json:"is_read"`
		TriggeredAt string  `json:"triggered_at"`
	}

	var items []notifResponse
	for _, n := range notifications {
		items = append(items, notifResponse{
			ID:          n.ID.String(),
			AlertType:   n.AlertType,
			Message:     n.Message,
			Value:       numericToFloat(n.Value),
			IsRead:      n.IsRead,
			TriggeredAt: n.TriggeredAt.Format("2006-01-02T15:04:05Z"),
		})
	}
	if items == nil {
		items = []notifResponse{}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"notifications": items,
		"unread_count":  unread,
	})
}

func (h *AlertsHandler) HandleMarkRead(w http.ResponseWriter, r *http.Request) {
	if err := h.queries.MarkNotificationsRead(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "mark read: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
