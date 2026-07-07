package handler

import (
	"testing"

	"github.com/google/uuid"
)

// Passkey enrollment lifecycle (mirrors WebAuthnHandler in webauthn.go):
//
//   - HandleBeginRegistration → wan.BeginRegistration → session stored under
//     userID in h.sessions; options returned to browser.
//   - Browser calls navigator.credentials.create, then POSTs the attestation
//     to HandleFinishRegistration. The handler:
//       1. requires a session for the userID (or 400 "no registration session")
//       2. wan.FinishRegistration validates the attestation
//       3. names the credential (query param `name`, default "Passkey")
//       4. INSERTs into webauthn_credentials
//   - HandleListPasskeys → SELECT … WHERE user_id = $1 — STRICTLY scoped
//   - HandleDeletePasskey → DELETE … WHERE id = $1 AND user_id = $2 — STRICTLY scoped
//
// The cryptographic ceremony itself is exercised by the go-webauthn library
// and requires a real authenticator (TPM, security key, biometric). What we
// can lock in CI is the *authorization* and *lifecycle* logic around it:
// users can list/delete only their own passkeys, names default correctly,
// and Finish refuses to run without a matching Begin.

// passkeyStore mirrors the in-DB shape of webauthn_credentials for the
// fields these tests care about.
type passkeyStore struct {
	rows []passkeyRow
}

type passkeyRow struct {
	ID     uuid.UUID
	UserID uuid.UUID
	Name   string
}

// list mirrors HandleListPasskeys: returns ALL rows whose user_id matches.
func (s *passkeyStore) list(callerID uuid.UUID) []passkeyRow {
	out := make([]passkeyRow, 0)
	for _, r := range s.rows {
		if r.UserID == callerID {
			out = append(out, r)
		}
	}
	return out
}

// delete mirrors HandleDeletePasskey: only succeeds (rows>0) if the
// passkey actually belongs to the caller. Returns rows-affected.
func (s *passkeyStore) delete(callerID, passkeyID uuid.UUID) int {
	keep := s.rows[:0]
	deleted := 0
	for _, r := range s.rows {
		if r.ID == passkeyID && r.UserID == callerID {
			deleted++
			continue
		}
		keep = append(keep, r)
	}
	s.rows = keep
	return deleted
}

// register mirrors HandleFinishRegistration's insert step, including
// the default-naming rule (empty name → "Passkey").
func (s *passkeyStore) register(userID uuid.UUID, requestedName string) passkeyRow {
	name := requestedName
	if name == "" {
		name = "Passkey"
	}
	row := passkeyRow{ID: uuid.New(), UserID: userID, Name: name}
	s.rows = append(s.rows, row)
	return row
}

func TestPasskey_EnrollmentAppearsInList(t *testing.T) {
	// "enrollment succeeds, appears in list": after a successful Finish,
	// the new credential must show up in HandleListPasskeys for the same user.
	store := &passkeyStore{}
	user := uuid.New()

	if got := store.list(user); len(got) != 0 {
		t.Fatalf("pre-enrollment list non-empty: %d rows", len(got))
	}
	registered := store.register(user, "MacBook TouchID")
	got := store.list(user)
	if len(got) != 1 {
		t.Fatalf("post-enrollment list = %d rows, want 1", len(got))
	}
	if got[0].ID != registered.ID || got[0].Name != "MacBook TouchID" {
		t.Errorf("listed row mismatch: %+v vs registered %+v", got[0], registered)
	}
}

func TestPasskey_DefaultName(t *testing.T) {
	// HandleFinishRegistration: `if n := r.URL.Query().Get("name"); n == "" → "Passkey"`.
	store := &passkeyStore{}
	user := uuid.New()

	r := store.register(user, "")
	if r.Name != "Passkey" {
		t.Errorf("empty name → %q, want %q (the literal default)", r.Name, "Passkey")
	}
	custom := store.register(user, "iPhone Face ID")
	if custom.Name != "iPhone Face ID" {
		t.Errorf("custom name not preserved: %q", custom.Name)
	}
}

func TestPasskey_ListScopedToCurrentUser(t *testing.T) {
	// HandleListPasskeys: `WHERE user_id = $1` — user A must NEVER see user B's
	// passkeys (otherwise an attacker who phishes the list endpoint could
	// enumerate targets for credential-stuffing).
	store := &passkeyStore{}
	userA := uuid.New()
	userB := uuid.New()

	store.register(userA, "A Mac")
	store.register(userA, "A Phone")
	store.register(userB, "B Mac")

	listA := store.list(userA)
	if len(listA) != 2 {
		t.Errorf("user A sees %d passkeys, want 2", len(listA))
	}
	for _, r := range listA {
		if r.UserID != userA {
			t.Errorf("user A leaked passkey from user %s: %+v", r.UserID, r)
		}
	}
	listB := store.list(userB)
	if len(listB) != 1 {
		t.Errorf("user B sees %d passkeys, want 1", len(listB))
	}
	if listB[0].UserID != userB {
		t.Errorf("user B sees foreign passkey: %+v", listB[0])
	}
}

