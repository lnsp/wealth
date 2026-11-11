package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/lnsp/wealth/internal/analytics"
	"github.com/lnsp/wealth/internal/assets"
	db "github.com/lnsp/wealth/internal/database/generated"
)

// AssetsHandler manages physical assets, their observations, improvements
// and the loans financing them. Every mutation re-runs the net-worth backfill
// because asset values and loan balances feed historical snapshots.
type AssetsHandler struct {
	queries  *db.Queries
	snapshot SnapshotTrigger
}

func NewAssetsHandler(q *db.Queries, snapshot SnapshotTrigger) *AssetsHandler {
	return &AssetsHandler{queries: q, snapshot: snapshot}
}

// Value curves extend this far past purchase.
const (
	carCurveYears      = 10
	propertyCurveYears = 20
)

type assetCurvePoint struct {
	Date      string  `json:"date"`
	Value     float64 `json:"value"`
	Owed      float64 `json:"owed"`
	Projected bool    `json:"projected"`
}

type assetObservationView struct {
	Date           string   `json:"date"`
	OdometerKm     *int32   `json:"odometer_km"`
	AppraisedValue *float64 `json:"appraised_value"`
	Note           string   `json:"note"`
}

type assetImprovementView struct {
	ID          string  `json:"id"`
	Date        string  `json:"date"`
	Amount      float64 `json:"amount"`
	Description string  `json:"description"`
}

type linkedTransactionView struct {
	ID           string  `json:"id"`
	Date         string  `json:"date"`
	Amount       float64 `json:"amount"`
	Counterparty string  `json:"counterparty"`
	Purpose      string  `json:"purpose"`
}

type carView struct {
	OdometerEstimateKm  float64 `json:"odometer_estimate_km"`
	EffectiveAnnualRate float64 `json:"effective_annual_rate"`
}

type indexInfoView struct {
	ID            string   `json:"id"`
	Label         string   `json:"label"`
	LatestQuarter string   `json:"latest_quarter"`
	LatestValue   float64  `json:"latest_value"`
	YearOverYear  *float64 `json:"year_over_year"`
}

// indexInfo describes a stored index series, nil when it has no data yet.
func indexInfo(id string, ix *analytics.PriceIndex) *indexInfoView {
	last, ok := ix.Latest()
	if !ok {
		return nil
	}
	v := &indexInfoView{ID: id, LatestValue: last.Value}
	for _, s := range assets.IndexSeriesList {
		if s.ID == id {
			v.Label = s.Label
		}
	}
	q := last.Date.AddDate(0, -1, -15) // midpoint back to quarter start
	v.LatestQuarter = fmt.Sprintf("%d-Q%d", q.Year(), (int(q.Month())+2)/3)
	if yoy, ok := ix.YearOverYear(); ok {
		y := math.Round(yoy*10000) / 10000
		v.YearOverYear = &y
	}
	return v
}

type propertyView struct {
	LandValueNow     float64 `json:"land_value_now"`
	BuildingValueNow float64 `json:"building_value_now"`
	RemainingLife    float64 `json:"remaining_life_years"`
	PriceEffectYear  float64 `json:"price_effect_per_year"`
	AgingEffectYear  float64 `json:"aging_effect_per_year"`
	HasBuildingAging bool    `json:"has_building_aging"`
}

type assetView struct {
	ID                 string   `json:"id"`
	Kind               string   `json:"kind"`
	Name               string   `json:"name"`
	Details            string   `json:"details"`
	ListPrice          *float64 `json:"list_price"`
	PurchasePrice      float64  `json:"purchase_price"`
	PurchaseCosts      float64  `json:"purchase_costs"`
	CostBasis          float64  `json:"cost_basis"`
	PurchaseDate       string   `json:"purchase_date"`
	NotarizedDate      *string  `json:"notarized_date"`
	FirstRegistration  *string  `json:"first_registration"`
	PurchaseOdometerKm int32    `json:"purchase_odometer_km"`
	AnnualDepreciation float64  `json:"annual_depreciation"`
	KmDepreciation     float64  `json:"km_depreciation"`
	ExpectedKmPerYear  int32    `json:"expected_km_per_year"`
	LandValue          *float64 `json:"land_value"`
	YearBuilt          *int32   `json:"year_built"`
	UsefulLifeYears    int32    `json:"useful_life_years"`
	PriceDrift         float64  `json:"price_drift"`
	PriceIndex         string   `json:"price_index"`
	SelfUsed           bool     `json:"self_used"`
	LivingAreaM2       *float64 `json:"living_area_m2"`

	CurrentValue   float64  `json:"current_value"`
	ValueChange    float64  `json:"value_change"`     // vs purchase price
	ValueChangePct float64  `json:"value_change_pct"` // vs purchase price
	VsCostBasis    float64  `json:"vs_cost_basis"`
	MonthlyChange  float64  `json:"monthly_change"`
	PctOfList      *float64 `json:"pct_of_list"`
	Owed           float64  `json:"owed"`
	Yours          float64  `json:"yours"`
	YoursPct       float64  `json:"yours_pct"`
	LoanIDs        []string `json:"loan_ids"`
	TaxFreeFrom    *string  `json:"tax_free_from"`
	TaxNote        string   `json:"tax_note"`

	Car                *carView                `json:"car"`
	Property           *propertyView           `json:"property"`
	Index              *indexInfoView          `json:"index"` // set when following a price index
	Observations       []assetObservationView  `json:"observations"`
	Improvements       []assetImprovementView  `json:"improvements"`
	LinkedTransactions []linkedTransactionView `json:"linked_transactions"`
	Curve              []assetCurvePoint       `json:"curve"`
}

