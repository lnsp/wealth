package handler

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/google/uuid"

	"github.com/lnsp/wealth/internal/auth"
	db "github.com/lnsp/wealth/internal/database/generated"
)

const (
	webauthnCeremonyCookie = "webauthn_ceremony"
	webauthnCeremonyTTL    = 5 * time.Minute
	maxWebAuthnSessions    = 1024
)

type webauthnSessionEntry struct {
	data      *webauthn.SessionData
	userID    string // empty for discoverable login ceremonies
	expiresAt time.Time
}

// WebAuthnHandler manages passkey registration and authentication.
type WebAuthnHandler struct {
	queries *db.Queries
	wan     *webauthn.WebAuthn
	auth    *auth.Auth

	mu       sync.Mutex
	sessions map[string]webauthnSessionEntry
}

// webauthnUser adapts our user model to the webauthn.User interface.
type webauthnUser struct {
	id          uuid.UUID
	name        string
	credentials []webauthn.Credential
}

func (u *webauthnUser) WebAuthnID() []byte                         { return u.id[:] }
func (u *webauthnUser) WebAuthnName() string                       { return u.name }
func (u *webauthnUser) WebAuthnDisplayName() string                { return u.name }
func (u *webauthnUser) WebAuthnIcon() string                       { return "" }
func (u *webauthnUser) WebAuthnCredentials() []webauthn.Credential { return u.credentials }

// NewWebAuthnHandler creates a handler with the given origin.
func NewWebAuthnHandler(q *db.Queries, a *auth.Auth, rpID, rpOrigin string) (*WebAuthnHandler, error) {
	wan, err := webauthn.New(&webauthn.Config{
		RPDisplayName: "Wealth",
		RPID:          rpID,
		RPOrigins:     []string{rpOrigin},
	})
	if err != nil {
		return nil, err
	}
	return &WebAuthnHandler{
		queries:  q,
		wan:      wan,
		auth:     a,
		sessions: make(map[string]webauthnSessionEntry),
	}, nil
}

func (h *WebAuthnHandler) putSession(key, userID string, data *webauthn.SessionData) {
	now := time.Now()
	h.mu.Lock()
	defer h.mu.Unlock()

	for k, entry := range h.sessions {
		if now.After(entry.expiresAt) {
			delete(h.sessions, k)
		}
	}
	if len(h.sessions) >= maxWebAuthnSessions {
		var oldestKey string
		var oldestExp time.Time
		for k, entry := range h.sessions {
			if oldestKey == "" || entry.expiresAt.Before(oldestExp) {
				oldestKey = k
				oldestExp = entry.expiresAt
			}
		}
		if oldestKey != "" {
			delete(h.sessions, oldestKey)
		}
	}

	h.sessions[key] = webauthnSessionEntry{
		data:      data,
		userID:    userID,
		expiresAt: now.Add(webauthnCeremonyTTL),
	}
}

func (h *WebAuthnHandler) takeSession(key string) (webauthnSessionEntry, bool) {
	now := time.Now()
	h.mu.Lock()
	defer h.mu.Unlock()

	entry, ok := h.sessions[key]
	if !ok {
		return webauthnSessionEntry{}, false
	}
	delete(h.sessions, key)
	if now.After(entry.expiresAt) || entry.data == nil {
		return webauthnSessionEntry{}, false
	}
	return entry, true
}

func setCeremonyCookie(w http.ResponseWriter, r *http.Request, ceremonyID string) {
	secure := r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
	http.SetCookie(w, &http.Cookie{
		Name:     webauthnCeremonyCookie,
		Value:    ceremonyID,
		Path:     "/api/auth/webauthn",
		MaxAge:   int(webauthnCeremonyTTL.Seconds()),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   secure,
	})
}

func clearCeremonyCookie(w http.ResponseWriter, r *http.Request) {
	secure := r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
	http.SetCookie(w, &http.Cookie{
		Name:     webauthnCeremonyCookie,
		Value:    "",
		Path:     "/api/auth/webauthn",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   secure,
	})
}

