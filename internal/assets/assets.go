// Package assets loads physical assets (cars, property, land, other) and the
// loans that finance them, and values them with the models in
// internal/analytics. It is shared by the net-worth scheduler and the HTTP
// handlers so both compute identical values.
package assets

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/lnsp/wealth/internal/analytics"
	db "github.com/lnsp/wealth/internal/database/generated"
)

// CategoryPurchase tags cash transactions that paid for an asset (purchase or
// improvement). Cashflow treats them as a transfer, net-worth attribution as
// an asset swap because the cash reappears as the asset's value.
const CategoryPurchase = "asset_purchase"

// CategoryLoanPayment tags matched loan payments. Cashflow splits them into
// interest (a cost) and principal (saving).
const CategoryLoanPayment = "loan_payment"

// Kinds of physical assets.
const (
	KindCar       = "car"
	KindApartment = "apartment"
	KindHouse     = "house"
	KindLand      = "land"
	KindOther     = "other"
)

// IndexSeries is a published price index a property can follow.
type IndexSeries struct {
	ID       string `json:"id"`
	Label    string `json:"label"`
	Geo      string `json:"-"` // Eurostat geo code
	Purchase string `json:"-"` // Eurostat purchase type
}

// IndexEurostatDE is Destatis' Häuserpreisindex for existing dwellings as
// republished by Eurostat (prc_hpi_q), quarterly, no registration needed.
const IndexEurostatDE = "eurostat-hpi-de-existing"

// IndexSeriesList lists the indices the scheduler keeps up to date.
var IndexSeriesList = []IndexSeries{
	{ID: IndexEurostatDE, Label: "House price index, Germany, existing dwellings (Destatis via Eurostat)", Geo: "DE", Purchase: "DW_EXST"},
}

// KnownIndex reports whether id is a supported index series.
func KnownIndex(id string) bool {
	for _, s := range IndexSeriesList {
		if s.ID == id {
			return true
		}
	}
	return false
}

// IsProperty reports whether a kind has a building that ages.
func IsProperty(kind string) bool { return kind == KindApartment || kind == KindHouse }

// Valuer is a valuation model.
type Valuer interface {
	ValueAt(t time.Time) float64
}

// Asset is a stored asset with its observations, improvements, linked
// payments and valuation model.
type Asset struct {
	db.Asset
	Observations []db.AssetObservation
	Improvements []db.AssetImprovement
	Links        []db.AssetTransaction
	Model        Valuer
	Car          *analytics.DepreciatingAsset // set for cars
	Property     *analytics.PropertyValuation // set for everything else
}

// Numeric converts a nullable numeric to float64 (0 when NULL).
func Numeric(n pgtype.Numeric) float64 {
	if !n.Valid {
		return 0
	}
	f, _ := n.Float64Value()
	return f.Float64
}

func observationModels(obs []db.AssetObservation) []analytics.AssetObservation {
	var out []analytics.AssetObservation
	for _, o := range obs {
		mo := analytics.AssetObservation{Date: o.Date}
		if o.OdometerKm.Valid {
			km := float64(o.OdometerKm.Int32)
			mo.OdometerKm = &km
		}
		if o.AppraisedValue.Valid {
			v := Numeric(o.AppraisedValue)
			mo.AppraisedValue = &v
		}
		out = append(out, mo)
	}
	return out
}

// FromRows builds the valuation model for one asset. The other rows must
// belong to it.
func FromRows(row db.Asset, obs []db.AssetObservation, improvements []db.AssetImprovement, links []db.AssetTransaction) Asset {
	return FromRowsWithIndex(row, obs, improvements, links, nil)
}

