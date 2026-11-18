package handler

import (
	"math"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/lnsp/wealth/internal/cashflow"
	db "github.com/lnsp/wealth/internal/database/generated"
)

func spendDays(s ...string) []time.Time {
	out := make([]time.Time, len(s))
	for i, v := range s {
		out[i] = day(v)
	}
	return out
}

func spendCashRow(date, typ string, amount float64, counterparty, reference, category string) db.ListCashTransactionsRow {
	row := db.ListCashTransactionsRow{
		ID:       uuid.New(),
		Date:     day(date),
		Type:     typ,
		Amount:   numericFromFloat(amount),
		Currency: "EUR",
	}
	if counterparty != "" {
		row.Counterparty = pgtype.Text{String: counterparty, Valid: true}
	}
	if reference != "" {
		row.Reference = pgtype.Text{String: reference, Valid: true}
	}
	if category != "" {
		row.Category = pgtype.Text{String: category, Valid: true}
	}
	return row
}

func spendIdentityEUR(amount float64, _ string, _ time.Time) float64 { return amount }

func spendNoLoans(uuid.UUID) (float64, bool, bool) { return 0, false, false }

// seedCheckingRows is the seeded "Test Checking" account: withdrawals are
// stored with a negative sign, five months of data (2026-01 to 2026-05).
func seedCheckingRows() []db.ListCashTransactionsRow {
	return []db.ListCashTransactionsRow{
		spendCashRow("2026-01-01", "deposit", 5200, "ACME GmbH", "Gehalt Januar", ""),
		spendCashRow("2026-01-03", "withdrawal", -1300, "WOHNUNG VERWALTUNG", "Miete Jan", ""),
		spendCashRow("2026-02-01", "deposit", 5200, "ACME GmbH", "Gehalt Februar", ""),
		spendCashRow("2026-02-04", "withdrawal", -1300, "WOHNUNG VERWALTUNG", "Miete Feb", ""),
		spendCashRow("2026-03-01", "deposit", 5200, "ACME GmbH", "Gehalt März", ""),
		spendCashRow("2026-03-03", "withdrawal", -1300, "WOHNUNG VERWALTUNG", "Miete März", ""),
		spendCashRow("2026-03-15", "withdrawal", -95, "EDEKA", "Grocery", ""),
		spendCashRow("2026-03-20", "withdrawal", -14.99, "SPOTIFY", "Family Plan", ""),
		spendCashRow("2026-04-01", "deposit", 5200, "ACME GmbH", "Gehalt April 2026", ""),
		spendCashRow("2026-04-03", "withdrawal", -1300, "WOHNUNG VERWALTUNG", "Miete April", ""),
		spendCashRow("2026-04-09", "withdrawal", -82, "REWE Markt", "EC Kauf", ""),
		spendCashRow("2026-04-14", "withdrawal", -240, "AMAZON.DE", "Bestellung", ""),
		spendCashRow("2026-04-22", "withdrawal", -14.99, "NETFLIX.COM", "Monatsabo", ""),
		spendCashRow("2026-05-01", "deposit", 5200, "ACME GmbH", "Gehalt Mai 2026", ""),
		spendCashRow("2026-05-03", "withdrawal", -1300, "WOHNUNG VERWALTUNG", "Miete Mai", ""),
		spendCashRow("2026-05-04", "withdrawal", -89, "STADTWERKE BERLIN", "Strom April", ""),
		spendCashRow("2026-05-07", "withdrawal", -14.99, "NETFLIX.COM", "Monatsabo", ""),
		spendCashRow("2026-05-09", "withdrawal", -67.4, "REWE Markt", "EC Kauf", ""),
		spendCashRow("2026-05-12", "withdrawal", -52.1, "EDEKA NORD", "EC Kauf", ""),
		spendCashRow("2026-05-14", "withdrawal", -38.5, "DB-VERTRIEB GMBH", "Ticket BER-MUC", ""),
		spendCashRow("2026-05-18", "withdrawal", -120, "ALLIANZ", "Haftpflicht", ""),
	}
}

