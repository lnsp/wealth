package handler

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/lnsp/wealth/internal/database/generated"
	"github.com/lnsp/wealth/internal/market"
	"github.com/lnsp/wealth/internal/parser"
)

// Regression tests for the 2026-11-18 calculation audit, Transactions &
// Settings (docs/calc-audit-2026-11-18.md).

func auditDay(s string) time.Time {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(err)
	}
	return t
}

func auditText(s string) pgtype.Text { return pgtype.Text{String: s, Valid: s != ""} }

// Root cause 1: rows with a signed amount are normalised at import so the
// stored magnitude plus type gives the same cash as txnSignedCashDelta.
func TestNormalizeTxnSigns(t *testing.T) {
	cases := []struct {
		inType            string
		inAmt, fee, tax   float64
		wantType          string
		wantAmt, wantCash float64
	}{
		{"withdrawal", -1300, 0, 0, "withdrawal", 1300, -1300},
		{"withdrawal", 95, 0, 0, "withdrawal", 95, -95},
		{"interest", -4.12, 0, 0, "fee", 4.12, -4.12},
		{"interest", 3.75, 0, 0, "interest", 3.75, 3.75},
		{"deposit", -50, 0, 0, "withdrawal", 50, -50},
		{"cash_transfer_in", -10, 0, 0, "cash_transfer_out", 10, -10},
		{"dividend", -12, 0, 0, "fee", 12, -12},
		{"dividend", 100, 0, -15, "dividend", 100, 85},
		{"buy", -600, -1.49, 0, "buy", 600, -601.49},
		{"sell", -360, 0.99, 0, "sell", 360, 359.01},
		{"fee", -2.5, 0, 0, "fee", 2.5, -2.5},
	}
	for _, c := range cases {
		txn := parser.Transaction{Type: c.inType, Amount: c.inAmt, Fee: c.fee, Tax: c.tax, ImportHash: "h"}
		normalizeTxnSigns(&txn)
		if txn.Type != c.wantType || txn.Amount != c.wantAmt || txn.Fee < 0 || txn.Tax < 0 {
			t.Errorf("%s %.2f: got %s %.2f fee %.2f tax %.2f, want %s %.2f with non-negative fee/tax",
				c.inType, c.inAmt, txn.Type, txn.Amount, txn.Fee, txn.Tax, c.wantType, c.wantAmt)
		}
		if txn.ImportHash != "h" {
			t.Errorf("%s: import hash changed", c.inType)
		}
		if got := txnSignedCashDelta(txn.Type, false, txn.Amount, txn.Fee, txn.Tax); math.Abs(got-c.wantCash) > 1e-9 {
			t.Errorf("%s %.2f: cash delta %.2f, want %.2f", c.inType, c.inAmt, got, c.wantCash)
		}
	}
}

// C4: Scalable rows reclassified by sign are still recognised under the type
// earlier versions stored, so a re-import under an older hash adopts them.
func TestLegacyImportTypes(t *testing.T) {
	has := func(list []string, v string) bool {
		for _, s := range list {
			if s == v {
				return true
			}
		}
		return false
	}
	for _, c := range []struct{ now, before string }{
		{"tax", "fee"}, {"deposit", "fee"}, {"deposit", "interest"}, {"withdrawal", "deposit"}, {"fee", "interest"},
	} {
		if !has(legacyImportTypes("scalable_capital", c.now), c.before) {
			t.Errorf("scalable %s: %q not tried", c.now, c.before)
		}
	}
	if got := legacyImportTypes("n26", "fee"); got != nil {
		t.Errorf("n26 fee: %v, want none (migration 032 rewrites those rows)", got)
	}
	if got := legacyImportTypes("scalable_capital", "buy"); got != nil {
		t.Errorf("scalable buy: %v, want none", got)
	}
}

