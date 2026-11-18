package handler

import (
	"strings"
	"testing"
)

// These tests run crossBrokerNote, the function computeAnlageKAP uses for the
// "cross_broker_note" field.

func domesticBank(name string) *bankIncome {
	return &bankIncome{taxBank: taxBank{Key: strings.ToLower(name), Name: name, Domestic: true}}
}

func foreignBank(name string) *bankIncome {
	return &bankIncome{taxBank: taxBank{Key: strings.ToLower(name), Name: name}}
}

func TestCrossBrokerNote_SingleBankNoNote(t *testing.T) {
	b := domesticBank("ING")
	b.ShareLosses = 500
	if got := crossBrokerNote([]*bankIncome{b}); got != "" {
		t.Errorf("1 bank → expected no note, got %q", got)
	}
	if got := crossBrokerNote(nil); got != "" {
		t.Errorf("0 banks → expected no note, got %q", got)
	}
}

// I5: losses at a German bank reach another bank's gains only with a
// Verlustbescheinigung requested by 15 December (§ 43a Abs. 3 S. 4 EStG).
func TestCrossBrokerNote_LossAtGermanBankNamesTheCertificateDeadline(t *testing.T) {
	loss := domesticBank("DKB")
	loss.ShareLosses = 800
	gain := domesticBank("ING")
	gain.ShareGains = 1200
	got := crossBrokerNote([]*bankIncome{loss, gain})
	for _, want := range []string{"Verlustbescheinigung", "15 December", "§ 43a Abs. 3 S. 4 EStG", "DKB", "ING", "Anlage KAP"} {
		if !strings.Contains(got, want) {
			t.Errorf("note %q should mention %q", got, want)
		}
	}
}

// A bank that absorbed its own losses has nothing to certify.
func TestCrossBrokerNote_LossesAbsorbedWithinBankUseGeneralNote(t *testing.T) {
	a := domesticBank("DKB")
	a.ShareLosses = 300
	a.ShareGains = 1000
	b := domesticBank("ING")
	b.Interest = 50
	got := crossBrokerNote([]*bankIncome{a, b})
	if !strings.Contains(got, "2 banks") || !strings.Contains(got, "Sparer-Pauschbetrag") {
		t.Errorf("note %q should name the bank count and the Sparer-Pauschbetrag", got)
	}
	if strings.Contains(got, "Losses at DKB") {
		t.Errorf("DKB has no unused losses, note should not point at it: %q", got)
	}
}

// Foreign brokers issue no Verlustbescheinigung; their losses are declared on
// lines 22/23 directly.
func TestCrossBrokerNote_ForeignLossesNeedNoCertificateRequest(t *testing.T) {
	ms := foreignBank("Morgan Stanley")
	ms.ShareLosses = 700
	ing := domesticBank("ING")
	ing.ShareGains = 2000
	got := crossBrokerNote([]*bankIncome{ms, ing})
	if strings.Contains(got, "Ask Morgan Stanley") {
		t.Errorf("a foreign broker issues no Verlustbescheinigung, got %q", got)
	}
	if got == "" {
		t.Error("two banks should still get the general note")
	}
}

func TestCrossBrokerNote_IsEnglish(t *testing.T) {
	a := domesticBank("DKB")
	a.OtherLosses = 100
	b := domesticBank("ING")
	b.Interest = 500
	c := foreignBank("Revolut")
	c.Interest = 10
	for _, banks := range [][]*bankIncome{{a, b}, {b, c}, {a, b, c}} {
		got := crossBrokerNote(banks)
		for _, german := range []string{"können", "Verluste bei", "Einkünfte aus", "einreichen", "lohnt sich"} {
			if strings.Contains(got, german) {
				t.Errorf("note %q mixes German prose (%q) into the English UI", got, german)
			}
		}
	}
}

func TestCrossBrokerNote_FiveBanksIncludesCount(t *testing.T) {
	var banks []*bankIncome
	for _, n := range []string{"ING", "DKB", "N26", "Sparkasse", "comdirect"} {
		b := domesticBank(n)
		b.Interest = 10
		banks = append(banks, b)
	}
	if got := crossBrokerNote(banks); !strings.Contains(got, "5 banks") {
		t.Errorf("expected the note to mention 5 banks, got %q", got)
	}
}