func spendCategoryByName(t *testing.T, res spendingSummary, cat cashflow.Category) spendingCategory {
	t.Helper()
	for _, c := range res.Categories {
		if c.Category == string(cat) {
			return c
		}
	}
	t.Fatalf("category %q not in %+v", cat, res.Categories)
	return spendingCategory{}
}

// Audit C1: the month denominator was every month with any transaction on
// any account (42 on the seed, from the brokerage account), so rent showed
// as 156.88 €/month. It must be the checking account's own calendar span.
func TestComputeSpending_SeedUsesCashAccountSpan(t *testing.T) {
	entries := spendingEntriesFromCash(seedCheckingRows(), spendIdentityEUR, spendNoLoans)
	res := computeSpending(entries, day("2026-11-18"))

	if res.MonthsAnalyzed != 5 || res.SpanFrom != "2026-01" || res.SpanTo != "2026-05" {
		t.Fatalf("span = %d months %s..%s, want 5 months 2026-01..2026-05", res.MonthsAnalyzed, res.SpanFrom, res.SpanTo)
	}
	housing := spendCategoryByName(t, res, cashflow.CatHousing)
	if math.Abs(housing.Total-6500) > 0.01 || math.Abs(housing.AvgMonthly-1300) > 0.01 {
		t.Errorf("housing = %.2f total, %.2f/month; want 6,500.00 and 1,300.00", housing.Total, housing.AvgMonthly)
	}
	// Strom goes to utilities under the Cashflow classifier, not housing.
	if u := spendCategoryByName(t, res, cashflow.CatUtilities); math.Abs(u.AvgMonthly-17.80) > 0.01 {
		t.Errorf("utilities = %.2f/month, want 17.80", u.AvgMonthly)
	}
	if math.Abs(res.TotalIncome-26000) > 0.01 {
		t.Errorf("income = %.2f, want 26,000.00", res.TotalIncome)
	}
	if math.Abs(res.TotalExpenses-7328.97) > 0.01 {
		t.Errorf("spending = %.2f, want 7,328.97", res.TotalExpenses)
	}
	if math.Abs(res.AvgMonthlyExpense-1465.79) > 0.01 {
		t.Errorf("avg monthly spending = %.2f, want 1,465.79 (7,328.97 / 5)", res.AvgMonthlyExpense)
	}
	// (26,000 − 7,328.97) / 26,000 = 71.8 %, matching the Cashflow page.
	if math.Abs(res.SavingsRatePct-71.8) > 0.05 {
		t.Errorf("savings rate = %.1f %%, want 71.8 %%", res.SavingsRatePct)
	}
	if res.SavingsRatePct12m == nil || math.Abs(*res.SavingsRatePct12m-71.8) > 0.05 {
		t.Errorf("12-month savings rate = %v, want 71.8", res.SavingsRatePct12m)
	}
	if res.WindowMonths != 5 || res.WindowFrom != "2026-01" || res.WindowTo != "2026-05" {
		t.Errorf("window = %d months %s..%s, want 5 months 2026-01..2026-05", res.WindowMonths, res.WindowFrom, res.WindowTo)
	}
	if len(res.Monthly) != 5 {
		t.Errorf("monthly series has %d points, want 5", len(res.Monthly))
	}
}