// I5: a re-imported row only counts as "updated" when the upsert changes it.
func TestUpsertChanges(t *testing.T) {
	stored := db.GetTransactionImportStateRow{Type: "fee", Quantity: mustNumeric("0"), Price: mustNumeric("0")}
	same := db.InsertTransactionParams{Type: "fee", Quantity: numericFromFloat(0), Price: numericFromFloat(0)}
	if upsertChanges(stored, same) {
		t.Error("identical re-import counted as updated")
	}
	retyped := same
	retyped.Type = "tax"
	if !upsertChanges(stored, retyped) {
		t.Error("type reclassification not counted as updated")
	}
	withISIN := same
	withISIN.SecurityISIN = auditText("US02079K1079")
	if !upsertChanges(stored, withISIN) {
		t.Error("new security not counted as updated")
	}
	sec := db.GetTransactionImportStateRow{Type: "sell", SecurityISIN: auditText("US02079K1079"), Quantity: mustNumeric("39.972"), Price: mustNumeric("96.01")}
	secSame := db.InsertTransactionParams{Type: "sell", SecurityISIN: auditText("US02079K1079"), Quantity: numericFromFloat(39.972), Price: numericFromFloat(96.01)}
	if upsertChanges(sec, secSame) {
		t.Error("unchanged security row counted as updated")
	}
	secNoQty := secSame
	secNoQty.Quantity = pgtype.Numeric{}
	if upsertChanges(sec, secNoQty) {
		t.Error("a missing incoming quantity keeps the stored one; not an update")
	}
}

func auditTxn(date, typ, isin string, qty, amount, fee, tax float64, cur string) db.ListTransactionsRow {
	return db.ListTransactionsRow{
		ID: uuid.New(), AccountID: uuid.Nil, Date: auditDay(date), Type: typ, SecurityISIN: auditText(isin),
		Quantity: numericFromFloat(qty), Amount: numericFromFloat(amount),
		Fee: numericFromFloat(fee), Tax: numericFromFloat(tax), Currency: cur,
	}
}

func auditSnap(date string, total float64) db.NetWorthSnapshot {
	return db.NetWorthSnapshot{Date: auditDay(date), Total: numericFromFloat(total)}
}

// C1 / I9: the report opens at the previous period's close, splits the change
// into deposits and market, and is computed from the snapshots it is given.
func TestBuildReportData_PreviousCloseAndDepositSplit(t *testing.T) {
	in := reportInputs{
		start: auditDay("2026-10-01"), end: auditDay("2026-10-31"),
		snaps: []db.NetWorthSnapshot{
			auditSnap("2016-08-25", 0), // placeholder before any data
			auditSnap("2026-09-30", 156614.57),
			auditSnap("2026-10-01", 157059.92),
			auditSnap("2026-10-31", 157246.25),
			auditSnap("2026-11-01", 157693.13),
		},
		txns: []db.ListTransactionsRow{ // newest first
			auditTxn("2026-11-01", "deposit", "", 0, 5200, 0, 0, "EUR"),
			auditTxn("2026-10-14", "withdrawal", "", 0, 1300, 0, 0, "EUR"),
			auditTxn("2026-10-01", "deposit", "", 0, 5200, 0, 0, "EUR"),
			auditTxn("2026-09-30", "deposit", "", 0, 999, 0, 0, "EUR"), // in the opening value
		},
		fx: newReportFX(nil, map[string]float64{"EUR": 1}),
	}
	got := buildReportData(in)
	if !got.HasNetWorth {
		t.Fatal("HasNetWorth = false, want true")
	}
	if got.StartDate != "2026-09-30" || got.NetWorthStart != 156614.57 {
		t.Errorf("start = %s %.2f, want previous close 2026-09-30 156614.57", got.StartDate, got.NetWorthStart)
	}
	if got.EndDate != "2026-10-31" || got.NetWorthEnd != 157246.25 {
		t.Errorf("end = %s %.2f, want 2026-10-31 157246.25", got.EndDate, got.NetWorthEnd)
	}
	if got.NetWorthChange != 631.68 {
		t.Errorf("change = %.2f, want 631.68", got.NetWorthChange)
	}
	if got.NetDeposits != 3900 {
		t.Errorf("net deposits = %.2f, want 3900 (5200 in, 1300 out; Sep 30 and Nov 1 outside)", got.NetDeposits)
	}
	if got.MarketChange != round2(631.68-3900) {
		t.Errorf("market = %.2f, want %.2f", got.MarketChange, round2(631.68-3900))
	}
	if got.NewTransactions != 2 {
		t.Errorf("transactions in period = %d, want 2", got.NewTransactions)
	}
}

func TestBuildReportData_NoHistoryBeforeFirstNonZeroSnapshot(t *testing.T) {
	in := reportInputs{
		start: auditDay("2022-11-01"), end: auditDay("2022-11-30"),
		snaps: []db.NetWorthSnapshot{auditSnap("2022-11-15", 0), auditSnap("2022-12-25", 4000)},
		fx:    newReportFX(nil, nil),
	}
	if got := buildReportData(in); got.HasNetWorth || got.NetWorthEnd != 0 {
		t.Errorf("placeholder-only period: HasNetWorth=%v end=%.2f, want false/0", got.HasNetWorth, got.NetWorthEnd)
	}
}

