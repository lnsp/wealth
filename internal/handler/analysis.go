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
	"github.com/lnsp/wealth/internal/cashflow"
	db "github.com/lnsp/wealth/internal/database/generated"
	"github.com/lnsp/wealth/internal/market"
)

// holdingKey returns a matching key for an ETF constituent holding. Real
// ISINs key by ISIN; synthetic IDs (empty, "SYNTH:" or legacy "XX_") key by
// the normalised name, because older SYNTH rows hashed the ETF ISIN into the
// ID and so differ per fund for the same company.
func holdingKey(isin, name string) string {
	if !market.IsSyntheticConstituentID(isin) {
		return isin
	}
	return "name:" + market.ConstituentNameKey(name)
}

// aggregateByISIN merges positions in the same security held in several
// accounts into one row (values summed), so per-fund views list each fund
// once and never compare a fund with itself.
func aggregateByISIN(enriched []enrichedHolding) []enrichedHolding {
	idx := make(map[string]int, len(enriched))
	out := make([]enrichedHolding, 0, len(enriched))
	for _, eh := range enriched {
		if i, ok := idx[eh.security.ISIN]; ok {
			out[i].value += eh.value
			out[i].costValue += eh.costValue
			continue
		}
		idx[eh.security.ISIN] = len(out)
		out = append(out, eh)
	}
	return out
}

// isLookThroughFund reports whether a security is a fund whose constituents
// may be on record: ETFs, other funds and bond funds (the metadata job
// fetches fund data for all three).
func isLookThroughFund(assetClass string) bool {
	return assetClass == "etf" || assetClass == "fund" || assetClass == "bond"
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

// buildExternalFlowsForRisk loads external cashflows at total-net-worth scope
// (+deposit/in-kind contribution, -withdrawal, plus implicit cash clamp deltas)
// in EUR, for series of snapshot totals. Transactions linked to a tracked asset
// or loan are skipped: at net-worth scope they swap cash for equity. The Risk
// tab, volatility callout and benchmark replay measure the investable scope and
// use buildInvestableFlowsForRisk instead.
func (h *AnalysisHandler) buildExternalFlowsForRisk(ctx context.Context) []analytics.CashFlow {
	return h.loadRiskFlows(ctx, false)
}

// buildInvestableFlowsForRisk loads the external cashflows of the investable
// scope (cash + securities = snapshot cash_component + investment_component,
// see investableRiskSeries). It is buildExternalFlowsForRisk plus every payment
// to or from a tracked asset or loan, because that money leaves or enters the
// scope.
func (h *AnalysisHandler) buildInvestableFlowsForRisk(ctx context.Context) []analytics.CashFlow {
	return h.loadRiskFlows(ctx, true)
}

func (h *AnalysisHandler) loadRiskFlows(ctx context.Context, investable bool) []analytics.CashFlow {
	txns, err := listAllTransactionsRows(ctx, h.queries)
	if err != nil || len(txns) == 0 {
		return nil
	}
	fxRates := loadFXRatesMap(ctx, h.queries)
	histFX := loadHistoricalFXRates(ctx, h.queries)
	accts, _ := h.queries.ListAccounts(ctx)
	var skip map[uuid.UUID]bool
	if !investable {
		physHoldings, _ := assets.Load(ctx, h.queries)
		skip = physHoldings.LinkedIDs()
	}
	txnsChrono := make([]db.ListTransactionsRow, len(txns))
	for i := range txns {
		txnsChrono[i] = txns[len(txns)-1-i]
	}
	return externalRiskFlows(txnsChrono, accts, histFX, fxRates, skip)
}

// externalRiskFlows turns chronological transactions into external cashflows
// in EUR: deposits and in-kind receipts (RSU vests, transfers from untracked
// depots) at +value, withdrawals and unmatched outgoing depot transfers at
// -value, plus implicit cash clamp deltas. Own-depot transfer pairs and the
// transaction IDs in skip are left out. Amounts are taken as magnitudes and
// the sign comes from the type, because some imports store withdrawals as
// negative amounts.
func externalRiskFlows(txnsChrono []db.ListTransactionsRow, accts []db.Account, histFX []market.HistoricalFXRate, fxRates map[string]float64, skip map[uuid.UUID]bool) []analytics.CashFlow {
	var inkind []analytics.InKindTransfer
	for _, t := range txnsChrono {
		if t.SecurityISIN.Valid && (t.Type == "transfer" || t.Type == "transfer_out") {
			inkind = append(inkind, analytics.InKindTransfer{
				ID: t.ID, Date: t.Date, Type: t.Type,
				ISIN: t.SecurityISIN.String, Quantity: numericToFloat(t.Quantity),
			})
		}
	}
	washed := analytics.MatchInKindTransferPairs(inkind)

	impEvents := computeImplicitCashClampEvents(accts, txnsChrono, histFX, fxRates)

	var flows []analytics.CashFlow
	for _, ev := range impEvents {
		flows = append(flows, analytics.CashFlow{Date: ev.Date, Amount: ev.DeltaEUR})
	}
	for _, t := range txnsChrono {
		if _, ok := washed[t.ID]; ok {
			continue
		}
		if skip[t.ID] {
			continue
		}
		amt := math.Abs(convertAmountToEURAtDate(numericToFloat(t.Amount), t.Currency, t.Date, histFX, fxRates))
		inKind := t.SecurityISIN.Valid && (t.Type == "transfer" || t.Type == "transfer_out")
		if amt == 0 && inKind {
			// In-kind move booked without a value: value it at quantity × price.
			amt = math.Abs(convertAmountToEURAtDate(numericToFloat(t.Quantity)*numericToFloat(t.Price), t.Currency, t.Date, histFX, fxRates))
		}
		if amt == 0 {
			continue
		}
		if inKind && t.Type == "transfer_out" {
			// No matching receipt in another tracked depot: the shares leave.
			flows = append(flows, analytics.CashFlow{Date: t.Date, Amount: -amt})
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
//
// Single stocks have no sector source (justETF covers funds only), so their
// sector stays "Unknown"; their country is taken from the ISIN prefix (the
// issuer's country) unless the security is a depositary receipt.
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
		if len(countryWeights) == 0 && eh.security.AssetClass == "stock" {
			if country := stockCountryFromISIN(eh.security.ISIN, eh.security.Name); country != "" {
				countryWeights = map[string]float64{country: 100}
			}
		}
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
			IsFund: isLookThroughFund(eh.security.AssetClass),
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

// isinCountries maps ISIN country prefixes to the country names justETF
// uses (and analytics.CountryToCurrency understands). Offshore domiciles
// (KY, BM, JE, VG, ...) are left out on purpose: they say little about where
// the business is, so those stocks stay "Unknown".
var isinCountries = map[string]string{
	"US": "United States", "CA": "Canada", "MX": "Mexico", "BR": "Brazil",
	"DE": "Germany", "FR": "France", "NL": "Netherlands", "IT": "Italy",
	"ES": "Spain", "BE": "Belgium", "AT": "Austria", "FI": "Finland",
	"IE": "Ireland", "PT": "Portugal", "LU": "Luxembourg", "GR": "Greece",
	"GB": "United Kingdom", "CH": "Switzerland", "SE": "Sweden",
	"DK": "Denmark", "NO": "Norway", "PL": "Poland", "JP": "Japan",
	"CN": "China", "HK": "Hong Kong", "TW": "Taiwan", "KR": "South Korea",
	"IN": "India", "SG": "Singapore", "AU": "Australia", "NZ": "New Zealand",
	"IL": "Israel", "ZA": "South Africa",
}

// stockCountryFromISIN returns the issuer country of a directly held share
// from its ISIN prefix, or "" when unknown: synthetic IDs, offshore
// domiciles, and depositary receipts (an ADR's US ISIN says nothing about
// the company behind it).
func stockCountryFromISIN(isin, name string) string {
	if len(isin) != 12 {
		return ""
	}
	tokens := strings.FieldsFunc(strings.ToUpper(name), func(r rune) bool {
		return !(r >= 'A' && r <= 'Z') && !(r >= '0' && r <= '9')
	})
	for _, tok := range tokens {
		switch {
		case tok == "ADR" || tok == "ADRS" || tok == "ADS" || tok == "GDR" || tok == "GDRS",
			strings.HasPrefix(tok, "DEPOSITAR"), strings.HasPrefix(tok, "DEPOSITOR"):
			return ""
		}
	}
	return isinCountries[strings.ToUpper(isin[:2])]
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

	if !isLookThroughFund(sec.AssetClass) {
		writeError(w, http.StatusBadRequest, "security is not a fund")
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
	coverage := 0.0
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
		// Synthetic IDs ("SYNTH:<hash>", legacy "XX_") are not ISINs.
		if market.IsSyntheticConstituentID(entry.ISIN) {
			entry.ISIN = ""
		}
		if eh.Sector.Valid {
			entry.Sector = eh.Sector.String
		}
		if eh.Country.Valid {
			entry.Country = eh.Country.String
		}
		coverage += weight
		holdings = append(holdings, entry)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"etf_isin": sec.ISIN,
		"etf_name": sec.Name,
		"holdings": holdings,
		// Share of the fund the listed constituents represent (justETF lists
		// the top 10 only).
		"coverage_pct": math.Round(coverage*100) / 100,
	})
}

// HandleTreemap returns the portfolio → fund → (sector →) company treemap.
// Each fund node carries its full market value; the part not covered by its
// listed constituents is a "Rest of fund" tile. justETF's constituent table
// has no sector column, so the sector level is used only when every listed
// constituent has a sector on record; otherwise constituents hang directly
// under the fund instead of all landing in "Other".
func (h *AnalysisHandler) HandleTreemap(w http.ResponseWriter, r *http.Request) {
	enriched, err := h.loadEnrichedHoldings(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "load holdings: "+err.Error())
		return
	}

	var etfs []analytics.ETFForTreemap
	for _, eh := range aggregateByISIN(enriched) {
		if eh.value <= 0 {
			continue
		}
		leaf := analytics.ETFForTreemap{Name: eh.security.Name, MarketValue: eh.value}
		if !isLookThroughFund(eh.security.AssetClass) {
			etfs = append(etfs, leaf)
			continue
		}

		etfHoldings, err := h.queries.ListETFHoldings(r.Context(), eh.security.ISIN)
		if err != nil || len(etfHoldings) == 0 {
			etfs = append(etfs, leaf)
			continue
		}

		type constituent struct {
			name, sector string
			value        float64
		}
		var order []string
		byKey := make(map[string]*constituent)
		allHaveSector := true
		for _, etfH := range etfHoldings {
			weight := numericToFloat(etfH.WeightPct)
			if weight <= 0 {
				continue
			}
			key := holdingKey(etfH.HoldingISIN, etfH.HoldingName)
			c, ok := byKey[key]
			if !ok {
				c = &constituent{name: etfH.HoldingName}
				byKey[key] = c
				order = append(order, key)
			}
			c.value += eh.value * weight / 100
			if etfH.Sector.Valid && strings.TrimSpace(etfH.Sector.String) != "" {
				c.sector = etfH.Sector.String
			} else if c.sector == "" {
				allHaveSector = false
			}
		}

		item := leaf
		for _, key := range order {
			c := byKey[key]
			node := analytics.TreemapNode{Name: c.name, Value: c.value}
			if allHaveSector {
				if item.HoldingsBySector == nil {
					item.HoldingsBySector = make(map[string][]analytics.TreemapNode)
				}
				item.HoldingsBySector[c.sector] = append(item.HoldingsBySector[c.sector], node)
			} else {
				item.Constituents = append(item.Constituents, node)
			}
		}
		etfs = append(etfs, item)
	}

	tree := analytics.BuildAllocationTreemap(etfs)
	if tree == nil {
		tree = []analytics.TreemapNode{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"children": tree})
}

// constituentSet is one fund's stored constituents keyed for matching.
type constituentSet struct {
	weights  map[string]float64 // holdingKey -> weight % of the fund
	names    map[string]string  // holdingKey -> display name
	coverage float64            // sum of the listed weights, % of the fund
	count    int                // number of listed constituents
}

// loadConstituents reads a fund's stored constituents. directByName maps a
// directly held stock's ConstituentNameKey to its ISIN, so a name-keyed
// constituent of the same company merges with the direct position.
func (h *AnalysisHandler) loadConstituents(ctx context.Context, isin string, directByName map[string]string) (constituentSet, error) {
	rows, err := h.queries.ListETFHoldings(ctx, isin)
	if err != nil {
		return constituentSet{}, err
	}
	set := constituentSet{weights: map[string]float64{}, names: map[string]string{}}
	for _, row := range rows {
		weight := numericToFloat(row.WeightPct)
		if weight <= 0 {
			continue
		}
		key := holdingKey(row.HoldingISIN, row.HoldingName)
		if strings.HasPrefix(key, "name:") {
			if direct, ok := directByName[strings.TrimPrefix(key, "name:")]; ok {
				key = direct
			}
		}
		set.weights[key] += weight
		if _, ok := set.names[key]; !ok {
			set.names[key] = row.HoldingName
		}
		set.coverage += weight
		set.count++
	}
	return set, nil
}

// HandleTopHoldings ranks the companies with the largest share of the whole
// portfolio: direct stocks at 100 % of their value plus each fund's listed
// constituents (justETF lists the top 10) weighted by the fund's value. The
// denominator is the total value of all positions, so funds without
// constituent data dilute the shares rather than being ignored.
func (h *AnalysisHandler) HandleTopHoldings(w http.ResponseWriter, r *http.Request) {
	enriched, err := h.loadEnrichedHoldings(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "load holdings: "+err.Error())
		return
	}
	positions := aggregateByISIN(enriched)

	totalValue := 0.0
	directByName := make(map[string]string)
	for _, eh := range positions {
		if eh.value <= 0 {
			continue
		}
		totalValue += eh.value
		if eh.security.AssetClass == "stock" {
			directByName[market.ConstituentNameKey(eh.security.Name)] = eh.security.ISIN
		}
	}

	var inputs []analytics.HoldingWithETF
	holdingNames := make(map[string]string)
	lookedThrough := 0.0 // portfolio value attributed to named companies
	for _, eh := range positions {
		if eh.value <= 0 {
			continue
		}
		if eh.security.AssetClass == "stock" {
			inputs = append(inputs, analytics.HoldingWithETF{ISIN: eh.security.ISIN, Name: eh.security.Name, MarketValue: eh.value})
			holdingNames[eh.security.ISIN] = eh.security.Name
			lookedThrough += eh.value
			continue
		}
		if !isLookThroughFund(eh.security.AssetClass) {
			continue
		}
		set, err := h.loadConstituents(r.Context(), eh.security.ISIN, directByName)
		if err != nil || len(set.weights) == 0 {
			continue
		}
		for key, name := range set.names {
			if _, ok := holdingNames[key]; !ok {
				holdingNames[key] = name
			}
		}
		inputs = append(inputs, analytics.HoldingWithETF{
			ISIN: eh.security.ISIN, Name: eh.security.Name,
			MarketValue: eh.value, ETFHoldings: set.weights,
		})
		lookedThrough += eh.value * math.Min(set.coverage, 100) / 100
	}

	exposure := analytics.ComputeLookThroughExposure(inputs, totalValue)

	type topHolding struct {
		ISIN        string  `json:"isin"`
		Name        string  `json:"name"`
		ExposurePct float64 `json:"exposure_pct"`
	}

	sorted := make([]topHolding, 0, len(exposure))
	for key, pct := range exposure {
		name := key
		if n, ok := holdingNames[key]; ok && n != "" {
			name = n
		}
		isin := key
		if strings.HasPrefix(key, "name:") {
			isin = ""
		}
		sorted = append(sorted, topHolding{ISIN: isin, Name: name, ExposurePct: pct * 100})
	}
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].ExposurePct != sorted[j].ExposurePct {
			return sorted[i].ExposurePct > sorted[j].ExposurePct
		}
		return sorted[i].Name < sorted[j].Name
	})
	if len(sorted) > 20 {
		sorted = sorted[:20]
	}

	coveragePct := 0.0
	if totalValue > 0 {
		coveragePct = lookedThrough / totalValue * 100
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"holdings":     sorted,
		"total_value":  math.Round(totalValue*100) / 100,
		"coverage_pct": math.Round(coveragePct*10) / 10,
	})
}

