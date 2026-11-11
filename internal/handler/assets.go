package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
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

// AssetsHandler manages physical assets (cars) and their observations.
// Every mutation re-runs the net-worth backfill because asset values feed
// the physical_component of historical snapshots.
type AssetsHandler struct {
	queries  *db.Queries
	snapshot SnapshotTrigger
}

func NewAssetsHandler(q *db.Queries, snapshot SnapshotTrigger) *AssetsHandler {
	return &AssetsHandler{queries: q, snapshot: snapshot}
}

// projectionYears is how far the value curve extends past purchase.
const projectionYears = 10

type assetCurvePoint struct {
	Date      string  `json:"date"`
	Value     float64 `json:"value"`
	Projected bool    `json:"projected"`
}

type assetObservationView struct {
	Date           string   `json:"date"`
	OdometerKm     *int32   `json:"odometer_km"`
	AppraisedValue *float64 `json:"appraised_value"`
	Note           string   `json:"note"`
}

type assetView struct {
	ID                    string                 `json:"id"`
	Kind                  string                 `json:"kind"`
	Name                  string                 `json:"name"`
	ListPrice             *float64               `json:"list_price"`
	PurchasePrice         float64                `json:"purchase_price"`
	PurchaseDate          string                 `json:"purchase_date"`
	FirstRegistration     *string                `json:"first_registration"`
	PurchaseOdometerKm    int32                  `json:"purchase_odometer_km"`
	AnnualDepreciation    float64                `json:"annual_depreciation"`
	KmDepreciation        float64                `json:"km_depreciation"`
	ExpectedKmPerYear     int32                  `json:"expected_km_per_year"`
	PurchaseTransactionID *string                `json:"purchase_transaction_id"`
	CurrentValue          float64                `json:"current_value"`
	ValueChange           float64                `json:"value_change"`
	ValueChangePct        float64                `json:"value_change_pct"`
	PctOfList             *float64               `json:"pct_of_list"`
	OdometerEstimateKm    float64                `json:"odometer_estimate_km"`
	EffectiveAnnualRate   float64                `json:"effective_annual_rate"`
	MonthlyDepreciation   float64                `json:"monthly_depreciation"`
	Observations          []assetObservationView `json:"observations"`
	Curve                 []assetCurvePoint      `json:"curve"`
}

func round2(v float64) float64 { return math.Round(v*100) / 100 }

