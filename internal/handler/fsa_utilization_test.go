package handler

import (
	"math"
	"testing"

	"github.com/lnsp/wealth/internal/analytics"
	db "github.com/lnsp/wealth/internal/database/generated"
)

// These tests run computeFSAStatus, the function HandleFSAStatus serves.

func fsaFor(t *testing.T, year int, accounts []db.Account, txns []analytics.TaxTransaction, vpEvents []analytics.TaxTransaction, joint bool, months int) fsaResult {
	t.Helper()
	return computeFSAStatus(fsaInput{
		capitalIncomeInput: capitalIncomeInput{
			Year:     year,
			Ledger:   analytics.MergeVorabpauschaleEvents(txns, vpEvents),
			Accounts: accounts,
		},
		Joint:  joint,
		Months: months,
	})
}

func approx(a, b float64) bool { return math.Abs(a-b) < 0.006 }

// C10: a bank nets gains and losses before applying the Freistellungsauftrag
// (§ 43a Abs. 3 EStG). Gain 900 and loss 600 at one bank use 300, leaving 700
// unused, worth 184.63 € — not "900 used, 100 unused, worth 26.38".
func TestFSAStatus_NetsLossesWithinBank(t *testing.T) {
	ing := testAccount("ING Depot", "ing")
	a, b := "DE0007164600", "DE0007236101"
	txns := []analytics.TaxTransaction{
		ttx(ing, "2026-01-10", "buy", a, "stock", 10, 1000, 0),
		ttx(ing, "2026-01-10", "buy", b, "stock", 10, 1000, 0),
		ttx(ing, "2026-03-01", "sell", a, "stock", 10, 1900, 0), // +900
		ttx(ing, "2026-04-01", "sell", b, "stock", 10, 400, 0),  // −600
	}
	res := fsaFor(t, 2026, []db.Account{ing}, txns, nil, false, 11)
	if !approx(res.Used, 300) || !approx(res.Remaining, 700) {
		t.Fatalf("used/remaining = %.2f/%.2f, want 300/700", res.Used, res.Remaining)
	}
	if worth := res.Remaining * analytics.EffectiveTaxRate; !approx(worth, 184.63) {
		t.Errorf("unused allowance worth %.2f, want 184.63", worth)
	}
	if len(res.Banks) != 1 || !approx(res.Banks[0].Gains, 300) || !approx(res.Banks[0].Total, 300) || !res.Banks[0].AcceptsFSA {
		t.Errorf("bank row = %+v, want net gains 300, total 300, accepts a Freistellungsauftrag", res.Banks)
	}
}

// Teilfreistellung applies to both sides before netting.
func TestFSAStatus_FundLossNettedAfterTeilfreistellung(t *testing.T) {
	dkb := testAccount("DKB", "dkb")
	etf := "IE00B4L5Y983"
	txns := []analytics.TaxTransaction{
		ttx(dkb, "2025-01-10", "buy", etf, "etf", 10, 2000, 0.30),
		ttx(dkb, "2026-03-01", "sell", etf, "etf", 10, 1000, 0.30), // −1,000 → −700
		ttx(dkb, "2026-04-01", "dividend", "DE0007164600", "stock", 0, 1000, 0),
	}
	res := fsaFor(t, 2026, []db.Account{dkb}, txns, nil, false, 12)
	if !approx(res.Used, 300) {
		t.Errorf("used = %.2f, want 300 (1,000 dividends − 700 fund loss)", res.Used)
	}
}

// Losses at another bank are not netted by the bank holding the gains; each
// bank's contribution is floored at 0.
func TestFSAStatus_LossAtOtherBankNotNetted(t *testing.T) {
	ing := testAccount("ING", "ing")
	dkb := testAccount("DKB", "dkb")
	txns := []analytics.TaxTransaction{
		ttx(ing, "2026-01-10", "buy", "A", "stock", 10, 1000, 0),
		ttx(dkb, "2026-01-10", "buy", "B", "stock", 10, 1000, 0),
		ttx(ing, "2026-03-01", "sell", "A", "stock", 10, 1900, 0), // +900
		ttx(dkb, "2026-04-01", "sell", "B", "stock", 10, 400, 0),  // −600
	}
	res := fsaFor(t, 2026, []db.Account{ing, dkb}, txns, nil, false, 12)
	if !approx(res.Used, 900) {
		t.Errorf("used = %.2f, want 900 (DKB's loss stays at DKB)", res.Used)
	}
}

func TestFSAStatus_JointFiler(t *testing.T) {
	ing := testAccount("ING", "ing")
	txns := []analytics.TaxTransaction{ttx(ing, "2026-03-01", "interest", "", "", 0, 500, 0)}
	res := fsaFor(t, 2026, []db.Account{ing}, txns, nil, true, 12)
	if res.Allowance != 2000 || !approx(res.Used, 500) || !approx(res.Remaining, 1500) || !approx(res.Utilization, 25) {
		t.Errorf("joint: allowance %.0f used %.2f remaining %.2f util %.2f, want 2,000/500/1,500/25", res.Allowance, res.Used, res.Remaining, res.Utilization)
	}
	single := fsaFor(t, 2026, []db.Account{ing}, txns, nil, false, 12)
	if single.Allowance != 1000 || !approx(single.Utilization, 50) {
		t.Errorf("single: allowance %.0f util %.2f, want 1,000/50", single.Allowance, single.Utilization)
	}
}

