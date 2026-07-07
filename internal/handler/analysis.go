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
	db "github.com/lnsp/wealth/internal/database/generated"
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

// enrichedHolding bundles a holding with its pre-loaded security and market value.
type enrichedHolding struct {
	holding  db.ListCurrentHoldingsRow
	security db.Security
	value    float64
}

type AnalysisHandler struct {
	queries *db.Queries
}

func NewAnalysisHandler(q *db.Queries) *AnalysisHandler {
	return &AnalysisHandler{queries: q}
}

// loadEnrichedHoldings batch-loads all current holdings with their securities
// and market values in 3 queries (holdings + securities + prices) instead of
// 2N+1 queries from per-holding GetSecurity + GetLatestPrice calls.
func (h *AnalysisHandler) loadEnrichedHoldings(ctx context.Context) ([]enrichedHolding, error) {
	holdings, err := h.queries.ListCurrentHoldings(ctx)
	if err != nil {
		return nil, err
	}

	// Filter by user's accounts if auth is enabled
	if allowedIDs := userAccountIDs(ctx, h.queries.DB()); allowedIDs != nil {
		allowed := make(map[uuid.UUID]bool, len(allowedIDs))
		for _, id := range allowedIDs { allowed[id] = true }
		filtered := holdings[:0]
		for _, hld := range holdings {
			if allowed[hld.AccountID] { filtered = append(filtered, hld) }
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

	prices, err := h.queries.ListLatestPrices(ctx)
	if err != nil {
		prices = nil // non-fatal: fall back to cost basis
	}
	pm := make(priceMapAnalysis, len(prices))
	for _, p := range prices {
		if p.Close.Valid {
			f, _ := p.Close.Float64Value()
			pm[p.SecurityISIN] = f.Float64
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
		value := 0.0
		if price, ok := pm[hold.SecurityISIN]; ok {
			value = qty * price
		} else if hold.AvgCostBasis.Valid {
			f, _ := hold.AvgCostBasis.Float64Value()
			value = qty * f.Float64
		}
		result = append(result, enrichedHolding{
			holding:  hold,
			security: sec,
			value:    value,
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

	alerts := analytics.ComputeConcentrationAlerts(etfs, holdingsWithETF, holdingNames)
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

	txns, err := h.queries.ListTransactions(ctx, db.ListTransactionsParams{Limit: 10000, Offset: 0})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list transactions: "+err.Error())
		return
	}
	if len(txns) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{"history": []any{}})
		return
	}

	// Sort chronologically (ListTransactions returns DESC)
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
	var snapshots []sectorSnapshot

	// Emit a snapshot of sector allocation from current holdings state
	emitSnapshot := func(date string) {
		totalValue := 0.0
		isinValues := make(map[string]float64)
		for isin, hs := range holdings {
			if hs.quantity <= 0 {
				continue
			}
			val := hs.totalCost // use cost basis for historical
			isinValues[isin] = val
			totalValue += val
		}
		if totalValue == 0 {
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
		amt := numericToFloat(txn.Amount)
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
					if hs.quantity <= 0.001 {
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
					if hs.quantity <= 0.001 {
						delete(holdings, isin)
					}
				}
			}
		}
	}

	// Final snapshot using market prices where available
	if lastMonth != "" {
		totalValue := 0.0
		isinValues := make(map[string]float64)
		for isin, hs := range holdings {
			if hs.quantity <= 0 {
				continue
			}
			val := hs.totalCost
			if price, err := h.queries.GetLatestPrice(ctx, isin); err == nil && price.Close.Valid {
				f, _ := price.Close.Float64Value()
				val = hs.quantity * f.Float64
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

// HandleRisk computes portfolio risk metrics from daily net worth snapshots.
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
	benchPrices := loadBenchmarkPrices(ctx, h.queries)
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
	metrics := analytics.ComputeRiskMetrics(points, riskFreeRate, currentValue)
	rolling := analytics.ComputeRollingMetrics(points, riskFreeRate)

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
func (h *AnalysisHandler) HandleTax(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	yearStr := r.URL.Query().Get("year")
	year := time.Now().Year()
	if yearStr != "" {
		if y, err := strconv.Atoi(yearStr); err == nil && y >= 2020 && y <= 2100 {
			year = y
		}
	}

	txns, err := h.queries.ListTransactions(ctx, db.ListTransactionsParams{Limit: 10000, Offset: 0})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list transactions: "+err.Error())
		return
	}

	// Load securities to determine equity fund status
	secs, err := h.queries.ListSecurities(ctx)
	if err != nil {
		secs = nil
	}
	secMap := make(map[string]db.Security, len(secs))
	for _, s := range secs {
		secMap[s.ISIN] = s
	}

	// Convert transactions to TaxTransaction format
	var taxTxns []analytics.TaxTransaction
	for _, txn := range txns {
		isin := ""
		if txn.SecurityISIN.Valid {
			isin = txn.SecurityISIN.String
		}
		amt := 0.0
		if txn.Amount.Valid {
			f, _ := txn.Amount.Float64Value()
			amt = f.Float64
		}
		qty := 0.0
		if txn.Quantity.Valid {
			f, _ := txn.Quantity.Float64Value()
			qty = f.Float64
		}

		name := ""
		isEquity := false
		if sec, ok := secMap[isin]; ok {
			name = sec.Name
			isEquity = sec.AssetClass == "etf" // simplified: treat all ETFs as equity funds
		}
		if txn.Counterparty.Valid && name == "" {
			name = txn.Counterparty.String
		}

		taxTxns = append(taxTxns, analytics.TaxTransaction{
			Year:         txn.Date.Year(),
			Type:         txn.Type,
			ISIN:         isin,
			Name:         name,
			Quantity:     qty,
			Amount:       amt,
			IsEquityFund: isEquity,
		})
	}

	// Sort chronologically (ListTransactions returns DESC)
	for i, j := 0, len(taxTxns)-1; i < j; i, j = i+1, j-1 {
		taxTxns[i], taxTxns[j] = taxTxns[j], taxTxns[i]
	}

	// Compute tax-loss harvesting hints from current holdings
	enriched, _ := h.loadEnrichedHoldings(ctx)
	var hints []analytics.TaxLossHint
	pm := make(map[string]float64)
	for _, eh := range enriched {
		qty := 0.0
		if eh.holding.Quantity.Valid {
			f, _ := eh.holding.Quantity.Float64Value()
			qty = f.Float64
		}
		costBasis := 0.0
		if eh.holding.AvgCostBasis.Valid {
			f, _ := eh.holding.AvgCostBasis.Float64Value()
			costBasis = f.Float64 * qty
		}
		if _, seen := pm[eh.security.ISIN]; seen {
			continue // skip duplicates from multi-account
		}
		pm[eh.security.ISIN] = eh.value
		unrealizedPL := eh.value - costBasis
		if unrealizedPL < -10 { // only suggest if meaningful loss
			saving := (-unrealizedPL) * analytics.EffectiveTaxRate
			hints = append(hints, analytics.TaxLossHint{
				ISIN:            eh.security.ISIN,
				Name:            eh.security.Name,
				UnrealizedPL:    math.Round(unrealizedPL*100) / 100,
				PotentialSaving: math.Round(saving*100) / 100,
			})
		}
	}

	// Compute available years from transactions
	yearSet := make(map[int]bool)
	for _, t := range taxTxns {
		if t.Type == "sell" || t.Type == "dividend" {
			yearSet[t.Year] = true
		}
	}
	var availableYears []int
	for y := range yearSet {
		availableYears = append(availableYears, y)
	}
	sort.Slice(availableYears, func(i, j int) bool { return availableYears[i] > availableYears[j] })

	summary := analytics.ComputeTaxSummary(taxTxns, year, hints)

	// Compute Vorabpauschale for accumulating ETFs
	jan1Holdings := make(map[string]float64)
	yearEndHoldings := make(map[string]float64)
	vpNames := make(map[string]string)

	// Use current holdings quantities × price at Jan 1 (approximation)
	jan1Date := time.Date(year, 1, 1, 0, 0, 0, 0, time.Local)
	for _, eh := range enriched {
		if eh.security.AssetClass != "etf" {
			continue
		}
		qty := 0.0
		if eh.holding.Quantity.Valid {
			f, _ := eh.holding.Quantity.Float64Value()
			qty = f.Float64
		}
		if qty <= 0 {
			continue
		}
		vpNames[eh.security.ISIN] = eh.security.Name
		yearEndHoldings[eh.security.ISIN] += eh.value

		// Get price near Jan 1
		if priceRow, err := h.queries.GetPriceAtDate(ctx, db.GetPriceAtDateParams{SecurityISIN: eh.security.ISIN, Date: jan1Date}); err == nil {
			p := numericToFloat(priceRow.Close)
			jan1Holdings[eh.security.ISIN] += qty * p
		}
	}

	vorabpauschale := analytics.ComputeVorabpauschale(year, jan1Holdings, yearEndHoldings, vpNames)

	writeJSON(w, http.StatusOK, map[string]any{
		"summary":         summary,
		"available_years": availableYears,
		"vorabpauschale":  vorabpauschale,
	})
}

// HandleCosts computes ETF cost analysis from TER data and current holdings.
func (h *AnalysisHandler) HandleCosts(w http.ResponseWriter, r *http.Request) {
	enriched, err := h.loadEnrichedHoldings(r.Context())
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

	var entries []costEntry
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

	// Coverage % = how much of total portfolio value has a known TER. When
	// some holdings lack metadata, the weighted_ter understates the true
	// portfolio TER (unpriced rows contribute 0). Frontend surfaces this
	// caveat when coverage < 100%.
	coveragePct := 0.0
	if totalValue > 0 {
		coveragePct = (coveredValue / totalValue) * 100
	}
	// Normalized TER: weighted_ter divided by coverage fraction. Represents
	// the average TER among priced holdings, useful when metadata is partial
	// but the user wants to extrapolate.
	weightedTERCovered := 0.0
	if coveredValue > 0 {
		weightedTERCovered = weightedTER * (totalValue / coveredValue)
	}

	// Sort by annual cost descending
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].AnnualCost > entries[j].AnnualCost
	})

	// 10-year projection
	var projection []map[string]any
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

	// Cost benchmarking: compare against typical German ETF investor
	// Average German ETF investor weighted TER is ~0.35-0.40% (BVI/Morningstar data)
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

	// Total cost of ownership: TER + estimated transaction costs + estimated spread
	txns, _ := h.queries.ListTransactions(r.Context(), db.ListTransactionsParams{Limit: 10000, Offset: 0})
	totalTxnVolume := 0.0
	txnCount := 0
	totalFees := 0.0
	for _, txn := range txns {
		if txn.Type == "buy" || txn.Type == "sell" || txn.Type == "savings_plan" {
			if txn.Amount.Valid {
				f, _ := txn.Amount.Float64Value()
				totalTxnVolume += math.Abs(f.Float64)
				txnCount++
			}
			if txn.Fee.Valid {
				f, _ := txn.Fee.Float64Value()
				totalFees += math.Abs(f.Float64)
			}
		}
	}
	// Estimate spread cost: ~0.05% of total volume for liquid ETFs
	spreadCostEst := totalTxnVolume * 0.0005
	// Annual transaction costs: assume similar volume per year going forward
	years := 1.0
	if len(txns) > 1 {
		first := txns[len(txns)-1].Date
		last := txns[0].Date
		y := last.Sub(first).Hours() / (365.25 * 24)
		if y > 0.5 { years = y }
	}
	annualTxnCost := totalFees / years
	annualSpreadCost := spreadCostEst / years
	totalAnnualCost := totalCost + annualTxnCost + annualSpreadCost

	writeJSON(w, http.StatusOK, map[string]any{
		"holdings":     entries,
		"total_value":  math.Round(totalValue*100) / 100,
		"weighted_ter": math.Round(weightedTER*100) / 100,
		"weighted_ter_covered_only": math.Round(weightedTERCovered*100) / 100,
		"coverage_pct": math.Round(coveragePct*10) / 10,
		"annual_cost":  math.Round(totalCost*100) / 100,
		"daily_cost":   math.Round(totalCost/365*100) / 100,
		"projection":   projection,
		"benchmark": map[string]any{
			"avg_ter":       avgTER,
			"your_ter":      math.Round(weightedTER*100) / 100,
			"grade":         grade,
			"detail":        gradeDetail,
			"annual_saving": math.Round(annualSaving*100) / 100,
			"ten_year_saving": math.Round(tenYearSaving*100) / 100,
		},
		"total_cost_ownership": map[string]any{
			"ter_cost":          math.Round(totalCost*100) / 100,
			"transaction_fees":  math.Round(annualTxnCost*100) / 100,
			"spread_estimate":   math.Round(annualSpreadCost*100) / 100,
			"total_annual":      math.Round(totalAnnualCost*100) / 100,
			"total_annual_pct":  math.Round(totalAnnualCost/totalValue*10000) / 100,
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
	txns, err := h.queries.ListTransactions(ctx, db.ListTransactionsParams{Limit: 10000, Offset: 0})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list transactions: "+err.Error())
		return
	}

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
			amt = f.Float64
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

	var history []monthFlow
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
	var seasonalData []seasonalMonth
	for m := 1; m <= 12; m++ {
		sm := seasonal[m]
		if sm != nil && sm.Count > 0 {
			seasonalData = append(seasonalData, seasonalMonth{
				CalMonth: m,
				Income:   math.Round(sm.Income / float64(sm.Count) * 100) / 100,
				Expenses: math.Round(sm.Expenses / float64(sm.Count) * 100) / 100,
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
			amt = f.Float64
		}
		depositsBySource[cp] = append(depositsBySource[cp], amt)
	}
	var incomeSources []incomeSource
	for name, amounts := range depositsBySource {
		if len(amounts) < 3 {
			continue
		}
		// Check if amounts are similar (within 20% of median)
		sorted := make([]float64, len(amounts))
		copy(sorted, amounts)
		sort.Float64s(sorted)
		median := sorted[len(sorted)/2]
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
	var projection []monthFlow
	now := time.Now()
	for i := 1; i <= 12; i++ {
		d := now.AddDate(0, i, 0)
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
	txns, err := h.queries.ListTransactions(ctx, db.ListTransactionsParams{Limit: 10000, Offset: 0})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list transactions: "+err.Error())
		return
	}
	if len(txns) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{"history": []any{}, "holdings": []string{}})
		return
	}

	// Sort chronologically
	for i, j := 0, len(txns)-1; i < j; i, j = i+1, j-1 {
		txns[i], txns[j] = txns[j], txns[i]
	}

	// Load security names
	secs, _ := h.queries.ListSecurities(ctx)
	secNames := make(map[string]string)
	for _, s := range secs {
		secNames[s.ISIN] = s.Name
	}

	type holdState struct{ qty, cost float64 }
	holdings := make(map[string]*holdState)

	type snapshot struct {
		Date    string             `json:"date"`
		Weights map[string]float64 `json:"weights"`
	}
	var snapshots []snapshot
	lastMonth := ""

	emitSnapshot := func(date string) {
		totalValue := 0.0
		for _, hs := range holdings {
			if hs.qty > 0 { totalValue += hs.cost }
		}
		if totalValue == 0 { return }
		weights := make(map[string]float64)
		var sum float64
		var largestISIN string
		var largestWeight float64
		for isin, hs := range holdings {
			if hs.qty > 0 {
				w := math.Round((hs.cost/totalValue)*1000) / 10
				weights[isin] = w
				sum += w
				if w > largestWeight {
					largestWeight = w
					largestISIN = isin
				}
			}
		}
		// Adjust largest weight so total is exactly 100
		if largestISIN != "" && sum != 100 {
			weights[largestISIN] += math.Round((100-sum)*10) / 10
		}
		snapshots = append(snapshots, snapshot{Date: date, Weights: weights})
	}

	for _, txn := range txns {
		isin := ""
		if txn.SecurityISIN.Valid { isin = txn.SecurityISIN.String }
		amt, qty := 0.0, 0.0
		if txn.Amount.Valid { f, _ := txn.Amount.Float64Value(); amt = f.Float64 }
		if txn.Quantity.Valid { f, _ := txn.Quantity.Float64Value(); qty = f.Float64 }

		month := txn.Date.Format("2006-01")
		if month != lastMonth && lastMonth != "" {
			emitSnapshot(lastMonth + "-01")
		}
		lastMonth = month

		if isin == "" { continue }
		switch txn.Type {
		case "buy", "savings_plan", "transfer":
			hs := holdings[isin]
			if hs == nil { hs = &holdState{}; holdings[isin] = hs }
			hs.qty += qty; hs.cost += amt
		case "sell", "transfer_out":
			if hs := holdings[isin]; hs != nil && hs.qty > 0 {
				costPer := hs.cost / hs.qty
				hs.qty -= qty; hs.cost -= costPer * qty
				if hs.qty <= 0.001 { delete(holdings, isin) }
			}
		}
	}
	if lastMonth != "" { emitSnapshot(lastMonth + "-01") }

	// Collect all holding ISINs
	holdingSet := make(map[string]bool)
	for _, s := range snapshots {
		for isin := range s.Weights { holdingSet[isin] = true }
	}
	var holdingNames []string
	for isin := range holdingSet {
		name := secNames[isin]
		if name == "" { name = isin }
		holdingNames = append(holdingNames, name)
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

	txns, err := h.queries.ListTransactions(ctx, db.ListTransactionsParams{Limit: 10000, Offset: 0})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list transactions: "+err.Error())
		return
	}
	secs, _ := h.queries.ListSecurities(ctx)
	secMap := make(map[string]db.Security, len(secs))
	for _, s := range secs {
		secMap[s.ISIN] = s
	}

	var taxTxns []analytics.TaxTransaction
	for _, txn := range txns {
		isin := ""
		if txn.SecurityISIN.Valid {
			isin = txn.SecurityISIN.String
		}
		amt, qty := 0.0, 0.0
		if txn.Amount.Valid {
			f, _ := txn.Amount.Float64Value()
			amt = f.Float64
		}
		if txn.Quantity.Valid {
			f, _ := txn.Quantity.Float64Value()
			qty = f.Float64
		}
		name := ""
		isEquity := false
		if sec, ok := secMap[isin]; ok {
			name = sec.Name
			isEquity = sec.AssetClass == "etf"
		}
		taxTxns = append(taxTxns, analytics.TaxTransaction{
			Year: txn.Date.Year(), Type: txn.Type, ISIN: isin,
			Name: name, Quantity: qty, Amount: amt, IsEquityFund: isEquity,
		})
	}
	for i, j := 0, len(taxTxns)-1; i < j; i, j = i+1, j-1 {
		taxTxns[i], taxTxns[j] = taxTxns[j], taxTxns[i]
	}

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

	txns, _ := h.queries.ListTransactions(ctx, db.ListTransactionsParams{Limit: 10000, Offset: 0})
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
		// Only export capital income relevant transactions
		if txn.Type != "dividend" && txn.Type != "interest" && txn.Type != "sell" && txn.Type != "fee" {
			continue
		}
		amt := 0.0
		if txn.Amount.Valid {
			f, _ := txn.Amount.Float64Value()
			amt = f.Float64
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

		sollHaben := "S" // Soll (debit)
		if amt > 0 {
			sollHaben = "H" // Haben (credit)
		}

		// DATEV Kontenrahmen: 2650 = Kapitalerträge, 1800 = Bank
		konto := "1800"    // Bank account
		gegenkonto := "2650" // Kapitalerträge
		if txn.Type == "fee" {
			gegenkonto = "4900" // Sonstige Aufwendungen
		}

		buchungstext := fmt.Sprintf("%s: %s", txn.Type, name)
		if len(buchungstext) > 60 {
			buchungstext = buchungstext[:60]
		}

		belegdatum := txn.Date.Format("0201") // DDMM format for DATEV
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

	txns, err := h.queries.ListTransactions(ctx, db.ListTransactionsParams{Limit: 10000, Offset: 0})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list transactions: "+err.Error())
		return
	}
	accts, _ := h.queries.ListAccounts(ctx)
	secs, _ := h.queries.ListSecurities(ctx)

	acctNames := make(map[string]string)
	acctInstitution := make(map[string]string)
	for _, a := range accts {
		acctNames[a.ID.String()] = a.Name
		acctInstitution[a.ID.String()] = a.Institution
	}
	equityMap := make(map[string]bool)
	secNames := make(map[string]string)
	for _, s := range secs {
		equityMap[s.ISIN] = s.AssetClass == "etf"
		secNames[s.ISIN] = s.Name
	}

	// Per-broker aggregation
	type brokerData struct {
		BrokerName    string  `json:"broker_name"`
		AccountName   string  `json:"account_name"`
		Dividends     float64 `json:"dividends"`        // Zeile 7: Kapitalerträge
		Interest      float64 `json:"interest"`          // Zeile 7: Zinsen
		RealizedGains float64 `json:"realized_gains"`    // Zeile 7: Gewinne
		RealizedLoss  float64 `json:"realized_losses"`   // Zeile 12: Verluste
		TeilfreiAmt   float64 `json:"teilfreistellung"`  // Zeile 7: Teilfreistellung (abgezogen)
		WithheldTax   float64 `json:"withheld_tax"`      // Zeile 37: Einbehaltene KapSt
		SoliPaid      float64 `json:"soli_paid"`         // Zeile 38: Solidaritätszuschlag
		FSAUsed       float64 `json:"fsa_used"`          // Zeile 37: Beanspruchter FSA
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

	// Track cost basis for gain computation (FIFO simplified to avg cost per broker)
	type lot struct{ qty, totalCost float64 }
	holdings := make(map[string]*lot) // ISIN -> lot

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
			amt = f.Float64
		}
		qty := 0.0
		if txn.Quantity.Valid {
			f, _ := txn.Quantity.Float64Value()
			qty = f.Float64
		}
		fee := 0.0
		if txn.Fee.Valid {
			f, _ := txn.Fee.Float64Value()
			fee = f.Float64
		}
		tax := 0.0
		if txn.Tax.Valid {
			f, _ := txn.Tax.Float64Value()
			tax = f.Float64
		}
		acctID := txn.AccountID.String()

		switch txn.Type {
		case "buy", "savings_plan", "transfer":
			if isin == "" {
				continue
			}
			l, ok := holdings[isin]
			if !ok {
				l = &lot{}
				holdings[isin] = l
			}
			l.qty += qty
			l.totalCost += amt
		case "transfer_out":
			if isin == "" {
				continue
			}
			l := holdings[isin]
			if l != nil && l.qty > 0 {
				avg := l.totalCost / l.qty
				l.qty -= qty
				l.totalCost -= qty * avg
			}
		case "sell":
			if txn.Date.Year() != year || isin == "" {
				// Still track cost basis for pre-year sells
				if txn.Date.Year() < year && isin != "" {
					l := holdings[isin]
					if l != nil && l.qty > 0 {
						avg := l.totalCost / l.qty
						l.qty -= qty
						l.totalCost -= qty * avg
					}
				}
				continue
			}
			l := holdings[isin]
			if l == nil || l.qty <= 0 {
				continue
			}
			avg := l.totalCost / l.qty
			gain := amt - qty*avg - fee
			l.qty -= qty
			l.totalCost -= qty * avg

			b := getBroker(acctID)
			if gain > 0 {
				// Apply Teilfreistellung for equity funds
				if equityMap[isin] {
					b.TeilfreiAmt += gain * analytics.TeilfreistellungEquity
					gain *= (1 - analytics.TeilfreistellungEquity)
				}
				b.RealizedGains += gain
			} else {
				b.RealizedLoss += gain // negative
			}
			b.WithheldTax += tax
			b.SoliPaid += tax * 0.055

		case "dividend":
			if txn.Date.Year() != year {
				continue
			}
			b := getBroker(acctID)
			divAmt := amt
			if equityMap[isin] {
				b.TeilfreiAmt += divAmt * analytics.TeilfreistellungEquity
				divAmt *= (1 - analytics.TeilfreistellungEquity)
			}
			b.Dividends += divAmt
			b.WithheldTax += tax
			b.SoliPaid += tax * 0.055

		case "interest":
			if txn.Date.Year() != year {
				continue
			}
			b := getBroker(acctID)
			b.Interest += amt
			b.WithheldTax += tax
			b.SoliPaid += tax * 0.055
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

	// Anlage KAP line mapping
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
		{50, "Anzurechnende Steuer (Zeile 37 + 38)", math.Round((totalWithheld + totalSoli) * 100) / 100},
	}

	// Cross-broker note — fires whenever taxable income spans 2+ institutions,
	// since the Sparerpauschbetrag is bank-specific (each broker withholds
	// against its own FSA allocation) and Anlage KAP is the mechanism for
	// reconciling unused FSA at one broker against income at another. The
	// stronger loss-offset message wins when a gain/loss split is also
	// available — that's the higher-value action.
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
		"total_withheld":    math.Round(totalWithheld*100) / 100,
		"cross_broker_note": crossBrokerNote,
		"broker_count":      len(brokerList),
	})
}

// HandleInflation returns inflation-adjusted net worth data using German HVPI.
func (h *AnalysisHandler) HandleInflation(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// German HVPI annual rates (Statistisches Bundesamt, approximate)
	cpiRates := map[int]float64{
		2020: 0.5, 2021: 3.1, 2022: 6.9, 2023: 5.9, 2024: 2.2, 2025: 2.0, 2026: 2.0,
	}

	snaps, err := h.queries.ListNetWorthSnapshots(ctx, 5000)
	if err != nil || len(snaps) < 2 {
		writeJSON(w, http.StatusOK, map[string]any{"history": []any{}})
		return
	}

	// Build monthly CPI index (base = latest month = 100)
	// Work backwards from most recent, deflating by annual rate / 12
	type inflPoint struct {
		Date     string  `json:"date"`
		Nominal  float64 `json:"nominal"`
		Real     float64 `json:"real"`
		CpiIndex float64 `json:"cpi_index"`
	}

	// Compute CPI index per month relative to present
	cpiIndex := make(map[string]float64)
	now := snaps[0].Date
	cpiIndex[now.Format("2006-01")] = 100.0
	for m := 1; m < len(snaps); m++ {
		d := snaps[m].Date
		month := d.Format("2006-01")
		if _, exists := cpiIndex[month]; exists {
			continue
		}
		// Monthly inflation = annual rate / 12
		yr := d.Year()
		annualRate := cpiRates[yr]
		if annualRate == 0 {
			annualRate = 2.0
		}
		monthsFromNow := now.Sub(d).Hours() / (24 * 30.44)
		// CPI at date = 100 / (1 + rate)^(months/12)
		cpiIndex[month] = 100.0 / math.Pow(1+annualRate/100, monthsFromNow/12)
	}

	// Build time series
	var history []inflPoint
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
		if cpi <= 0 {
			cpi = 100
		}
		real := nominal * (100.0 / cpi) // adjust to today's euros
		history = append(history, inflPoint{
			Date:     month,
			Nominal:  math.Round(nominal),
			Real:     math.Round(real),
			CpiIndex: math.Round(cpi*10) / 10,
		})
	}

	// Compute summary — use 12-month return if available, else total return
	if len(history) > 1 {
		last := history[len(history)-1]
		// Find the point ~12 months ago for annualized comparison
		baseIdx := 0
		if len(history) > 12 {
			baseIdx = len(history) - 13 // ~12 months ago
		}
		base := history[baseIdx]
		nominalReturn := 0.0
		realReturn := 0.0
		if base.Nominal > 1000 { // only show % when base is meaningful
			nominalReturn = ((last.Nominal - base.Nominal) / base.Nominal) * 100
		}
		if base.Real > 1000 {
			realReturn = ((last.Real - base.Real) / base.Real) * 100
		}
		erosion := last.Nominal - last.Real

		writeJSON(w, http.StatusOK, map[string]any{
			"history":        history,
			"nominal_return": math.Round(nominalReturn*10) / 10,
			"real_return":    math.Round(realReturn*10) / 10,
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
		benchISIN = "IE00B4L5Y983" // default MSCI World
	}

	// Load benchmark price history
	benchPrices, err := h.queries.ListPriceHistory(ctx, benchISIN)
	if err != nil || len(benchPrices) < 30 {
		writeError(w, http.StatusBadRequest, "insufficient benchmark price data")
		return
	}
	priceByDate := make(map[string]float64)
	for _, p := range benchPrices {
		if p.Close.Valid {
			f, _ := p.Close.Float64Value()
			priceByDate[p.Date.Format("2006-01-02")] = f.Float64
		}
	}
	// Get latest benchmark price
	latestBenchPrice := 0.0
	if benchPrices[len(benchPrices)-1].Close.Valid {
		f, _ := benchPrices[len(benchPrices)-1].Close.Float64Value()
		latestBenchPrice = f.Float64
	}

	// Helper: find closest price on or before date
	closestPrice := func(date time.Time) float64 {
		for d := 0; d < 14; d++ {
			key := date.AddDate(0, 0, -d).Format("2006-01-02")
			if p, ok := priceByDate[key]; ok { return p }
		}
		return 0
	}

	// Load user transactions (deposits only)
	txns, _ := h.queries.ListTransactions(ctx, db.ListTransactionsParams{Limit: 10000, Offset: 0})

	// Replay: for each deposit, compute how many benchmark shares user could have bought
	benchShares := 0.0
	totalDeposited := 0.0
	type replayPoint struct {
		Date       string  `json:"date"`
		Actual     float64 `json:"actual"`
		Benchmark  float64 `json:"benchmark"`
	}

	// Build monthly snapshots
	snaps, _ := h.queries.ListNetWorthSnapshots(ctx, 5000)
	snapByMonth := make(map[string]float64)
	for _, s := range snaps {
		m := s.Date.Format("2006-01")
		if s.Total.Valid {
			f, _ := s.Total.Float64Value()
			snapByMonth[m] = f.Float64
		}
	}

	// Process deposits chronologically
	for i := len(txns) - 1; i >= 0; i-- {
		txn := txns[i]
		if txn.Type != "deposit" && txn.Type != "cash_transfer_in" { continue }
		amt := 0.0
		if txn.Amount.Valid {
			f, _ := txn.Amount.Float64Value()
			amt = f.Float64
		}
		price := closestPrice(txn.Date)
		if price > 0 {
			benchShares += amt / price
		}
		totalDeposited += amt
	}

	benchValue := benchShares * latestBenchPrice

	// Build comparison chart (monthly)
	var comparison []replayPoint
	lastMonth := ""
	runningShares := 0.0
	for i := len(txns) - 1; i >= 0; i-- {
		txn := txns[i]
		if txn.Type == "deposit" || txn.Type == "cash_transfer_in" {
			amt := 0.0
			if txn.Amount.Valid { f, _ := txn.Amount.Float64Value(); amt = f.Float64 }
			price := closestPrice(txn.Date)
			if price > 0 { runningShares += amt / price }
		}
		m := txn.Date.Format("2006-01")
		if m == lastMonth { continue }
		lastMonth = m
		// Benchmark value at this month
		bPrice := closestPrice(txn.Date)
		bVal := runningShares * bPrice
		actual := snapByMonth[m]
		if actual > 0 || bVal > 0 {
			comparison = append(comparison, replayPoint{
				Date: m, Actual: math.Round(actual), Benchmark: math.Round(bVal),
			})
		}
	}

	// Get benchmark name
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
	})
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
		Name   string  `json:"name"`
		Score  int     `json:"score"`
		Weight int     `json:"weight"`
		Status string  `json:"status"`
		Detail string  `json:"detail"`
	}

	// 1. Diversification (25%) — based on number of holdings and max weight
	maxWeight := 0.0
	totalValue := 0.0
	for _, eh := range enriched {
		totalValue += eh.value
	}
	seen := make(map[string]bool)
	for _, eh := range enriched {
		if seen[eh.security.ISIN] { continue }
		seen[eh.security.ISIN] = true
		w := eh.value / totalValue * 100
		if w > maxWeight { maxWeight = w }
	}
	numHoldings := len(seen)
	divScore := 50
	if numHoldings >= 5 { divScore = 70 }
	if numHoldings >= 8 { divScore = 85 }
	if maxWeight < 40 { divScore += 15 }
	if maxWeight < 25 { divScore += 10 }
	if divScore > 100 { divScore = 100 }
	divDetail := fmt.Sprintf("%d holdings, max weight %.0f%%", numHoldings, maxWeight)

	// 2. Cost Efficiency (15%) — based on weighted avg TER
	weightedTER := 0.0
	for _, eh := range enriched {
		ter := numericToFloat(eh.security.TER)
		if ter > 0 {
			weightedTER += (eh.value / totalValue) * ter
		}
	}
	costScore := 90
	if weightedTER > 0.10 { costScore = 80 }
	if weightedTER > 0.20 { costScore = 65 }
	if weightedTER > 0.40 { costScore = 40 }
	costDetail := fmt.Sprintf("Weighted TER %.2f%%", weightedTER)

	// 3. Risk Balance (20%) — based on Sharpe ratio from risk endpoint
	snaps, _ := h.queries.ListNetWorthSnapshots(ctx, 3000)
	points := make([]analytics.DailyValuation, 0, len(snaps))
	for i := len(snaps) - 1; i >= 0; i-- {
		val := 0.0
		if snaps[i].Total.Valid { f, _ := snaps[i].Total.Float64Value(); val = f.Float64 }
		if val > 0 { points = append(points, analytics.DailyValuation{Date: snaps[i].Date, Value: val}) }
	}
	riskScore := 50
	riskDetail := "insufficient data"
	if len(points) > 60 {
		metrics := analytics.ComputeRiskMetrics(points, 0.03, points[len(points)-1].Value)
		sharpe := metrics.SharpeRatio
		if sharpe >= 1.0 { riskScore = 95 } else if sharpe >= 0.5 { riskScore = 80 } else if sharpe >= 0 { riskScore = 60 } else { riskScore = 30 }
		riskDetail = fmt.Sprintf("Sharpe %.2f, Vol %.1f%%", sharpe, metrics.AnnualizedVolatility)
	}

	// 4. Allocation Discipline (25%) — 100 if no targets, score based on max drift if targets set
	allocScore := 75 // default when no targets
	allocDetail := "no targets set"
	targets, _ := h.queries.ListTargetAllocations(ctx)
	if len(targets) > 0 {
		maxDrift := 0.0
		for _, eh := range enriched {
			actual := eh.value / totalValue * 100
			for _, t := range targets {
				if t.SecurityISIN == eh.security.ISIN {
					drift := math.Abs(actual - numericToFloat(t.TargetWeightPct))
					if drift > maxDrift { maxDrift = drift }
				}
			}
		}
		allocScore = 95
		if maxDrift > 2 { allocScore = 80 }
		if maxDrift > 5 { allocScore = 60 }
		if maxDrift > 10 { allocScore = 35 }
		allocDetail = fmt.Sprintf("max drift %.1f%%", maxDrift)
	}

	// 5. Income Stability (15%) — based on dividend consistency
	incScore := 70
	incDetail := "no dividend data"
	txns, _ := h.queries.ListTransactions(ctx, db.ListTransactionsParams{Limit: 10000, Offset: 0})
	divMonths := make(map[string]bool)
	for _, txn := range txns {
		if txn.Type == "dividend" { divMonths[txn.Date.Format("2006-01")] = true }
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

// HandleTaxLots returns FIFO lot inventory with per-lot tax impact.
func (h *AnalysisHandler) HandleTaxLots(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	txns, err := h.queries.ListTransactions(ctx, db.ListTransactionsParams{Limit: 10000, Offset: 0})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list transactions: "+err.Error())
		return
	}

	secs, _ := h.queries.ListSecurities(ctx)
	secMap := make(map[string]db.Security)
	names := make(map[string]string)
	equityMap := make(map[string]bool)
	for _, s := range secs {
		secMap[s.ISIN] = s
		names[s.ISIN] = s.Name
		equityMap[s.ISIN] = s.AssetClass == "etf"
	}

	var taxTxns []analytics.TaxTransaction
	for _, txn := range txns {
		isin := ""
		if txn.SecurityISIN.Valid { isin = txn.SecurityISIN.String }
		amt, qty := 0.0, 0.0
		if txn.Amount.Valid { f, _ := txn.Amount.Float64Value(); amt = f.Float64 }
		if txn.Quantity.Valid { f, _ := txn.Quantity.Float64Value(); qty = f.Float64 }
		taxTxns = append(taxTxns, analytics.TaxTransaction{
			Year: txn.Date.Year(), Type: txn.Type, ISIN: isin,
			Name: names[isin], Quantity: qty, Amount: amt, IsEquityFund: equityMap[isin],
		})
	}
	// Reverse to chronological
	for i, j := 0, len(taxTxns)-1; i < j; i, j = i+1, j-1 {
		taxTxns[i], taxTxns[j] = taxTxns[j], taxTxns[i]
	}

	// Get current prices from active holdings
	prices := make(map[string]float64)
	activeISINs := make(map[string]bool)
	enriched, _ := h.loadEnrichedHoldings(ctx)
	for _, eh := range enriched {
		if eh.value > 0 {
			qty := 0.0
			if eh.holding.Quantity.Valid { f, _ := eh.holding.Quantity.Float64Value(); qty = f.Float64 }
			if qty > 0 {
				prices[eh.security.ISIN] = eh.value / qty
				activeISINs[eh.security.ISIN] = true
			}
		}
	}

	lots := analytics.ComputeTaxLots(taxTxns, prices, names, equityMap)

	// Filter out lots for ISINs no longer held (price=0 means sold/no market data)
	filtered := lots[:0]
	for _, lot := range lots {
		if activeISINs[lot.ISIN] || prices[lot.ISIN] > 0 {
			filtered = append(filtered, lot)
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{"lots": filtered})
}

// HandleSellSimulator simulates the tax impact of selling specified EUR amounts.
func (h *AnalysisHandler) HandleSellSimulator(w http.ResponseWriter, r *http.Request) {
	var requests []analytics.SellRequest
	if err := json.NewDecoder(r.Body).Decode(&requests); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}

	ctx := r.Context()
	txns, err := h.queries.ListTransactions(ctx, db.ListTransactionsParams{Limit: 10000, Offset: 0})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list transactions: "+err.Error())
		return
	}

	secs, _ := h.queries.ListSecurities(ctx)
	names := make(map[string]string)
	equityMap := make(map[string]bool)
	for _, s := range secs {
		names[s.ISIN] = s.Name
		equityMap[s.ISIN] = s.AssetClass == "etf"
	}

	var taxTxns []analytics.TaxTransaction
	for _, txn := range txns {
		isin := ""
		if txn.SecurityISIN.Valid { isin = txn.SecurityISIN.String }
		amt, qty := 0.0, 0.0
		if txn.Amount.Valid { f, _ := txn.Amount.Float64Value(); amt = f.Float64 }
		if txn.Quantity.Valid { f, _ := txn.Quantity.Float64Value(); qty = f.Float64 }
		taxTxns = append(taxTxns, analytics.TaxTransaction{
			Year: txn.Date.Year(), Type: txn.Type, ISIN: isin,
			Name: names[isin], Quantity: qty, Amount: amt, IsEquityFund: equityMap[isin],
		})
	}
	for i, j := 0, len(taxTxns)-1; i < j; i, j = i+1, j-1 {
		taxTxns[i], taxTxns[j] = taxTxns[j], taxTxns[i]
	}

	prices := make(map[string]float64)
	activeISINs := make(map[string]bool)
	enriched, _ := h.loadEnrichedHoldings(ctx)
	for _, eh := range enriched {
		if eh.value > 0 {
			qty := 0.0
			if eh.holding.Quantity.Valid { f, _ := eh.holding.Quantity.Float64Value(); qty = f.Float64 }
			if qty > 0 {
				prices[eh.security.ISIN] = eh.value / qty
				activeISINs[eh.security.ISIN] = true
			}
		}
	}

	allLots := analytics.ComputeTaxLots(taxTxns, prices, names, equityMap)
	// Filter to active
	var lots []analytics.TaxLot
	for _, lot := range allLots {
		if activeISINs[lot.ISIN] {
			lots = append(lots, lot)
		}
	}

	// Church tax (Kirchensteuer) surcharge on the Abgeltungssteuer. 0 (no
	// church) is the safe default; 0.08 (BY/BW) and 0.09 (other) are the
	// real-world configurable values. Read from query so the simulator can
	// be re-run without persisting state server-side.
	churchTaxRate := 0.0
	if v := r.URL.Query().Get("church_tax"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			churchTaxRate = f
		}
	}
	results, totalTax, totalProceeds := analytics.SimulateSell(lots, requests, churchTaxRate)

	writeJSON(w, http.StatusOK, map[string]any{
		"results":              results,
		"total_tax":            totalTax,
		"total_proceeds":       totalProceeds,
		"effective_rate":       math.Round(analytics.SellTaxRate(churchTaxRate)*100000) / 100000,
		"church_tax_rate":      churchTaxRate,
	})
}