func (h *AnalysisHandler) HandleAlerts(w http.ResponseWriter, r *http.Request) {
	enriched, err := h.loadEnrichedHoldings(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "load holdings: "+err.Error())
		return
	}

	var etfs []analytics.ETFWithHoldings
	holdingNames := make(map[string]string)
	constituents := make(map[string]map[string]float64)

	for _, eh := range enriched {
		if eh.security.AssetClass != "etf" {
			continue
		}
		if _, done := constituents[eh.security.ISIN]; done {
			continue // same ETF held in another account
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
		constituents[eh.security.ISIN] = weights
		etfs = append(etfs, analytics.ETFWithHoldings{
			ISIN:     eh.security.ISIN,
			Name:     eh.security.Name,
			Holdings: weights,
		})
	}
	holdingsWithETF := concentrationExposureInputs(enriched, constituents, holdingNames)

	alerts := analytics.ComputeConcentrationAlertsWithThresholds(
		etfs, holdingsWithETF, holdingNames, h.overlapWarnPct, h.concentrationWarnPct,
	)
	if alerts == nil {
		alerts = []analytics.Alert{}
	}

	writeJSON(w, http.StatusOK, map[string]any{"alerts": alerts})
}

// concentrationExposureInputs builds the look-through input for single-name
// concentration from every security held, so exposure is a share of the
// total securities value rather than of the ETF sleeve: ETFs with constituent
// data look through to their constituents, directly held stocks and other
// single assets count 100 % towards themselves (keyed like an ETF constituent,
// so a stock held directly and inside an ETF adds up), and funds without
// constituent data still count in the denominator.
func concentrationExposureInputs(enriched []enrichedHolding, constituents map[string]map[string]float64, names map[string]string) []analytics.HoldingWithETF {
	out := make([]analytics.HoldingWithETF, 0, len(enriched))
	for _, eh := range enriched {
		if eh.value <= 0 {
			continue
		}
		weights := constituents[eh.security.ISIN]
		if eh.security.AssetClass != "etf" && eh.security.AssetClass != "fund" {
			key := holdingKey(eh.security.ISIN, eh.security.Name)
			weights = map[string]float64{key: 100}
			if names != nil && names[key] == "" {
				names[key] = eh.security.Name
			}
		}
		out = append(out, analytics.HoldingWithETF{
			ISIN:        eh.security.ISIN,
			Name:        eh.security.Name,
			MarketValue: eh.value,
			ETFHoldings: weights,
		})
	}
	return out
}

// HandleOverlap returns the pairwise overlap of the funds held, one row per
// fund ISIN. The stored constituents are justETF's top holdings only (about
// 10 per fund), so each cell is a lower bound on the true overlap: it counts
// only companies listed for both funds. coverage[i] is the share of fund i
// that its listed constituents represent; the frontend shows it next to the
// matrix so a low value reads as "partial data", not "little overlap".
func (h *AnalysisHandler) HandleOverlap(w http.ResponseWriter, r *http.Request) {
	enriched, err := h.loadEnrichedHoldings(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "load holdings: "+err.Error())
		return
	}

	var etfs []analytics.ETFWithHoldings
	labels := []string{}
	isins := []string{}
	coverage := []float64{}
	counts := []int{}
	for _, eh := range aggregateByISIN(enriched) {
		if !isLookThroughFund(eh.security.AssetClass) {
			continue
		}
		set, err := h.loadConstituents(r.Context(), eh.security.ISIN, nil)
		if err != nil || len(set.weights) == 0 {
			continue
		}
		etfs = append(etfs, analytics.ETFWithHoldings{ISIN: eh.security.ISIN, Name: eh.security.Name, Holdings: set.weights})
		labels = append(labels, eh.security.Name)
		isins = append(isins, eh.security.ISIN)
		coverage = append(coverage, math.Round(math.Min(set.coverage, 100)*10)/10)
		counts = append(counts, set.count)
	}

	// Build overlap matrix via the shared helper — same symmetry + diagonal
	// guarantees the unit tests in internal/analytics/decomposition_test.go
	// already cover.
	if len(etfs) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{"labels": []string{}, "matrix": [][]float64{}, "isins": []string{}, "coverage": []float64{}, "constituent_counts": []int{}})
		return
	}
	matrix := analytics.BuildOverlapMatrix(etfs)
	for i := range matrix {
		for j := range matrix[i] {
			matrix[i][j] = math.Round(matrix[i][j]*100) / 100
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"labels":             labels,
		"isins":              isins,
		"matrix":             matrix,
		"coverage":           coverage,
		"constituent_counts": counts,
	})
}

// sectorHistoryTxn is one security transaction for the sector-drift replay.
type sectorHistoryTxn struct {
	Date    time.Time
	Type    string
	ISIN    string
	Qty     float64 // units, positive
	CostEUR float64 // EUR amount at the trade date, positive
}

// sectorSnapshot is one point of the sector-drift series: sector → % of the
// portfolio's market value at Date.
type sectorSnapshot struct {
	Date    string             `json:"date"`
	Sectors map[string]float64 `json:"sectors"`
}

// buildSectorHistory replays security transactions and values the holdings
// at every month-end from the first trade's month to now (the current,
// partial month is valued at now). Each position is valued at
// priceEUR(isin, monthEnd), the last close on or before that date converted
// at that date's FX; positions without a price fall back to their running
// EUR cost basis. The share of a position not covered by its sector weights
// goes to "Unknown", so every point sums to 100 %. Months with no holdings
// are emitted with no sectors, so the series has no gaps.
func buildSectorHistory(txns []sectorHistoryTxn, now time.Time, priceEUR func(isin string, at time.Time) (float64, bool), sectorWeights map[string]map[string]float64) []sectorSnapshot {
	snapshots := []sectorSnapshot{}
	if len(txns) == 0 {
		return snapshots
	}
	type position struct{ qty, cost float64 }
	positions := make(map[string]*position)
	apply := func(t sectorHistoryTxn) {
		p := positions[t.ISIN]
		switch t.Type {
		case "buy", "savings_plan", "transfer":
			if p == nil {
				p = &position{}
				positions[t.ISIN] = p
			}
			p.qty += t.Qty
			p.cost += t.CostEUR
		case "sell", "transfer_out":
			if p == nil || p.qty <= 0 {
				return
			}
			sold := math.Min(t.Qty, p.qty)
			p.cost -= p.cost / p.qty * sold
			p.qty -= sold
			if p.qty <= 1e-9 {
				delete(positions, t.ISIN)
			}
		}
	}

	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	first := txns[0].Date
	month := time.Date(first.Year(), first.Month(), 1, 0, 0, 0, 0, time.UTC)
	next := 0
	for !month.After(today) {
		end := time.Date(month.Year(), month.Month()+1, 0, 0, 0, 0, 0, time.UTC)
		if end.After(today) {
			end = today
		}
		for next < len(txns) && !txns[next].Date.After(end) {
			apply(txns[next])
			next++
		}

		values := make(map[string]float64, len(positions))
		total := 0.0
		for isin, p := range positions {
			if p.qty <= 1e-9 {
				continue
			}
			v := p.cost
			if price, ok := priceEUR(isin, end); ok && price > 0 {
				v = p.qty * price
			}
			if v <= 0 {
				continue
			}
			values[isin] = v
			total += v
		}
		sectors := make(map[string]float64)
		if total > 0 {
			for isin, v := range values {
				weight := v / total
				covered := 0.0
				for sector, pct := range sectorWeights[isin] {
					if pct <= 0 {
						continue
					}
					sectors[sector] += weight * pct
					covered += pct
				}
				if covered < 100 {
					sectors["Unknown"] += weight * (100 - covered)
				}
			}
		}
		snapshots = append(snapshots, sectorSnapshot{Date: end.Format("2006-01-02"), Sectors: sectors})
		month = addMonthsFirstOfMonth(month, 1)
	}
	return snapshots
}

// HandleSectorHistory computes historical sector allocation by replaying the
// transaction log and valuing the holdings at market prices at every
// month-end up to today (see buildSectorHistory).
func (h *AnalysisHandler) HandleSectorHistory(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	rows, err := listAllTransactionsRows(ctx, h.queries)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list transactions: "+err.Error())
		return
	}

	fxRates := loadFXRatesMap(ctx, h.queries)
	histFX := loadHistoricalFXRates(ctx, h.queries)

	// Chronological security transactions (ListAllTransactions returns DESC).
	var txns []sectorHistoryTxn
	for i := len(rows) - 1; i >= 0; i-- {
		txn := rows[i]
		if !txn.SecurityISIN.Valid || txn.SecurityISIN.String == "" {
			continue
		}
		switch txn.Type {
		case "buy", "savings_plan", "transfer", "sell", "transfer_out":
		default:
			continue
		}
		qty := math.Abs(numericToFloat(txn.Quantity))
		if qty <= 0 {
			continue
		}
		txns = append(txns, sectorHistoryTxn{
			Date:    txn.Date,
			Type:    txn.Type,
			ISIN:    txn.SecurityISIN.String,
			Qty:     qty,
			CostEUR: math.Abs(convertAmountToEURAtDate(numericToFloat(txn.Amount), txn.Currency, txn.Date, histFX, fxRates)),
		})
	}
	if len(txns) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{"history": []any{}, "sectors": []string{}})
		return
	}

	sectorWeights := make(map[string]map[string]float64)
	if secs, err := h.queries.ListSecurities(ctx); err == nil {
		for _, s := range secs {
			sectorWeights[s.ISIN] = analytics.ParseWeights(s.SectorWeights)
		}
	}

	// Price history per ISIN, loaded once (ASC by date).
	priceCache := make(map[string][]db.ListPriceHistoryRow)
	priceEUR := func(isin string, at time.Time) (float64, bool) {
		hist, ok := priceCache[isin]
		if !ok {
			hist, _ = h.queries.ListPriceHistory(ctx, isin)
			priceCache[isin] = hist
		}
		// Last close on or before `at`.
		i := sort.Search(len(hist), func(i int) bool { return hist[i].Date.After(at) }) - 1
		for ; i >= 0; i-- {
			if close := numericToFloat(hist[i].Close); close > 0 {
				return convertAmountToEURAtDate(close, hist[i].Currency, at, histFX, fxRates), true
			}
		}
		return 0, false
	}

	snapshots := buildSectorHistory(txns, time.Now(), priceEUR, sectorWeights)

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

// HandleRisk computes portfolio risk metrics from the cashflow-adjusted daily
// value of cash and securities (the investable scope). Property, vehicles and
// the loans on them are left out: buying a flat or a car, or a new appraisal,
// is not a market return.
func (h *AnalysisHandler) HandleRisk(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	snaps, err := h.queries.ListNetWorthSnapshots(ctx, 3000)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list snapshots: "+err.Error())
		return
	}
	points := investableRiskSeries(snaps)
	if len(points) < 2 {
		writeJSON(w, http.StatusOK, map[string]any{"risk": analytics.RiskMetrics{}})
		return
	}

	currentValue := points[len(points)-1].Value
	adjustedPoints := analytics.BuildCashflowAdjustedSeries(points, h.buildInvestableFlowsForRisk(ctx))
	prepared := analytics.PrepareRiskSeries(adjustedPoints)
	if len(prepared) < 2 {
		writeJSON(w, http.StatusOK, map[string]any{"risk": analytics.RiskMetrics{}})
		return
	}
	windowStart, windowEnd := prepared[0].Date, prepared[len(prepared)-1].Date
	riskFreeRate := analytics.AverageRiskFreeRate(windowStart, windowEnd)

	metrics := analytics.ComputeRiskMetrics(adjustedPoints, riskFreeRate, currentValue)
	setRecordedHigh(metrics, points)
	rolling := analytics.ComputeRollingMetrics(adjustedPoints, riskFreeRate)

	// Benchmark over the same window, in EUR, annualised at its own sampling
	// frequency (the Yahoo fallback is weekly).
	var benchMetrics *analytics.RiskMetrics
	benchPoints := benchmarkRiskWindow(loadBenchmarkPrices(ctx, h.queries, h.benchmarkISIN, h.benchmarkTicker), windowStart, windowEnd)
	if len(benchPoints) > 1 {
		benchMetrics = analytics.ComputeRiskMetrics(benchPoints, riskFreeRate, benchPoints[len(benchPoints)-1].Value)
	}

	result := map[string]any{
		"risk":           metrics,
		"risk_free_rate": math.Round(riskFreeRate*10000) / 100,
	}
	if benchMetrics != nil {
		result["benchmark_risk"] = benchMetrics
	}
	if rolling != nil {
		result["rolling"] = rolling
	}

	writeJSON(w, http.StatusOK, result)
}

// investableRiskSeries turns newest-first snapshots into the chronological
// value of cash and securities (snapshot total without physical assets and
// the loans on them, see investableNetWorth), dropping leading all-zero days.
// Risk, the volatility callout, purchasing power and the benchmark replay are
// all measured on this series with the flows of buildInvestableFlowsForRisk.
func investableRiskSeries(snaps []db.NetWorthSnapshot) []analytics.DailyValuation {
	points := make([]analytics.DailyValuation, 0, len(snaps))
	for i := len(snaps) - 1; i >= 0; i-- {
		points = append(points, analytics.DailyValuation{Date: snaps[i].Date, Value: investableNetWorth(snaps[i])})
	}
	start := 0
	for start < len(points) && points[start].Value <= 0 {
		start++
	}
	return points[start:]
}

