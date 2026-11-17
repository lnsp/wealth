package handler

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/pquerna/otp/totp"
	"golang.org/x/crypto/bcrypt"

	"github.com/lnsp/wealth/internal/auth"
	db "github.com/lnsp/wealth/internal/database/generated"
)

type pendingTOTPEntry struct {
	secret    string
	expiresAt time.Time
}

type UsersHandler struct {
	queries     *db.Queries
	auth        *auth.Auth
	cryptoKey   []byte // AES-256 key for TOTP secret encryption
	totpMu      sync.Mutex
	pendingTOTP map[uuid.UUID]pendingTOTPEntry
}

func NewUsersHandler(q *db.Queries, a *auth.Auth, sessionSecret string) *UsersHandler {
	return &UsersHandler{
		queries:     q,
		auth:        a,
		cryptoKey:   auth.DeriveKey(sessionSecret),
		pendingTOTP: make(map[uuid.UUID]pendingTOTPEntry),
	}
}

func (h *UsersHandler) callerUserID(r *http.Request) string {
	if id := UserIDFromContext(r.Context()); id != "" {
		return id
	}
	if h.auth != nil {
		return h.auth.UserIDFromRequest(r)
	}
	return ""
}

// requireAdmin checks if the requesting user has an active admin role.
// Returns true if authorized, false if denied (and writes error response).
func (h *UsersHandler) requireAdmin(w http.ResponseWriter, r *http.Request) bool {
	if h.auth == nil {
		return true // auth disabled
	}
	userID := h.callerUserID(r)
	if userID == "" {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return false
	}
	uid, err := uuid.Parse(userID)
	if err != nil {
		writeError(w, http.StatusForbidden, "invalid session")
		return false
	}
	user, err := h.queries.GetUserByID(r.Context(), uid)
	if err != nil || !user.IsActive {
		writeError(w, http.StatusForbidden, "user not found or inactive")
		return false
	}
	if user.Role != "admin" {
		writeError(w, http.StatusForbidden, "admin access required")
		return false
	}
	return true
}

