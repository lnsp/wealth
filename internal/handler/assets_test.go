package handler

import (
	"math"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/lnsp/wealth/internal/analytics"
	"github.com/lnsp/wealth/internal/assets"
	db "github.com/lnsp/wealth/internal/database/generated"
)

func day(s string) time.Time {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(err)
	}
	return t
}

func testCarRow() db.Asset {
	return db.Asset{
		ID:                 uuid.New(),
		Kind:               assets.KindCar,
		Name:               "ID.3 Neo",
		ListPrice:          numericFromFloat(58000),
		PurchasePrice:      numericFromFloat(44900),
		PurchaseCosts:      numericFromFloat(1600),
		PurchaseDate:       day("2026-08-01"),
		PurchaseOdometerKm: 2700,
		AnnualDepreciation: numericFromFloat(0.078),
		KmDepreciation:     numericFromFloat(0.03),
		ExpectedKmPerYear:  15000,
	}
}

func testApartmentRow() db.Asset {
	return db.Asset{
		ID:              uuid.New(),
		Kind:            assets.KindApartment,
		Name:            "Wohnung",
		PurchasePrice:   numericFromFloat(320000),
		PurchaseCosts:   numericFromFloat(35424),
		PurchaseDate:    day("2025-04-01"),
		NotarizedDate:   pgtype.Date{Time: day("2025-02-12"), Valid: true},
		LandValue:       numericFromFloat(75000),
		YearBuilt:       pgtype.Int4{Int32: 1998, Valid: true},
		UsefulLifeYears: 80,
		PriceDrift:      numericFromFloat(0.02),
		SelfUsed:        true,
	}
}

func testMortgageRow(assetID uuid.UUID) db.Loan {
	return db.Loan{
		ID:               uuid.New(),
		AssetID:          assetID,
		Name:             "Annuity loan",
		Principal:        numericFromFloat(260000),
		PayoutDate:       day("2025-04-01"),
		FirstPaymentDate: day("2025-05-01"),
		NominalRate:      numericFromFloat(0.0365),
		MonthlyPayment:   numericFromFloat(1224.17),
		FixedUntil:       pgtype.Date{Time: day("2040-04-01"), Valid: true},
	}
}

func TestAssetRequestValidate_Defaults(t *testing.T) {
	p, err := assetRequest{Name: "Car", PurchasePrice: 44900, PurchaseDate: "2026-08-01"}.validate()
	if err != nil {
		t.Fatal(err)
	}
	if p.Kind != assets.KindCar || p.ExpectedKmPerYear != 15000 || !p.SelfUsed {
		t.Errorf("car defaults not applied: %+v", p.CreateAssetParams)
	}
	if numericToFloat(p.AnnualDepreciation) != 0.078 || numericToFloat(p.PriceDrift) != 0 {
		t.Errorf("annual=%v drift=%v", numericToFloat(p.AnnualDepreciation), numericToFloat(p.PriceDrift))
	}

	year := int32(1998)
	p, err = assetRequest{Kind: "apartment", Name: "Wohnung", PurchasePrice: 320000, PurchaseDate: "2025-04-01", YearBuilt: &year}.validate()
	if err != nil {
		t.Fatal(err)
	}
	if numericToFloat(p.PriceDrift) != 0.02 || p.UsefulLifeYears != 80 || numericToFloat(p.LandValue) != 80000 {
		t.Errorf("property defaults: drift=%v life=%d land=%v", numericToFloat(p.PriceDrift), p.UsefulLifeYears, numericToFloat(p.LandValue))
	}
	if p.PriceIndex != assets.IndexEurostatDE {
		t.Errorf("apartments should follow the Eurostat index by default, got %q", p.PriceIndex)
	}
	fixed := ""
	p, _ = assetRequest{Kind: "house", Name: "Haus", PurchasePrice: 500000, PurchaseDate: "2025-04-01", YearBuilt: &year, PriceIndex: &fixed}.validate()
	if p.PriceIndex != "" {
		t.Errorf("explicit fixed drift ignored: %q", p.PriceIndex)
	}
}