// I9: dividends use the FX rate of the payment date, gross and net are both
// reported, and a currency without any rate is left out with a warning
// instead of being counted 1:1.
func TestBuildReportData_DividendsFXAndWithholding(t *testing.T) {
	hist := []market.HistoricalFXRate{
		{Date: auditDay("2026-10-01"), Currency: "USD", Rate: 1.25},
		{Date: auditDay("2026-10-14"), Currency: "USD", Rate: 1.10},
		{Date: auditDay("2026-10-20"), Currency: "USD", Rate: 1.00},
	}
	in := reportInputs{
		start: auditDay("2026-10-01"), end: auditDay("2026-10-31"),
		txns: []db.ListTransactionsRow{
			auditTxn("2026-10-16", "dividend", "CH0012345678", 0, 50, 0, 0, "CHF"), // no CHF rate at all
			auditTxn("2026-10-15", "dividend", "US0378331005", 0, 110, 0, 16.5, "USD"),
			auditTxn("2026-10-02", "dividend", "IE00B3RBWM25", 0, 20, 0, 0, "EUR"),
		},
		fx: newReportFX(hist, map[string]float64{"EUR": 1, "USD": 1.00}),
	}
	got := buildReportData(in)
	// 110 USD at 1.10 (rate of 10-14, the last business day before 10-15) = 100 €
	if got.DividendsGross != 120 {
		t.Errorf("gross = %.2f, want 120 (100 € from USD + 20 €)", got.DividendsGross)
	}
	if got.DividendsWithheld != 15 {
		t.Errorf("withheld = %.2f, want 15 (16.50 USD at 1.10)", got.DividendsWithheld)
	}
	if got.TotalDividends != 105 {
		t.Errorf("net dividends = %.2f, want 105", got.TotalDividends)
	}
	if len(got.Warnings) != 1 || !strings.Contains(got.Warnings[0], "CHF") {
		t.Errorf("warnings = %v, want one naming CHF", got.Warnings)
	}
}

// I9: a single holding is not both the best and the weakest.
func TestBuildReportData_SingleHoldingHasNoBestOrWeakest(t *testing.T) {
	in := reportInputs{
		start: auditDay("2026-10-01"), end: auditDay("2026-10-31"),
		txns: []db.ListTransactionsRow{
			auditTxn("2026-03-01", "buy", "IE00B3RBWM25", 10, 1000, 0, 0, "EUR"),
		},
		fx: newReportFX(nil, nil),
		priceAt: func(string, time.Time) (float64, string, bool) {
			return 120, "EUR", true
		},
	}
	got := buildReportData(in)
	if len(got.Holdings) != 1 || got.Holdings[0].Value != 1200 || got.Holdings[0].ReturnPct != 20 {
		t.Fatalf("holdings = %+v, want one at 1200 € (+20 %%)", got.Holdings)
	}
	if got.TopGainer != nil || got.TopLoser != nil {
		t.Errorf("single holding shown as best/weakest: %+v / %+v", got.TopGainer, got.TopLoser)
	}

	in.txns = append([]db.ListTransactionsRow{
		auditTxn("2026-04-01", "buy", "US0378331005", 1, 200, 0, 0, "EUR"),
	}, in.txns...)
	got = buildReportData(in)
	if got.TopGainer == nil || got.TopLoser == nil || got.TopGainer.ISIN != "IE00B3RBWM25" || got.TopLoser.ISIN != "US0378331005" {
		t.Errorf("two holdings: best %+v weakest %+v", got.TopGainer, got.TopLoser)
	}
}

