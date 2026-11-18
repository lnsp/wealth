package handler

import (
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/lnsp/wealth/internal/analytics"
	db "github.com/lnsp/wealth/internal/database/generated"
	"github.com/lnsp/wealth/internal/market"
)

func mustNumeric(s string) pgtype.Numeric {
	var n pgtype.Numeric
	_ = n.Scan(s)
	return n
}

func TestConvertAmountToEUR_GBpPenceAndHistoricalLookup(t *testing.T) {
	fx := map[string]float64{"EUR": 1.0, "GBP": 0.85, "USD": 1.10}
	// 1,000 GBp = £10.00 = 10 / 0.85 = 11.7647 EUR (must NOT be 1,000 EUR!)
	got := convertAmountToEUR(1000, "GBp", fx)
	want := 10.0 / 0.85
	if math.Abs(got-want) > 0.001 {
		t.Fatalf("convertAmountToEUR(1000, GBp) = %.4f, want %.4f", got, want)
	}

	hist := []market.HistoricalFXRate{
		{Date: time.Date(2025, 3, 7, 0, 0, 0, 0, time.UTC), Currency: "GBP", Rate: 0.80}, // Friday
	}
	// Sunday 2025-03-09 falls back to Friday 0.80 rate: 800 GBp = £8 / 0.80 = 10.00 EUR
	gotHist := convertAmountToEURAtDate(800, "GBp", time.Date(2025, 3, 9, 0, 0, 0, 0, time.UTC), hist, fx)
	if math.Abs(gotHist-10.0) > 0.001 {
		t.Fatalf("convertAmountToEURAtDate(800, GBp, Sunday) = %.4f, want 10.00", gotHist)
	}
}

func TestComputeImplicitCashClampEvents_ReconcilesTelescopingFlows(t *testing.T) {
	depotID := uuid.New()
	accounts := []db.Account{
		{ID: depotID, Name: "Depot", Type: "brokerage", IsActive: true},
	}
	d1 := time.Date(2025, 1, 10, 0, 0, 0, 0, time.UTC)
	d2 := time.Date(2025, 2, 10, 0, 0, 0, 0, time.UTC)
	d3 := time.Date(2025, 3, 10, 0, 0, 0, 0, time.UTC)

	// Chronological transactions on a securities-only depot with NO explicit deposits:
	// 1. Buy 1,000 EUR -> rawCash = -1000, offset = +1000 -> implicit +1000
	// 2. Dividend 50 EUR -> rawCash = -950, offset = +950 -> implicit -50
	// 3. Sell 1,200 EUR -> rawCash = +250, offset = 0 -> implicit -950
	// Net sum of implicit events = +1000 - 50 - 950 = 0 (matches clamped cash == raw cash +250!).
	txnsChrono := []db.ListTransactionsRow{
		{AccountID: depotID, Date: d1, Type: "buy", Amount: mustNumeric("1000.00"), Currency: "EUR", SecurityISIN: pgtype.Text{String: "IE00TEST0001", Valid: true}},
		{AccountID: depotID, Date: d2, Type: "dividend", Amount: mustNumeric("50.00"), Currency: "EUR", SecurityISIN: pgtype.Text{String: "IE00TEST0001", Valid: true}},
		{AccountID: depotID, Date: d3, Type: "sell", Amount: mustNumeric("1200.00"), Currency: "EUR", SecurityISIN: pgtype.Text{String: "IE00TEST0001", Valid: true}},
	}

	events := computeImplicitCashClampEvents(accounts, txnsChrono, nil, map[string]float64{"EUR": 1})
	if len(events) != 3 {
		t.Fatalf("expected 3 telescoping clamp events, got %d", len(events))
	}
	expected := []float64{1000, -50, -950}
	sum := 0.0
	for i, ev := range events {
		sum += ev.DeltaEUR
		if math.Abs(ev.DeltaEUR-expected[i]) > 0.01 {
			t.Fatalf("event[%d] DeltaEUR = %.2f, want %.2f", i, ev.DeltaEUR, expected[i])
		}
	}
	if math.Abs(sum) > 1e-6 {
		t.Fatalf("expected telescoping sum 0 once net cash turns positive, got %.4f", sum)
	}

	// Once an explicit cash deposit is present, NO implicit clamp events may be emitted.
	withDeposit := append([]db.ListTransactionsRow{
		{AccountID: depotID, Date: d1, Type: "deposit", Amount: mustNumeric("500.00"), Currency: "EUR"},
	}, txnsChrono...)
	if got := computeImplicitCashClampEvents(accounts, withDeposit, nil, nil); len(got) != 0 {
		t.Fatalf("expected 0 implicit events when explicit deposit exists, got %d", len(got))
	}
}