func round2(v float64) float64 { return math.Round(v*100) / 100 }

func dateString(d pgtype.Date) *string {
	if !d.Valid {
		return nil
	}
	s := d.Time.Format("2006-01-02")
	return &s
}

func optionalNumeric(n pgtype.Numeric) *float64 {
	if !n.Valid {
		return nil
	}
	v := numericToFloat(n)
	return &v
}

// taxStatus explains whether selling would be taxed (§23 EStG): property is
// tax-free when self-used or held 10 years since notarisation, other valuables
// after one year, cars as everyday items never.
func taxStatus(a assets.Asset) (*string, string) {
	switch a.Kind {
	case assets.KindCar:
		return nil, "Everyday item — no tax on a sale"
	case assets.KindApartment, assets.KindHouse, assets.KindLand:
		if a.SelfUsed && a.Kind != assets.KindLand {
			return nil, "Tax-free while self-used"
		}
		start := a.PurchaseDate
		if a.NotarizedDate.Valid {
			start = a.NotarizedDate.Time
		}
		s := start.AddDate(10, 0, 0).Format("2006-01-02")
		return &s, "Tax-free 10 years after notarisation"
	default:
		s := a.PurchaseDate.AddDate(1, 0, 0).Format("2006-01-02")
		return &s, "Tax-free after one year"
	}
}

func (h *AssetsHandler) linkedTransactions(ctx context.Context, a assets.Asset) []linkedTransactionView {
	out := make([]linkedTransactionView, 0, len(a.Links))
	for _, l := range a.Links {
		txn, err := h.queries.GetTransaction(ctx, l.TransactionID)
		if err != nil {
			continue
		}
		out = append(out, linkedTransactionView{
			ID: txn.ID.String(), Date: txn.Date.Format("2006-01-02"), Amount: numericToFloat(txn.Amount),
			Counterparty: txn.Counterparty.String, Purpose: l.Purpose,
		})
	}
	return out
}

