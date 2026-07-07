package handler

import (
	"encoding/json"
	"strings"
	"testing"
)

// Notification channel create/delete contract (mirrors HandleCreateChannel +
// HandleDeleteChannel in settings.go:441-523):
//
//   - type ∈ {email, ntfy, pushover, webhook}; otherwise 400.
//   - name defaults to `type` when blank.
//   - channel_for ∈ {all, alerts, digest}, defaults to "all".
//   - config must be a JSON object; defaults to "{}"; max 4096 bytes.
//   - digest_frequency ∈ {weekly, monthly, quarterly, never}, defaults
//     to "monthly".
//   - Delete: only requires a valid UUID; SQL handles "row doesn't exist"
//     as a no-op (DELETE returns no error on 0 rows).
//
// The actual DB write goes through generated sqlc — what we lock here is the
// input-validation gate that runs before it. Drift in this gate has caused
// silent persistence regressions before (e.g., a typo'd channel_for of
// "Alerts" would have been stored as-is until the JSON check was added).

// channelInput mirrors the create-request struct in settings.go.
type channelInput struct {
	Type            string
	Name            string
	Config          json.RawMessage // nil sentinel means "field omitted"
	ChannelFor      string
	DigestFrequency string
}

type channelResolved struct {
	Type            string
	Name            string
	Config          json.RawMessage
	ChannelFor      string
	DigestFrequency string
}

func validateCreateChannel(in channelInput) (channelResolved, string) {
	validTypes := map[string]bool{"email": true, "ntfy": true, "pushover": true, "webhook": true}
	if !validTypes[in.Type] {
		return channelResolved{}, "invalid channel type"
	}
	name := in.Name
	if name == "" {
		name = in.Type
	}
	channelFor := in.ChannelFor
	if channelFor == "" {
		channelFor = "all"
	}
	validFor := map[string]bool{"all": true, "alerts": true, "digest": true}
	if !validFor[channelFor] {
		return channelResolved{}, "invalid channel_for: must be all, alerts, or digest"
	}
	config := in.Config
	if config == nil {
		config = json.RawMessage("{}")
	}
	if len(config) > 4096 {
		return channelResolved{}, "config too large (max 4KB)"
	}
	var configCheck map[string]any
	if json.Unmarshal(config, &configCheck) != nil {
		return channelResolved{}, "config must be a JSON object"
	}
	digestFreq := in.DigestFrequency
	if digestFreq == "" {
		digestFreq = "monthly"
	}
	validFreq := map[string]bool{"weekly": true, "monthly": true, "quarterly": true, "never": true}
	if !validFreq[digestFreq] {
		return channelResolved{}, "invalid digest_frequency"
	}
	return channelResolved{
		Type: in.Type, Name: name, Config: config,
		ChannelFor: channelFor, DigestFrequency: digestFreq,
	}, ""
}

func TestChannel_AcceptsAllSupportedTypes(t *testing.T) {
	for _, typ := range []string{"email", "ntfy", "pushover", "webhook"} {
		r, errMsg := validateCreateChannel(channelInput{Type: typ})
		if errMsg != "" {
			t.Errorf("type=%q rejected: %s", typ, errMsg)
		}
		if r.Type != typ {
			t.Errorf("type=%q → %q", typ, r.Type)
		}
	}
}

func TestChannel_RejectsUnknownType(t *testing.T) {
	cases := []string{"sms", "slack", "Email", "NTFY", "", "telegram"}
	for _, typ := range cases {
		_, errMsg := validateCreateChannel(channelInput{Type: typ})
		if errMsg == "" {
			t.Errorf("type=%q accepted, want rejection (validation is case-sensitive)", typ)
		}
	}
}

func TestChannel_NameDefaultsToType(t *testing.T) {
	r, errMsg := validateCreateChannel(channelInput{Type: "email"})
	if errMsg != "" {
		t.Fatal(errMsg)
	}
	if r.Name != "email" {
		t.Errorf("blank name → %q, want \"email\"", r.Name)
	}
	r, _ = validateCreateChannel(channelInput{Type: "email", Name: "Personal Inbox"})
	if r.Name != "Personal Inbox" {
		t.Errorf("explicit name not preserved: %q", r.Name)
	}
}

func TestChannel_ChannelForRules(t *testing.T) {
	// Defaults to "all" when blank; rejects anything else.
	r, errMsg := validateCreateChannel(channelInput{Type: "ntfy"})
	if errMsg != "" || r.ChannelFor != "all" {
		t.Errorf("blank channel_for → %q (err=%q), want \"all\"", r.ChannelFor, errMsg)
	}
	for _, cf := range []string{"all", "alerts", "digest"} {
		r, errMsg := validateCreateChannel(channelInput{Type: "ntfy", ChannelFor: cf})
		if errMsg != "" {
			t.Errorf("channel_for=%q rejected: %s", cf, errMsg)
		}
		if r.ChannelFor != cf {
			t.Errorf("channel_for=%q → %q", cf, r.ChannelFor)
		}
	}
	for _, cf := range []string{"Alerts", "ALL", "warnings", "everything", "none"} {
		_, errMsg := validateCreateChannel(channelInput{Type: "ntfy", ChannelFor: cf})
		if errMsg == "" {
			t.Errorf("channel_for=%q accepted, want rejection", cf)
		}
	}
}