// FromRowsWithIndex is FromRows for a property that follows a price index
// (used when row.PriceIndex is set).
func FromRowsWithIndex(row db.Asset, obs []db.AssetObservation, improvements []db.AssetImprovement, links []db.AssetTransaction, index *analytics.PriceIndex) Asset {
	a := Asset{Asset: row, Observations: obs, Improvements: improvements, Links: links}
	if row.Kind == KindCar || row.Kind == "" {
		car := analytics.DepreciatingAsset{
			PurchaseDate:       row.PurchaseDate,
			PurchasePrice:      Numeric(row.PurchasePrice),
			PurchaseOdometerKm: float64(row.PurchaseOdometerKm),
			AnnualRate:         Numeric(row.AnnualDepreciation),
			KmRate:             Numeric(row.KmDepreciation),
			ExpectedKmPerYear:  float64(row.ExpectedKmPerYear),
			Observations:       observationModels(obs),
		}
		a.Car, a.Model = &car, car
		return a
	}
	prop := analytics.PropertyValuation{
		PurchaseDate:  row.PurchaseDate,
		PurchasePrice: Numeric(row.PurchasePrice),
		Drift:         Numeric(row.PriceDrift),
		Appraisals:    observationModels(obs),
	}
	if IsProperty(row.Kind) && row.YearBuilt.Valid {
		prop.BuildingEnd = float64(row.YearBuilt.Int32 + row.UsefulLifeYears)
		prop.LandValue = Numeric(row.LandValue)
	}
	for _, im := range improvements {
		prop.Improvements = append(prop.Improvements, analytics.Improvement{Date: im.Date, Amount: Numeric(im.Amount)})
	}
	if row.PriceIndex != "" && index != nil && len(index.Points) > 0 {
		prop.Index = index
	}
	a.Property, a.Model = &prop, prop
	return a
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

// PurchasePrice is the market value paid (excluding purchase costs).
func (a Asset) PurchasePrice() float64 { return Numeric(a.Asset.PurchasePrice) }

// PurchaseCosts are transfer tax, notary, broker etc.
func (a Asset) PurchaseCosts() float64 { return Numeric(a.Asset.PurchaseCosts) }

// ImprovementsTotal sums capital spent after purchase.
func (a Asset) ImprovementsTotal() float64 {
	total := 0.0
	for _, im := range a.Improvements {
		total += Numeric(im.Amount)
	}
	return total
}

// CostBasis is everything paid for the asset: price, purchase costs and
// improvements.
func (a Asset) CostBasis() float64 {
	return a.PurchasePrice() + a.PurchaseCosts() + a.ImprovementsTotal()
}

// Loan is a stored loan with its extra repayments, matched payments and model.
type Loan struct {
	db.Loan
	Extras   []db.LoanExtraRepayment
	Links    []db.LoanPaymentLink
	Model    analytics.Loan
	Schedule []analytics.LoanPayment
}

// LoanFromRows builds the loan model. The other rows must belong to it.
func LoanFromRows(row db.Loan, extras []db.LoanExtraRepayment, links []db.LoanPaymentLink) Loan {
	m := analytics.Loan{
		Principal:        Numeric(row.Principal),
		PayoutDate:       row.PayoutDate,
		FirstPaymentDate: row.FirstPaymentDate,
		Rate:             Numeric(row.NominalRate),
		MonthlyPayment:   Numeric(row.MonthlyPayment),
		FollowupRate:     -1,
	}
	if row.FixedUntil.Valid {
		m.FixedUntil = row.FixedUntil.Time
	}
	if row.FollowupRate.Valid {
		m.FollowupRate = Numeric(row.FollowupRate)
	}
	for _, e := range extras {
		m.Extras = append(m.Extras, analytics.ExtraRepayment{Date: e.Date, Amount: Numeric(e.Amount)})
	}
	return Loan{Loan: row, Extras: extras, Links: links, Model: m, Schedule: m.Schedule()}
}

// BalanceOn returns what is owed at the end of t's calendar day.
func (l Loan) BalanceOn(t time.Time) float64 {
	return l.Model.BalanceAt(CivilDate(t))
}

// Holdings is everything physical: assets and the loans on them.
type Holdings struct {
	Assets  []Asset
	Loans   []Loan
	Indexes map[string]*analytics.PriceIndex
}

// LoadIndexes returns the stored price index series.
func LoadIndexes(ctx context.Context, q *db.Queries) (map[string]*analytics.PriceIndex, error) {
	pts, err := q.ListPriceIndexPoints(ctx)
	if err != nil {
		return nil, err
	}
	periods := map[string][]time.Time{}
	values := map[string][]float64{}
	for _, p := range pts {
		periods[p.Series] = append(periods[p.Series], p.Period)
		values[p.Series] = append(values[p.Series], Numeric(p.Value))
	}
	out := make(map[string]*analytics.PriceIndex, len(periods))
	for series := range periods {
		out[series] = analytics.NewQuarterlyIndex(periods[series], values[series])
	}
	return out, nil
}

// Load returns all assets and loans with their related rows.
func Load(ctx context.Context, q *db.Queries) (Holdings, error) {
	rows, err := q.ListAssets(ctx)
	if err != nil {
		return Holdings{}, err
	}
	allObs, err := q.ListAssetObservations(ctx)
	if err != nil {
		return Holdings{}, err
	}
	allImpr, err := q.ListAssetImprovements(ctx)
	if err != nil {
		return Holdings{}, err
	}
	allLinks, err := q.ListAssetTransactions(ctx)
	if err != nil {
		return Holdings{}, err
	}
	obsBy := make(map[uuid.UUID][]db.AssetObservation)
	for _, o := range allObs {
		obsBy[o.AssetID] = append(obsBy[o.AssetID], o)
	}
	imprBy := make(map[uuid.UUID][]db.AssetImprovement)
	for _, im := range allImpr {
		imprBy[im.AssetID] = append(imprBy[im.AssetID], im)
	}
	linkBy := make(map[uuid.UUID][]db.AssetTransaction)
	for _, l := range allLinks {
		linkBy[l.AssetID] = append(linkBy[l.AssetID], l)
	}
	indexes, err := LoadIndexes(ctx, q)
	if err != nil {
		return Holdings{}, err
	}
	h := Holdings{Indexes: indexes}
	for _, r := range rows {
		h.Assets = append(h.Assets, FromRowsWithIndex(r, obsBy[r.ID], imprBy[r.ID], linkBy[r.ID], indexes[r.PriceIndex]))
	}

	loanRows, err := q.ListLoans(ctx)
	if err != nil {
		return Holdings{}, err
	}
	allExtras, err := q.ListLoanExtraRepayments(ctx)
	if err != nil {
		return Holdings{}, err
	}
	allPayLinks, err := q.ListLoanPaymentLinks(ctx)
	if err != nil {
		return Holdings{}, err
	}
	extrasBy := make(map[uuid.UUID][]db.LoanExtraRepayment)
	for _, e := range allExtras {
		extrasBy[e.LoanID] = append(extrasBy[e.LoanID], e)
	}
	payBy := make(map[uuid.UUID][]db.LoanPaymentLink)
	for _, p := range allPayLinks {
		payBy[p.LoanID] = append(payBy[p.LoanID], p)
	}
	for _, r := range loanRows {
		h.Loans = append(h.Loans, LoanFromRows(r, extrasBy[r.ID], payBy[r.ID]))
	}
	return h, nil
}

// PhysicalAt sums the modelled value of all assets on t's calendar day.
func (h Holdings) PhysicalAt(t time.Time) float64 {
	total := 0.0
	for _, a := range h.Assets {
		total += a.ValueOn(t)
	}
	return total
}

// DebtAt sums what is owed on all loans at the end of t's calendar day.
func (h Holdings) DebtAt(t time.Time) float64 {
	total := 0.0
	for _, l := range h.Loans {
		total += l.BalanceOn(t)
	}
	return total
}

// LoansFor returns the loans financing an asset.
func (h Holdings) LoansFor(assetID uuid.UUID) []Loan {
	var out []Loan
	for _, l := range h.Loans {
		if l.AssetID == assetID {
			out = append(out, l)
		}
	}
	return out
}

// AssetLinkedIDs returns transactions that paid for assets (asset swaps).
func (h Holdings) AssetLinkedIDs() map[uuid.UUID]bool {
	ids := make(map[uuid.UUID]bool)
	for _, a := range h.Assets {
		for _, l := range a.Links {
			ids[l.TransactionID] = true
		}
	}
	return ids
}

// LoanLinkedIDs returns transactions that paid loan instalments or extra
// repayments.
func (h Holdings) LoanLinkedIDs() map[uuid.UUID]bool {
	ids := make(map[uuid.UUID]bool)
	for _, l := range h.Loans {
		for _, p := range l.Links {
			ids[p.TransactionID] = true
		}
		for _, e := range l.Extras {
			if e.TransactionID != nil {
				ids[*e.TransactionID] = true
			}
		}
	}
	return ids
}

// LinkedIDs is the union of asset and loan payment links.
func (h Holdings) LinkedIDs() map[uuid.UUID]bool {
	ids := h.AssetLinkedIDs()
	for id := range h.LoanLinkedIDs() {
		ids[id] = true
	}
	return ids
}

// PaymentSplit returns the scheduled interest and principal for a matched
// loan payment transaction.
func (h Holdings) PaymentSplit(txnID uuid.UUID) (interest, principal float64, kind string, ok bool) {
	for _, l := range h.Loans {
		for _, link := range l.Links {
			if link.TransactionID != txnID {
				continue
			}
			for _, p := range l.Schedule {
				if p.Due.Equal(link.DueDate) {
					for _, a := range h.Assets {
						if a.ID == l.AssetID {
							kind = a.Kind
						}
					}
					return p.Interest, p.Principal, kind, true
				}
			}
		}
	}
	return 0, 0, "", false
}