func buildAssetView(a assets.Asset, now time.Time) assetView {
	today := assets.CivilDate(now)
	cur := a.ValueOn(today)
	purchase := a.Model.PurchasePrice
	v := assetView{
		ID:                  a.ID.String(),
		Kind:                a.Kind,
		Name:                a.Name,
		PurchasePrice:       purchase,
		PurchaseDate:        a.PurchaseDate.Format("2006-01-02"),
		PurchaseOdometerKm:  a.PurchaseOdometerKm,
		AnnualDepreciation:  a.Model.AnnualRate,
		KmDepreciation:      a.Model.KmRate,
		ExpectedKmPerYear:   a.ExpectedKmPerYear,
		CurrentValue:        round2(cur),
		ValueChange:         round2(cur - purchase),
		OdometerEstimateKm:  math.Round(a.Model.OdometerAt(today)),
		EffectiveAnnualRate: a.Model.EffectiveAnnualRate(),
		MonthlyDepreciation: round2(cur - a.ValueOn(today.AddDate(0, 1, 0))),
		Observations:        make([]assetObservationView, 0, len(a.Observations)),
	}
	if today.Before(a.PurchaseDate) {
		// Not owned yet: report the purchase price, not the pre-purchase zero.
		v.CurrentValue, v.ValueChange, v.MonthlyDepreciation = purchase, 0, 0
	}
	if purchase > 0 {
		v.ValueChangePct = round2(v.ValueChange / purchase * 100)
	}
	if a.ListPrice.Valid {
		lp := numericToFloat(a.ListPrice)
		v.ListPrice = &lp
		if lp > 0 {
			pct := round2(v.CurrentValue / lp * 100)
			v.PctOfList = &pct
		}
	}
	if a.FirstRegistration.Valid {
		s := a.FirstRegistration.Time.Format("2006-01-02")
		v.FirstRegistration = &s
	}
	if a.PurchaseTransactionID != nil {
		s := a.PurchaseTransactionID.String()
		v.PurchaseTransactionID = &s
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

	// Monthly curve from purchase to the projection horizon; points after
	// today are flagged so the UI can draw them as a forecast. Today itself
	// is inserted so the history/forecast seam lands on the current value.
	end := a.PurchaseDate.AddDate(projectionYears, 0, 0)
	addedToday := false
	for d := a.PurchaseDate; !d.After(end); d = d.AddDate(0, 1, 0) {
		if !addedToday && d.After(today) && !today.Before(a.PurchaseDate) {
			v.Curve = append(v.Curve, assetCurvePoint{today.Format("2006-01-02"), round2(cur), false})
			addedToday = true
		}
		v.Curve = append(v.Curve, assetCurvePoint{d.Format("2006-01-02"), round2(a.ValueOn(d)), d.After(today)})
	}
	return v
}

// HandleList returns all assets with current value, metrics and value curve.
func (h *AssetsHandler) HandleList(w http.ResponseWriter, r *http.Request) {
	all, err := assets.Load(r.Context(), h.queries)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "load assets: "+err.Error())
		return
	}
	now := time.Now()
	views := make([]assetView, 0, len(all))
	total := 0.0
	for _, a := range all {
		v := buildAssetView(a, now)
		total += v.CurrentValue
		views = append(views, v)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"assets":      views,
		"total_value": round2(total),
		"defaults": map[string]any{
			"annual_depreciation":  analytics.DefaultCarAnnualDepreciation,
			"km_depreciation":      analytics.DefaultCarKmDepreciation,
			"expected_km_per_year": analytics.DefaultCarKmPerYear,
		},
	})
}

type assetRequest struct {
	Kind                  string   `json:"kind"`
	Name                  string   `json:"name"`
	ListPrice             *float64 `json:"list_price"`
	PurchasePrice         float64  `json:"purchase_price"`
	PurchaseDate          string   `json:"purchase_date"`
	FirstRegistration     string   `json:"first_registration"`
	PurchaseOdometerKm    int32    `json:"purchase_odometer_km"`
	AnnualDepreciation    *float64 `json:"annual_depreciation"`
	KmDepreciation        *float64 `json:"km_depreciation"`
	ExpectedKmPerYear     *int32   `json:"expected_km_per_year"`
	PurchaseTransactionID string   `json:"purchase_transaction_id"`
}

// assetParams is the validated, DB-ready form of an assetRequest.
type assetParams struct {
	kind, name                              string
	listPrice, purchasePrice, annual, perKm pgtype.Numeric
	purchaseDate                            time.Time
	firstRegistration                       pgtype.Date
	odometerKm, kmPerYear                   int32
	purchaseTxnID                           *uuid.UUID
}

func numericOf(v float64, format string) pgtype.Numeric {
	var n pgtype.Numeric
	n.Scan(fmt.Sprintf(format, v))
	return n
}