// W5: the export quotes fields that contain the separator, so a counterparty
// such as "Miete; NK" re-imports as one column; plain rows keep the exact
// bytes (and import hashes) of the previous Sprintf export.
func TestWriteTransactionsCSV_QuotesAndRoundTrips(t *testing.T) {
	rows := []db.ListAllTransactionsForExportRow{
		{Date: auditDay("2026-05-01"), Type: "withdrawal", AccountName: "Checking", Counterparty: auditText(`Miete; NK "Mai"`),
			Amount: mustNumeric("850"), Fee: mustNumeric("0"), Tax: mustNumeric("0"), Currency: "EUR", Reference: auditText("Mai; 2026")},
		{Date: auditDay("2026-02-01"), Type: "buy", AccountName: "Brokerage", Counterparty: auditText("Trade Republic"),
			SecurityISIN: auditText("IE00B3RBWM25"), Quantity: mustNumeric("10.5"), Price: mustNumeric("119.5"),
			Amount: mustNumeric("1254.75"), Fee: mustNumeric("0.99"), Tax: mustNumeric("0"), Currency: "EUR", Reference: auditText("TR-OR-001")},
	}
	var buf bytes.Buffer
	if err := writeTransactionsCSV(&buf, rows); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.HasPrefix(out, "\xEF\xBB\xBFdate;type;account;counterparty;security_isin;quantity;price;amount;fee;tax;currency;reference\n") {
		t.Fatalf("missing BOM or header: %q", out[:min(len(out), 120)])
	}
	legacy := fmt.Sprintf("%s;%s;%s;%s;%s;%.8f;%.8f;%.4f;%.4f;%.4f;%s;%s\n",
		"2026-02-01", "buy", "Brokerage", "Trade Republic", "IE00B3RBWM25", 10.5, 119.5, 1254.75, 0.99, 0.0, "EUR", "TR-OR-001")
	if !strings.Contains(out, legacy) {
		t.Errorf("plain row changed bytes; want %q in\n%s", legacy, out)
	}

	acct := uuid.New()
	txns, _, result, err := parser.ParseCSV(buf.Bytes(), acct)
	if err != nil {
		t.Fatalf("re-import: %v", err)
	}
	if result.Institution != "wealth_export" || len(txns) != 2 {
		t.Fatalf("re-import: institution %q, %d rows", result.Institution, len(txns))
	}
	if txns[0].Counterparty != `Miete; NK "Mai"` || txns[0].Reference != "Mai; 2026" || txns[0].Amount != 850 || txns[0].Currency != "EUR" {
		t.Errorf("quoted row re-imported as %+v", txns[0])
	}
	if txns[1].SecurityISIN != "IE00B3RBWM25" || txns[1].Quantity != 10.5 {
		t.Errorf("plain row re-imported as %+v", txns[1])
	}
}

// Root cause 1 / C2: the SQL cash queries agree with txnSignedCashDelta for
// every account. Runs read-only against a real database when
// WEALTH_TEST_DATABASE_URL is set (e.g. the seeded local DB, whose negative
// "Test Checking" withdrawals made SQL cash 33,328.97 instead of 18,671.03).
func TestCashQueriesMatchTxnSignedCashDelta(t *testing.T) {
	url := os.Getenv("WEALTH_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("WEALTH_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close(ctx)
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		t.Fatalf("begin read-only: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := db.New(tx)

	accounts, err := q.ListAllAccounts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	all, err := q.ListAllAccountCashByCurrency(ctx)
	if err != nil {
		t.Fatal(err)
	}
	allByKey := map[string]float64{}
	for _, r := range all {
		allByKey[r.AccountID.String()+"|"+r.Currency] = numericToFloat(r.Balance)
	}
	for _, a := range accounts {
		rows, err := q.ListTransactionsByAccount(ctx, db.ListTransactionsByAccountParams{AccountID: a.ID, Limit: 1 << 30})
		if err != nil {
			t.Fatal(err)
		}
		want := map[string]float64{}
		for _, r := range rows {
			want[r.Currency] += txnSignedCashDelta(r.Type, r.SecurityISIN.Valid,
				numericToFloat(r.Amount), numericToFloat(r.Fee), numericToFloat(r.Tax))
		}
		got, err := q.GetAccountCashByCurrency(ctx, a.ID)
		if err != nil {
			t.Fatal(err)
		}
		for _, g := range got {
			if bal := numericToFloat(g.Balance); math.Abs(bal-want[g.Currency]) > 0.005 {
				t.Errorf("%s %s: GetAccountCashByCurrency %.2f, txnSignedCashDelta %.2f", a.Name, g.Currency, bal, want[g.Currency])
			}
			if a.IsActive {
				if bal := allByKey[a.ID.String()+"|"+g.Currency]; math.Abs(bal-want[g.Currency]) > 0.005 {
					t.Errorf("%s %s: ListAllAccountCashByCurrency %.2f, txnSignedCashDelta %.2f", a.Name, g.Currency, bal, want[g.Currency])
				}
			}
		}
		if len(want) == 1 {
			for cur, w := range want {
				bal, err := q.GetCashBalance(ctx, a.ID)
				if err != nil {
					t.Fatal(err)
				}
				if b := numericToFloat(bal); w >= 0 && math.Abs(b-w) > 0.005 {
					t.Errorf("%s %s: GetCashBalance %.2f, txnSignedCashDelta %.2f", a.Name, cur, b, w)
				}
			}
		}
	}
}
