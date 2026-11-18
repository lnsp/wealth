package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/lnsp/wealth/data"
	"github.com/lnsp/wealth/internal/analytics"
	"github.com/lnsp/wealth/internal/assets"
	db "github.com/lnsp/wealth/internal/database/generated"
	"github.com/lnsp/wealth/internal/market"
)

// holdingKey returns a matching key for an ETF constituent holding.
func holdingKey(isin, name string) string {
	if isin != "" && !strings.HasPrefix(isin, "XX_") {
		return isin
	}
	return "name:" + strings.ToLower(strings.TrimSpace(name))
}

type securityMap map[string]db.Security
type priceMapAnalysis map[string]float64

// enrichedHolding bundles a holding with its pre-loaded security and EUR market/cost values.
type enrichedHolding struct {
	holding   db.ListCurrentHoldingsRow
	security  db.Security
	value     float64 // EUR market value (or EUR cost basis fallback)
	costValue float64 // EUR cost basis total (qty * avg_cost_basis converted to EUR)
}

type AnalysisHandler struct {
	queries              *db.Queries
	benchmarkISIN        string
	benchmarkTicker      string
	overlapWarnPct       float64
	concentrationWarnPct float64
}

func NewAnalysisHandler(q *db.Queries) *AnalysisHandler {
	return &AnalysisHandler{
		queries:              q,
		benchmarkISIN:        "IE00B4L5Y983",
		benchmarkTicker:      "IWDA.AS",
		overlapWarnPct:       70.0,
		concentrationWarnPct: 5.0,
	}
}

// SetConfig configures benchmark ISIN/ticker and overlap/concentration alert thresholds.
func (h *AnalysisHandler) SetConfig(benchmarkISIN, benchmarkTicker string, overlapWarnPct, concentrationWarnPct float64) {
	if strings.TrimSpace(benchmarkISIN) != "" {
		h.benchmarkISIN = strings.TrimSpace(benchmarkISIN)
	}
	if strings.TrimSpace(benchmarkTicker) != "" {
		h.benchmarkTicker = strings.TrimSpace(benchmarkTicker)
	}
	if overlapWarnPct > 0 {
		h.overlapWarnPct = overlapWarnPct
	}
	if concentrationWarnPct > 0 {
		h.concentrationWarnPct = concentrationWarnPct
	}
}

// buildTaxTransactions converts newest-first DB transaction rows into chronologically
// ordered analytics.TaxTransaction records with trade-date EUR amounts, fee-adjusted
// buy/sell basis (§ 20 Abs. 4 EStG), per-depot AccountID, trade Date, and InvStG TFS.
func buildTaxTransactions(
	txns []db.ListTransactionsRow,
	secs []db.Security,
	histFX []market.HistoricalFXRate,
	fxRates map[string]float64,
) ([]analytics.TaxTransaction, map[string]string, map[string]float64, map[string]bool) {
	secMap := make(map[string]db.Security, len(secs))
	names := make(map[string]string, len(secs))
	tfsMap := make(map[string]float64, len(secs))
	equityMap := make(map[string]bool, len(secs))
	for _, s := range secs {
		secMap[s.ISIN] = s
		names[s.ISIN] = s.Name
		tfs := securityTFSRate(s)
		tfsMap[s.ISIN] = tfs
		equityMap[s.ISIN] = tfs >= analytics.TeilfreistellungEquity
	}

	taxTxns := make([]analytics.TaxTransaction, 0, len(txns))
	for i := len(txns) - 1; i >= 0; i-- {
		txn := txns[i]
		isin := ""
		if txn.SecurityISIN.Valid {
			isin = txn.SecurityISIN.String
		}
		amt := 0.0
		if txn.Amount.Valid {
			f, _ := txn.Amount.Float64Value()
			amt = convertAmountToEURAtDate(f.Float64, txn.Currency, txn.Date, histFX, fxRates)
		}
		fee := 0.0
		if txn.Fee.Valid {
			f, _ := txn.Fee.Float64Value()
			fee = convertAmountToEURAtDate(f.Float64, txn.Currency, txn.Date, histFX, fxRates)
		}
		qty := 0.0
		if txn.Quantity.Valid {
			f, _ := txn.Quantity.Float64Value()
			qty = f.Float64
		}

		taxAmt := amt
		switch txn.Type {
		case "buy", "savings_plan", "transfer":
			taxAmt = amt + fee
		case "sell":
			taxAmt = math.Max(0, amt-fee)
		}

		name := names[isin]
		if name == "" && txn.Counterparty.Valid {
			name = txn.Counterparty.String
		}
		sec, hasSec := secMap[isin]
		tfs := tfsMap[isin]

		taxTxns = append(taxTxns, analytics.TaxTransaction{
			AccountID:      txn.AccountID.String(),
			Date:           txn.Date.Format("2006-01-02"),
			Year:           txn.Date.Year(),
			Type:           txn.Type,
			ISIN:           isin,
			Name:           name,
			Quantity:       qty,
			Amount:         taxAmt,
			IsEquityFund:   equityMap[isin],
			AssetClass:     sec.AssetClass, // loss pot (stock → Aktientopf) and §23 exclusion (crypto, commodity)
			TFS:            tfs,
			HasExplicitTFS: hasSec,
		})
	}
	return taxTxns, names, tfsMap, equityMap
}

// buildExternalFlowsForRisk loads portfolio-level external cashflows (+deposit/in-kind
// contribution, -withdrawal, plus implicit cash clamp deltas) in EUR so risk and
// volatility endpoints neutralize deposit/withdrawal jumps via BuildCashflowAdjustedSeries.
func (h *AnalysisHandler) buildExternalFlowsForRisk(ctx context.Context) []analytics.CashFlow {
	txns, err := listAllTransactionsRows(ctx, h.queries)
	if err != nil || len(txns) == 0 {
		return nil
	}
	fxRates := loadFXRatesMap(ctx, h.queries)
	histFX := loadHistoricalFXRates(ctx, h.queries)
	accts, _ := h.queries.ListAccounts(ctx)
	physHoldings, _ := assets.Load(ctx, h.queries)
	linkedIDs := physHoldings.LinkedIDs()

	var inkind []analytics.InKindTransfer
	for _, t := range txns {
		if t.SecurityISIN.Valid && (t.Type == "transfer" || t.Type == "transfer_out") {
			inkind = append(inkind, analytics.InKindTransfer{
				ID: t.ID, Date: t.Date, Type: t.Type,
				ISIN: t.SecurityISIN.String, Quantity: numericToFloat(t.Quantity),
			})
		}
	}
	washed := analytics.MatchInKindTransferPairs(inkind)

	txnsChrono := make([]db.ListTransactionsRow, len(txns))
	for i := range txns {
		txnsChrono[i] = txns[len(txns)-1-i]
	}
	impEvents := computeImplicitCashClampEvents(accts, txnsChrono, histFX, fxRates)

	var flows []analytics.CashFlow
	for _, ev := range impEvents {
		flows = append(flows, analytics.CashFlow{Date: ev.Date, Amount: ev.DeltaEUR})
	}
	for _, t := range txnsChrono {
		if _, ok := washed[t.ID]; ok {
			continue
		}
		if linkedIDs[t.ID] {
			continue
		}
		amt := convertAmountToEURAtDate(numericToFloat(t.Amount), t.Currency, t.Date, histFX, fxRates)
		if amt == 0 {
			continue
		}
		switch analytics.ClassifyForAttribution(t.Type, t.SecurityISIN.Valid, analytics.ScopePortfolio) {
		case analytics.BucketContribution:
			flows = append(flows, analytics.CashFlow{Date: t.Date, Amount: amt})
		case analytics.BucketWithdrawal:
			flows = append(flows, analytics.CashFlow{Date: t.Date, Amount: -amt})
		}
	}
	return flows
}

// loadEnrichedHoldings batch-loads all current holdings with their securities
// and EUR market values in 4 queries (holdings + securities + prices + FX rates).
func (h *AnalysisHandler) loadEnrichedHoldings(ctx context.Context) ([]enrichedHolding, error) {
	holdings, err := h.queries.ListCurrentHoldings(ctx)
	if err != nil {
		return nil, err
	}

	// Filter by user's accounts if auth is enabled
	if allowedIDs := userAccountIDs(ctx, h.queries.DB()); allowedIDs != nil {
		allowed := make(map[uuid.UUID]bool, len(allowedIDs))
		for _, id := range allowedIDs {
			allowed[id] = true
		}
		filtered := holdings[:0]
		for _, hld := range holdings {
			if allowed[hld.AccountID] {
				filtered = append(filtered, hld)
			}
		}
		holdings = filtered
	}

	secs, err := h.queries.ListSecurities(ctx)
	if err != nil {
		return nil, err
	}
	secMap := make(securityMap, len(secs))
	for _, s := range secs {
		secMap[s.ISIN] = s
	}

	fxRates := loadFXRatesMap(ctx, h.queries)
	prices, err := h.queries.ListLatestPrices(ctx)
	if err != nil {
		prices = nil // non-fatal: fall back to cost basis
	}
	pm := make(priceMapAnalysis, len(prices))
	for _, p := range prices {
		if p.Close.Valid {
			f, _ := p.Close.Float64Value()
			eurPrice := convertAmountToEUR(f.Float64, p.Currency, fxRates)
			if eurPrice > 0 {
				pm[p.SecurityISIN] = eurPrice
			}
		}
	}

	var result []enrichedHolding
	for _, hold := range holdings {
		sec, ok := secMap[hold.SecurityISIN]
		if !ok {
			continue
		}
		qty := 0.0
		if hold.Quantity.Valid {
			f, _ := hold.Quantity.Float64Value()
			qty = f.Float64
		}
		if qty <= 0 {
			continue
		}
		costValue := 0.0
		if hold.AvgCostBasis.Valid {
			f, _ := hold.AvgCostBasis.Float64Value()
			costValue = qty * convertAmountToEUR(f.Float64, hold.Currency, fxRates)
		}
		value := costValue
		if price, ok := pm[hold.SecurityISIN]; ok && price > 0 {
			value = qty * price
		}
		result = append(result, enrichedHolding{
			holding:   hold,
			security:  sec,
			value:     value,
			costValue: costValue,
		})
	}
	return result, nil
}

// HandleAllocationSummary combines sectors, countries, and currency in one response.
// Holdings without sector/country metadata route their value into an
// "Unknown" bucket so each allocation map sums to (essentially) the
// portfolio's total invested value — the donuts then render the
// unattributed slice instead of silently understating the rest.
func (h *AnalysisHandler) HandleAllocationSummary(w http.ResponseWriter, r *http.Request) {
	enriched, err := h.loadEnrichedHoldings(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "load holdings: "+err.Error())
		return
	}

	sectors := make(map[string]float64)
	countries := make(map[string]float64)
	var currencyHoldings []analytics.HoldingForCurrency

	for _, eh := range enriched {
		if eh.value <= 0 {
			continue
		}
		sectorWeights := analytics.ParseWeights(eh.security.SectorWeights)
		sectorSum := 0.0
		for sector, pct := range sectorWeights {
			sectors[sector] += eh.value * pct / 100
			sectorSum += pct
		}
		// Either no metadata (sum=0) or partial coverage (sum<100): route
		// the missing fraction to "Unknown" so the donut reflects the full
		// portfolio value.
		if sectorSum < 100 {
			sectors["Unknown"] += eh.value * (100 - sectorSum) / 100
		}

		countryWeights := analytics.ParseWeights(eh.security.CountryWeights)
		countrySum := 0.0
		for country, pct := range countryWeights {
			countries[country] += eh.value * pct / 100
			countrySum += pct
		}
		if countrySum < 100 {
			countries["Unknown"] += eh.value * (100 - countrySum) / 100
		}

		currencyHoldings = append(currencyHoldings, analytics.HoldingForCurrency{
			ISIN: eh.security.ISIN, Currency: eh.security.Currency,
			MarketValue: eh.value, CountryWeights: countryWeights,
		})
	}

	currencies := analytics.ComputeCurrencyExposure(currencyHoldings)
	if currencies == nil {
		currencies = []analytics.CurrencyExposureEntry{}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"sectors":    sectors,
		"countries":  countries,
		"currencies": currencies,
	})
}

func (h *AnalysisHandler) HandleSectors(w http.ResponseWriter, r *http.Request) {
	enriched, err := h.loadEnrichedHoldings(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "load holdings: "+err.Error())
		return
	}

	exposure := make(map[string]float64)
	for _, eh := range enriched {
		if eh.value <= 0 {
			continue
		}
		sectorWeights := analytics.ParseWeights(eh.security.SectorWeights)
		sum := 0.0
		for sector, pct := range sectorWeights {
			exposure[sector] += eh.value * pct / 100
			sum += pct
		}
		if sum < 100 {
			exposure["Unknown"] += eh.value * (100 - sum) / 100
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{"sectors": exposure})
}

func (h *AnalysisHandler) HandleCountries(w http.ResponseWriter, r *http.Request) {
	enriched, err := h.loadEnrichedHoldings(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "load holdings: "+err.Error())
		return
	}

	exposure := make(map[string]float64)
	for _, eh := range enriched {
		if eh.value <= 0 {
			continue
		}
		countryWeights := analytics.ParseWeights(eh.security.CountryWeights)
		sum := 0.0
		for country, pct := range countryWeights {
			exposure[country] += eh.value * pct / 100
			sum += pct
		}
		if sum < 100 {
			exposure["Unknown"] += eh.value * (100 - sum) / 100
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{"countries": exposure})
}

func (h *AnalysisHandler) HandleCurrency(w http.ResponseWriter, r *http.Request) {
	enriched, err := h.loadEnrichedHoldings(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "load holdings: "+err.Error())
		return
	}

	var currencyHoldings []analytics.HoldingForCurrency
	for _, eh := range enriched {
		countryWeights := analytics.ParseWeights(eh.security.CountryWeights)
		currencyHoldings = append(currencyHoldings, analytics.HoldingForCurrency{
			ISIN:           eh.security.ISIN,
			Currency:       eh.security.Currency,
			MarketValue:    eh.value,
			CountryWeights: countryWeights,
		})
	}

	entries := analytics.ComputeCurrencyExposure(currencyHoldings)
	if entries == nil {
		entries = []analytics.CurrencyExposureEntry{}
	}

	writeJSON(w, http.StatusOK, map[string]any{"currencies": entries})
}

func (h *AnalysisHandler) HandleETFHoldings(w http.ResponseWriter, r *http.Request) {
	isin := chi.URLParam(r, "isin")
	if isin == "" {
		writeError(w, http.StatusBadRequest, "isin is required")
		return
	}

	sec, err := h.queries.GetSecurity(r.Context(), isin)
	if err != nil {
		writeError(w, http.StatusNotFound, "security not found")
		return
	}

	if sec.AssetClass != "etf" {
		writeError(w, http.StatusBadRequest, "security is not an ETF")
		return
	}

	etfHoldings, err := h.queries.ListETFHoldings(r.Context(), isin)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list holdings: "+err.Error())
		return
	}

	type holdingEntry struct {
		ISIN    string  `json:"isin"`
		Name    string  `json:"name"`
		Weight  float64 `json:"weight"`
		Sector  string  `json:"sector,omitempty"`
		Country string  `json:"country,omitempty"`
	}

	holdings := make([]holdingEntry, 0, len(etfHoldings))
	for _, eh := range etfHoldings {
		weight := 0.0
		if eh.WeightPct.Valid {
			f, _ := eh.WeightPct.Float64Value()
			weight = f.Float64
		}
		entry := holdingEntry{
			ISIN:   eh.HoldingISIN,
			Name:   eh.HoldingName,
			Weight: weight,
		}
		if eh.Sector.Valid {
			entry.Sector = eh.Sector.String
		}
		if eh.Country.Valid {
			entry.Country = eh.Country.String
		}
		holdings = append(holdings, entry)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"etf_isin": sec.ISIN,
		"etf_name": sec.Name,
		"holdings": holdings,
	})
}

func (h *AnalysisHandler) HandleTreemap(w http.ResponseWriter, r *http.Request) {
	enriched, err := h.loadEnrichedHoldings(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "load holdings: "+err.Error())
		return
	}

	var etfs []analytics.ETFForTreemap
	for _, eh := range enriched {
		if eh.value <= 0 {
			continue
		}

		if eh.security.AssetClass != "etf" {
			etfs = append(etfs, analytics.ETFForTreemap{
				Name:        eh.security.Name,
				MarketValue: eh.value,
			})
			continue
		}

		etfHoldings, err := h.queries.ListETFHoldings(r.Context(), eh.security.ISIN)
		if err != nil || len(etfHoldings) == 0 {
			etfs = append(etfs, analytics.ETFForTreemap{
				Name:        eh.security.Name,
				MarketValue: eh.value,
			})
			continue
		}

		bySector := make(map[string][]analytics.TreemapNode)
		for _, etfH := range etfHoldings {
			weight := 0.0
			if etfH.WeightPct.Valid {
				f, _ := etfH.WeightPct.Float64Value()
				weight = f.Float64
			}
			if weight <= 0 {
				continue
			}
			hValue := eh.value * weight / 100
			sector := "Other"
			if etfH.Sector.Valid && etfH.Sector.String != "" {
				sector = etfH.Sector.String
			}
			bySector[sector] = append(bySector[sector], analytics.TreemapNode{
				Name:  etfH.HoldingName,
				Value: hValue,
			})
		}

		etfs = append(etfs, analytics.ETFForTreemap{
			Name:             eh.security.Name,
			MarketValue:      eh.value,
			HoldingsBySector: bySector,
		})
	}

	tree := analytics.BuildAllocationTreemap(etfs)
	if tree == nil {
		tree = []analytics.TreemapNode{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"children": tree})
}

func (h *AnalysisHandler) HandleTopHoldings(w http.ResponseWriter, r *http.Request) {
	enriched, err := h.loadEnrichedHoldings(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "load holdings: "+err.Error())
		return
	}

	var holdingsWithETF []analytics.HoldingWithETF
	holdingNames := make(map[string]string)

	for _, eh := range enriched {
		if eh.security.AssetClass != "etf" {
			continue
		}
		etfHoldings, err := h.queries.ListETFHoldings(r.Context(), eh.security.ISIN)
		if err != nil || len(etfHoldings) == 0 {
			continue
		}
		weights := make(map[string]float64)
		for _, etfH := range etfHoldings {
			if etfH.WeightPct.Valid {
				f, _ := etfH.WeightPct.Float64Value()
				key := holdingKey(etfH.HoldingISIN, etfH.HoldingName)
				weights[key] += f.Float64
			}
			holdingNames[holdingKey(etfH.HoldingISIN, etfH.HoldingName)] = etfH.HoldingName
		}
		holdingsWithETF = append(holdingsWithETF, analytics.HoldingWithETF{
			ISIN:        eh.security.ISIN,
			Name:        eh.security.Name,
			MarketValue: eh.value,
			ETFHoldings: weights,
		})
	}

	exposure := analytics.ComputeEffectiveExposure(holdingsWithETF)

	type topHolding struct {
		ISIN        string  `json:"isin"`
		Name        string  `json:"name"`
		ExposurePct float64 `json:"exposure_pct"`
	}

	sorted := make([]topHolding, 0, len(exposure))
	for isin, pct := range exposure {
		name := isin
		if n, ok := holdingNames[isin]; ok && n != "" {
			name = n
		}
		sorted = append(sorted, topHolding{ISIN: isin, Name: name, ExposurePct: pct * 100})
	}
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].ExposurePct > sorted[j].ExposurePct
	})
	if len(sorted) > 20 {
		sorted = sorted[:20]
	}

	writeJSON(w, http.StatusOK, map[string]any{"holdings": sorted})
}

func (h *AnalysisHandler) HandleAlerts(w http.ResponseWriter, r *http.Request) {
	enriched, err := h.loadEnrichedHoldings(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "load holdings: "+err.Error())
		return
	}

	var etfs []analytics.ETFWithHoldings
	var holdingsWithETF []analytics.HoldingWithETF
	holdingNames := make(map[string]string)

	for _, eh := range enriched {
		if eh.security.AssetClass != "etf" {
			continue
		}
		etfHoldings, err := h.queries.ListETFHoldings(r.Context(), eh.security.ISIN)
		if err != nil || len(etfHoldings) == 0 {
			continue
		}
		weights := make(map[string]float64)
		for _, etfH := range etfHoldings {
			if etfH.WeightPct.Valid {
				f, _ := etfH.WeightPct.Float64Value()
				key := holdingKey(etfH.HoldingISIN, etfH.HoldingName)
				weights[key] += f.Float64
			}
			holdingNames[holdingKey(etfH.HoldingISIN, etfH.HoldingName)] = etfH.HoldingName
		}
		etfs = append(etfs, analytics.ETFWithHoldings{
			ISIN:     eh.security.ISIN,
			Name:     eh.security.Name,
			Holdings: weights,
		})
		holdingsWithETF = append(holdingsWithETF, analytics.HoldingWithETF{
			ISIN:        eh.security.ISIN,
			Name:        eh.security.Name,
			MarketValue: eh.value,
			ETFHoldings: weights,
		})
	}

	alerts := analytics.ComputeConcentrationAlertsWithThresholds(
		etfs, holdingsWithETF, holdingNames, h.overlapWarnPct, h.concentrationWarnPct,
	)
	if alerts == nil {
		alerts = []analytics.Alert{}
	}

	writeJSON(w, http.StatusOK, map[string]any{"alerts": alerts})
}

