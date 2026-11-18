package handler

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/lnsp/wealth/internal/assets"
	db "github.com/lnsp/wealth/internal/database/generated"
	"github.com/lnsp/wealth/internal/market"
)

// cfNow is the audit date; the seed's newest month is May 2026.
var cfNow = time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)

func cfDate(s string) time.Time {
	d, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(err)
	}
	return d
}

func cfRow(date, typ string, amount float64, cp, ref string) cashRow {
	return cashRow{ID: uuid.New(), Date: cfDate(date), Type: typ, Amount: amount, Counterparty: cp, Reference: ref, AccountType: "checking"}
}

// cfSeed mirrors the seeded "Test Checking" account (withdrawals stored negative).
func cfSeed() []cashRow {
	return []cashRow{
		cfRow("2026-01-01", "deposit", 5200, "ACME GmbH", "Gehalt Januar"),
		cfRow("2026-01-03", "withdrawal", -1300, "WOHNUNG VERWALTUNG", "Miete Jan"),
		cfRow("2026-02-01", "deposit", 5200, "ACME GmbH", "Gehalt Februar"),
		cfRow("2026-02-04", "withdrawal", -1300, "WOHNUNG VERWALTUNG", "Miete Feb"),
		cfRow("2026-03-01", "deposit", 5200, "ACME GmbH", "Gehalt März"),
		cfRow("2026-03-03", "withdrawal", -1300, "WOHNUNG VERWALTUNG", "Miete März"),
		cfRow("2026-03-15", "withdrawal", -95, "EDEKA", "Grocery"),
		cfRow("2026-03-20", "withdrawal", -14.99, "SPOTIFY", "Family Plan"),
		cfRow("2026-04-01", "deposit", 5200, "ACME GmbH", "Gehalt April 2026"),
		cfRow("2026-04-03", "withdrawal", -1300, "WOHNUNG VERWALTUNG", "Miete April"),
		cfRow("2026-04-09", "withdrawal", -82, "REWE Markt", "EC Kauf"),
		cfRow("2026-04-14", "withdrawal", -240, "AMAZON.DE", "Bestellung"),
		cfRow("2026-04-22", "withdrawal", -14.99, "NETFLIX.COM", "Monatsabo"),
		cfRow("2026-05-01", "deposit", 5200, "ACME GmbH", "Gehalt Mai 2026"),
		cfRow("2026-05-03", "withdrawal", -1300, "WOHNUNG VERWALTUNG", "Miete Mai"),
		cfRow("2026-05-04", "withdrawal", -89, "STADTWERKE BERLIN", "Strom April"),
		cfRow("2026-05-07", "withdrawal", -14.99, "NETFLIX.COM", "Monatsabo"),
		cfRow("2026-05-09", "withdrawal", -67.4, "REWE Markt", "EC Kauf"),
		cfRow("2026-05-12", "withdrawal", -52.1, "EDEKA NORD", "EC Kauf"),
		cfRow("2026-05-14", "withdrawal", -38.5, "DB-VERTRIEB GMBH", "Ticket BER-MUC"),
		cfRow("2026-05-18", "withdrawal", -120, "ALLIANZ", "Haftpflicht"),
	}
}

func cfMonth(t *testing.T, resp cashflowResponse, month string) monthlyTotals {
	t.Helper()
	for _, m := range resp.Months {
		if m.Month == month {
			return m
		}
	}
	t.Fatalf("month %s not in response", month)
	return monthlyTotals{}
}

func cfNear(t *testing.T, what string, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 0.005 {
		t.Errorf("%s = %.2f, want %.2f", what, got, want)
	}
}

func cfSpend(m monthlyTotals) float64 { return m.Fixed + m.Variable }