func (h *UsersHandler) HandleListUsers(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdmin(w, r) {
		return
	}
	users, err := h.queries.ListUsers(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list users: "+err.Error())
		return
	}
	type userWithTOTP struct {
		ID          string `json:"id"`
		Username    string `json:"username"`
		Role        string `json:"role"`
		IsActive    bool   `json:"is_active"`
		CreatedAt   string `json:"created_at"`
		TOTPEnabled bool   `json:"totp_enabled"`
	}
	var result []userWithTOTP
	for _, u := range users {
		ut := userWithTOTP{
			ID: u.ID.String(), Username: u.Username, Role: u.Role,
			IsActive: u.IsActive, CreatedAt: u.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		}
		if full, err := h.queries.GetUserByID(r.Context(), u.ID); err == nil {
			ut.TOTPEnabled = full.TOTPEnabled
		}
		result = append(result, ut)
	}
	if result == nil {
		result = []userWithTOTP{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": result})
}

func (h *UsersHandler) HandleCreateUser(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdmin(w, r) {
		return
	}
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Role     string `json:"role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	if req.Username == "" || req.Password == "" {
		writeError(w, http.StatusBadRequest, "username and password are required")
		return
	}
	if req.Role == "" {
		req.Role = "member"
	}
	if req.Role != "admin" && req.Role != "member" {
		writeError(w, http.StatusBadRequest, "role must be admin or member")
		return
	}
	if len(req.Password) < 8 {
		writeError(w, http.StatusBadRequest, "password must be at least 8 characters")
		return
	}
	if len([]byte(req.Password)) > 72 {
		writeError(w, http.StatusBadRequest, "password must be at most 72 bytes")
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "hash password: "+err.Error())
		return
	}

	user, err := h.queries.CreateUser(r.Context(), db.CreateUserParams{
		Username: req.Username, PasswordHash: string(hash), Role: req.Role,
	})
	if err != nil {
		writeError(w, http.StatusConflict, "username already exists")
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"id":       user.ID.String(),
		"username": user.Username,
		"role":     user.Role,
	})
}

func (h *UsersHandler) hasOtherActiveAdmin(r *http.Request, excludeID uuid.UUID) (bool, error) {
	users, err := h.queries.ListUsers(r.Context())
	if err != nil {
		return false, err
	}
	for _, u := range users {
		if u.ID != excludeID && u.IsActive && u.Role == "admin" {
			return true, nil
		}
	}
	return false, nil
}

func (h *UsersHandler) HandleDeleteUser(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdmin(w, r) {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid user id")
		return
	}
	if callerID := h.callerUserID(r); callerID != "" && callerID == id.String() {
		writeError(w, http.StatusBadRequest, "cannot delete your own user account")
		return
	}
	target, err := h.queries.GetUserByID(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "user not found")
		return
	}
	if target.Role == "admin" && target.IsActive {
		otherAdmin, err := h.hasOtherActiveAdmin(r, id)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "check admins: "+err.Error())
			return
		}
		if !otherAdmin {
			writeError(w, http.StatusBadRequest, "cannot delete the last active admin")
			return
		}
	}
	if err := h.queries.DeleteUser(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, "delete user: "+err.Error())
		return
	}
	h.totpMu.Lock()
	delete(h.pendingTOTP, id)
	h.totpMu.Unlock()
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *UsersHandler) HandleToggleUser(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdmin(w, r) {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid user id")
		return
	}
	user, err := h.queries.GetUserByID(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "user not found")
		return
	}
	if user.IsActive {
		if callerID := h.callerUserID(r); callerID != "" && callerID == id.String() {
			writeError(w, http.StatusBadRequest, "cannot deactivate your own user account")
			return
		}
		if user.Role == "admin" {
			otherAdmin, err := h.hasOtherActiveAdmin(r, id)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "check admins: "+err.Error())
				return
			}
			if !otherAdmin {
				writeError(w, http.StatusBadRequest, "cannot deactivate the last active admin")
				return
			}
		}
	}
	if err := h.queries.UpdateUserActive(r.Context(), db.UpdateUserActiveParams{ID: id, IsActive: !user.IsActive}); err != nil {
		writeError(w, http.StatusInternalServerError, "toggle user: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"is_active": !user.IsActive})
}

// requireSelfOrAdmin checks if the requesting user is either the target user or an active admin.
func (h *UsersHandler) requireSelfOrAdmin(w http.ResponseWriter, r *http.Request, targetID uuid.UUID) (*db.User, bool) {
	if h.auth == nil {
		return nil, true
	}
	userID := h.callerUserID(r)
	if userID == "" {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return nil, false
	}
	uid, err := uuid.Parse(userID)
	if err != nil {
		writeError(w, http.StatusForbidden, "invalid session")
		return nil, false
	}
	caller, err := h.queries.GetUserByID(r.Context(), uid)
	if err != nil || !caller.IsActive {
		writeError(w, http.StatusForbidden, "invalid or inactive session")
		return nil, false
	}
	if uid == targetID || caller.Role == "admin" {
		return &caller, true
	}
	writeError(w, http.StatusForbidden, "can only manage your own 2FA or require admin")
	return nil, false
}

// HandleSetupTOTP generates a pending TOTP secret in memory and returns the provisioning URI.
// The database secret is only updated once HandleVerifyTOTP confirms a valid 6-digit code.
func (h *UsersHandler) HandleSetupTOTP(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid user id")
		return
	}
	if _, ok := h.requireSelfOrAdmin(w, r, id); !ok {
		return
	}
	user, err := h.queries.GetUserByID(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "user not found")
		return
	}
	if user.TOTPEnabled {
		writeError(w, http.StatusConflict, "2FA is already enabled — disable it first")
		return
	}

	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      "Wealth",
		AccountName: user.Username,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "generate TOTP: "+err.Error())
		return
	}

	now := time.Now()
	h.totpMu.Lock()
	for uid, entry := range h.pendingTOTP {
		if now.After(entry.expiresAt) {
			delete(h.pendingTOTP, uid)
		}
	}
	h.pendingTOTP[id] = pendingTOTPEntry{
		secret:    key.Secret(),
		expiresAt: now.Add(10 * time.Minute),
	}
	h.totpMu.Unlock()

	writeJSON(w, http.StatusOK, map[string]any{
		"secret": key.Secret(),
		"url":    key.URL(),
	})
}

// HandleVerifyTOTP verifies a TOTP code and atomically stores + enables 2FA for the user.
func (h *UsersHandler) HandleVerifyTOTP(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid user id")
		return
	}
	if _, ok := h.requireSelfOrAdmin(w, r, id); !ok {
		return
	}

	var body struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	user, err := h.queries.GetUserByID(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "user not found")
		return
	}
	if user.TOTPEnabled {
		h.totpMu.Lock()
		delete(h.pendingTOTP, id)
		h.totpMu.Unlock()
		writeError(w, http.StatusConflict, "2FA is already enabled — disable it first")
		return
	}

	now := time.Now()
	h.totpMu.Lock()
	pending, hasPending := h.pendingTOTP[id]
	if hasPending && now.After(pending.expiresAt) {
		delete(h.pendingTOTP, id)
		hasPending = false
	}
	h.totpMu.Unlock()

	var secret string
	if hasPending {
		secret = pending.secret
	} else if !user.TOTPEnabled && user.TOTPSecret.Valid && user.TOTPSecret.String != "" {
		secret, err = auth.DecryptSecret(user.TOTPSecret.String, h.cryptoKey)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "decrypt TOTP secret: "+err.Error())
			return
		}
	} else {
		writeError(w, http.StatusBadRequest, "TOTP not set up — call setup first")
		return
	}

	if !totp.Validate(strings.TrimSpace(body.Code), secret) {
		writeError(w, http.StatusUnauthorized, "invalid TOTP code")
		return
	}

	encrypted, err := auth.EncryptSecret(secret, h.cryptoKey)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "encrypt TOTP secret: "+err.Error())
		return
	}

	if err := withTx(r.Context(), h.queries, func(qtx *db.Queries) error {
		if err := qtx.SetUserTOTPSecret(r.Context(), db.SetUserTOTPSecretParams{
			ID: id, TOTPSecret: pgtype.Text{String: encrypted, Valid: true},
		}); err != nil {
			return err
		}
		return qtx.EnableUserTOTP(r.Context(), id)
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "enable TOTP: "+err.Error())
		return
	}

	h.totpMu.Lock()
	delete(h.pendingTOTP, id)
	h.totpMu.Unlock()

	writeJSON(w, http.StatusOK, map[string]string{"status": "totp_enabled"})
}

// HandleDisableTOTP disables 2FA for a user after verifying a TOTP code or caller password.
func (h *UsersHandler) HandleDisableTOTP(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid user id")
		return
	}
	caller, ok := h.requireSelfOrAdmin(w, r, id)
	if !ok {
		return
	}

	var body struct {
		Code     string `json:"code"`
		Password string `json:"password"`
	}
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil && err != io.EOF {
			writeError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
	}

	target, err := h.queries.GetUserByID(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "user not found")
		return
	}

	if h.auth != nil && target.TOTPEnabled {
		verified := false
		if strings.TrimSpace(body.Code) != "" && target.TOTPSecret.Valid && target.TOTPSecret.String != "" {
			if secret, decErr := auth.DecryptSecret(target.TOTPSecret.String, h.cryptoKey); decErr == nil {
				verified = totp.Validate(strings.TrimSpace(body.Code), secret)
			}
		}
		if !verified && body.Password != "" && caller != nil {
			verified = bcrypt.CompareHashAndPassword([]byte(caller.PasswordHash), []byte(body.Password)) == nil
		}
		if !verified {
			writeError(w, http.StatusUnauthorized, "current password or TOTP code is required to disable 2FA")
			return
		}
	}

	if err := h.queries.DisableUserTOTP(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, "disable TOTP: "+err.Error())
		return
	}
	h.totpMu.Lock()
	delete(h.pendingTOTP, id)
	h.totpMu.Unlock()
	writeJSON(w, http.StatusOK, map[string]string{"status": "totp_disabled"})
}