func buildAssetView(a assets.Asset, loans []assets.Loan, now time.Time) assetView {
	today := assets.CivilDate(now)
	cur := a.ValueOn(today)
	price := a.PurchasePrice()
	owned := !today.Before(a.PurchaseDate)
	if !owned {
		cur = price // not owned yet: show the price, not the pre-purchase zero
	}
	owed := 0.0
	loanIDs := make([]string, 0, len(loans))
	for _, l := range loans {
		owed += l.BalanceOn(today)
		loanIDs = append(loanIDs, l.ID.String())
	}
	v := assetView{
		ID: a.ID.String(), Kind: a.Kind, Name: a.Name, Details: a.Details,
		ListPrice: optionalNumeric(a.ListPrice), PurchasePrice: price, PurchaseCosts: a.PurchaseCosts(),
		CostBasis: round2(a.CostBasis()), PurchaseDate: a.PurchaseDate.Format("2006-01-02"),
		NotarizedDate: dateString(a.NotarizedDate), FirstRegistration: dateString(a.FirstRegistration),
		PurchaseOdometerKm: a.PurchaseOdometerKm, AnnualDepreciation: numericToFloat(a.AnnualDepreciation),
		KmDepreciation: numericToFloat(a.KmDepreciation), ExpectedKmPerYear: a.ExpectedKmPerYear,
		LandValue: optionalNumeric(a.LandValue), UsefulLifeYears: a.UsefulLifeYears,
		PriceDrift: numericToFloat(a.PriceDrift), PriceIndex: a.Asset.PriceIndex, SelfUsed: a.SelfUsed, LivingAreaM2: optionalNumeric(a.LivingAreaM2),
		CurrentValue: round2(cur), ValueChange: round2(cur - price), VsCostBasis: round2(cur - a.CostBasis()),
		Owed: round2(owed), Yours: round2(cur - owed), LoanIDs: loanIDs,
		Observations: make([]assetObservationView, 0, len(a.Observations)),
		Improvements: make([]assetImprovementView, 0, len(a.Improvements)),
	}
	if a.YearBuilt.Valid {
		y := a.YearBuilt.Int32
		v.YearBuilt = &y
	}
	if owned {
		v.MonthlyChange = round2(a.ValueOn(today.AddDate(0, 1, 0)) - cur)
	}
	if price > 0 {
		v.ValueChangePct = round2(v.ValueChange / price * 100)
	}
	if cur > 0 {
		v.YoursPct = round2(v.Yours / cur * 100)
	}
	if v.ListPrice != nil && *v.ListPrice > 0 {
		pct := round2(cur / *v.ListPrice * 100)
		v.PctOfList = &pct
	}
	v.TaxFreeFrom, v.TaxNote = taxStatus(a)

	if a.Car != nil {
		v.Car = &carView{OdometerEstimateKm: math.Round(a.Car.OdometerAt(today)), EffectiveAnnualRate: a.Car.EffectiveAnnualRate()}
	}
	if a.Property != nil && owned {
		land, building := a.Property.Components(today)
		next := a.ValueOn(today.AddDate(1, 0, 0)) - cur
		prices := (land + building) * (a.Property.PriceGrowth(today, today.AddDate(1, 0, 0)) - 1)
		pv := &propertyView{
			LandValueNow: round2(land), BuildingValueNow: round2(building),
			HasBuildingAging: a.Property.BuildingEnd > 0,
			PriceEffectYear:  round2(prices), AgingEffectYear: round2(next - prices),
		}
		if pv.HasBuildingAging {
			remaining := a.Property.BuildingEnd - float64(today.Year()) - float64(today.YearDay()-1)/365.25
			pv.RemainingLife = math.Round(math.Max(0, remaining)*10) / 10
		}
		v.Property = pv
		if a.Property.Index != nil {
			v.Index = indexInfo(a.Asset.PriceIndex, a.Property.Index)
		}
	}
	for _, o := range a.Observations {
		ov := assetObservationView{Date: o.Date.Format("2006-01-02"), Note: o.Note.String}
		if o.OdometerKm.Valid {
			km := o.OdometerKm.Int32
			ov.OdometerKm = &km
		}
		if o.AppraisedValue.Valid {
			av := numericToFloat(o.AppraisedValue)
			ov.AppraisedValue = &av
		}
		v.Observations = append(v.Observations, ov)
	}
	for _, im := range a.Improvements {
		v.Improvements = append(v.Improvements, assetImprovementView{
			ID: im.ID.String(), Date: im.Date.Format("2006-01-02"), Amount: numericToFloat(im.Amount), Description: im.Description,
		})
	}

	// Monthly curve of value and what is owed, from purchase to the horizon;
	// points after today are a forecast. Today is inserted so the history /
	// forecast seam lands on the current value.
	years := propertyCurveYears
	if a.Kind == assets.KindCar {
		years = carCurveYears
	}
	owedOn := func(d time.Time) float64 {
		total := 0.0
		for _, l := range loans {
			total += l.BalanceOn(d)
		}
		return round2(total)
	}
	end := a.PurchaseDate.AddDate(years, 0, 0)
	addedToday := false
	for d := a.PurchaseDate; !d.After(end); d = d.AddDate(0, 1, 0) {
		if !addedToday && d.After(today) && owned {
			v.Curve = append(v.Curve, assetCurvePoint{today.Format("2006-01-02"), round2(cur), owedOn(today), false})
			addedToday = true
		}
		v.Curve = append(v.Curve, assetCurvePoint{d.Format("2006-01-02"), round2(a.ValueOn(d)), owedOn(d), d.After(today)})
	}
	return v
}

// HandleList returns all assets and loans with the net-worth composition the
// Assets tab shows: what every part is worth, what is owed on it, and what is
// yours.
func (h *AssetsHandler) HandleList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	holdings, err := assets.Load(ctx, h.queries)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "load assets: "+err.Error())
		return
	}
	now := time.Now()
	views := make([]assetView, 0, len(holdings.Assets))
	worth, owed := 0.0, 0.0
	for _, a := range holdings.Assets {
		v := buildAssetView(a, holdings.LoansFor(a.ID), now)
		v.LinkedTransactions = h.linkedTransactions(ctx, a)
		worth += v.CurrentValue
		owed += v.Owed
		views = append(views, v)
	}
	loans := make([]loanView, 0, len(holdings.Loans))
	for _, l := range holdings.Loans {
		loans = append(loans, buildLoanSummary(l, assetName(holdings, l.AssetID), now))
	}
	composition := map[string]any{"investments": 0.0, "cash": 0.0}
	if snaps, err := h.queries.ListNetWorthSnapshots(ctx, 1); err == nil && len(snaps) > 0 {
		composition = map[string]any{
			"investments": round2(numericToFloat(snaps[0].InvestmentComponent)),
			"cash":        round2(numericToFloat(snaps[0].CashComponent)),
			"as_of":       snaps[0].Date.Format("2006-01-02"),
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"assets":      views,
		"loans":       loans,
		"totals":      map[string]float64{"worth": round2(worth), "owed": round2(owed), "yours": round2(worth - owed)},
		"composition": composition,
		"defaults": map[string]any{
			"annual_depreciation":  analytics.DefaultCarAnnualDepreciation,
			"km_depreciation":      analytics.DefaultCarKmDepreciation,
			"expected_km_per_year": analytics.DefaultCarKmPerYear,
			"property_drift":       analytics.DefaultPropertyDrift,
			"useful_life_years":    analytics.DefaultBuildingUsefulLife,
			"price_index":          assets.IndexEurostatDE,
		},
		"price_indexes": availableIndexes(holdings),
	})
}