func (h *AnalysisHandler) HandleOverlap(w http.ResponseWriter, r *http.Request) {
	enriched, err := h.loadEnrichedHoldings(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "load holdings: "+err.Error())
		return
	}

	var etfs []analytics.ETFWithHoldings
	var labels []string
	for _, eh := range enriched {
		if eh.security.AssetClass != "etf" {
			continue
		}
		etfHoldings, err := h.queries.ListETFHoldings(r.Context(), eh.security.ISIN)
		if err != nil || len(etfHoldings) == 0 {
			continue
		}
		weights := make(map[string]float64)
		for _, etfH := range etfHoldings {
			if etfH.WeightPct.Valid {
				f, _ := etfH.WeightPct.Float64Value()
				key := holdingKey(etfH.HoldingISIN, etfH.HoldingName)
				weights[key] += f.Float64
			}
		}
		etfs = append(etfs, analytics.ETFWithHoldings{ISIN: eh.security.ISIN, Name: eh.security.Name, Holdings: weights})
		labels = append(labels, eh.security.Name)
	}

	// Build overlap matrix via the shared helper — same symmetry + diagonal
	// guarantees the unit tests in internal/analytics/decomposition_test.go
	// already cover.
	if len(etfs) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{"labels": []string{}, "matrix": [][]float64{}})
		return
	}
	matrix := analytics.BuildOverlapMatrix(etfs)

	writeJSON(w, http.StatusOK, map[string]any{
		"labels": labels,
		"matrix": matrix,
	})
}

// HandleSectorHistory computes historical sector allocation by replaying the
// transaction log and applying each security's sector weights at monthly intervals.
func (h *AnalysisHandler) HandleSectorHistory(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	txns, err := listAllTransactionsRows(ctx, h.queries)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list transactions: "+err.Error())
		return
	}
	if len(txns) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{"history": []any{}, "sectors": []string{}})
		return
	}

	fxRates := loadFXRatesMap(ctx, h.queries)
	histFX := loadHistoricalFXRates(ctx, h.queries)

	// Sort chronologically (ListAllTransactions returns DESC)
	for i, j := 0, len(txns)-1; i < j; i, j = i+1, j-1 {
		txns[i], txns[j] = txns[j], txns[i]
	}

	// Cache sector weights per ISIN
	sectorCache := make(map[string]map[string]float64)
	getSectorWeights := func(isin string) map[string]float64 {
		if w, ok := sectorCache[isin]; ok {
			return w
		}
		sec, err := h.queries.GetSecurity(ctx, isin)
		if err != nil {
			sectorCache[isin] = nil
			return nil
		}
		w := analytics.ParseWeights(sec.SectorWeights)
		sectorCache[isin] = w
		return w
	}

	type holdingState struct {
		quantity  float64
		totalCost float64
	}
	holdings := make(map[string]*holdingState)

	type sectorSnapshot struct {
		Date    string             `json:"date"`
		Sectors map[string]float64 `json:"sectors"`
	}
	snapshots := []sectorSnapshot{}

	// Emit a snapshot of sector allocation from current holdings state
	emitSnapshot := func(date string) {
		totalValue := 0.0
		isinValues := make(map[string]float64)
		for isin, hs := range holdings {
			if hs.quantity <= 1e-9 {
				continue
			}
			val := hs.totalCost // use EUR cost basis for historical
			isinValues[isin] = val
			totalValue += val
		}
		if totalValue <= 0 {
			return
		}

		sectorExposure := make(map[string]float64)
		for isin, val := range isinValues {
			weight := val / totalValue
			sw := getSectorWeights(isin)
			for sector, pct := range sw {
				sectorExposure[sector] += weight * pct
			}
		}
		snapshots = append(snapshots, sectorSnapshot{
			Date:    date,
			Sectors: sectorExposure,
		})
	}

	lastMonth := ""
	for _, txn := range txns {
		amt := convertAmountToEURAtDate(numericToFloat(txn.Amount), txn.Currency, txn.Date, histFX, fxRates)
		qty := numericToFloat(txn.Quantity)
		isin := ""
		if txn.SecurityISIN.Valid {
			isin = txn.SecurityISIN.String
		}

		month := txn.Date.Format("2006-01")
		if month != lastMonth && lastMonth != "" {
			emitSnapshot(lastMonth + "-01")
		}
		lastMonth = month

		switch txn.Type {
		case "buy", "savings_plan":
			if isin != "" {
				hs, ok := holdings[isin]
				if !ok {
					hs = &holdingState{}
					holdings[isin] = hs
				}
				hs.quantity += qty
				hs.totalCost += amt
			}
		case "sell":
			if isin != "" {
				hs, ok := holdings[isin]
				if ok && hs.quantity > 0 {
					costPerUnit := hs.totalCost / hs.quantity
					hs.quantity -= qty
					hs.totalCost -= costPerUnit * qty
					if hs.quantity <= 1e-9 {
						delete(holdings, isin)
					}
				}
			}
		case "transfer":
			if isin != "" {
				hs, ok := holdings[isin]
				if !ok {
					hs = &holdingState{}
					holdings[isin] = hs
				}
				hs.quantity += qty
				hs.totalCost += amt
			}
		case "transfer_out":
			if isin != "" {
				hs, ok := holdings[isin]
				if ok && hs.quantity > 0 {
					costPerUnit := hs.totalCost / hs.quantity
					hs.quantity -= qty
					hs.totalCost -= costPerUnit * qty
					if hs.quantity <= 1e-9 {
						delete(holdings, isin)
					}
				}
			}
		}
	}

	// Final snapshot using EUR market prices where available
	if lastMonth != "" {
		totalValue := 0.0
		isinValues := make(map[string]float64)
		for isin, hs := range holdings {
			if hs.quantity <= 1e-9 {
				continue
			}
			val := hs.totalCost
			if price, err := h.queries.GetLatestPrice(ctx, isin); err == nil && price.Close.Valid {
				f, _ := price.Close.Float64Value()
				eurPrice := convertAmountToEUR(f.Float64, price.Currency, fxRates)
				if eurPrice > 0 {
					val = hs.quantity * eurPrice
				}
			}
			isinValues[isin] = val
			totalValue += val
		}
		if totalValue > 0 {
			sectorExposure := make(map[string]float64)
			for isin, val := range isinValues {
				weight := val / totalValue
				sw := getSectorWeights(isin)
				for sector, pct := range sw {
					sectorExposure[sector] += weight * pct
				}
			}
			snapshots = append(snapshots, sectorSnapshot{
				Date:    lastMonth + "-01",
				Sectors: sectorExposure,
			})
		}
	}

	// Collect all sector names across all snapshots
	sectorSet := make(map[string]bool)
	for _, s := range snapshots {
		for sector := range s.Sectors {
			sectorSet[sector] = true
		}
	}
	allSectors := make([]string, 0, len(sectorSet))
	for s := range sectorSet {
		allSectors = append(allSectors, s)
	}
	sort.Strings(allSectors)

	writeJSON(w, http.StatusOK, map[string]any{
		"history": snapshots,
		"sectors": allSectors,
	})
}

// HandleRisk computes portfolio risk metrics from cashflow-adjusted daily net worth snapshots.
func (h *AnalysisHandler) HandleRisk(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	snaps, err := h.queries.ListNetWorthSnapshots(ctx, 3000)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list snapshots: "+err.Error())
		return
	}
	if len(snaps) < 2 {
		writeJSON(w, http.StatusOK, map[string]any{"risk": analytics.RiskMetrics{}})
		return
	}

	// Snapshots are returned newest-first; reverse for chronological order
	points := make([]analytics.DailyValuation, len(snaps))
	for i, s := range snaps {
		val := 0.0
		if s.Total.Valid {
			f, _ := s.Total.Float64Value()
			val = f.Float64
		}
		points[len(snaps)-1-i] = analytics.DailyValuation{
			Date:  s.Date,
			Value: val,
		}
	}

	// Filter out zero-value leading snapshots (before any data)
	start := 0
	for start < len(points) && points[start].Value <= 0 {
		start++
	}
	points = points[start:]
	if len(points) < 2 {
		writeJSON(w, http.StatusOK, map[string]any{"risk": analytics.RiskMetrics{}})
		return
	}

	const riskFreeRate = 0.03 // ECB deposit facility rate (approximate)

	// Compute benchmark risk metrics if available
	benchPrices := loadBenchmarkPrices(ctx, h.queries, h.benchmarkISIN, h.benchmarkTicker)
	var benchMetrics *analytics.RiskMetrics
	if len(benchPrices) > 1 {
		portfolioStart := points[0].Date
		portfolioEnd := points[len(points)-1].Date
		var benchPoints []analytics.DailyValuation
		for _, bp := range benchPrices {
			if !bp.date.Before(portfolioStart) && !bp.date.After(portfolioEnd) {
				benchPoints = append(benchPoints, analytics.DailyValuation{
					Date:  bp.date,
					Value: bp.price,
				})
			}
		}
		if len(benchPoints) > 1 {
			benchMetrics = analytics.ComputeRiskMetrics(benchPoints, riskFreeRate, benchPoints[len(benchPoints)-1].Value)
		}
	}

	currentValue := points[len(points)-1].Value
	externalFlows := h.buildExternalFlowsForRisk(ctx)
	adjustedPoints := analytics.BuildCashflowAdjustedSeries(points, externalFlows)
	metrics := analytics.ComputeRiskMetrics(adjustedPoints, riskFreeRate, currentValue)
	rolling := analytics.ComputeRollingMetrics(adjustedPoints, riskFreeRate)

	result := map[string]any{"risk": metrics}
	if benchMetrics != nil {
		result["benchmark_risk"] = benchMetrics
	}
	if rolling != nil {
		result["rolling"] = rolling
	}

	writeJSON(w, http.StatusOK, result)
}

// HandleTax computes German tax summary for a given year.
// Query: year, joint=1 (Zusammenveranlagung), church_tax=0.08|0.09.
func (h *AnalysisHandler) HandleTax(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	yearStr := r.URL.Query().Get("year")
	now := time.Now()
	curYear := now.Year()
	year := curYear
	if yearStr != "" {
		if y, err := strconv.Atoi(yearStr); err == nil && y >= 2020 && y <= 2100 {
			year = y
		}
	}
	opts := taxOptionsFromRequest(r)

	txns, err := listAllTransactionsRows(ctx, h.queries)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list transactions: "+err.Error())
		return
	}

	fxRates := loadFXRatesMap(ctx, h.queries)
	histFX := loadHistoricalFXRates(ctx, h.queries)

	secs, err := h.queries.ListSecurities(ctx)
	if err != nil {
		secs = nil
	}
	secMap := make(map[string]db.Security, len(secs))
	for _, s := range secs {
		secMap[s.ISIN] = s
	}

	taxTxns, names, tfsMap, equityMap := buildTaxTransactions(txns, secs, histFX, fxRates)
	curPrices := h.currentUnitPricesEUR(ctx)

	// Vorabpauschalen of completed years: taxed in the following January
	// (§18 Abs. 3 InvStG) and deducted from later sale gains (§19 Abs. 1 S. 3).
	vpEvents := h.vorabpauschaleEvents(ctx, taxTxns, secMap, tfsMap, histFX, fxRates, 0, curYear-1, nil)
	ledgerTxns := analytics.MergeVorabpauschaleEvents(taxTxns, vpEvents)

	// Loss-harvest hints only make sense for the running year.
	var hints []analytics.TaxLossHint
	if year == curYear {
		hints = h.lossHarvestHints(ctx, ledgerTxns, curPrices, names, equityMap, now, opts)
	}

	summary := analytics.ComputeTaxSummaryWithOptions(ledgerTxns, year, hints, opts)

	// The table shows the Vorabpauschale computed for calendar year `year`,
	// which is taxed in January of year+1. For the running year it is an
	// estimate with today's price standing in for the year-end price.
	var vorabpauschale []analytics.VorabpauschaleEntry
	switch {
	case year < curYear:
		vorabpauschale = analytics.VorabpauschaleForYear(ledgerTxns, year, opts)
	case year == curYear:
		est := h.vorabpauschaleEvents(ctx, taxTxns, secMap, tfsMap, histFX, fxRates, curYear, curYear, curPrices)
		vorabpauschale = analytics.VorabpauschaleForYear(analytics.MergeVorabpauschaleEvents(taxTxns, append(append([]analytics.TaxTransaction{}, vpEvents...), est...)), year, opts)
	}
	if vorabpauschale == nil {
		vorabpauschale = []analytics.VorabpauschaleEntry{}
	}

	// Years with any realised sale or income (including Vorabpauschale
	// received in January and §23 private sales).
	yearSet := make(map[int]bool)
	sales, income := analytics.RealizedTaxEvents(ledgerTxns)
	for _, s := range sales {
		yearSet[s.Year] = true
	}
	for _, e := range income {
		if e.Year <= curYear {
			yearSet[e.Year] = true
		}
	}
	availableYears := []int{}
	for y := range yearSet {
		availableYears = append(availableYears, y)
	}
	sort.Slice(availableYears, func(i, j int) bool { return availableYears[i] > availableYears[j] })

	writeJSON(w, http.StatusOK, map[string]any{
		"summary":                 summary,
		"available_years":         availableYears,
		"vorabpauschale":          vorabpauschale,
		"vorabpauschale_year":     year,
		"vorabpauschale_taxed_in": year + 1,
		"vorabpauschale_estimate": year == curYear,
	})
}

// taxOptionsFromRequest reads the filing settings the Tax page keeps in the
// browser: ?joint=1 (Zusammenveranlagung, doubles the Sparer-Pauschbetrag)
// and ?church_tax=0.08|0.09 (Kirchensteuer, deductible per §32d Abs. 1 EStG).
func taxOptionsFromRequest(r *http.Request) analytics.TaxOptions {
	opts := analytics.TaxOptions{Joint: r.URL.Query().Get("joint") == "1"}
	if v := r.URL.Query().Get("church_tax"); v != "" {
		if f, err := parseFiniteFloat(v); err == nil && f >= 0 && f <= 1 {
			opts.ChurchTaxRate = f
		}
	}
	return opts
}

// currentUnitPricesEUR returns today's EUR price per unit for every security
// currently held (latest price at the latest FX rate).
func (h *AnalysisHandler) currentUnitPricesEUR(ctx context.Context) map[string]float64 {
	value := make(map[string]float64)
	qty := make(map[string]float64)
	enriched, _ := h.loadEnrichedHoldings(ctx)
	for _, eh := range enriched {
		q := numericToFloat(eh.holding.Quantity)
		if eh.value > 0 && q > 0 {
			value[eh.security.ISIN] += eh.value
			qty[eh.security.ISIN] += q
		}
	}
	prices := make(map[string]float64, len(value))
	for isin, v := range value {
		prices[isin] = v / qty[isin]
	}
	return prices
}

// vorabpauschaleEvents builds one "vorabpauschale" pseudo-transaction per
// fund and calendar year in [fromYear, throughYear], starting no earlier than
// the fund's first acquisition. Each carries the EUR redemption price at the
// start of the year (last close on or before 1 January) and at its end (last
// close on or before 31 December, §18 Abs. 1 InvStG). For the running year
// the year-end price is today's price from curPrices.
func (h *AnalysisHandler) vorabpauschaleEvents(
	ctx context.Context,
	taxTxns []analytics.TaxTransaction,
	secMap map[string]db.Security,
	tfsMap map[string]float64,
	histFX []market.HistoricalFXRate,
	fxRates map[string]float64,
	fromYear, throughYear int,
	curPrices map[string]float64,
) []analytics.TaxTransaction {
	firstYear := make(map[string]int)
	for _, t := range taxTxns {
		if t.ISIN == "" || t.Quantity <= 0 || (t.Type != "buy" && t.Type != "savings_plan" && t.Type != "transfer") {
			continue
		}
		sec, ok := secMap[t.ISIN]
		if !ok || (sec.AssetClass != "etf" && sec.AssetClass != "fund") {
			continue
		}
		if y, seen := firstYear[t.ISIN]; !seen || t.Year < y {
			firstYear[t.ISIN] = t.Year
		}
	}
	isins := make([]string, 0, len(firstYear))
	for isin := range firstYear {
		isins = append(isins, isin)
	}
	sort.Strings(isins)

	curYear := time.Now().Year()
	var events []analytics.TaxTransaction
	for _, isin := range isins {
		sec := secMap[isin]
		start := firstYear[isin]
		if fromYear > start {
			start = fromYear
		}
		for y := start; y <= throughYear; y++ {
			if bz, ok := analytics.BasiszinsByYear[y]; !ok || bz <= 0 {
				continue
			}
			p0 := h.priceEURNear(ctx, isin, time.Date(y, 1, 1, 0, 0, 0, 0, time.UTC), histFX, fxRates)
			p1 := 0.0
			if y == curYear && curPrices != nil {
				p1 = curPrices[isin]
			}
			if p1 <= 0 {
				p1 = h.priceEURNear(ctx, isin, time.Date(y, 12, 31, 0, 0, 0, 0, time.UTC), histFX, fxRates)
			}
			events = append(events, analytics.TaxTransaction{
				Date:           fmt.Sprintf("%04d-12-31", y),
				Year:           y,
				Type:           "vorabpauschale",
				ISIN:           isin,
				Name:           sec.Name,
				AssetClass:     sec.AssetClass,
				TFS:            tfsMap[isin],
				HasExplicitTFS: true,
				VPJan1Price:    p0,
				VPYearEndPrice: p1,
			})
		}
	}
	return events
}

// computeYearVorabpauschale returns the per-fund Vorabpauschale for calendar
// year `year` (taxed in January year+1), computed by the tax ledger: units
// held at year end with their original acquisition dates, start-of-year
// prices, the cap and the 1/12 reduction after it (§18 InvStG). For the
// running year, today's value stands in for the year-end price.
func (h *AnalysisHandler) computeYearVorabpauschale(
	ctx context.Context,
	year int,
	txns []db.ListTransactionsRow,
	secMap map[string]db.Security,
	tfsMap map[string]float64,
	curValByISIN map[string]float64,
	curQtyByISIN map[string]float64,
	histFX []market.HistoricalFXRate,
	fxRates map[string]float64,
) []analytics.VorabpauschaleEntry {
	secs := make([]db.Security, 0, len(secMap))
	for _, s := range secMap {
		secs = append(secs, s)
	}
	taxTxns, _, _, _ := buildTaxTransactions(txns, secs, histFX, fxRates)
	curPrices := make(map[string]float64, len(curValByISIN))
	for isin, v := range curValByISIN {
		if q := curQtyByISIN[isin]; v > 0 && q > 0 {
			curPrices[isin] = v / q
		}
	}
	events := h.vorabpauschaleEvents(ctx, taxTxns, secMap, tfsMap, histFX, fxRates, 0, year, curPrices)
	return analytics.VorabpauschaleForYear(analytics.MergeVorabpauschaleEvents(taxTxns, events), year, analytics.TaxOptions{})
}

// priceEURNear returns the last EUR close on or before date, ignoring prices
// more than a month older (a stale price would misstate the year's change).
func (h *AnalysisHandler) priceEURNear(ctx context.Context, isin string, date time.Time, histFX []market.HistoricalFXRate, fxRates map[string]float64) float64 {
	row, err := h.queries.GetPriceAtDate(ctx, db.GetPriceAtDateParams{SecurityISIN: isin, Date: date})
	if err != nil || !row.Close.Valid || date.Sub(row.Date) > 31*24*time.Hour {
		return 0
	}
	return convertAmountToEURAtDate(numericToFloat(row.Close), row.Currency, row.Date, histFX, fxRates)
}

// lossHarvestHints lists depot positions whose FIFO sale today (cost basis at
// purchase-date FX, value at today's price) would realise a loss, with the
// reduction in this year's tax that sale would bring. The saving runs through
// the full engine, so a share loss only counts against share gains, and
// nothing is saved when this year's income is already covered by the
// Sparer-Pauschbetrag or carried losses — the loss then just carries forward.
func (h *AnalysisHandler) lossHarvestHints(
	ctx context.Context,
	ledgerTxns []analytics.TaxTransaction,
	prices map[string]float64,
	names map[string]string,
	equityMap map[string]bool,
	now time.Time,
	opts analytics.TaxOptions,
) []analytics.TaxLossHint {
	acctNames := make(map[string]string)
	if accts, err := h.queries.ListAccounts(ctx); err == nil {
		for _, a := range accts {
			acctNames[a.ID.String()] = a.Name
		}
	}
	return computeLossHarvestHints(ledgerTxns, prices, names, equityMap, acctNames, now, opts)
}

