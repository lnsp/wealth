package handler

import (
	"fmt"
	"strings"
	"testing"
)

// crossBrokerNote mirrors the logic in HandleAnlageKAP that decides which
// hint to surface (and whether to surface one at all) based on the per-
// broker income shape. Spec: appears when income spans ≥2 institutions.
// Stronger loss-offset wording when a gain/loss split exists.
func crossBrokerNote(brokerCount int, hasGainBroker, hasLossBroker bool) string {
	if brokerCount < 2 {
		return ""
	}
	if hasGainBroker && hasLossBroker {
		return "Verluste bei einem Broker können mit Gewinnen bei einem anderen Broker verrechnet werden. Anlage KAP einreichen lohnt sich!"
	}
	return fmt.Sprintf("Einkünfte aus %d Banken — Anlage KAP einreichen, damit der Sparerpauschbetrag korrekt verrechnet wird.", brokerCount)
}

func TestCrossBrokerNote_SingleBrokerNoNote(t *testing.T) {
	if got := crossBrokerNote(1, true, false); got != "" {
		t.Errorf("1 broker → expected no note, got %q", got)
	}
	if got := crossBrokerNote(1, false, true); got != "" {
		t.Errorf("1 broker (with losses) → expected no note, got %q", got)
	}
}

func TestCrossBrokerNote_TwoBrokersGainsOnly(t *testing.T) {
	// Per spec: note fires whenever ≥2 brokers, even without a gain/loss
	// split — Sparerpauschbetrag reconciliation is bank-specific.
	got := crossBrokerNote(2, true, false)
	if got == "" {
		t.Fatal("2 brokers with gains-only → expected a note (Sparerpauschbetrag reconciliation)")
	}
	if !strings.Contains(got, "Sparerpauschbetrag") {
		t.Errorf("gains-only note should mention Sparerpauschbetrag, got %q", got)
	}
	if !strings.Contains(got, "2 Banken") {
		t.Errorf("note should report the broker count, got %q", got)
	}
}

func TestCrossBrokerNote_TwoBrokersLossesOnly(t *testing.T) {
	got := crossBrokerNote(2, false, true)
	if got == "" {
		t.Fatal("2 brokers with losses-only → expected a note")
	}
	if !strings.Contains(got, "Sparerpauschbetrag") {
		t.Errorf("losses-only note should mention Sparerpauschbetrag, got %q", got)
	}
}

func TestCrossBrokerNote_GainAndLossSplitWinsStrongerMessage(t *testing.T) {
	// Gain at one broker + loss at another → the LOSS OFFSET message wins
	// (higher-value action than basic FSA reconciliation).
	got := crossBrokerNote(2, true, true)
	if !strings.Contains(got, "Verluste") || !strings.Contains(got, "Gewinnen") {
		t.Errorf("gain/loss split → expected loss-offset wording, got %q", got)
	}
	if strings.Contains(got, "Sparerpauschbetrag") {
		t.Errorf("gain/loss split should NOT use the FSA-only wording, got %q", got)
	}
}

func TestCrossBrokerNote_FiveBrokersIncludesCountInMessage(t *testing.T) {
	// Larger broker counts surface in the message so the user sees the
	// scale of the cross-broker reconciliation they need to do.
	got := crossBrokerNote(5, true, false)
	if !strings.Contains(got, "5 Banken") {
		t.Errorf("expected message to mention 5 Banken, got %q", got)
	}
}

func TestCrossBrokerNote_ZeroBrokersNoCrashAndNoNote(t *testing.T) {
	// Empty portfolio — shouldn't panic, shouldn't emit anything.
	if got := crossBrokerNote(0, false, false); got != "" {
		t.Errorf("0 brokers → expected no note, got %q", got)
	}
}
