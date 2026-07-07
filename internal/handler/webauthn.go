package handler

import (
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/google/uuid"

	"github.com/lnsp/wealth/internal/auth"
	db "github.com/lnsp/wealth/internal/database/generated"
)

// WebAuthnHandler manages passkey registration and authentication.
type WebAuthnHandler struct {
	queries *db.Queries
	wan     *webauthn.WebAuthn
	auth    *auth.Auth
	// In-memory session storage (simple approach for single-instance deployment)
	sessions sync.Map // userID -> *webauthn.SessionData
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
	return &WebAuthnHandler{queries: q, wan: wan, auth: a}, nil
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
	if err != nil {
		writeError(w, http.StatusNotFound, "user not found")
		return
	}

	user := &webauthnUser{id: uid, name: dbUser.Username}
	// Load existing credentials to exclude them
	creds := h.loadCredentials(r, uid)
	user.credentials = creds

	options, session, err := h.wan.BeginRegistration(user,
		webauthn.WithExclusions(credentialDescriptors(creds)),
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "begin registration: "+err.Error())
		return
	}

	h.sessions.Store(userID, session)
	writeJSON(w, http.StatusOK, options)
}

// HandleFinishRegistration completes the passkey registration.
func (h *WebAuthnHandler) HandleFinishRegistration(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromContext(r.Context())
	if userID == "" {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	uid, _ := uuid.Parse(userID)

	sessionData, ok := h.sessions.LoadAndDelete(userID)
	if !ok {
		writeError(w, http.StatusBadRequest, "no registration session — call begin first")
		return
	}

	dbUser, _ := h.queries.GetUserByID(r.Context(), uid)
	user := &webauthnUser{id: uid, name: dbUser.Username}

	credential, err := h.wan.FinishRegistration(user, *sessionData.(*webauthn.SessionData), r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "finish registration: "+err.Error())
		return
	}

	// Store credential in database
	var name string
	if n := r.URL.Query().Get("name"); n != "" {
		name = n
	} else {
		name = "Passkey"
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
	json.NewDecoder(r.Body).Decode(&body)

	if body.Username == "" {
		// Discoverable credential flow (usernameless)
		options, session, err := h.wan.BeginDiscoverableLogin()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "begin login: "+err.Error())
			return
		}
		h.sessions.Store("discoverable", session)
		writeJSON(w, http.StatusOK, options)
		return
	}

	dbUser, err := h.queries.GetUserByUsername(r.Context(), body.Username)
	if err != nil {
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

	h.sessions.Store(dbUser.ID.String(), session)
	writeJSON(w, http.StatusOK, options)
}

// HandleFinishLogin completes the passkey authentication ceremony and issues a session.
func (h *WebAuthnHandler) HandleFinishLogin(w http.ResponseWriter, r *http.Request) {
	// Try discoverable flow first
	sessionData, ok := h.sessions.LoadAndDelete("discoverable")
	if ok {
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
				user := &webauthnUser{id: uid, name: dbUser.Username}
				user.credentials = h.loadCredentials(r, uid)
				return user, nil
			},
			*sessionData.(*webauthn.SessionData),
			r,
		)
		if err != nil {
			log.Printf("WARNING: webauthn discoverable login: %v", err)
			writeError(w, http.StatusUnauthorized, "passkey verification failed")
			return
		}
		// Find user by credential
		userID := h.findUserByCredential(r, credential.ID)
		if userID == "" {
			writeError(w, http.StatusUnauthorized, "unknown credential")
			return
		}
		h.updateSignCount(r, credential.ID, credential.Authenticator.SignCount)
		h.auth.SetSessionCookieForUser(w, r, userID)
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
		return
	}

	// Named user flow — try all stored user sessions
	// The login/begin stored session under the user's ID
	var matchedUserID string
	h.sessions.Range(func(key, value any) bool {
		keyStr, ok := key.(string)
		if !ok || keyStr == "discoverable" {
			return true
		}
		uid, err := uuid.Parse(keyStr)
		if err != nil {
			return true
		}
		dbUser, err := h.queries.GetUserByID(r.Context(), uid)
		if err != nil {
			return true
		}
		user := &webauthnUser{id: uid, name: dbUser.Username}
		user.credentials = h.loadCredentials(r, uid)

		_, err = h.wan.FinishLogin(user, *value.(*webauthn.SessionData), r)
		if err == nil {
			matchedUserID = keyStr
			h.sessions.Delete(keyStr)
			return false // found match
		}
		return true
	})

	if matchedUserID == "" {
		writeError(w, http.StatusUnauthorized, "passkey verification failed")
		return
	}

	h.auth.SetSessionCookieForUser(w, r, matchedUserID)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// HandleListPasskeys returns the registered passkeys for the current user.
func (h *WebAuthnHandler) HandleListPasskeys(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromContext(r.Context())
	if userID == "" {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	uid, _ := uuid.Parse(userID)

	rows, err := h.queries.DB().Query(r.Context(),
		`SELECT id, name, created_at FROM webauthn_credentials WHERE user_id = $1 ORDER BY created_at`,
		uid,
	)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"passkeys": []any{}})
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
	writeJSON(w, http.StatusOK, map[string]any{"passkeys": passkeys})
}

// HandleDeletePasskey removes a registered passkey.
func (h *WebAuthnHandler) HandleDeletePasskey(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromContext(r.Context())
	if userID == "" {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	uid, _ := uuid.Parse(userID)

	passkeyID := r.PathValue("id")
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
	h.queries.DB().Exec(r.Context(),
		`UPDATE webauthn_credentials SET sign_count = $1 WHERE credential_id = $2`,
		signCount, credentialID,
	)
}

// loadCredentials fetches stored WebAuthn credentials for a user.
func (h *WebAuthnHandler) loadCredentials(r *http.Request, userID uuid.UUID) []webauthn.Credential {
	rows, err := h.queries.DB().Query(r.Context(),
		`SELECT credential_id, public_key, attestation_type, aaguid, sign_count, backup_eligible, backup_state FROM webauthn_credentials WHERE user_id = $1`,
		userID,
	)
	if err != nil {
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
	return creds
}

func credentialDescriptors(creds []webauthn.Credential) []protocol.CredentialDescriptor {
	var descs []protocol.CredentialDescriptor
	for _, c := range creds {
		descs = append(descs, protocol.CredentialDescriptor{
			Type:            protocol.PublicKeyCredentialType,
			CredentialID:    c.ID,
		})
	}
	return descs
}