// HandleLossPots returns the German Aktienverlusttopf / allgemeiner Verlusttopf per year.
func (h *AnalysisHandler) HandleLossPots(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	txns, err := h.queries.ListTransactions(ctx, db.ListTransactionsParams{Limit: 10000, Offset: 0})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list transactions: "+err.Error())
		return
	}

	secs, _ := h.queries.ListSecurities(ctx)
	equityMap := make(map[string]bool)
	for _, s := range secs {
		equityMap[s.ISIN] = s.AssetClass == "etf" // simplified: ETFs are equity
	}

	var taxTxns []analytics.TaxTransaction
	for _, txn := range txns {
		isin := ""
		if txn.SecurityISIN.Valid { isin = txn.SecurityISIN.String }
		amt, qty := 0.0, 0.0
		if txn.Amount.Valid { f, _ := txn.Amount.Float64Value(); amt = f.Float64 }
		if txn.Quantity.Valid { f, _ := txn.Quantity.Float64Value(); qty = f.Float64 }
		taxTxns = append(taxTxns, analytics.TaxTransaction{
			Year: txn.Date.Year(), Type: txn.Type, ISIN: isin,
			Quantity: qty, Amount: amt, IsEquityFund: equityMap[isin],
		})
	}
	// Reverse to chronological
	for i, j := 0, len(taxTxns)-1; i < j; i, j = i+1, j-1 {
		taxTxns[i], taxTxns[j] = taxTxns[j], taxTxns[i]
	}

	pots := analytics.ComputeLossPots(taxTxns)

	// Per-account loss pots for current year (cross-broker netting)
	accts, _ := h.queries.ListAccounts(ctx)
	acctNames := make(map[string]string)
	for _, a := range accts {
		acctNames[a.ID.String()] = a.Name
	}

	currentYear := time.Now().Year()
	type acctPot struct {
		Account      string  `json:"account"`
		EquityGains  float64 `json:"equity_gains"`
		EquityLosses float64 `json:"equity_losses"`
		GeneralGains float64 `json:"general_gains"`
		GeneralLosses float64 `json:"general_losses"`
	}
	acctPots := make(map[string]*acctPot)
	// Track cost basis per ISIN
	type lot struct{ qty, totalCost float64 }
	holdings := make(map[string]*lot)

	for i := len(txns) - 1; i >= 0; i-- { // chronological
		txn := txns[i]
		isin := ""
		if txn.SecurityISIN.Valid { isin = txn.SecurityISIN.String }
		amt, qty := 0.0, 0.0
		if txn.Amount.Valid { f, _ := txn.Amount.Float64Value(); amt = f.Float64 }
		if txn.Quantity.Valid { f, _ := txn.Quantity.Float64Value(); qty = f.Float64 }
		acctID := txn.AccountID.String()

		switch txn.Type {
		case "buy", "savings_plan", "transfer":
			if isin == "" { continue }
			l, ok := holdings[isin]; if !ok { l = &lot{}; holdings[isin] = l }
			l.qty += qty; l.totalCost += amt
		case "transfer_out":
			if isin == "" { continue }
			if l := holdings[isin]; l != nil && l.qty > 0 {
				avg := l.totalCost / l.qty; l.qty -= qty; l.totalCost -= qty * avg
			}
		case "sell":
			if isin == "" || txn.Date.Year() != currentYear { continue }
			l := holdings[isin]; if l == nil || l.qty <= 0 { continue }
			avg := l.totalCost / l.qty
			pl := amt - qty*avg
			l.qty -= qty; l.totalCost -= qty * avg
			if equityMap[isin] { pl *= (1 - analytics.TeilfreistellungEquity) }
			ap, ok := acctPots[acctID]; if !ok { ap = &acctPot{Account: acctNames[acctID]}; acctPots[acctID] = ap }
			if equityMap[isin] {
				if pl >= 0 { ap.EquityGains += pl } else { ap.EquityLosses += pl }
			} else {
				if pl >= 0 { ap.GeneralGains += pl } else { ap.GeneralLosses += pl }
			}
		case "dividend":
			if txn.Date.Year() != currentYear { continue }
			divAmt := amt
			if equityMap[isin] { divAmt *= (1 - analytics.TeilfreistellungEquity) }
			ap, ok := acctPots[acctID]; if !ok { ap = &acctPot{Account: acctNames[acctID]}; acctPots[acctID] = ap }
			ap.GeneralGains += divAmt
		case "interest":
			if txn.Date.Year() != currentYear { continue }
			ap, ok := acctPots[acctID]; if !ok { ap = &acctPot{Account: acctNames[acctID]}; acctPots[acctID] = ap }
			ap.GeneralGains += amt
		}
	}

	var perAccount []acctPot
	for _, ap := range acctPots {
		ap.EquityGains = math.Round(ap.EquityGains*100)/100
		ap.EquityLosses = math.Round(ap.EquityLosses*100)/100
		ap.GeneralGains = math.Round(ap.GeneralGains*100)/100
		ap.GeneralLosses = math.Round(ap.GeneralLosses*100)/100
		perAccount = append(perAccount, *ap)
	}

	// Cross-broker netting: check if losses at one broker could offset gains at another
	totalEquityLoss, totalEquityGain := 0.0, 0.0
	totalGeneralLoss, totalGeneralGain := 0.0, 0.0
	for _, ap := range perAccount {
		totalEquityLoss += ap.EquityLosses
		totalEquityGain += ap.EquityGains
		totalGeneralLoss += ap.GeneralLosses
		totalGeneralGain += ap.GeneralGains
	}
	crossBrokerSaving := 0.0
	if len(perAccount) > 1 {
		// If any broker has losses and another has gains in the same pot, filing Anlage KAP helps
		offsetableEquity := math.Min(math.Abs(totalEquityLoss), totalEquityGain)
		offsetableGeneral := math.Min(math.Abs(totalGeneralLoss), totalGeneralGain)
		crossBrokerSaving = (offsetableEquity + offsetableGeneral) * analytics.EffectiveTaxRate
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"years":              pots,
		"per_account":        perAccount,
		"cross_broker_saving": math.Round(crossBrokerSaving*100)/100,
		"file_anlage_kap":    crossBrokerSaving > 0,
	})
}