func (req assetRequest) validate() (assetParams, error) {
	p := assetParams{kind: req.Kind, name: req.Name, odometerKm: req.PurchaseOdometerKm}
	if p.kind == "" {
		p.kind = "car"
	}
	if p.kind != "car" {
		return p, errors.New("kind must be 'car'")
	}
	if p.name == "" || req.PurchasePrice <= 0 || req.PurchaseDate == "" {
		return p, errors.New("name, purchase_price and purchase_date are required")
	}
	var err error
	if p.purchaseDate, err = time.Parse("2006-01-02", req.PurchaseDate); err != nil {
		return p, errors.New("invalid purchase_date (expected YYYY-MM-DD)")
	}
	if req.FirstRegistration != "" {
		fr, err := time.Parse("2006-01-02", req.FirstRegistration)
		if err != nil {
			return p, errors.New("invalid first_registration (expected YYYY-MM-DD)")
		}
		p.firstRegistration = pgtype.Date{Time: fr, Valid: true}
	}
	if p.odometerKm < 0 {
		return p, errors.New("purchase_odometer_km must be >= 0")
	}
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
	p.kmPerYear = analytics.DefaultCarKmPerYear
	if req.ExpectedKmPerYear != nil {
		if *req.ExpectedKmPerYear < 0 {
			return p, errors.New("expected_km_per_year must be >= 0")
		}
		p.kmPerYear = *req.ExpectedKmPerYear
	}
	if req.ListPrice != nil && *req.ListPrice > 0 {
		p.listPrice = numericOf(*req.ListPrice, "%.2f")
	}
	p.purchasePrice = numericOf(req.PurchasePrice, "%.2f")
	p.annual = numericOf(annual, "%.4f")
	p.perKm = numericOf(perKm, "%.4f")
	if req.PurchaseTransactionID != "" {
		id, err := uuid.Parse(req.PurchaseTransactionID)
		if err != nil {
			return p, errors.New("invalid purchase_transaction_id")
		}
		p.purchaseTxnID = &id
	}
	return p, nil
}

// errBadPurchaseTxn marks a client error in linkPurchase.
var errBadPurchaseTxn = errors.New("purchase transaction must be an existing withdrawal")

// linkPurchase moves the asset_purchase category from the previously linked
// payment (if any) to the newly linked one.
func (h *AssetsHandler) linkPurchase(ctx context.Context, prev, next *uuid.UUID) error {
	if next != nil {
		txn, err := h.queries.GetTransaction(ctx, *next)
		if errors.Is(err, pgx.ErrNoRows) {
			return errBadPurchaseTxn
		}
		if err != nil {
			return err
		}
		if txn.Type != "withdrawal" && txn.Type != "transfer" {
			return errBadPurchaseTxn
		}
	}
	if prev != nil && (next == nil || *prev != *next) {
		txn, err := h.queries.GetTransaction(ctx, *prev)
		if err == nil && txn.Category.String == assets.CategoryPurchase {
			if err := h.queries.UpdateTransactionCategory(ctx, db.UpdateTransactionCategoryParams{ID: *prev}); err != nil {
				return err
			}
		}
	}
	if next != nil {
		return h.queries.UpdateTransactionCategory(ctx, db.UpdateTransactionCategoryParams{
			ID: *next, Category: pgtype.Text{String: assets.CategoryPurchase, Valid: true},
		})
	}
	return nil
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
	row, err := h.queries.GetAsset(ctx, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "get asset: "+err.Error())
		return
	}
	all, err := assets.Load(ctx, h.queries)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "load assets: "+err.Error())
		return
	}
	for _, a := range all {
		if a.ID == row.ID {
			writeJSON(w, status, buildAssetView(a, time.Now()))
			return
		}
	}
	writeJSON(w, status, buildAssetView(assets.FromRows(row, nil), time.Now()))
}

func parseAssetID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid asset id")
		return uuid.Nil, false
	}
	return id, true
}

// HandleCreate creates an asset and tags its linked purchase payment.
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
	ctx := r.Context()
	if err := h.linkPurchase(ctx, nil, p.purchaseTxnID); err != nil {
		h.writeLinkError(w, err)
		return
	}
	row, err := h.queries.CreateAsset(ctx, db.CreateAssetParams{
		Kind: p.kind, Name: p.name, ListPrice: p.listPrice, PurchasePrice: p.purchasePrice,
		PurchaseDate: p.purchaseDate, FirstRegistration: p.firstRegistration,
		PurchaseOdometerKm: p.odometerKm, AnnualDepreciation: p.annual, KmDepreciation: p.perKm,
		ExpectedKmPerYear: p.kmPerYear, PurchaseTransactionID: p.purchaseTxnID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "create asset: "+err.Error())
		return
	}
	h.refreshSnapshots()
	h.writeAsset(w, ctx, http.StatusCreated, row.ID)
}