// setRecordedHigh replaces the high of the flow-adjusted index (a value the
// portfolio never had) with the highest value actually recorded.
func setRecordedHigh(m *analytics.RiskMetrics, raw []analytics.DailyValuation) {
	if m == nil {
		return
	}
	high, highDate := 0.0, time.Time{}
	for _, p := range raw {
		if p.Value > high {
			high, highDate = p.Value, p.Date
		}
	}
	m.AllTimeHigh = math.Round(high*100) / 100
	m.ATHDate = ""
	if !highDate.IsZero() {
		m.ATHDate = highDate.Format("2006-01-02")
	}
}

// benchmarkRiskWindow keeps the benchmark prices inside the window the
// portfolio metrics cover, so both are measured over the same dates.
func benchmarkRiskWindow(prices []benchmarkPrice, from, to time.Time) []analytics.DailyValuation {
	var out []analytics.DailyValuation
	for _, bp := range prices {
		if bp.price > 0 && !bp.date.Before(from) && !bp.date.After(to) {
			out = append(out, analytics.DailyValuation{Date: bp.date, Value: bp.price})
		}
	}
	return out
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

// costProjectionGrowthPct is the growth rate before costs (% a year) assumed
// when projecting fee drag and the value of switching to a cheaper fund.
const costProjectionGrowthPct = 5.0

// avgGermanFundTER is the benchmark ongoing charge (%) of a typical German
// fund investor that the cost grade compares against.
const avgGermanFundTER = 0.38

// feeDragFV returns what an ongoing charge of terPct % a year costs over
// `years` on `value`, including the growth the deducted fees would have
// earned: V·(1+g)^n − V·((1+g)·(1−t))^n with g = growthPct/100, t = terPct/100.
// Example: 10,000 € at 0.20 % for 30 years at 5 % growth costs 2,519 €, not
// the 600 € a linear 20 €/year suggests.
func feeDragFV(value, terPct, growthPct float64, years int) float64 {
	if value <= 0 || terPct <= 0 || years <= 0 {
		return 0
	}
	g := 1 + growthPct/100
	n := float64(years)
	return value*math.Pow(g, n) - value*math.Pow(g*(1-terPct/100), n)
}

// costGrade grades a fund-weighted ongoing charge (in %), computed over the
// funds with a known charge only.
func costGrade(ter float64) (grade, detail string) {
	switch {
	case ter > 0.60:
		return "D", "High — significantly above average, review fund selection"
	case ter > 0.40:
		return "C", "Above average — consider lower-cost alternatives"
	case ter > 0.30:
		return "B", "Average — close to typical German ETF investor"
	case ter > 0.20:
		return "B+", "Good — below average costs"
	case ter > 0.10:
		return "A", "Very good — significantly below average"
	default:
		return "A+", "Excellent — well below average German ETF investor costs"
	}
}

// costBenchmark compares the funds' weighted charge with the German average.
// It deliberately uses the TER over funds with a known charge (terCovered)
// and their value (coveredValue): single stocks and funds without a charge on
// record would otherwise count as 0 % and flatter the grade. The saving is
// what the funds would cost at the average charge minus what they cost now
// (negative when the funds are dearer than average).
func costBenchmark(terCovered, coveredValue float64) map[string]any {
	grade, detail := costGrade(terCovered)
	annualSaving := (avgGermanFundTER - terCovered) / 100 * coveredValue
	tenYearSaving := feeDragFV(coveredValue, avgGermanFundTER, costProjectionGrowthPct, 10) -
		feeDragFV(coveredValue, terCovered, costProjectionGrowthPct, 10)
	return map[string]any{
		"avg_ter":         avgGermanFundTER,
		"your_ter":        math.Round(terCovered*100) / 100,
		"funds_value":     math.Round(coveredValue*100) / 100,
		"grade":           grade,
		"detail":          detail,
		"annual_saving":   math.Round(annualSaving*100) / 100,
		"ten_year_saving": math.Round(tenYearSaving*100) / 100,
		"growth_pct":      costProjectionGrowthPct,
	}
}

// tradingCostInput is one transaction for the total-cost-of-ownership
// figures, already in EUR.
type tradingCostInput struct {
	Date        time.Time
	Type        string
	AccountType string
	AmountEUR   float64
	FeeEUR      float64
}

// tradingCosts sums trading volume and fees on investment accounts and
// averages them per year from the first trade to `now` (at least one year,
// so a single recent purchase is not extrapolated). Fees are the fee column
// of trades plus standalone `fee` rows such as custody charges.
func tradingCosts(txns []tradingCostInput, now time.Time) (volume, fees float64, trades int, years float64, ok bool) {
	var first time.Time
	for _, t := range txns {
		if !isSecuritiesOnlyAccountType(t.AccountType) {
			continue
		}
		switch t.Type {
		case "buy", "sell", "savings_plan":
			volume += math.Abs(t.AmountEUR)
			fees += math.Abs(t.FeeEUR)
			trades++
		case "fee":
			fees += math.Abs(t.AmountEUR) + math.Abs(t.FeeEUR)
		default:
			continue
		}
		if first.IsZero() || t.Date.Before(first) {
			first = t.Date
		}
	}
	if first.IsZero() {
		return 0, 0, 0, 0, false
	}
	years = now.Sub(first).Hours() / (365.25 * 24)
	if years < 1 {
		years = 1
	}
	return volume, fees, trades, years, true
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

	// One row per fund, even when it is held in several accounts.
	positions := aggregateByISIN(enriched)
	totalValue := 0.0
	for _, eh := range positions {
		totalValue += eh.value
	}

	entries := []costEntry{}
	totalCost := 0.0
	coveredValue := 0.0 // sum of value across holdings with a known TER
	type charged struct{ value, ter float64 }
	var funds []charged

	for _, eh := range positions {
		ter := numericToFloat(eh.security.TER)
		if ter <= 0 || eh.value <= 0 {
			continue
		}
		weight := 0.0
		if totalValue > 0 {
			weight = (eh.value / totalValue) * 100
		}
		annualCost := eh.value * ter / 100
		totalCost += annualCost
		coveredValue += eh.value
		funds = append(funds, charged{value: eh.value, ter: ter})

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
	weightedTER := 0.0 // over the whole portfolio, uncharged holdings at 0 %
	if totalValue > 0 {
		coveragePct = (coveredValue / totalValue) * 100
		weightedTER = totalCost / totalValue * 100
	}
	weightedTERCovered := 0.0 // over funds with a known charge only
	if coveredValue > 0 {
		weightedTERCovered = totalCost / coveredValue * 100
	}

	// Sort by annual cost descending
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].AnnualCost > entries[j].AnnualCost
	})

	// Fee drag for every year up to 30: the future-value difference with and
	// without each fund's charge at the stated growth rate.
	projection := []map[string]any{}
	if totalCost > 0 {
		for year := 1; year <= 30; year++ {
			drag := 0.0
			for _, f := range funds {
				drag += feeDragFV(f.value, f.ter, costProjectionGrowthPct, year)
			}
			projection = append(projection, map[string]any{
				"year":       year,
				"cumulative": math.Round(drag*100) / 100,
			})
		}
	}

	resp := map[string]any{
		"holdings":                  entries,
		"total_value":               math.Round(totalValue*100) / 100,
		"weighted_ter":              math.Round(weightedTER*100) / 100,
		"weighted_ter_covered_only": math.Round(weightedTERCovered*100) / 100,
		"coverage_pct":              math.Round(coveragePct*10) / 10,
		"annual_cost":               math.Round(totalCost*100) / 100,
		"daily_cost":                math.Round(totalCost/365*100) / 100,
		"projection":                projection,
		"projection_growth_pct":     costProjectionGrowthPct,
	}
	if coveredValue > 0 {
		resp["benchmark"] = costBenchmark(weightedTERCovered, coveredValue)
	}

	// Total cost of ownership: TER + transaction fees and custody charges +
	// estimated spread (in EUR), averaged from the first trade to today.
	fxRates := loadFXRatesMap(ctx, h.queries)
	histFX := loadHistoricalFXRates(ctx, h.queries)
	txns, _ := listAllTransactionsRows(ctx, h.queries)
	acctType := make(map[uuid.UUID]string)
	if accts, err := h.queries.ListAccounts(ctx); err == nil {
		for _, a := range accts {
			acctType[a.ID] = a.Type
		}
	}
	inputs := make([]tradingCostInput, 0, len(txns))
	for _, txn := range txns {
		inputs = append(inputs, tradingCostInput{
			Date:        txn.Date,
			Type:        txn.Type,
			AccountType: acctType[txn.AccountID],
			AmountEUR:   convertAmountToEURAtDate(numericToFloat(txn.Amount), txn.Currency, txn.Date, histFX, fxRates),
			FeeEUR:      convertAmountToEURAtDate(numericToFloat(txn.Fee), txn.Currency, txn.Date, histFX, fxRates),
		})
	}
	totalTxnVolume, totalFees, txnCount, years, hasTrading := tradingCosts(inputs, time.Now())
	if hasTrading || totalCost > 0 {
		annualTxnCost, annualSpreadCost := 0.0, 0.0
		if hasTrading {
			annualTxnCost = totalFees / years
			annualSpreadCost = totalTxnVolume * 0.0005 / years
		}
		totalAnnualCost := totalCost + annualTxnCost + annualSpreadCost
		totalAnnualPct := 0.0
		if totalValue > 0 {
			totalAnnualPct = math.Round(totalAnnualCost/totalValue*10000) / 100
		}
		resp["total_cost_ownership"] = map[string]any{
			"ter_cost":          math.Round(totalCost*100) / 100,
			"transaction_fees":  math.Round(annualTxnCost*100) / 100,
			"spread_estimate":   math.Round(annualSpreadCost*100) / 100,
			"total_annual":      math.Round(totalAnnualCost*100) / 100,
			"total_annual_pct":  totalAnnualPct,
			"lifetime_fees":     math.Round(totalFees*100) / 100,
			"lifetime_volume":   math.Round(totalTxnVolume),
			"transaction_count": txnCount,
			"years":             math.Round(years*10) / 10,
		}
	}

	writeJSON(w, http.StatusOK, resp)
}

// correlationMinObservations is the fewest aligned returns a pair needs
// before a correlation is reported; with fewer the cell is null ("not enough
// shared history") rather than 0, which would read as "uncorrelated".
const correlationMinObservations = 20

// eurPricePoint is one close converted to EUR at its own date.
type eurPricePoint struct {
	Date time.Time
	EUR  float64
}

// alignedReturnCorrelation computes the Pearson correlation of two EUR price
// series' returns over their common dates. Returns are taken between
// consecutive common dates, so both sides of every pair cover the same
// interval even when one series has gaps. Pairs where either return is
// ±50 % or more (data errors, splits) are dropped. ok is false when fewer
// than minObs returns remain.
func alignedReturnCorrelation(a, b []eurPricePoint, minObs int) (corr float64, n int, ok bool) {
	bByDate := make(map[string]float64, len(b))
	for _, p := range b {
		if p.EUR > 0 {
			bByDate[p.Date.Format("2006-01-02")] = p.EUR
		}
	}
	var ra, rb []float64
	prevA, prevB := 0.0, 0.0
	for _, p := range a {
		pb, found := bByDate[p.Date.Format("2006-01-02")]
		if !found || p.EUR <= 0 {
			continue
		}
		if prevA > 0 && prevB > 0 {
			x := p.EUR/prevA - 1
			y := pb/prevB - 1
			if math.Abs(x) < 0.5 && math.Abs(y) < 0.5 {
				ra = append(ra, x)
				rb = append(rb, y)
			}
		}
		prevA, prevB = p.EUR, pb
	}
	n = len(ra)
	if n < minObs || n < 2 {
		return 0, n, false
	}
	meanA, meanB := 0.0, 0.0
	for k := range ra {
		meanA += ra[k]
		meanB += rb[k]
	}
	meanA /= float64(n)
	meanB /= float64(n)
	cov, varA, varB := 0.0, 0.0, 0.0
	for k := range ra {
		da, dbv := ra[k]-meanA, rb[k]-meanB
		cov += da * dbv
		varA += da * da
		varB += dbv * dbv
	}
	if varA <= 0 || varB <= 0 {
		return 0, n, false
	}
	corr = cov / math.Sqrt(varA*varB)
	if math.IsNaN(corr) || math.IsInf(corr, 0) {
		return 0, n, false
	}
	return math.Max(-1, math.Min(1, corr)), n, true
}

// HandleCorrelation computes pairwise price correlation between all held
// securities, from EUR prices on the dates both securities have a close.
// Cells with too little shared history are null.
func (h *AnalysisHandler) HandleCorrelation(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	enriched, err := h.loadEnrichedHoldings(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "load holdings: "+err.Error())
		return
	}

	fxRates := loadFXRatesMap(ctx, h.queries)
	histFX := loadHistoricalFXRates(ctx, h.queries)

	// One series per ISIN, each close converted to EUR at its own date.
	var labels []string
	var series [][]eurPricePoint
	for _, eh := range aggregateByISIN(enriched) {
		prices, err := h.queries.ListPriceHistory(ctx, eh.security.ISIN)
		if err != nil || len(prices) <= correlationMinObservations {
			continue
		}
		pts := make([]eurPricePoint, 0, len(prices))
		for _, p := range prices {
			if close := numericToFloat(p.Close); close > 0 {
				pts = append(pts, eurPricePoint{Date: p.Date, EUR: convertAmountToEURAtDate(close, p.Currency, p.Date, histFX, fxRates)})
			}
		}
		if len(pts) <= correlationMinObservations {
			continue
		}
		labels = append(labels, eh.security.Name)
		series = append(series, pts)
	}

	n := len(series)
	if n < 2 {
		writeJSON(w, http.StatusOK, map[string]any{"labels": []string{}, "matrix": [][]*float64{}, "min_observations": correlationMinObservations})
		return
	}

	matrix := make([][]*float64, n)
	for i := range matrix {
		matrix[i] = make([]*float64, n)
		one := 1.0
		matrix[i][i] = &one
	}
	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			corr, _, ok := alignedReturnCorrelation(series[i], series[j], correlationMinObservations)
			if !ok {
				continue
			}
			v := math.Round(corr*100) / 100
			matrix[i][j] = &v
			matrix[j][i] = &v
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"labels":           labels,
		"matrix":           matrix,
		"min_observations": correlationMinObservations,
	})
}

