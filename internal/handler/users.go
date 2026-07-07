package handler

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/pquerna/otp/totp"
	"golang.org/x/crypto/bcrypt"

	"github.com/lnsp/wealth/internal/auth"
	db "github.com/lnsp/wealth/internal/database/generated"
)

type UsersHandler struct {
	queries    *db.Queries
	auth       *auth.Auth
	cryptoKey  []byte // AES-256 key for TOTP secret encryption
}

func NewUsersHandler(q *db.Queries, a *auth.Auth, sessionSecret string) *UsersHandler {
	return &UsersHandler{queries: q, auth: a, cryptoKey: auth.DeriveKey(sessionSecret)}
}

// requireAdmin checks if the requesting user has the admin role.
// Returns true if authorized, false if denied (and writes 403 response).
func (h *UsersHandler) requireAdmin(w http.ResponseWriter, r *http.Request) bool {
	if h.auth == nil {
		return true // auth disabled
	}
	userID := h.auth.UserIDFromRequest(r)
	if userID == "" {
		return true // no auth context (dev mode)
	}
	uid, err := uuid.Parse(userID)
	if err != nil {
		writeError(w, http.StatusForbidden, "invalid session")
		return false
	}
	user, err := h.queries.GetUserByID(r.Context(), uid)
	if err != nil {
		writeError(w, http.StatusForbidden, "user not found")
		return false
	}
	if user.Role != "admin" {
		writeError(w, http.StatusForbidden, "admin access required")
		return false
	}
	return true
}

func (h *UsersHandler) HandleListUsers(w http.ResponseWriter, r *http.Request) {
	users, err := h.queries.ListUsers(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list users: "+err.Error())
		return
	}
	// Enrich with TOTP status
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

func (h *UsersHandler) HandleDeleteUser(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdmin(w, r) {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid user id")
		return
	}
	if err := h.queries.DeleteUser(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, "delete user: "+err.Error())
		return
	}
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
	if err := h.queries.UpdateUserActive(r.Context(), db.UpdateUserActiveParams{ID: id, IsActive: !user.IsActive}); err != nil {
		writeError(w, http.StatusInternalServerError, "toggle user: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"is_active": !user.IsActive})
}

// requireSelfOrAdmin checks if the requesting user is either the target user or an admin.
func (h *UsersHandler) requireSelfOrAdmin(w http.ResponseWriter, r *http.Request, targetID uuid.UUID) bool {
	if h.auth == nil {
		return true
	}
	userID := h.auth.UserIDFromRequest(r)
	if userID == "" {
		return true // no auth context (dev mode)
	}
	if userID == targetID.String() {
		return true // acting on own account
	}
	uid, err := uuid.Parse(userID)
	if err != nil {
		writeError(w, http.StatusForbidden, "invalid session")
		return false
	}
	user, err := h.queries.GetUserByID(r.Context(), uid)
	if err != nil || user.Role != "admin" {
		writeError(w, http.StatusForbidden, "can only manage your own 2FA or require admin")
		return false
	}
	return true
}

// HandleSetupTOTP generates a TOTP secret and returns the provisioning URI.
func (h *UsersHandler) HandleSetupTOTP(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid user id")
		return
	}
	if !h.requireSelfOrAdmin(w, r, id) {
		return
	}
	user, err := h.queries.GetUserByID(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "user not found")
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

	// Encrypt the TOTP secret before storing
	encrypted, err := auth.EncryptSecret(key.Secret(), h.cryptoKey)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "encrypt TOTP secret: "+err.Error())
		return
	}
	if err := h.queries.SetUserTOTPSecret(r.Context(), db.SetUserTOTPSecretParams{
		ID: id, TOTPSecret: pgtype.Text{String: encrypted, Valid: true},
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "store TOTP secret: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"secret": key.Secret(),
		"url":    key.URL(),
	})
}

// HandleVerifyTOTP verifies a TOTP code and enables 2FA for the user.
func (h *UsersHandler) HandleVerifyTOTP(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid user id")
		return
	}
	if !h.requireSelfOrAdmin(w, r, id) {
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
	if !user.TOTPSecret.Valid || user.TOTPSecret.String == "" {
		writeError(w, http.StatusBadRequest, "TOTP not set up — call setup first")
		return
	}

	// Decrypt the stored TOTP secret
	secret, err := auth.DecryptSecret(user.TOTPSecret.String, h.cryptoKey)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "decrypt TOTP secret: "+err.Error())
		return
	}

	if !totp.Validate(body.Code, secret) {
		writeError(w, http.StatusUnauthorized, "invalid TOTP code")
		return
	}

	if err := h.queries.EnableUserTOTP(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, "enable TOTP: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "totp_enabled"})
}

// HandleDisableTOTP disables 2FA for a user.
func (h *UsersHandler) HandleDisableTOTP(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid user id")
		return
	}
	if !h.requireSelfOrAdmin(w, r, id) {
		return
	}
	if err := h.queries.DisableUserTOTP(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, "disable TOTP: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "totp_disabled"})
}
