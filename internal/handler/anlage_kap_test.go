package handler

import (
	"math"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/lnsp/wealth/internal/analytics"
	db "github.com/lnsp/wealth/internal/database/generated"
)

// These tests run the real Anlage KAP mapping (computeAnlageKAP, the function
// HandleAnlageKAP serves) on synthetic transactions.

func testAccount(name, institution string) db.Account {
	return db.Account{ID: uuid.New(), Name: name, Institution: institution, Type: "brokerage", TaxTreatment: "taxable", IsActive: true}
}

// ttx builds a chronological tax transaction. Funds get their Teilfreistellung
// rate set explicitly, as buildTaxTransactions does for known securities.
func ttx(a db.Account, date, typ, isin, assetClass string, qty, amount, tfs float64) analytics.TaxTransaction {
	year, _ := strconv.Atoi(date[:4])
	return analytics.TaxTransaction{
		AccountID:      a.ID.String(),
		Date:           date,
		Year:           year,
		Type:           typ,
		ISIN:           isin,
		Quantity:       qty,
		Amount:         amount,
		AssetClass:     assetClass,
		TFS:            tfs,
		HasExplicitTFS: true,
		IsEquityFund:   tfs >= analytics.TeilfreistellungEquity,
	}
}

func kapFor(t *testing.T, year int, accounts []db.Account, txns []analytics.TaxTransaction, withheld []withheldTax, priorVP []accountVP, joint bool, church float64) anlageKAPResult {
	t.Helper()
	return computeAnlageKAP(anlageKAPInput{
		capitalIncomeInput: capitalIncomeInput{
			Year:     year,
			Events:   realiseCapitalEvents(txns),
			Withheld: withheld,
			PriorVP:  priorVP,
			Accounts: accounts,
		},
		Joint:      joint,
		ChurchRate: church,
	})
}

func findLine(lines []kapLine, n int) (kapLine, bool) {
	for _, l := range lines {
		if l.Line == n {
			return l, true
		}
	}
	return kapLine{}, false
}

func wantLine(t *testing.T, lines []kapLine, n int, want float64) {
	t.Helper()
	l, ok := findLine(lines, n)
	if !ok {
		t.Errorf("line %d missing, want %.2f", n, want)
		return
	}
	if math.Abs(l.Amount-want) > 0.005 {
		t.Errorf("line %d (%s) = %.2f, want %.2f", n, l.Description, l.Amount, want)
	}
}

func wantNoLine(t *testing.T, lines []kapLine, n int) {
	t.Helper()
	if l, ok := findLine(lines, n); ok {
		t.Errorf("line %d (%s) = %.2f should not be emitted", n, l.Description, l.Amount)
	}
}

const goog = "US02079K1079"

// The seed data: GOOG RSUs at Morgan Stanley, a US broker that withholds no
// German tax. 2025 has one sale at a gain of 6,870.82 and one at a loss of
// 692.94 (FIFO). That income belongs on lines 19-23, not 7ff, and line 16 is
// never prefilled; tax is (6,177.88 − 1,000) × 26.375 % = 1,365.67.
func TestAnlageKAP_ForeignBrokerSharesGoOnLines19To23(t *testing.T) {
	ms := testAccount("MS Google RSUs", "morgan_stanley")
	txns := []analytics.TaxTransaction{
		ttx(ms, "2024-01-10", "transfer", goog, "stock", 100, 10000, 0), // vest at 100
		ttx(ms, "2025-02-01", "sell", goog, "stock", 50, 11870.82, 0),   // cost 5,000 → +6,870.82
		ttx(ms, "2025-03-01", "transfer", goog, "stock", 10, 2500, 0),   // vest at 250
		ttx(ms, "2025-06-01", "sell", goog, "stock", 60, 6807.06, 0),    // cost 5,000 + 2,500 → −692.94
	}
	res := kapFor(t, 2025, []db.Account{ms}, txns, nil, nil, false, 0)

	wantLine(t, res.Lines, 19, 6177.88)
	wantLine(t, res.Lines, 20, 6870.82)
	wantLine(t, res.Lines, 23, 692.94)
	for _, n := range []int{7, 8, 12, 13, 14, 22, 37, 38, 50} {
		wantNoLine(t, res.Lines, n)
	}
	l16, ok := findLine(res.Lines, 16)
	if !ok || !l16.Blank || l16.Amount != 0 {
		t.Errorf("line 16 = %+v, want present, blank and 0 (never a computed 1,000)", l16)
	}
	if math.Abs(res.EstimatedTax-1365.67) > 0.01 {
		t.Errorf("estimated tax = %.2f, want 1,365.67 ((6,177.88 − 1,000) × 26.375 %%)", res.EstimatedTax)
	}
	if len(res.Brokers) != 1 || res.Brokers[0].Domestic || res.Brokers[0].BrokerName != "Morgan Stanley" {
		t.Errorf("brokers = %+v, want one foreign Morgan Stanley entry", res.Brokers)
	}
	if len(res.InvLines) != 0 {
		t.Errorf("no funds held, want no KAP-INV lines, got %+v", res.InvLines)
	}
	if res.LayoutNote != "" {
		t.Errorf("2025 layout is verified, want no layout note, got %q", res.LayoutNote)
	}
}

