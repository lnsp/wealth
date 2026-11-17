package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/lnsp/wealth/data"
	db "github.com/lnsp/wealth/internal/database/generated"
)

type SettingsHandler struct {
	queries *db.Queries
}

func NewSettingsHandler(q *db.Queries) *SettingsHandler {
	return &SettingsHandler{queries: q}
}

func isValidCurrencyCode(c string) bool {
	if len(c) != 3 {
		return false
	}
	for i := 0; i < 3; i++ {
		if c[i] < 'A' || c[i] > 'Z' {
			return false
		}
	}
	return true
}

type CreateAccountRequest struct {
	Name               string   `json:"name"`
	Institution        string   `json:"institution"`
	Type               string   `json:"type"`
	Currency           string   `json:"currency"`
	IBAN               string   `json:"iban"`
	TaxTreatment       string   `json:"tax_treatment"`
	EmployerMatchPct   *float64 `json:"employer_match_pct"`
	ImportSecurityISIN string   `json:"import_security_isin"`
}

func (h *SettingsHandler) HandleCreateAccount(w http.ResponseWriter, r *http.Request) {
	var req CreateAccountRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	req.Name = strings.TrimSpace(req.Name)
	req.Institution = strings.TrimSpace(req.Institution)
	if req.Name == "" || req.Institution == "" || req.Type == "" {
		writeError(w, http.StatusBadRequest, "name, institution, and type are required")
		return
	}

	// Validate account type enum
	validTypes := map[string]bool{
		"checking": true, "savings": true, "brokerage": true, "crypto": true, "credit": true,
		"real_estate": true, "pension": true, "precious_metals": true, "liability": true,
	}
	if !validTypes[req.Type] {
		writeError(w, http.StatusBadRequest, "invalid account type")
		return
	}

	if req.Currency == "" {
		req.Currency = "EUR"
	}
	req.Currency = strings.ToUpper(strings.TrimSpace(req.Currency))
	if !isValidCurrencyCode(req.Currency) {
		writeError(w, http.StatusBadRequest, "invalid currency code: must be 3 letters (e.g., EUR, USD)")
		return
	}

	iban := pgtype.Text{}
	if req.IBAN != "" {
		iban = pgtype.Text{String: req.IBAN, Valid: true}
	}

	taxTreatment := req.TaxTreatment
	if taxTreatment == "" {
		taxTreatment = "taxable"
	}
	validTax := map[string]bool{"taxable": true, "bav": true, "riester": true, "rurup": true, "savings": true}
	if !validTax[taxTreatment] {
		writeError(w, http.StatusBadRequest, "invalid tax_treatment")
		return
	}

	var employerMatch pgtype.Numeric
	if req.EmployerMatchPct != nil {
		if math.IsNaN(*req.EmployerMatchPct) || math.IsInf(*req.EmployerMatchPct, 0) || *req.EmployerMatchPct < 0 {
			writeError(w, http.StatusBadRequest, "invalid employer_match_pct")
			return
		}
		employerMatch.Scan(fmt.Sprintf("%f", *req.EmployerMatchPct))
	}

	importISIN := pgtype.Text{}
	if req.ImportSecurityISIN != "" {
		importISIN = pgtype.Text{String: req.ImportSecurityISIN, Valid: true}
	}

	acc, err := h.queries.CreateAccount(r.Context(), db.CreateAccountParams{
		Name: req.Name, Institution: req.Institution, Type: req.Type, Currency: req.Currency,
		IBAN: iban, TaxTreatment: taxTreatment, EmployerMatchPct: employerMatch, ImportSecurityISIN: importISIN,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "create account: "+err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, acc)
}

func (h *SettingsHandler) HandleListAllAccounts(w http.ResponseWriter, r *http.Request) {
	accounts, err := h.queries.ListAllAccounts(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list accounts: "+err.Error())
		return
	}
	if allowedIDs := userAccountIDs(r.Context(), h.queries.DB()); allowedIDs != nil {
		allowed := make(map[uuid.UUID]bool, len(allowedIDs))
		for _, id := range allowedIDs {
			allowed[id] = true
		}
		filtered := accounts[:0]
		for _, a := range accounts {
			if allowed[a.ID] {
				filtered = append(filtered, a)
			}
		}
		accounts = filtered
	}
	if accounts == nil {
		accounts = []db.Account{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"accounts": accounts})
}

type UpdateAccountRequest struct {
	Name               string   `json:"name"`
	IBAN               string   `json:"iban"`
	IsActive           *bool    `json:"is_active"`
	TaxTreatment       string   `json:"tax_treatment"`
	EmployerMatchPct   *float64 `json:"employer_match_pct"`
	ImportSecurityISIN *string  `json:"import_security_isin"`
	Currency           string   `json:"currency"`
}

func (h *SettingsHandler) HandleUpdateAccount(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
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

	acc, err := h.queries.GetAccount(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "account not found")
		return
	}

	name := acc.Name
	if strings.TrimSpace(req.Name) != "" {
		name = strings.TrimSpace(req.Name)
	}
	iban := acc.IBAN
	if req.IBAN != "" {
		iban = pgtype.Text{String: req.IBAN, Valid: true}
	}
	isActive := acc.IsActive
	if req.IsActive != nil {
		isActive = *req.IsActive
	}
	taxTreatment := acc.TaxTreatment
	if req.TaxTreatment != "" {
		validTax := map[string]bool{"taxable": true, "bav": true, "riester": true, "rurup": true, "savings": true}
		if !validTax[req.TaxTreatment] {
			writeError(w, http.StatusBadRequest, "invalid tax_treatment")
			return
		}
		taxTreatment = req.TaxTreatment
	}
	employerMatch := acc.EmployerMatchPct
	if req.EmployerMatchPct != nil {
		if math.IsNaN(*req.EmployerMatchPct) || math.IsInf(*req.EmployerMatchPct, 0) || *req.EmployerMatchPct < 0 {
			writeError(w, http.StatusBadRequest, "invalid employer_match_pct")
			return
		}
		employerMatch = pgtype.Numeric{}
		employerMatch.Scan(fmt.Sprintf("%f", *req.EmployerMatchPct))
	}
	importISIN := acc.ImportSecurityISIN
	if req.ImportSecurityISIN != nil {
		if *req.ImportSecurityISIN == "" {
			importISIN = pgtype.Text{}
		} else {
			importISIN = pgtype.Text{String: *req.ImportSecurityISIN, Valid: true}
		}
	}
	currency := acc.Currency
	if req.Currency != "" {
		upper := strings.ToUpper(strings.TrimSpace(req.Currency))
		if !isValidCurrencyCode(upper) {
			writeError(w, http.StatusBadRequest, "invalid currency code: must be 3 letters (e.g., EUR, USD)")
			return
		}
		currency = upper
	}

	if err := h.queries.UpdateAccount(r.Context(), db.UpdateAccountParams{
		ID: id, Name: name, IBAN: iban, IsActive: isActive, TaxTreatment: taxTreatment, EmployerMatchPct: employerMatch, ImportSecurityISIN: importISIN, Currency: currency,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "update account: "+err.Error())
		return
	}

	if isActive != acc.IsActive {
		if err := h.queries.RefreshCurrentHoldings(r.Context()); err != nil {
			log.Printf("WARNING: refresh holdings on account toggle: %v", err)
		}
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})
}