// computeLossHarvestHints is the DB-free core of lossHarvestHints.
func computeLossHarvestHints(
	ledgerTxns []analytics.TaxTransaction,
	prices map[string]float64,
	names map[string]string,
	equityMap map[string]bool,
	acctNames map[string]string,
	now time.Time,
	opts analytics.TaxOptions,
) []analytics.TaxLossHint {
	lots := analytics.ComputeTaxLots(ledgerTxns, prices, names, equityMap)
	type position struct {
		acct, isin string
		lots       []analytics.TaxLot
		pl         float64
		taxable    float64
	}
	byKey := make(map[string]*position)
	var order []string
	for _, l := range lots {
		if l.PrivateSale || prices[l.ISIN] <= 0 {
			continue
		}
		k := l.AccountID + "|" + l.ISIN
		p, ok := byKey[k]
		if !ok {
			p = &position{acct: l.AccountID, isin: l.ISIN}
			byKey[k] = p
			order = append(order, k)
		}
		p.lots = append(p.lots, l)
		p.pl += l.UnrealizedPL
		p.taxable += (l.CurrentValue - l.CostBasis - l.VorabpauschaleTaxed) * (1 - l.TFSRate)
	}

	base := analytics.ComputeTaxSummaryWithOptions(ledgerTxns, now.Year(), nil, opts).EstimatedTax
	var hints []analytics.TaxLossHint
	for _, k := range order {
		p := byKey[k]
		if p.pl >= -10 || p.taxable >= 0 { // only meaningful losses
			continue
		}
		all := make([]analytics.TaxTransaction, 0, len(ledgerTxns)+1)
		all = append(all, ledgerTxns...)
		all = append(all, analytics.HypotheticalSellTxns(p.lots, now)...)
		with := analytics.ComputeTaxSummaryWithOptions(all, now.Year(), nil, opts).EstimatedTax
		pot := p.lots[0].Pot
		hints = append(hints, analytics.TaxLossHint{
			ISIN:            p.isin,
			Name:            p.lots[0].Name,
			UnrealizedPL:    math.Round(p.pl*100) / 100,
			PotentialSaving: math.Round(math.Max(0, base-with)*100) / 100,
			AccountID:       p.acct,
			Account:         acctNames[p.acct],
			TaxableLoss:     math.Round(p.taxable*100) / 100,
			Pot:             pot,
		})
	}
	sort.SliceStable(hints, func(i, j int) bool {
		if hints[i].PotentialSaving != hints[j].PotentialSaving {
			return hints[i].PotentialSaving > hints[j].PotentialSaving
		}
		return hints[i].UnrealizedPL < hints[j].UnrealizedPL
	})
	return hints
}

// HandleCosts computes ETF cost analysis from TER data and current holdings.
func (h *AnalysisHandler) HandleCosts(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	enriched, err := h.loadEnrichedHoldings(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "load holdings: "+err.Error())
		return
	}

	type costEntry struct {
		ISIN       string  `json:"isin"`
		Name       string  `json:"name"`
		TER        float64 `json:"ter"`
		Value      float64 `json:"value"`
		Weight     float64 `json:"weight"`
		AnnualCost float64 `json:"annual_cost"`
	}

	totalValue := 0.0
	for _, eh := range enriched {
		totalValue += eh.value
	}

	entries := []costEntry{}
	totalCost := 0.0
	weightedTER := 0.0
	coveredValue := 0.0 // sum of value across holdings with a known TER

	for _, eh := range enriched {
		ter := numericToFloat(eh.security.TER)
		if ter <= 0 {
			continue
		}
		weight := 0.0
		if totalValue > 0 {
			weight = (eh.value / totalValue) * 100
		}
		annualCost := eh.value * ter / 100
		totalCost += annualCost
		weightedTER += weight * ter / 100
		coveredValue += eh.value

		entries = append(entries, costEntry{
			ISIN:       eh.security.ISIN,
			Name:       eh.security.Name,
			TER:        ter,
			Value:      math.Round(eh.value*100) / 100,
			Weight:     math.Round(weight*10) / 10,
			AnnualCost: math.Round(annualCost*100) / 100,
		})
	}

	coveragePct := 0.0
	if totalValue > 0 {
		coveragePct = (coveredValue / totalValue) * 100
	}
	weightedTERCovered := 0.0
	if coveredValue > 0 {
		weightedTERCovered = weightedTER * (totalValue / coveredValue)
	}

	// Sort by annual cost descending
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].AnnualCost > entries[j].AnnualCost
	})

	// 30-year milestones projection
	projection := []map[string]any{}
	cumulative := 0.0
	for year := 1; year <= 30; year++ {
		cumulative += totalCost
		if year == 1 || year == 5 || year == 10 || year == 20 || year == 30 {
			projection = append(projection, map[string]any{
				"year":       year,
				"cumulative": math.Round(cumulative*100) / 100,
			})
		}
	}

	avgTER := 0.38
	grade := "A+"
	gradeDetail := "Excellent — well below average German ETF investor costs"
	if weightedTER > 0.10 {
		grade = "A"
		gradeDetail = "Very good — significantly below average"
	}
	if weightedTER > 0.20 {
		grade = "B+"
		gradeDetail = "Good — below average costs"
	}
	if weightedTER > 0.30 {
		grade = "B"
		gradeDetail = "Average — close to typical German ETF investor"
	}
	if weightedTER > 0.40 {
		grade = "C"
		gradeDetail = "Above average — consider lower-cost alternatives"
	}
	if weightedTER > 0.60 {
		grade = "D"
		gradeDetail = "High — significantly above average, review fund selection"
	}
	annualSaving := (avgTER - weightedTER) / 100 * totalValue
	tenYearSaving := annualSaving * 10

	// Total cost of ownership: TER + estimated transaction costs + estimated spread (in EUR)
	fxRates := loadFXRatesMap(ctx, h.queries)
	histFX := loadHistoricalFXRates(ctx, h.queries)
	txns, _ := listAllTransactionsRows(ctx, h.queries)
	totalTxnVolume := 0.0
	txnCount := 0
	totalFees := 0.0
	for _, txn := range txns {
		if txn.Type == "buy" || txn.Type == "sell" || txn.Type == "savings_plan" {
			if txn.Amount.Valid {
				f, _ := txn.Amount.Float64Value()
				totalTxnVolume += math.Abs(convertAmountToEURAtDate(f.Float64, txn.Currency, txn.Date, histFX, fxRates))
				txnCount++
			}
			if txn.Fee.Valid {
				f, _ := txn.Fee.Float64Value()
				totalFees += math.Abs(convertAmountToEURAtDate(f.Float64, txn.Currency, txn.Date, histFX, fxRates))
			}
		}
	}
	spreadCostEst := totalTxnVolume * 0.0005
	years := 1.0
	if len(txns) > 1 {
		first := txns[len(txns)-1].Date
		last := txns[0].Date
		y := last.Sub(first).Hours() / (365.25 * 24)
		if y > 0.5 {
			years = y
		}
	}
	annualTxnCost := totalFees / years
	annualSpreadCost := spreadCostEst / years
	totalAnnualCost := totalCost + annualTxnCost + annualSpreadCost
	totalAnnualPct := 0.0
	if totalValue > 0 {
		totalAnnualPct = math.Round(totalAnnualCost/totalValue*10000) / 100
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"holdings":                  entries,
		"total_value":               math.Round(totalValue*100) / 100,
		"weighted_ter":              math.Round(weightedTER*100) / 100,
		"weighted_ter_covered_only": math.Round(weightedTERCovered*100) / 100,
		"coverage_pct":              math.Round(coveragePct*10) / 10,
		"annual_cost":               math.Round(totalCost*100) / 100,
		"daily_cost":                math.Round(totalCost/365*100) / 100,
		"projection":                projection,
		"benchmark": map[string]any{
			"avg_ter":         avgTER,
			"your_ter":        math.Round(weightedTER*100) / 100,
			"grade":           grade,
			"detail":          gradeDetail,
			"annual_saving":   math.Round(annualSaving*100) / 100,
			"ten_year_saving": math.Round(tenYearSaving*100) / 100,
		},
		"total_cost_ownership": map[string]any{
			"ter_cost":          math.Round(totalCost*100) / 100,
			"transaction_fees":  math.Round(annualTxnCost*100) / 100,
			"spread_estimate":   math.Round(annualSpreadCost*100) / 100,
			"total_annual":      math.Round(totalAnnualCost*100) / 100,
			"total_annual_pct":  totalAnnualPct,
			"lifetime_fees":     math.Round(totalFees*100) / 100,
			"lifetime_volume":   math.Round(totalTxnVolume),
			"transaction_count": txnCount,
		},
	})
}

// HandleCorrelation computes pairwise price correlation between all held securities.
func (h *AnalysisHandler) HandleCorrelation(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	enriched, err := h.loadEnrichedHoldings(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "load holdings: "+err.Error())
		return
	}

	// Deduplicate by ISIN
	type secInfo struct {
		isin, name string
	}
	seen := make(map[string]bool)
	var secs []secInfo
	for _, eh := range enriched {
		if seen[eh.security.ISIN] {
			continue
		}
		seen[eh.security.ISIN] = true
		secs = append(secs, secInfo{isin: eh.security.ISIN, name: eh.security.Name})
	}

	if len(secs) < 2 {
		writeJSON(w, http.StatusOK, map[string]any{"labels": []string{}, "matrix": [][]float64{}})
		return
	}

	// Load price histories and compute daily returns per ISIN
	returnsByISIN := make(map[string]map[string]float64) // isin -> date -> return

	for _, sec := range secs {
		prices, err := h.queries.ListPriceHistory(ctx, sec.isin)
		if err != nil || len(prices) < 30 {
			continue
		}
		returns := make(map[string]float64)
		for i := 1; i < len(prices); i++ {
			if !prices[i-1].Close.Valid || !prices[i].Close.Valid {
				continue
			}
			prev, _ := prices[i-1].Close.Float64Value()
			cur, _ := prices[i].Close.Float64Value()
			if prev.Float64 > 0 {
				r := (cur.Float64 - prev.Float64) / prev.Float64
				if r > -0.5 && r < 0.5 { // skip outliers
					returns[prices[i].Date.Format("2006-01-02")] = r
				}
			}
		}
		if len(returns) > 20 {
			returnsByISIN[sec.isin] = returns
		}
	}

	// Filter to securities with price data
	var validSecs []secInfo
	for _, sec := range secs {
		if _, ok := returnsByISIN[sec.isin]; ok {
			validSecs = append(validSecs, sec)
		}
	}

	n := len(validSecs)
	if n < 2 {
		writeJSON(w, http.StatusOK, map[string]any{"labels": []string{}, "matrix": [][]float64{}})
		return
	}

	// Compute pairwise Pearson correlation
	labels := make([]string, n)
	matrix := make([][]float64, n)
	for i := 0; i < n; i++ {
		labels[i] = validSecs[i].name
		matrix[i] = make([]float64, n)
		matrix[i][i] = 1.0
	}

	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			rA := returnsByISIN[validSecs[i].isin]
			rB := returnsByISIN[validSecs[j].isin]

			// Find common dates
			var a, b []float64
			for date, ra := range rA {
				if rb, ok := rB[date]; ok {
					a = append(a, ra)
					b = append(b, rb)
				}
			}

			corr := 0.0
			if len(a) > 10 {
				meanA, meanB := 0.0, 0.0
				for k := range a {
					meanA += a[k]
					meanB += b[k]
				}
				meanA /= float64(len(a))
				meanB /= float64(len(b))

				cov, varA, varB := 0.0, 0.0, 0.0
				for k := range a {
					da := a[k] - meanA
					db := b[k] - meanB
					cov += da * db
					varA += da * da
					varB += db * db
				}
				if varA > 0 && varB > 0 {
					corr = cov / (math.Sqrt(varA) * math.Sqrt(varB))
					if math.IsNaN(corr) || math.IsInf(corr, 0) {
						corr = 0
					} else {
						corr = math.Max(-1, math.Min(1, corr))
					}
				}
			}

			matrix[i][j] = math.Round(corr*100) / 100
			matrix[j][i] = matrix[i][j]
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"labels": labels,
		"matrix": matrix,
	})
}

// HandleFXHistory returns historical exchange rates for major currency pairs.
func (h *AnalysisHandler) HandleFXHistory(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	currencies := []string{"USD", "GBP", "JPY", "CHF"}

	type ratePoint struct {
		Date string  `json:"date"`
		Rate float64 `json:"rate"`
	}
	result := make(map[string][]ratePoint)

	for _, cur := range currencies {
		rates, err := h.queries.ListExchangeRateHistory(ctx, cur)
		if err != nil || len(rates) == 0 {
			continue
		}
		// Sample weekly for chart readability
		var points []ratePoint
		for i, r := range rates {
			if i%7 == 0 || i == len(rates)-1 {
				rate := numericToFloat(r.Rate)
				if rate > 0 {
					points = append(points, ratePoint{
						Date: r.Date.Format("2006-01-02"),
						Rate: math.Round(1/rate*10000) / 10000, // convert EUR→X to X per EUR
					})
				}
			}
		}
		if len(points) > 0 {
			result[cur] = points
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{"rates": result})
}

// HandleCashFlow computes monthly income/expense summary and forward projection.
func (h *AnalysisHandler) HandleCashFlow(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	txns, err := listAllTransactionsRows(ctx, h.queries)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list transactions: "+err.Error())
		return
	}
	fxRates := loadFXRatesMap(ctx, h.queries)
	histFX := loadHistoricalFXRates(ctx, h.queries)
	holdings, _ := assets.Load(ctx, h.queries)

	type monthFlow struct {
		Month    string  `json:"month"`
		Income   float64 `json:"income"`
		Expenses float64 `json:"expenses"`
		Net      float64 `json:"net"`
	}

	monthly := make(map[string]*monthFlow)
	for _, txn := range txns {
		amt := 0.0
		if txn.Amount.Valid {
			f, _ := txn.Amount.Float64Value()
			amt = convertAmountToEURAtDate(f.Float64, txn.Currency, txn.Date, histFX, fxRates)
		}
		if isAssetPurchase(txn.Category) {
			continue // cash → physical asset, not an expense
		}
		if isLoanPayment(txn.Category) {
			interest, ok := loanInterestPart(holdings, txn.ID)
			if !ok {
				continue // extra repayment: saving
			}
			amt = math.Copysign(interest, amt) // keep the import's sign convention
		}
		month := txn.Date.Format("2006-01")
		if monthly[month] == nil {
			monthly[month] = &monthFlow{Month: month}
		}
		switch txn.Type {
		case "deposit", "dividend", "interest":
			monthly[month].Income += amt
		case "withdrawal", "fee", "tax":
			monthly[month].Expenses += amt
		}
	}

	var months []string
	for m := range monthly {
		months = append(months, m)
	}
	sort.Strings(months)

	history := []monthFlow{}
	for _, m := range months {
		mf := monthly[m]
		mf.Net = math.Round((mf.Income-mf.Expenses)*100) / 100
		mf.Income = math.Round(mf.Income*100) / 100
		mf.Expenses = math.Round(mf.Expenses*100) / 100
		history = append(history, *mf)
	}

	// Compute averages for projection
	avgIncome, avgExpenses := 0.0, 0.0
	recent := history
	if len(recent) > 12 {
		recent = recent[len(recent)-12:]
	}
	for _, m := range recent {
		avgIncome += m.Income
		avgExpenses += m.Expenses
	}
	if len(recent) > 0 {
		avgIncome /= float64(len(recent))
		avgExpenses /= float64(len(recent))
	}

	// Seasonal patterns: compute average income/expenses by calendar month
	type seasonalMonth struct {
		CalMonth int     `json:"month"` // 1-12
		Income   float64 `json:"income"`
		Expenses float64 `json:"expenses"`
		Count    int     `json:"-"`
	}
	seasonal := make(map[int]*seasonalMonth)
	for _, mf := range history {
		t, err := time.Parse("2006-01", mf.Month)
		if err != nil {
			continue
		}
		cm := int(t.Month())
		sm := seasonal[cm]
		if sm == nil {
			sm = &seasonalMonth{CalMonth: cm}
			seasonal[cm] = sm
		}
		sm.Income += mf.Income
		sm.Expenses += mf.Expenses
		sm.Count++
	}
	seasonalData := []seasonalMonth{}
	for m := 1; m <= 12; m++ {
		sm := seasonal[m]
		if sm != nil && sm.Count > 0 {
			seasonalData = append(seasonalData, seasonalMonth{
				CalMonth: m,
				Income:   math.Round(sm.Income/float64(sm.Count)*100) / 100,
				Expenses: math.Round(sm.Expenses/float64(sm.Count)*100) / 100,
			})
		}
	}

	// Income auto-detection: find recurring deposit patterns
	type incomeSource struct {
		Name      string  `json:"name"`
		Amount    float64 `json:"amount"`
		Frequency int     `json:"occurrences"`
		Monthly   bool    `json:"monthly"`
	}
	depositsBySource := make(map[string][]float64)
	for _, txn := range txns {
		if txn.Type != "deposit" {
			continue
		}
		cp := ""
		if txn.Counterparty.Valid {
			cp = txn.Counterparty.String
		}
		if cp == "" {
			continue
		}
		amt := 0.0
		if txn.Amount.Valid {
			f, _ := txn.Amount.Float64Value()
			amt = convertAmountToEURAtDate(f.Float64, txn.Currency, txn.Date, histFX, fxRates)
		}
		if amt > 0 {
			depositsBySource[cp] = append(depositsBySource[cp], amt)
		}
	}
	incomeSources := []incomeSource{}
	for name, amounts := range depositsBySource {
		if len(amounts) < 3 {
			continue
		}
		// Check if amounts are similar (within 20% of median)
		sorted := make([]float64, len(amounts))
		copy(sorted, amounts)
		sort.Float64s(sorted)
		median := sorted[len(sorted)/2]
		if median <= 0 {
			continue
		}
		consistent := 0
		for _, a := range amounts {
			if math.Abs(a-median)/median < 0.2 {
				consistent++
			}
		}
		if consistent >= len(amounts)/2 {
			incomeSources = append(incomeSources, incomeSource{
				Name: name, Amount: math.Round(median*100) / 100,
				Frequency: len(amounts), Monthly: len(amounts) >= 6,
			})
		}
	}
	sort.Slice(incomeSources, func(i, j int) bool {
		return incomeSources[i].Amount > incomeSources[j].Amount
	})

	// Project 12 months forward using seasonal patterns when available
	projection := []monthFlow{}
	now := time.Now()
	for i := 1; i <= 12; i++ {
		d := addMonthsFirstOfMonth(now, i)
		cm := int(d.Month())
		inc := avgIncome
		exp := avgExpenses
		// Use seasonal average if we have data for this calendar month
		if sm, ok := seasonal[cm]; ok && sm.Count >= 2 {
			inc = sm.Income / float64(sm.Count)
			exp = sm.Expenses / float64(sm.Count)
		}
		projection = append(projection, monthFlow{
			Month:    d.Format("2006-01"),
			Income:   math.Round(inc*100) / 100,
			Expenses: math.Round(exp*100) / 100,
			Net:      math.Round((inc-exp)*100) / 100,
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"history":        history,
		"projection":     projection,
		"avg_income":     math.Round(avgIncome*100) / 100,
		"avg_expenses":   math.Round(avgExpenses*100) / 100,
		"avg_net":        math.Round((avgIncome-avgExpenses)*100) / 100,
		"seasonal":       seasonalData,
		"income_sources": incomeSources,
	})
}

// HandleAllocationHistory computes monthly holding weight history.
func (h *AnalysisHandler) HandleAllocationHistory(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	txns, err := listAllTransactionsRows(ctx, h.queries)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list transactions: "+err.Error())
		return
	}
	if len(txns) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{"history": []any{}, "holdings": []string{}})
		return
	}

	fxRates := loadFXRatesMap(ctx, h.queries)
	histFX := loadHistoricalFXRates(ctx, h.queries)

	// Sort chronologically
	for i, j := 0, len(txns)-1; i < j; i, j = i+1, j-1 {
		txns[i], txns[j] = txns[j], txns[i]
	}

	// Load security names and build collision-free display labels so sold
	// historical positions resolve directly via weights[label] on the frontend.
	secs, _ := h.queries.ListSecurities(ctx)
	rawNames := make(map[string]string, len(secs))
	nameCount := make(map[string]int, len(secs))
	for _, s := range secs {
		rawNames[s.ISIN] = s.Name
		if s.Name != "" {
			nameCount[s.Name]++
		}
	}
	labelForISIN := func(isin string) string {
		name := rawNames[isin]
		if name == "" {
			return isin
		}
		if nameCount[name] > 1 {
			return name + " (" + isin + ")"
		}
		return name
	}

	type holdState struct{ qty, cost float64 }
	holdings := make(map[string]*holdState)

	type snapshot struct {
		Date    string             `json:"date"`
		Weights map[string]float64 `json:"weights"`
	}
	snapshots := []snapshot{}
	lastMonth := ""

	emitSnapshot := func(date string) {
		totalValue := 0.0
		for _, hs := range holdings {
			if hs.qty > 1e-9 && hs.cost > 0 {
				totalValue += hs.cost
			}
		}
		if totalValue <= 0 {
			return
		}
		weights := make(map[string]float64)
		var sum float64
		var largestKey string
		var largestWeight float64
		for isin, hs := range holdings {
			if hs.qty > 1e-9 && hs.cost > 0 {
				label := labelForISIN(isin)
				w := math.Round((hs.cost/totalValue)*1000) / 10
				weights[label] += w
				sum += w
				if weights[label] > largestWeight {
					largestWeight = weights[label]
					largestKey = label
				}
			}
		}
		if largestKey != "" && sum != 100 {
			weights[largestKey] += math.Round((100-sum)*10) / 10
		}
		snapshots = append(snapshots, snapshot{Date: date, Weights: weights})
	}

	for _, txn := range txns {
		isin := ""
		if txn.SecurityISIN.Valid {
			isin = txn.SecurityISIN.String
		}
		amt, qty := 0.0, 0.0
		if txn.Amount.Valid {
			f, _ := txn.Amount.Float64Value()
			amt = convertAmountToEURAtDate(f.Float64, txn.Currency, txn.Date, histFX, fxRates)
		}
		if txn.Quantity.Valid {
			f, _ := txn.Quantity.Float64Value()
			qty = f.Float64
		}

		month := txn.Date.Format("2006-01")
		if month != lastMonth && lastMonth != "" {
			emitSnapshot(lastMonth + "-01")
		}
		lastMonth = month

		if isin == "" {
			continue
		}
		switch txn.Type {
		case "buy", "savings_plan", "transfer":
			hs := holdings[isin]
			if hs == nil {
				hs = &holdState{}
				holdings[isin] = hs
			}
			hs.qty += qty
			hs.cost += amt
		case "sell", "transfer_out":
			if hs := holdings[isin]; hs != nil && hs.qty > 0 {
				costPer := hs.cost / hs.qty
				hs.qty -= qty
				hs.cost -= costPer * qty
				if hs.qty <= 1e-9 {
					delete(holdings, isin)
				}
			}
		}
	}
	if lastMonth != "" {
		emitSnapshot(lastMonth + "-01")
	}

	holdingSet := make(map[string]bool)
	for _, s := range snapshots {
		for label := range s.Weights {
			holdingSet[label] = true
		}
	}
	holdingNames := []string{}
	for label := range holdingSet {
		holdingNames = append(holdingNames, label)
	}
	sort.Strings(holdingNames)

	writeJSON(w, http.StatusOK, map[string]any{"history": snapshots, "holdings": holdingNames})
}