func TestComputeSpending_CountsMonthsWithoutActivity(t *testing.T) {
	entries := spendingEntriesFromCash([]db.ListCashTransactionsRow{
		spendCashRow("2026-01-05", "withdrawal", 400, "REWE", "", ""),
		spendCashRow("2026-04-05", "withdrawal", 400, "REWE", "", ""),
	}, spendIdentityEUR, spendNoLoans)
	res := computeSpending(entries, day("2026-04-30"))
	if res.MonthsAnalyzed != 4 {
		t.Fatalf("months = %d, want 4 (January to April, February and March empty)", res.MonthsAnalyzed)
	}
	if g := spendCategoryByName(t, res, cashflow.CatGroceries); math.Abs(g.AvgMonthly-200) > 0.01 {
		t.Errorf("groceries = %.2f/month, want 200.00 (800 over 4 months)", g.AvgMonthly)
	}
	if len(res.Monthly) != 4 || res.Monthly[1].Expenses != 0 {
		t.Errorf("monthly = %+v, want 4 months with an empty February", res.Monthly)
	}
}

// Audit W3: "last 12 months" must be the calendar window ending this month,
// not the last 12 months that happen to have data.
func TestComputeSpending_WindowIsCalendarMonthsEndingNow(t *testing.T) {
	var rows []db.ListCashTransactionsRow
	start := day("2024-12-01")
	for i := 0; i < 24; i++ {
		m := addMonthsFirstOfMonth(start, i)
		spend := 2900.0 // ~3 % savings in the first year
		if i >= 12 {
			spend = 2100 // 30 % in the second year
		}
		rows = append(rows,
			spendCashRow(m.Format("2006-01-02"), "deposit", 3000, "ACME GmbH", "Gehalt", ""),
			spendCashRow(m.AddDate(0, 0, 2).Format("2006-01-02"), "withdrawal", spend, "WOHNUNG VERWALTUNG", "Miete", ""))
	}
	entries := spendingEntriesFromCash(rows, spendIdentityEUR, spendNoLoans)

	res := computeSpending(entries, day("2026-11-18"))
	if res.WindowMonths != 12 || res.WindowFrom != "2025-12" || res.WindowTo != "2026-11" {
		t.Fatalf("window = %d months %s..%s, want 12 months 2025-12..2026-11", res.WindowMonths, res.WindowFrom, res.WindowTo)
	}
	if res.SavingsRatePct12m == nil || math.Abs(*res.SavingsRatePct12m-30) > 0.05 {
		t.Errorf("12-month rate = %v, want 30", res.SavingsRatePct12m)
	}
	if math.Abs(res.SavingsRatePct-16.7) > 0.05 {
		t.Errorf("all-time rate = %.1f, want 16.7", res.SavingsRatePct)
	}

	// Six months later with no new imports, the window holds only the six
	// months of data that fall inside it.
	later := computeSpending(entries, day("2027-05-18"))
	if later.WindowMonths != 6 || later.WindowFrom != "2026-06" {
		t.Errorf("later window = %d months from %s, want 6 from 2026-06", later.WindowMonths, later.WindowFrom)
	}
	// No data in the last 12 months at all: no windowed rate.
	stale := computeSpending(entries, day("2028-12-01"))
	if stale.SavingsRatePct12m != nil || stale.WindowMonths != 0 {
		t.Errorf("stale window = %v over %d months, want nil over 0", stale.SavingsRatePct12m, stale.WindowMonths)
	}
}

// Audit W3 / KNOWN 'Transfer' asymmetry: internal moves and investments are
// neither spending nor income, on both sides.
func TestComputeSpending_TransfersExcludedOnBothSides(t *testing.T) {
	entries := spendingEntriesFromCash([]db.ListCashTransactionsRow{
		spendCashRow("2026-03-01", "deposit", 3000, "ACME GmbH", "Gehalt", ""),
		spendCashRow("2026-03-02", "cash_transfer_out", 1000, "Own savings", "Umbuchung", ""),
		spendCashRow("2026-03-03", "withdrawal", 500, "Scalable Capital", "Sparplan", "investment"),
		spendCashRow("2026-03-04", "cash_transfer_in", 800, "Own savings", "Umbuchung", ""),
		spendCashRow("2026-03-05", "withdrawal", 300, "REWE", "", ""),
	}, spendIdentityEUR, spendNoLoans)
	res := computeSpending(entries, day("2026-03-31"))
	if math.Abs(res.TotalIncome-3000) > 0.01 || math.Abs(res.TotalExpenses-300) > 0.01 {
		t.Errorf("income %.2f / spending %.2f, want 3,000 / 300", res.TotalIncome, res.TotalExpenses)
	}
	for _, c := range res.Categories {
		if c.Category == string(cashflow.CatInternal) || c.Category == string(cashflow.CatInvestment) {
			t.Errorf("transfer category %q counted as spending", c.Category)
		}
	}
}