func TestAssetRequestValidate_Rejects(t *testing.T) {
	one, neg := 1.0, int32(-1)
	tooMuchLand, year := 400000.0, int32(1998)
	idx, unknown := assets.IndexEurostatDE, "acme-index"
	cases := map[string]assetRequest{
		"missing name":      {PurchasePrice: 1, PurchaseDate: "2026-08-01"},
		"zero price":        {Name: "x", PurchaseDate: "2026-08-01"},
		"bad date":          {Name: "x", PurchasePrice: 1, PurchaseDate: "01.08.2026"},
		"bad kind":          {Kind: "boat", Name: "x", PurchasePrice: 1, PurchaseDate: "2026-08-01"},
		"rate of 100 %":     {Name: "x", PurchasePrice: 1, PurchaseDate: "2026-08-01", AnnualDepreciation: &one},
		"negative km/yr":    {Name: "x", PurchasePrice: 1, PurchaseDate: "2026-08-01", ExpectedKmPerYear: &neg},
		"bad txn id":        {Name: "x", PurchasePrice: 1, PurchaseDate: "2026-08-01", TransactionIDs: []string{"nope"}},
		"bad first reg":     {Name: "x", PurchasePrice: 1, PurchaseDate: "2026-08-01", FirstRegistration: "2026-13-01"},
		"negative costs":    {Name: "x", PurchasePrice: 1, PurchaseDate: "2026-08-01", PurchaseCosts: -5},
		"apartment no year": {Kind: "apartment", Name: "x", PurchasePrice: 320000, PurchaseDate: "2025-04-01"},
		"land above price":  {Kind: "house", Name: "x", PurchasePrice: 320000, PurchaseDate: "2025-04-01", YearBuilt: &year, LandValue: &tooMuchLand},
		"drift of 100 %":    {Kind: "land", Name: "x", PurchasePrice: 1, PurchaseDate: "2025-04-01", PriceDrift: &one},
		"index on land":     {Kind: "land", Name: "x", PurchasePrice: 1, PurchaseDate: "2025-04-01", PriceIndex: &idx},
		"unknown index":     {Kind: "house", Name: "x", PurchasePrice: 1, PurchaseDate: "2025-04-01", YearBuilt: &year, PriceIndex: &unknown},
	}
	for name, req := range cases {
		if _, err := req.validate(); err == nil {
			t.Errorf("%s: expected validation error", name)
		}
	}
}

func TestLoanRequestValidate(t *testing.T) {
	rep := 0.02
	p, err := loanRequest{Principal: 260000, PayoutDate: "2025-04-01", NominalRate: 0.0365, InitialRepayment: &rep, FixedUntil: "2040-04-01"}.validate()
	if err != nil {
		t.Fatal(err)
	}
	if numericToFloat(p.MonthlyPayment) != 1224.17 || p.FirstPaymentDate.Format("2006-01-02") != "2025-05-01" || p.Name != "Loan" {
		t.Errorf("derived: payment=%v first=%s name=%q", numericToFloat(p.MonthlyPayment), p.FirstPaymentDate.Format("2006-01-02"), p.Name)
	}
	tiny := 100.0
	bad := map[string]loanRequest{
		"no payment":       {Principal: 1000, PayoutDate: "2025-04-01", NominalRate: 0.03},
		"doesn't cover":    {Principal: 260000, PayoutDate: "2025-04-01", NominalRate: 0.0365, MonthlyPayment: &tiny},
		"first before":     {Principal: 1000, PayoutDate: "2025-04-01", FirstPaymentDate: "2025-03-01", NominalRate: 0.03, InitialRepayment: &rep},
		"rate as percent":  {Principal: 1000, PayoutDate: "2025-04-01", NominalRate: 3.65, InitialRepayment: &rep},
		"bad payment acct": {Principal: 1000, PayoutDate: "2025-04-01", NominalRate: 0.03, InitialRepayment: &rep, PaymentAccountID: "x"},
	}
	for name, req := range bad {
		if _, err := req.validate(); err == nil {
			t.Errorf("%s: expected validation error", name)
		}
	}
}