// availableIndexes lists index series with data, for the asset form.
func availableIndexes(h assets.Holdings) []indexInfoView {
	out := []indexInfoView{}
	for _, s := range assets.IndexSeriesList {
		if info := indexInfo(s.ID, h.Indexes[s.ID]); info != nil {
			out = append(out, *info)
		}
	}
	return out
}

func assetName(h assets.Holdings, id uuid.UUID) string {
	for _, a := range h.Assets {
		if a.ID == id {
			return a.Name
		}
	}
	return ""
}

type assetRequest struct {
	Kind               string       `json:"kind"`
	Name               string       `json:"name"`
	Details            string       `json:"details"`
	ListPrice          *float64     `json:"list_price"`
	PurchasePrice      float64      `json:"purchase_price"`
	PurchaseCosts      float64      `json:"purchase_costs"`
	PurchaseDate       string       `json:"purchase_date"`
	NotarizedDate      string       `json:"notarized_date"`
	FirstRegistration  string       `json:"first_registration"`
	PurchaseOdometerKm int32        `json:"purchase_odometer_km"`
	AnnualDepreciation *float64     `json:"annual_depreciation"`
	KmDepreciation     *float64     `json:"km_depreciation"`
	ExpectedKmPerYear  *int32       `json:"expected_km_per_year"`
	LandValue          *float64     `json:"land_value"`
	YearBuilt          *int32       `json:"year_built"`
	UsefulLifeYears    *int32       `json:"useful_life_years"`
	PriceDrift         *float64     `json:"price_drift"`
	PriceIndex         *string      `json:"price_index"` // nil = default for the kind, "" = fixed drift
	SelfUsed           *bool        `json:"self_used"`
	LivingAreaM2       *float64     `json:"living_area_m2"`
	TransactionIDs     []string     `json:"transaction_ids"` // payments for the purchase
	Loan               *loanRequest `json:"loan"`            // optional financing, created with the asset
}

// assetParams is the validated, DB-ready form of an assetRequest.
type assetParams struct {
	db.CreateAssetParams
	txnIDs []uuid.UUID
}

func numericOf(v float64, format string) pgtype.Numeric {
	var n pgtype.Numeric
	n.Scan(fmt.Sprintf(format, v))
	return n
}

func parseOptionalDate(s, field string) (pgtype.Date, error) {
	if s == "" {
		return pgtype.Date{}, nil
	}
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return pgtype.Date{}, fmt.Errorf("invalid %s (expected YYYY-MM-DD)", field)
	}
	return pgtype.Date{Time: t, Valid: true}, nil
}