// HandleExportTaxReport generates a CSV tax report for a given year.
func (h *AnalysisHandler) HandleExportTaxReport(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	yearStr := r.URL.Query().Get("year")
	year := time.Now().Year()
	if yearStr != "" {
		if y, err := strconv.Atoi(yearStr); err == nil && y >= 2020 && y <= 2100 {
			year = y
		}
	}

	txns, err := listAllTransactionsRows(ctx, h.queries)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list transactions: "+err.Error())
		return
	}
	fxRates := loadFXRatesMap(ctx, h.queries)
	histFX := loadHistoricalFXRates(ctx, h.queries)
	secs, _ := h.queries.ListSecurities(ctx)
	taxTxns, _, _, _ := buildTaxTransactions(txns, secs, histFX, fxRates)

	summary := analytics.ComputeTaxSummary(taxTxns, year, nil)

	filename := fmt.Sprintf("tax-report-%d.csv", year)
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename="+filename)
	w.Write([]byte{0xEF, 0xBB, 0xBF})
	fmt.Fprintf(w, "Steuerreport %d\n\n", year)
	fmt.Fprintf(w, "Zusammenfassung\n")
	fmt.Fprintf(w, "Realisierte Gewinne;%.2f EUR\n", summary.RealizedGains)
	fmt.Fprintf(w, "Realisierte Verluste;%.2f EUR\n", summary.RealizedLosses)
	fmt.Fprintf(w, "Netto-Gewinn;%.2f EUR\n", summary.NetGain)
	fmt.Fprintf(w, "Teilfreistellung;%.2f EUR\n", summary.TeilfreistellungAmt)
	fmt.Fprintf(w, "Steuerpflichtiger Gewinn;%.2f EUR\n", summary.TaxableGain)
	fmt.Fprintf(w, "Sparerpauschbetrag genutzt;%.2f EUR\n", summary.FreistellungUsed)
	fmt.Fprintf(w, "Sparerpauschbetrag verbleibend;%.2f EUR\n", summary.FreistellungRemain)
	fmt.Fprintf(w, "Dividendeneinkuenfte;%.2f EUR\n", summary.DividendIncome)
	fmt.Fprintf(w, "Geschaetzte Steuer (26,375%%);%.2f EUR\n", summary.EstimatedTax)
	fmt.Fprintf(w, "Effektiver Steuersatz;%.1f%%\n", summary.EffectiveRate)

	if len(summary.BySecurity) > 0 {
		fmt.Fprintf(w, "\nRealisierte Gewinne/Verluste nach Wertpapier\n")
		fmt.Fprintf(w, "ISIN;Name;Realisierter G/V;Teilfreistellung;Steuerpflichtiger G/V;Aktienfonds\n")
		for _, s := range summary.BySecurity {
			equity := "Nein"
			if s.IsEquityFund {
				equity = "Ja"
			}
			fmt.Fprintf(w, "%s;%s;%.2f;%.2f;%.2f;%s\n",
				s.ISIN, s.Name, s.RealizedPL, s.Teilfreistellung, s.TaxablePL, equity)
		}
	}
}

// HandleExportDATEV generates a DATEV-compatible CSV for Steuerberater import.
func (h *AnalysisHandler) HandleExportDATEV(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	yearStr := r.URL.Query().Get("year")
	year := time.Now().Year() - 1
	if yearStr != "" {
		if y, err := strconv.Atoi(yearStr); err == nil && y >= 2020 && y <= 2100 {
			year = y
		}
	}

	txns, _ := listAllTransactionsRows(ctx, h.queries)
	fxRates := loadFXRatesMap(ctx, h.queries)
	histFX := loadHistoricalFXRates(ctx, h.queries)
	secs, _ := h.queries.ListSecurities(ctx)
	accts, _ := h.queries.ListAccounts(ctx)

	secNames := make(map[string]string)
	for _, s := range secs {
		secNames[s.ISIN] = s.Name
	}
	acctNames := make(map[string]string)
	for _, a := range accts {
		acctNames[a.ID.String()] = a.Name
	}

	filename := fmt.Sprintf("DATEV-Kapitalertraege-%d.csv", year)
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename="+filename)
	w.Write([]byte{0xEF, 0xBB, 0xBF}) // UTF-8 BOM for Excel

	// DATEV header
	fmt.Fprintf(w, "\"EXTF\";510;21;\"Buchungsstapel\";7;%d0101;4;\"%d\";\"%d\";;;;\"\";\"\";1;\n", year, year, year)
	// Column headers
	fmt.Fprintf(w, "Umsatz;Soll/Haben;WKZ;Konto;Gegenkonto;Belegdatum;Buchungstext;Belegfeld1\n")

	for _, txn := range txns {
		if txn.Date.Year() != year {
			continue
		}
		if txn.Type != "dividend" && txn.Type != "interest" && txn.Type != "sell" && txn.Type != "fee" {
			continue
		}
		amt := 0.0
		if txn.Amount.Valid {
			f, _ := txn.Amount.Float64Value()
			amt = convertAmountToEURAtDate(f.Float64, txn.Currency, txn.Date, histFX, fxRates)
		}
		isin := ""
		if txn.SecurityISIN.Valid {
			isin = txn.SecurityISIN.String
		}
		name := secNames[isin]
		if name == "" {
			name = isin
		}
		acctName := acctNames[txn.AccountID.String()]

		sollHaben := "S"
		if amt > 0 && txn.Type != "fee" {
			sollHaben = "H"
		}

		konto := "1800"
		gegenkonto := "2650"
		if txn.Type == "fee" {
			gegenkonto = "4900"
		}

		buchungstext := fmt.Sprintf("%s: %s", txn.Type, name)
		if len(buchungstext) > 60 {
			buchungstext = buchungstext[:60]
		}

		belegdatum := txn.Date.Format("0201")
		belegfeld := acctName

		fmt.Fprintf(w, "%.2f;%s;EUR;%s;%s;%s;\"%s\";\"%s\"\n",
			math.Abs(amt), sollHaben, konto, gegenkonto, belegdatum, buchungstext, belegfeld)
	}
}

// HandleAnlageKAP generates Anlage KAP preparation data with per-broker detail.
func (h *AnalysisHandler) HandleAnlageKAP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	yearStr := r.URL.Query().Get("year")
	year := time.Now().Year() - 1 // default to previous year
	if yearStr != "" {
		if y, err := strconv.Atoi(yearStr); err == nil && y >= 2020 && y <= 2100 {
			year = y
		}
	}

	txns, err := listAllTransactionsRows(ctx, h.queries)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list transactions: "+err.Error())
		return
	}
	fxRates := loadFXRatesMap(ctx, h.queries)
	histFX := loadHistoricalFXRates(ctx, h.queries)
	accts, _ := h.queries.ListAccounts(ctx)
	secs, _ := h.queries.ListSecurities(ctx)

	acctNames := make(map[string]string)
	acctInstitution := make(map[string]string)
	for _, a := range accts {
		acctNames[a.ID.String()] = a.Name
		acctInstitution[a.ID.String()] = a.Institution
	}
	tfsMap := make(map[string]float64, len(secs))
	for _, s := range secs {
		tfsMap[s.ISIN] = securityTFSRate(s)
	}

	// Per-broker aggregation
	type brokerData struct {
		BrokerName    string  `json:"broker_name"`
		AccountName   string  `json:"account_name"`
		Dividends     float64 `json:"dividends"`       // Zeile 7: Kapitalerträge
		Interest      float64 `json:"interest"`        // Zeile 7: Zinsen
		RealizedGains float64 `json:"realized_gains"`  // Zeile 7: Gewinne
		RealizedLoss  float64 `json:"realized_losses"` // Zeile 12: Verluste
		TeilfreiAmt   float64 `json:"teilfreistellung"` // Zeile 8: Teilfreistellung
		WithheldTax   float64 `json:"withheld_tax"`    // Zeile 37: Einbehaltene KapESt (ohne Soli)
		SoliPaid      float64 `json:"soli_paid"`       // Zeile 38: Solidaritätszuschlag
		FSAUsed       float64 `json:"fsa_used"`        // Zeile 16: Beanspruchter FSA
	}
	brokers := make(map[string]*brokerData)
	getBroker := func(acctID string) *brokerData {
		key := acctInstitution[acctID]
		if key == "" {
			key = acctNames[acctID]
		}
		if key == "" {
			key = "Unknown"
		}
		b, ok := brokers[key]
		if !ok {
			b = &brokerData{BrokerName: key, AccountName: acctNames[acctID]}
			brokers[key] = b
		}
		return b
	}

	// Decompose total withheld broker tax (KapESt + Soli) into Zeile 37 KapESt
	// and Zeile 38 Soli so Zeile 50 (37 + 38) equals the exact tax withheld.
	addWithheldTax := func(b *brokerData, totalWithheld float64) {
		if totalWithheld <= 0 {
			return
		}
		kapESt := totalWithheld / (1 + analytics.Solidaritaetszuschlag)
		soli := totalWithheld - kapESt
		b.WithheldTax += kapESt
		b.SoliPaid += soli
	}

	// Per-depot FIFO lots (§ 20 Abs. 4 Satz 7 EStG)
	type depotKey struct {
		acctID string
		isin   string
	}
	type fifoLot struct {
		qty     float64
		costPer float64
	}
	lots := make(map[depotKey][]fifoLot)
	consumeFIFO := func(k depotKey, qty float64) (soldQty, costBasis float64) {
		rem := qty
		q := lots[k]
		for rem > 1e-9 && len(q) > 0 {
			if q[0].qty <= rem+1e-9 {
				soldQty += q[0].qty
				costBasis += q[0].qty * q[0].costPer
				rem -= q[0].qty
				q = q[1:]
			} else {
				soldQty += rem
				costBasis += rem * q[0].costPer
				q[0].qty -= rem
				rem = 0
			}
		}
		lots[k] = q
		return soldQty, costBasis
	}

	for i := len(txns) - 1; i >= 0; i-- { // chronological
		txn := txns[i]
		if txn.Date.Year() > year {
			continue // future transactions
		}
		isin := ""
		if txn.SecurityISIN.Valid {
			isin = txn.SecurityISIN.String
		}
		amt := 0.0
		if txn.Amount.Valid {
			f, _ := txn.Amount.Float64Value()
			amt = convertAmountToEURAtDate(f.Float64, txn.Currency, txn.Date, histFX, fxRates)
		}
		qty := 0.0
		if txn.Quantity.Valid {
			f, _ := txn.Quantity.Float64Value()
			qty = f.Float64
		}
		fee := 0.0
		if txn.Fee.Valid {
			f, _ := txn.Fee.Float64Value()
			fee = convertAmountToEURAtDate(f.Float64, txn.Currency, txn.Date, histFX, fxRates)
		}
		tax := 0.0
		if txn.Tax.Valid {
			f, _ := txn.Tax.Float64Value()
			tax = convertAmountToEURAtDate(f.Float64, txn.Currency, txn.Date, histFX, fxRates)
		}
		acctID := txn.AccountID.String()
		dk := depotKey{acctID: acctID, isin: isin}

		switch txn.Type {
		case "buy", "savings_plan", "transfer":
			if isin == "" || qty <= 0 {
				continue
			}
			lots[dk] = append(lots[dk], fifoLot{qty: qty, costPer: (amt + fee) / qty})
		case "transfer_out":
			if isin == "" || qty <= 0 {
				continue
			}
			consumeFIFO(dk, qty)
		case "sell":
			if isin == "" || qty <= 0 {
				continue
			}
			soldQty, costBasis := consumeFIFO(dk, qty)
			if txn.Date.Year() != year || soldQty <= 1e-9 {
				continue
			}
			proceeds := amt - fee
			if soldQty < qty {
				proceeds = (amt - fee) * (soldQty / qty)
			}
			gain := proceeds - costBasis

			b := getBroker(acctID)
			if gain > 0 {
				tfs := tfsMap[isin]
				if tfs > 0 {
					b.TeilfreiAmt += gain * tfs
					gain *= (1 - tfs)
				}
				b.RealizedGains += gain
			} else {
				// Preserved at full negative magnitude per anlage_kap_test.go contract
				b.RealizedLoss += gain
			}
			addWithheldTax(b, tax)

		case "dividend":
			if txn.Date.Year() != year {
				continue
			}
			b := getBroker(acctID)
			divAmt := amt
			tfs := tfsMap[isin]
			if tfs > 0 {
				b.TeilfreiAmt += divAmt * tfs
				divAmt *= (1 - tfs)
			}
			b.Dividends += divAmt
			addWithheldTax(b, tax)

		case "interest":
			if txn.Date.Year() != year {
				continue
			}
			b := getBroker(acctID)
			b.Interest += amt
			addWithheldTax(b, tax)
		}
	}

	// Build result
	brokerList := make([]brokerData, 0)
	for _, b := range brokers {
		b.Dividends = math.Round(b.Dividends*100) / 100
		b.Interest = math.Round(b.Interest*100) / 100
		b.RealizedGains = math.Round(b.RealizedGains*100) / 100
		b.RealizedLoss = math.Round(b.RealizedLoss*100) / 100
		b.TeilfreiAmt = math.Round(b.TeilfreiAmt*100) / 100
		b.WithheldTax = math.Round(b.WithheldTax*100) / 100
		b.SoliPaid = math.Round(b.SoliPaid*100) / 100
		brokerList = append(brokerList, *b)
	}
	sort.Slice(brokerList, func(i, j int) bool {
		return (brokerList[i].Dividends + brokerList[i].RealizedGains) > (brokerList[j].Dividends + brokerList[j].RealizedGains)
	})

	// Cross-broker aggregation for Anlage KAP
	totalDividends := 0.0
	totalInterest := 0.0
	totalGains := 0.0
	totalLosses := 0.0
	totalTeilfrei := 0.0
	totalWithheld := 0.0
	totalSoli := 0.0
	for _, b := range brokerList {
		totalDividends += b.Dividends
		totalInterest += b.Interest
		totalGains += b.RealizedGains
		totalLosses += b.RealizedLoss
		totalTeilfrei += b.TeilfreiAmt
		totalWithheld += b.WithheldTax
		totalSoli += b.SoliPaid
	}

	grossIncome := totalDividends + totalInterest + totalGains
	netTaxable := grossIncome + totalLosses // losses are negative
	fsaApplied := math.Min(math.Max(netTaxable, 0), analytics.Sparerpauschbetrag)
	afterFSA := math.Max(netTaxable-fsaApplied, 0)
	estimatedTax := afterFSA * analytics.EffectiveTaxRate

	type kapLine struct {
		Line        int     `json:"line"`
		Description string  `json:"description"`
		Amount      float64 `json:"amount"`
	}
	anlageKAP := []kapLine{
		{7, "Kapitalerträge (Dividenden, Zinsen, Gewinne)", math.Round(grossIncome*100) / 100},
		{8, "Davon: Teilfreistellung nach §20 Abs.1 InvStG", math.Round(totalTeilfrei*100) / 100},
		{12, "Verluste aus Kapitalvermögen", math.Round(totalLosses*100) / 100},
		{14, "Steuerpflichtiger Betrag nach Teilfreistellung", math.Round(netTaxable*100) / 100},
		{16, "In Anspruch genommener Sparer-Pauschbetrag", math.Round(fsaApplied*100) / 100},
		{37, "Einbehaltene Kapitalertragsteuer", math.Round(totalWithheld*100) / 100},
		{38, "Einbehaltener Solidaritätszuschlag", math.Round(totalSoli*100) / 100},
		{50, "Anzurechnende Steuer (Zeile 37 + 38)", math.Round((totalWithheld+totalSoli)*100) / 100},
	}

	crossBrokerNote := ""
	if len(brokerList) > 1 {
		hasGainBroker := false
		hasLossBroker := false
		for _, b := range brokerList {
			if b.RealizedGains > 0 {
				hasGainBroker = true
			}
			if b.RealizedLoss < 0 {
				hasLossBroker = true
			}
		}
		if hasGainBroker && hasLossBroker {
			crossBrokerNote = "Verluste bei einem Broker können mit Gewinnen bei einem anderen Broker verrechnet werden. Anlage KAP einreichen lohnt sich!"
		} else {
			crossBrokerNote = fmt.Sprintf("Einkünfte aus %d Banken — Anlage KAP einreichen, damit der Sparerpauschbetrag korrekt verrechnet wird.", len(brokerList))
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"year":              year,
		"brokers":           brokerList,
		"anlage_kap":        anlageKAP,
		"total_income":      math.Round(grossIncome*100) / 100,
		"total_losses":      math.Round(totalLosses*100) / 100,
		"total_teilfrei":    math.Round(totalTeilfrei*100) / 100,
		"net_taxable":       math.Round(netTaxable*100) / 100,
		"fsa_applied":       math.Round(fsaApplied*100) / 100,
		"estimated_tax":     math.Round(estimatedTax*100) / 100,
		"total_withheld":    math.Round((totalWithheld+totalSoli)*100) / 100,
		"cross_broker_note": crossBrokerNote,
		"broker_count":      len(brokerList),
	})
}