func (h *AssetsHandler) writeLinkError(w http.ResponseWriter, err error) {
	if errors.Is(err, errBadPurchaseTxn) {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeError(w, http.StatusInternalServerError, "link purchase: "+err.Error())
}

// HandleUpdate replaces an asset's fields.
func (h *AssetsHandler) HandleUpdate(w http.ResponseWriter, r *http.Request) {
	id, ok := parseAssetID(w, r)
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
	existing, err := h.queries.GetAsset(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "asset not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "get asset: "+err.Error())
		return
	}
	if err := h.linkPurchase(ctx, existing.PurchaseTransactionID, p.purchaseTxnID); err != nil {
		h.writeLinkError(w, err)
		return
	}
	if _, err := h.queries.UpdateAsset(ctx, db.UpdateAssetParams{
		ID: id, Name: p.name, ListPrice: p.listPrice, PurchasePrice: p.purchasePrice,
		PurchaseDate: p.purchaseDate, FirstRegistration: p.firstRegistration,
		PurchaseOdometerKm: p.odometerKm, AnnualDepreciation: p.annual, KmDepreciation: p.perKm,
		ExpectedKmPerYear: p.kmPerYear, PurchaseTransactionID: p.purchaseTxnID,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "update asset: "+err.Error())
		return
	}
	h.refreshSnapshots()
	h.writeAsset(w, ctx, http.StatusOK, id)
}

// HandleDelete removes an asset and untags its purchase payment.
func (h *AssetsHandler) HandleDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := parseAssetID(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	existing, err := h.queries.GetAsset(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "asset not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "get asset: "+err.Error())
		return
	}
	if err := h.linkPurchase(ctx, existing.PurchaseTransactionID, nil); err != nil {
		h.writeLinkError(w, err)
		return
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
	id, ok := parseAssetID(w, r)
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
	id, ok := parseAssetID(w, r)
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

// isAssetPurchase reports whether a transaction paid for a tracked asset.
func isAssetPurchase(category pgtype.Text) bool {
	return category.Valid && category.String == assets.CategoryPurchase
}

// investableNetWorth is a snapshot's net worth without physical assets, for
// return metrics and projections where a depreciating car would distort the
// result.
func investableNetWorth(s db.NetWorthSnapshot) float64 {
	return numericToFloat(s.Total) - numericToFloat(s.PhysicalComponent)
}

// assetAttribution carries what net-worth attribution needs to separate
// physical assets from market returns.
type assetAttribution struct {
	assets []assets.Asset
	linked map[uuid.UUID]bool // purchase payments: asset swaps, not withdrawals
}

func (h *PortfolioHandler) loadAssetAttribution(ctx context.Context) assetAttribution {
	all, err := assets.Load(ctx, h.queries)
	if err != nil {
		log.Printf("WARNING: load assets for attribution: %v", err)
	}
	return assetAttribution{assets: all, linked: assets.PurchaseTransactionIDs(all)}
}

// purchasesIn sums purchase prices of assets bought in (from, to]: all of
// them, and those without a linked payment (bought with untracked money).
func (a assetAttribution) purchasesIn(from, to time.Time) (all, unlinked float64) {
	for _, as := range a.assets {
		if !as.PurchaseDate.After(from) || as.PurchaseDate.After(to) {
			continue
		}
		all += as.Model.PurchasePrice
		if as.PurchaseTransactionID == nil {
			unlinked += as.Model.PurchasePrice
		}
	}
	return all, unlinked
}