func (req assetRequest) validate() (assetParams, error) {
	var p assetParams
	kind := req.Kind
	if kind == "" {
		kind = assets.KindCar
	}
	switch kind {
	case assets.KindCar, assets.KindApartment, assets.KindHouse, assets.KindLand, assets.KindOther:
	default:
		return p, errors.New("kind must be car, apartment, house, land or other")
	}
	if req.Name == "" || req.PurchasePrice <= 0 || req.PurchaseDate == "" {
		return p, errors.New("name, purchase_price and purchase_date are required")
	}
	purchaseDate, err := time.Parse("2006-01-02", req.PurchaseDate)
	if err != nil {
		return p, errors.New("invalid purchase_date (expected YYYY-MM-DD)")
	}
	if req.PurchaseCosts < 0 || req.PurchaseOdometerKm < 0 {
		return p, errors.New("purchase_costs and purchase_odometer_km must be >= 0")
	}
	p.Kind, p.Name, p.Details = kind, req.Name, req.Details
	p.PurchaseDate = purchaseDate
	p.PurchasePrice = numericOf(req.PurchasePrice, "%.2f")
	p.PurchaseCosts = numericOf(req.PurchaseCosts, "%.2f")
	p.PurchaseOdometerKm = req.PurchaseOdometerKm
	if p.NotarizedDate, err = parseOptionalDate(req.NotarizedDate, "notarized_date"); err != nil {
		return p, err
	}
	if p.FirstRegistration, err = parseOptionalDate(req.FirstRegistration, "first_registration"); err != nil {
		return p, err
	}
	if req.ListPrice != nil && *req.ListPrice > 0 {
		p.ListPrice = numericOf(*req.ListPrice, "%.2f")
	}

	// Car depreciation (stored for every kind, used for cars only).
	annual, perKm := analytics.DefaultCarAnnualDepreciation, analytics.DefaultCarKmDepreciation
	if req.AnnualDepreciation != nil {
		annual = *req.AnnualDepreciation
	}
	if req.KmDepreciation != nil {
		perKm = *req.KmDepreciation
	}
	if annual < 0 || annual >= 1 || perKm < 0 || perKm >= 1 {
		return p, errors.New("depreciation rates must be fractions in [0, 1)")
	}
	p.AnnualDepreciation = numericOf(annual, "%.4f")
	p.KmDepreciation = numericOf(perKm, "%.4f")
	p.ExpectedKmPerYear = analytics.DefaultCarKmPerYear
	if req.ExpectedKmPerYear != nil {
		if *req.ExpectedKmPerYear < 0 {
			return p, errors.New("expected_km_per_year must be >= 0")
		}
		p.ExpectedKmPerYear = *req.ExpectedKmPerYear
	}

	// Property, land and other valuables.
	drift := 0.0
	if kind != assets.KindCar && kind != assets.KindOther {
		drift = analytics.DefaultPropertyDrift
	}
	if req.PriceDrift != nil {
		drift = *req.PriceDrift
	}
	if drift <= -0.5 || drift >= 0.5 {
		return p, errors.New("price_drift must be a fraction between -0.5 and 0.5")
	}
	p.PriceDrift = numericOf(drift, "%.4f")
	p.UsefulLifeYears = analytics.DefaultBuildingUsefulLife
	if req.UsefulLifeYears != nil {
		if *req.UsefulLifeYears <= 0 {
			return p, errors.New("useful_life_years must be > 0")
		}
		p.UsefulLifeYears = *req.UsefulLifeYears
	}
	p.SelfUsed = true
	if req.SelfUsed != nil {
		p.SelfUsed = *req.SelfUsed
	}
	if req.LivingAreaM2 != nil && *req.LivingAreaM2 > 0 {
		p.LivingAreaM2 = numericOf(*req.LivingAreaM2, "%.2f")
	}
	if req.PriceIndex != nil {
		p.PriceIndex = *req.PriceIndex
	} else if assets.IsProperty(kind) {
		p.PriceIndex = assets.IndexEurostatDE
	}
	if p.PriceIndex != "" && (!assets.IsProperty(kind) || !assets.KnownIndex(p.PriceIndex)) {
		return p, errors.New("price_index is only supported for apartments and houses")
	}
	if assets.IsProperty(kind) {
		if req.YearBuilt == nil || *req.YearBuilt < 1000 || *req.YearBuilt > 2200 {
			return p, errors.New("year_built is required for apartments and houses")
		}
		p.YearBuilt = pgtype.Int4{Int32: *req.YearBuilt, Valid: true}
		land := req.PurchasePrice * 0.25 // fallback land share when unknown
		if req.LandValue != nil {
			land = *req.LandValue
		}
		if land < 0 || land > req.PurchasePrice {
			return p, errors.New("land_value must be between 0 and the purchase price")
		}
		p.LandValue = numericOf(land, "%.2f")
	}
	for _, s := range req.TransactionIDs {
		id, err := uuid.Parse(s)
		if err != nil {
			return p, errors.New("invalid transaction id")
		}
		p.txnIDs = append(p.txnIDs, id)
	}
	return p, nil
}

// errBadPurchaseTxn marks a client error when linking payments.
var errBadPurchaseTxn = errors.New("linked payments must be existing withdrawals not linked elsewhere")

// setPurchaseLinks replaces an asset's purchase payment links: removed ones
// lose the asset_purchase category, new ones get it.
func (h *AssetsHandler) setPurchaseLinks(ctx context.Context, asset assets.Asset, want []uuid.UUID, taken map[uuid.UUID]bool) error {
	keep := make(map[uuid.UUID]bool, len(want))
	for _, id := range want {
		keep[id] = true
	}
	current := make(map[uuid.UUID]bool)
	for _, l := range asset.Links {
		if l.Purpose != "purchase" {
			continue
		}
		current[l.TransactionID] = true
		if !keep[l.TransactionID] {
			if err := h.unlinkTransaction(ctx, l.TransactionID, assets.CategoryPurchase); err != nil {
				return err
			}
		}
	}
	for _, id := range want {
		if current[id] {
			continue
		}
		if taken[id] {
			return errBadPurchaseTxn
		}
		txn, err := h.queries.GetTransaction(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && txn.Type != "withdrawal" && txn.Type != "transfer") {
			return errBadPurchaseTxn
		}
		if err != nil {
			return err
		}
		if err := h.queries.InsertAssetTransaction(ctx, db.InsertAssetTransactionParams{TransactionID: id, AssetID: asset.ID, Purpose: "purchase"}); err != nil {
			return err
		}
		if err := h.queries.UpdateTransactionCategory(ctx, db.UpdateTransactionCategoryParams{
			ID: id, Category: pgtype.Text{String: assets.CategoryPurchase, Valid: true},
		}); err != nil {
			return err
		}
	}
	return nil
}