// HandleInflation returns inflation-adjusted net worth data using German HVPI.
func (h *AnalysisHandler) HandleInflation(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	cpiRates := map[int]float64{
		2020: 0.5, 2021: 3.1, 2022: 6.9, 2023: 5.9, 2024: 2.2, 2025: 2.0, 2026: 2.0,
	}

	snaps, err := h.queries.ListNetWorthSnapshots(ctx, 5000)
	if err != nil || len(snaps) < 2 {
		writeJSON(w, http.StatusOK, map[string]any{"history": []any{}})
		return
	}

	type inflPoint struct {
		Date     string  `json:"date"`
		Nominal  float64 `json:"nominal"`
		Real     float64 `json:"real"`
		CpiIndex float64 `json:"cpi_index"`
	}

	cpiIndex := make(map[string]float64)
	now := snaps[0].Date
	cpiIndex[now.Format("2006-01")] = 100.0
	for m := 1; m < len(snaps); m++ {
		d := snaps[m].Date
		month := d.Format("2006-01")
		if _, exists := cpiIndex[month]; exists {
			continue
		}
		yr := d.Year()
		annualRate := cpiRates[yr]
		if annualRate == 0 {
			annualRate = 2.0
		}
		if annualRate <= -99.99 {
			annualRate = -99.99
		}
		monthsFromNow := now.Sub(d).Hours() / (24 * 30.44)
		denom := math.Pow(1+annualRate/100, monthsFromNow/12)
		if denom <= 0 || math.IsNaN(denom) || math.IsInf(denom, 0) {
			cpiIndex[month] = 100.0
		} else {
			cpiIndex[month] = 100.0 / denom
		}
	}

	history := []inflPoint{}
	lastMonth := ""
	for i := len(snaps) - 1; i >= 0; i-- {
		s := snaps[i]
		month := s.Date.Format("2006-01")
		if month == lastMonth {
			continue
		}
		lastMonth = month
		nominal := 0.0
		if s.Total.Valid {
			f, _ := s.Total.Float64Value()
			nominal = f.Float64
		}
		cpi := cpiIndex[month]
		if cpi <= 0 || math.IsNaN(cpi) || math.IsInf(cpi, 0) {
			cpi = 100
		}
		real := nominal * (100.0 / cpi)
		history = append(history, inflPoint{
			Date:     month,
			Nominal:  math.Round(nominal),
			Real:     math.Round(real),
			CpiIndex: math.Round(cpi*10) / 10,
		})
	}

	if len(history) > 1 {
		last := history[len(history)-1]
		baseIdx := 0
		if len(history) > 12 {
			baseIdx = len(history) - 13
		}
		base := history[baseIdx]
		nominalReturn := 0.0
		realReturn := 0.0
		if base.Nominal > 1000 {
			nominalReturn = ((last.Nominal - base.Nominal) / base.Nominal) * 100
		}
		if base.Real > 1000 {
			realReturn = ((last.Real - base.Real) / base.Real) * 100
		}
		erosion := last.Nominal - last.Real

		writeJSON(w, http.StatusOK, map[string]any{
			"history":               history,
			"nominal_return":        math.Round(nominalReturn*10) / 10,
			"real_return":           math.Round(realReturn*10) / 10,
			"purchasing_power_lost": math.Round(erosion),
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"history": history})
}

// HandleBenchmarkComparison replays user's deposit history through a benchmark
// to show "what if I had bought benchmark instead?"
func (h *AnalysisHandler) HandleBenchmarkComparison(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	benchISIN := r.URL.Query().Get("isin")
	if benchISIN == "" {
		benchISIN = h.benchmarkISIN
		if benchISIN == "" {
			benchISIN = "IE00B4L5Y983" // default MSCI World
		}
	}

	fxRates := loadFXRatesMap(ctx, h.queries)
	histFX := loadHistoricalFXRates(ctx, h.queries)

	benchPrices, err := h.queries.ListPriceHistory(ctx, benchISIN)
	if err != nil || len(benchPrices) < benchmarkMinPrices {
		// Not an error: the benchmark simply has too little history yet. Answer 200 with a
		// machine-readable shape so the page can explain what is missing instead of failing.
		benchName := benchISIN
		if sec, secErr := h.queries.GetSecurity(ctx, benchISIN); secErr == nil && sec.Name != "" {
			benchName = sec.Name
		}
		have := 0
		if err == nil {
			have = len(benchPrices)
		}
		writeJSON(w, http.StatusOK, benchmarkInsufficientResponse(benchISIN, benchName, have))
		return
	}
	priceByDate := make(map[string]float64, len(benchPrices))
	for _, p := range benchPrices {
		if p.Close.Valid {
			f, _ := p.Close.Float64Value()
			eurP := convertAmountToEURAtDate(f.Float64, p.Currency, p.Date, histFX, fxRates)
			if eurP > 0 {
				priceByDate[p.Date.Format("2006-01-02")] = eurP
			}
		}
	}
	latestBenchPrice := 0.0
	lastRow := benchPrices[len(benchPrices)-1]
	if lastRow.Close.Valid {
		f, _ := lastRow.Close.Float64Value()
		latestBenchPrice = convertAmountToEUR(f.Float64, lastRow.Currency, fxRates)
	}

	closestPrice := func(date time.Time) float64 {
		for d := 0; d < 14; d++ {
			key := date.AddDate(0, 0, -d).Format("2006-01-02")
			if p, ok := priceByDate[key]; ok {
				return p
			}
		}
		return 0
	}

	txns, _ := listAllTransactionsRows(ctx, h.queries)
	accts, _ := h.queries.ListAccounts(ctx)
	txnsChrono := make([]db.ListTransactionsRow, len(txns))
	for i := range txns {
		txnsChrono[i] = txns[len(txns)-1-i]
	}
	impEvents := computeImplicitCashClampEvents(accts, txnsChrono, histFX, fxRates)

	type flowEvent struct {
		date time.Time
		amt  float64
	}
	var contribFlows []flowEvent
	for _, ev := range impEvents {
		if ev.DeltaEUR != 0 {
			contribFlows = append(contribFlows, flowEvent{date: ev.Date, amt: ev.DeltaEUR})
		}
	}
	for _, txn := range txnsChrono {
		switch txn.Type {
		case "deposit":
			if txn.Amount.Valid {
				f, _ := txn.Amount.Float64Value()
				amt := convertAmountToEURAtDate(f.Float64, txn.Currency, txn.Date, histFX, fxRates)
				if amt > 0 {
					contribFlows = append(contribFlows, flowEvent{date: txn.Date, amt: amt})
				}
			}
		case "withdrawal":
			if txn.Amount.Valid {
				f, _ := txn.Amount.Float64Value()
				amt := convertAmountToEURAtDate(f.Float64, txn.Currency, txn.Date, histFX, fxRates)
				if amt > 0 {
					contribFlows = append(contribFlows, flowEvent{date: txn.Date, amt: -amt})
				}
			}
		}
	}
	sort.SliceStable(contribFlows, func(i, j int) bool {
		if contribFlows[i].date.Equal(contribFlows[j].date) {
			return (contribFlows[i].amt > 0) && !(contribFlows[j].amt > 0)
		}
		return contribFlows[i].date.Before(contribFlows[j].date)
	})

	benchShares := 0.0
	for _, cf := range contribFlows {
		price := closestPrice(cf.date)
		if price > 0 {
			benchShares = math.Max(0, benchShares+cf.amt/price)
		}
	}
	benchValue := benchShares * latestBenchPrice

	type replayPoint struct {
		Date      string  `json:"date"`
		Actual    float64 `json:"actual"`
		Benchmark float64 `json:"benchmark"`
	}

	snaps, _ := h.queries.ListNetWorthSnapshots(ctx, 5000)
	snapByMonth := make(map[string]float64)
	for _, s := range snaps {
		m := s.Date.Format("2006-01")
		if _, exists := snapByMonth[m]; !exists && s.Total.Valid {
			f, _ := s.Total.Float64Value()
			snapByMonth[m] = f.Float64
		}
	}

	comparison := []replayPoint{}
	lastMonth := ""
	runningShares := 0.0
	cfIdx := 0
	for _, txn := range txnsChrono {
		for cfIdx < len(contribFlows) && !contribFlows[cfIdx].date.After(txn.Date) {
			price := closestPrice(contribFlows[cfIdx].date)
			if price > 0 {
				runningShares = math.Max(0, runningShares+contribFlows[cfIdx].amt/price)
			}
			cfIdx++
		}
		m := txn.Date.Format("2006-01")
		if m == lastMonth {
			continue
		}
		lastMonth = m
		bPrice := closestPrice(txn.Date)
		bVal := runningShares * bPrice
		actual := snapByMonth[m]
		if actual > 0 || bVal > 0 {
			comparison = append(comparison, replayPoint{
				Date: m, Actual: math.Round(actual), Benchmark: math.Round(bVal),
			})
		}
	}

	benchName := benchISIN
	if sec, err := h.queries.GetSecurity(ctx, benchISIN); err == nil {
		benchName = sec.Name
	}

	actualValue := 0.0
	if len(snaps) > 0 && snaps[0].Total.Valid {
		f, _ := snaps[0].Total.Float64Value()
		actualValue = f.Float64
	}
	diff := actualValue - benchValue

	writeJSON(w, http.StatusOK, map[string]any{
		"benchmark_name":  benchName,
		"benchmark_isin":  benchISIN,
		"actual_value":    math.Round(actualValue),
		"benchmark_value": math.Round(benchValue),
		"difference":      math.Round(diff),
		"comparison":      comparison,
		"available":       true,
		"have":            len(benchPrices),
		"need":            benchmarkMinPrices,
	})
}

// benchmarkMinPrices is the number of benchmark price rows needed before the
// "what if you had bought the benchmark" replay is meaningful.
const benchmarkMinPrices = 30

// benchmarkInsufficientResponse is the 200 body returned when the benchmark has fewer
// than benchmarkMinPrices price rows. It keeps every field of the normal response
// (zeroed, with an empty comparison array) and adds available/have/need/reason.
func benchmarkInsufficientResponse(isin, name string, have int) map[string]any {
	if have < 0 {
		have = 0
	}
	return map[string]any{
		"benchmark_name":  name,
		"benchmark_isin":  isin,
		"actual_value":    0.0,
		"benchmark_value": 0.0,
		"difference":      0.0,
		"comparison":      []any{},
		"available":       false,
		"reason":          "insufficient_data",
		"have":            have,
		"need":            benchmarkMinPrices,
	}
}

// HandleHealthScore computes a 0-100 portfolio health score from existing metrics.
func (h *AnalysisHandler) HandleHealthScore(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	enriched, err := h.loadEnrichedHoldings(ctx)
	if err != nil || len(enriched) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{"score": 0, "subscores": []any{}})
		return
	}

	type subScore struct {
		Name   string `json:"name"`
		Score  int    `json:"score"`
		Weight int    `json:"weight"`
		Status string `json:"status"`
		Detail string `json:"detail"`
	}

	// 1. Diversification (25%) — aggregate multi-account positions by ISIN
	valByISIN := make(map[string]float64)
	totalValue := 0.0
	for _, eh := range enriched {
		totalValue += eh.value
		valByISIN[eh.security.ISIN] += eh.value
	}
	maxWeight := 0.0
	if totalValue > 0 {
		for _, v := range valByISIN {
			w := v / totalValue * 100
			if w > maxWeight {
				maxWeight = w
			}
		}
	}
	numHoldings := len(valByISIN)
	divScore := 50
	if numHoldings >= 5 {
		divScore = 70
	}
	if numHoldings >= 8 {
		divScore = 85
	}
	if maxWeight < 40 {
		divScore += 15
	}
	if maxWeight < 25 {
		divScore += 10
	}
	if divScore > 100 {
		divScore = 100
	}
	divDetail := fmt.Sprintf("%d holdings, max weight %.0f%%", numHoldings, maxWeight)

	// 2. Cost Efficiency (15%) — based on weighted avg TER
	weightedTER := 0.0
	if totalValue > 0 {
		for _, eh := range enriched {
			ter := numericToFloat(eh.security.TER)
			if ter > 0 {
				weightedTER += (eh.value / totalValue) * ter
			}
		}
	}
	costScore := 90
	if weightedTER > 0.10 {
		costScore = 80
	}
	if weightedTER > 0.20 {
		costScore = 65
	}
	if weightedTER > 0.40 {
		costScore = 40
	}
	costDetail := fmt.Sprintf("Weighted TER %.2f%%", weightedTER)

	// 3. Risk Balance (20%) — based on cashflow-adjusted Sharpe ratio
	snaps, _ := h.queries.ListNetWorthSnapshots(ctx, 3000)
	points := make([]analytics.DailyValuation, 0, len(snaps))
	for i := len(snaps) - 1; i >= 0; i-- {
		val := 0.0
		if snaps[i].Total.Valid {
			f, _ := snaps[i].Total.Float64Value()
			val = f.Float64
		}
		if val > 0 {
			points = append(points, analytics.DailyValuation{Date: snaps[i].Date, Value: val})
		}
	}
	riskScore := 50
	riskDetail := "insufficient data"
	if len(points) > 60 {
		adjustedPoints := analytics.BuildCashflowAdjustedSeries(points, h.buildExternalFlowsForRisk(ctx))
		metrics := analytics.ComputeRiskMetrics(adjustedPoints, 0.03, points[len(points)-1].Value)
		sharpe := metrics.SharpeRatio
		if sharpe >= 1.0 {
			riskScore = 95
		} else if sharpe >= 0.5 {
			riskScore = 80
		} else if sharpe >= 0 {
			riskScore = 60
		} else {
			riskScore = 30
		}
		riskDetail = fmt.Sprintf("Sharpe %.2f, Vol %.1f%%", sharpe, metrics.AnnualizedVolatility)
	}

	// 4. Allocation Discipline (25%) — score based on max drift if targets set
	allocScore := 75 // default when no targets
	allocDetail := "no targets set"
	targets, _ := h.queries.ListTargetAllocations(ctx)
	if len(targets) > 0 && totalValue > 0 {
		maxDrift := 0.0
		for _, t := range targets {
			actual := valByISIN[t.SecurityISIN] / totalValue * 100
			drift := math.Abs(actual - numericToFloat(t.TargetWeightPct))
			if drift > maxDrift {
				maxDrift = drift
			}
		}
		allocScore = 95
		if maxDrift > 2 {
			allocScore = 80
		}
		if maxDrift > 5 {
			allocScore = 60
		}
		if maxDrift > 10 {
			allocScore = 35
		}
		allocDetail = fmt.Sprintf("max drift %.1f%%", maxDrift)
	}

	// 5. Income Stability (15%) — based on dividend consistency
	incScore := 70
	incDetail := "no dividend data"
	txns, _ := listAllTransactionsRows(ctx, h.queries)
	divMonths := make(map[string]bool)
	for _, txn := range txns {
		if txn.Type == "dividend" {
			divMonths[txn.Date.Format("2006-01")] = true
		}
	}
	if len(divMonths) > 0 {
		incScore = 50
		if len(divMonths) >= 6 { incScore = 70 }
		if len(divMonths) >= 12 { incScore = 85 }
		if len(divMonths) >= 24 { incScore = 95 }
		incDetail = fmt.Sprintf("%d months with dividends", len(divMonths))
	}

	scores := []subScore{
		{Name: "Diversification", Score: divScore, Weight: 25, Detail: divDetail},
		{Name: "Cost Efficiency", Score: costScore, Weight: 15, Detail: costDetail},
		{Name: "Risk Balance", Score: riskScore, Weight: 20, Detail: riskDetail},
		{Name: "Allocation", Score: allocScore, Weight: 25, Detail: allocDetail},
		{Name: "Income", Score: incScore, Weight: 15, Detail: incDetail},
	}

	total := 0
	for i := range scores {
		total += scores[i].Score * scores[i].Weight
		if scores[i].Score >= 80 { scores[i].Status = "good" } else if scores[i].Score >= 60 { scores[i].Status = "fair" } else { scores[i].Status = "poor" }
	}
	composite := total / 100

	writeJSON(w, http.StatusOK, map[string]any{
		"score":     composite,
		"subscores": scores,
	})
}

// HandleTaxLots returns FIFO lot inventory with per-lot tax impact, plus the
// tax of selling everything today netted per loss pot.
// Query: joint=1, church_tax=0.08|0.09.
func (h *AnalysisHandler) HandleTaxLots(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	opts := taxOptionsFromRequest(r)
	ledgerTxns, names, equityMap, prices, err := h.loadTaxLedgerInputs(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list transactions: "+err.Error())
		return
	}

	lots := analytics.ComputeTaxLots(ledgerTxns, prices, names, equityMap)

	// Filter out lots for ISINs no longer held (price=0 means sold/no market data)
	filtered := make([]analytics.TaxLot, 0, len(lots))
	for _, lot := range lots {
		if prices[lot.ISIN] > 0 {
			filtered = append(filtered, lot)
		}
	}

	now := time.Now()
	sellAll := analytics.SummarizeSellAll(filtered, now, opts.ChurchTaxRate)
	without, with := analytics.MarginalSaleTax(ledgerTxns, analytics.HypotheticalSellTxns(filtered, now), now.Year(), opts)
	sellAll.TaxThisYear = math.Round((with-without)*100) / 100

	writeJSON(w, http.StatusOK, map[string]any{"lots": filtered, "sell_all": sellAll})
}

// loadTaxLedgerInputs loads the chronological tax transactions with the
// Vorabpauschalen of completed years merged in, plus today's EUR unit prices.
func (h *AnalysisHandler) loadTaxLedgerInputs(ctx context.Context) ([]analytics.TaxTransaction, map[string]string, map[string]bool, map[string]float64, error) {
	txns, err := listAllTransactionsRows(ctx, h.queries)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	fxRates := loadFXRatesMap(ctx, h.queries)
	histFX := loadHistoricalFXRates(ctx, h.queries)
	secs, _ := h.queries.ListSecurities(ctx)
	secMap := make(map[string]db.Security, len(secs))
	for _, s := range secs {
		secMap[s.ISIN] = s
	}
	taxTxns, names, tfsMap, equityMap := buildTaxTransactions(txns, secs, histFX, fxRates)
	vpEvents := h.vorabpauschaleEvents(ctx, taxTxns, secMap, tfsMap, histFX, fxRates, 0, time.Now().Year()-1, nil)
	return analytics.MergeVorabpauschaleEvents(taxTxns, vpEvents), names, equityMap, h.currentUnitPricesEUR(ctx), nil
}

// HandleSellSimulator simulates the tax impact of selling specified EUR amounts.
// Query: joint=1, church_tax=0.08|0.09.
func (h *AnalysisHandler) HandleSellSimulator(w http.ResponseWriter, r *http.Request) {
	var requests []analytics.SellRequest
	if err := json.NewDecoder(r.Body).Decode(&requests); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	for _, req := range requests {
		if math.IsNaN(req.AmountEUR) || math.IsInf(req.AmountEUR, 0) || req.AmountEUR < 0 {
			writeError(w, http.StatusBadRequest, "sell amount must be a finite non-negative number")
			return
		}
	}

	ctx := r.Context()
	opts := taxOptionsFromRequest(r)
	ledgerTxns, names, equityMap, prices, err := h.loadTaxLedgerInputs(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list transactions: "+err.Error())
		return
	}

	allLots := analytics.ComputeTaxLots(ledgerTxns, prices, names, equityMap)
	var lots []analytics.TaxLot
	for _, lot := range allLots {
		if prices[lot.ISIN] > 0 {
			lots = append(lots, lot)
		}
	}

	now := time.Now()
	results, totalTax, totalProceeds := analytics.SimulateSellAt(lots, requests, opts.ChurchTaxRate, now)
	if results == nil {
		results = []analytics.SellSimResult{}
	}
	sold := 0.0
	for _, res := range results {
		sold += res.SellAmount
	}

	// The same sale priced through the full engine: this year's realised
	// income, both loss pots, carried losses and the remaining allowance.
	base := analytics.ComputeTaxSummaryWithOptions(ledgerTxns, now.Year(), nil, opts)
	withTxns := append(append(make([]analytics.TaxTransaction, 0, len(ledgerTxns)+len(requests)), ledgerTxns...), analytics.SimulatedSellTxns(lots, requests, now)...)
	withSale := analytics.ComputeTaxSummaryWithOptions(withTxns, now.Year(), nil, opts)
	taxThisYear := withSale.EstimatedTax - base.EstimatedTax

	writeJSON(w, http.StatusOK, map[string]any{
		"results":                results,
		"total_tax":              totalTax,
		"total_proceeds":         totalProceeds,
		"effective_rate":         math.Round(analytics.SellTaxRate(opts.ChurchTaxRate)*100000) / 100000,
		"church_tax_rate":        opts.ChurchTaxRate,
		"tax_this_year":          math.Round(taxThisYear*100) / 100,
		"net_proceeds_this_year": math.Round((sold-taxThisYear)*100) / 100,
		"allowance_remaining":    base.FreistellungRemain,
		"allowance":              base.Allowance,
	})
}

// HandleLossPots returns the German Aktienverlusttopf / allgemeiner Verlusttopf per year.
// Query: church_tax=0.08|0.09 (rate for the cross-broker saving).
func (h *AnalysisHandler) HandleLossPots(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	opts := taxOptionsFromRequest(r)
	ledgerTxns, _, _, _, err := h.loadTaxLedgerInputs(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list transactions: "+err.Error())
		return
	}

	pots := analytics.ComputeLossPots(ledgerTxns)
	if pots == nil {
		pots = []analytics.LossPotYear{}
	}

	// Per-account loss pots for current year (cross-broker netting, per-depot FIFO)
	accts, _ := h.queries.ListAccounts(ctx)
	acctNames := make(map[string]string)
	for _, a := range accts {
		acctNames[a.ID.String()] = a.Name
	}

	currentYear := time.Now().Year()
	type acctPot struct {
		Account       string  `json:"account"`
		EquityGains   float64 `json:"equity_gains"`
		EquityLosses  float64 `json:"equity_losses"`
		GeneralGains  float64 `json:"general_gains"`
		GeneralLosses float64 `json:"general_losses"`
	}
	acctPots := make(map[string]*acctPot)
	var acctOrder []string
	get := func(id string) *acctPot {
		ap, ok := acctPots[id]
		if !ok {
			ap = &acctPot{Account: acctNames[id]}
			acctPots[id] = ap
			acctOrder = append(acctOrder, id)
		}
		return ap
	}

	// Same engine as the summary: share pot = stock sales only, Teilfreistellung
	// on fund gains and losses, in-kind transfers keep their lots, §23 assets
	// (crypto, physical gold) excluded.
	sales, income := analytics.RealizedTaxEvents(ledgerTxns)
	for _, s := range sales {
		if s.Year != currentYear || s.PrivateSale {
			continue
		}
		ap := get(s.AccountID)
		pl := s.TaxablePL()
		switch {
		case analytics.IsShareAssetClass(s.AssetClass) && pl >= 0:
			ap.EquityGains += pl
		case analytics.IsShareAssetClass(s.AssetClass):
			ap.EquityLosses += pl
		case pl >= 0:
			ap.GeneralGains += pl
		default:
			ap.GeneralLosses += pl
		}
	}
	for _, e := range income {
		if e.Year != currentYear || e.PrivateSale {
			continue
		}
		get(e.AccountID).GeneralGains += e.TaxableAmount()
	}

	perAccount := []acctPot{}
	for _, id := range acctOrder {
		ap := acctPots[id]
		ap.EquityGains = math.Round(ap.EquityGains*100) / 100
		ap.EquityLosses = math.Round(ap.EquityLosses*100) / 100
		ap.GeneralGains = math.Round(ap.GeneralGains*100) / 100
		ap.GeneralLosses = math.Round(ap.GeneralLosses*100) / 100
		perAccount = append(perAccount, *ap)
	}

	// Each bank nets its own pots (general losses may offset share gains,
	// not the reverse). What is left over — losses at one bank, taxed income
	// at another — can only be netted in the tax return (Anlage KAP).
	shareIncome, generalIncome, shareLoss, generalLoss := 0.0, 0.0, 0.0, 0.0
	for _, ap := range perAccount {
		s := ap.EquityGains + ap.EquityLosses
		g := ap.GeneralGains + ap.GeneralLosses
		if s > 0 && g < 0 {
			o := math.Min(s, -g)
			s -= o
			g += o
		}
		shareIncome += math.Max(0, s)
		generalIncome += math.Max(0, g)
		shareLoss += math.Min(0, s)
		generalLoss += math.Min(0, g)
	}
	crossBrokerSaving := 0.0
	if len(perAccount) > 1 {
		offShare := math.Min(-shareLoss, shareIncome)
		offGeneral := math.Min(-generalLoss, shareIncome-offShare+generalIncome)
		crossBrokerSaving = (offShare + offGeneral) * analytics.CapitalIncomeTaxRate(opts.ChurchTaxRate)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"years":               pots,
		"per_account":         perAccount,
		"cross_broker_saving": math.Round(crossBrokerSaving*100) / 100,
		"file_anlage_kap":     crossBrokerSaving > 0,
	})
}