func TestBuildAssetView_Car(t *testing.T) {
	row := testCarRow()
	a := assets.FromRows(row, []db.AssetObservation{{
		AssetID: row.ID, Date: day("2026-11-11"), AppraisedValue: numericFromFloat(47500),
	}}, nil, nil)
	v := buildAssetView(a, nil, time.Date(2026, 11, 11, 15, 0, 0, 0, time.UTC))

	if v.CurrentValue != 47500 || v.ValueChange != 2600 || v.CostBasis != 46500 || v.VsCostBasis != 1000 {
		t.Errorf("current=%.0f change=%.0f basis=%.0f vsBasis=%.0f", v.CurrentValue, v.ValueChange, v.CostBasis, v.VsCostBasis)
	}
	if v.Yours != 47500 || v.YoursPct != 100 || v.Owed != 0 {
		t.Errorf("yours=%.0f (%.0f %%) owed=%.0f", v.Yours, v.YoursPct, v.Owed)
	}
	if v.Car == nil || v.Property != nil || v.TaxFreeFrom != nil {
		t.Error("car view expected, no tax date")
	}
	// ≈ 47,500 · (1 − 0.8809^(1/12)) ≈ 500 €/month.
	if v.MonthlyChange > -450 || v.MonthlyChange < -550 {
		t.Errorf("monthly change = %.2f, want ≈ −500", v.MonthlyChange)
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
	if last := v.Curve[len(v.Curve)-1]; last.Date != "2036-08-01" {
		t.Errorf("car curve ends %s, want 10 years after purchase", last.Date)
	}
}

func TestBuildAssetView_ApartmentWithMortgage(t *testing.T) {
	row := testApartmentRow()
	a := assets.FromRows(row, nil, nil, nil)
	loan := assets.LoanFromRows(testMortgageRow(row.ID), nil, nil)
	v := buildAssetView(a, []assets.Loan{loan}, day("2026-11-11"))

	if math.Abs(v.CurrentValue-322650) > 300 {
		t.Errorf("value = %.0f, want ≈322.650", v.CurrentValue)
	}
	if math.Abs(v.Owed-251537) > 3 {
		t.Errorf("owed = %.0f, want ≈251.537 after 19 payments", v.Owed)
	}
	if math.Abs(v.Yours-(v.CurrentValue-v.Owed)) > 0.01 || v.YoursPct < 21 || v.YoursPct > 23 {
		t.Errorf("yours = %.0f (%.1f %%)", v.Yours, v.YoursPct)
	}
	if v.Property == nil || v.Property.LandValueNow < 77000 || v.Property.RemainingLife < 51 || v.Property.RemainingLife > 51.5 {
		t.Errorf("property breakdown = %+v", v.Property)
	}
	if v.Property.PriceEffectYear <= 0 || v.Property.AgingEffectYear >= 0 {
		t.Errorf("prices should add and aging subtract: %+v", v.Property)
	}
	if v.TaxFreeFrom != nil || v.TaxNote != "Tax-free while self-used" {
		t.Errorf("tax = %v %q", v.TaxFreeFrom, v.TaxNote)
	}
	if last := v.Curve[len(v.Curve)-1]; last.Date != "2045-04-01" || last.Owed <= 0 || last.Owed >= 156357 {
		t.Errorf("curve end = %+v", last)
	}

	row.SelfUsed = false
	v = buildAssetView(assets.FromRows(row, nil, nil, nil), nil, day("2026-11-11"))
	if v.TaxFreeFrom == nil || *v.TaxFreeFrom != "2035-02-12" {
		t.Errorf("rented: tax-free from %v, want 10 y after notarisation", v.TaxFreeFrom)
	}
}

func TestBuildLoanSummary(t *testing.T) {
	loan := assets.LoanFromRows(testMortgageRow(uuid.New()), nil, nil)
	v := buildLoanSummary(loan, "Wohnung", day("2026-11-11"))
	if v.DueCount != 19 || math.Abs(v.Balance-251537) > 3 || math.Abs(v.Repaid-8463) > 3 {
		t.Errorf("due=%d balance=%.0f repaid=%.0f", v.DueCount, v.Balance, v.Repaid)
	}
	if v.InitialRepayment != 0.02 || v.NextPayment == nil || v.NextPayment.Due != "2026-12-01" {
		t.Errorf("initial=%v next=%+v", v.InitialRepayment, v.NextPayment)
	}
	if v.RemainingAtFixedEnd == nil || math.Abs(*v.RemainingAtFixedEnd-156357) > 5 || v.MonthsLeftFixed == nil || *v.MonthsLeftFixed != 161 {
		t.Errorf("fixed end: remaining=%v months=%v", v.RemainingAtFixedEnd, v.MonthsLeftFixed)
	}
	if math.Abs(v.InterestPaid-14797) > 20 || v.PayoffDate[:4] != "2053" {
		t.Errorf("interest paid=%.0f payoff=%s", v.InterestPaid, v.PayoffDate)
	}
}

// The attribution identity must hold for any mix of linked and unlinked
// payments, and market returns must not absorb purchases, mortgage payouts,
// repayments, depreciation or purchase costs.
func TestPhysicalAttribution_Identity(t *testing.T) {
	apt := testApartmentRow()
	car := testCarRow()
	today := day("2026-11-11")

	// Own funds for the apartment and the car payment were linked; the first
	// six mortgage instalments were paid from an untracked account, the rest
	// matched to Giro withdrawals.
	ownFunds := db.ListTransactionsRow{ID: uuid.New(), Date: day("2025-03-28"), Type: "withdrawal", Amount: numericFromFloat(95424)}
	carPay := db.ListTransactionsRow{ID: uuid.New(), Date: day("2026-08-01"), Type: "withdrawal", Amount: numericFromFloat(46500)}
	txns := []db.ListTransactionsRow{ownFunds, carPay}
	mortgageRow := testMortgageRow(apt.ID)
	plain := assets.LoanFromRows(mortgageRow, nil, nil)
	var links []db.LoanPaymentLink
	for i, p := range plain.Schedule {
		if p.Due.After(today) {
			break
		}
		if i < 6 {
			continue
		}
		txn := db.ListTransactionsRow{ID: uuid.New(), Date: p.Due, Type: "withdrawal", Amount: numericFromFloat(1224.17)}
		txns = append(txns, txn)
		links = append(links, db.LoanPaymentLink{TransactionID: txn.ID, LoanID: mortgageRow.ID, DueDate: p.Due})
	}
	holdings := assets.Holdings{
		Assets: []assets.Asset{
			assets.FromRows(apt, nil, nil, []db.AssetTransaction{{TransactionID: ownFunds.ID, AssetID: apt.ID, Purpose: "purchase"}}),
			assets.FromRows(car, nil, nil, []db.AssetTransaction{{TransactionID: carPay.ID, AssetID: car.ID, Purpose: "purchase"}}),
		},
		Loans: []assets.Loan{assets.LoanFromRows(mortgageRow, nil, links)},
	}
	pa := buildPhysicalAttribution(holdings, txns, today)

	// Cash side: every withdrawal leaves the tracked accounts; none of them
	// are classified as contributions because they're all linked.
	cashOut := 0.0
	for _, txn := range txns {
		if !pa.linked[txn.ID] {
			t.Fatalf("transaction %s should be linked", txn.ID)
		}
		cashOut += numericToFloat(txn.Amount)
	}
	start := day("2025-01-01")
	physicalNow, debtNow := holdings.PhysicalAt(today), holdings.DebtAt(today)
	nwChange := -cashOut + physicalNow - debtNow // no market moves in this test

	w := pa.window(start, today)
	explained := w.contrib + (physicalNow - w.capital) - w.costs - w.interest
	if math.Abs(nwChange-explained) > 0.05 {
		t.Fatalf("identity broken: ΔNW %.2f vs explained %.2f (market residual would be %.2f)", nwChange, explained, nwChange-explained)
	}
	// The six unlinked instalments came from outside: they are the only contribution.
	if math.Abs(w.contrib-6*1224.17) > 0.05 {
		t.Errorf("contributions = %.2f, want 6 unlinked instalments = %.2f", w.contrib, 6*1224.17)
	}
	if math.Abs(w.costs-(35424+1600)) > 0.01 {
		t.Errorf("purchase costs = %.2f", w.costs)
	}
	if w.interest < 14700 || w.interest > 14900 {
		t.Errorf("loan interest = %.2f, want ≈14.797", w.interest)
	}

	// Monthly totals add up to the window.
	var sum physicalTotals
	for _, m := range pa.monthly() {
		sum.contrib += m.contrib
		sum.capital += m.capital
	}
	if math.Abs(sum.contrib-w.contrib) > 0.01 || math.Abs(sum.capital-w.capital) > 0.01 {
		t.Errorf("monthly sums %+v vs window %+v", sum, w)
	}
}

func TestInvestableNetWorth(t *testing.T) {
	s := db.NetWorthSnapshot{
		Total:             numericFromFloat(328275),
		PhysicalComponent: numericFromFloat(370187),
		DebtComponent:     numericFromFloat(251537),
	}
	if got := investableNetWorth(s); got != 209625 {
		t.Errorf("investable = %.0f, want 209625", got)
	}
	if !isAssetPurchase(pgtype.Text{String: "asset_purchase", Valid: true}) || isAssetPurchase(pgtype.Text{}) {
		t.Error("isAssetPurchase misclassifies")
	}
	if !isLoanPayment(pgtype.Text{String: "loan_payment", Valid: true}) {
		t.Error("isLoanPayment misclassifies")
	}
}

func TestBuildAssetView_FollowsIndex(t *testing.T) {
	row := testApartmentRow()
	row.PriceIndex = assets.IndexEurostatDE
	ix := analytics.NewQuarterlyIndex(
		[]time.Time{day("2025-01-01"), day("2025-04-01"), day("2025-07-01"), day("2025-10-01"), day("2026-01-01"), day("2026-04-01")},
		[]float64{151.5, 153.0, 154.4, 153.2, 153.3, 153.7},
	)
	v := buildAssetView(assets.FromRowsWithIndex(row, nil, nil, nil, ix), nil, day("2026-11-11"))
	if v.Index == nil || v.Index.LatestQuarter != "2026-Q2" || v.Index.LatestValue != 153.7 || v.Index.YearOverYear == nil {
		t.Fatalf("index info = %+v", v.Index)
	}
	// The index rose much less than 2 %/yr since purchase, so the value is
	// below the fixed-drift model's.
	drift := buildAssetView(assets.FromRows(testApartmentRow(), nil, nil, nil), nil, day("2026-11-11"))
	if v.CurrentValue >= drift.CurrentValue {
		t.Errorf("index-based %.0f should be below drift-based %.0f", v.CurrentValue, drift.CurrentValue)
	}
}