// The seed months must come out exactly as before the classifier changes.
func TestBuildCashflow_SeedUnchanged(t *testing.T) {
	resp := buildCashflow(cfSeed(), nil, nil, cfNow, 12)

	want := []struct {
		month              string
		income, spend, net float64
	}{
		{"2026-01", 5200, 1300, 3900},
		{"2026-02", 5200, 1300, 3900},
		{"2026-03", 5200, 1409.99, 3790.01},
		{"2026-04", 5200, 1636.99, 3563.01},
		{"2026-05", 5200, 1681.99, 3518.01},
	}
	for _, w := range want {
		m := cfMonth(t, resp, w.month)
		cfNear(t, w.month+" income", m.Income, w.income)
		cfNear(t, w.month+" spend", cfSpend(m), w.spend)
		cfNear(t, w.month+" net", m.Net, w.net)
		if !m.HasData || m.Partial {
			t.Errorf("%s has_data=%v partial=%v, want true/false", w.month, m.HasData, m.Partial)
		}
	}

	// F7: the running month plus 12 complete months.
	if len(resp.Months) != 13 || resp.Months[0].Month != "2026-11" || !resp.Months[0].Partial {
		t.Fatalf("months = %d, first %s partial=%v; want 13 starting with partial 2026-11", len(resp.Months), resp.Months[0].Month, resp.Months[0].Partial)
	}
	if resp.Months[12].Month != "2025-11" {
		t.Errorf("oldest month = %s, want 2025-11", resp.Months[12].Month)
	}

	med := resp.Medians
	cfNear(t, "median income", med.MonthlyIncome, 5200)
	cfNear(t, "median spend", med.MonthlySpend, 1409.99)
	cfNear(t, "median net", med.MonthlySurplus, 3790.01)
	// F8: Σnet / Σincome = 18,671.03 / 26,000.
	if med.SavingsRatePct == nil {
		t.Fatal("savings rate is null")
	}
	cfNear(t, "savings rate", *med.SavingsRatePct, 71.81)
	// F5: 26,000 salary over 5 months, annualised.
	cfNear(t, "annual take-home", med.AnnualTakeHome, 62400)
	if med.Months != 5 || med.WindowMonths != 12 {
		t.Errorf("median months = %d of %d, want 5 of 12", med.Months, med.WindowMonths)
	}
}

// F1: unmatched inflows, own-account transfers, top-ups and broker wires are not income.
func TestBuildCashflow_UnmatchedInflowsAreNotIncome(t *testing.T) {
	rows := append(cfSeed(),
		cfRow("2026-03-10", "deposit", 5000, "Max Mustermann", "Sparplan Tagesgeld"),
		cfRow("2026-03-11", "deposit", 500, "Apple Pay Top-Up by *1234", "TOPUP"),
		cfRow("2026-03-12", "deposit", 24405.76, "MORGAN STANLEY SMITH BARNEY", "WIRE"),
		cfRow("2026-03-13", "deposit", 100, "Erika Musterfrau", "Danke"),
		cfRow("2026-03-14", "deposit", 20, "AMAZON.DE", "Gutschrift"),
	)
	resp := buildCashflow(rows, nil, nil, cfNow, 12)
	mar := cfMonth(t, resp, "2026-03")
	cfNear(t, "March income", mar.Income, 5220) // salary + the Amazon refund
	cfNear(t, "March salary", mar.Salary, 5200)
	cfNear(t, "March unclassified", mar.Unclassified, 100)
	cfNear(t, "March transfer", mar.Transfer, 5000+500+24405.76)
	cfNear(t, "March spend", cfSpend(mar), 1409.99)

	var refund, unclassified float64
	for _, b := range resp.Buckets {
		if b.Month != "2026-03" {
			continue
		}
		switch b.Category {
		case "refund":
			refund += b.Amount
		case "unclassified":
			unclassified += b.Amount
			if b.Bucket != "unclassified" {
				t.Errorf("unclassified bucket = %q", b.Bucket)
			}
		}
	}
	cfNear(t, "March refund bucket", refund, 20)
	cfNear(t, "March unclassified bucket", unclassified, 100)
}

func TestBuildCashflow_OwnIBANTransfer(t *testing.T) {
	rows := append(cfSeed(), cfRow("2026-04-20", "deposit", 2000, "Max Mustermann", "DE89 3704 0044 0532 0130 00"))
	resp := buildCashflow(rows, nil, []string{"DE89370400440532013000"}, cfNow, 12)
	apr := cfMonth(t, resp, "2026-04")
	cfNear(t, "April income", apr.Income, 5200)
	cfNear(t, "April transfer", apr.Transfer, 2000)
}