// HandleFSAStatus returns Freistellungsauftrag (Sparerpauschbetrag) usage per account.
// Accepts ?joint=1 to double the allowance for married couples filing jointly
// (Zusammenveranlagung), which raises the Sparerpauschbetrag to 2000 EUR.
func (h *AnalysisHandler) HandleFSAStatus(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	year := time.Now().Year()
	if y := r.URL.Query().Get("year"); y != "" {
		if v, err := strconv.Atoi(y); err == nil { year = v }
	}
	allowance := analytics.Sparerpauschbetrag
	joint := r.URL.Query().Get("joint") == "1"
	if joint {
		allowance *= 2
	}

	txns, _ := h.queries.ListTransactions(ctx, db.ListTransactionsParams{Limit: 10000, Offset: 0})
	accts, _ := h.queries.ListAccounts(ctx)
	secs, _ := h.queries.ListSecurities(ctx)

	acctNames := make(map[string]string)
	for _, a := range accts {
		acctNames[a.ID.String()] = a.Name
	}
	equityMap := make(map[string]bool)
	for _, s := range secs {
		equityMap[s.ISIN] = s.AssetClass == "etf"
	}

	// Per-account taxable income for the year
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
		if a, ok := incomeByAcct[id]; ok { return a }
		a := &acctIncome{AccountID: id, AccountName: acctNames[id]}
		if a.AccountName == "" { a.AccountName = "Unknown" }
		incomeByAcct[id] = a
		return a
	}

	// Track cost basis for gain computation
	type lot struct{ qty, totalCost float64 }
	holdings := make(map[string]*lot)

	for i := len(txns) - 1; i >= 0; i-- { // chronological
		txn := txns[i]
		isin := ""
		if txn.SecurityISIN.Valid { isin = txn.SecurityISIN.String }
		amt := 0.0
		if txn.Amount.Valid { f, _ := txn.Amount.Float64Value(); amt = f.Float64 }
		qty := 0.0
		if txn.Quantity.Valid { f, _ := txn.Quantity.Float64Value(); qty = f.Float64 }
		acctID := txn.AccountID.String()

		switch txn.Type {
		case "buy", "savings_plan", "transfer":
			if isin == "" { continue }
			l, ok := holdings[isin]
			if !ok { l = &lot{}; holdings[isin] = l }
			l.qty += qty; l.totalCost += amt

		case "transfer_out":
			if isin == "" { continue }
			l := holdings[isin]
			if l != nil && l.qty > 0 {
				avg := l.totalCost / l.qty
				l.qty -= qty; l.totalCost -= qty * avg
			}

		case "sell":
			if isin == "" || txn.Date.Year() != year { continue }
			l := holdings[isin]
			if l == nil || l.qty <= 0 { continue }
			avg := l.totalCost / l.qty
			gain := amt - qty*avg
			l.qty -= qty; l.totalCost -= qty * avg
			// Apply Teilfreistellung
			if equityMap[isin] && gain > 0 {
				gain *= (1 - analytics.TeilfreistellungEquity)
			}
			if gain > 0 {
				getAcct(acctID).Gains += gain
			}

		case "dividend":
			if txn.Date.Year() != year { continue }
			divAmt := amt
			if equityMap[isin] {
				divAmt *= (1 - analytics.TeilfreistellungEquity)
			}
			getAcct(acctID).Dividends += divAmt

		case "interest":
			if txn.Date.Year() != year { continue }
			getAcct(acctID).Interest += amt
		}
	}

	var accounts []acctIncome
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
	var recs []recommendation
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

	var results []altResult
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
	txns, err := h.queries.ListTransactions(ctx, db.ListTransactionsParams{Limit: 10000, Offset: 0})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list transactions: "+err.Error())
		return
	}

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
		cp := ""
		if txn.Counterparty.Valid { cp = txn.Counterparty.String }
		ref := ""
		if txn.Reference.Valid { ref = txn.Reference.String }
		amt := 0.0
		if txn.Amount.Valid { f, _ := txn.Amount.Float64Value(); amt = f.Float64 }

		cat := categorize(cp, ref, txn.Type)
		// Use user-assigned category if set
		if txn.Category.Valid && txn.Category.String != "" {
			cat = txn.Category.String
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

	var monthlySeries []monthlySpend
	for _, m := range months {
		ms := monthMap[m]
		ms.Net = ms.Income - ms.Expenses
		monthlySeries = append(monthlySeries, *ms)
	}

	// Build category totals
	numMonths := float64(len(months))
	if numMonths < 1 { numMonths = 1 }
	var categories []categoryTotal
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

// HandleTaxCalendar generates a 12-month German tax compliance calendar
// with auto-computed amounts based on portfolio data.
func (h *AnalysisHandler) HandleTaxCalendar(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	now := time.Now()
	year := now.Year()

	type calendarEvent struct {
		Month       int     `json:"month"` // 1-12
		Date        string  `json:"date"`  // YYYY-MM-DD or YYYY-MM
		Title       string  `json:"title"`
		Description string  `json:"description"`
		Amount      float64 `json:"amount,omitempty"` // EUR amount if applicable
		Category    string  `json:"category"`          // vorabpauschale, steuererklaerung, fsa, verlust, harvest, schenkung
		Urgency     string  `json:"urgency"`           // info, action, deadline
		ActionURL   string  `json:"action_url,omitempty"`
	}
	var events []calendarEvent

	// Load data for amount computation
	enriched, _ := h.loadEnrichedHoldings(ctx)
	txns, _ := h.queries.ListTransactions(ctx, db.ListTransactionsParams{Limit: 10000, Offset: 0})
	secs, _ := h.queries.ListSecurities(ctx)
	equityMap := make(map[string]bool)
	for _, s := range secs {
		equityMap[s.ISIN] = s.AssetClass == "etf"
	}

	// --- 1. Vorabpauschale (Jan 2) ---
	// Compute from ETF holdings at year start
	etfValueTotal := 0.0
	for _, eh := range enriched {
		if equityMap[eh.security.ISIN] {
			etfValueTotal += eh.value
		}
	}
	basiszins := analytics.BasiszinsByYear[year]
	if basiszins <= 0 {
		basiszins = 2.5
	}
	basisertrag := etfValueTotal * basiszins / 100 * 0.7
	vpTax := basisertrag * (1 - analytics.TeilfreistellungEquity) * analytics.EffectiveTaxRate
	if vpTax > 10 {
		events = append(events, calendarEvent{
			Month: 1, Date: fmt.Sprintf("%d-01-02", year),
			Title:       "Vorabpauschale fällig",
			Description: fmt.Sprintf("Steuer auf Basisertrag von %s EUR ETF-Vermögen. Verrechnungskonto muss gedeckt sein.", fmtEUR(etfValueTotal)),
			Amount:      math.Round(vpTax*100) / 100,
			Category:    "vorabpauschale", Urgency: "deadline",
			ActionURL:   "/analysis",
		})
	}

	// --- 2. Freistellungsauftrag Review (Jan) ---
	// Check if current FSA allocation is optimal
	events = append(events, calendarEvent{
		Month: 1, Date: fmt.Sprintf("%d-01-15", year),
		Title:       "Freistellungsauftrag prüfen",
		Description: "Prüfen, ob der FSA (1.000 EUR Sparerpauschbetrag) optimal auf Depots verteilt ist.",
		Amount:      analytics.Sparerpauschbetrag,
		Category:    "fsa", Urgency: "action",
		ActionURL:   "/analysis",
	})

	// --- 3. Steuererklärung deadline (Jul 31 or Sep 30 with Steuerberater) ---
	events = append(events, calendarEvent{
		Month: 7, Date: fmt.Sprintf("%d-07-31", year),
		Title:       fmt.Sprintf("Steuererklärung %d Abgabe", year-1),
		Description: fmt.Sprintf("Einkommensteuererklärung %d einreichen. Mit Steuerberater: 28.02.%d.", year-1, year+1),
		Category:    "steuererklaerung", Urgency: "deadline",
		ActionURL:   "/analysis",
	})

	// --- 4. Verlustbescheinigung beantragen (Nov 15-Dec 15) ---
	events = append(events, calendarEvent{
		Month: 12, Date: fmt.Sprintf("%d-12-15", year),
		Title:       "Verlustbescheinigung beantragen",
		Description: "Bei der Bank Verlustbescheinigung für die Steuererklärung beantragen (Frist: 15.12.).",
		Category:    "verlust", Urgency: "deadline",
	})

	// --- 5. Tax-Loss Harvesting Window (Q4) ---
	// Identify positions with unrealized losses > 500 EUR
	names := make(map[string]string)
	for _, s := range secs {
		names[s.ISIN] = s.Name
	}
	var taxTxns []analytics.TaxTransaction
	for _, txn := range txns {
		isin := ""
		if txn.SecurityISIN.Valid {
			isin = txn.SecurityISIN.String
		}
		amt, qty := 0.0, 0.0
		if txn.Amount.Valid {
			f, _ := txn.Amount.Float64Value()
			amt = f.Float64
		}
		if txn.Quantity.Valid {
			f, _ := txn.Quantity.Float64Value()
			qty = f.Float64
		}
		taxTxns = append(taxTxns, analytics.TaxTransaction{
			Year: txn.Date.Year(), Type: txn.Type, ISIN: isin,
			Name: names[isin], Quantity: qty, Amount: amt, IsEquityFund: equityMap[isin],
		})
	}
	for i, j := 0, len(taxTxns)-1; i < j; i, j = i+1, j-1 {
		taxTxns[i], taxTxns[j] = taxTxns[j], taxTxns[i]
	}
	prices := make(map[string]float64)
	for _, eh := range enriched {
		if eh.value > 0 {
			qty := numericToFloat(eh.holding.Quantity)
			if qty > 0 {
				prices[eh.security.ISIN] = eh.value / qty
			}
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
	for _, lp := range lossByISIN {
		if lp.loss < -500 {
			totalHarvestable += math.Abs(lp.loss)
		}
	}
	if totalHarvestable > 500 {
		saving := totalHarvestable * 0.7 * analytics.EffectiveTaxRate
		events = append(events, calendarEvent{
			Month: 10, Date: fmt.Sprintf("%d-10-01", year),
			Title:       "Tax-Loss Harvesting Fenster",
			Description: fmt.Sprintf("%s EUR an Verlusten realisierbar. Potenzielle Steuerersparnis: ~%s EUR.", fmtEUR(totalHarvestable), fmtEUR(saving)),
			Amount:      math.Round(saving),
			Category:    "harvest", Urgency: "action",
			ActionURL:   "/analysis",
		})
	}

	// --- 6. FSA Mid-Year Check (Jul) ---
	// Check YTD income vs FSA usage
	ytdIncome := 0.0
	for _, txn := range txns {
		if txn.Date.Year() != year {
			continue
		}
		if txn.Type == "dividend" || txn.Type == "interest" {
			if txn.Amount.Valid {
				f, _ := txn.Amount.Float64Value()
				ytdIncome += f.Float64
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
				Title:       "FSA-Nutzung unter 80%%",
				Description: fmt.Sprintf("Voraussichtlich nur %s EUR von 1.000 EUR Sparerpauschbetrag genutzt. ~%s EUR verfallen.", fmtEUR(projected), fmtEUR(unused)),
				Amount:      math.Round(unused * analytics.EffectiveTaxRate),
				Category:    "fsa", Urgency: "action",
				ActionURL:   "/analysis",
			})
		}
	}

	// --- 7. Schenkung Window (Year-round) ---
	events = append(events, calendarEvent{
		Month: 3, Date: fmt.Sprintf("%d-03-01", year),
		Title:       "Schenkungsfreibetrag prüfen",
		Description: "Alle 10 Jahre: Kinder 400K, Ehepartner 500K, Enkel 200K steuerfrei. Regelmäßige Schenkungen optimal nutzen.",
		Category:    "schenkung", Urgency: "info",
	})

	// --- 8. Year-End Portfolio Review (Dec) ---
	events = append(events, calendarEvent{
		Month: 12, Date: fmt.Sprintf("%d-12-01", year),
		Title:       "Jahresabschluss Portfolio-Review",
		Description: "Rebalancing, Sparplan-Anpassung, Anlage KAP Vorbereitung für das Steuerjahr.",
		Category:    "steuererklaerung", Urgency: "action",
		ActionURL:   "/portfolio",
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
	if err != nil || len(snaps) < 30 {
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
	if len(points) < 30 {
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
	drawdownPct := (current - peak) / peak * 100

	// Compute 30-day volatility
	returns := make([]float64, 0, 30)
	for i := len(points) - 30; i < len(points); i++ {
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
	vol30d := math.Sqrt(variance) * math.Sqrt(252) * 100

	// Historical drawdown recovery analysis
	type drawdownEpisode struct {
		FromDate     string  `json:"from_date"`
		TroughDate   string  `json:"trough_date"`
		RecoveryDate string  `json:"recovery_date"`
		MaxDraw      float64 `json:"max_drawdown_pct"`
		RecoveryDays int     `json:"recovery_days"`
	}
	var episodes []drawdownEpisode
	// Find past drawdown episodes (>5%)
	localPeak := points[0].Value
	localPeakDate := points[0].Date
	inDrawdown := false
	var epStart time.Time
	trough := localPeak
	troughDate := localPeakDate
	for _, p := range points {
		if p.Value > localPeak {
			if inDrawdown {
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
	now := time.Now()
	year := now.Year()

	accts, _ := h.queries.ListAccounts(ctx)
	txns, _ := h.queries.ListTransactions(ctx, db.ListTransactionsParams{Limit: 10000, Offset: 0})
	enriched, _ := h.loadEnrichedHoldings(ctx)

	// Portfolio total and annual return estimate
	portfolioValue := 0.0
	for _, eh := range enriched {
		portfolioValue += eh.value
	}
	annualReturn := 0.07 // 7% nominal

	// --- 1. Cash Drag Analysis ---
	type accountCashDrag struct {
		AccountName    string  `json:"account_name"`
		AccountType    string  `json:"account_type"`
		CashBalance    float64 `json:"cash_balance"`
		OpportunityCost float64 `json:"opportunity_cost_annual"`
	}
	var cashDragAccounts []accountCashDrag
	totalCash := 0.0

	for _, acc := range accts {
		if !acc.IsActive {
			continue
		}
		bal, err := h.queries.GetCashBalance(ctx, acc.ID)
		if err != nil {
			continue
		}
		cash := numericToFloat(bal)
		if cash <= 0 {
			continue
		}
		totalCash += cash
		oc := cash * annualReturn
		cashDragAccounts = append(cashDragAccounts, accountCashDrag{
			AccountName: acc.Name, AccountType: acc.Type,
			CashBalance: math.Round(cash*100) / 100,
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
			amt = f.Float64
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
				ytdIncome += f.Float64
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
	var monthlySeries []monthCost
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
	txns, _ := h.queries.ListTransactions(ctx, db.ListTransactionsParams{Limit: 10000, Offset: 0})
	now := time.Now()

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
		fee, _ := txn.Fee.Float64Value()
		amt := 0.0
		if txn.Amount.Valid {
			f, _ := txn.Amount.Float64Value()
			amt = f.Float64
		}
		if fee.Float64 > 0 && amt > 0 {
			feePct := fee.Float64 / amt * 100
			if feePct > 1 && fee.Float64 > 10 {
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
					Title: fmt.Sprintf("High fee: %.2f EUR (%.1f%%) on %s", fee.Float64, feePct, name),
					Detail: fmt.Sprintf("Transaction on %s for %s EUR had %.2f EUR fee.", txn.Date.Format("2006-01-02"), fmtEUR(amt), fee.Float64),
					ISIN: isin,
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
				Title: fmt.Sprintf("Possible duplicate: %s (%d ISINs)", name, len(isins)),
				Detail: fmt.Sprintf("ISINs: %s. These may be the same security listed separately.", strings.Join(isins, ", ")),
			})
		}
	}

	// --- 5. Concentration Drift (single position >30% from pure appreciation) ---
	totalValue := 0.0
	for _, eh := range enriched {
		totalValue += eh.value
	}
	if totalValue > 0 {
		for _, eh := range enriched {
			pct := eh.value / totalValue * 100
			if pct > 30 {
				// Check if drift is from appreciation vs intentional allocation
				qty := numericToFloat(eh.holding.Quantity)
				avg := numericToFloat(eh.holding.AvgCostBasis)
				costValue := qty * avg
				costPct := costValue / totalValue * 100
				if pct-costPct > 5 {
					issues = append(issues, issue{
						Type: "concentration_drift", Severity: "warning",
						Title: fmt.Sprintf("Concentration drift: %s at %.0f%%", eh.security.Name, pct),
						Detail: fmt.Sprintf("Cost-basis weight was %.0f%%, now %.0f%% from appreciation. Consider rebalancing.", costPct, pct),
						ISIN: eh.security.ISIN,
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
		writeJSON(w, http.StatusOK, map[string]any{"scenarios": scenarios, "portfolio_impact": nil})
		return
	}

	totalValue := 0.0
	equityValue := 0.0
	for _, eh := range enriched {
		val := eh.value
		totalValue += val
		if eh.security.AssetClass == "etf" || eh.security.AssetClass == "stock" {
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

	// Cash buffer: sum cash balances across all accounts
	cashBuffer := 0.0
	allAccounts, _ := h.queries.ListAccounts(ctx)
	for _, acc := range allAccounts {
		bal, err := h.queries.GetCashBalance(ctx, acc.ID)
		if err == nil && bal.Valid {
			f, _ := bal.Float64Value()
			cashBuffer += f.Float64
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
		isEq := eh.security.AssetClass == "etf" || eh.security.AssetClass == "stock"
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
		Name        string `json:"name"`
		WithoutDCA  int    `json:"without_dca_months"`
		WithDCA     int    `json:"with_dca_months"`
		Acceleration int   `json:"acceleration_months"`
	}, len(scenarios))
	for i, s := range scenarios {
		withoutDCA := s.RecoveryMo
		// With DCA: approximate recovery by simulating monthly contributions during drawdown recovery
		// Each month of DCA buys at lower prices, accelerating recovery by ~20-30%
		drawdownAbs := math.Abs(s.IndexReturn) / 100
		dcaBoost := monthlyDCA * float64(withoutDCA) / (totalValue * drawdownAbs)
		withDCA := int(math.Round(float64(withoutDCA) * (1 - math.Min(dcaBoost*0.5, 0.4))))
		if withDCA < 1 { withDCA = 1 }
		dcaRecovery[i] = struct {
			Name        string `json:"name"`
			WithoutDCA  int    `json:"without_dca_months"`
			WithDCA     int    `json:"with_dca_months"`
			Acceleration int   `json:"acceleration_months"`
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
