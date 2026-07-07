package handler

import (
	"os"
	"strings"
	"testing"
)

// Login form contract (frontend/src/pages/Login.tsx):
//
//   1. Submit button is disabled iff `loading || !password` (line 97).
//      Username being empty does NOT disable submit — handleSubmit posts
//      whatever's in the field; the backend validates.
//   2. Enter submits: the form uses native HTML <form onSubmit={handleSubmit}>
//      (line 70) with <button type="submit"> (line 96). HTML spec guarantees
//      that pressing Enter in any descendant <input> dispatches a submit
//      event on the form, so we lock the structural pattern instead of
//      simulating keydown.
//   3. handleSubmit calls e.preventDefault() (line 23) so the page doesn't
//      reload — this is what lets React handle the response inline.
//
// These tests mirror the predicate AND do a structural read of Login.tsx so
// that a refactor (e.g., dropping the form, switching to a div+onClick) gets
// flagged in CI before the keyboard accessibility regression ships.

// submitDisabled mirrors `disabled={loading || !password}` from Login.tsx:97.
func submitDisabled(loading bool, password string) bool {
	return loading || password == ""
}

func TestLogin_SubmitDisabledRule(t *testing.T) {
	cases := []struct {
		name     string
		loading  bool
		password string
		want     bool
	}{
		{"empty password, idle", false, "", true},
		{"empty password, loading", true, "", true},
		{"filled password, idle", false, "hunter2", false},
		{"filled password, loading", true, "hunter2", true},
		{"single-char password, idle", false, "x", false}, // no min-length on the client
		{"whitespace-only password", false, "   ", false}, // " " is truthy in JS, so submit is enabled
	}
	for _, c := range cases {
		got := submitDisabled(c.loading, c.password)
		if got != c.want {
			t.Errorf("%s: submitDisabled(loading=%v, password=%q) = %v, want %v",
				c.name, c.loading, c.password, got, c.want)
		}
	}
}

func TestLogin_SubmitNotGatedOnUsername(t *testing.T) {
	// The disable predicate intentionally ignores username — typing only
	// a password (and tabbing back) must NOT block submit. Documented here
	// because a future "require username too" tweak should be a conscious
	// choice, not a drift.
	if submitDisabled(false, "secret") {
		t.Error("submit disabled with password set + empty username — predicate has drifted to gate on username")
	}
}

// Structural assertions against Login.tsx — these guard the keyboard
// accessibility contract that the predicate test alone can't cover.

const loginTsxPath = "../../frontend/src/pages/Login.tsx"

func readLoginTsx(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(loginTsxPath)
	if err != nil {
		t.Fatalf("read %s: %v", loginTsxPath, err)
	}
	return string(data)
}

func TestLogin_FormStructureLocksEnterToSubmit(t *testing.T) {
	src := readLoginTsx(t)
	// Native HTML form: Enter in any <input> dispatches submit.
	if !strings.Contains(src, "onSubmit={handleSubmit}") {
		t.Error("Login.tsx: missing <form onSubmit={handleSubmit}> — Enter-to-submit relies on the native form contract")
	}
	if !strings.Contains(src, `type="submit"`) {
		t.Error("Login.tsx: missing <button type=\"submit\"> — Enter-to-submit needs a submit button inside the form")
	}
	if !strings.Contains(src, "e.preventDefault()") {
		t.Error("Login.tsx: handleSubmit must call e.preventDefault() to avoid a full-page reload on Enter")
	}
	// The two inputs must live inside the form (not pulled out into siblings),
	// since Enter only fires submit when the input is a form descendant.
	if !strings.Contains(src, `type="text"`) || !strings.Contains(src, `type="password"`) {
		t.Error("Login.tsx: expected both username + password inputs")
	}
}

func TestLogin_DisabledPredicateLockedInJSX(t *testing.T) {
	src := readLoginTsx(t)
	// The actual disable expression. If someone changes this to
	// `!username || !password`, the test catches the drift.
	if !strings.Contains(src, "disabled={loading || !password}") {
		t.Error("Login.tsx: expected `disabled={loading || !password}` on the submit button")
	}
	// Sanity: confirm the disable lives on the SUBMIT button, not the
	// passkey button (which has its own `disabled={loading}`).
	idx := strings.Index(src, "disabled={loading || !password}")
	if idx < 0 {
		return // already reported
	}
	tail := src[idx:]
	// The submit button text "Sign In" should appear within 200 chars after
	// the disable attr (button label is one of the next JSX nodes).
	window := tail
	if len(window) > 400 {
		window = window[:400]
	}
	if !strings.Contains(window, "Sign In") {
		t.Error("disabled predicate is not on the Sign In button — gate may have migrated")
	}
}

func TestLogin_PasswordInputBindsToPasswordState(t *testing.T) {
	src := readLoginTsx(t)
	// The disable predicate keys off the `password` state. The password
	// <input> must call `setPassword` so the gate actually responds to typing.
	if !strings.Contains(src, "onChange={(e) => setPassword(e.target.value)}") {
		t.Error("Login.tsx: password input must bind to setPassword — otherwise the submit gate never updates")
	}
	if !strings.Contains(src, "value={password}") {
		t.Error("Login.tsx: password input must be controlled (value={password})")
	}
}