func TestAnlageKAP_LineDescriptionsMatchTheForm(t *testing.T) {
	ms := testAccount("MS", "morgan_stanley")
	txns := []analytics.TaxTransaction{
		ttx(ms, "2024-01-10", "buy", goog, "stock", 10, 1000, 0),
		ttx(ms, "2025-02-01", "sell", goog, "stock", 10, 2000, 0),
	}
	res := kapFor(t, 2025, []db.Account{ms}, txns, nil, nil, false, 0)
	l20, _ := findLine(res.Lines, 20)
	if !strings.Contains(l20.Description, "Gewinne aus Aktienveräußerungen") {
		t.Errorf("line 20 description = %q, want the share-gain wording", l20.Description)
	}
	for _, l := range res.Lines {
		if strings.Contains(l.Description, "Teilfreistellung") || strings.Contains(l.Description, "Steuerpflichtiger Betrag") {
			t.Errorf("line %d has an invented description %q", l.Line, l.Description)
		}
	}
}

func TestAnlageKAP_DividendOnEquityETFAtGermanBank_AppliesTeilfreistellung(t *testing.T) {
	sc := testAccount("Scalable", "scalable_capital")
	txns := []analytics.TaxTransaction{
		ttx(sc, "2025-03-01", "dividend", "IE00B4L5Y983", "etf", 0, 100, 0.30),
	}
	res := kapFor(t, 2025, []db.Account{sc}, txns, nil, nil, false, 0)
	wantLine(t, res.Lines, 7, 70)
	wantNoLine(t, res.Lines, 19)
	l7, _ := findLine(res.Lines, 7)
	if !l7.Estimate || !strings.Contains(l7.Hint, "Steuerbescheinigung") {
		t.Errorf("line 7 at a German bank must be flagged as an estimate to check against the certificate: %+v", l7)
	}
	if b := res.Brokers[0]; math.Abs(b.Dividends-70) > 0.01 || math.Abs(b.TeilfreiAmt-30) > 0.01 || !b.Domestic {
		t.Errorf("broker = %+v, want dividends 70, Teilfreistellung 30, domestic", b)
	}
	if len(res.InvLines) != 0 {
		t.Errorf("fund income at a German bank is on its certificate, not KAP-INV: %+v", res.InvLines)
	}
}

func TestAnlageKAP_DividendOnSingleStock_NoTeilfreistellung(t *testing.T) {
	ing := testAccount("ING Depot", "ing")
	txns := []analytics.TaxTransaction{ttx(ing, "2025-03-01", "dividend", goog, "stock", 0, 200, 0)}
	res := kapFor(t, 2025, []db.Account{ing}, txns, nil, nil, false, 0)
	wantLine(t, res.Lines, 7, 200)
	if res.Brokers[0].TeilfreiAmt != 0 {
		t.Errorf("stock dividend must not carry a Teilfreistellung, got %.2f", res.Brokers[0].TeilfreiAmt)
	}
}