func (h *SettingsHandler) HandleDeleteAccount(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid account ID")
		return
	}

	// Delete all related data for this account inside a single transaction.
	if err := withTx(r.Context(), h.queries, func(qtx *db.Queries) error {
		if err := qtx.DeleteAccountRSUVests(r.Context(), id); err != nil {
			return fmt.Errorf("delete rsu vests: %w", err)
		}
		if err := qtx.DeleteAccountTransactions(r.Context(), id); err != nil {
			return fmt.Errorf("delete transactions: %w", err)
		}
		if err := qtx.DeleteAccountImportHistory(r.Context(), id); err != nil {
			return fmt.Errorf("delete import history: %w", err)
		}
		if err := qtx.DeleteAccount(r.Context(), id); err != nil {
			return fmt.Errorf("delete account: %w", err)
		}
		return nil
	}); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Refresh materialized view outside transaction
	if err := h.queries.RefreshCurrentHoldings(r.Context()); err != nil {
		log.Printf("WARNING: refresh holdings after account delete: %v", err)
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
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
	isin := chi.URLParam(r, "isin")
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

	if err := h.queries.UpdateSecuritySymbol(r.Context(), db.UpdateSecuritySymbolParams{ISIN: isin, Symbol: symbol}); err != nil {
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

func (h *SettingsHandler) HandleExportTransactions(w http.ResponseWriter, r *http.Request) {
	txns, err := h.queries.ListAllTransactionsForExport(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list transactions: "+err.Error())
		return
	}

	filename := fmt.Sprintf("transactions-%s.csv", time.Now().UTC().Format("2006-01-02"))
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename="+filename)

	// Write BOM for Excel compatibility
	w.Write([]byte{0xEF, 0xBB, 0xBF})
	// Header
	w.Write([]byte("date;type;account;counterparty;security_isin;quantity;price;amount;fee;tax;currency;reference\n"))

	for _, t := range txns {
		isin := ""
		if t.SecurityISIN.Valid {
			isin = t.SecurityISIN.String
		}
		counterparty := ""
		if t.Counterparty.Valid {
			counterparty = t.Counterparty.String
		}
		ref := ""
		if t.Reference.Valid {
			ref = t.Reference.String
		}

		qty := numericToFloat(t.Quantity)
		price := numericToFloat(t.Price)
		amt := numericToFloat(t.Amount)
		fee := numericToFloat(t.Fee)
		tax := numericToFloat(t.Tax)

		line := fmt.Sprintf("%s;%s;%s;%s;%s;%.8f;%.8f;%.4f;%.4f;%.4f;%s;%s\n",
			t.Date.Format("2006-01-02"), t.Type, t.AccountName,
			counterparty, isin, qty, price, amt, fee, tax,
			t.Currency, ref)
		w.Write([]byte(line))
	}
}

func numericToFloat(n pgtype.Numeric) float64 {
	if !n.Valid {
		return 0
	}
	f, _ := n.Float64Value()
	if math.IsNaN(f.Float64) || math.IsInf(f.Float64, 0) {
		return 0
	}
	return f.Float64
}

// HandleListGoals returns all financial goals.
func (h *SettingsHandler) HandleListGoals(w http.ResponseWriter, r *http.Request) {
	goals, err := h.queries.ListFinancialGoals(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list goals: "+err.Error())
		return
	}

	type goalResponse struct {
		ID                  string  `json:"id"`
		Name                string  `json:"name"`
		TargetAmount        float64 `json:"target_amount"`
		TargetDate          string  `json:"target_date"`
		MonthlyContribution float64 `json:"monthly_contribution"`
		AssumedReturnPct    float64 `json:"assumed_return_pct"`
	}

	var items []goalResponse
	for _, g := range goals {
		items = append(items, goalResponse{
			ID:                  g.ID.String(),
			Name:                g.Name,
			TargetAmount:        numericToFloat(g.TargetAmount),
			TargetDate:          g.TargetDate.Format("2006-01-02"),
			MonthlyContribution: numericToFloat(g.MonthlyContribution),
			AssumedReturnPct:    numericToFloat(g.AssumedReturnPct),
		})
	}
	if items == nil {
		items = []goalResponse{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"goals": items})
}

// HandleCreateGoal creates a new financial goal.
func (h *SettingsHandler) HandleCreateGoal(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name                string  `json:"name"`
		TargetAmount        float64 `json:"target_amount"`
		TargetDate          string  `json:"target_date"`
		MonthlyContribution float64 `json:"monthly_contribution"`
		AssumedReturnPct    float64 `json:"assumed_return_pct"`
		FundingAccountID    string  `json:"funding_account_id"`
		Priority            int     `json:"priority"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.Name == "" || req.TargetAmount <= 0 || math.IsNaN(req.TargetAmount) || math.IsInf(req.TargetAmount, 0) || req.TargetDate == "" {
		writeError(w, http.StatusBadRequest, "name, target_amount, and target_date are required")
		return
	}
	if math.IsNaN(req.MonthlyContribution) || math.IsInf(req.MonthlyContribution, 0) || req.MonthlyContribution < 0 ||
		math.IsNaN(req.AssumedReturnPct) || math.IsInf(req.AssumedReturnPct, 0) {
		writeError(w, http.StatusBadRequest, "invalid contribution or return rate")
		return
	}
	targetDate, err := time.Parse("2006-01-02", req.TargetDate)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid target_date format (expected YYYY-MM-DD)")
		return
	}
	if req.AssumedReturnPct == 0 {
		req.AssumedReturnPct = 7.0 // default 7% annual return
	}

	var amount, contrib, returnPct pgtype.Numeric
	amount.Scan(fmt.Sprintf("%.2f", req.TargetAmount))
	contrib.Scan(fmt.Sprintf("%.2f", req.MonthlyContribution))
	returnPct.Scan(fmt.Sprintf("%.2f", req.AssumedReturnPct))

	var fundingID *uuid.UUID
	if req.FundingAccountID != "" {
		parsed, err := uuid.Parse(req.FundingAccountID)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid funding_account_id")
			return
		}
		fundingID = &parsed
	}
	priority := int32(req.Priority)
	if priority == 0 {
		priority = 50
	}

	goal, err := h.queries.CreateFinancialGoal(r.Context(), db.CreateFinancialGoalParams{
		Name: req.Name, TargetAmount: amount, TargetDate: targetDate,
		MonthlyContribution: contrib, AssumedReturnPct: returnPct,
		FundingAccountID: fundingID, Priority: priority,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "create goal: "+err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"id":   goal.ID.String(),
		"name": goal.Name,
	})
}

// HandleDeleteGoal deletes a financial goal.
func (h *SettingsHandler) HandleDeleteGoal(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid goal id")
		return
	}
	if err := h.queries.DeleteFinancialGoal(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, "delete goal: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// Notification Channels

var secretChannelKeys = map[string]bool{
	"password":   true,
	"api_token":  true,
	"token":      true,
	"secret":     true,
	"auth_token": true,
}

func maskChannelConfig(raw []byte) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage("{}")
	}
	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return json.RawMessage("{}")
	}
	for k, v := range cfg {
		if secretChannelKeys[strings.ToLower(k)] {
			if s, ok := v.(string); ok && s != "" {
				cfg[k] = "••••••••"
			}
		}
	}
	out, err := json.Marshal(cfg)
	if err != nil {
		return json.RawMessage("{}")
	}
	return out
}

func redactChannel(ch db.NotificationChannel) db.NotificationChannel {
	ch.Config = maskChannelConfig(ch.Config)
	return ch
}

func isDisallowedOutboundIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
		return true
	}
	if ip4 := ip.To4(); ip4 != nil {
		// Block "this network" 0.0.0.0/8 and RFC 6598 CGNAT 100.64.0.0/10
		if ip4[0] == 0 || (ip4[0] == 100 && ip4[1] >= 64 && ip4[1] <= 127) {
			return true
		}
	}
	return false
}

func validateOutboundEndpointURL(raw string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return errors.New("url must be a valid http:// or https:// URL")
	}
	host := strings.ToLower(u.Hostname())
	if host == "" || host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") || strings.HasSuffix(host, ".internal") {
		return errors.New("url host must not target localhost or internal addresses")
	}
	if ip := net.ParseIP(host); ip != nil && isDisallowedOutboundIP(ip) {
		return errors.New("url host must not target private or loopback IP addresses")
	}
	return nil
}

func validateChannelConfig(chType string, cfg map[string]string) error {
	switch chType {
	case "webhook":
		if err := validateOutboundEndpointURL(cfg["url"]); err != nil {
			return err
		}
	case "ntfy":
		if err := validateOutboundEndpointURL(cfg["url"]); err != nil {
			return err
		}
		topic := strings.TrimSpace(cfg["topic"])
		if topic == "" || strings.ContainsAny(topic, "/\\\r\n") || strings.Contains(topic, "..") {
			return errors.New("ntfy topic is required and must not contain path separators")
		}
	case "email":
		host := strings.TrimSpace(cfg["smtp_host"])
		from := strings.TrimSpace(cfg["from"])
		to := strings.TrimSpace(cfg["to"])
		if host == "" || from == "" || to == "" {
			return errors.New("email channel requires smtp_host, from, and to")
		}
		for _, field := range []string{host, cfg["smtp_port"], from, to, cfg["username"]} {
			if strings.ContainsAny(field, "\r\n") {
				return errors.New("email fields must not contain newline characters")
			}
		}
	case "pushover":
		if strings.TrimSpace(cfg["user_key"]) == "" || strings.TrimSpace(cfg["api_token"]) == "" {
			return errors.New("pushover channel requires user_key and api_token")
		}
	}
	return nil
}

func (h *SettingsHandler) HandleListChannels(w http.ResponseWriter, r *http.Request) {
	channels, err := h.queries.ListNotificationChannels(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list channels")
		return
	}
	redacted := make([]db.NotificationChannel, len(channels))
	for i, ch := range channels {
		redacted[i] = redactChannel(ch)
	}
	writeJSON(w, http.StatusOK, map[string]any{"channels": redacted})
}

func (h *SettingsHandler) HandleCreateChannel(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Type            string          `json:"type"`
		Name            string          `json:"name"`
		Config          json.RawMessage `json:"config"`
		Enabled         bool            `json:"enabled"`
		ChannelFor      string          `json:"channel_for"`
		DigestFrequency string          `json:"digest_frequency"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	validTypes := map[string]bool{"email": true, "ntfy": true, "pushover": true, "webhook": true}
	if !validTypes[req.Type] {
		writeError(w, http.StatusBadRequest, "invalid channel type")
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		req.Name = req.Type
	}
	if req.ChannelFor == "" {
		req.ChannelFor = "all"
	}
	validFor := map[string]bool{"all": true, "alerts": true, "digest": true}
	if !validFor[req.ChannelFor] {
		writeError(w, http.StatusBadRequest, "invalid channel_for: must be all, alerts, or digest")
		return
	}
	if req.Config == nil {
		req.Config = json.RawMessage("{}")
	}
	if len(req.Config) > 4096 {
		writeError(w, http.StatusBadRequest, "config too large (max 4KB)")
		return
	}
	var configCheck map[string]string
	if err := json.Unmarshal(req.Config, &configCheck); err != nil {
		writeError(w, http.StatusBadRequest, "config must be a JSON object of string values")
		return
	}
	if err := validateChannelConfig(req.Type, configCheck); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	digestFreq := req.DigestFrequency
	if digestFreq == "" {
		digestFreq = "monthly"
	}
	validFreq := map[string]bool{"weekly": true, "monthly": true, "quarterly": true, "never": true}
	if !validFreq[digestFreq] {
		writeError(w, http.StatusBadRequest, "invalid digest_frequency")
		return
	}
	ch, err := h.queries.CreateNotificationChannel(r.Context(), db.CreateNotificationChannelParams{
		Type: req.Type, Name: req.Name, Config: req.Config, Enabled: req.Enabled,
		ChannelFor: req.ChannelFor, DigestFrequency: digestFreq,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "create channel")
		return
	}
	writeJSON(w, http.StatusCreated, redactChannel(ch))
}

func (h *SettingsHandler) HandleDeleteChannel(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid channel ID")
		return
	}
	if err := h.queries.DeleteNotificationChannel(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, "delete channel")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}