// F2: a 1,000 €/month savings plan at a broker is saving, not spending.
func TestBuildCashflow_BrokerPaymentsAreNotSpending(t *testing.T) {
	rows := cfSeed()
	for _, d := range []string{"2026-01-05", "2026-02-05", "2026-03-05", "2026-04-05", "2026-05-05"} {
		rows = append(rows, cfRow(d, "withdrawal", -1000, "Scalable Capital GmbH", "Sparplan"))
	}
	med := buildCashflow(rows, nil, nil, cfNow, 12).Medians
	cfNear(t, "median spend", med.MonthlySpend, 1409.99)
	cfNear(t, "FIRE expenses", med.MonthlySpend*12, 16919.88)
	cfNear(t, "savings rate", *med.SavingsRatePct, 71.81)
}

// F3: bank fees and withheld tax on a checking account are spending.
func TestBuildCashflow_BankFeesAreSpending(t *testing.T) {
	rows := append(cfSeed(),
		cfRow("2026-05-20", "fee", 9.99, "Plan subscription fee", "FEE"),
		cfRow("2026-05-21", "fee", 2.5, "Tax on interest", "TAX"),
	)
	may := cfMonth(t, buildCashflow(rows, nil, nil, cfNow, 12), "2026-05")
	cfNear(t, "May spend", cfSpend(may), 1681.99+9.99+2.5)
	cfNear(t, "May fixed", may.Fixed, 1300+89+14.99+120+9.99)
}

// F4: "KfW Rückzahlung Studienkredit" is a payment, not negative income.
func TestBuildCashflow_WithdrawalRefundWordIsSpend(t *testing.T) {
	rows := append(cfSeed(), cfRow("2026-05-25", "withdrawal", -300, "KfW", "Rückzahlung Studienkredit"))
	may := cfMonth(t, buildCashflow(rows, nil, nil, cfNow, 12), "2026-05")
	cfNear(t, "May income", may.Income, 5200)
	cfNear(t, "May spend", cfSpend(may), 1981.99)
}

// F5: the summary carries annualised take-home pay, never a gross-income field.
func TestBuildCashflow_AnnualTakeHomeNotGross(t *testing.T) {
	rows := append(cfSeed(), cfRow("2026-04-15", "deposit", 45.10, "Finanzamt", "Steuererstattung"))
	resp := buildCashflow(rows, nil, nil, cfNow, 12)
	cfNear(t, "annual take-home", resp.Medians.AnnualTakeHome, 62400) // the tax refund is income, not pay
	raw, err := json.Marshal(resp)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "gross") {
		t.Errorf("response still exposes a gross-income figure: %s", raw)
	}
}

// F6: tax withheld on interest is subtracted.
func TestBuildCashflow_InterestNetOfTax(t *testing.T) {
	row := cfRow("2026-02-28", "interest", 100, "Bank", "Zinsen")
	row.Tax = 26.38
	feb := cfMonth(t, buildCashflow(append(cfSeed(), row), nil, nil, cfNow, 12), "2026-02")
	cfNear(t, "February income", feb.Income, 5200+73.62)
}

// F7: the running month is shown, marked partial, and left out of the medians.
func TestBuildCashflow_PartialMonthExcludedFromMedians(t *testing.T) {
	base := buildCashflow(cfSeed(), nil, nil, cfNow, 12).Medians
	rows := append(cfSeed(),
		cfRow("2026-11-01", "deposit", 5200, "ACME GmbH", "Gehalt November"),
		cfRow("2026-11-02", "withdrawal", -20, "REWE", "EC Kauf"),
	)
	resp := buildCashflow(rows, nil, nil, cfNow, 12)
	nov := resp.Months[0]
	if nov.Month != "2026-11" || !nov.Partial || !nov.HasData {
		t.Fatalf("first month = %+v, want partial 2026-11 with data", nov)
	}
	cfNear(t, "November net", nov.Net, 5180)
	med := resp.Medians
	cfNear(t, "median spend", med.MonthlySpend, base.MonthlySpend)
	cfNear(t, "median net", med.MonthlySurplus, base.MonthlySurplus)
	cfNear(t, "annual take-home", med.AnnualTakeHome, base.AnnualTakeHome)
	if med.Months != base.Months {
		t.Errorf("median months = %d, want %d", med.Months, base.Months)
	}
}