// C3: the Teilfreistellung cuts fund losses as well as gains (§ 21 InvStG).
// Stock dividends 6,000 and an equity-ETF loss of 5,000: the deductible loss is
// 3,500, so (6,000 − 3,500 − 1,000) × 26.375 % = 395.63 is due, not 0.
func TestAnlageKAP_FundLossesReducedByTeilfreistellung(t *testing.T) {
	dkb := testAccount("DKB", "dkb")
	etf := "IE00B4L5Y983"
	txns := []analytics.TaxTransaction{
		ttx(dkb, "2024-01-10", "buy", etf, "etf", 100, 10000, 0.30),
		ttx(dkb, "2025-05-01", "sell", etf, "etf", 100, 5000, 0.30),
		ttx(dkb, "2025-06-01", "dividend", goog, "stock", 0, 6000, 0),
	}
	res := kapFor(t, 2025, []db.Account{dkb}, txns, nil, nil, false, 0)
	b := res.Brokers[0]
	if math.Abs(b.RealizedLoss-(-3500)) > 0.01 {
		t.Errorf("equity-ETF loss after Teilfreistellung = %.2f, want −3,500 (−5,000 × 0.70)", b.RealizedLoss)
	}
	if math.Abs(b.TeilfreiAmt-(-1500)) > 0.01 {
		t.Errorf("net Teilfreistellung = %.2f, want −1,500 (the exempt share of the loss)", b.TeilfreiAmt)
	}
	wantLine(t, res.Lines, 7, 2500)
	if math.Abs(res.EstimatedTax-395.63) > 0.01 {
		t.Errorf("estimated tax = %.2f, want 395.63", res.EstimatedTax)
	}
}

// Share losses are ring-fenced; equity-ETF losses are not share losses and go
// to the general pot, which also offsets share gains.
func TestAnlageKAP_LossPotsRoutedByAssetClass(t *testing.T) {
	rev := testAccount("Revolut", "revolut")
	etf := "IE00B4L5Y983"
	stock := "US0378331005"
	txns := []analytics.TaxTransaction{
		ttx(rev, "2024-01-10", "buy", etf, "etf", 10, 2000, 0.30),
		ttx(rev, "2024-01-10", "buy", stock, "stock", 10, 2000, 0),
		ttx(rev, "2024-01-10", "buy", goog, "stock", 10, 1000, 0),
		ttx(rev, "2025-03-01", "sell", etf, "etf", 10, 1000, 0.30),  // −1,000 gross, −700 after TFS
		ttx(rev, "2025-03-01", "sell", stock, "stock", 10, 1000, 0), // −1,000 share loss
		ttx(rev, "2025-04-01", "sell", goog, "stock", 10, 3000, 0),  // +2,000 share gain
	}
	res := kapFor(t, 2025, []db.Account{rev}, txns, nil, nil, false, 0)

	wantLine(t, res.Lines, 19, 1000) // 2,000 − 1,000; the fund is on KAP-INV
	wantLine(t, res.Lines, 20, 2000)
	wantLine(t, res.Lines, 23, 1000)
	wantNoLine(t, res.Lines, 22)
	wantLine(t, res.InvLines, 14, -1000) // Aktienfonds, before Teilfreistellung
	// Share pot 2,000 − 1,000 = 1,000; general loss 700 offsets it → 300.
	if math.Abs(res.NetTaxable-300) > 0.01 {
		t.Errorf("net taxable = %.2f, want 300", res.NetTaxable)
	}
	if res.EstimatedTax != 0 {
		t.Errorf("estimated tax = %.2f, want 0 (300 is below the allowance)", res.EstimatedTax)
	}
	if res.Brokers[0].Domestic {
		t.Errorf("Revolut must be treated as foreign: %+v", res.Brokers[0])
	}
}