func TestComputeSpending_InflowsNeverSpendingOrIncomeByAccident(t *testing.T) {
	entries := spendingEntriesFromCash([]db.ListCashTransactionsRow{
		spendCashRow("2026-03-01", "deposit", 3000, "ACME GmbH", "Gehalt", ""),
		spendCashRow("2026-03-05", "withdrawal", 240, "AMAZON.DE", "Bestellung", ""),
		spendCashRow("2026-03-09", "deposit", 40, "AMAZON.DE", "Erstattung Rücksendung", "shopping"),
		spendCashRow("2026-03-10", "deposit", 5000, "Max Mustermann", "", ""), // unmatched: likely own money
	}, spendIdentityEUR, spendNoLoans)
	res := computeSpending(entries, day("2026-03-31"))
	if math.Abs(res.TotalIncome-3000) > 0.01 {
		t.Errorf("income = %.2f, want 3,000 (the unmatched 5,000 is not income)", res.TotalIncome)
	}
	if s := spendCategoryByName(t, res, cashflow.CatShopping); math.Abs(s.Total-200) > 0.01 {
		t.Errorf("shopping = %.2f, want 200 (240 less a 40 refund)", s.Total)
	}
	if res.UnclassifiedInflowCount != 1 || math.Abs(res.UnclassifiedInflow-5000) > 0.01 {
		t.Errorf("unclassified inflow = %d / %.2f, want 1 / 5,000", res.UnclassifiedInflowCount, res.UnclassifiedInflow)
	}
}

func TestSpendingEntriesFromCash_LoanInstalmentSplit(t *testing.T) {
	row := spendCashRow("2026-03-01", "withdrawal", -1500, "Sparkasse", "Darlehen", "loan_payment")
	entries := spendingEntriesFromCash([]db.ListCashTransactionsRow{row}, spendIdentityEUR,
		func(id uuid.UUID) (float64, bool, bool) { return 400, false, id == row.ID })
	if len(entries) != 2 {
		t.Fatalf("entries = %+v, want interest and principal", entries)
	}
	if entries[0].Category != cashflow.CatHousing || math.Abs(entries[0].Amount+400) > 0.01 {
		t.Errorf("interest entry = %+v, want housing −400", entries[0])
	}
	if entries[1].Category != cashflow.CatLoanPrincipal || math.Abs(entries[1].Amount+1100) > 0.01 {
		t.Errorf("principal entry = %+v, want loan_principal −1,100", entries[1])
	}
	res := computeSpending(entries, day("2026-03-31"))
	if math.Abs(res.TotalExpenses-400) > 0.01 {
		t.Errorf("spending = %.2f, want 400 (interest only)", res.TotalExpenses)
	}
	if len(res.Subscriptions) != 0 {
		t.Errorf("loan instalment detected as subscription: %+v", res.Subscriptions)
	}
}

// Audit C2: annual cost came from the lifetime charge count (15.99 seen 3×
// → 47.97/yr, a yearly 120 seen 3× → 360).
func TestDetectSubscriptions_MonthlyAnnualisedByCadence(t *testing.T) {
	out := detectSubscriptions([]subscriptionCharges{
		{Name: "Netflix", Amount: 15.99, Dates: spendDays("2026-01-15", "2026-02-15", "2026-03-15")},
	}, day("2026-03-20"))
	if len(out) != 1 {
		t.Fatalf("expected 1 subscription, got %+v", out)
	}
	if out[0].Cadence != "monthly" || math.Abs(out[0].AnnualCost-191.88) > 0.005 {
		t.Errorf("got %s %.2f/yr, want monthly 191.88 (15.99 × 12)", out[0].Cadence, out[0].AnnualCost)
	}
}

