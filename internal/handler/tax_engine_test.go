package handler

import (
	"math"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/lnsp/wealth/internal/analytics"
	db "github.com/lnsp/wealth/internal/database/generated"
)

func taxRow(acct uuid.UUID, date, typ, isin, qty, amount string) db.ListTransactionsRow {
	d, _ := time.Parse("2006-01-02", date)
	r := db.ListTransactionsRow{ID: uuid.New(), AccountID: acct, Date: d, Type: typ, Currency: "EUR",
		Quantity: mustNumeric(qty), Amount: mustNumeric(amount), Fee: mustNumeric("0")}
	if isin != "" {
		r.SecurityISIN = pgtype.Text{String: isin, Valid: true}
	}
	return r
}

// newestFirst mirrors listAllTransactionsRows' ordering.
func newestFirst(rows ...db.ListTransactionsRow) []db.ListTransactionsRow {
	out := make([]db.ListTransactionsRow, len(rows))
	for i, r := range rows {
		out[len(rows)-1-i] = r
	}
	return out
}

// Audit C7: buildTaxTransactions must carry the asset class so crypto is
// kept out of §20 and stocks land in the share pot.
func TestBuildTaxTransactions_AssetClassDrivesPotsAndPrivateSales(t *testing.T) {
	acct := uuid.New()
	secs := []db.Security{
		{ISIN: "CRYPTO:BTC", Name: "Bitcoin", AssetClass: "crypto"},
		{ISIN: "US02079K1079", Name: "Alphabet", AssetClass: "stock"},
	}
	rows := newestFirst(
		taxRow(acct, "2023-02-01", "buy", "CRYPTO:BTC", "1", "10000"),
		taxRow(acct, "2025-01-10", "buy", "US02079K1079", "10", "1000"),
		taxRow(acct, "2025-03-01", "sell", "CRYPTO:BTC", "1", "20000"),
		taxRow(acct, "2025-06-01", "sell", "US02079K1079", "10", "3000"),
	)
	taxTxns, _, _, _ := buildTaxTransactions(rows, secs, nil, map[string]float64{"EUR": 1})
	classes := map[string]string{}
	for _, tx := range taxTxns {
		classes[tx.ISIN] = tx.AssetClass
	}
	if classes["CRYPTO:BTC"] != "crypto" || classes["US02079K1079"] != "stock" {
		t.Fatalf("asset classes = %v", classes)
	}

	s := analytics.ComputeTaxSummary(taxTxns, 2025, nil)
	// Only the stock gain (2,000) is §20 income: (2,000 − 1,000) × 26.375 %.
	if math.Abs(s.EstimatedTax-263.75) > 0.01 || math.Abs(s.ShareGains-2000) > 0.01 {
		t.Errorf("tax = %.2f, share gains = %.2f; want 263.75 and 2000 (BTC held 2 years is tax-free)", s.EstimatedTax, s.ShareGains)
	}
	if s.PrivateSales == nil || math.Abs(s.PrivateSales.TaxFreePL-10000) > 0.01 {
		t.Errorf("private sales = %+v, want 10,000 tax-free", s.PrivateSales)
	}
}

// Audit W2: the harvest saving is what selling the position would take off
// this year's tax, so it is 0 when there are no gains left in a pot it may offset.
func TestComputeLossHarvestHints_RespectPotsAndAllowance(t *testing.T) {
	now := time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC)
	stock := func(date, typ, isin string, qty, amount float64) analytics.TaxTransaction {
		return analytics.TaxTransaction{AccountID: "d", Date: date, Year: 2026, Type: typ, ISIN: isin, Name: isin, Quantity: qty, Amount: amount, AssetClass: "stock", HasExplicitTFS: true}
	}
	losing := []analytics.TaxTransaction{
		{AccountID: "d", Date: "2025-01-02", Year: 2025, Type: "buy", ISIN: "LOSS", Name: "Loser AG", Quantity: 10, Amount: 3000, AssetClass: "stock", HasExplicitTFS: true},
		{AccountID: "d", Date: "2025-01-02", Year: 2025, Type: "buy", ISIN: "ETF", Name: "World ETF", Quantity: 10, Amount: 2000, AssetClass: "etf", TFS: 0.30, HasExplicitTFS: true, IsEquityFund: true},
	}
	prices := map[string]float64{"LOSS": 150, "ETF": 100} // stock −1,500; ETF −1,000 (−700 after TFS)

	// No realised gains this year: nothing to offset, the losses would carry forward.
	hints := computeLossHarvestHints(losing, prices, nil, nil, map[string]string{"d": "Broker"}, now, analytics.TaxOptions{})
	if len(hints) != 2 {
		t.Fatalf("hints = %+v", hints)
	}
	for _, h := range hints {
		if h.PotentialSaving != 0 {
			t.Errorf("%s: saving %.2f with no gains this year, want 0", h.ISIN, h.PotentialSaving)
		}
		if h.Account != "Broker" {
			t.Errorf("%s: account %q", h.ISIN, h.Account)
		}
	}

	// A realised share gain of 3,000 this year (2,000 above the allowance).
	withGain := append(append([]analytics.TaxTransaction{}, losing...),
		stock("2026-01-10", "buy", "WIN", 10, 1000),
		stock("2026-03-10", "sell", "WIN", 10, 4000),
	)
	hints = computeLossHarvestHints(withGain, prices, nil, nil, nil, now, analytics.TaxOptions{})
	got := map[string]analytics.TaxLossHint{}
	for _, h := range hints {
		got[h.ISIN] = h
	}
	// Share loss offsets the share gain: tax falls from 527.50 to 131.88.
	if h := got["LOSS"]; math.Abs(h.PotentialSaving-395.63) > 0.01 || h.Pot != "share" {
		t.Errorf("stock hint = %+v, want saving 395.63 in the share pot", h)
	}
	// A general (fund) loss may offset share gains too, after TFS: 700 × 26.375 %.
	if h := got["ETF"]; math.Abs(h.PotentialSaving-184.63) > 0.01 || math.Abs(h.TaxableLoss-(-700)) > 0.01 || h.Pot != "general" {
		t.Errorf("ETF hint = %+v, want saving 184.63 on a −700 taxable loss", h)
	}
}

func TestTaxOptionsFromRequest(t *testing.T) {
	r := httptest.NewRequest("GET", "/api/analysis/tax?year=2025&joint=1&church_tax=0.09", nil)
	o := taxOptionsFromRequest(r)
	if !o.Joint || o.ChurchTaxRate != 0.09 {
		t.Errorf("options = %+v", o)
	}
	r = httptest.NewRequest("GET", "/api/analysis/tax?church_tax=abc", nil)
	if o := taxOptionsFromRequest(r); o.Joint || o.ChurchTaxRate != 0 {
		t.Errorf("defaults = %+v", o)
	}
}