// W7: the tax column at a German bank is KapESt + Soli + church tax.
func TestAnlageKAP_GermanWithholdingSplitWithChurchTax(t *testing.T) {
	cd := testAccount("comdirect", "comdirect")
	txns := []analytics.TaxTransaction{ttx(cd, "2025-03-01", "dividend", goog, "stock", 0, 1000, 0)}
	withheld := []withheldTax{{AccountID: cd.ID.String(), Year: 2025, Kind: "dividend", Amount: 279.95}}
	res := kapFor(t, 2025, []db.Account{cd}, txns, withheld, nil, false, 0.09)
	wantLine(t, res.Lines, 37, 244.50)
	wantLine(t, res.Lines, 38, 13.45)
	wantLine(t, res.Lines, 39, 22.00)
	wantNoLine(t, res.Lines, 41)

	noChurch := kapFor(t, 2025, []db.Account{cd}, txns, []withheldTax{{AccountID: cd.ID.String(), Year: 2025, Kind: "dividend", Amount: 263.75}}, nil, false, 0)
	wantLine(t, noChurch.Lines, 37, 250)
	wantLine(t, noChurch.Lines, 38, 13.75)
	wantNoLine(t, noChurch.Lines, 39)
}

// W7: at a foreign broker the tax column is foreign withholding (line 41),
// never German KapESt/Soli.
func TestAnlageKAP_ForeignWithholdingGoesOnLine41(t *testing.T) {
	ms := testAccount("MS", "morgan_stanley")
	txns := []analytics.TaxTransaction{ttx(ms, "2025-06-15", "dividend", goog, "stock", 0, 100, 0)}
	withheld := []withheldTax{{AccountID: ms.ID.String(), Year: 2025, Kind: "dividend", Amount: 15}}
	res := kapFor(t, 2025, []db.Account{ms}, txns, withheld, nil, false, 0)
	wantLine(t, res.Lines, 19, 100)
	wantLine(t, res.Lines, 41, 15)
	wantNoLine(t, res.Lines, 37)
	wantNoLine(t, res.Lines, 38)
	if math.Abs(res.TotalWithheld-15) > 0.01 || res.Brokers[0].WithheldTax != 0 || math.Abs(res.Brokers[0].ForeignTax-15) > 0.01 {
		t.Errorf("withholding = %.2f, broker %+v; want 15 € foreign tax, 0 German", res.TotalWithheld, res.Brokers[0])
	}
}

// W3: line 16 stays blank for joint filers too; the estimate uses 2,000 €.
func TestAnlageKAP_JointFilingAllowance(t *testing.T) {
	ms := testAccount("MS", "morgan_stanley")
	txns := []analytics.TaxTransaction{
		ttx(ms, "2024-01-10", "buy", goog, "stock", 10, 1000, 0),
		ttx(ms, "2025-02-01", "sell", goog, "stock", 10, 4000, 0), // +3,000
	}
	single := kapFor(t, 2025, []db.Account{ms}, txns, nil, nil, false, 0)
	joint := kapFor(t, 2025, []db.Account{ms}, txns, nil, nil, true, 0)
	if single.Allowance != 1000 || joint.Allowance != 2000 {
		t.Errorf("allowance single/joint = %.0f/%.0f, want 1,000/2,000", single.Allowance, joint.Allowance)
	}
	if math.Abs(joint.EstimatedTax-1000*analytics.EffectiveTaxRate) > 0.01 {
		t.Errorf("joint estimated tax = %.2f, want %.2f", joint.EstimatedTax, 1000*analytics.EffectiveTaxRate)
	}
	if l, _ := findLine(joint.Lines, 16); !l.Blank || l.Amount != 0 {
		t.Errorf("joint line 16 = %+v, want blank", l)
	}
}

