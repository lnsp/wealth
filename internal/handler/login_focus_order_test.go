package handler

import (
	"strings"
	"testing"
)

// Username autofocus + tab order contract (frontend/src/pages/Login.tsx):
//
//   - Username <input type="text"> carries the `autoFocus` attribute (line 77)
//     → on mount, the caret lands on username so the user can type
//     immediately without reaching for the mouse.
//   - No other element carries autoFocus (only one element per page should).
//   - Tab order follows DOM order when no `tabIndex` overrides are present:
//        username (input) → password (input) → Sign In (button submit)
//        → Sign in with Passkey (button, only when passkeyAvailable)
//   - No `tabIndex={…}` attribute appears anywhere — relying on natural
//     DOM order keeps focus management trivial and a11y-correct.
//
// These tests lock the DOM ordering and the autoFocus placement so a
// refactor (e.g., reordering the inputs, adding a tabIndex hack, or
// silently shifting autoFocus to password) surfaces in CI.

func TestLogin_UsernameInputCarriesAutoFocus(t *testing.T) {
	src := readLoginTsx(t)
	// The autoFocus attribute belongs on the FIRST input (username), not
	// the password field. Per spec a page should declare it exactly once.
	count := strings.Count(src, "autoFocus")
	if count != 1 {
		t.Errorf("Login.tsx: %d occurrences of `autoFocus`, want exactly 1 (only on the username input)", count)
	}

	// The autoFocus attribute must sit between the username opening tag
	// and the username closing tag — i.e., before the password <input>.
	usernameIdx := strings.Index(src, `type="text"`)
	passwordIdx := strings.Index(src, `type="password"`)
	autoFocusIdx := strings.Index(src, "autoFocus")
	if usernameIdx < 0 || passwordIdx < 0 || autoFocusIdx < 0 {
		t.Fatalf("Login.tsx: missing key element — username=%d password=%d autoFocus=%d",
			usernameIdx, passwordIdx, autoFocusIdx)
	}
	if !(usernameIdx < autoFocusIdx && autoFocusIdx < passwordIdx) {
		t.Errorf("autoFocus position wrong: username=%d autoFocus=%d password=%d (want username < autoFocus < password)",
			usernameIdx, autoFocusIdx, passwordIdx)
	}
}

func TestLogin_TabOrderFollowsDOMOrder(t *testing.T) {
	src := readLoginTsx(t)
	// Locate each tabbable element in source order. Browsers tab through
	// natively focusable elements in DOM order when no tabIndex overrides
	// are present; pinning DOM order pins tab order.
	usernameIdx := strings.Index(src, `type="text"`)
	passwordIdx := strings.Index(src, `type="password"`)
	signInIdx := strings.Index(src, "Sign In")
	passkeyIdx := strings.Index(src, "Sign in with Passkey")

	if usernameIdx < 0 || passwordIdx < 0 || signInIdx < 0 || passkeyIdx < 0 {
		t.Fatalf("Login.tsx: missing element in DOM-order check: u=%d p=%d s=%d k=%d",
			usernameIdx, passwordIdx, signInIdx, passkeyIdx)
	}

	type stop struct {
		name string
		idx  int
	}
	want := []stop{
		{"username", usernameIdx},
		{"password", passwordIdx},
		{"Sign In", signInIdx},
		{"Sign in with Passkey", passkeyIdx},
	}
	for i := 1; i < len(want); i++ {
		if want[i].idx <= want[i-1].idx {
			t.Errorf("DOM order broken: %s (%d) must come after %s (%d)",
				want[i].name, want[i].idx, want[i-1].name, want[i-1].idx)
		}
	}
}

func TestLogin_NoTabIndexOverrides(t *testing.T) {
	// Adding a `tabIndex={…}` attribute to override the natural order is a
	// recurring a11y anti-pattern. The contract is: trust DOM order. If
	// future code adds `tabIndex={1}` to "prioritize" the passkey button,
	// the page traps focus on first tab and then jumps around in confusing
	// ways. Pin the absence.
	src := readLoginTsx(t)
	if strings.Contains(src, "tabIndex") {
		t.Error("Login.tsx: tabIndex attribute present — drop it; rely on DOM order for natural tab flow")
	}
}

func TestLogin_PasswordFieldDoesNotAutoFocus(t *testing.T) {
	// Defense in depth: explicitly assert the password <input> opening tag
	// does NOT contain autoFocus. (TestLogin_UsernameInputCarriesAutoFocus
	// already implies this via count=1, but a future tweak that adds
	// autoFocus to BOTH inputs and removes one elsewhere could pass that
	// count check while still putting focus on the wrong field.)
	src := readLoginTsx(t)
	passwordIdx := strings.Index(src, `type="password"`)
	if passwordIdx < 0 {
		t.Fatal("Login.tsx: password input not found")
	}
	// Scan from `type="password"` to the next `/>` (end of self-closing tag)
	// and assert autoFocus is NOT inside that range.
	tail := src[passwordIdx:]
	closeIdx := strings.Index(tail, "/>")
	if closeIdx < 0 {
		t.Fatal("Login.tsx: password input has no self-closing /> within reasonable distance")
	}
	passwordTag := tail[:closeIdx]
	if strings.Contains(passwordTag, "autoFocus") {
		t.Error("Login.tsx: password input has autoFocus — focus should land on username")
	}
}

func TestLogin_PasskeyButtonOutsideFormButTabbable(t *testing.T) {
	// The passkey button sits OUTSIDE the <form> (it's after </form>) so
	// pressing Enter in the password field can't accidentally trigger it.
	// But it must still be a native <button> (not a <div role="button">)
	// so it's in the natural tab order after the submit button.
	src := readLoginTsx(t)
	formCloseIdx := strings.Index(src, "</form>")
	passkeyIdx := strings.Index(src, "Sign in with Passkey")
	if formCloseIdx < 0 || passkeyIdx < 0 {
		t.Fatalf("Login.tsx: </form>=%d, passkey=%d", formCloseIdx, passkeyIdx)
	}
	if passkeyIdx < formCloseIdx {
		t.Error("passkey button is INSIDE the <form> — Enter in password would compete with passkey button activation")
	}
	// The button opening tag for the passkey UI must use a native <button>.
	// Look backwards from "Sign in with Passkey" to find the nearest opening tag.
	prefix := src[:passkeyIdx]
	if idx := strings.LastIndex(prefix, "<button"); idx < 0 {
		t.Error("passkey UI is not a native <button> — would fall out of natural tab order")
	}
}
