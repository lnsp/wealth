package handler

import (
	"math"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/lnsp/wealth/internal/assets"
	db "github.com/lnsp/wealth/internal/database/generated"
)

func testAssetRow() db.Asset {
	return db.Asset{
		ID:                 uuid.New(),
		Kind:               "car",
		Name:               "ID.3 Neo",
		ListPrice:          numericFromFloat(58000),
		PurchasePrice:      numericFromFloat(44900),
		PurchaseDate:       time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
		PurchaseOdometerKm: 2700,
		AnnualDepreciation: numericFromFloat(0.078),
		KmDepreciation:     numericFromFloat(0.03),
		ExpectedKmPerYear:  15000,
	}
}

func TestAssetRequestValidate_Defaults(t *testing.T) {
	p, err := assetRequest{Name: "Car", PurchasePrice: 44900, PurchaseDate: "2026-08-01"}.validate()
	if err != nil {
		t.Fatal(err)
	}
	if p.kind != "car" || p.kmPerYear != 15000 {
		t.Errorf("defaults not applied: kind=%q km/yr=%d", p.kind, p.kmPerYear)
	}
	if got := numericToFloat(p.annual); got != 0.078 {
		t.Errorf("annual = %v, want 0.078", got)
	}
	if got := numericToFloat(p.perKm); got != 0.03 {
		t.Errorf("per-km = %v, want 0.03", got)
	}
	if p.listPrice.Valid || p.purchaseTxnID != nil || p.firstRegistration.Valid {
		t.Error("optional fields should stay unset")
	}
}

func TestAssetRequestValidate_Rejects(t *testing.T) {
	one, neg := 1.0, int32(-1)
	cases := map[string]assetRequest{
		"missing name":      {PurchasePrice: 1, PurchaseDate: "2026-08-01"},
		"zero price":        {Name: "x", PurchaseDate: "2026-08-01"},
		"bad date":          {Name: "x", PurchasePrice: 1, PurchaseDate: "01.08.2026"},
		"bad kind":          {Kind: "boat", Name: "x", PurchasePrice: 1, PurchaseDate: "2026-08-01"},
		"rate of 100 %":     {Name: "x", PurchasePrice: 1, PurchaseDate: "2026-08-01", AnnualDepreciation: &one},
		"negative km/yr":    {Name: "x", PurchasePrice: 1, PurchaseDate: "2026-08-01", ExpectedKmPerYear: &neg},
		"bad txn id":        {Name: "x", PurchasePrice: 1, PurchaseDate: "2026-08-01", PurchaseTransactionID: "nope"},
		"bad first reg":     {Name: "x", PurchasePrice: 1, PurchaseDate: "2026-08-01", FirstRegistration: "2026-13-01"},
		"negative odometer": {Name: "x", PurchasePrice: 1, PurchaseDate: "2026-08-01", PurchaseOdometerKm: -5},
	}
	for name, req := range cases {
		if _, err := req.validate(); err == nil {
			t.Errorf("%s: expected validation error", name)
		}
	}
}

func TestBuildAssetView(t *testing.T) {
	obs := []db.AssetObservation{{
		Date: time.Date(2026, 11, 11, 0, 0, 0, 0, time.UTC), AppraisedValue: numericFromFloat(47500),
	}}
	a := assets.FromRows(testAssetRow(), obs)
	now := time.Date(2026, 11, 11, 15, 0, 0, 0, time.UTC)
	v := buildAssetView(a, now)

	if v.CurrentValue != 47500 || v.ValueChange != 2600 {
		t.Errorf("current=%.2f change=%.2f, want 47500 / 2600", v.CurrentValue, v.ValueChange)
	}
	if v.PctOfList == nil || math.Abs(*v.PctOfList-81.9) > 0.05 {
		t.Errorf("pct of list = %v, want ≈81.9", v.PctOfList)
	}
	// ≈ 47,500 · (1 − 0.8809^(1/12)) ≈ 500 €/month.
	if v.MonthlyDepreciation < 450 || v.MonthlyDepreciation > 550 {
		t.Errorf("monthly depreciation = %.2f, want ≈500", v.MonthlyDepreciation)
	}

	// Curve: starts at purchase, has today as the last non-projected point,
	// then strictly declining forecast for ~10 years.
	if v.Curve[0].Date != "2026-08-01" || v.Curve[0].Value != 44900 || v.Curve[0].Projected {
		t.Errorf("first point = %+v", v.Curve[0])
	}
	seam := -1
	for i, p := range v.Curve {
		if p.Projected {
			seam = i
			break
		}
	}
	if seam < 1 || v.Curve[seam-1].Date != "2026-11-11" || v.Curve[seam-1].Value != 47500 {
		t.Fatalf("history/forecast seam wrong: %+v", v.Curve[seam-1])
	}
	for i := seam + 1; i < len(v.Curve); i++ {
		if v.Curve[i].Value >= v.Curve[i-1].Value {
			t.Fatalf("forecast not declining at %s", v.Curve[i].Date)
		}
	}
	if last := v.Curve[len(v.Curve)-1]; last.Date != "2036-08-01" {
		t.Errorf("curve ends %s, want 2036-08-01", last.Date)
	}
}

func TestBuildAssetView_FuturePurchase(t *testing.T) {
	a := assets.FromRows(testAssetRow(), nil)
	v := buildAssetView(a, time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC))
	if v.CurrentValue != 44900 || v.ValueChange != 0 {
		t.Errorf("before purchase: current=%.2f change=%.2f", v.CurrentValue, v.ValueChange)
	}
	for _, p := range v.Curve {
		if !p.Projected {
			t.Fatalf("all points should be projected before purchase, got %+v", p)
		}
	}
}

func TestAssetAttribution_PurchasesIn(t *testing.T) {
	linked := testAssetRow()
	txnID := uuid.New()
	linked.PurchaseTransactionID = &txnID
	unlinked := testAssetRow()
	unlinked.PurchasePrice = numericFromFloat(10000)
	unlinked.PurchaseDate = time.Date(2025, 3, 1, 0, 0, 0, 0, time.UTC)

	all := []assets.Asset{assets.FromRows(linked, nil), assets.FromRows(unlinked, nil)}
	attr := assetAttribution{assets: all, linked: assets.PurchaseTransactionIDs(all)}
	if !attr.linked[txnID] || len(attr.linked) != 1 {
		t.Fatalf("linked = %v", attr.linked)
	}

	now := time.Date(2026, 11, 11, 0, 0, 0, 0, time.UTC)
	if a, u := attr.purchasesIn(time.Time{}, now); a != 54900 || u != 10000 {
		t.Errorf("all-time: all=%.0f unlinked=%.0f, want 54900 / 10000", a, u)
	}
	// Window is (from, to]: a purchase exactly on the target date is before it.
	if a, u := attr.purchasesIn(time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC), now); a != 0 || u != 0 {
		t.Errorf("window starting on purchase day: all=%.0f unlinked=%.0f, want 0", a, u)
	}
	if a, _ := attr.purchasesIn(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), now); a != 44900 {
		t.Errorf("2026 window: all=%.0f, want 44900", a)
	}
}

func TestInvestableNetWorth(t *testing.T) {
	s := db.NetWorthSnapshot{Total: numericFromFloat(300000), PhysicalComponent: numericFromFloat(47500)}
	if got := investableNetWorth(s); got != 252500 {
		t.Errorf("investable = %.0f, want 252500", got)
	}
	if !isAssetPurchase(pgtype.Text{String: "asset_purchase", Valid: true}) || isAssetPurchase(pgtype.Text{}) {
		t.Error("isAssetPurchase misclassifies")
	}
}