// HandleFXHistory returns the exchange-rate history of every foreign currency
// the user holds securities or cash in.
func (h *AnalysisHandler) HandleFXHistory(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	result := make(map[string][]fxRatePoint)
	for _, cur := range h.heldForeignCurrencies(ctx) {
		rates, err := h.queries.ListExchangeRateHistory(ctx, cur)
		if err != nil || len(rates) == 0 {
			continue
		}
		if points := fxRatePoints(rates); len(points) > 0 {
			result[cur] = points
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{"rates": result})
}

type fxRatePoint struct {
	Date string  `json:"date"`
	Rate float64 `json:"rate"`
}

// fxRatePoints samples a rate history weekly for the chart. ECB reference
// rates are stored as units of the currency per 1 EUR (USD 1.0989 means
// 1 € buys 1.0989 $), which is exactly what the chart shows, so the rate is
// passed through unchanged.
func fxRatePoints(rates []db.ListExchangeRateHistoryRow) []fxRatePoint {
	var points []fxRatePoint
	for i, r := range rates {
		if i%7 == 0 || i == len(rates)-1 {
			rate := numericToFloat(r.Rate)
			if rate > 0 {
				points = append(points, fxRatePoint{
					Date: r.Date.Format("2006-01-02"),
					Rate: math.Round(rate*10000) / 10000,
				})
			}
		}
	}
	return points
}

// heldForeignCurrencies lists the non-EUR currencies of held securities,
// non-zero cash balances and active accounts.
func (h *AnalysisHandler) heldForeignCurrencies(ctx context.Context) []string {
	var raw []string
	if holdings, err := h.queries.ListCurrentHoldings(ctx); err == nil {
		for _, hd := range holdings {
			if numericToFloat(hd.Quantity) > 0 {
				raw = append(raw, hd.Currency)
			}
		}
	}
	if rows, err := h.queries.ListAllAccountCashByCurrency(ctx); err == nil {
		for _, row := range rows {
			if math.Abs(numericToFloat(row.Balance)) >= 0.01 {
				raw = append(raw, row.Currency)
			}
		}
	}
	if accts, err := h.queries.ListAccounts(ctx); err == nil {
		for _, a := range accts {
			if a.IsActive {
				raw = append(raw, a.Currency)
			}
		}
	}
	return foreignCurrencyList(raw)
}

// foreignCurrencyList normalises currency codes (GBp/GBX → GBP), drops EUR
// and duplicates, and sorts the rest.
func foreignCurrencyList(raw []string) []string {
	seen := make(map[string]bool)
	var out []string
	for _, c := range raw {
		norm, _ := normalizeCurrencyAndScale(c)
		if norm == "EUR" || seen[norm] {
			continue
		}
		seen[norm] = true
		out = append(out, norm)
	}
	sort.Strings(out)
	return out
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

// HandleExportTaxReport generates a CSV tax report for a given year: the tax
// summary, realised P/L per security and the Anlage KAP / KAP-INV lines.
// Accepts the same joint=1 and church_tax parameters as HandleAnlageKAP.
func (h *AnalysisHandler) HandleExportTaxReport(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	yearStr := r.URL.Query().Get("year")
	year := time.Now().Year()
	if yearStr != "" {
		if y, err := strconv.Atoi(yearStr); err == nil && y >= 2020 && y <= 2100 {
			year = y
		}
	}

	d, err := h.loadTaxFilingData(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list transactions: "+err.Error())
		return
	}
	summary := analytics.ComputeTaxSummary(d.taxTxns, year, nil)
	enriched, _ := h.loadEnrichedHoldings(ctx)
	kap := computeAnlageKAP(anlageKAPInput{
		capitalIncomeInput: h.capitalIncome(ctx, d, year, enriched),
		Joint:              parseJointParam(r.URL.Query().Get("joint")),
		ChurchRate:         parseChurchTaxParam(r.URL.Query().Get("church_tax")),
	})

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
				csvField(s.ISIN), csvField(s.Name), s.RealizedPL, s.Teilfreistellung, s.TaxablePL, equity)
		}
	}

	fmt.Fprintf(w, "\nAnlage KAP / KAP-INV %d\n", year)
	fmt.Fprintf(w, "Formular;Zeile;Bezeichnung;Betrag;Hinweis\n")
	writeKAPLines := func(form string, lines []kapLine) {
		for _, l := range lines {
			note := ""
			amount := fmt.Sprintf("%.2f", l.Amount)
			switch {
			case l.Blank:
				note = "Nur lt. Steuerbescheinigung eintragen, sonst leer lassen"
				amount = ""
			case l.Estimate:
				note = "Schätzung, mit der Steuerbescheinigung abgleichen"
			}
			fmt.Fprintf(w, "%s;%d;%s;%s;%s\n", form, l.Line, csvField(l.Description), amount, csvField(note))
		}
	}
	writeKAPLines("KAP", kap.Lines)
	writeKAPLines("KAP-INV", kap.InvLines)
	if kap.LayoutNote != "" {
		fmt.Fprintf(w, "%s\n", csvField("Hinweis: Zeilennummern nach den Vordrucken 2024/2025; bitte anhand der Bezeichnung zuordnen."))
	}
}

// csvField quotes a semicolon-separated CSV field when it needs it.
func csvField(s string) string {
	if strings.ContainsAny(s, ";\"\n\r") {
		return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
	}
	return s
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

// HandleAnlageKAP prepares Anlage KAP and Anlage KAP-INV for a tax year with
// per-bank detail. Query: year (default: last year), joint=1 for joint filing,
// church_tax=0.08|0.09 for the church-tax rate used to split withheld tax.
func (h *AnalysisHandler) HandleAnlageKAP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	yearStr := r.URL.Query().Get("year")
	year := time.Now().Year() - 1 // default to previous year
	if yearStr != "" {
		if y, err := strconv.Atoi(yearStr); err == nil && y >= 2020 && y <= 2100 {
			year = y
		}
	}

	d, err := h.loadTaxFilingData(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list transactions: "+err.Error())
		return
	}
	enriched, _ := h.loadEnrichedHoldings(ctx)
	res := computeAnlageKAP(anlageKAPInput{
		capitalIncomeInput: h.capitalIncome(ctx, d, year, enriched),
		Joint:              parseJointParam(r.URL.Query().Get("joint")),
		ChurchRate:         parseChurchTaxParam(r.URL.Query().Get("church_tax")),
	})
	writeJSON(w, http.StatusOK, res.response())
}

// vpiTable describes the German consumer price index (Destatis
// Verbraucherpreisindex, VPI: the national index, not the harmonised HVPI).
type vpiTable struct {
	// annual is the annual average change in % per year.
	annual map[int]float64
	// monthly holds published monthly index values (2020 = 100) for the
	// months the annual averages do not cover yet.
	monthly map[string]float64
}

// germanVPI sources: Destatis press release 019/2026 of 16 Jan 2026 (annual
// averages 2021 +3.1, 2022 +6.9, 2023 +5.9, 2024 +2.2, 2025 +2.2); Destatis'
// 2021 annual release (2020 +0.5); monthly 2026 values from Destatis press
// release 392/2026 (Oct 2026: 126.5) and the IHK Ostwestfalen VPI table
// (Jan–Sep 2026). 2026 +2.5 is provisional: the Jan–Oct average (124.97)
// over the 2025 average (121.9). Update both in January.
var germanVPI = vpiTable{
	annual: map[int]float64{
		2020: 0.5, 2021: 3.1, 2022: 6.9, 2023: 5.9, 2024: 2.2, 2025: 2.2, 2026: 2.5,
	},
	monthly: map[string]float64{
		"2026-01": 122.8, "2026-02": 123.1, "2026-03": 124.5, "2026-04": 125.2, "2026-05": 125.0,
		"2026-06": 124.6, "2026-07": 125.6, "2026-08": 125.8, "2026-09": 126.6, "2026-10": 126.5,
	},
}

// vpiFallbackRate is used for years outside the table (the ECB's 2 % target).
const vpiFallbackRate = 2.0

func (t vpiTable) rate(year int) float64 {
	if r, ok := t.annual[year]; ok {
		return r
	}
	return vpiFallbackRate
}

// annualAverage chains the annual rates into an annual average level
// (2020 = 100).
func (t vpiTable) annualAverage(year int) float64 {
	level := 100.0
	for y := 2021; y <= year; y++ {
		level *= 1 + t.rate(y)/100
	}
	for y := 2020; y > year; y-- {
		level /= 1 + t.rate(y)/100
	}
	return level
}

// level returns the index level (2020 = 100) of the month containing m:
// the published value if there is one; after the latest published month,
// that value grown at the year's rate; otherwise a geometric interpolation
// between the annual averages, each placed at mid-year. (Treating an annual
// average change as the change within that year would overstate the
// 2022→2026 price rise by about 1.5 points, because 2023's average still
// carried the late-2022 surge.)
func (t vpiTable) level(m time.Time) float64 {
	key := m.Format("2006-01")
	if v, ok := t.monthly[key]; ok {
		return v
	}
	lastKey := ""
	for k := range t.monthly {
		if k > lastKey {
			lastKey = k
		}
	}
	if lastKey != "" && key > lastKey {
		last, _ := time.Parse("2006-01", lastKey)
		months := (m.Year()-last.Year())*12 + int(m.Month()) - int(last.Month())
		return t.monthly[lastKey] * math.Pow(1+t.rate(m.Year())/100, float64(months)/12)
	}
	pos := float64(m.Year()) + (float64(m.Month())-0.5)/12 - 0.5
	y0 := int(math.Floor(pos))
	a0, a1 := t.annualAverage(y0), t.annualAverage(y0+1)
	return a0 * math.Pow(a1/a0, pos-float64(y0))
}

type inflationPoint struct {
	Date     string  `json:"date"`
	Nominal  float64 `json:"nominal"`
	Real     float64 `json:"real"`
	CpiIndex float64 `json:"cpi_index"`
}

// inflationSummary is the Purchasing Power section: month-end values in
// nominal and today's euros, and the last 12 months (or the history, if
// shorter) as a return and a euro amount.
type inflationSummary struct {
	History     []inflationPoint
	HasWindow   bool
	WindowStart time.Time
	WindowEnd   time.Time
	// NominalReturn is the time-weighted return in % (deposits and
	// withdrawals excluded); RealReturn deflates it by InflationPct.
	NominalReturn float64
	RealReturn    float64
	InflationPct  float64
	// PurchasingPowerLost is what consumer-price inflation over the window
	// took from the value held at its start, in today's euros (start value in
	// today's euros minus start value as recorded).
	PurchasingPowerLost float64
}

// monthlyCPILevels returns the consumer-price level of every month from
// first to last, rebased so the last month is 100 ("today's euros").
func monthlyCPILevels(first, last time.Time, vpi vpiTable) map[string]float64 {
	month := time.Date(first.Year(), first.Month(), 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(last.Year(), last.Month(), 1, 0, 0, 0, 0, time.UTC)
	base := vpi.level(end)
	levels := make(map[string]float64)
	for ; !month.After(end); month = month.AddDate(0, 1, 0) {
		levels[month.Format("2006-01")] = 100 * vpi.level(month) / base
	}
	return levels
}

// valueOnOrBefore returns the last value dated on or before d, and whether
// there is one.
func valueOnOrBefore(series []analytics.DailyValuation, d time.Time) (analytics.DailyValuation, bool) {
	i := sort.Search(len(series), func(i int) bool { return series[i].Date.After(d) })
	if i == 0 {
		return analytics.DailyValuation{}, false
	}
	return series[i-1], true
}

// computeInflation measures the chronological values in raw (leading zeros
// already dropped) against German consumer prices. adjusted is the
// flow-adjusted index of the same series, used for the window's return so
// that deposits do not count as growth.
func computeInflation(raw, adjusted []analytics.DailyValuation, vpi vpiTable) inflationSummary {
	var out inflationSummary
	out.History = []inflationPoint{}
	if len(raw) == 0 {
		return out
	}
	end := raw[len(raw)-1]
	cpi := monthlyCPILevels(raw[0].Date, end.Date, vpi)

	// One point per month at its last snapshot (the latest one for the
	// current month).
	for i, p := range raw {
		month := p.Date.Format("2006-01")
		if i+1 < len(raw) && raw[i+1].Date.Format("2006-01") == month {
			continue
		}
		level := cpi[month]
		out.History = append(out.History, inflationPoint{
			Date:     p.Date.Format("2006-01-02"),
			Nominal:  math.Round(p.Value),
			Real:     math.Round(p.Value * 100 / level),
			CpiIndex: math.Round(level*10) / 10,
		})
	}

	base, ok := valueOnOrBefore(raw, end.Date.AddDate(-1, 0, 0))
	if !ok {
		base = raw[0]
	}
	if !base.Date.Before(end.Date) || base.Value <= 0 {
		return out
	}
	out.HasWindow = true
	out.WindowStart, out.WindowEnd = base.Date, end.Date
	out.InflationPct = (cpi[end.Date.Format("2006-01")]/cpi[base.Date.Format("2006-01")] - 1) * 100
	idxStart, okStart := valueOnOrBefore(adjusted, base.Date)
	idxEnd, okEnd := valueOnOrBefore(adjusted, end.Date)
	if okStart && okEnd && idxStart.Value > 0 {
		out.NominalReturn = (idxEnd.Value/idxStart.Value - 1) * 100
	}
	out.RealReturn = ((1+out.NominalReturn/100)/(1+out.InflationPct/100) - 1) * 100
	out.PurchasingPowerLost = base.Value * out.InflationPct / 100
	return out
}

// HandleInflation returns the value of cash and securities in nominal and
// today's euros, deflated with the German consumer price index (VPI).
// Property, vehicles and their loans are left out like on the Risk tab.
func (h *AnalysisHandler) HandleInflation(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	snaps, err := h.queries.ListNetWorthSnapshots(ctx, 5000)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list snapshots: "+err.Error())
		return
	}
	raw := investableRiskSeries(snaps)
	if len(raw) < 2 {
		writeJSON(w, http.StatusOK, map[string]any{"history": []any{}})
		return
	}
	adjusted := analytics.BuildCashflowAdjustedSeries(raw, h.buildInvestableFlowsForRisk(ctx))
	s := computeInflation(raw, adjusted, germanVPI)

	resp := map[string]any{"history": s.History}
	if s.HasWindow {
		resp["nominal_return"] = math.Round(s.NominalReturn*10) / 10
		resp["real_return"] = math.Round(s.RealReturn*10) / 10
		resp["inflation_pct"] = math.Round(s.InflationPct*10) / 10
		resp["purchasing_power_lost"] = math.Round(s.PurchasingPowerLost)
		resp["window_start"] = s.WindowStart.Format("2006-01-02")
		resp["window_end"] = s.WindowEnd.Format("2006-01-02")
	}
	writeJSON(w, http.StatusOK, resp)
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
	prices := make([]benchmarkPrice, 0, len(benchPrices))
	for _, p := range benchPrices {
		if p.Close.Valid {
			f, _ := p.Close.Float64Value()
			eurP := convertAmountToEURAtDate(f.Float64, p.Currency, p.Date, histFX, fxRates)
			if eurP > 0 {
				prices = append(prices, benchmarkPrice{date: p.Date, price: eurP})
			}
		}
	}

	snaps, err := h.queries.ListNetWorthSnapshots(ctx, 5000)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list snapshots: "+err.Error())
		return
	}
	replay := replayBenchmark(investableRiskSeries(snaps), h.buildInvestableFlowsForRisk(ctx), prices)

	benchName := benchISIN
	if sec, err := h.queries.GetSecurity(ctx, benchISIN); err == nil {
		benchName = sec.Name
	}

	resp := map[string]any{
		"benchmark_name":  benchName,
		"benchmark_isin":  benchISIN,
		"actual_value":    math.Round(replay.ActualValue),
		"benchmark_value": math.Round(replay.BenchmarkValue),
		"difference":      math.Round(replay.ActualValue - replay.BenchmarkValue),
		"comparison":      replay.Points,
		"available":       true,
		"have":            len(benchPrices),
		"need":            benchmarkMinPrices,
	}
	if !replay.Start.IsZero() {
		resp["start_date"] = replay.Start.Format("2006-01-02")
	}
	writeJSON(w, http.StatusOK, resp)
}

