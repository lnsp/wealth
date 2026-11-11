// Package assets loads physical assets (cars) from the database and values
// them with the depreciation model in internal/analytics. It is shared by the
// net-worth scheduler and the HTTP handlers so both compute identical values.
package assets

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/lnsp/wealth/internal/analytics"
	db "github.com/lnsp/wealth/internal/database/generated"
)

// CategoryPurchase tags the cash transaction that paid for an asset. Cashflow
// treats it as a transfer (not spending); net-worth attribution treats it as
// an asset swap because the cash reappears as the asset's value.
const CategoryPurchase = "asset_purchase"

// Asset is a stored asset with its observations and valuation model.
type Asset struct {
	db.Asset
	Observations []db.AssetObservation
	Model        analytics.DepreciatingAsset
}

func numeric(n pgtype.Numeric) float64 {
	if !n.Valid {
		return 0
	}
	f, _ := n.Float64Value()
	return f.Float64
}

// FromRows builds the valuation model for one asset. obs must belong to row.
func FromRows(row db.Asset, obs []db.AssetObservation) Asset {
	model := analytics.DepreciatingAsset{
		PurchaseDate:       row.PurchaseDate,
		PurchasePrice:      numeric(row.PurchasePrice),
		PurchaseOdometerKm: float64(row.PurchaseOdometerKm),
		AnnualRate:         numeric(row.AnnualDepreciation),
		KmRate:             numeric(row.KmDepreciation),
		ExpectedKmPerYear:  float64(row.ExpectedKmPerYear),
	}
	for _, o := range obs {
		mo := analytics.AssetObservation{Date: o.Date}
		if o.OdometerKm.Valid {
			km := float64(o.OdometerKm.Int32)
			mo.OdometerKm = &km
		}
		if o.AppraisedValue.Valid {
			v := numeric(o.AppraisedValue)
			mo.AppraisedValue = &v
		}
		model.Observations = append(model.Observations, mo)
	}
	return Asset{Asset: row, Observations: obs, Model: model}
}

// Load returns all assets with their observations.
func Load(ctx context.Context, q *db.Queries) ([]Asset, error) {
	rows, err := q.ListAssets(ctx)
	if err != nil {
		return nil, err
	}
	allObs, err := q.ListAssetObservations(ctx)
	if err != nil {
		return nil, err
	}
	byAsset := make(map[uuid.UUID][]db.AssetObservation)
	for _, o := range allObs {
		byAsset[o.AssetID] = append(byAsset[o.AssetID], o)
	}
	out := make([]Asset, 0, len(rows))
	for _, r := range rows {
		out = append(out, FromRows(r, byAsset[r.ID]))
	}
	return out, nil
}

// CivilDate maps t to midnight UTC of its calendar day, matching how DATE
// columns are scanned. Without this, a local-midnight snapshot date in
// Europe/Berlin falls before a UTC-midnight purchase date on the same day.
func CivilDate(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// ValueOn returns the asset's modelled value on t's calendar day.
func (a Asset) ValueOn(t time.Time) float64 {
	return a.Model.ValueAt(CivilDate(t))
}

// TotalValueAt sums the modelled value of all assets on t's calendar day.
func TotalValueAt(as []Asset, t time.Time) float64 {
	total := 0.0
	for _, a := range as {
		total += a.ValueOn(t)
	}
	return total
}

// PurchaseTransactionIDs returns the IDs of linked purchase payments.
func PurchaseTransactionIDs(as []Asset) map[uuid.UUID]bool {
	ids := make(map[uuid.UUID]bool)
	for _, a := range as {
		if a.PurchaseTransactionID != nil {
			ids[*a.PurchaseTransactionID] = true
		}
	}
	return ids
}