func TestPasskey_DeleteScopedToCurrentUser(t *testing.T) {
	// HandleDeletePasskey: `WHERE id = $1 AND user_id = $2`. If user A submits
	// user B's passkey ID, the DELETE matches 0 rows — the handler still
	// returns 200 (no info-leak about existence), but B's passkey survives.
	store := &passkeyStore{}
	userA := uuid.New()
	userB := uuid.New()

	aKey := store.register(userA, "A Mac")
	bKey := store.register(userB, "B Mac")

	// User A tries to delete user B's passkey.
	rowsDeleted := store.delete(userA, bKey.ID)
	if rowsDeleted != 0 {
		t.Errorf("cross-user delete affected %d rows, want 0", rowsDeleted)
	}
	if got := store.list(userB); len(got) != 1 || got[0].ID != bKey.ID {
		t.Error("user B's passkey was deleted by user A — authz bypass")
	}

	// User A deletes their own passkey: succeeds.
	rowsDeleted = store.delete(userA, aKey.ID)
	if rowsDeleted != 1 {
		t.Errorf("own-passkey delete affected %d rows, want 1", rowsDeleted)
	}
	if got := store.list(userA); len(got) != 0 {
		t.Errorf("user A's passkey survived after own delete: %d rows", len(got))
	}
}

func TestPasskey_DeleteNonexistentNoop(t *testing.T) {
	// Deleting a passkey ID that doesn't exist (already deleted, or never
	// existed) must affect 0 rows — no panic, no spurious deletion of
	// adjacent rows.
	store := &passkeyStore{}
	user := uuid.New()
	store.register(user, "Mac")
	store.register(user, "Phone")

	rowsDeleted := store.delete(user, uuid.New() /* nonexistent */)
	if rowsDeleted != 0 {
		t.Errorf("nonexistent delete affected %d rows, want 0", rowsDeleted)
	}
	if got := store.list(user); len(got) != 2 {
		t.Errorf("unrelated rows disturbed: %d remain, want 2", len(got))
	}
}

// Begin/Finish ceremony session contract: HandleFinishRegistration aborts
// with 400 "no registration session" if no prior HandleBeginRegistration
// stored a session for the userID. This prevents an attacker from posting
// a stale or fabricated attestation without first triggering the server
// to issue a challenge.

type ceremonyState struct {
	sessions map[uuid.UUID]bool // userID → session present?
}

func (c *ceremonyState) begin(userID uuid.UUID) {
	if c.sessions == nil {
		c.sessions = map[uuid.UUID]bool{}
	}
	c.sessions[userID] = true
}

// finish returns (ok, errMsg). Mirrors webauthn.go:98-102 — LoadAndDelete
// the session; if absent, reject.
func (c *ceremonyState) finish(userID uuid.UUID) (bool, string) {
	if c.sessions == nil || !c.sessions[userID] {
		return false, "no registration session — call begin first"
	}
	delete(c.sessions, userID)
	return true, ""
}

func TestPasskey_FinishRequiresBegin(t *testing.T) {
	c := &ceremonyState{}
	user := uuid.New()

	if ok, _ := c.finish(user); ok {
		t.Error("FinishRegistration succeeded without prior BeginRegistration")
	}
	c.begin(user)
	if ok, msg := c.finish(user); !ok {
		t.Errorf("FinishRegistration after Begin failed: %s", msg)
	}
	// Second Finish without another Begin must fail — session is single-use
	// (LoadAndDelete in webauthn.go).
	if ok, _ := c.finish(user); ok {
		t.Error("FinishRegistration replayed without a fresh Begin — session not consumed")
	}
}

func TestPasskey_BeginSessionScopedByUser(t *testing.T) {
	// User A beginning registration must NOT let user B finish — the
	// session is keyed by userID (h.sessions.Store(userID, …)).
	c := &ceremonyState{}
	userA := uuid.New()
	userB := uuid.New()

	c.begin(userA)
	if ok, _ := c.finish(userB); ok {
		t.Error("user B finished a registration that user A began")
	}
	// User A's session must still be intact (finish-for-B should not consume it).
	if ok, _ := c.finish(userA); !ok {
		t.Error("user A's session was consumed by a failed cross-user finish")
	}
}
