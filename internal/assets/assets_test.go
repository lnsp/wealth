package assets

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/lnsp/wealth/internal/database/generated"
)

func num(s string) pgtype.Numeric {
	var n pgtype.Numeric
	n.Scan(s)
	return n
}

func TestTotalValueAt_LocalMidnightOnPurchaseDay(t *testing.T) {
	berlin, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Skip("tzdata unavailable")
	}
	a := FromRows(db.Asset{
		PurchasePrice:      num("44900"),
		PurchaseDate:       time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
		AnnualDepreciation: num("0.078"),
		KmDepreciation:     num("0.030"),
		ExpectedKmPerYear:  15000,
	}, nil, nil, nil)

	// The backfill iterates local midnights; 2026-08-01 00:00 CEST is
	// 2026-07-31 22:00 UTC and must still count as the purchase day.
	got := Holdings{Assets: []Asset{a}}.PhysicalAt(time.Date(2026, 8, 1, 0, 0, 0, 0, berlin))
	if got != 44900 {
		t.Errorf("value on purchase day = %.2f, want 44900", got)
	}
	if v := (Holdings{Assets: []Asset{a}}).PhysicalAt(time.Date(2026, 7, 31, 0, 0, 0, 0, berlin)); v != 0 {
		t.Errorf("value before purchase = %.2f, want 0", v)
	}
}

func TestFromRows_Observations(t *testing.T) {
	id := [16]byte{1}
	a := FromRows(db.Asset{
		ID:                 id,
		PurchasePrice:      num("44900"),
		PurchaseDate:       time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
		PurchaseOdometerKm: 2700,
		AnnualDepreciation: num("0.078"),
		KmDepreciation:     num("0.030"),
		ExpectedKmPerYear:  15000,
	}, []db.AssetObservation{
		{AssetID: id, Date: time.Date(2026, 11, 11, 0, 0, 0, 0, time.UTC), AppraisedValue: num("47500")},
		{AssetID: id, Date: time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC), OdometerKm: pgtype.Int4{Int32: 6000, Valid: true}},
	}, nil, nil)
	if v := a.ValueOn(time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)); v != 47500 {
		t.Errorf("appraisal day value = %.2f, want 47500", v)
	}
	if km := a.Car.OdometerAt(time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)); km != 6000 {
		t.Errorf("odometer = %.0f, want 6000", km)
	}
}

func d(s string) time.Time {
	t, _ := time.Parse("2006-01-02", s)
	return t
}

func TestFromRows_Apartment(t *testing.T) {
	a := FromRows(db.Asset{
		Kind:            KindApartment,
		PurchasePrice:   num("320000"),
		PurchaseCosts:   num("35424"),
		PurchaseDate:    d("2025-04-01"),
		LandValue:       num("75000"),
		YearBuilt:       pgtype.Int4{Int32: 1998, Valid: true},
		UsefulLifeYears: 80,
		PriceDrift:      num("0.02"),
	}, nil, []db.AssetImprovement{{Date: d("2026-01-01"), Amount: num("18000")}}, nil)
	if a.Property == nil || a.Car != nil {
		t.Fatal("apartment must use the property model")
	}
	if got := a.CostBasis(); got != 320000+35424+18000 {
		t.Errorf("cost basis = %.0f", got)
	}
	if v := a.ValueOn(d("2025-04-01")); v != 320000 {
		t.Errorf("value at purchase = %.2f, want 320000", v)
	}
	if v := a.ValueOn(d("2026-11-11")); v < 335000 || v > 345000 {
		t.Errorf("value with improvement = %.0f, want ≈340k", v)
	}
}

func testLoanRow() db.Loan {
	return db.Loan{
		ID:               uuid.New(),
		Principal:        num("260000"),
		PayoutDate:       d("2025-04-01"),
		FirstPaymentDate: d("2025-05-01"),
		NominalRate:      num("0.0365"),
		MonthlyPayment:   num("1224.17"),
		FixedUntil:       pgtype.Date{Time: d("2040-04-01"), Valid: true},
	}
}

func TestHoldings_DebtAndSplit(t *testing.T) {
	assetID := uuid.New()
	row := testLoanRow()
	row.AssetID = assetID
	txn := uuid.New()
	l := LoanFromRows(row, nil, []db.LoanPaymentLink{{TransactionID: txn, LoanID: row.ID, DueDate: d("2025-05-01")}})
	h := Holdings{Assets: []Asset{{Asset: db.Asset{ID: assetID, Kind: KindApartment}}}, Loans: []Loan{l}}
	if got := h.DebtAt(d("2025-03-31")); got != 0 {
		t.Errorf("debt before payout = %.2f", got)
	}
	if got := h.DebtAt(d("2025-04-01")); got != 260000 {
		t.Errorf("debt at payout = %.2f", got)
	}
	interest, principal, kind, ok := h.PaymentSplit(txn)
	if !ok || kind != KindApartment || interest != 790.83 || principal < 433.33 || principal > 433.35 {
		t.Errorf("split = %.2f / %.2f (%s, %v)", interest, principal, kind, ok)
	}
	if !h.LinkedIDs()[txn] {
		t.Error("matched payment must count as linked")
	}
}

func TestMatchPayments(t *testing.T) {
	acct, other := uuid.New(), uuid.New()
	row := testLoanRow()
	row.PaymentAccountID = &acct
	l := LoanFromRows(row, nil, nil)
	exact := PaymentCandidate{ID: uuid.New(), AccountID: acct, Date: d("2025-05-02"), Amount: 1224.17}
	late := PaymentCandidate{ID: uuid.New(), AccountID: acct, Date: d("2025-06-04"), Amount: 1224.17}
	wrongAcct := PaymentCandidate{ID: uuid.New(), AccountID: other, Date: d("2025-07-01"), Amount: 1224.17}
	wrongAmt := PaymentCandidate{ID: uuid.New(), AccountID: acct, Date: d("2025-08-01"), Amount: 1200}
	tooFar := PaymentCandidate{ID: uuid.New(), AccountID: acct, Date: d("2025-09-15"), Amount: 1224.17}
	taken := PaymentCandidate{ID: uuid.New(), AccountID: acct, Date: d("2025-10-01"), Amount: 1224.17}

	links := MatchPayments(l, []PaymentCandidate{exact, late, wrongAcct, wrongAmt, tooFar, taken},
		map[uuid.UUID]bool{taken.ID: true}, d("2026-11-11"))
	if len(links) != 2 {
		t.Fatalf("got %d links, want 2: %+v", len(links), links)
	}
	if links[0].TransactionID != exact.ID || !links[0].DueDate.Equal(d("2025-05-01")) {
		t.Errorf("first link = %+v", links[0])
	}
	if links[1].TransactionID != late.ID || !links[1].DueDate.Equal(d("2025-06-01")) {
		t.Errorf("second link = %+v", links[1])
	}

	// Already-linked due dates and future instalments are skipped.
	l.Links = []db.LoanPaymentLink{{TransactionID: exact.ID, LoanID: row.ID, DueDate: d("2025-05-01")}}
	if again := MatchPayments(l, []PaymentCandidate{late}, map[uuid.UUID]bool{exact.ID: true}, d("2025-05-20")); len(again) != 0 {
		t.Errorf("future due date matched: %+v", again)
	}
}
