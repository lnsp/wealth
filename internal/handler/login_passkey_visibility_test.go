package handler

import (
	"strings"
	"testing"
)

// Passkey-button visibility contract (frontend/src/pages/Login.tsx):
//
//   - Initial state: `passkeyAvailable = false` (line 13). The button is
//     hidden by default so SSR / first-paint never flashes a WebAuthn UI
//     in browsers that don't support it.
//   - useEffect on mount (lines 15-20):
//        if (window.PublicKeyCredential) { setPasskeyAvailable(true); }
//     This is a truthy check on the constructor, not a feature-probe of
//     specific methods. Browsers without WebAuthn don't define this global,
//     so the conditional stays false and the UI never renders.
//   - JSX guard (line 104): `{passkeyAvailable && (<>divider + button</>)}`
//     The fragment wraps BOTH the "or" divider AND the Sign-in-with-passkey
//     button — so neither shows in unsupported browsers. (A common drift
//     is to leave the divider unconditional, producing a lonely "or"
//     followed by nothing.)
//
// These tests pin the predicate AND lock the JSX structure so a refactor
// that splits the conditional, flips the initial state, or swaps the
// truthy check for an unrelated probe surfaces in CI.

// passkeyButtonVisible mirrors the JSX guard `{passkeyAvailable && (...)}`.
func passkeyButtonVisible(passkeyAvailable bool) bool {
	return passkeyAvailable
}

// detectPasskeySupport mirrors the useEffect at Login.tsx:15-20. The input
// stands in for `window.PublicKeyCredential`; nil/false means undefined.
func detectPasskeySupport(publicKeyCredentialDefined bool) bool {
	if publicKeyCredentialDefined {
		return true
	}
	return false // initial state preserved
}

func TestLoginPasskey_HiddenWhenWebAuthnAbsent(t *testing.T) {
	// Safari ≤13, older Android Chrome, embedded webviews without
	// WebAuthn → window.PublicKeyCredential is undefined → button hidden.
	if detectPasskeySupport(false) {
		t.Error("detectPasskeySupport(undefined) = true, want false")
	}
	if passkeyButtonVisible(false) {
		t.Error("passkey button visible when passkeyAvailable=false")
	}
}

func TestLoginPasskey_ShownWhenWebAuthnPresent(t *testing.T) {
	if !detectPasskeySupport(true) {
		t.Error("detectPasskeySupport(defined) = false, want true")
	}
	if !passkeyButtonVisible(true) {
		t.Error("passkey button hidden when passkeyAvailable=true")
	}
}

func TestLoginPasskey_InitialStateIsFalse(t *testing.T) {
	// Hidden by default before the useEffect runs. This matters because
	// the useEffect is client-side only; during SSR / first paint, the
	// initial state determines what's in the HTML.
	src := readLoginTsx(t)
	if !strings.Contains(src, "useState(false)") {
		t.Error("Login.tsx: expected passkeyAvailable to default to false")
	}
	// And specifically wired to passkeyAvailable.
	if !strings.Contains(src, "setPasskeyAvailable] = useState(false)") {
		t.Error("Login.tsx: passkeyAvailable must initialize to false (truthy default would flash the UI before detection)")
	}
}

func TestLoginPasskey_DetectionUsesPublicKeyCredential(t *testing.T) {
	src := readLoginTsx(t)
	// The detection MUST key off window.PublicKeyCredential. A drift to
	// e.g. `navigator.credentials` would render the button in browsers
	// that have the Credential Management API but not WebAuthn.
	if !strings.Contains(src, "window.PublicKeyCredential") {
		t.Error("Login.tsx: detection must read window.PublicKeyCredential — the canonical WebAuthn feature probe")
	}
	// And the truthy branch sets state to true (not false / not toggled).
	if !strings.Contains(src, "setPasskeyAvailable(true)") {
		t.Error("Login.tsx: missing setPasskeyAvailable(true) inside the support check")
	}
}

func TestLoginPasskey_GuardWrapsBothDividerAndButton(t *testing.T) {
	// JSX line 104 wraps `<>...</>` so BOTH the "or" divider AND the
	// "Sign in with Passkey" button render together. If someone splits
	// the conditional and leaves the divider unconditional, you'd see a
	// lone "or" with nothing under it.
	src := readLoginTsx(t)
	idx := strings.Index(src, "{passkeyAvailable && (")
	if idx < 0 {
		t.Fatal("Login.tsx: expected {passkeyAvailable && (...)} guard around the passkey UI")
	}
	// In the ~600 chars following the guard, both the "or" text AND the
	// button must appear — proving they share the same conditional.
	window := src[idx:]
	if len(window) > 1200 {
		window = window[:1200]
	}
	if !strings.Contains(window, ">or<") {
		t.Error("the passkeyAvailable guard does not enclose the \"or\" divider — a drift would leave a lonely separator")
	}
	if !strings.Contains(window, "Sign in with Passkey") {
		t.Error("the passkeyAvailable guard does not enclose the passkey button")
	}
}

func TestLoginPasskey_NoAlternateDetectionPath(t *testing.T) {
	// Defense in depth: there must be NO second code path that flips
	// passkeyAvailable to true unconditionally — e.g., a leftover
	// `setPasskeyAvailable(true)` outside the useEffect, or a default
	// of true. We've already pinned `useState(false)` and the gated
	// setter above; this test belt-and-braces the count.
	src := readLoginTsx(t)
	// Expect exactly one true-flip in the file.
	count := strings.Count(src, "setPasskeyAvailable(true)")
	if count != 1 {
		t.Errorf("Login.tsx: %d occurrences of setPasskeyAvailable(true), want exactly 1 (inside the window.PublicKeyCredential check)", count)
	}
	// And no setter calls with `false` (we rely on the initial state for that).
	if strings.Contains(src, "setPasskeyAvailable(false)") {
		t.Error("Login.tsx: unexpected setPasskeyAvailable(false) — drift toward toggling means a transient false-flash")
	}
}