// F8: typical net is the median of monthly nets; the savings rate is Σnet / Σincome.
func TestComputeMedians_TypicalNetIsMedianOfNets(t *testing.T) {
	months := []monthlyTotals{
		{Month: "2026-11", Partial: true},
		{Month: "2026-10", Income: 5000, Variable: 4500, Net: 500},
		{Month: "2026-09", Income: 6000, Variable: 1000, Net: 5000},
		{Month: "2026-08", Income: 7000, Variable: 6800, Net: 200},
	}
	med := computeMedians(months)
	cfNear(t, "typical net", med.MonthlySurplus, 500) // median income − median spend would be +1,500
	cfNear(t, "savings rate", *med.SavingsRatePct, 31.67)
}

func TestComputeMedians_NoIncomeHasNoSavingsRate(t *testing.T) {
	med := computeMedians([]monthlyTotals{{Month: "2026-10", Variable: 100, Net: -100}})
	if med.SavingsRatePct != nil {
		t.Errorf("savings rate = %v, want null", *med.SavingsRatePct)
	}
}

// F11: an import-set spending category on money in becomes a refund instead
// of a negative spending category that the breakdown would clamp away.
func TestBuildCashflow_SpendingOverrideOnInflowIsRefund(t *testing.T) {
	row := cfRow("2026-05-28", "deposit", 120, "ALLIANZ", "Beitragsrueckgabe")
	row.Category = "insurance" // set by the Sparkasse import from the booking text
	resp := buildCashflow(append(cfSeed(), row), nil, nil, cfNow, 12)
	may := cfMonth(t, resp, "2026-05")
	cfNear(t, "May fixed", may.Fixed, 1300+89+14.99+120)
	cfNear(t, "May income", may.Income, 5320)
	var spendBuckets, incomeBuckets float64
	for _, b := range resp.Buckets {
		if b.Month != "2026-05" {
			continue
		}
		if b.Amount < 0 && (b.Bucket == "fixed" || b.Bucket == "variable") {
			t.Errorf("negative spending bucket %s %.2f", b.Category, b.Amount)
		}
		switch b.Bucket {
		case "fixed", "variable":
			spendBuckets += b.Amount
		case "income":
			incomeBuckets += b.Amount
		}
	}
	cfNear(t, "May spend buckets", spendBuckets, cfSpend(may))
	cfNear(t, "May income buckets", incomeBuckets, may.Income)
}

// F13: overdraft interest imported as a negative interest row is a cost.
func TestBuildCashflow_NegativeInterestIsACharge(t *testing.T) {
	rows := append(cfSeed(), cfRow("2026-05-31", "interest", -4.12, "N26", "Overdraft interest"))
	may := cfMonth(t, buildCashflow(rows, nil, nil, cfNow, 12), "2026-05")
	cfNear(t, "May income", may.Income, 5200)
	cfNear(t, "May spend", cfSpend(may), 1681.99+4.12)
}

// F13: a month with only transfers still counts as imported.
func TestBuildCashflow_TransferOnlyMonthHasData(t *testing.T) {
	rows := append(cfSeed(), cfRow("2026-07-10", "withdrawal", -500, "Max Mustermann", "Umbuchung Tagesgeld"))
	jul := cfMonth(t, buildCashflow(rows, nil, nil, cfNow, 12), "2026-07")
	if !jul.HasData {
		t.Error("July has a transfer but has_data is false")
	}
	cfNear(t, "July net", jul.Net, 0)
	cfNear(t, "July transfer", jul.Transfer, -500)
}