// HandleFSAStatus returns Freistellungsauftrag (Sparerpauschbetrag) usage per account.
// Accepts ?joint=1 to double the allowance for married couples filing jointly
// (Zusammenveranlagung), which raises the Sparerpauschbetrag to 2000 EUR.
func (h *AnalysisHandler) HandleFSAStatus(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	year := time.Now().Year()
	if y := r.URL.Query().Get("year"); y != "" {
		if v, err := strconv.Atoi(y); err == nil {
			year = v
		}
	}
	allowance := analytics.Sparerpauschbetrag
	joint := r.URL.Query().Get("joint") == "1"
	if joint {
		allowance *= 2
	}

	txns, _ := listAllTransactionsRows(ctx, h.queries)
	fxRates := loadFXRatesMap(ctx, h.queries)
	histFX := loadHistoricalFXRates(ctx, h.queries)
	accts, _ := h.queries.ListAccounts(ctx)
	secs, _ := h.queries.ListSecurities(ctx)
	taxTxns, _, tfsMap, _ := buildTaxTransactions(txns, secs, histFX, fxRates)

	acctNames := make(map[string]string)
	for _, a := range accts {
		acctNames[a.ID.String()] = a.Name
	}

	type acctIncome struct {
		AccountID   string  `json:"account_id"`
		AccountName string  `json:"account_name"`
		Dividends   float64 `json:"dividends"`
		Interest    float64 `json:"interest"`
		Gains       float64 `json:"realized_gains"`
		Total       float64 `json:"total_income"`
	}
	incomeByAcct := make(map[string]*acctIncome)
	getAcct := func(id string) *acctIncome {
		if a, ok := incomeByAcct[id]; ok {
			return a
		}
		a := &acctIncome{AccountID: id, AccountName: acctNames[id]}
		if a.AccountName == "" {
			a.AccountName = "Unknown"
		}
		incomeByAcct[id] = a
		return a
	}

	type depotKey struct {
		acctID string
		isin   string
	}
	type fifoLot struct {
		qty     float64
		costPer float64
	}
	lots := make(map[depotKey][]fifoLot)
	consumeFIFO := func(k depotKey, qty float64) (soldQty, costBasis float64) {
		rem := qty
		q := lots[k]
		for rem > 1e-9 && len(q) > 0 {
			if q[0].qty <= rem+1e-9 {
				soldQty += q[0].qty
				costBasis += q[0].qty * q[0].costPer
				rem -= q[0].qty
				q = q[1:]
			} else {
				soldQty += rem
				costBasis += rem * q[0].costPer
				q[0].qty -= rem
				rem = 0
			}
		}
		lots[k] = q
		return soldQty, costBasis
	}

	for _, txn := range taxTxns { // already chronological
		if txn.Year > year {
			continue
		}
		dk := depotKey{acctID: txn.AccountID, isin: txn.ISIN}

		switch txn.Type {
		case "buy", "savings_plan", "transfer":
			if txn.ISIN == "" || txn.Quantity <= 0 {
				continue
			}
			lots[dk] = append(lots[dk], fifoLot{qty: txn.Quantity, costPer: txn.Amount / txn.Quantity})

		case "transfer_out":
			if txn.ISIN == "" || txn.Quantity <= 0 {
				continue
			}
			consumeFIFO(dk, txn.Quantity)

		case "sell":
			if txn.ISIN == "" || txn.Quantity <= 0 {
				continue
			}
			// Always consume prior-year sells so sold shares don't inflate future basis!
			soldQty, costBasis := consumeFIFO(dk, txn.Quantity)
			if txn.Year != year || soldQty <= 1e-9 {
				continue
			}
			proceeds := txn.Amount
			if soldQty < txn.Quantity {
				proceeds = txn.Amount * (soldQty / txn.Quantity)
			}
			gain := proceeds - costBasis
			if gain > 0 {
				gain *= (1 - tfsMap[txn.ISIN])
				getAcct(txn.AccountID).Gains += gain
			}

		case "dividend":
			if txn.Year != year {
				continue
			}
			divAmt := txn.Amount * (1 - tfsMap[txn.ISIN])
			getAcct(txn.AccountID).Dividends += divAmt

		case "interest":
			if txn.Year != year {
				continue
			}
			getAcct(txn.AccountID).Interest += txn.Amount
		}
	}

	accounts := []acctIncome{}
	totalIncome := 0.0
	for _, a := range incomeByAcct {
		a.Total = math.Round((a.Dividends+a.Interest+a.Gains)*100) / 100
		a.Dividends = math.Round(a.Dividends*100) / 100
		a.Interest = math.Round(a.Interest*100) / 100
		a.Gains = math.Round(a.Gains*100) / 100
		totalIncome += a.Total
		accounts = append(accounts, *a)
	}
	sort.Slice(accounts, func(i, j int) bool { return accounts[i].Total > accounts[j].Total })

	used := math.Min(totalIncome, allowance)
	remaining := allowance - used

	// Recommendation: optimal FSA split for next year based on projected income
	type recommendation struct {
		Account       string  `json:"account"`
		ProjectedIncome float64 `json:"projected_income"`
		RecommendedFSA float64 `json:"recommended_fsa"`
	}
	recs := []recommendation{}
	if len(accounts) > 0 {
		// Annualize current YTD income to project full year
		monthsElapsed := float64(time.Now().Month())
		if monthsElapsed < 1 { monthsElapsed = 1 }
		for _, a := range accounts {
			projected := a.Total / monthsElapsed * 12
			recs = append(recs, recommendation{
				Account: a.AccountName, ProjectedIncome: math.Round(projected*100) / 100,
			})
		}
		// Allocate FSA proportionally to projected income
		totalProjected := 0.0
		for _, r := range recs { totalProjected += r.ProjectedIncome }
		for i := range recs {
			if totalProjected > 0 {
				recs[i].RecommendedFSA = math.Round(allowance * recs[i].ProjectedIncome / totalProjected)
			}
		}
	}

	utilization := 0.0
	if allowance > 0 {
		utilization = math.Round(used/allowance*10000) / 100
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"year":            year,
		"accounts":        accounts,
		"total_income":    math.Round(totalIncome*100) / 100,
		"allowance":       allowance,
		"joint":           joint,
		"used":            math.Round(used*100) / 100,
		"remaining":       math.Round(remaining*100) / 100,
		"utilization_pct": utilization,
		"recommendation":  recs,
	})
}

// HandleAlternatives returns cheaper ETF alternatives for current holdings.
func (h *AnalysisHandler) HandleAlternatives(w http.ResponseWriter, r *http.Request) {
	enriched, err := h.loadEnrichedHoldings(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "load holdings: "+err.Error())
		return
	}

	// Load static alternatives data
	var altMap map[string]struct {
		Name         string `json:"name"`
		Index        string `json:"index"`
		TER          float64 `json:"ter"`
		Alternatives []struct {
			ISIN  string  `json:"isin"`
			Name  string  `json:"name"`
			TER   float64 `json:"ter"`
			Index string  `json:"index"`
		} `json:"alternatives"`
	}
	if err := json.Unmarshal(data.ETFAlternativesJSON, &altMap); err != nil {
		writeError(w, http.StatusInternalServerError, "parse alternatives: "+err.Error())
		return
	}

	type altResult struct {
		ISIN        string  `json:"isin"`
		Name        string  `json:"name"`
		CurrentTER  float64 `json:"current_ter"`
		Value       float64 `json:"value"`
		Alternatives []struct {
			ISIN          string  `json:"isin"`
			Name          string  `json:"name"`
			TER           float64 `json:"ter"`
			AnnualSaving  float64 `json:"annual_saving"`
			TenYearSaving float64 `json:"ten_year_saving"`
		} `json:"alternatives"`
	}

	results := []altResult{}
	for _, eh := range enriched {
		alt, ok := altMap[eh.security.ISIN]
		if !ok || len(alt.Alternatives) == 0 {
			continue
		}
		// Prefer database TER (from metadata fetcher) over stale static JSON
		currentTER := numericToFloat(eh.security.TER)
		if currentTER <= 0 {
			currentTER = alt.TER
		}
		r := altResult{
			ISIN: eh.security.ISIN, Name: eh.security.Name,
			CurrentTER: currentTER, Value: eh.value,
		}
		for _, a := range alt.Alternatives {
			if a.TER >= currentTER {
				continue // only show cheaper alternatives
			}
			saving := eh.value * (currentTER - a.TER) / 100
			r.Alternatives = append(r.Alternatives, struct {
				ISIN          string  `json:"isin"`
				Name          string  `json:"name"`
				TER           float64 `json:"ter"`
				AnnualSaving  float64 `json:"annual_saving"`
				TenYearSaving float64 `json:"ten_year_saving"`
			}{
				ISIN: a.ISIN, Name: a.Name, TER: a.TER,
				AnnualSaving: math.Round(saving*100) / 100,
				TenYearSaving: math.Round(saving*10*100) / 100,
			})
		}
		if len(r.Alternatives) > 0 {
			results = append(results, r)
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{"alternatives": results})
}

// HandleSpending categorizes transactions and computes spending analytics.
func (h *AnalysisHandler) HandleSpending(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	txns, err := listAllTransactionsRows(ctx, h.queries)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list transactions: "+err.Error())
		return
	}
	holdings, _ := assets.Load(ctx, h.queries)
	fxRates := loadFXRatesMap(ctx, h.queries)
	histFX := loadHistoricalFXRates(ctx, h.queries)

	// Category patterns: match counterparty or reference
	type pattern struct {
		category string
		keywords []string
	}
	patterns := []pattern{
		{"Housing", []string{"miete", "rent", "wohnung", "hausgeld", "nebenkosten", "strom", "gas", "heizung", "stadtwerke", "wasser", "gez", "rundfunk"}},
		{"Insurance", []string{"versicherung", "insurance", "allianz", "huk", "axa", "ergo", "debeka", "krankenversicherung", "haftpflicht", "berufsunfähigkeit"}},
		{"Subscriptions", []string{"spotify", "netflix", "amazon prime", "disney", "youtube", "apple", "google storage", "cloud", "abo", "mitgliedschaft", "fitnessstudio", "gym"}},
		{"Groceries", []string{"rewe", "edeka", "aldi", "lidl", "penny", "netto", "kaufland", "dm ", "rossmann", "supermarkt", "lebensmittel"}},
		{"Transport", []string{"tankstelle", "aral", "shell", "total", "db ", "bahn", "bvg", "mvv", "hvv", "swb", "tier", "uber", "taxi", "kfz", "tüv", "adac"}},
		{"Dining", []string{"restaurant", "gaststätte", "lieferando", "lieferservice", "pizz", "burger", "sushi", "café", "cafe", "bistro", "imbiss", "mcdonald", "starbuck"}},
		{"Shopping", []string{"amazon", "zalando", "otto", "ebay", "mediamarkt", "saturn", "ikea", "h&m", "zara", "deichmann"}},
		{"Health", []string{"apotheke", "arzt", "praxis", "labor", "krankenhaus", "physio", "zahnarzt", "optiker"}},
		{"Education", []string{"schule", "universität", "studium", "kurs", "seminar", "vhs", "bücher", "udemy"}},
		{"Investment", []string{"sparplan", "depot", "wertpapier", "etf", "aktie", "fonds", "scalable", "trade republic", "comdirect", "dkb"}},
		{"Transfer", []string{"umbuchung", "übertrag", "migration", "internal transfer", "transfer"}},
		{"Income", []string{"gehalt", "lohn", "salary", "bonus", "gutschrift", "erstattung", "rückzahlung", "kindergeld"}},
	}

	categorize := func(counterparty, reference, txnType string) string {
		text := strings.ToLower(counterparty + " " + reference)
		for _, p := range patterns {
			for _, kw := range p.keywords {
				if strings.Contains(text, kw) {
					return p.category
				}
			}
		}
		// Fallback by transaction type
		switch txnType {
		case "buy", "savings_plan":
			return "Investment"
		case "sell":
			return "Investment"
		case "dividend":
			return "Dividends"
		case "interest":
			return "Interest"
		case "fee":
			return "Fees"
		case "deposit":
			return "Income"
		case "withdrawal":
			return "Transfer"
		case "transfer", "transfer_out":
			return "Transfer"
		}
		return "Other"
	}

	// Process transactions
	type monthlySpend struct {
		Month    string             `json:"month"`
		Income   float64            `json:"income"`
		Expenses float64            `json:"expenses"`
		Net      float64            `json:"net"`
		ByCategory map[string]float64 `json:"by_category"`
	}
	type categoryTotal struct {
		Category string  `json:"category"`
		Total    float64 `json:"total"`
		Count    int     `json:"count"`
		AvgMonthly float64 `json:"avg_monthly"`
	}

	monthMap := make(map[string]*monthlySpend)
	catTotals := make(map[string]*categoryTotal)
	allCategories := make(map[string]bool)

	// Track recurring charges for subscription detection
	type chargeKey struct {
		counterparty string
		amount       float64
	}
	chargeFreq := make(map[chargeKey]int)

	for _, txn := range txns {
		if isAssetPurchase(txn.Category) {
			continue // cash → physical asset, not an expense
		}
		cp := ""
		if txn.Counterparty.Valid { cp = txn.Counterparty.String }
		ref := ""
		if txn.Reference.Valid { ref = txn.Reference.String }
		amt := 0.0
		if txn.Amount.Valid {
			f, _ := txn.Amount.Float64Value()
			amt = convertAmountToEURAtDate(f.Float64, txn.Currency, txn.Date, histFX, fxRates)
		}

		cat := categorize(cp, ref, txn.Type)
		// Use user-assigned category if set
		if txn.Category.Valid && txn.Category.String != "" {
			cat = txn.Category.String
		}
		// Loan instalment: only the interest is spending; the principal is saved.
		if isLoanPayment(txn.Category) {
			interest, _, kind, ok := holdings.PaymentSplit(txn.ID)
			if !ok {
				continue
			}
			amt = math.Copysign(interest, amt)
			cat = "Housing"
			if kind == assets.KindCar {
				cat = "Transport"
			}
		}
		allCategories[cat] = true

		month := txn.Date.Format("2006-01")
		ms, ok := monthMap[month]
		if !ok {
			ms = &monthlySpend{Month: month, ByCategory: make(map[string]float64)}
			monthMap[month] = ms
		}

		// For spending analysis, focus on cash flow (not investment transactions)
		// Classify: withdrawals from brokerage accounts are transfers, not expenses
		// Same for deposits that are internal transfers (Sparplan, Umbuchung, Migration)
		// Cash transfers between accounts are never income or expenses
		if txn.Type == "cash_transfer_in" || txn.Type == "cash_transfer_out" {
			continue
		}
		isBrokerageTransfer := (txn.Type == "withdrawal" || txn.Type == "deposit") &&
			(strings.Contains(strings.ToLower(cp), "broker") || strings.Contains(strings.ToLower(cp), "scalable") ||
				strings.Contains(strings.ToLower(cp), "trade republic") || strings.Contains(strings.ToLower(cp), "sparplan") ||
				strings.Contains(strings.ToLower(cp), "umbuchung") || strings.Contains(strings.ToLower(cp), "migration") ||
				strings.Contains(strings.ToLower(cp), "internal transfer") || strings.Contains(strings.ToLower(cp), "übertrag"))
		isExpense := (txn.Type == "fee") ||
			(txn.Type == "withdrawal" && !isBrokerageTransfer) ||
			(amt < 0 && txn.Type != "buy" && txn.Type != "savings_plan" && txn.Type != "transfer" && txn.Type != "transfer_out" && txn.Type != "withdrawal")
		isIncome := (txn.Type == "dividend" || txn.Type == "interest") ||
			(txn.Type == "deposit" && !isBrokerageTransfer) ||
			(amt > 0 && txn.Type != "sell" && txn.Type != "transfer" && txn.Type != "transfer_out" && txn.Type != "deposit" && txn.Type != "dividend" && txn.Type != "interest")

		if isExpense {
			expAmt := math.Abs(amt)
			ms.Expenses += expAmt
			ms.ByCategory[cat] += expAmt
			ct := catTotals[cat]
			if ct == nil { ct = &categoryTotal{Category: cat}; catTotals[cat] = ct }
			ct.Total += expAmt
			ct.Count++
		} else if isIncome && cat != "Investment" && cat != "Transfer" {
			ms.Income += amt
		}

		// Track for subscription detection (exclude investment/dividend/transfer types)
		if cp != "" && amt != 0 && txn.Type != "buy" && txn.Type != "sell" && txn.Type != "savings_plan" &&
			txn.Type != "dividend" && txn.Type != "transfer" && txn.Type != "transfer_out" && txn.Type != "fee" {
			key := chargeKey{counterparty: strings.ToLower(cp), amount: math.Round(math.Abs(amt)*100) / 100}
			chargeFreq[key]++
		}
	}

	// Build monthly series (sorted chronologically)
	var months []string
	for m := range monthMap {
		months = append(months, m)
	}
	sort.Strings(months)

	monthlySeries := []monthlySpend{}
	for _, m := range months {
		ms := monthMap[m]
		ms.Net = ms.Income - ms.Expenses
		monthlySeries = append(monthlySeries, *ms)
	}

	// Build category totals
	numMonths := float64(len(months))
	if numMonths < 1 { numMonths = 1 }
	categories := []categoryTotal{}
	for _, ct := range catTotals {
		ct.AvgMonthly = math.Round(ct.Total/numMonths*100) / 100
		ct.Total = math.Round(ct.Total*100) / 100
		categories = append(categories, *ct)
	}
	sort.Slice(categories, func(i, j int) bool { return categories[i].Total > categories[j].Total })

	// Detect subscriptions: same counterparty + same amount appearing 3+ times
	type subscription struct {
		Name       string  `json:"name"`
		Amount     float64 `json:"amount"`
		Frequency  int     `json:"occurrences"`
		AnnualCost float64 `json:"annual_cost"`
	}
	subscriptions := []subscription{}
	for key, freq := range chargeFreq {
		if freq >= 3 && key.amount > 1 && key.amount < 500 {
			annual := key.amount * 12 // assume monthly
			if freq < 8 { annual = key.amount * float64(freq) } // use actual frequency if < monthly
			subscriptions = append(subscriptions, subscription{
				Name: key.counterparty, Amount: key.amount,
				Frequency: freq, AnnualCost: math.Round(annual*100) / 100,
			})
		}
	}
	sort.Slice(subscriptions, func(i, j int) bool { return subscriptions[i].AnnualCost > subscriptions[j].AnnualCost })

	// Compute totals — lifetime (across all months_analyzed) AND a trailing
	// 12-month window so seasonality and old high-spend years don't drag the
	// headline savings rate down. Both fields surface; frontend headline uses
	// the windowed one for recency, with lifetime available as a sub-line.
	totalExpenses := 0.0
	totalIncome := 0.0
	for _, ms := range monthlySeries {
		totalExpenses += ms.Expenses
		totalIncome += ms.Income
	}
	savingsRate := 0.0
	if totalIncome > 0 {
		savingsRate = (totalIncome - totalExpenses) / totalIncome * 100
	}

	// Trailing 12 months — slice the chronologically-sorted monthlySeries.
	windowStart := len(monthlySeries) - 12
	if windowStart < 0 {
		windowStart = 0
	}
	windowExpenses := 0.0
	windowIncome := 0.0
	for _, ms := range monthlySeries[windowStart:] {
		windowExpenses += ms.Expenses
		windowIncome += ms.Income
	}
	savingsRate12m := 0.0
	if windowIncome > 0 {
		savingsRate12m = (windowIncome - windowExpenses) / windowIncome * 100
	}
	windowMonths := len(monthlySeries) - windowStart

	writeJSON(w, http.StatusOK, map[string]any{
		"monthly":             monthlySeries,
		"categories":          categories,
		"subscriptions":       subscriptions,
		"total_income":        math.Round(totalIncome*100) / 100,
		"total_expenses":      math.Round(totalExpenses*100) / 100,
		"savings_rate_pct":    math.Round(savingsRate*10) / 10,
		"savings_rate_pct_12m": math.Round(savingsRate12m*10) / 10,
		"window_months":       windowMonths,
		"avg_monthly_expense": math.Round(totalExpenses/numMonths*100) / 100,
		"months_analyzed":     len(months),
	})
}

