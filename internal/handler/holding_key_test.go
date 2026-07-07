package handler

import "testing"

func TestHoldingKey(t *testing.T) {
	tests := []struct {
		name    string
		isin    string
		hname   string
		wantKey string
	}{
		{
			name:    "real ISIN is used as-is",
			isin:    "US0378331005",
			hname:   "Apple Inc.",
			wantKey: "US0378331005",
		},
		{
			name:    "synthetic ISIN falls back to name",
			isin:    "XX_IE00B5_0",
			hname:   "Apple Inc.",
			wantKey: "name:apple inc.",
		},
		{
			name:    "empty ISIN falls back to name",
			isin:    "",
			hname:   "Microsoft Corp.",
			wantKey: "name:microsoft corp.",
		},
		{
			name:    "name is trimmed and lowercased",
			isin:    "XX_test_1",
			hname:   "  NVIDIA Corp  ",
			wantKey: "name:nvidia corp",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := holdingKey(tt.isin, tt.hname)
			if got != tt.wantKey {
				t.Errorf("holdingKey(%q, %q) = %q, want %q", tt.isin, tt.hname, got, tt.wantKey)
			}
		})
	}
}

func TestHoldingKey_OverlapMatching(t *testing.T) {
	// Same stock in different ETFs should produce the same key
	// even with different synthetic ISINs
	key1 := holdingKey("XX_IE00B5_1", "Apple")
	key2 := holdingKey("XX_IE00BK_3", "Apple")
	if key1 != key2 {
		t.Errorf("same holding name with different synthetic ISINs should match: %q != %q", key1, key2)
	}

	// Different stocks should produce different keys
	key3 := holdingKey("XX_IE00B5_0", "Microsoft")
	if key1 == key3 {
		t.Error("different holding names should not match")
	}

	// Real ISINs should always be used even if names differ
	key4 := holdingKey("US0378331005", "Apple Inc.")
	key5 := holdingKey("US0378331005", "Apple")
	if key4 != key5 {
		t.Errorf("same real ISIN should match regardless of name: %q != %q", key4, key5)
	}
}