type benchmarkReplayPoint struct {
	Date      string  `json:"date"`
	Actual    float64 `json:"actual"`
	Benchmark float64 `json:"benchmark"`
}

type benchmarkReplay struct {
	Points         []benchmarkReplayPoint
	Start          time.Time
	ActualValue    float64
	BenchmarkValue float64
}

// replayBenchmark answers "what if all of this money had been in the
// benchmark": it buys the benchmark with the value of cash and securities on
// the first day both series have data, then buys and sells with every later
// external flow (deposits, withdrawals, RSU vests and in-kind transfers at
// market value: the same flows the Risk tab neutralises) at the latest price
// on or before the flow's date. Units may go negative; flooring them would
// silently drop withdrawals. Both lines are valued on the same dates: every
// month end and the latest snapshot. actual is chronological; prices are in
// EUR, ascending.
func replayBenchmark(actual []analytics.DailyValuation, flows []analytics.CashFlow, prices []benchmarkPrice) benchmarkReplay {
	out := benchmarkReplay{Points: []benchmarkReplayPoint{}}
	if len(actual) == 0 || len(prices) == 0 {
		return out
	}
	priceOn := func(d time.Time) float64 {
		i := sort.Search(len(prices), func(i int) bool { return prices[i].date.After(d) })
		if i == 0 {
			return 0
		}
		return prices[i-1].price
	}

	startIdx := -1
	for i, p := range actual {
		if p.Value > 0 && priceOn(p.Date) > 0 {
			startIdx = i
			break
		}
	}
	if startIdx < 0 {
		return out
	}
	start := actual[startIdx]
	end := actual[len(actual)-1]
	out.Start = start.Date

	sorted := append([]analytics.CashFlow(nil), flows...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Date.Before(sorted[j].Date) })
	next := sort.Search(len(sorted), func(i int) bool { return sorted[i].Date.After(start.Date) })

	units := start.Value / priceOn(start.Date)
	series := actual[startIdx:]
	valueAt := func(d time.Time) {
		for next < len(sorted) && !sorted[next].Date.After(d) {
			if p := priceOn(sorted[next].Date); p > 0 {
				units += sorted[next].Amount / p
			}
			next++
		}
		a, _ := valueOnOrBefore(series, d)
		out.Points = append(out.Points, benchmarkReplayPoint{
			Date:      d.Format("2006-01-02"),
			Actual:    math.Round(a.Value),
			Benchmark: math.Round(units * priceOn(d)),
		})
	}

	valueAt(start.Date)
	for m := time.Date(start.Date.Year(), start.Date.Month(), 1, 0, 0, 0, 0, time.UTC); ; m = m.AddDate(0, 1, 0) {
		monthEnd := m.AddDate(0, 1, -1)
		if !monthEnd.Before(end.Date) {
			break
		}
		if monthEnd.After(start.Date) {
			valueAt(monthEnd)
		}
	}
	if end.Date.After(start.Date) {
		valueAt(end.Date)
	}

	out.ActualValue = end.Value
	out.BenchmarkValue = units * priceOn(end.Date)
	return out
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

// HandleFSAStatus returns Freistellungsauftrag (Sparer-Pauschbetrag) usage per bank.
// Each bank nets gains and losses before applying the order (§ 43a Abs. 3 EStG),
// and the previous year's Vorabpauschale counts as income in January.
// Accepts ?joint=1 for married couples filing jointly (Zusammenveranlagung),
// which doubles the allowance, and ?year= for another tax year.
func (h *AnalysisHandler) HandleFSAStatus(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	now := time.Now()
	year := now.Year()
	if y := r.URL.Query().Get("year"); y != "" {
		if v, err := strconv.Atoi(y); err == nil && v >= 2020 && v <= 2100 {
			year = v
		}
	}
	months := 12
	if year == now.Year() {
		months = int(now.Month())
	}

	d, err := h.loadTaxFilingData(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list transactions: "+err.Error())
		return
	}
	enriched, _ := h.loadEnrichedHoldings(ctx)
	res := computeFSAStatus(fsaInput{
		capitalIncomeInput: h.capitalIncome(ctx, d, year, enriched),
		Joint:              parseJointParam(r.URL.Query().Get("joint")),
		Months:             months,
	})
	writeJSON(w, http.StatusOK, res.response())
}

// switchOutcome compares keeping a fund with selling it and buying a cheaper
// one, after German tax: switching realises the gain now (switchTaxCost on
// value − costBasis), and both paths pay tax on their remaining gain at the
// horizon. Growth before costs is costProjectionGrowthPct; the Vorabpauschale
// and the saver's allowance are ignored. It returns the after-tax advantage of
// switching after 10 years and the first year (1-50) in which switching is
// ahead, or 0 when it never is within 50 years.
func switchOutcome(value, costBasis, curTER, altTER, tfs float64) (tenYearNet float64, breakevenYears int) {
	g := 1 + costProjectionGrowthPct/100
	kKeep := g * (1 - curTER/100)
	kSwitch := g * (1 - altTER/100)
	switchTax := switchTaxCost(value-costBasis, tfs, 0)
	reinvested := value - switchTax
	advantage := func(years int) float64 {
		n := float64(years)
		keep := value * math.Pow(kKeep, n)
		keep -= switchTaxCost(keep-costBasis, tfs, 0)
		sw := reinvested * math.Pow(kSwitch, n)
		sw -= switchTaxCost(sw-reinvested, tfs, 0)
		return sw - keep
	}
	tenYearNet = advantage(10)
	for y := 1; y <= 50; y++ {
		if advantage(y) >= 0 {
			return tenYearNet, y
		}
	}
	return tenYearNet, 0
}

// HandleAlternatives returns cheaper ETF alternatives for current holdings,
// with what a switch would cost in tax: selling realises the unrealised gain
// now (Teilfreistellung applied, saver's allowance ignored).
func (h *AnalysisHandler) HandleAlternatives(w http.ResponseWriter, r *http.Request) {
	enriched, err := h.loadEnrichedHoldings(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "load holdings: "+err.Error())
		return
	}

	// Load static alternatives data
	var altMap map[string]struct {
		Name         string  `json:"name"`
		Index        string  `json:"index"`
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

	type alternative struct {
		ISIN          string  `json:"isin"`
		Name          string  `json:"name"`
		Index         string  `json:"index"`
		SameIndex     bool    `json:"same_index"`
		TER           float64 `json:"ter"`
		AnnualSaving  float64 `json:"annual_saving"`
		TenYearSaving float64 `json:"ten_year_saving"`
		// After the switch tax and tax at the end; null when the cost basis
		// is unknown.
		TenYearNetAfterTax *float64 `json:"ten_year_net_after_tax"`
		// First year in which switching is ahead after tax; null when it
		// never is within 50 years or the cost basis is unknown.
		BreakevenYears *int `json:"breakeven_years"`
	}
	type altResult struct {
		ISIN           string        `json:"isin"`
		Name           string        `json:"name"`
		Index          string        `json:"index"`
		CurrentTER     float64       `json:"current_ter"`
		Value          float64       `json:"value"`
		UnrealizedGain *float64      `json:"unrealized_gain"`
		SwitchTax      *float64      `json:"switch_tax"`
		Alternatives   []alternative `json:"alternatives"`
	}

	results := []altResult{}
	for _, eh := range aggregateByISIN(enriched) {
		alt, ok := altMap[eh.security.ISIN]
		if !ok || len(alt.Alternatives) == 0 || eh.value <= 0 {
			continue
		}
		// Prefer database TER (from metadata fetcher) over stale static JSON
		currentTER := numericToFloat(eh.security.TER)
		if currentTER <= 0 {
			currentTER = alt.TER
		}
		res := altResult{
			ISIN: eh.security.ISIN, Name: eh.security.Name, Index: alt.Index,
			CurrentTER: currentTER, Value: math.Round(eh.value*100) / 100,
			Alternatives: []alternative{},
		}
		tfs := securityTFSRate(eh.security)
		knownBasis := eh.costValue > 0
		if knownBasis {
			gain := math.Round((eh.value-eh.costValue)*100) / 100
			tax := math.Round(switchTaxCost(eh.value-eh.costValue, tfs, 0)*100) / 100
			res.UnrealizedGain = &gain
			res.SwitchTax = &tax
		}
		for _, a := range alt.Alternatives {
			if a.TER >= currentTER {
				continue // only show cheaper alternatives
			}
			saving := eh.value * (currentTER - a.TER) / 100
			tenYear := feeDragFV(eh.value, currentTER, costProjectionGrowthPct, 10) -
				feeDragFV(eh.value, a.TER, costProjectionGrowthPct, 10)
			item := alternative{
				ISIN: a.ISIN, Name: a.Name, Index: a.Index,
				SameIndex:     strings.EqualFold(strings.TrimSpace(a.Index), strings.TrimSpace(alt.Index)),
				TER:           a.TER,
				AnnualSaving:  math.Round(saving*100) / 100,
				TenYearSaving: math.Round(tenYear*100) / 100,
			}
			if knownBasis {
				net, breakeven := switchOutcome(eh.value, eh.costValue, currentTER, a.TER, tfs)
				net = math.Round(net*100) / 100
				item.TenYearNetAfterTax = &net
				if breakeven > 0 {
					item.BreakevenYears = &breakeven
				}
			}
			res.Alternatives = append(res.Alternatives, item)
		}
		if len(res.Alternatives) > 0 {
			results = append(results, res)
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"alternatives": results,
		"growth_pct":   costProjectionGrowthPct,
	})
}

// spendingEntry is one classified cash movement on a checking or savings
// account, in EUR.
type spendingEntry struct {
	Date         time.Time
	Category     cashflow.Category
	Amount       float64 // signed: + inflow, − outflow
	Counterparty string
	Explicit     bool // category set by the user
	LoanPart     bool // interest or principal part of a matched loan instalment
}

// spendingEntriesFromCash classifies checking/savings rows exactly like the
// Cashflow page: signed cash delta (amount and fee, whatever sign the import
// stored), matched loan instalments split into interest (housing/transport)
// and principal, everything else through cashflow.ResolveCategory.
// split reports a matched loan instalment's interest and whether it finances
// a car.
func spendingEntriesFromCash(rows []db.ListCashTransactionsRow, toEUR func(amount float64, currency string, date time.Time) float64, split func(id uuid.UUID) (interest float64, car bool, ok bool)) []spendingEntry {
	out := make([]spendingEntry, 0, len(rows))
	for _, row := range rows {
		amt := toEUR(numericToFloat(row.Amount), row.Currency, row.Date)
		fee := toEUR(numericToFloat(row.Fee), row.Currency, row.Date)
		tax := toEUR(numericToFloat(row.Tax), row.Currency, row.Date)
		amount := txnSignedCashDelta(row.Type, false, amt, fee, tax)
		if amount == 0 {
			continue
		}
		cp := ""
		if row.Counterparty.Valid {
			cp = row.Counterparty.String
		}
		if interest, car, ok := split(row.ID); ok {
			interestCat := cashflow.CatHousing
			if car {
				interestCat = cashflow.CatTransport
			}
			interestPart := math.Copysign(math.Min(interest, math.Abs(amount)), amount)
			out = append(out,
				spendingEntry{Date: row.Date, Category: interestCat, Amount: interestPart, Counterparty: cp, LoanPart: true},
				spendingEntry{Date: row.Date, Category: cashflow.CatLoanPrincipal, Amount: amount - interestPart, Counterparty: cp, LoanPart: true})
			continue
		}
		ref, override := "", ""
		if row.Reference.Valid {
			ref = row.Reference.String
		}
		if row.Category.Valid {
			override = row.Category.String
		}
		env := cashflow.Env{AccountType: row.AccountType}
		cat := cashflow.ResolveCategoryWith(env, override, row.Type, cp, ref)
		if amount > 0 {
			if spendCat, ok := spendingRefundCategory(env, override, cp, ref, cat); ok {
				cat = spendCat
			}
		}
		out = append(out, spendingEntry{
			Date:         row.Date,
			Category:     cat,
			Amount:       amount,
			Counterparty: cp,
			Explicit:     strings.TrimSpace(override) != "",
		})
	}
	return out
}

// spendingRefundCategory returns the spending category an inflow should net
// against on the Spending tab. The shared classifier books money back from a
// merchant as CatRefund (income on the Cashflow page); here a refund reduces
// the category it came from: the user's spending-category override if there
// is one, else the merchant category the same text would get as an outflow.
func spendingRefundCategory(env cashflow.Env, override, counterparty, reference string, resolved cashflow.Category) (cashflow.Category, bool) {
	isSpend := func(c cashflow.Category) bool {
		b := cashflow.BucketOf(c)
		return b == cashflow.BucketFixed || b == cashflow.BucketVariable
	}
	if o := cashflow.Category(strings.ToLower(strings.TrimSpace(override))); o != "" && cashflow.IsKnown(o) && isSpend(o) {
		return o, true
	}
	if resolved == cashflow.CatRefund {
		if c := cashflow.ClassifyWith(env, "withdrawal", counterparty, reference); isSpend(c) {
			return c, true
		}
	}
	return "", false
}

// spendingMonth is one calendar month of the spending series.
type spendingMonth struct {
	Month      string             `json:"month"`
	Income     float64            `json:"income"`
	Expenses   float64            `json:"expenses"`
	Net        float64            `json:"net"`
	ByCategory map[string]float64 `json:"by_category"`
}