// taxCalendarFSATitle is a plain string constant (not a format string), so the percent
// sign is written once. FSA = Freistellungsauftrag, the tax exemption order filed with a broker.
const taxCalendarFSATitle = "Freistellungsauftrag (tax exemption order) use below 80 %"

// HandleTaxCalendar generates a 12-month German tax compliance calendar
// with auto-computed amounts based on portfolio data.
func (h *AnalysisHandler) HandleTaxCalendar(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	now := time.Now().UTC()
	year := now.Year()

	type calendarEvent struct {
		Month       int     `json:"month"` // 1-12
		Date        string  `json:"date"`  // YYYY-MM-DD or YYYY-MM
		Title       string  `json:"title"`
		Description string  `json:"description"`
		Amount      float64 `json:"amount,omitempty"` // EUR amount if applicable
		Category    string  `json:"category"`         // vorabpauschale, steuererklaerung, fsa, verlust, harvest, schenkung
		Urgency     string  `json:"urgency"`          // info, action, deadline
		ActionURL   string  `json:"action_url,omitempty"`
	}
	var events []calendarEvent

	// Load data for amount computation
	enriched, _ := h.loadEnrichedHoldings(ctx)
	txns, _ := listAllTransactionsRows(ctx, h.queries)
	secs, _ := h.queries.ListSecurities(ctx)
	fxRates := loadFXRatesMap(ctx, h.queries)
	histFX := loadHistoricalFXRates(ctx, h.queries)
	secMap := make(map[string]db.Security, len(secs))
	for _, s := range secs {
		secMap[s.ISIN] = s
	}
	taxTxns, names, tfsMap, equityMap := buildTaxTransactions(txns, secs, histFX, fxRates)

	valByISIN := make(map[string]float64)
	qtyByISIN := make(map[string]float64)
	for _, eh := range enriched {
		if eh.value > 0 {
			valByISIN[eh.security.ISIN] += eh.value
			qtyByISIN[eh.security.ISIN] += numericToFloat(eh.holding.Quantity)
		}
	}

	// --- 1. Vorabpauschale (Jan 2) ---
	// Jan 2 of `year` settles the Vorabpauschale for `year - 1` (§ 18 Abs. 3 InvStG).
	vpEntries := h.computeYearVorabpauschale(ctx, year-1, txns, secMap, tfsMap, valByISIN, qtyByISIN, histFX, fxRates)
	etfValueTotal := 0.0
	vpTax := 0.0
	for _, vp := range vpEntries {
		etfValueTotal += vp.YearEndValue
		vpTax += vp.TaxOnVP
	}
	if vpTax > 10 {
		events = append(events, calendarEvent{
			Month: 1, Date: fmt.Sprintf("%d-01-02", year),
			Title:       "Vorabpauschale (advance flat-rate fund tax) due",
			Description: fmt.Sprintf("Tax on the %d Basisertrag (base return) of %s € in fund holdings. Your broker's settlement account must hold enough cash.", year-1, fmtEUR(etfValueTotal)),
			Amount:      math.Round(vpTax*100) / 100,
			Category:    "vorabpauschale", Urgency: "deadline",
			ActionURL: "/analysis",
		})
	}

	// --- 2. Freistellungsauftrag Review (Jan) ---
	// Check if current FSA allocation is optimal
	events = append(events, calendarEvent{
		Month: 1, Date: fmt.Sprintf("%d-01-15", year),
		Title:       "Review your Freistellungsauftrag (tax exemption order)",
		Description: "Check that the 1.000 € Sparer-Pauschbetrag (saver's allowance) is split sensibly across your brokers.",
		Amount:      analytics.Sparerpauschbetrag,
		Category:    "fsa", Urgency: "action",
		ActionURL: "/analysis",
	})

	// --- 3. Steuererklärung deadline (Jul 31 or Sep 30 with Steuerberater) ---
	events = append(events, calendarEvent{
		Month: 7, Date: fmt.Sprintf("%d-07-31", year),
		Title:       fmt.Sprintf("Steuererklärung (income tax return) %d due", year-1),
		Description: fmt.Sprintf("File your %d income tax return. With a Steuerberater (tax adviser) the deadline is 28.02.%d.", year-1, year+1),
		Category:    "steuererklaerung", Urgency: "deadline",
		ActionURL: "/analysis",
	})

	// --- 4. Verlustbescheinigung beantragen (Nov 15-Dec 15) ---
	events = append(events, calendarEvent{
		Month: 12, Date: fmt.Sprintf("%d-12-15", year),
		Title:       "Request a Verlustbescheinigung (loss certificate)",
		Description: "Ask your bank for a loss certificate for the tax return. The deadline is 15 December.",
		Category:    "verlust", Urgency: "deadline",
	})

	// --- 5. Tax-Loss Harvesting Window (Q4) ---
	// Identify positions with unrealized losses > 500 EUR
	prices := make(map[string]float64, len(valByISIN))
	for isin, val := range valByISIN {
		if qty := qtyByISIN[isin]; qty > 0 {
			prices[isin] = val / qty
		}
	}
	lots := analytics.ComputeTaxLots(taxTxns, prices, names, equityMap)

	// Sum unrealized losses per ISIN
	type lossPos struct {
		name string
		loss float64
	}
	lossByISIN := make(map[string]*lossPos)
	for _, lot := range lots {
		if lot.UnrealizedPL < 0 && prices[lot.ISIN] > 0 {
			lp := lossByISIN[lot.ISIN]
			if lp == nil {
				lp = &lossPos{name: lot.Name}
				lossByISIN[lot.ISIN] = lp
			}
			lp.loss += lot.UnrealizedPL
		}
	}
	totalHarvestable := 0.0
	saving := 0.0
	for isin, lp := range lossByISIN {
		if lp.loss < -500 {
			absLoss := math.Abs(lp.loss)
			totalHarvestable += absLoss
			saving += absLoss * (1 - tfsMap[isin]) * analytics.EffectiveTaxRate
		}
	}
	if totalHarvestable > 500 {
		events = append(events, calendarEvent{
			Month: 10, Date: fmt.Sprintf("%d-10-01", year),
			Title:       "Tax-loss harvesting window",
			Description: fmt.Sprintf("%s € of losses could be realised. Potential tax saving: about %s €.", fmtEUR(totalHarvestable), fmtEUR(saving)),
			Amount:      math.Round(saving),
			Category:    "harvest", Urgency: "action",
			ActionURL: "/analysis",
		})
	}

	// --- 6. FSA Mid-Year Check (Jul) ---
	// Check YTD taxable income vs FSA usage
	ytdIncome := 0.0
	for _, txn := range txns {
		if txn.Date.Year() != year {
			continue
		}
		if txn.Type == "dividend" || txn.Type == "interest" {
			if txn.Amount.Valid {
				f, _ := txn.Amount.Float64Value()
				amtEUR := convertAmountToEURAtDate(f.Float64, txn.Currency, txn.Date, histFX, fxRates)
				if txn.Type == "dividend" && txn.SecurityISIN.Valid {
					amtEUR *= 1 - tfsMap[txn.SecurityISIN.String]
				}
				ytdIncome += amtEUR
			}
		}
	}
	monthsElapsed := float64(now.Month())
	if monthsElapsed >= 6 {
		projected := ytdIncome / monthsElapsed * 12
		if projected < analytics.Sparerpauschbetrag*0.8 {
			unused := analytics.Sparerpauschbetrag - projected
			events = append(events, calendarEvent{
				Month: 7, Date: fmt.Sprintf("%d-07-01", year),
				Title:       taxCalendarFSATitle,
				Description: fmt.Sprintf("On course to use only %s € of the 1.000 € Sparer-Pauschbetrag (saver's allowance). About %s € would go unused.", fmtEUR(projected), fmtEUR(unused)),
				Amount:      math.Round(unused * analytics.EffectiveTaxRate),
				Category:    "fsa", Urgency: "action",
				ActionURL: "/analysis",
			})
		}
	}

	// --- 7. Schenkung Window (Year-round) ---
	events = append(events, calendarEvent{
		Month: 3, Date: fmt.Sprintf("%d-03-01", year),
		Title:       "Review the Schenkungsfreibetrag (gift tax allowance)",
		Description: "Every 10 years you can give tax-free: 400.000 € per child, 500.000 € to a spouse, 200.000 € per grandchild. Regular gifts make full use of it.",
		Category:    "schenkung", Urgency: "info",
	})

	// --- 8. Year-End Portfolio Review (Dec) ---
	events = append(events, calendarEvent{
		Month: 12, Date: fmt.Sprintf("%d-12-01", year),
		Title:       "Year-end portfolio review",
		Description: "Rebalance, adjust your Sparplan (savings plan) and prepare Anlage KAP (the capital income tax form) for the tax year.",
		Category:    "steuererklaerung", Urgency: "action",
		ActionURL: "/portfolio",
	})

	// Sort by month
	sort.Slice(events, func(i, j int) bool {
		if events[i].Month != events[j].Month {
			return events[i].Month < events[j].Month
		}
		return events[i].Date < events[j].Date
	})

	// Highlight upcoming events (next 30 days)
	upcoming := 0
	for _, ev := range events {
		evDate, err := time.Parse("2006-01-02", ev.Date)
		if err != nil {
			continue
		}
		daysUntil := int(evDate.Sub(now).Hours() / 24)
		if daysUntil >= -7 && daysUntil <= 30 {
			upcoming++
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"year":     year,
		"events":   events,
		"upcoming": upcoming,
	})
}

// HandleVolatilityContext provides factual context during elevated volatility.
func (h *AnalysisHandler) HandleVolatilityContext(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	snaps, err := h.queries.ListNetWorthSnapshots(ctx, 3000)
	if err != nil || len(snaps) < 31 {
		writeJSON(w, http.StatusOK, map[string]any{"elevated": false})
		return
	}

	// Build chronological points
	points := make([]analytics.DailyValuation, len(snaps))
	for i, s := range snaps {
		val := 0.0
		if s.Total.Valid {
			f, _ := s.Total.Float64Value()
			val = f.Float64
		}
		points[len(snaps)-1-i] = analytics.DailyValuation{Date: s.Date, Value: val}
	}
	start := 0
	for start < len(points) && points[start].Value <= 0 {
		start++
	}
	points = points[start:]
	points = analytics.BuildCashflowAdjustedSeries(points, h.buildExternalFlowsForRisk(ctx))
	if len(points) < 31 {
		writeJSON(w, http.StatusOK, map[string]any{"elevated": false})
		return
	}

	current := points[len(points)-1].Value
	peak := 0.0
	peakDate := ""
	for _, p := range points {
		if p.Value > peak {
			peak = p.Value
			peakDate = p.Date.Format("2006-01-02")
		}
	}
	drawdownPct := 0.0
	if peak > 0 {
		drawdownPct = (current - peak) / peak * 100
	}

	// Compute 30-day volatility (safe index bounds: startIdx >= 1)
	returns := make([]float64, 0, 30)
	startIdx := len(points) - 30
	if startIdx < 1 {
		startIdx = 1
	}
	for i := startIdx; i < len(points); i++ {
		if points[i-1].Value > 0 {
			r := (points[i].Value - points[i-1].Value) / points[i-1].Value
			returns = append(returns, r)
		}
	}
	avg := 0.0
	for _, r := range returns {
		avg += r
	}
	if len(returns) > 0 {
		avg /= float64(len(returns))
	}
	variance := 0.0
	for _, r := range returns {
		d := r - avg
		variance += d * d
	}
	if len(returns) > 1 {
		variance /= float64(len(returns) - 1)
	}
	periodsPerYear := 252.0
	if len(points) >= 10 {
		spanDays := points[len(points)-1].Date.Sub(points[0].Date).Hours() / 24.0
		if spanDays >= 14 && float64(len(points)-1)/(spanDays/365.25) > 300 {
			periodsPerYear = 365.25
		}
	}
	vol30d := math.Sqrt(variance) * math.Sqrt(periodsPerYear) * 100

	// Historical drawdown recovery analysis
	type drawdownEpisode struct {
		FromDate     string  `json:"from_date"`
		TroughDate   string  `json:"trough_date"`
		RecoveryDate string  `json:"recovery_date"`
		MaxDraw      float64 `json:"max_drawdown_pct"`
		RecoveryDays int     `json:"recovery_days"`
	}
	episodes := []drawdownEpisode{}
	// Find past drawdown episodes (>5%)
	localPeak := points[0].Value
	localPeakDate := points[0].Date
	inDrawdown := false
	var epStart time.Time
	trough := localPeak
	troughDate := localPeakDate
	for _, p := range points {
		if p.Value > localPeak {
			if inDrawdown && localPeak > 0 {
				// Recovered
				dd := (trough - localPeak) / localPeak * 100
				if dd < -5 && dd > -80 { // skip >80% drops (data anomalies)
					recovery := int(p.Date.Sub(troughDate).Hours() / 24)
					episodes = append(episodes, drawdownEpisode{
						FromDate: epStart.Format("2006-01-02"), TroughDate: troughDate.Format("2006-01-02"),
						RecoveryDate: p.Date.Format("2006-01-02"),
						MaxDraw: math.Round(dd*10) / 10, RecoveryDays: recovery,
					})
				}
				inDrawdown = false
			}
			localPeak = p.Value
			localPeakDate = p.Date
			trough = p.Value
			troughDate = p.Date
		} else if p.Value < trough {
			if !inDrawdown {
				epStart = localPeakDate
				inDrawdown = true
			}
			trough = p.Value
			troughDate = p.Date
		}
	}

	// Elevated volatility detection
	elevated := vol30d > 20 || drawdownPct < -5

	// Context message
	message := ""
	if drawdownPct < -10 {
		message = fmt.Sprintf("Portfolio is %.1f%% below its all-time high (%s). ", math.Abs(drawdownPct), peakDate)
		if len(episodes) > 0 {
			avgRecovery := 0
			for _, ep := range episodes {
				avgRecovery += ep.RecoveryDays
			}
			avgRecovery /= len(episodes)
			message += fmt.Sprintf("Your portfolio has recovered from %d drawdowns before, averaging %d days to recover.", len(episodes), avgRecovery)
		}
	} else if drawdownPct < -5 {
		message = fmt.Sprintf("Portfolio is %.1f%% off its peak. This is within normal market fluctuation.", math.Abs(drawdownPct))
	} else if vol30d > 25 {
		message = fmt.Sprintf("30-day volatility is elevated at %.0f%% (annualized). Markets are choppy but your portfolio is near its highs.", vol30d)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"elevated":       elevated,
		"current_value":  math.Round(current),
		"all_time_high":  math.Round(peak),
		"ath_date":       peakDate,
		"drawdown_pct":   math.Round(drawdownPct*10) / 10,
		"vol_30d":        math.Round(vol30d*10) / 10,
		"message":        message,
		"past_drawdowns": episodes,
		"drawdown_count": len(episodes),
	})
}