// Loan instalments keep their interest/principal split.
func TestBuildCashflow_LoanSplit(t *testing.T) {
	row := cfRow("2026-05-30", "withdrawal", -900, "Bank", "Darlehen")
	split := func(id uuid.UUID) (float64, string, bool) {
		if id == row.ID {
			return 250, "property", true
		}
		return 0, "", false
	}
	may := cfMonth(t, buildCashflow(append(cfSeed(), row), split, nil, cfNow, 12), "2026-05")
	cfNear(t, "May fixed", may.Fixed, 1300+89+14.99+120+250)
	cfNear(t, "May transfer", may.Transfer, -650)
}

// F13: an amount in a currency without a rate must not be counted 1:1 as euros.
func TestHasEURRate(t *testing.T) {
	d := cfDate("2026-05-01")
	latest := map[string]float64{"EUR": 1, "USD": 1.08, "GBP": 0.85}
	hist := []market.HistoricalFXRate{{Date: cfDate("2026-04-30"), Currency: "CHF", Rate: 0.94}}
	for _, tc := range []struct {
		cur  string
		want bool
	}{
		{"EUR", true}, {"", true}, {"USD", true}, {"GBp", true}, {"CHF", true}, {"HUF", false},
	} {
		if got := hasEURRate(tc.cur, d, hist, latest); got != tc.want {
			t.Errorf("hasEURRate(%q) = %v, want %v", tc.cur, got, tc.want)
		}
	}
}

// Prod shape: salary and spending run through an untracked bank; only a
// savings account and brokers are imported. The external series is what the
// page can still show: savings plans in, RSU vests, wires out, a car payment.
func TestBuildExternalMonths_UntrackedCurrentAccount(t *testing.T) {
	savings, broker, ms := uuid.New(), uuid.New(), uuid.New()
	types := map[uuid.UUID]string{savings: "savings", broker: "brokerage", ms: "brokerage"}
	row := func(acct uuid.UUID, date, typ string, amount float64, isin string) db.ListTransactionsRow {
		d, _ := time.Parse("2006-01-02", date)
		r := db.ListTransactionsRow{ID: uuid.New(), AccountID: acct, Date: d, Type: typ, Amount: numericFromFloat(amount), Currency: "EUR"}
		if isin != "" {
			r.SecurityISIN = pgtype.Text{String: isin, Valid: true}
			r.Quantity = numericFromFloat(1)
		}
		return r
	}
	carPay := row(savings, "2026-11-08", "withdrawal", 46500, "")
	txns := []db.ListTransactionsRow{
		row(broker, "2026-10-02", "deposit", 2000, ""),            // savings plan from the salary account
		row(ms, "2026-10-20", "transfer", 1500, "US02079K1079"),   // RSU vest
		row(ms, "2026-10-21", "withdrawal", 1400, ""),             // auto-wire to the salary account
		row(broker, "2026-10-13", "cash_transfer_out", 35000, ""), // own accounts: not external
		row(savings, "2026-10-13", "cash_transfer_in", 35000, ""),
		row(savings, "2026-10-31", "interest", 51.02, ""), // income, not a boundary flow
		carPay,
	}
	car := assets.Asset{Asset: db.Asset{ID: uuid.New(), Name: "ID.3 Neo", PurchaseDate: time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC), PurchasePrice: numericFromFloat(44990)},
		Links: []db.AssetTransaction{{TransactionID: carPay.ID, Purpose: "purchase"}}}
	got := buildExternalMonths(txns, types, assets.Holdings{Assets: []assets.Asset{car}}, nil, nil, []string{"2026-11", "2026-10"})
	nov, oct := got[0], got[1]
	if oct.In != 2000 || oct.InKind != 1500 || oct.Out != 1400 || oct.Net != 2100 {
		t.Errorf("October = %+v, want in 2,000, in-kind 1,500, out 1,400, net 2,100", oct)
	}
	if nov.Out != 0 || nov.IntoAssets != 46500 || nov.Net != -46500 {
		t.Errorf("November = %+v, want 46,500 into assets and no other outflow", nov)
	}
}