func TestDetectSubscriptions_YearlyChargeCountedOnce(t *testing.T) {
	out := detectSubscriptions([]subscriptionCharges{
		{Name: "Domain", Amount: 120, Dates: spendDays("2024-02-01", "2025-02-01", "2026-02-02")},
	}, day("2026-05-01"))
	if len(out) != 1 {
		t.Fatalf("expected 1 subscription, got %+v", out)
	}
	if out[0].Cadence != "yearly" || math.Abs(out[0].AnnualCost-120) > 0.005 {
		t.Errorf("got %s %.2f/yr, want yearly 120", out[0].Cadence, out[0].AnnualCost)
	}
}

func TestDetectSubscriptions_LapsedIsDropped(t *testing.T) {
	charges := []subscriptionCharges{
		{Name: "Gym", Amount: 39.99, Dates: spendDays("2025-09-01", "2025-10-01", "2025-11-01", "2025-12-01")},
	}
	// Last charge 1 Dec; 1.5 monthly cycles ≈ 45.7 days.
	if out := detectSubscriptions(charges, day("2026-01-10")); len(out) != 1 {
		t.Errorf("still active on 10 Jan: got %+v", out)
	}
	if out := detectSubscriptions(charges, day("2026-03-01")); len(out) != 0 {
		t.Errorf("lapsed by 1 Mar: got %+v", out)
	}
}

func TestDetectSubscriptions_NeedsThreeRegularCharges(t *testing.T) {
	out := detectSubscriptions([]subscriptionCharges{
		{Name: "Twice", Amount: 25, Dates: spendDays("2026-01-01", "2026-02-01")},
		{Name: "Cinema", Amount: 12, Dates: spendDays("2026-01-03", "2026-01-10", "2026-04-20")}, // median gap ~53 d
		{Name: "Small Fee", Amount: 0.99, Dates: spendDays("2026-01-01", "2026-02-01", "2026-03-01")},
		{Name: "Rent", Amount: 500.01, Dates: spendDays("2026-01-01", "2026-02-01", "2026-03-01")},
		{Name: "Gym", Amount: 39.99, Dates: spendDays("2026-01-01", "2026-02-01", "2026-03-01")},
	}, day("2026-03-05"))
	if len(out) != 1 || out[0].Name != "Gym" {
		t.Errorf("got %+v, want only Gym", out)
	}
}

// Audit W4: deposits and interest are not subscriptions, even when they
// recur at the same amount every month.
func TestComputeSpending_InflowsAreNotSubscriptions(t *testing.T) {
	var rows []db.ListCashTransactionsRow
	for _, d := range []string{"2026-01-05", "2026-02-05", "2026-03-05", "2026-04-05"} {
		rows = append(rows,
			spendCashRow(d, "deposit", 255, "Familienkasse", "Kindergeld", "salary"),
			spendCashRow(d, "interest", 3.5, "Bank", "Zinsen", ""),
			spendCashRow(d, "withdrawal", -14.99, "NETFLIX.COM", "Monatsabo", ""))
	}
	res := computeSpending(spendingEntriesFromCash(rows, spendIdentityEUR, spendNoLoans), day("2026-04-30"))
	if len(res.Subscriptions) != 1 || res.Subscriptions[0].Name != "NETFLIX.COM" {
		t.Fatalf("subscriptions = %+v, want only NETFLIX.COM", res.Subscriptions)
	}
	if math.Abs(res.Subscriptions[0].AnnualCost-179.88) > 0.005 {
		t.Errorf("Netflix = %.2f/yr, want 179.88", res.Subscriptions[0].AnnualCost)
	}
}
