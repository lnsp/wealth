package assets

import (
	"testing"
	"time"

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
	}, nil)

	// The backfill iterates local midnights; 2026-08-01 00:00 CEST is
	// 2026-07-31 22:00 UTC and must still count as the purchase day.
	got := TotalValueAt([]Asset{a}, time.Date(2026, 8, 1, 0, 0, 0, 0, berlin))
	if got != 44900 {
		t.Errorf("value on purchase day = %.2f, want 44900", got)
	}
	if v := TotalValueAt([]Asset{a}, time.Date(2026, 7, 31, 0, 0, 0, 0, berlin)); v != 0 {
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
	})
	if v := a.ValueOn(time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)); v != 47500 {
		t.Errorf("appraisal day value = %.2f, want 47500", v)
	}
	if km := a.Model.OdometerAt(time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)); km != 6000 {
		t.Errorf("odometer = %.0f, want 6000", km)
	}
}