func TestFSAStatus_OverConsumedClamps(t *testing.T) {
	ing := testAccount("ING", "ing")
	txns := []analytics.TaxTransaction{ttx(ing, "2026-03-01", "interest", "", "", 0, 3000, 0)}
	for _, joint := range []bool{false, true} {
		res := fsaFor(t, 2026, []db.Account{ing}, txns, nil, joint, 12)
		if res.Used != res.Allowance || res.Remaining != 0 || res.Utilization != 100 {
			t.Errorf("joint=%v: used %.2f of %.0f, remaining %.2f, util %.2f; want fully used", joint, res.Used, res.Allowance, res.Remaining, res.Utilization)
		}
	}
}

func TestFSAStatus_ZeroIncomeAndYearAwareAllowance(t *testing.T) {
	res := fsaFor(t, 2026, nil, nil, nil, false, 12)
	if res.Used != 0 || res.Remaining != 1000 || res.Utilization != 0 || len(res.Banks) != 0 {
		t.Errorf("no income: %+v, want 0 used of 1,000", res)
	}
	if old := fsaFor(t, 2022, nil, nil, nil, true, 12); old.Allowance != 1602 {
		t.Errorf("2022 joint allowance = %.0f, want 1,602", old.Allowance)
	}
}

// C8: last year's Vorabpauschale counts as received in January and uses this
// year's allowance (after Teilfreistellung).
func TestFSAStatus_IncludesPriorYearVorabpauschale(t *testing.T) {
	sc := testAccount("Scalable", "scalable_capital")
	etf := "IE00B4L5Y983"
	txns := []analytics.TaxTransaction{ttx(sc, "2024-01-10", "buy", etf, "etf", 100, 10000, 0.30)}
	// 2025: 100 units × 100 € × 2.53 % × 0.7 = 177.10 €, received January 2026; 123.97 € after Teilfreistellung.
	vp := []analytics.TaxTransaction{vpEvent(2025, etf, 0.30, 100, 110)}
	res := fsaFor(t, 2026, []db.Account{sc}, txns, vp, false, 2)
	if !approx(res.Used, 123.97) || len(res.Banks) != 1 || !approx(res.Banks[0].Vorabpauschale, 123.97) {
		t.Errorf("used %.2f, banks %+v; want 123.97 from the 2025 Vorabpauschale", res.Used, res.Banks)
	}
	// It belongs to 2026, not to the year it was computed for.
	if prev := fsaFor(t, 2025, []db.Account{sc}, txns, vp, false, 12); prev.Used != 0 {
		t.Errorf("2025 used %.2f, want 0: the 2025 Vorabpauschale is received in January 2026", prev.Used)
	}
}

// W4: recommend per bank, only German banks, annualising only recurring income.
func TestFSAStatus_RecommendationPerBank(t *testing.T) {
	ing1 := testAccount("ING Depot", "ing")
	ing2 := testAccount("ING Tagesgeld", "ing")
	dkb := testAccount("DKB", "dkb")
	ms := testAccount("MS", "morgan_stanley")
	txns := []analytics.TaxTransaction{
		ttx(ing1, "2026-01-10", "buy", "A", "stock", 10, 1000, 0),
		ttx(ing1, "2026-05-01", "dividend", "A", "stock", 0, 55, 0),
		ttx(ing2, "2026-06-01", "interest", "", "", 0, 55, 0),
		ttx(ing1, "2026-10-01", "sell", "A", "stock", 10, 4000, 0), // +3,000, one-off
		ttx(dkb, "2026-07-01", "interest", "", "", 0, 110, 0),
		ttx(ms, "2025-01-10", "transfer", goog, "stock", 10, 1000, 0),
		ttx(ms, "2026-03-01", "sell", goog, "stock", 10, 3000, 0),
	}
	res := fsaFor(t, 2026, []db.Account{ing1, ing2, dkb, ms}, txns, nil, false, 11)

	if len(res.Recommendation) != 2 {
		t.Fatalf("recommendations = %+v, want ING and DKB only (Morgan Stanley cannot take one)", res.Recommendation)
	}
	got := map[string]fsaRecommendation{}
	sum := 0.0
	for _, r := range res.Recommendation {
		got[r.Account] = r
		sum += r.RecommendedFSA
	}
	// ING: 110 recurring × 12/11 = 120 + 3,000 realised = 3,120. DKB: 120.
	if !approx(got["ING"].ProjectedIncome, 3120) || !approx(got["DKB"].ProjectedIncome, 120) {
		t.Errorf("projections = %+v, want ING 3,120 and DKB 120", got)
	}
	if sum != res.Allowance {
		t.Errorf("recommended total = %.0f, want the full allowance %.0f", sum, res.Allowance)
	}
	if _, ok := got["Morgan Stanley"]; ok {
		t.Error("a foreign broker must not get a recommended Freistellungsauftrag")
	}
	// Morgan Stanley's gain still uses the allowance, through Anlage KAP.
	if !approx(res.ForeignIncome, 2000) {
		t.Errorf("foreign income = %.2f, want 2,000", res.ForeignIncome)
	}
	for _, b := range res.Banks {
		if b.AccountName == "Morgan Stanley" && b.AcceptsFSA {
			t.Error("Morgan Stanley must be flagged as not accepting a Freistellungsauftrag")
		}
		if b.AccountName == "ING" && len(b.Accounts) != 2 {
			t.Errorf("ING row should cover both ING accounts, got %v", b.Accounts)
		}
	}
}