// unlinkTransaction removes an asset link and clears the category if this
// feature set it.
func (h *AssetsHandler) unlinkTransaction(ctx context.Context, id uuid.UUID, category string) error {
	if err := h.queries.DeleteAssetTransaction(ctx, id); err != nil {
		return err
	}
	return h.clearCategory(ctx, id, category)
}

func (h *AssetsHandler) clearCategory(ctx context.Context, id uuid.UUID, category string) error {
	txn, err := h.queries.GetTransaction(ctx, id)
	if err != nil || txn.Category.String != category {
		return nil
	}
	return h.queries.UpdateTransactionCategory(ctx, db.UpdateTransactionCategoryParams{ID: id})
}

func (h *AssetsHandler) refreshSnapshots() {
	if h.snapshot == nil {
		return
	}
	go func() {
		h.snapshot.BackfillNetWorthSnapshots()
		h.snapshot.RunNetWorthSnapshot()
	}()
}

func (h *AssetsHandler) writeAsset(w http.ResponseWriter, ctx context.Context, status int, id uuid.UUID) {
	holdings, err := assets.Load(ctx, h.queries)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "load assets: "+err.Error())
		return
	}
	for _, a := range holdings.Assets {
		if a.ID == id {
			v := buildAssetView(a, holdings.LoansFor(a.ID), time.Now())
			v.LinkedTransactions = h.linkedTransactions(ctx, a)
			writeJSON(w, status, v)
			return
		}
	}
	writeError(w, http.StatusNotFound, "asset not found")
}

func (h *AssetsHandler) loadAsset(w http.ResponseWriter, ctx context.Context, id uuid.UUID) (assets.Holdings, assets.Asset, bool) {
	holdings, err := assets.Load(ctx, h.queries)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "load assets: "+err.Error())
		return holdings, assets.Asset{}, false
	}
	for _, a := range holdings.Assets {
		if a.ID == id {
			return holdings, a, true
		}
	}
	writeError(w, http.StatusNotFound, "asset not found")
	return holdings, assets.Asset{}, false
}

func parseIDParam(w http.ResponseWriter, r *http.Request, name, what string) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, name))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid "+what+" id")
		return uuid.Nil, false
	}
	return id, true
}

func (h *AssetsHandler) writeLinkError(w http.ResponseWriter, err error) {
	if errors.Is(err, errBadPurchaseTxn) {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeError(w, http.StatusInternalServerError, "link payments: "+err.Error())
}

// HandleCreate creates an asset, links its purchase payments and optionally
// the loan that finances it.
func (h *AssetsHandler) HandleCreate(w http.ResponseWriter, r *http.Request) {
	var req assetRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	p, err := req.validate()
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	var loanParams *db.CreateLoanParams
	if req.Loan != nil {
		lp, err := req.Loan.validate()
		if err != nil {
			writeError(w, http.StatusBadRequest, "loan: "+err.Error())
			return
		}
		loanParams = &lp
	}
	ctx := r.Context()
	holdings, err := assets.Load(ctx, h.queries)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "load assets: "+err.Error())
		return
	}
	row, err := h.queries.CreateAsset(ctx, p.CreateAssetParams)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "create asset: "+err.Error())
		return
	}
	if err := h.setPurchaseLinks(ctx, assets.Asset{Asset: row}, p.txnIDs, holdings.LinkedIDs()); err != nil {
		_ = h.queries.DeleteAsset(ctx, row.ID)
		h.writeLinkError(w, err)
		return
	}
	if loanParams != nil {
		loanParams.AssetID = row.ID
		if _, err := h.queries.CreateLoan(ctx, *loanParams); err != nil {
			writeError(w, http.StatusInternalServerError, "create loan: "+err.Error())
			return
		}
		if _, err := assets.MatchAllLoanPayments(ctx, h.queries); err != nil {
			writeError(w, http.StatusInternalServerError, "match loan payments: "+err.Error())
			return
		}
	}
	h.refreshSnapshots()
	h.writeAsset(w, ctx, http.StatusCreated, row.ID)
}