// spendingCategory is one category's spending over the whole span.
type spendingCategory struct {
	Category   string  `json:"category"`
	Total      float64 `json:"total"`
	Count      int     `json:"count"`
	AvgMonthly float64 `json:"avg_monthly"`
}

// spendingSubscription is a detected recurring charge.
type spendingSubscription struct {
	Name        string  `json:"name"`
	Amount      float64 `json:"amount"`
	Frequency   int     `json:"occurrences"`
	Cadence     string  `json:"cadence"`
	LastCharged string  `json:"last_charged"`
	AnnualCost  float64 `json:"annual_cost"`
}

// spendingSummary is the /analysis/spending response.
type spendingSummary struct {
	Monthly           []spendingMonth        `json:"monthly"`
	Categories        []spendingCategory     `json:"categories"`
	Subscriptions     []spendingSubscription `json:"subscriptions"`
	TotalIncome       float64                `json:"total_income"`
	TotalExpenses     float64                `json:"total_expenses"`
	SavingsRatePct    float64                `json:"savings_rate_pct"`
	SavingsRatePct12m *float64               `json:"savings_rate_pct_12m"`
	WindowMonths      int                    `json:"window_months"`
	WindowFrom        string                 `json:"window_from,omitempty"`
	WindowTo          string                 `json:"window_to,omitempty"`
	AvgMonthlyExpense float64                `json:"avg_monthly_expense"`
	MonthsAnalyzed    int                    `json:"months_analyzed"`
	SpanFrom          string                 `json:"span_from,omitempty"`
	SpanTo            string                 `json:"span_to,omitempty"`
	// Inflows that matched no category and have no user category: likely
	// own-account transfers, so they count as neither income nor a refund.
	UnclassifiedInflow      float64 `json:"unclassified_inflow"`
	UnclassifiedInflowCount int     `json:"unclassified_inflow_count"`
}

// computeSpending aggregates classified cash entries.
//   - Income is the income bucket (salary, interest, refunds); spending is
//     the fixed and variable buckets; transfers, investments, asset
//     purchases and loan principal count as neither, on both sides.
//   - An inflow in a spending category nets against that category (a shop
//     refund). An inflow that matched no rule and has no user category is
//     left out and reported as unclassified.
//   - The month denominator is the calendar span of the data, first to last
//     month with any checking/savings activity, counting months without
//     activity.
//   - The savings-rate window is the last 12 calendar months ending in the
//     month of `now`, limited to that span.
func computeSpending(entries []spendingEntry, now time.Time) spendingSummary {
	res := spendingSummary{
		Monthly:       []spendingMonth{},
		Categories:    []spendingCategory{},
		Subscriptions: []spendingSubscription{},
	}
	if len(entries) == 0 {
		return res
	}
	monthStart := func(t time.Time) time.Time { return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC) }
	spanFrom, spanTo := monthStart(entries[0].Date), monthStart(entries[0].Date)
	asOf := entries[0].Date
	for _, e := range entries {
		if m := monthStart(e.Date); m.Before(spanFrom) {
			spanFrom = m
		} else if m.After(spanTo) {
			spanTo = m
		}
		if e.Date.After(asOf) {
			asOf = e.Date
		}
	}

	months := map[string]*spendingMonth{}
	var order []string
	for m := spanFrom; !m.After(spanTo); m = addMonthsFirstOfMonth(m, 1) {
		key := m.Format("2006-01")
		months[key] = &spendingMonth{Month: key, ByCategory: map[string]float64{}}
		order = append(order, key)
	}
	catTotals := map[string]*spendingCategory{}
	type chargeKey struct {
		counterparty string
		amount       float64
	}
	charges := map[chargeKey][]spendingEntry{}

	for _, e := range entries {
		ms := months[e.Date.Format("2006-01")]
		cat := e.Category
		var spend float64 // > 0 adds to spending, < 0 is a refund
		switch cashflow.BucketOf(cat) {
		case cashflow.BucketIncome:
			if e.Amount > 0 {
				ms.Income += e.Amount
				continue
			}
			// Money out under an income label (e.g. a repaid refund) is
			// still money spent.
			cat = cashflow.CatOther
			spend = -e.Amount
		case cashflow.BucketUnclassified:
			// Unmatched money in (often own money moved between banks) is
			// neither income nor a refund; report it separately.
			if e.Amount > 0 {
				res.UnclassifiedInflow += e.Amount
				res.UnclassifiedInflowCount++
			}
			continue
		case cashflow.BucketFixed, cashflow.BucketVariable:
			if e.Amount > 0 && !e.Explicit && cat == cashflow.CatOther {
				res.UnclassifiedInflow += e.Amount
				res.UnclassifiedInflowCount++
				continue
			}
			spend = -e.Amount
		default: // transfers, investments, asset purchases, loan principal
			continue
		}
		ms.Expenses += spend
		ms.ByCategory[string(cat)] += spend
		ct := catTotals[string(cat)]
		if ct == nil {
			ct = &spendingCategory{Category: string(cat)}
			catTotals[string(cat)] = ct
		}
		ct.Total += spend
		if spend > 0 {
			ct.Count++
		}
		if e.Amount < 0 && !e.LoanPart && strings.TrimSpace(e.Counterparty) != "" {
			key := chargeKey{
				counterparty: strings.ToLower(strings.TrimSpace(e.Counterparty)),
				amount:       math.Round(-e.Amount*100) / 100,
			}
			charges[key] = append(charges[key], e)
		}
	}

	numMonths := float64(len(order))
	windowStart := addMonthsFirstOfMonth(monthStart(now), -11)
	windowEnd := monthStart(now)
	windowIncome, windowExpenses := 0.0, 0.0
	for _, key := range order {
		ms := months[key]
		ms.Net = ms.Income - ms.Expenses
		res.TotalIncome += ms.Income
		res.TotalExpenses += ms.Expenses
		m, _ := time.Parse("2006-01", key)
		if !m.Before(windowStart) && !m.After(windowEnd) {
			windowIncome += ms.Income
			windowExpenses += ms.Expenses
			if res.WindowFrom == "" {
				res.WindowFrom = key
			}
			res.WindowTo = key
			res.WindowMonths++
		}
		ms.Income = math.Round(ms.Income*100) / 100
		ms.Expenses = math.Round(ms.Expenses*100) / 100
		ms.Net = math.Round(ms.Net*100) / 100
		res.Monthly = append(res.Monthly, *ms)
	}
	if res.TotalIncome > 0 {
		res.SavingsRatePct = math.Round((res.TotalIncome-res.TotalExpenses)/res.TotalIncome*1000) / 10
	}
	if windowIncome > 0 {
		rate := math.Round((windowIncome-windowExpenses)/windowIncome*1000) / 10
		res.SavingsRatePct12m = &rate
	}
	res.MonthsAnalyzed = len(order)
	res.SpanFrom = spanFrom.Format("2006-01")
	res.SpanTo = spanTo.Format("2006-01")
	res.AvgMonthlyExpense = math.Round(res.TotalExpenses/numMonths*100) / 100
	res.TotalIncome = math.Round(res.TotalIncome*100) / 100
	res.TotalExpenses = math.Round(res.TotalExpenses*100) / 100
	res.UnclassifiedInflow = math.Round(res.UnclassifiedInflow*100) / 100

	for _, ct := range catTotals {
		if ct.Total <= 0 {
			continue // fully refunded
		}
		ct.AvgMonthly = math.Round(ct.Total/numMonths*100) / 100
		ct.Total = math.Round(ct.Total*100) / 100
		res.Categories = append(res.Categories, *ct)
	}
	sort.Slice(res.Categories, func(i, j int) bool {
		if res.Categories[i].Total != res.Categories[j].Total {
			return res.Categories[i].Total > res.Categories[j].Total
		}
		return res.Categories[i].Category < res.Categories[j].Category
	})

	groups := make([]subscriptionCharges, 0, len(charges))
	for key, list := range charges {
		g := subscriptionCharges{Amount: key.amount}
		sort.Slice(list, func(i, j int) bool { return list[i].Date.Before(list[j].Date) })
		for _, e := range list {
			g.Dates = append(g.Dates, e.Date)
		}
		g.Name = list[len(list)-1].Counterparty
		groups = append(groups, g)
	}
	res.Subscriptions = detectSubscriptions(groups, asOf)
	return res
}

// subscriptionCharges is every charge of one amount to one payee, oldest first.
type subscriptionCharges struct {
	Name   string
	Amount float64
	Dates  []time.Time
}

// subscriptionCadences are the billing cycles recognised by
// detectSubscriptions: a median gap between charges inside [minDays,
// maxDays] snaps to perYear charges a year.
var subscriptionCadences = []struct {
	name             string
	minDays, maxDays float64
	perYear          float64
}{
	{"weekly", 5, 9, 52},
	{"every two weeks", 12, 16, 26},
	{"monthly", 26, 35, 12},
	{"every two months", 55, 66, 6},
	{"quarterly", 80, 100, 4},
	{"half-yearly", 170, 195, 2},
	{"yearly", 340, 390, 1},
}