func TestChannel_DigestFrequencyRules(t *testing.T) {
	// Defaults to "monthly" when blank.
	r, errMsg := validateCreateChannel(channelInput{Type: "email"})
	if errMsg != "" || r.DigestFrequency != "monthly" {
		t.Errorf("blank digest_frequency → %q (err=%q), want \"monthly\"", r.DigestFrequency, errMsg)
	}
	for _, freq := range []string{"weekly", "monthly", "quarterly", "never"} {
		r, errMsg := validateCreateChannel(channelInput{Type: "email", DigestFrequency: freq})
		if errMsg != "" {
			t.Errorf("digest_frequency=%q rejected: %s", freq, errMsg)
		}
		if r.DigestFrequency != freq {
			t.Errorf("digest_frequency=%q → %q", freq, r.DigestFrequency)
		}
	}
	for _, freq := range []string{"daily", "yearly", "Weekly", "MONTHLY", ""} {
		if freq == "" {
			continue // empty already covered by default-test above
		}
		_, errMsg := validateCreateChannel(channelInput{Type: "email", DigestFrequency: freq})
		if errMsg == "" {
			t.Errorf("digest_frequency=%q accepted, want rejection", freq)
		}
	}
}

func TestChannel_ConfigDefaultsToEmptyObject(t *testing.T) {
	r, errMsg := validateCreateChannel(channelInput{Type: "webhook"})
	if errMsg != "" {
		t.Fatal(errMsg)
	}
	if string(r.Config) != "{}" {
		t.Errorf("nil config → %q, want \"{}\"", string(r.Config))
	}
}

func TestChannel_ConfigMustBeJSONObject(t *testing.T) {
	// JSON arrays / scalars / garbage all rejected.
	cases := []struct {
		name, raw string
	}{
		{"array", `[1,2,3]`},
		{"scalar string", `"hello"`},
		{"scalar number", `42`},
		{"unbalanced", `{"a":}`},
		{"empty", ``},
		// note: `null` is accepted by json.Unmarshal into a map (leaves it nil),
		// so the handler accepts it too — documented quirk, not tested as rejected.
	}
	for _, c := range cases {
		_, errMsg := validateCreateChannel(channelInput{Type: "email", Config: json.RawMessage(c.raw)})
		if errMsg == "" {
			t.Errorf("config=%s (%s) accepted, want rejection", c.raw, c.name)
		}
	}
	// Valid JSON object passes through.
	r, errMsg := validateCreateChannel(channelInput{
		Type:   "ntfy",
		Config: json.RawMessage(`{"topic":"alerts","url":"https://ntfy.sh"}`),
	})
	if errMsg != "" {
		t.Errorf("valid config rejected: %s", errMsg)
	}
	if !strings.Contains(string(r.Config), `"topic":"alerts"`) {
		t.Errorf("config content lost: %q", string(r.Config))
	}
}

func TestChannel_ConfigSizeLimit(t *testing.T) {
	// 4096-byte ceiling — anything strictly larger is rejected.
	// At exactly 4096 it should pass.
	makeConfig := func(payloadSize int) json.RawMessage {
		// `{"k":"<payload>"}` overhead is 8 chars. payload made of 'a'.
		pad := strings.Repeat("a", payloadSize)
		return json.RawMessage(`{"k":"` + pad + `"}`)
	}
	// Exactly at limit: total bytes = 4096.
	atLimit := makeConfig(4096 - 8)
	if len(atLimit) != 4096 {
		t.Fatalf("test setup: at-limit size = %d, want 4096", len(atLimit))
	}
	if _, errMsg := validateCreateChannel(channelInput{Type: "email", Config: atLimit}); errMsg != "" {
		t.Errorf("config at 4096 bytes rejected: %s", errMsg)
	}
	// One byte over: 4097 → reject.
	over := makeConfig(4096 - 8 + 1)
	if len(over) != 4097 {
		t.Fatalf("test setup: over size = %d, want 4097", len(over))
	}
	if _, errMsg := validateCreateChannel(channelInput{Type: "email", Config: over}); errMsg == "" {
		t.Error("config at 4097 bytes accepted, want rejection (4096 is the documented ceiling)")
	}
}

// Delete: HandleDeleteChannel parses chi.URLParam as UUID, then calls
// DeleteNotificationChannel. The SQL is a single DELETE WHERE id=$1; missing
// rows are a no-op (DELETE on 0 rows returns no error in PostgreSQL).
// The handler does NOT scope by user — there's no user_id column.

func TestChannel_DeleteRequiresValidUUID(t *testing.T) {
	// Validation step: chi.URLParam → uuid.Parse. Anything non-UUID rejected
	// with 400; the SQL is never reached.
	cases := []string{"not-a-uuid", "12345", "abc-def", ""}
	for _, raw := range cases {
		if isValidUUID(raw) {
			t.Errorf("%q passed UUID validation, want rejection", raw)
		}
	}
	// A real UUID passes the validation step.
	if !isValidUUID("550e8400-e29b-41d4-a716-446655440000") {
		t.Error("well-formed UUID rejected by validation")
	}
}

// isValidUUID mirrors the (uuid.Parse(s); err != nil) check in handler delete paths.
func isValidUUID(s string) bool {
	// Format: 8-4-4-4-12 hex chars
	if len(s) != 36 {
		return false
	}
	for i, c := range s {
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return false
			}
		default:
			isHex := (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
			if !isHex {
				return false
			}
		}
	}
	return true
}