// HandleUpdate replaces an asset's fields and purchase payment links.
func (h *AssetsHandler) HandleUpdate(w http.ResponseWriter, r *http.Request) {
	id, ok := parseIDParam(w, r, "id", "asset")
	if !ok {
		return
	}
	var req assetRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	p, err := req.validate()
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx := r.Context()
	holdings, existing, ok := h.loadAsset(w, ctx, id)
	if !ok {
		return
	}
	if p.Kind != existing.Kind {
		writeError(w, http.StatusBadRequest, "the kind of an asset can't change")
		return
	}
	taken := holdings.LinkedIDs()
	for _, l := range existing.Links {
		delete(taken, l.TransactionID)
	}
	if err := h.setPurchaseLinks(ctx, existing, p.txnIDs, taken); err != nil {
		h.writeLinkError(w, err)
		return
	}
	c := p.CreateAssetParams
	if _, err := h.queries.UpdateAsset(ctx, db.UpdateAssetParams{
		ID: id, Name: c.Name, ListPrice: c.ListPrice, PurchasePrice: c.PurchasePrice, PurchaseDate: c.PurchaseDate,
		FirstRegistration: c.FirstRegistration, PurchaseOdometerKm: c.PurchaseOdometerKm,
		AnnualDepreciation: c.AnnualDepreciation, KmDepreciation: c.KmDepreciation, ExpectedKmPerYear: c.ExpectedKmPerYear,
		PurchaseCosts: c.PurchaseCosts, NotarizedDate: c.NotarizedDate, LandValue: c.LandValue, YearBuilt: c.YearBuilt,
		UsefulLifeYears: c.UsefulLifeYears, PriceDrift: c.PriceDrift, SelfUsed: c.SelfUsed,
		LivingAreaM2: c.LivingAreaM2, Details: c.Details, PriceIndex: c.PriceIndex,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "update asset: "+err.Error())
		return
	}
	h.refreshSnapshots()
	h.writeAsset(w, ctx, http.StatusOK, id)
}