// detectSubscriptions finds recurring charges: the same payee charging the
// same amount (1-500 €) at least three times. The cadence comes from the
// median gap between charges and must match a regular billing cycle; the
// annual cost is amount × charges per year of that cycle (equivalently
// amount × 365.25 / gap). A subscription whose last charge is more than 1.5
// cycles before asOf (the newest transaction on record) has lapsed and is
// dropped.
func detectSubscriptions(groups []subscriptionCharges, asOf time.Time) []spendingSubscription {
	out := []spendingSubscription{}
	for _, g := range groups {
		if len(g.Dates) < 3 || g.Amount <= 1 || g.Amount >= 500 {
			continue
		}
		dates := append([]time.Time(nil), g.Dates...)
		sort.Slice(dates, func(i, j int) bool { return dates[i].Before(dates[j]) })
		gaps := make([]float64, 0, len(dates)-1)
		for i := 1; i < len(dates); i++ {
			gaps = append(gaps, dates[i].Sub(dates[i-1]).Hours()/24)
		}
		sort.Float64s(gaps)
		median := gaps[len(gaps)/2]
		if len(gaps)%2 == 0 {
			median = (gaps[len(gaps)/2-1] + gaps[len(gaps)/2]) / 2
		}
		cadence := -1
		for i, c := range subscriptionCadences {
			if median >= c.minDays && median <= c.maxDays {
				cadence = i
				break
			}
		}
		if cadence < 0 {
			continue
		}
		c := subscriptionCadences[cadence]
		last := dates[len(dates)-1]
		if asOf.Sub(last).Hours()/24 > 1.5*365.25/c.perYear {
			continue // lapsed
		}
		out = append(out, spendingSubscription{
			Name:        g.Name,
			Amount:      g.Amount,
			Frequency:   len(dates),
			Cadence:     c.name,
			LastCharged: last.Format("2006-01-02"),
			AnnualCost:  math.Round(g.Amount*c.perYear*100) / 100,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].AnnualCost != out[j].AnnualCost {
			return out[i].AnnualCost > out[j].AnnualCost
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// HandleSpending computes the savings rate, spending by category and
// recurring charges from checking and savings accounts, with the same scope
// and classifier as the Cashflow page (see computeSpending).
func (h *AnalysisHandler) HandleSpending(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	now := time.Now()
	to := time.Date(now.Year(), now.Month()+1, 0, 0, 0, 0, 0, time.UTC) // last day of this month
	rows, err := h.queries.ListCashTransactions(ctx, db.ListCashTransactionsParams{
		Date:   time.Date(1900, 1, 1, 0, 0, 0, 0, time.UTC),
		Date_2: to,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list cash transactions: "+err.Error())
		return
	}
	holdings, err := assets.Load(ctx, h.queries)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "load loans: "+err.Error())
		return
	}
	fxRates := loadFXRatesMap(ctx, h.queries)
	histFX := loadHistoricalFXRates(ctx, h.queries)

	entries := spendingEntriesFromCash(rows,
		func(amount float64, currency string, date time.Time) float64 {
			return convertAmountToEURAtDate(amount, currency, date, histFX, fxRates)
		},
		func(id uuid.UUID) (float64, bool, bool) {
			interest, _, kind, ok := holdings.PaymentSplit(id)
			return interest, kind == assets.KindCar, ok
		})
	writeJSON(w, http.StatusOK, computeSpending(entries, now))
}

// taxCalendarFSATitle is a plain string constant (not a format string), so the percent
// sign is written once. FSA = Freistellungsauftrag, the tax exemption order filed with a broker.
const taxCalendarFSATitle = "Freistellungsauftrag (tax exemption order) use below 80 %"

// HandleTaxCalendar generates a 12-month German tax compliance calendar
// with auto-computed amounts based on portfolio data.
// Query: joint=1 for joint filing, church_tax=0.08|0.09 for the church-tax rate.
func (h *AnalysisHandler) HandleTaxCalendar(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	now := time.Now().UTC()
	year := now.Year()
	joint := parseJointParam(r.URL.Query().Get("joint"))
	rate := analytics.CapitalIncomeTaxRate(parseChurchTaxParam(r.URL.Query().Get("church_tax")))
	allowance := analytics.SparerPauschbetragForYear(year, joint)
	allowanceLabel := fmtEUR(allowance) + " €"
	if joint {
		allowanceLabel += " (joint filing)"
	}

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
	d, err := h.loadTaxFilingData(ctx)
	if err != nil {
		// The fixed dates still apply; only the amounts need transactions.
		d = &taxFilingData{secMap: map[string]db.Security{}, tfsMap: map[string]float64{}}
	}
	valByISIN, qtyByISIN := currentFundValues(enriched)

	// --- 1. Vorabpauschale (first working day of the year) ---
	// The Vorabpauschale for `year - 1` counts as received on the first working
	// day of `year` (§ 18 Abs. 3 InvStG); German banks debit the tax that day.
	vpEntries := h.computeYearVorabpauschale(ctx, year-1, d.rows, d.secMap, d.tfsMap, valByISIN, qtyByISIN, d.histFX, d.fxRates)
	fundValue, vpTotal, vpTax := 0.0, 0.0, 0.0
	for _, vp := range vpEntries {
		fundValue += vp.YearEndValue
		vpTotal += vp.Vorabpauschale
		vpTax += vp.TaxOnVP
	}
	if vpTax > 10 {
		vpDate := firstWorkingDayOfYear(year)
		events = append(events, calendarEvent{
			Month: int(vpDate.Month()), Date: vpDate.Format("2006-01-02"),
			Title:       "Vorabpauschale (advance flat-rate fund tax) due",
			Description: fmt.Sprintf("Tax on the %d Vorabpauschale of %s € (deemed income on funds worth %s € at year end). German brokers debit it from the settlement account, so keep enough cash there; it may be lower if your Freistellungsauftrag covers it.", year-1, fmtEUR(vpTotal), fmtEUR(fundValue)),
			Amount:      math.Round(vpTax*100) / 100,
			Category:    "vorabpauschale", Urgency: "deadline",
			ActionURL: "/analysis",
		})
	}

	// --- 2. Freistellungsauftrag Review (Jan) ---
	events = append(events, calendarEvent{
		Month: 1, Date: fmt.Sprintf("%d-01-15", year),
		Title:       "Review your Freistellungsauftrag (tax exemption order)",
		Description: fmt.Sprintf("Check that the %s Sparer-Pauschbetrag (saver's allowance) is split sensibly across your German banks. Foreign brokers such as Morgan Stanley cannot take one; their income gets the allowance through Anlage KAP.", allowanceLabel),
		Amount:      allowance,
		Category:    "fsa", Urgency: "action",
		ActionURL: "/analysis",
	})

	// --- 3. Steuererklärung deadline (§ 149 AO, rolled forward per § 108 Abs. 3 AO) ---
	unadvised, advised := incomeTaxReturnDeadlines(year - 1)
	events = append(events, calendarEvent{
		Month: int(unadvised.Month()), Date: unadvised.Format("2006-01-02"),
		Title:       fmt.Sprintf("Steuererklärung (income tax return) %d due", year-1),
		Description: fmt.Sprintf("File your %d income tax return, including Anlage KAP for income without German withholding. With a Steuerberater (tax adviser) the deadline is %s.", year-1, advised.Format("02.01.2006")),
		Category:    "steuererklaerung", Urgency: "deadline",
		ActionURL: "/analysis",
	})

	// --- 4. Verlustbescheinigung beantragen (Dec 15) ---
	events = append(events, calendarEvent{
		Month: 12, Date: fmt.Sprintf("%d-12-15", year),
		Title:       "Request a Verlustbescheinigung (loss certificate)",
		Description: "To offset losses at one German bank against gains at another, ask the loss-making bank for a loss certificate by 15 December (§ 43a Abs. 3 S. 4 EStG) and file Anlage KAP. Without it the bank carries the loss forward.",
		Category:    "verlust", Urgency: "deadline",
	})

	// This year's realised capital income per bank, for the harvest and FSA checks.
	ytdBanks := aggregateCapitalIncome(d.capitalIncomeWithVP(year, vpEntries))

	// --- 5. Tax-Loss Harvesting Window (Q4) ---
	// Sell-all per depot in FIFO order: a position's loss is the sum over all its
	// lots, not just the lots under water. The saving is what that loss removes
	// from this year's taxable income in the same pots, after the allowance.
	prices := make(map[string]float64, len(valByISIN))
	for isin, val := range valByISIN {
		if qty := qtyByISIN[isin]; qty > 0 {
			prices[isin] = val / qty
		}
	}
	assetClass := make(map[string]string, len(d.secMap))
	for isin, s := range d.secMap {
		assetClass[isin] = s.AssetClass
	}
	lots := analytics.ComputeTaxLots(d.taxTxns, prices, d.names, d.equity)
	priced := lots[:0:0]
	for _, l := range lots {
		if prices[l.ISIN] > 0 {
			priced = append(priced, l)
		}
	}
	harvest := harvestAnalysis(priced, assetClass, d.excludedAccounts(), ytdBanks, allowance, rate)
	if harvest.GrossLoss > 500 {
		desc := fmt.Sprintf("%d positions are at a loss: selling them in full (FIFO order) would realise %s € of losses.", harvest.Positions, fmtEUR(harvest.GrossLoss))
		if harvest.Positions == 1 {
			desc = fmt.Sprintf("One position is at a loss: selling it in full (FIFO order) would realise %s € of losses.", fmtEUR(harvest.GrossLoss))
		}
		if harvest.Saving >= 1 {
			desc += fmt.Sprintf(" Against this year's realised income that saves about %s € in tax.", fmtEUR(harvest.Saving))
		} else {
			desc += " There are no gains in the same loss pot to offset this year, so it would save no tax now."
		}
		if harvest.CarryForward >= 1 {
			desc += fmt.Sprintf(" %s € would carry forward to later years (share losses only against share gains).", fmtEUR(harvest.CarryForward))
		}
		urgency := "action"
		if harvest.Saving < 1 {
			urgency = "info"
		}
		events = append(events, calendarEvent{
			Month: 10, Date: fmt.Sprintf("%d-10-01", year),
			Title:       "Tax-loss harvesting window",
			Description: desc,
			Amount:      math.Round(harvest.Saving),
			Category:    "harvest", Urgency: urgency,
			ActionURL: "/analysis",
		})
	}

	// --- 6. FSA Mid-Year Check (Jul) ---
	// Projected use of the allowance: dividends and interest annualised, realised
	// gains (net of losses per bank) and the Vorabpauschale as they are.
	monthsElapsed := int(now.Month())
	if monthsElapsed >= 6 {
		projected := projectedAllowanceUse(ytdBanks, monthsElapsed)
		if projected < allowance*0.8 {
			unused := allowance - projected
			events = append(events, calendarEvent{
				Month: 7, Date: fmt.Sprintf("%d-07-01", year),
				Title:       taxCalendarFSATitle,
				Description: fmt.Sprintf("On course to use only %s € of the %s Sparer-Pauschbetrag (saver's allowance). About %s € would go unused; realising gains before year end would use it tax-free.", fmtEUR(projected), allowanceLabel, fmtEUR(unused)),
				Amount:      math.Round(unused * rate),
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

// volElevatedPct is the 30-day annualised volatility (in %) above which the
// Risk tab flags elevated volatility; the flag and its message use the same
// threshold so the callout never shows up empty.
const volElevatedPct = 25.0

// HandleVolatilityContext provides factual context during elevated volatility.
// It uses the same investable, flow-adjusted and smoothed series as the Risk
// tab's KPI tiles.
func (h *AnalysisHandler) HandleVolatilityContext(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	snaps, err := h.queries.ListNetWorthSnapshots(ctx, 3000)
	if err != nil || len(snaps) < 31 {
		writeJSON(w, http.StatusOK, map[string]any{"elevated": false})
		return
	}

	raw := investableRiskSeries(snaps)
	points := analytics.PrepareRiskSeries(analytics.BuildCashflowAdjustedSeries(raw, h.buildInvestableFlowsForRisk(ctx)))
	if len(points) < 31 {
		writeJSON(w, http.StatusOK, map[string]any{"elevated": false})
		return
	}
	writeJSON(w, http.StatusOK, volatilityContext(points, raw))
}

// volatilityContext builds the elevated-volatility callout from a prepared
// flow-adjusted series (points) and the recorded values (raw).
func volatilityContext(points, raw []analytics.DailyValuation) map[string]any {
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

	vol30d := analytics.RecentVolatility(points, 30) * 100

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

	avgRecovery := 0
	if len(episodes) > 0 {
		for _, ep := range episodes {
			avgRecovery += ep.RecoveryDays
		}
		avgRecovery /= len(episodes)
	}

	// Elevated volatility detection. Every case that sets the flag also sets
	// a reason and a message, so the callout is never empty.
	reason, message := "", ""
	switch {
	case drawdownPct < -10:
		reason = "deep_drawdown"
		message = fmt.Sprintf("Returns are %.1f%% below their peak of %s. ", math.Abs(drawdownPct), peakDate)
		if len(episodes) > 0 {
			message += fmt.Sprintf("The portfolio has recovered from %d drawdowns before, averaging %d days to recover.", len(episodes), avgRecovery)
		}
	case drawdownPct < -5:
		reason = "drawdown"
		message = fmt.Sprintf("Returns are %.1f%% below their peak. This is within normal market fluctuation.", math.Abs(drawdownPct))
	case vol30d > volElevatedPct:
		reason = "volatility"
		message = fmt.Sprintf("30-day volatility is elevated at %.0f%% (annualized). Markets are choppy but returns are near their peak.", vol30d)
	}
	elevated := reason != ""

	// Recorded high and current value of cash and securities (the index above
	// is scaled, so its peak is not a value the portfolio ever had).
	recordedHigh, recordedHighDate := 0.0, ""
	for _, p := range raw {
		if p.Value > recordedHigh {
			recordedHigh, recordedHighDate = p.Value, p.Date.Format("2006-01-02")
		}
	}
	recordedCurrent := 0.0
	if len(raw) > 0 {
		recordedCurrent = raw[len(raw)-1].Value
	}

	return map[string]any{
		"elevated":          elevated,
		"reason":            reason,
		"current_value":     math.Round(recordedCurrent),
		"all_time_high":     math.Round(recordedHigh),
		"ath_date":          recordedHighDate,
		"peak_date":         peakDate,
		"drawdown_pct":      math.Round(drawdownPct*10) / 10,
		"vol_30d":           math.Round(vol30d*10) / 10,
		"vol_threshold":     volElevatedPct,
		"message":           message,
		"past_drawdowns":    episodes,
		"drawdown_count":    len(episodes),
		"avg_recovery_days": avgRecovery,
	}
}

// HandleOpportunityCost computes opportunity costs from cash drag, timing delays, and FSA waste.
// Query params: monthly_expenses (€) and reserve_months (default 6) size the
// emergency reserve the same way as the Contribution Router; joint=1 doubles
// the saver's allowance; church_rate (0–0.09) sets the capital-income rate.
func (h *AnalysisHandler) HandleOpportunityCost(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	now := time.Now().UTC()
	year := now.Year()
	q := r.URL.Query()

	monthlyExpenses := 0.0
	if v, err := parseFiniteFloat(q.Get("monthly_expenses")); err == nil && v > 0 && v <= 1e7 {
		monthlyExpenses = v
	}
	reserveMonths := float64(analytics.PlanningReserveMonths)
	if v, err := parseFiniteFloat(q.Get("reserve_months")); err == nil && v >= 0 && v <= 36 {
		reserveMonths = v
	}
	joint := q.Get("joint") == "1"
	churchRate := 0.0
	if v, err := parseFiniteFloat(q.Get("church_rate")); err == nil && v >= 0 && v <= 0.09 {
		churchRate = v
	}
	capRate := analytics.CapitalIncomeTaxRate(churchRate)

	accts, _ := h.queries.ListAccounts(ctx)
	txns, _ := listAllTransactionsRows(ctx, h.queries)
	secs, _ := h.queries.ListSecurities(ctx)
	enriched, _ := h.loadEnrichedHoldings(ctx)
	fxRates := loadFXRatesMap(ctx, h.queries)
	histFX := loadHistoricalFXRates(ctx, h.queries)
	tfsMap := make(map[string]float64, len(secs))
	secMap := make(map[string]db.Security, len(secs))
	for _, s := range secs {
		tfsMap[s.ISIN] = securityTFSRate(s)
		secMap[s.ISIN] = s
	}

	// Portfolio total and annual return estimate
	portfolioValue := 0.0
	valueByISIN := make(map[string]float64)
	qtyByISIN := make(map[string]float64)
	unrealisedTaxable := 0.0 // § 20 gains that could be realised tax-free within the allowance
	for _, eh := range enriched {
		portfolioValue += eh.value
		if eh.value > 0 {
			valueByISIN[eh.security.ISIN] += eh.value
			qtyByISIN[eh.security.ISIN] += numericToFloat(eh.holding.Quantity)
		}
		if gain := eh.value - eh.costValue; gain > 0 && !analytics.IsPrivateSaleAssetClass(eh.security.AssetClass) {
			unrealisedTaxable += gain * (1 - tfsMap[eh.security.ISIN])
		}
	}
	annualReturnPct := analytics.PlanningDefaultReturnPct
	annualReturn := annualReturnPct / 100
	cashRatePct := analytics.PlanningCashRatePct
	cashSpread := math.Max(0, annualReturnPct-cashRatePct) / 100

	// --- 1. Cash Drag Analysis ---
	// Idle cash still earns savings interest, so the cost is the spread between
	// the expected portfolio return and the stated cash rate.
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
		cashDragAccounts = append(cashDragAccounts, accountCashDrag{
			AccountName: acc.Name, AccountType: acc.Type,
			CashBalance:     math.Round(cash*100) / 100,
			OpportunityCost: math.Round(cash*cashSpread*100) / 100,
		})
	}
	sort.Slice(cashDragAccounts, func(i, j int) bool {
		return cashDragAccounts[i].CashBalance > cashDragAccounts[j].CashBalance
	})

	// Reserve: the same months-of-expenses rule as the Contribution Router.
	// Without known expenses no cash is called idle.
	reserveKnown := monthlyExpenses > 0
	recommendedReserve := monthlyExpenses * reserveMonths
	excessCash, cashDragAnnual := 0.0, 0.0
	if reserveKnown {
		excessCash, cashDragAnnual = idleCashCost(totalCash, recommendedReserve, annualReturnPct, cashRatePct)
	}

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
	// Euros that would have to move to reach the targets (half the summed
	// absolute drift), assumed to earn 2 % less a year.
	targets, _ := h.queries.ListTargetAllocations(ctx)
	rebalanceCost := 0.0
	if len(targets) > 0 && portfolioValue > 0 {
		targetPct := make(map[string]float64, len(targets))
		for _, t := range targets {
			targetPct[t.SecurityISIN] += numericToFloat(t.TargetWeightPct)
		}
		rebalanceCost = rebalanceTurnover(valueByISIN, targetPct) * 0.02
	}

	// --- 4. Unused saver's allowance ---
	// Income using the allowance this year: dividends and interest (projected
	// to the full year), gains and losses already realised, and the
	// Vorabpauschale deemed received on the first working day of the year.
	recurringYTD := 0.0
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
				recurringYTD += amtEUR
			}
		}
	}
	monthsElapsed := float64(now.Month())
	if monthsElapsed < 1 {
		monthsElapsed = 1
	}
	projectedRecurring := recurringYTD / monthsElapsed * 12

	realisedTaxable := 0.0
	taxTxns, _, _, _ := buildTaxTransactions(txns, secs, histFX, fxRates)
	if summary := analytics.ComputeTaxSummary(taxTxns, year, nil); summary != nil {
		for _, s := range summary.BySecurity {
			if analytics.IsPrivateSaleAssetClass(secMap[s.ISIN].AssetClass) {
				continue
			}
			realisedTaxable += s.TaxablePL
		}
	}
	vorabTaxable := 0.0
	for _, vp := range h.computeYearVorabpauschale(ctx, year-1, txns, secMap, tfsMap, valueByISIN, qtyByISIN, histFX, fxRates) {
		vorabTaxable += vp.Vorabpauschale * (1 - tfsMap[vp.ISIN])
	}

	allowance := analytics.SparerPauschbetragForYear(year, joint)
	projectedIncome := math.Max(0, projectedRecurring+realisedTaxable+vorabTaxable)
	fsaUnused := math.Max(allowance-projectedIncome, 0)
	// The allowance is only worth something if there are gains to realise
	// into it (sell and rebuy to step up the cost basis).
	fsaHarvestable := math.Min(fsaUnused, unrealisedTaxable)
	fsaWaste := fsaHarvestable * capRate

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
			"accounts":            cashDragAccounts,
			"total_cash":          math.Round(totalCash*100) / 100,
			"recommended_reserve": math.Round(recommendedReserve),
			"reserve_known":       reserveKnown,
			"reserve_months":      reserveMonths,
			"excess_cash":         math.Round(excessCash),
			"annual_cost":         math.Round(cashDragAnnual),
			"expected_return_pct": annualReturnPct,
			"cash_rate_pct":       cashRatePct,
		},
		"timing": map[string]any{
			"avg_invest_day": avgDay,
			"avg_monthly":    math.Round(avgMonthly),
			"annual_cost":    math.Round(avgDelayCost),
		},
		"rebalance": map[string]any{
			"annual_cost": math.Round(rebalanceCost),
		},
		"fsa_waste": map[string]any{
			"allowance":           allowance,
			"projected_income":    math.Round(projectedIncome),
			"projected_recurring": math.Round(projectedRecurring),
			"realised_gains":      math.Round(realisedTaxable),
			"vorabpauschale":      math.Round(vorabTaxable),
			"unused":              math.Round(fsaUnused),
			"harvestable":         math.Round(fsaHarvestable),
			"annual_cost":         math.Round(fsaWaste),
		},
		"total_annual":   math.Round(totalAnnual),
		"monthly_rollup": monthlySeries,
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

	// Holdings come per account; the price and dividend checks are about the
	// security, so they run once per ISIN on the summed value (otherwise one
	// ISIN held in two depots is reported twice).
	type isinHolding struct {
		security db.Security
		value    float64
	}
	var byISIN []*isinHolding
	isinIdx := make(map[string]*isinHolding)
	for _, eh := range enriched {
		ih := isinIdx[eh.security.ISIN]
		if ih == nil {
			ih = &isinHolding{security: eh.security}
			isinIdx[eh.security.ISIN] = ih
			byISIN = append(byISIN, ih)
		}
		ih.value += eh.value
	}

	// --- 1. Stale Prices ---
	for _, eh := range byISIN {
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
	for _, eh := range byISIN {
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

	// Static crisis library: approximate MSCI World price-index drawdowns in
	// US dollars, and the index's recovery time after the trough. In euros the
	// declines differed; the page says so.
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
		writeError(w, http.StatusInternalServerError, "load holdings: "+err.Error())
		return
	}

	totalValue, equityValue, cryptoValue := 0.0, 0.0, 0.0
	for _, eh := range enriched {
		eq, cr := stressExposure(eh.security)
		totalValue += eh.value
		equityValue += eh.value * eq
		cryptoValue += eh.value * cr
	}
	equityShare, cryptoShare := 0.0, 0.0
	if totalValue > 0 {
		equityShare = equityValue / totalValue
		cryptoShare = cryptoValue / totalValue
	}

	type portfolioImpact struct {
		Name           string  `json:"name"`
		DrawdownPct    float64 `json:"drawdown_pct"`
		DrawdownEUR    float64 `json:"drawdown_eur"`
		RecoveryMonths int     `json:"recovery_months"`
		WorstMonthPct  float64 `json:"worst_month_pct"`
		WorstMonthEUR  float64 `json:"worst_month_eur"`
	}

	// Net contributions of the last 12 months, the rate at which money keeps
	// going in during a crisis (project rule: trailing 12 months, not an
	// all-time average).
	now := time.Now().UTC()
	monthlyContribution := trailingMonthlyContribution(h.buildInvestableFlowsForRisk(ctx), now)

	type dcaResult struct {
		Name         string `json:"name"`
		WithoutDCA   int    `json:"without_dca_months"`
		WithDCA      int    `json:"with_dca_months"`
		Acceleration int    `json:"acceleration_months"`
	}
	impacts := make([]portfolioImpact, len(scenarios))
	dcaRecovery := make([]dcaResult, len(scenarios))
	for i, s := range scenarios {
		drawdownPct := crisisDrop(s.IndexReturn, equityShare, cryptoShare)
		worstMonthPct := crisisDrop(s.WorstMonth, equityShare, cryptoShare)
		impacts[i] = portfolioImpact{
			Name:           s.Name,
			DrawdownPct:    math.Round(drawdownPct*10) / 10,
			DrawdownEUR:    math.Round(totalValue * drawdownPct / 100),
			RecoveryMonths: s.RecoveryMo,
			WorstMonthPct:  math.Round(worstMonthPct*10) / 10,
			WorstMonthEUR:  math.Round(totalValue * worstMonthPct / 100),
		}
		withDCA := dcaRecoveryMonths(drawdownPct, scenarioMonths(s.StartDate, s.EndDate), s.RecoveryMo, totalValue, monthlyContribution)
		dcaRecovery[i] = dcaResult{
			Name:         s.Name,
			WithoutDCA:   s.RecoveryMo,
			WithDCA:      withDCA,
			Acceleration: s.RecoveryMo - withDCA,
		}
	}

	// Per-holding impact for worst crisis (2008 GFC)
	type holdingImpact struct {
		Name        string  `json:"name"`
		ISIN        string  `json:"isin"`
		Value       float64 `json:"value"`
		IsEquity    bool    `json:"is_equity"`
		EquityPct   float64 `json:"equity_pct"`
		IsCrypto    bool    `json:"is_crypto"`
		DrawdownEUR float64 `json:"drawdown_eur"`
		DrawdownPct float64 `json:"drawdown_pct"`
	}
	worstDrawdown := scenarios[0].IndexReturn // GFC = -54%
	holdingImpacts := make([]holdingImpact, 0, len(enriched))
	for _, eh := range enriched {
		eq, cr := stressExposure(eh.security)
		dd := crisisDrop(worstDrawdown, eq, cr)
		holdingImpacts = append(holdingImpacts, holdingImpact{
			Name:        eh.security.Name,
			ISIN:        eh.security.ISIN,
			Value:       math.Round(eh.value),
			IsEquity:    eq >= 0.5,
			EquityPct:   math.Round(eq * 100),
			IsCrypto:    cr > 0,
			DrawdownEUR: math.Round(eh.value * dd / 100),
			DrawdownPct: math.Round(dd*10) / 10,
		})
	}

	// Cash buffer: liquid cash (checking, savings and broker cash) against
	// recorded spending; the 5 % rule of thumb only when nothing is recorded.
	allAccounts, _ := h.queries.ListAccounts(ctx)
	fxRates := loadFXRatesMap(ctx, h.queries)
	histFX := loadHistoricalFXRates(ctx, h.queries)
	cashByAcct, _ := computeAllAccountCashEUR(ctx, h.queries, allAccounts, fxRates)
	cashBuffer := liquidCashEUR(allAccounts, cashByAcct)

	var txnsChrono []db.ListTransactionsRow
	if txns, err := listAllTransactionsRows(ctx, h.queries); err == nil {
		txnsChrono = make([]db.ListTransactionsRow, len(txns))
		for i := range txns {
			txnsChrono[i] = txns[len(txns)-1-i]
		}
	}
	physHoldings, _ := assets.Load(ctx, h.queries)
	monthlyExpense, spendingMonths := recordedMonthlySpending(txnsChrono, allAccounts, physHoldings.AssetLinkedIDs(), histFX, fxRates, now)
	spendingBasis := "recorded"
	if monthlyExpense <= 0 {
		monthlyExpense = totalValue * 0.05 / 12
		spendingBasis = "assumed"
		spendingMonths = 0
	}
	cashMonths := 0.0
	if monthlyExpense > 0 {
		cashMonths = cashBuffer / monthlyExpense
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"scenarios":            scenarios,
		"portfolio_impact":     impacts,
		"holding_impacts":      holdingImpacts,
		"dca_recovery":         dcaRecovery,
		"monthly_contribution": math.Round(monthlyContribution),
		"portfolio_value":      math.Round(totalValue),
		"equity_pct":           math.Round(equityShare*1000) / 10,
		"crypto_pct":           math.Round(cryptoShare*1000) / 10,
		"crypto_beta":          cryptoCrisisBeta,
		"cash_buffer":          math.Round(cashBuffer),
		"cash_buffer_months":   math.Round(cashMonths*10) / 10,
		"monthly_spending":     math.Round(monthlyExpense),
		"spending_basis":       spendingBasis,
		"spending_months":      math.Round(spendingMonths*10) / 10,
	})
}

const (
	// cryptoCrisisBeta assumes crypto falls twice as far as equities in a
	// crisis (2022: MSCI World about −25 %, Bitcoin about −65 %; March 2020:
	// −34 % vs about −50 %), capped at cryptoMaxCrisisDrop. Crypto did not
	// exist in the older scenarios; this is a stated assumption, not data.
	cryptoCrisisBeta    = 2.0
	cryptoMaxCrisisDrop = -90.0
	// mixedFundEquityShare is assumed for a mixed fund (15 % Teilfreistellung,
	// at least 25 % equities) without a recorded equity_pct.
	mixedFundEquityShare = 0.5
)

// stressExposure returns the share of a holding's value that moves with
// equities and the share that moves like crypto in a crisis. Funds use
// securities.equity_pct when recorded; otherwise equity funds count fully
// and mixed funds half. Bonds, gold and other commodities are not shocked.
func stressExposure(sec db.Security) (equity, crypto float64) {
	switch sec.AssetClass {
	case "crypto":
		return 0, 1
	case "stock":
		return 1, 0
	case "etf", "fund":
		if sec.EquityPct.Valid {
			v := numericToFloat(sec.EquityPct)
			if v > 1 {
				v /= 100
			}
			return math.Max(0, math.Min(1, v)), 0
		}
		switch securityTFSRate(sec) {
		case analytics.TeilfreistellungEquity:
			return 1, 0
		case analytics.TeilfreistellungMixed:
			return mixedFundEquityShare, 0
		}
	}
	return 0, 0
}

// crisisDrop scales an equity-index drop (negative %) to a holding or
// portfolio with the given equity and crypto shares.
func crisisDrop(indexDropPct, equityShare, cryptoShare float64) float64 {
	cryptoDrop := math.Max(indexDropPct*cryptoCrisisBeta, cryptoMaxCrisisDrop)
	return indexDropPct*equityShare + cryptoDrop*cryptoShare
}

// scenarioMonths is the length of a crisis' decline in whole months (≥ 1).
func scenarioMonths(start, end string) int {
	s, err1 := time.Parse("2006-01-02", start)
	e, err2 := time.Parse("2006-01-02", end)
	if err1 != nil || err2 != nil || !e.After(s) {
		return 1
	}
	m := int(math.Round(e.Sub(s).Hours() / 24 / 30.44))
	if m < 1 {
		m = 1
	}
	return m
}

// trailingMonthlyContribution is the net external flow (deposits, vests and
// transfers in, minus withdrawals) of the 12 months up to now, per month.
func trailingMonthlyContribution(flows []analytics.CashFlow, now time.Time) float64 {
	from := now.AddDate(-1, 0, 0)
	total := 0.0
	for _, f := range flows {
		if f.Date.After(from) && !f.Date.After(now) {
			total += f.Amount
		}
	}
	return total / 12
}

// dcaRecoveryMonths simulates a crisis path (a steady fall of dropPct over
// declineMonths, then a steady climb back over recoveryMonths, both
// geometric) with `monthly` invested at each month-end price. It returns the
// months after the trough until the value covers everything put in (start
// value plus contributions). Without contributions that is recoveryMonths,
// the index's own recovery.
func dcaRecoveryMonths(dropPct float64, declineMonths, recoveryMonths int, startValue, monthly float64) int {
	if dropPct >= 0 || startValue <= 0 || monthly <= 0 || recoveryMonths <= 0 {
		return recoveryMonths
	}
	if declineMonths < 1 {
		declineMonths = 1
	}
	trough := 1 + dropPct/100
	if trough <= 0 {
		return recoveryMonths
	}
	price := func(t int) float64 {
		if t <= declineMonths {
			return math.Pow(trough, float64(t)/float64(declineMonths))
		}
		if t >= declineMonths+recoveryMonths {
			return 1
		}
		return math.Pow(trough, 1-float64(t-declineMonths)/float64(recoveryMonths))
	}
	units, invested := startValue, startValue
	for t := 1; t <= declineMonths+recoveryMonths; t++ {
		p := price(t)
		units += monthly / p
		invested += monthly
		if t > declineMonths && units*p >= invested {
			return t - declineMonths
		}
	}
	return recoveryMonths
}

// liquidCashEUR sums positive cash on active checking, savings and brokerage
// accounts: money that can be spent without selling anything.
func liquidCashEUR(accounts []db.Account, cashByAcct map[uuid.UUID]float64) float64 {
	total := 0.0
	for _, a := range accounts {
		if !a.IsActive {
			continue
		}
		switch a.Type {
		case "checking", "savings", "brokerage":
			if c := cashByAcct[a.ID]; c > 0 {
				total += c
			}
		}
	}
	return total
}

// recordedMonthlySpending averages withdrawals from checking accounts over
// the last 12 months, dividing by the months the imports actually cover
// (from the window start, or the first checking transaction if later, to the
// last checking transaction), so a short history is not diluted. Payments
// for tracked assets (skip) are not spending. Returns 0 when nothing is
// recorded.
func recordedMonthlySpending(txnsChrono []db.ListTransactionsRow, accounts []db.Account, skip map[uuid.UUID]bool, histFX []market.HistoricalFXRate, fxRates map[string]float64, now time.Time) (perMonth, months float64) {
	checking := make(map[uuid.UUID]bool)
	for _, a := range accounts {
		if a.IsActive && a.Type == "checking" {
			checking[a.ID] = true
		}
	}
	from := now.AddDate(-1, 0, 0)
	var first, last time.Time
	total := 0.0
	for _, t := range txnsChrono {
		if !checking[t.AccountID] || t.Date.After(now) {
			continue
		}
		if first.IsZero() || t.Date.Before(first) {
			first = t.Date
		}
		if !t.Date.After(from) {
			continue
		}
		if t.Date.After(last) {
			last = t.Date
		}
		if t.Type == "withdrawal" && !skip[t.ID] {
			total += math.Abs(convertAmountToEURAtDate(numericToFloat(t.Amount), t.Currency, t.Date, histFX, fxRates))
		}
	}
	if total <= 0 || last.IsZero() {
		return 0, 0
	}
	start := from
	if first.After(from) {
		start = first
	}
	months = last.Sub(start).Hours() / 24 / 30.44
	if months < 1 {
		months = 1
	}
	return total / months, months
}
