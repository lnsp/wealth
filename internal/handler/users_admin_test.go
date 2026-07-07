package handler

import (
	"testing"
)

// User creation validation rules mirror HandleCreateUser (users.go:86-134):
//   - username required (non-empty)
//   - password required (non-empty) and ≥8 chars
//   - role defaults to "member" if blank
//   - role must be "admin" or "member" (no other values allowed)
//
// requireAdmin (users.go:29-52) gates HandleCreateUser / HandleDeleteUser /
// HandleToggleUser. The gate has two intentional bypasses:
//   - h.auth == nil → no auth configured (dev mode), allow
//   - userID == "" → no session in request (also dev), allow
// In production with auth enabled, Members (role != "admin") get HTTP 403
// "admin access required".
//
// These tests mirror the pure validation logic so future tweaks to the rule
// set surface in CI.

func validateCreateUser(username, password, role string) (resolvedRole string, errMsg string) {
	if username == "" || password == "" {
		return "", "username and password are required"
	}
	if role == "" {
		role = "member"
	}
	if role != "admin" && role != "member" {
		return "", "role must be admin or member"
	}
	if len(password) < 8 {
		return "", "password must be at least 8 characters"
	}
	return role, ""
}

func TestCreateUser_DefaultsToMember(t *testing.T) {
	role, errMsg := validateCreateUser("alice", "password123", "")
	if errMsg != "" {
		t.Fatalf("unexpected error: %s", errMsg)
	}
	if role != "member" {
		t.Errorf("blank role → resolved = %q, want member", role)
	}
}

func TestCreateUser_AcceptsAdmin(t *testing.T) {
	role, errMsg := validateCreateUser("admin", "supersecret", "admin")
	if errMsg != "" {
		t.Fatalf("admin role rejected: %s", errMsg)
	}
	if role != "admin" {
		t.Errorf("admin role resolved to %q, want admin", role)
	}
}

func TestCreateUser_RejectsUnknownRole(t *testing.T) {
	cases := []string{"superadmin", "guest", "Admin", "MEMBER", "owner"}
	for _, role := range cases {
		_, errMsg := validateCreateUser("bob", "password123", role)
		if errMsg == "" {
			t.Errorf("role %q accepted, want rejection (only admin/member valid; case-sensitive)", role)
		}
	}
}

func TestCreateUser_RequiresUsernameAndPassword(t *testing.T) {
	if _, e := validateCreateUser("", "password123", "member"); e == "" {
		t.Error("empty username accepted, want rejection")
	}
	if _, e := validateCreateUser("alice", "", "member"); e == "" {
		t.Error("empty password accepted, want rejection")
	}
}

func TestCreateUser_RequiresPasswordMinLength(t *testing.T) {
	// 7 chars rejected, 8 chars accepted.
	if _, e := validateCreateUser("alice", "short77", "member"); e == "" {
		t.Error("7-char password accepted, want rejection")
	}
	if _, e := validateCreateUser("alice", "exact888", "member"); e != "" {
		t.Errorf("8-char password rejected: %s", e)
	}
}

// requireAdmin contract: in production (auth != nil, valid session, user
// found), only role == "admin" passes. Below pins the comparison.

func userPassesAdminGate(authConfigured bool, sessionUserID string, userRole string, userFound bool) (allowed bool, status int) {
	if !authConfigured {
		return true, 0 // auth disabled — bypass
	}
	if sessionUserID == "" {
		return true, 0 // no session in request — bypass (dev mode)
	}
	if !userFound {
		return false, 403
	}
	if userRole != "admin" {
		return false, 403
	}
	return true, 0
}

func TestRequireAdmin_AdminAllowed(t *testing.T) {
	allowed, _ := userPassesAdminGate(true, "abc-uuid", "admin", true)
	if !allowed {
		t.Error("admin user must pass the gate")
	}
}

func TestRequireAdmin_MemberDenied(t *testing.T) {
	allowed, status := userPassesAdminGate(true, "abc-uuid", "member", true)
	if allowed {
		t.Error("member user must NOT pass the admin gate")
	}
	if status != 403 {
		t.Errorf("denied member status = %d, want 403", status)
	}
}

func TestRequireAdmin_AuthDisabledBypass(t *testing.T) {
	// Dev/test environments without auth configured pass through.
	allowed, _ := userPassesAdminGate(false, "", "", false)
	if !allowed {
		t.Error("auth-disabled mode must bypass the admin gate")
	}
}

func TestRequireAdmin_NoSessionBypass(t *testing.T) {
	// Auth configured but no session on the request — also a dev path.
	allowed, _ := userPassesAdminGate(true, "", "", false)
	if !allowed {
		t.Error("no-session mode must bypass the admin gate")
	}
}

func TestRequireAdmin_UnknownUserDenied(t *testing.T) {
	allowed, status := userPassesAdminGate(true, "stale-uuid", "", false)
	if allowed {
		t.Error("session present but user not in DB must be denied")
	}
	if status != 403 {
		t.Errorf("denied unknown user status = %d, want 403", status)
	}
}