// HandleBeginRegistration starts the passkey registration ceremony.
func (h *WebAuthnHandler) HandleBeginRegistration(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromContext(r.Context())
	if userID == "" {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	uid, err := uuid.Parse(userID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid user ID")
		return
	}

	dbUser, err := h.queries.GetUserByID(r.Context(), uid)
	if err != nil || !dbUser.IsActive {
		writeError(w, http.StatusNotFound, "user not found")
		return
	}

	user := &webauthnUser{id: uid, name: dbUser.Username}
	creds := h.loadCredentials(r, uid)
	user.credentials = creds

	options, session, err := h.wan.BeginRegistration(user,
		webauthn.WithExclusions(credentialDescriptors(creds)),
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "begin registration: "+err.Error())
		return
	}

	h.putSession("reg:"+userID, userID, session)
	writeJSON(w, http.StatusOK, options)
}

// HandleFinishRegistration completes the passkey registration.
func (h *WebAuthnHandler) HandleFinishRegistration(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromContext(r.Context())
	if userID == "" {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	uid, err := uuid.Parse(userID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid user ID")
		return
	}

	entry, ok := h.takeSession("reg:" + userID)
	if !ok {
		writeError(w, http.StatusBadRequest, "no registration session — call begin first")
		return
	}

	dbUser, err := h.queries.GetUserByID(r.Context(), uid)
	if err != nil || !dbUser.IsActive {
		writeError(w, http.StatusNotFound, "user not found")
		return
	}
	user := &webauthnUser{id: uid, name: dbUser.Username, credentials: h.loadCredentials(r, uid)}

	credential, err := h.wan.FinishRegistration(user, *entry.data, r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "finish registration: "+err.Error())
		return
	}

	name := "Passkey"
	if n := strings.TrimSpace(r.URL.Query().Get("name")); n != "" {
		name = n
	}

	_, err = h.queries.DB().Exec(r.Context(),
		`INSERT INTO webauthn_credentials (user_id, credential_id, public_key, attestation_type, aaguid, sign_count, name, backup_eligible, backup_state)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		uid, credential.ID, credential.PublicKey, credential.AttestationType,
		credential.Authenticator.AAGUID, credential.Authenticator.SignCount, name,
		credential.Flags.BackupEligible, credential.Flags.BackupState,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "store credential: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "registered"})
}

// HandleBeginLogin starts the passkey authentication ceremony.
func (h *WebAuthnHandler) HandleBeginLogin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string `json:"username"`
	}
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil && err != io.EOF {
			writeError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
	}

	ceremonyID := uuid.NewString()
	username := strings.TrimSpace(body.Username)
	if username == "" {
		// Discoverable credential flow (usernameless)
		options, session, err := h.wan.BeginDiscoverableLogin()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "begin login: "+err.Error())
			return
		}
		h.putSession("login:"+ceremonyID, "", session)
		setCeremonyCookie(w, r, ceremonyID)
		writeJSON(w, http.StatusOK, options)
		return
	}

	dbUser, err := h.queries.GetUserByUsername(r.Context(), username)
	if err != nil || !dbUser.IsActive {
		writeError(w, http.StatusNotFound, "user not found")
		return
	}

	user := &webauthnUser{id: dbUser.ID, name: dbUser.Username}
	user.credentials = h.loadCredentials(r, dbUser.ID)
	if len(user.credentials) == 0 {
		writeError(w, http.StatusBadRequest, "no passkeys registered for this user")
		return
	}

	options, session, err := h.wan.BeginLogin(user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "begin login: "+err.Error())
		return
	}

	h.putSession("login:"+ceremonyID, dbUser.ID.String(), session)
	setCeremonyCookie(w, r, ceremonyID)
	writeJSON(w, http.StatusOK, options)
}

// HandleFinishLogin completes the passkey authentication ceremony and issues a session.
func (h *WebAuthnHandler) HandleFinishLogin(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie(webauthnCeremonyCookie)
	clearCeremonyCookie(w, r)
	if err != nil || cookie.Value == "" {
		writeError(w, http.StatusBadRequest, "no passkey login session — call begin first")
		return
	}

	entry, ok := h.takeSession("login:" + cookie.Value)
	if !ok {
		writeError(w, http.StatusBadRequest, "passkey login session expired — try again")
		return
	}

	if entry.userID == "" {
		// Discoverable flow
		var verifiedUID uuid.UUID
		credential, err := h.wan.FinishDiscoverableLogin(
			func(rawID, userHandle []byte) (webauthn.User, error) {
				uid, err := uuid.FromBytes(userHandle)
				if err != nil {
					return nil, err
				}
				dbUser, err := h.queries.GetUserByID(r.Context(), uid)
				if err != nil {
					return nil, err
				}
				if !dbUser.IsActive {
					return nil, errors.New("user account is inactive")
				}
				verifiedUID = uid
				user := &webauthnUser{id: uid, name: dbUser.Username}
				user.credentials = h.loadCredentials(r, uid)
				return user, nil
			},
			*entry.data,
			r,
		)
		if err != nil {
			log.Printf("WARNING: webauthn discoverable login: %v", err)
			writeError(w, http.StatusUnauthorized, "passkey verification failed")
			return
		}
		if credential.Authenticator.CloneWarning {
			log.Printf("SECURITY WARNING: webauthn clone warning for user %s", verifiedUID)
			writeError(w, http.StatusUnauthorized, "passkey clone warning detected")
			return
		}
		userID := h.findUserByCredential(r, credential.ID)
		if userID == "" || (verifiedUID != uuid.Nil && userID != verifiedUID.String()) {
			writeError(w, http.StatusUnauthorized, "unknown credential")
			return
		}
		uid, err := uuid.Parse(userID)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "unknown credential")
			return
		}
		dbUser, err := h.queries.GetUserByID(r.Context(), uid)
		if err != nil || !dbUser.IsActive {
			writeError(w, http.StatusUnauthorized, "user account is inactive")
			return
		}
		h.updateSignCount(r, credential.ID, credential.Authenticator.SignCount)
		h.auth.SetSessionCookieForUser(w, r, userID)
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
		return
	}

	// Named user flow
	uid, err := uuid.Parse(entry.userID)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "passkey verification failed")
		return
	}
	dbUser, err := h.queries.GetUserByID(r.Context(), uid)
	if err != nil || !dbUser.IsActive {
		writeError(w, http.StatusUnauthorized, "passkey verification failed")
		return
	}
	user := &webauthnUser{id: uid, name: dbUser.Username}
	user.credentials = h.loadCredentials(r, uid)

	credential, err := h.wan.FinishLogin(user, *entry.data, r)
	if err != nil {
		log.Printf("WARNING: webauthn named login: %v", err)
		writeError(w, http.StatusUnauthorized, "passkey verification failed")
		return
	}
	if credential.Authenticator.CloneWarning {
		log.Printf("SECURITY WARNING: webauthn clone warning for user %s", uid)
		writeError(w, http.StatusUnauthorized, "passkey clone warning detected")
		return
	}

	h.updateSignCount(r, credential.ID, credential.Authenticator.SignCount)
	h.auth.SetSessionCookieForUser(w, r, entry.userID)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// HandleListPasskeys returns the registered passkeys for the current user.
func (h *WebAuthnHandler) HandleListPasskeys(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromContext(r.Context())
	if userID == "" {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	uid, err := uuid.Parse(userID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid user ID")
		return
	}

	rows, err := h.queries.DB().Query(r.Context(),
		`SELECT id, name, created_at FROM webauthn_credentials WHERE user_id = $1 ORDER BY created_at`,
		uid,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list passkeys: "+err.Error())
		return
	}
	defer rows.Close()

	type passkey struct {
		ID        string `json:"id"`
		Name      string `json:"name"`
		CreatedAt string `json:"created_at"`
	}
	passkeys := make([]passkey, 0)
	for rows.Next() {
		var p passkey
		var createdAt time.Time
		var id uuid.UUID
		if err := rows.Scan(&id, &p.Name, &createdAt); err != nil {
			continue
		}
		p.ID = id.String()
		p.CreatedAt = createdAt.Format(time.RFC3339)
		passkeys = append(passkeys, p)
	}
	if err := rows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, "list passkeys: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"passkeys": passkeys})
}

// HandleDeletePasskey removes a registered passkey.
func (h *WebAuthnHandler) HandleDeletePasskey(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromContext(r.Context())
	if userID == "" {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	uid, err := uuid.Parse(userID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid user ID")
		return
	}

	passkeyID := chi.URLParam(r, "id")
	if passkeyID == "" {
		passkeyID = r.PathValue("id")
	}
	pid, err := uuid.Parse(passkeyID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid passkey ID")
		return
	}

	_, err = h.queries.DB().Exec(r.Context(),
		`DELETE FROM webauthn_credentials WHERE id = $1 AND user_id = $2`,
		pid, uid,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "delete passkey: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// findUserByCredential looks up which user owns a credential ID.
func (h *WebAuthnHandler) findUserByCredential(r *http.Request, credentialID []byte) string {
	var userID uuid.UUID
	err := h.queries.DB().QueryRow(r.Context(),
		`SELECT user_id FROM webauthn_credentials WHERE credential_id = $1`,
		credentialID,
	).Scan(&userID)
	if err != nil {
		return ""
	}
	return userID.String()
}

// updateSignCount updates the sign count for a credential after successful login.
func (h *WebAuthnHandler) updateSignCount(r *http.Request, credentialID []byte, signCount uint32) {
	if _, err := h.queries.DB().Exec(r.Context(),
		`UPDATE webauthn_credentials SET sign_count = $1 WHERE credential_id = $2`,
		signCount, credentialID,
	); err != nil {
		log.Printf("WARNING: update webauthn sign count: %v", err)
	}
}

// loadCredentials fetches stored WebAuthn credentials for a user.
func (h *WebAuthnHandler) loadCredentials(r *http.Request, userID uuid.UUID) []webauthn.Credential {
	rows, err := h.queries.DB().Query(r.Context(),
		`SELECT credential_id, public_key, attestation_type, aaguid, sign_count, backup_eligible, backup_state FROM webauthn_credentials WHERE user_id = $1`,
		userID,
	)
	if err != nil {
		log.Printf("WARNING: query webauthn credentials: %v", err)
		return nil
	}
	defer rows.Close()

	var creds []webauthn.Credential
	for rows.Next() {
		var credID, pubKey, aaguid []byte
		var attestType string
		var signCount int
		var backupEligible, backupState bool
		if err := rows.Scan(&credID, &pubKey, &attestType, &aaguid, &signCount, &backupEligible, &backupState); err != nil {
			continue
		}
		creds = append(creds, webauthn.Credential{
			ID:              credID,
			PublicKey:       pubKey,
			AttestationType: attestType,
			Flags: webauthn.CredentialFlags{
				BackupEligible: backupEligible,
				BackupState:    backupState,
			},
			Authenticator: webauthn.Authenticator{
				AAGUID:    aaguid,
				SignCount: uint32(signCount),
			},
		})
	}
	if err := rows.Err(); err != nil {
		log.Printf("WARNING: iterate webauthn credentials: %v", err)
	}
	return creds
}

func credentialDescriptors(creds []webauthn.Credential) []protocol.CredentialDescriptor {
	var descs []protocol.CredentialDescriptor
	for _, c := range creds {
		descs = append(descs, protocol.CredentialDescriptor{
			Type:         protocol.PublicKeyCredentialType,
			CredentialID: c.ID,
		})
	}
	return descs
}