func TestAnlageKAP_UnusedLossesAtGermanBank(t *testing.T) {
	sc := testAccount("Scalable", "scalable_capital")
	stock := "US0378331005"
	txns := []analytics.TaxTransaction{
		ttx(sc, "2024-01-10", "buy", goog, "stock", 10, 1000, 0),
		ttx(sc, "2024-01-10", "buy", stock, "stock", 10, 1000, 0),
		ttx(sc, "2025-02-01", "sell", goog, "stock", 10, 1500, 0), // +500
		ttx(sc, "2025-02-01", "sell", stock, "stock", 10, 200, 0), // −800
		ttx(sc, "2025-03-01", "dividend", "IE00B4L5Y983", "etf", 0, 100, 0.30),
	}
	res := kapFor(t, 2025, []db.Account{sc}, txns, nil, nil, false, 0)
	wantLine(t, res.Lines, 7, 70)
	wantNoLine(t, res.Lines, 8)
	wantLine(t, res.Lines, 13, 300)
	l13, _ := findLine(res.Lines, 13)
	if !strings.Contains(l13.Hint, "Verlustbescheinigung") || !strings.Contains(l13.Hint, "15 December") {
		t.Errorf("line 13 hint must explain the Verlustbescheinigung deadline, got %q", l13.Hint)
	}
}

func TestAnlageKAP_FundsAtForeignBrokerGoOnKAPINV(t *testing.T) {
	rev := testAccount("Revolut", "revolut")
	equity, mixed := "IE00B4L5Y983", "IE00BMVB5R75"
	txns := []analytics.TaxTransaction{
		ttx(rev, "2025-03-01", "dividend", equity, "etf", 0, 100, 0.30),
		ttx(rev, "2025-03-01", "dividend", mixed, "etf", 0, 40, 0.15),
	}
	vp := []accountVP{{AccountID: rev.ID.String(), ISIN: equity, TFS: 0.30, Amount: 50}}
	res := kapFor(t, 2025, []db.Account{rev}, txns, nil, vp, false, 0)
	wantLine(t, res.InvLines, 4, 100) // Aktienfonds distributions, gross
	wantLine(t, res.InvLines, 5, 40)  // Mischfonds
	wantLine(t, res.InvLines, 9, 50)  // Vorabpauschale Aktienfonds
	wantNoLine(t, res.Lines, 19)
	// After Teilfreistellung: 70 + 34 + 35 = 139.
	if math.Abs(res.NetTaxable-139) > 0.01 {
		t.Errorf("net taxable = %.2f, want 139", res.NetTaxable)
	}
}

func TestAnlageKAP_PensionWrappersAndPrivateSalesExcluded(t *testing.T) {
	bav := testAccount("bAV", "other")
	bav.TaxTreatment = "bav"
	dl := testAccount("Delta", "delta")
	txns := []analytics.TaxTransaction{
		ttx(bav, "2025-03-01", "dividend", "IE00B4L5Y983", "etf", 0, 500, 0.30),
		ttx(dl, "2023-01-10", "buy", "XX_BTC", "crypto", 1, 10000, 0),
		ttx(dl, "2025-03-01", "sell", "XX_BTC", "crypto", 1, 30000, 0),
	}
	res := kapFor(t, 2025, []db.Account{bav, dl}, txns, nil, nil, false, 0)
	if len(res.Brokers) != 0 || res.NetTaxable != 0 {
		t.Errorf("brokers = %+v, net = %.2f; pension wrappers and § 23 crypto must stay off Anlage KAP", res.Brokers, res.NetTaxable)
	}
	if len(res.Lines) != 1 || res.Lines[0].Line != 16 {
		t.Errorf("lines = %+v, want only the blank line 16", res.Lines)
	}
}

func TestAnlageKAP_DepotTransferIsNotASale(t *testing.T) {
	a := testAccount("ING", "ing")
	b := testAccount("DKB", "dkb")
	txns := []analytics.TaxTransaction{
		ttx(a, "2024-01-10", "buy", goog, "stock", 10, 1000, 0),
		ttx(a, "2025-02-01", "transfer_out", goog, "stock", 10, 0, 0),
		ttx(b, "2025-02-01", "transfer", goog, "stock", 10, 1000, 0),
	}
	res := kapFor(t, 2025, []db.Account{a, b}, txns, nil, nil, false, 0)
	if len(res.Brokers) != 0 {
		t.Errorf("a depot transfer must not realise gains or losses, got %+v", res.Brokers)
	}
}