// HandleDelete removes an asset, its loans and the categories they set.
func (h *AssetsHandler) HandleDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := parseIDParam(w, r, "id", "asset")
	if !ok {
		return
	}
	ctx := r.Context()
	holdings, existing, ok := h.loadAsset(w, ctx, id)
	if !ok {
		return
	}
	for _, l := range existing.Links {
		if err := h.clearCategory(ctx, l.TransactionID, assets.CategoryPurchase); err != nil {
			writeError(w, http.StatusInternalServerError, "unlink payment: "+err.Error())
			return
		}
	}
	for _, loan := range holdings.LoansFor(id) {
		if err := h.clearLoanCategories(ctx, loan); err != nil {
			writeError(w, http.StatusInternalServerError, "unlink loan payments: "+err.Error())
			return
		}
	}
	if err := h.queries.DeleteAsset(ctx, id); err != nil {
		writeError(w, http.StatusInternalServerError, "delete asset: "+err.Error())
		return
	}
	h.refreshSnapshots()
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// HandleUpsertObservation records an odometer reading and/or appraisal.
func (h *AssetsHandler) HandleUpsertObservation(w http.ResponseWriter, r *http.Request) {
	id, ok := parseIDParam(w, r, "id", "asset")
	if !ok {
		return
	}
	var req struct {
		Date           string   `json:"date"`
		OdometerKm     *int32   `json:"odometer_km"`
		AppraisedValue *float64 `json:"appraised_value"`
		Note           string   `json:"note"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	date, err := time.Parse("2006-01-02", req.Date)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid date (expected YYYY-MM-DD)")
		return
	}
	if req.OdometerKm == nil && req.AppraisedValue == nil {
		writeError(w, http.StatusBadRequest, "odometer_km or appraised_value is required")
		return
	}
	params := db.UpsertAssetObservationParams{
		AssetID: id, Date: date, Note: pgtype.Text{String: req.Note, Valid: req.Note != ""},
	}
	if req.OdometerKm != nil {
		if *req.OdometerKm < 0 {
			writeError(w, http.StatusBadRequest, "odometer_km must be >= 0")
			return
		}
		params.OdometerKm = pgtype.Int4{Int32: *req.OdometerKm, Valid: true}
	}
	if req.AppraisedValue != nil {
		if *req.AppraisedValue <= 0 {
			writeError(w, http.StatusBadRequest, "appraised_value must be > 0")
			return
		}
		params.AppraisedValue = numericOf(*req.AppraisedValue, "%.2f")
	}
	ctx := r.Context()
	asset, err := h.queries.GetAsset(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "asset not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "get asset: "+err.Error())
		return
	}
	if date.Before(asset.PurchaseDate) {
		writeError(w, http.StatusBadRequest, "observation date is before the purchase date")
		return
	}
	if _, err := h.queries.UpsertAssetObservation(ctx, params); err != nil {
		writeError(w, http.StatusInternalServerError, "upsert observation: "+err.Error())
		return
	}
	h.refreshSnapshots()
	h.writeAsset(w, ctx, http.StatusOK, id)
}

// HandleDeleteObservation removes the observation on the given date.
func (h *AssetsHandler) HandleDeleteObservation(w http.ResponseWriter, r *http.Request) {
	id, ok := parseIDParam(w, r, "id", "asset")
	if !ok {
		return
	}
	date, err := time.Parse("2006-01-02", chi.URLParam(r, "date"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid date (expected YYYY-MM-DD)")
		return
	}
	if err := h.queries.DeleteAssetObservation(r.Context(), db.DeleteAssetObservationParams{AssetID: id, Date: date}); err != nil {
		writeError(w, http.StatusInternalServerError, "delete observation: "+err.Error())
		return
	}
	h.refreshSnapshots()
	h.writeAsset(w, r.Context(), http.StatusOK, id)
}

// HandleCreateImprovement records capital spent on an asset after purchase.
func (h *AssetsHandler) HandleCreateImprovement(w http.ResponseWriter, r *http.Request) {
	id, ok := parseIDParam(w, r, "id", "asset")
	if !ok {
		return
	}
	var req struct {
		Date          string  `json:"date"`
		Amount        float64 `json:"amount"`
		Description   string  `json:"description"`
		TransactionID string  `json:"transaction_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	date, err := time.Parse("2006-01-02", req.Date)
	if err != nil || req.Amount <= 0 {
		writeError(w, http.StatusBadRequest, "date (YYYY-MM-DD) and a positive amount are required")
		return
	}
	ctx := r.Context()
	holdings, asset, ok := h.loadAsset(w, ctx, id)
	if !ok {
		return
	}
	if !date.After(asset.PurchaseDate) {
		writeError(w, http.StatusBadRequest, "an improvement must be after the purchase date")
		return
	}
	if req.TransactionID != "" {
		txnID, err := uuid.Parse(req.TransactionID)
		if err != nil || holdings.LinkedIDs()[txnID] {
			writeError(w, http.StatusBadRequest, errBadPurchaseTxn.Error())
			return
		}
		if err := h.queries.InsertAssetTransaction(ctx, db.InsertAssetTransactionParams{TransactionID: txnID, AssetID: id, Purpose: "improvement"}); err != nil {
			writeError(w, http.StatusBadRequest, errBadPurchaseTxn.Error())
			return
		}
		if err := h.queries.UpdateTransactionCategory(ctx, db.UpdateTransactionCategoryParams{
			ID: txnID, Category: pgtype.Text{String: assets.CategoryPurchase, Valid: true},
		}); err != nil {
			writeError(w, http.StatusInternalServerError, "tag payment: "+err.Error())
			return
		}
	}
	if _, err := h.queries.CreateAssetImprovement(ctx, db.CreateAssetImprovementParams{
		AssetID: id, Date: date, Amount: numericOf(req.Amount, "%.2f"), Description: req.Description,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "create improvement: "+err.Error())
		return
	}
	h.refreshSnapshots()
	h.writeAsset(w, ctx, http.StatusCreated, id)
}

// HandleDeleteImprovement removes an improvement.
func (h *AssetsHandler) HandleDeleteImprovement(w http.ResponseWriter, r *http.Request) {
	id, ok := parseIDParam(w, r, "id", "asset")
	if !ok {
		return
	}
	imprID, ok := parseIDParam(w, r, "improvementID", "improvement")
	if !ok {
		return
	}
	if err := h.queries.DeleteAssetImprovement(r.Context(), db.DeleteAssetImprovementParams{ID: imprID, AssetID: id}); err != nil {
		writeError(w, http.StatusInternalServerError, "delete improvement: "+err.Error())
		return
	}
	h.refreshSnapshots()
	h.writeAsset(w, r.Context(), http.StatusOK, id)
}

// isAssetPurchase reports whether a transaction paid for a tracked asset.
func isAssetPurchase(category pgtype.Text) bool {
	return category.Valid && category.String == assets.CategoryPurchase
}

// isLoanPayment reports whether a transaction is a matched loan instalment.
func isLoanPayment(category pgtype.Text) bool {
	return category.Valid && category.String == assets.CategoryLoanPayment
}

// investableNetWorth is a snapshot's net worth without physical assets and
// the loans on them, for return metrics and projections where a depreciating
// car or a mortgage would distort the result.
func investableNetWorth(s db.NetWorthSnapshot) float64 {
	return numericToFloat(s.Total) - numericToFloat(s.PhysicalComponent) + numericToFloat(s.DebtComponent)
}

// loanInterestPart returns the interest share of a matched instalment, i.e.
// the part that is spent; false for linked extra repayments (all principal).
func loanInterestPart(h assets.Holdings, txnID uuid.UUID) (float64, bool) {
	interest, _, _, ok := h.PaymentSplit(txnID)
	return interest, ok
}