func TestSecurityTFSRate_EquityMixedAndBondETFs(t *testing.T) {
	stock := db.Security{ISIN: "US0378331005", Name: "Apple Inc.", AssetClass: "stock"}
	if got := securityTFSRate(stock); got != 0 {
		t.Fatalf("stock TFS = %.2f, want 0", got)
	}

	xeon := db.Security{ISIN: "LU0290358497", Name: "Xtrackers II EUR Overnight Rate Swap UCITS ETF", AssetClass: "etf"}
	if got := securityTFSRate(xeon); got != 0 {
		t.Fatalf("money-market ETF (XEON) TFS = %.2f, want 0", got)
	}

	mixed := db.Security{ISIN: "IE00BMVB5P51", Name: "Vanguard LifeStrategy 40", AssetClass: "etf", EquityPct: mustNumeric("0.4000")}
	if got := securityTFSRate(mixed); got != 0.15 {
		t.Fatalf("mixed 40%% equity ETF TFS = %.2f, want 0.15", got)
	}

	eqETF := db.Security{ISIN: "IE00BK5BQT80", Name: "Vanguard FTSE All-World UCITS ETF", AssetClass: "etf"}
	if got := securityTFSRate(eqETF); got != 0.30 {
		t.Fatalf("equity ETF TFS = %.2f, want 0.30", got)
	}
}

func TestAddMonthsClampedAndFirstOfMonth_NoDay31Overflow(t *testing.T) {
	jan31 := time.Date(2026, 1, 31, 0, 0, 0, 0, time.UTC)
	feb := addMonthsClamped(jan31, 1)
	if got := feb.Format("2006-01-02"); got != "2026-02-28" {
		t.Fatalf("addMonthsClamped(2026-01-31, 1) = %s, want 2026-02-28", got)
	}
	mar31 := time.Date(2026, 3, 31, 0, 0, 0, 0, time.UTC)
	prevMonth := addMonthsFirstOfMonth(mar31, -1)
	if got := prevMonth.Format("2006-01-02"); got != "2026-02-01" {
		t.Fatalf("addMonthsFirstOfMonth(2026-03-31, -1) = %s, want 2026-02-01", got)
	}
}

func TestComputeMedians_Trailing12MonthsIncome(t *testing.T) {
	// 60 months of 4,000 EUR/month take-home pay -> AnnualTakeHome must be 48,000 EUR (12 months), NOT 240,000 EUR (60 months).
	months := make([]monthlyTotals, 60)
	for i := 0; i < 60; i++ {
		months[i] = monthlyTotals{
			Month:    fmt.Sprintf("%04d-%02d", 2025-i/12, 12-i%12),
			Income:   4000,
			Salary:   4000,
			Fixed:    1500,
			Variable: 500,
			Net:      2000,
		}
	}
	med := computeMedians(months)
	if math.Abs(med.AnnualTakeHome-48000) > 0.01 || med.Months != 12 {
		t.Fatalf("AnnualTakeHome = %.2f over %d months, want 48000 over 12", med.AnnualTakeHome, med.Months)
	}
	if math.Abs(med.MonthlySpend-2000) > 0.01 {
		t.Fatalf("MonthlySpend = %.2f, want 2000", med.MonthlySpend)
	}
}

func TestCompatibleImportAccountType_SecuritiesOnCryptoAndBankGuard(t *testing.T) {
	for _, target := range []string{"brokerage", "crypto", "pension", "precious_metals"} {
		if !compatibleImportAccountType("brokerage", target) {
			t.Fatalf("expected brokerage CSV (e.g. Delta) to be accepted on %q account", target)
		}
	}
	if compatibleImportAccountType("brokerage", "checking") {
		t.Fatalf("expected brokerage CSV to be rejected on checking account")
	}
	if compatibleImportAccountType("checking", "crypto") {
		t.Fatalf("expected checking CSV to be rejected on crypto account")
	}
}

func TestScenarioJSON_ZeroPayoffDateNeverEmits0001(t *testing.T) {
	got := scenarioJSON(analytics.LoanScenario{RemainingAtFixedEnd: 50000})
	if got["payoff_date"] != "" {
		t.Fatalf("scenarioJSON zero PayoffDate = %q, want empty string", got["payoff_date"])
	}
}