func TestAnlageKAP_LayoutNoteForUnverifiedYears(t *testing.T) {
	res := kapFor(t, 2026, nil, nil, nil, nil, false, 0)
	if !strings.Contains(res.LayoutNote, "2026") {
		t.Errorf("layout note = %q, want a hint to match lines by description", res.LayoutNote)
	}
	if old := kapFor(t, 2022, nil, nil, nil, nil, false, 0); old.Allowance != 801 {
		t.Errorf("2022 allowance = %.0f, want 801", old.Allowance)
	}
}

func TestAnlageKAP_ResponseShape(t *testing.T) {
	ms := testAccount("MS", "morgan_stanley")
	body := kapFor(t, 2025, []db.Account{ms}, nil, nil, nil, false, 0).response()
	for _, key := range []string{"year", "brokers", "anlage_kap", "anlage_kap_inv", "estimated_tax", "total_withheld", "balance_due", "allowance", "joint", "cross_broker_note"} {
		if _, ok := body[key]; !ok {
			t.Errorf("response is missing %q", key)
		}
	}
	if _, ok := body["anlage_kap_inv"].([]kapLine); !ok {
		t.Errorf("anlage_kap_inv must be a (possibly empty) slice, got %T", body["anlage_kap_inv"])
	}
}

func TestAccountTaxBank_Domicile(t *testing.T) {
	cases := []struct {
		inst     string
		domestic bool
		assumed  bool
	}{
		{"sparkasse", true, false}, {"ing", true, false}, {"dkb", true, false},
		{"comdirect", true, false}, {"scalable_capital", true, false},
		{"trade_republic", true, false}, {"n26", true, false},
		{"morgan_stanley", false, false}, {"revolut", false, false},
		{"delta", false, true}, {"manual", false, true}, {"other", false, true},
	}
	for _, c := range cases {
		tb := accountTaxBank(testAccount("x", c.inst))
		if tb.Domestic != c.domestic || tb.Assumed != c.assumed {
			t.Errorf("%s: domestic=%v assumed=%v, want %v/%v", c.inst, tb.Domestic, tb.Assumed, c.domestic, c.assumed)
		}
	}
	// Two accounts at one bank share one Freistellungsauftrag.
	if accountTaxBank(testAccount("a", "ing")).Key != accountTaxBank(testAccount("b", "ing")).Key {
		t.Error("accounts at the same institution must group into one bank")
	}
	if accountTaxBank(testAccount("a", "other")).Key == accountTaxBank(testAccount("b", "other")).Key {
		t.Error("accounts with an unknown institution must stay separate")
	}
}

func TestNetCapitalIncome_LossPots(t *testing.T) {
	// Share losses only offset share gains; general losses offset anything.
	n := netCapitalIncome(1000, 0, 0, 500)
	if n.Net != 1000 || n.UnusedShare != 500 {
		t.Errorf("share loss vs dividends: %+v, want net 1,000 and 500 share loss carried", n)
	}
	n = netCapitalIncome(0, 500, 1000, 0)
	if n.Net != 500 || n.ShareGain != 500 || n.UnusedGeneral != 0 {
		t.Errorf("general loss vs share gain: %+v, want net 500", n)
	}
	n = netCapitalIncome(200, 1000, 300, 100)
	if math.Abs(n.Net) > 1e-9 || math.Abs(n.UnusedGeneral-600) > 1e-9 {
		t.Errorf("general loss larger than all income: %+v, want net 0, 600 carried", n)
	}
}

func TestCSVField_QuotesSeparators(t *testing.T) {
	cases := map[string]string{
		"Alphabet Inc. Class C": "Alphabet Inc. Class C",
		"A; B":                  `"A; B"`,
		`Say "hi"`:              `"Say ""hi"""`,
	}
	for in, want := range cases {
		if got := csvField(in); got != want {
			t.Errorf("csvField(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAnlageKAP_TeilfreistellungConstantIs30Percent(t *testing.T) {
	if math.Abs(analytics.TeilfreistellungEquity-0.30) > 1e-9 {
		t.Errorf("TeilfreistellungEquity = %.4f, want 0.30", analytics.TeilfreistellungEquity)
	}
}
