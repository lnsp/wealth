package handler

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/lnsp/wealth/internal/database/generated"
)

// The DATEV export books Kapitalertragsteuer rows (type "tax", as Scalable
// imports now arrive) as a debit against 1810 Privatsteuern, alongside the
// income, sale and fee rows it already exported.
func TestDATEVRows_IncludesTaxRows(t *testing.T) {
	acct := uuid.New()
	day := func(s string) time.Time { d, _ := time.Parse("2006-01-02", s); return d }
	isin := pgtype.Text{String: "IE00B4L5Y983", Valid: true}
	txns := []db.ListTransactionsRow{
		{AccountID: acct, Date: day("2025-12-30"), Type: "tax", Amount: mustNumeric("26.38"), Currency: "EUR", Counterparty: pgtype.Text{String: "Kapitalertragsteuer", Valid: true}},
		{AccountID: acct, Date: day("2025-06-01"), Type: "dividend", Amount: mustNumeric("100.00"), Currency: "EUR", SecurityISIN: isin},
		{AccountID: acct, Date: day("2025-05-01"), Type: "fee", Amount: mustNumeric("1.00"), Currency: "EUR"},
		{AccountID: acct, Date: day("2025-04-01"), Type: "buy", Amount: mustNumeric("500.00"), Currency: "EUR", SecurityISIN: isin},
		{AccountID: acct, Date: day("2024-12-30"), Type: "tax", Amount: mustNumeric("10.00"), Currency: "EUR"},
	}
	rows := datevRows(txns, 2025, map[string]string{"IE00B4L5Y983": "iShares Core MSCI World"}, map[string]string{acct.String(): "Scalable Depot"}, nil, map[string]float64{"EUR": 1})
	if len(rows) != 3 {
		t.Fatalf("rows = %+v, want tax, dividend and fee for 2025 (no buy, no 2024 tax)", rows)
	}
	tax := rows[0]
	if tax.Amount != 26.38 || tax.SollHaben != "S" || tax.Gegenkonto != "1810" || tax.Belegdatum != "3012" || !strings.Contains(tax.Text, "Kapitalertragsteuer") {
		t.Errorf("tax row = %+v, want 26.38 S against 1810 on 3012", tax)
	}
	if div := rows[1]; div.SollHaben != "H" || div.Gegenkonto != "2650" {
		t.Errorf("dividend row = %+v, want H against 2650", div)
	}
	if fee := rows[2]; fee.SollHaben != "S" || fee.Gegenkonto != "4900" {
		t.Errorf("fee row = %+v, want S against 4900", fee)
	}

	var b strings.Builder
	writeDATEV(&b, 2025, rows)
	if !strings.Contains(b.String(), `26.38;S;EUR;1800;1810;3012;"tax: Kapitalertragsteuer";"Scalable Depot"`) {
		t.Errorf("DATEV output missing the tax booking:\n%s", b.String())
	}
}