// HandleOpportunityCost computes opportunity costs from cash drag, timing delays, and FSA waste.
func (h *AnalysisHandler) HandleOpportunityCost(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	now := time.Now().UTC()
	year := now.Year()

	accts, _ := h.queries.ListAccounts(ctx)
	txns, _ := listAllTransactionsRows(ctx, h.queries)
	secs, _ := h.queries.ListSecurities(ctx)
	enriched, _ := h.loadEnrichedHoldings(ctx)
	fxRates := loadFXRatesMap(ctx, h.queries)
	histFX := loadHistoricalFXRates(ctx, h.queries)
	tfsMap := make(map[string]float64, len(secs))
	for _, s := range secs {
		tfsMap[s.ISIN] = securityTFSRate(s)
	}

	// Portfolio total and annual return estimate
	portfolioValue := 0.0
	for _, eh := range enriched {
		portfolioValue += eh.value
	}
	annualReturn := 0.07 // 7% nominal

	// --- 1. Cash Drag Analysis ---
	type accountCashDrag struct {
		AccountName     string  `json:"account_name"`
		AccountType     string  `json:"account_type"`
		CashBalance     float64 `json:"cash_balance"`
		OpportunityCost float64 `json:"opportunity_cost_annual"`
	}
	cashDragAccounts := []accountCashDrag{}
	totalCash := 0.0
	cashByAcct, _ := computeAllAccountCashEUR(ctx, h.queries, accts, fxRates)

	for _, acc := range accts {
		if !acc.IsActive {
			continue
		}
		cash := cashByAcct[acc.ID]
		if cash <= 0 {
			continue
		}
		totalCash += cash
		oc := cash * annualReturn
		cashDragAccounts = append(cashDragAccounts, accountCashDrag{
			AccountName: acc.Name, AccountType: acc.Type,
			CashBalance:     math.Round(cash*100) / 100,
			OpportunityCost: math.Round(oc*100) / 100,
		})
	}
	sort.Slice(cashDragAccounts, func(i, j int) bool {
		return cashDragAccounts[i].CashBalance > cashDragAccounts[j].CashBalance
	})

	totalWealth := portfolioValue + totalCash
	recommendedReserve := totalWealth * 0.05 // 5% cash reserve
	if recommendedReserve < 5000 {
		recommendedReserve = 5000
	}
	excessCash := math.Max(totalCash-recommendedReserve, 0)
	cashDragAnnual := excessCash * annualReturn

	// --- 2. Timing Cost: Sparplan delays ---
	// Find months where savings_plan/buy was later than usual
	type monthInvest struct {
		month string
		total float64
		day   int // earliest investment day of month
	}
	monthMap := make(map[string]*monthInvest)
	for _, txn := range txns {
		if txn.Type != "savings_plan" && txn.Type != "buy" {
			continue
		}
		amt := 0.0
		if txn.Amount.Valid {
			f, _ := txn.Amount.Float64Value()
			amt = convertAmountToEURAtDate(f.Float64, txn.Currency, txn.Date, histFX, fxRates)
		}
		m := txn.Date.Format("2006-01")
		mi, ok := monthMap[m]
		if !ok {
			mi = &monthInvest{month: m, day: txn.Date.Day()}
			monthMap[m] = mi
		}
		mi.total += amt
		if txn.Date.Day() < mi.day {
			mi.day = txn.Date.Day()
		}
	}
	// Average investment day
	totalDay := 0
	dayCount := 0
	for _, mi := range monthMap {
		totalDay += mi.day
		dayCount++
	}
	avgDay := 15
	if dayCount > 0 {
		avgDay = totalDay / dayCount
	}
	// Delay cost: each day later = 1 day less compounding
	avgMonthly := 0.0
	if dayCount > 0 {
		total := 0.0
		for _, mi := range monthMap {
			total += mi.total
		}
		avgMonthly = total / float64(dayCount)
	}
	dailyReturn := math.Pow(1+annualReturn, 1.0/365.0) - 1
	avgDelayCost := float64(avgDay-1) * dailyReturn * avgMonthly * 12 // annual cost of avg delay

	// --- 3. Rebalancing delay cost ---
	// Check allocation drift from targets
	targets, _ := h.queries.ListTargetAllocations(ctx)
	rebalanceCost := 0.0
	if len(targets) > 0 && portfolioValue > 0 {
		// Sum of absolute drift × estimated return difference
		for _, t := range targets {
			targetPct := numericToFloat(t.TargetWeightPct)
			for _, eh := range enriched {
				if eh.security.ISIN == t.SecurityISIN {
					actualPct := eh.value / portfolioValue * 100
					drift := math.Abs(actualPct - targetPct)
					// Overweight in low-return assets or underweight in high-return = cost
					// Estimate: 2% return drag per 10% drift
					rebalanceCost += portfolioValue * drift / 100 * 0.02
				}
			}
		}
	}

	// --- 4. FSA Waste ---
	ytdIncome := 0.0
	for _, txn := range txns {
		if txn.Date.Year() != year {
			continue
		}
		if txn.Type == "dividend" || txn.Type == "interest" {
			if txn.Amount.Valid {
				f, _ := txn.Amount.Float64Value()
				amtEUR := convertAmountToEURAtDate(f.Float64, txn.Currency, txn.Date, histFX, fxRates)
				if txn.Type == "dividend" && txn.SecurityISIN.Valid {
					amtEUR *= 1 - tfsMap[txn.SecurityISIN.String]
				}
				ytdIncome += amtEUR
			}
		}
	}
	monthsElapsed := float64(now.Month())
	if monthsElapsed < 1 {
		monthsElapsed = 1
	}
	projectedIncome := ytdIncome / monthsElapsed * 12
	fsaUnused := math.Max(analytics.Sparerpauschbetrag-projectedIncome, 0)
	fsaWaste := fsaUnused * analytics.EffectiveTaxRate

	// --- Total ---
	totalAnnual := cashDragAnnual + avgDelayCost + rebalanceCost + fsaWaste

	// Monthly rollup for cumulative chart
	type monthCost struct {
		Month     string  `json:"month"`
		CashDrag  float64 `json:"cash_drag"`
		Timing    float64 `json:"timing"`
		Rebalance float64 `json:"rebalance"`
		FSA       float64 `json:"fsa"`
		Total     float64 `json:"total"`
	}
	monthlyCashDrag := cashDragAnnual / 12
	monthlyTiming := avgDelayCost / 12
	monthlyRebal := rebalanceCost / 12
	monthlyFSA := fsaWaste / 12
	monthlySeries := []monthCost{}
	cumTotal := 0.0
	for m := 1; m <= 12; m++ {
		mTotal := monthlyCashDrag + monthlyTiming + monthlyRebal + monthlyFSA
		cumTotal += mTotal
		monthlySeries = append(monthlySeries, monthCost{
			Month:     fmt.Sprintf("%d-%02d", year, m),
			CashDrag:  math.Round(monthlyCashDrag*100) / 100,
			Timing:    math.Round(monthlyTiming*100) / 100,
			Rebalance: math.Round(monthlyRebal*100) / 100,
			FSA:       math.Round(monthlyFSA*100) / 100,
			Total:     math.Round(cumTotal*100) / 100,
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"cash_drag": map[string]any{
			"accounts":           cashDragAccounts,
			"total_cash":         math.Round(totalCash*100) / 100,
			"recommended_reserve": math.Round(recommendedReserve),
			"excess_cash":        math.Round(excessCash),
			"annual_cost":        math.Round(cashDragAnnual),
		},
		"timing": map[string]any{
			"avg_invest_day":  avgDay,
			"avg_monthly":     math.Round(avgMonthly),
			"annual_cost":     math.Round(avgDelayCost),
		},
		"rebalance": map[string]any{
			"annual_cost": math.Round(rebalanceCost),
		},
		"fsa_waste": map[string]any{
			"projected_income": math.Round(projectedIncome),
			"unused":           math.Round(fsaUnused),
			"annual_cost":      math.Round(fsaWaste),
		},
		"total_annual":    math.Round(totalAnnual),
		"monthly_rollup":  monthlySeries,
	})
}

// HandleDataQuality checks for data quality issues and financial anomalies.
func (h *AnalysisHandler) HandleDataQuality(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	type issue struct {
		Type     string `json:"type"`     // stale_price, missing_dividend, fee_anomaly, duplicate, concentration_drift, ter_change
		Severity string `json:"severity"` // warning, error, info
		Title    string `json:"title"`
		Detail   string `json:"detail"`
		ISIN     string `json:"isin,omitempty"`
	}
	issues := []issue{}

	enriched, _ := h.loadEnrichedHoldings(ctx)
	secs, _ := h.queries.ListSecurities(ctx)
	txns, _ := listAllTransactionsRows(ctx, h.queries)
	fxRates := loadFXRatesMap(ctx, h.queries)
	histFX := loadHistoricalFXRates(ctx, h.queries)
	now := time.Now().UTC()

	secMap := make(map[string]db.Security)
	for _, s := range secs {
		secMap[s.ISIN] = s
	}

	// --- 1. Stale Prices ---
	for _, eh := range enriched {
		if eh.value <= 0 {
			continue
		}
		// Check last price date
		priceRows, _ := h.queries.ListPriceHistory(ctx, eh.security.ISIN)
		if len(priceRows) == 0 {
			issues = append(issues, issue{
				Type: "stale_price", Severity: "error",
				Title: "No price data: " + eh.security.Name,
				Detail: "No market data available. Holdings valued at cost basis.",
				ISIN: eh.security.ISIN,
			})
			continue
		}
		lastPrice := priceRows[len(priceRows)-1]
		daysSince := int(now.Sub(lastPrice.Date).Hours() / 24)
		if daysSince > 5 {
			issues = append(issues, issue{
				Type: "stale_price", Severity: "warning",
				Title: fmt.Sprintf("Stale price: %s (%d days old)", eh.security.Name, daysSince),
				Detail: fmt.Sprintf("Last price from %s. Current valuation may be inaccurate.", lastPrice.Date.Format("2006-01-02")),
				ISIN: eh.security.ISIN,
			})
		}
	}

	// --- 2. Missing Dividends (equity ETFs with no dividends in 12+ months) ---
	// Track last dividend per ISIN
	lastDiv := make(map[string]time.Time)
	for _, txn := range txns {
		if txn.Type == "dividend" && txn.SecurityISIN.Valid {
			isin := txn.SecurityISIN.String
			if txn.Date.After(lastDiv[isin]) {
				lastDiv[isin] = txn.Date
			}
		}
	}
	for _, eh := range enriched {
		sec := eh.security
		if sec.AssetClass != "etf" || eh.value < 1000 {
			continue
		}
		// Distributing ETFs should have dividends
		if strings.Contains(strings.ToLower(sec.Name), "dist") {
			last, ok := lastDiv[sec.ISIN]
			if !ok {
				issues = append(issues, issue{
					Type: "missing_dividend", Severity: "warning",
					Title: "No dividends recorded: " + sec.Name,
					Detail: "This is a distributing ETF but no dividend transactions found.",
					ISIN: sec.ISIN,
				})
			} else if now.Sub(last).Hours() > 365*24 {
				issues = append(issues, issue{
					Type: "missing_dividend", Severity: "warning",
					Title: fmt.Sprintf("No dividend in 12+ months: %s", sec.Name),
					Detail: fmt.Sprintf("Last dividend: %s. Check if distributions are being recorded.", last.Format("2006-01-02")),
					ISIN: sec.ISIN,
				})
			}
		}
	}

	// --- 3. Fee Anomalies (unusually high fees) ---
	for _, txn := range txns {
		if !txn.Fee.Valid {
			continue
		}
		feeRaw, _ := txn.Fee.Float64Value()
		feeEUR := convertAmountToEURAtDate(feeRaw.Float64, txn.Currency, txn.Date, histFX, fxRates)
		amtEUR := 0.0
		if txn.Amount.Valid {
			f, _ := txn.Amount.Float64Value()
			amtEUR = convertAmountToEURAtDate(f.Float64, txn.Currency, txn.Date, histFX, fxRates)
		}
		if feeEUR > 0 && amtEUR > 0 {
			feePct := feeEUR / amtEUR * 100
			if feePct > 1 && feeEUR > 10 {
				isin := ""
				if txn.SecurityISIN.Valid {
					isin = txn.SecurityISIN.String
				}
				name := isin
				if s, ok := secMap[isin]; ok {
					name = s.Name
				}
				issues = append(issues, issue{
					Type: "fee_anomaly", Severity: "warning",
					Title:  fmt.Sprintf("High fee: %.2f EUR (%.1f%%) on %s", feeEUR, feePct, name),
					Detail: fmt.Sprintf("Transaction on %s for %s EUR had %.2f EUR fee.", txn.Date.Format("2006-01-02"), fmtEUR(amtEUR), feeEUR),
					ISIN:   isin,
				})
			}
		}
	}

	// --- 4. Duplicate Securities (same name, different ISIN) ---
	nameCount := make(map[string][]string) // name -> ISINs
	for _, s := range secs {
		// Normalize name for comparison
		normalized := strings.ToLower(strings.TrimSpace(s.Name))
		nameCount[normalized] = append(nameCount[normalized], s.ISIN)
	}
	for name, isins := range nameCount {
		if len(isins) > 1 {
			issues = append(issues, issue{
				Type: "duplicate", Severity: "info",
				Title:  fmt.Sprintf("Possible duplicate: %s (%d ISINs)", name, len(isins)),
				Detail: fmt.Sprintf("ISINs: %s. These may be the same security listed separately.", strings.Join(isins, ", ")),
			})
		}
	}

	// --- 5. Concentration Drift (single position >30% from pure appreciation) ---
	totalValue := 0.0
	valByISIN := make(map[string]float64)
	costByISIN := make(map[string]float64)
	nameByISIN := make(map[string]string)
	var isinOrder []string
	for _, eh := range enriched {
		totalValue += eh.value
		isin := eh.security.ISIN
		if _, seen := nameByISIN[isin]; !seen {
			nameByISIN[isin] = eh.security.Name
			isinOrder = append(isinOrder, isin)
		}
		valByISIN[isin] += eh.value
		costByISIN[isin] += eh.costValue
	}
	if totalValue > 0 {
		for _, isin := range isinOrder {
			pct := valByISIN[isin] / totalValue * 100
			if pct > 30 {
				costPct := costByISIN[isin] / totalValue * 100
				if pct-costPct > 5 {
					issues = append(issues, issue{
						Type: "concentration_drift", Severity: "warning",
						Title:  fmt.Sprintf("Concentration drift: %s at %.0f%%", nameByISIN[isin], pct),
						Detail: fmt.Sprintf("Cost-basis weight was %.0f%%, now %.0f%% from appreciation. Consider rebalancing.", costPct, pct),
						ISIN:   isin,
					})
				}
			}
		}
	}

	// Sort: errors first, then warnings, then info
	severityOrder := map[string]int{"error": 0, "warning": 1, "info": 2}
	sort.Slice(issues, func(i, j int) bool {
		return severityOrder[issues[i].Severity] < severityOrder[issues[j].Severity]
	})

	errCount, warnCount, infoCount := 0, 0, 0
	for _, iss := range issues {
		switch iss.Severity {
		case "error": errCount++
		case "warning": warnCount++
		case "info": infoCount++
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"issues":   issues,
		"count":    len(issues),
		"errors":   errCount,
		"warnings": warnCount,
		"info":     infoCount,
	})
}

// HandleJournalList returns decision journal entries.
func (h *AnalysisHandler) HandleJournalList(w http.ResponseWriter, r *http.Request) {
	entries, err := h.queries.ListJournalEntries(r.Context(), 50)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list journal: "+err.Error())
		return
	}
	type entry struct {
		ID         string  `json:"id"`
		Date       string  `json:"date"`
		ActionType string  `json:"action_type"`
		ISIN       string  `json:"isin,omitempty"`
		Amount     float64 `json:"amount,omitempty"`
		Reason     string  `json:"reason"`
		Outcome    string  `json:"outcome,omitempty"`
		OutcomeDate string `json:"outcome_date,omitempty"`
		DaysAgo    int     `json:"days_ago"`
	}
	var result []entry
	now := time.Now()
	for _, e := range entries {
		isin := ""
		if e.SecurityISIN.Valid { isin = e.SecurityISIN.String }
		amt := numericToFloat(e.Amount)
		outcome := ""
		if e.Outcome.Valid { outcome = e.Outcome.String }
		outcomeDate := ""
		if e.OutcomeDate.Valid { outcomeDate = e.OutcomeDate.Time.Format("2006-01-02") }
		daysAgo := int(now.Sub(e.Date).Hours() / 24)
		result = append(result, entry{
			ID: e.ID.String(), Date: e.Date.Format("2006-01-02"),
			ActionType: e.ActionType, ISIN: isin, Amount: amt,
			Reason: e.Reason, Outcome: outcome, OutcomeDate: outcomeDate, DaysAgo: daysAgo,
		})
	}
	if result == nil { result = []entry{} }
	writeJSON(w, http.StatusOK, map[string]any{"entries": result})
}

// HandleJournalCreate creates a new decision journal entry.
func (h *AnalysisHandler) HandleJournalCreate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ActionType string  `json:"action_type"`
		ISIN       string  `json:"isin"`
		Amount     float64 `json:"amount"`
		Reason     string  `json:"reason"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if body.Reason == "" {
		writeError(w, http.StatusBadRequest, "reason is required")
		return
	}
	if math.IsNaN(body.Amount) || math.IsInf(body.Amount, 0) || body.Amount < 0 {
		writeError(w, http.StatusBadRequest, "amount must be a non-negative finite number")
		return
	}
	amt := pgtype.Numeric{}
	if body.Amount > 0 {
		amt.Scan(fmt.Sprintf("%.2f", body.Amount))
	}
	isin := pgtype.Text{}
	if body.ISIN != "" {
		isin = pgtype.Text{String: body.ISIN, Valid: true}
	}
	entry, err := h.queries.CreateJournalEntry(r.Context(), db.CreateJournalEntryParams{
		ActionType: body.ActionType, SecurityISIN: isin, Amount: amt, Reason: body.Reason,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "create journal entry: "+err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": entry.ID.String()})
}

// HandleJournalOutcome updates a journal entry with the outcome.
func (h *AnalysisHandler) HandleJournalOutcome(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var body struct {
		Outcome string `json:"outcome"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if err := h.queries.UpdateJournalOutcome(r.Context(), db.UpdateJournalOutcomeParams{
		ID: id, Outcome: pgtype.Text{String: body.Outcome, Valid: true},
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "update outcome: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})
}

// HandleCrisisStressTest returns crisis scenarios with portfolio-specific impact.
func (h *AnalysisHandler) HandleCrisisStressTest(w http.ResponseWriter, r *http.Request) {
	type crisisScenario struct {
		Name        string  `json:"name"`
		Period      string  `json:"period"`
		StartDate   string  `json:"start_date"`
		EndDate     string  `json:"end_date"`
		IndexReturn float64 `json:"index_return_pct"`
		RecoveryMo  int     `json:"recovery_months"`
		WorstMonth  float64 `json:"worst_month_pct"`
		Description string  `json:"description"`
	}

	// Static crisis library — MSCI World drawdowns (approximate)
	scenarios := []crisisScenario{
		{Name: "2008 Global Financial Crisis", Period: "Oct 2007 – Mar 2009", StartDate: "2007-10-01", EndDate: "2009-03-09", IndexReturn: -54.0, RecoveryMo: 50, WorstMonth: -18.9, Description: "Lehman collapse, global bank bailouts, housing market crash"},
		{Name: "Dot-Com Crash", Period: "Mar 2000 – Oct 2002", StartDate: "2000-03-01", EndDate: "2002-10-09", IndexReturn: -47.0, RecoveryMo: 72, WorstMonth: -11.0, Description: "Tech bubble burst, 9/11 aftermath, Enron/WorldCom scandals"},
		{Name: "COVID-19 Crash", Period: "Feb – Mar 2020", StartDate: "2020-02-19", EndDate: "2020-03-23", IndexReturn: -34.0, RecoveryMo: 5, WorstMonth: -13.5, Description: "Global pandemic lockdowns, fastest bear market in history"},
		{Name: "2022 Rate Shock", Period: "Jan – Oct 2022", StartDate: "2022-01-03", EndDate: "2022-10-12", IndexReturn: -25.0, RecoveryMo: 14, WorstMonth: -9.3, Description: "Aggressive Fed rate hikes, inflation spike, Ukraine war"},
		{Name: "Eurozone Crisis", Period: "May – Sep 2011", StartDate: "2011-05-01", EndDate: "2011-09-22", IndexReturn: -22.0, RecoveryMo: 7, WorstMonth: -7.5, Description: "Greek debt crisis, ECB intervention, sovereign debt fears"},
		{Name: "1970s Stagflation", Period: "Jan 1973 – Oct 1974", StartDate: "1973-01-01", EndDate: "1974-10-03", IndexReturn: -45.0, RecoveryMo: 87, WorstMonth: -11.0, Description: "Oil embargo, high inflation, deep recession"},
		{Name: "Black Monday", Period: "Oct 1987", StartDate: "1987-10-14", EndDate: "1987-10-19", IndexReturn: -22.6, RecoveryMo: 20, WorstMonth: -22.6, Description: "Single-day market crash, program trading cascade"},
		{Name: "China Devaluation", Period: "Jun – Aug 2015", StartDate: "2015-06-12", EndDate: "2015-08-25", IndexReturn: -12.0, RecoveryMo: 4, WorstMonth: -6.3, Description: "Chinese yuan devaluation, emerging market selloff"},
	}

	// Compute portfolio-specific impact using current holdings
	ctx := r.Context()
	enriched, err := h.loadEnrichedHoldings(ctx)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"scenarios": scenarios, "portfolio_impact": []any{}})
		return
	}

	totalValue := 0.0
	equityValue := 0.0
	for _, eh := range enriched {
		val := eh.value
		totalValue += val
		if eh.security.AssetClass == "stock" || securityTFSRate(eh.security) > 0 {
			equityValue += val
		}
	}

	equityPct := 0.0
	if totalValue > 0 {
		equityPct = equityValue / totalValue
	}

	type portfolioImpact struct {
		Name           string  `json:"name"`
		DrawdownPct    float64 `json:"drawdown_pct"`
		DrawdownEUR    float64 `json:"drawdown_eur"`
		RecoveryMonths int     `json:"recovery_months"`
		WorstMonthPct  float64 `json:"worst_month_pct"`
		WorstMonthEUR  float64 `json:"worst_month_eur"`
	}

	impacts := make([]portfolioImpact, len(scenarios))
	for i, s := range scenarios {
		// Scale crisis drawdown by equity exposure
		drawdownPct := s.IndexReturn * equityPct
		impacts[i] = portfolioImpact{
			Name:           s.Name,
			DrawdownPct:    math.Round(drawdownPct*10) / 10,
			DrawdownEUR:    math.Round(totalValue * drawdownPct / 100),
			RecoveryMonths: s.RecoveryMo,
			WorstMonthPct:  math.Round(s.WorstMonth*equityPct*10) / 10,
			WorstMonthEUR:  math.Round(totalValue * s.WorstMonth * equityPct / 100),
		}
	}

	// Cash buffer: sum positive EUR cash balances across all active accounts
	cashBuffer := 0.0
	allAccounts, _ := h.queries.ListAccounts(ctx)
	fxRates := loadFXRatesMap(ctx, h.queries)
	cashByAcct, _ := computeAllAccountCashEUR(ctx, h.queries, allAccounts, fxRates)
	for _, acc := range allAccounts {
		if acc.IsActive && cashByAcct[acc.ID] > 0 {
			cashBuffer += cashByAcct[acc.ID]
		}
	}

	// Per-holding impact for worst crisis (2008 GFC)
	type holdingImpact struct {
		Name        string  `json:"name"`
		ISIN        string  `json:"isin"`
		Value       float64 `json:"value"`
		IsEquity    bool    `json:"is_equity"`
		DrawdownEUR float64 `json:"drawdown_eur"`
		DrawdownPct float64 `json:"drawdown_pct"`
	}
	worstDrawdown := scenarios[0].IndexReturn // GFC = -54%
	holdingImpacts := make([]holdingImpact, 0, len(enriched))
	for _, eh := range enriched {
		isEq := eh.security.AssetClass == "stock" || securityTFSRate(eh.security) > 0
		dd := 0.0
		if isEq {
			dd = worstDrawdown
		}
		holdingImpacts = append(holdingImpacts, holdingImpact{
			Name:        eh.security.Name,
			ISIN:        eh.security.ISIN,
			Value:       math.Round(eh.value),
			IsEquity:    isEq,
			DrawdownEUR: math.Round(eh.value * dd / 100),
			DrawdownPct: dd,
		})
	}

	// DCA recovery: how much faster recovery is with continued monthly contributions
	// Assume monthly DCA of totalValue * 0.01 (1% of portfolio)
	monthlyDCA := totalValue * 0.01
	dcaRecovery := make([]struct {
		Name         string `json:"name"`
		WithoutDCA   int    `json:"without_dca_months"`
		WithDCA      int    `json:"with_dca_months"`
		Acceleration int    `json:"acceleration_months"`
	}, len(scenarios))
	for i, s := range scenarios {
		withoutDCA := s.RecoveryMo
		// With DCA: approximate recovery by simulating monthly contributions during drawdown recovery
		// Each month of DCA buys at lower prices, accelerating recovery by ~20-30%
		drawdownAbs := math.Abs(s.IndexReturn) / 100
		dcaBoost := 0.0
		if totalValue > 0 && drawdownAbs > 0 {
			dcaBoost = monthlyDCA * float64(withoutDCA) / (totalValue * drawdownAbs)
		}
		withDCA := int(math.Round(float64(withoutDCA) * (1 - math.Min(dcaBoost*0.5, 0.4))))
		if withDCA < 1 {
			withDCA = 1
		}
		dcaRecovery[i] = struct {
			Name         string `json:"name"`
			WithoutDCA   int    `json:"without_dca_months"`
			WithDCA      int    `json:"with_dca_months"`
			Acceleration int    `json:"acceleration_months"`
		}{
			Name:         s.Name,
			WithoutDCA:   withoutDCA,
			WithDCA:      withDCA,
			Acceleration: withoutDCA - withDCA,
		}
	}

	// Cash buffer adequacy: months of expenses covered
	monthlyExpense := totalValue * 0.05 / 12 // rough 5% annual withdrawal rate
	cashMonths := 0
	if monthlyExpense > 0 {
		cashMonths = int(cashBuffer / monthlyExpense)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"scenarios":         scenarios,
		"portfolio_impact":  impacts,
		"holding_impacts":   holdingImpacts,
		"dca_recovery":      dcaRecovery,
		"portfolio_value":   math.Round(totalValue),
		"equity_pct":        math.Round(equityPct * 1000) / 10,
		"cash_buffer":       math.Round(cashBuffer),
		"cash_buffer_months": cashMonths,
	})
}
