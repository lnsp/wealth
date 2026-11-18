package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/lnsp/wealth/internal/analytics"
	"github.com/lnsp/wealth/internal/assets"
	db "github.com/lnsp/wealth/internal/database/generated"
	"github.com/lnsp/wealth/internal/market"
)

const benchmarkISIN = "IE00B4L5Y983" // iShares Core MSCI World
const benchmarkTicker = "IWDA.AS"

// convertToEUR converts an amount from the given currency to EUR using the latest ECB rate.
// ECB rates are EUR→currency (1 EUR = X currency), so amountEUR = amount / rate.
// Returns the amount unchanged if already EUR or if no rate is available.
func convertToEUR(ctx context.Context, q *db.Queries, amount float64, currency string) float64 {
	if math.IsNaN(amount) || math.IsInf(amount, 0) || amount == 0 {
		return 0
	}
	norm, scale := normalizeCurrencyAndScale(currency)
	scaled := amount * scale
	if norm == "EUR" {
		return scaled
	}
	rateRow, err := q.GetLatestExchangeRate(ctx, norm)
	if err != nil {
		return scaled // no rate available, return scaled base-unit amount
	}
	rate := numericToFloat(rateRow.Rate)
	if rate <= 0 {
		return scaled
	}
	return scaled / rate
}

type PortfolioHandler struct {
	queries         *db.Queries
	benchmarkISIN   string
	benchmarkTicker string
}

func NewPortfolioHandler(q *db.Queries) *PortfolioHandler {
	return &PortfolioHandler{
		queries:         q,
		benchmarkISIN:   benchmarkISIN,
		benchmarkTicker: benchmarkTicker,
	}
}

// SetBenchmarkConfig configures the default benchmark ISIN and Yahoo ticker.
func (h *PortfolioHandler) SetBenchmarkConfig(isin, ticker string) {
	if strings.TrimSpace(isin) != "" {
		h.benchmarkISIN = strings.TrimSpace(isin)
	}
	if strings.TrimSpace(ticker) != "" {
		h.benchmarkTicker = strings.TrimSpace(ticker)
	}
}

// priceMap holds latest prices indexed by ISIN for batch lookups.
type priceMap map[string]db.ListLatestPricesRow

func (h *PortfolioHandler) loadPriceMap(ctx context.Context) priceMap {
	prices, err := h.queries.ListLatestPrices(ctx)
	if err != nil {
		return nil
	}
	m := make(priceMap, len(prices))
	for _, p := range prices {
		m[p.SecurityISIN] = p
	}
	return m
}

func (h *PortfolioHandler) HandleHoldings(w http.ResponseWriter, r *http.Request) {
	holdings, err := h.queries.ListCurrentHoldings(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list holdings: "+err.Error())
		return
	}

	// Filter by user's accounts
	if allowedIDs := userAccountIDs(r.Context(), h.queries.DB()); allowedIDs != nil {
		allowed := make(map[uuid.UUID]bool, len(allowedIDs))
		for _, id := range allowedIDs { allowed[id] = true }
		filtered := holdings[:0]
		for _, h := range holdings {
			if allowed[h.AccountID] { filtered = append(filtered, h) }
		}
		holdings = filtered
	}

	// Account filter
	filterAccountID := r.URL.Query().Get("account_id")

	// Build account name map
	accts, _ := h.queries.ListAccounts(r.Context())
	acctNames := make(map[string]string)
	for _, a := range accts {
		acctNames[a.ID.String()] = a.Name
	}

	type holdingWithPrice struct {
		db.ListCurrentHoldingsRow
		AccountName    string   `json:"account_name"`
		PriceCurrency  string   `json:"price_currency,omitempty"`
		CurrentPrice   *float64 `json:"current_price"`
		PriceDate      string   `json:"price_date,omitempty"`
		MarketValue    *float64 `json:"market_value"`
		UnrealizedPL   *float64 `json:"unrealized_pl"`
		WeightPct      *float64 `json:"weight_pct"`
		FXExposure     string   `json:"fx_exposure,omitempty"`      // dominant currency
		FXImpactPct    *float64 `json:"fx_impact_pct,omitempty"`    // estimated FX contribution to return
		AssetReturnPct *float64 `json:"asset_return_pct,omitempty"` // return excluding FX
		// ValueEUR is what this position contributes to account balances and
		// net worth: market value, or EUR cost basis when no price is known.
		// PriceMissing flags the latter.
		ValueEUR     float64 `json:"value_eur"`
		PriceMissing bool    `json:"price_missing"`
		// CostBasisEUR is the moving-average cost of the units held, each
		// purchase converted at the ECB rate of its own date (so the gain
		// includes the currency move since purchase). Tax uses FIFO instead.
		CostBasisEUR float64 `json:"cost_basis_eur"`
		AvgCostEUR   float64 `json:"avg_cost_eur"`
		// AvgCostPaid is the average cost per unit in the currency the units
		// were paid in (AvgCostCurrency); omitted when purchases mix currencies.
		AvgCostPaid     *float64 `json:"avg_cost_paid,omitempty"`
		AvgCostCurrency string   `json:"avg_cost_currency,omitempty"`
		// Dividends received on this position, EUR at each payment date's ECB
		// rate: gross, and net of withheld tax and fees.
		DividendsEUR    float64 `json:"dividends_eur"`
		DividendsNetEUR float64 `json:"dividends_net_eur"`
		// ISINWeightPct is the ISIN's share of the whole portfolio across all
		// accounts, for concentration checks (weight_pct is per row).
		ISINWeightPct *float64 `json:"isin_weight_pct"`
	}

	ctx := r.Context()
	// Batch-load all latest prices and FX rates in one query (eliminates N+1)
	prices := h.loadPriceMap(ctx)
	fxRates := loadFXRatesMap(ctx, h.queries)
	fx := newFXTable(loadHistoricalFXRates(ctx, h.queries), fxRates)
	costs := loadPositionCosts(ctx, h.queries, fx)

	// Load securities for country weights → FX exposure
	secs, _ := h.queries.ListSecurities(ctx)
	secMap := make(map[string]db.Security, len(secs))
	for _, s := range secs {
		secMap[s.ISIN] = s
	}

	// One-year currency effect for a EUR investor holding foreign assets:
	// EUR value = local value / rate, so the FX return is old/new − 1. The
	// rate a year back is looked up by date, not by row count.
	today := civilDay(time.Now())
	yearAgo := today.AddDate(-1, 0, 0)
	fxChanges := make(map[string]float64) // currency → 1yr change %
	for _, cur := range []string{"USD", "GBP", "JPY", "CHF"} {
		if s := fx.series[cur]; len(s) == 0 || s[0].date.After(yearAgo) {
			continue // no rate a year back: no estimate
		}
		oldRate, newRate := fx.rate(cur, yearAgo), fx.rate(cur, today)
		if oldRate > 0 && newRate > 0 {
			fxChanges[cur] = (oldRate/newRate - 1) * 100
		}
	}

	// valueEUR prices a holding row: market value at the latest close, or the
	// EUR cost basis when no price is stored.
	valueEUR := func(holding db.ListCurrentHoldingsRow) float64 {
		qty := numericToFloat(holding.Quantity)
		if p, ok := prices[holding.SecurityISIN]; ok && p.Close.Valid {
			return convertAmountToEUR(qty*numericToFloat(p.Close), p.Currency, fxRates)
		}
		if c := costs[positionKey{AccountID: holding.AccountID, ISIN: holding.SecurityISIN}]; c != nil && c.Qty > 0 {
			return c.AvgCostEUR() * qty
		}
		return convertAmountToEUR(qty*numericToFloat(holding.AvgCostBasis), holding.Currency, fxRates)
	}

	// Portfolio-wide weight per ISIN (all accounts), for concentration.
	isinValue := make(map[string]float64)
	portfolioValue := 0.0
	for _, holding := range holdings {
		v := valueEUR(holding)
		isinValue[holding.SecurityISIN] += v
		portfolioValue += v
	}

	var result []holdingWithPrice
	totalValue := 0.0
	var oldestPriceDate, newestPriceDate time.Time
	for _, holding := range holdings {
		// Apply account filter
		if filterAccountID != "" && holding.AccountID.String() != filterAccountID {
			continue
		}

		hwp := holdingWithPrice{
			ListCurrentHoldingsRow: holding,
			AccountName:            acctNames[holding.AccountID.String()],
		}
		qty := numericToFloat(holding.Quantity)
		pc := costs[positionKey{AccountID: holding.AccountID, ISIN: holding.SecurityISIN}]
		if pc != nil && pc.Qty > 0 {
			hwp.AvgCostEUR = pc.AvgCostEUR()
			hwp.CostBasisEUR = hwp.AvgCostEUR * qty
			if avg, cur, ok := pc.AvgCostNative(); ok {
				hwp.AvgCostPaid = &avg
				hwp.AvgCostCurrency = cur
			}
		} else {
			// No replayable purchases (should not happen): fall back to the
			// view's average cost at today's rate.
			hwp.CostBasisEUR = convertAmountToEUR(qty*numericToFloat(holding.AvgCostBasis), holding.Currency, fxRates)
			if qty > 0 {
				hwp.AvgCostEUR = hwp.CostBasisEUR / qty
			}
		}
		if pc != nil {
			hwp.DividendsEUR = pc.DividendsEUR
			hwp.DividendsNetEUR = pc.DividendsEUR - pc.DividendTaxEUR
		}
		if portfolioValue > 0 {
			w := isinValue[holding.SecurityISIN] / portfolioValue * 100
			hwp.ISINWeightPct = &w
		}

		// Prices are stored in the security's native currency; market_value
		// and unrealized_pl are EUR so the frontend formatter (locked to EUR)
		// and /api/portfolio/performance.current_value reconcile.
		if priceRow, ok := prices[holding.SecurityISIN]; ok && priceRow.Close.Valid {
			rawPrice := numericToFloat(priceRow.Close)
			normPriceCur, priceScale := normalizeCurrencyAndScale(priceRow.Currency)
			price := rawPrice * priceScale
			hwp.CurrentPrice = &price
			hwp.PriceCurrency = normPriceCur
			hwp.PriceDate = priceRow.Date.Format("2006-01-02")
			if oldestPriceDate.IsZero() || priceRow.Date.Before(oldestPriceDate) {
				oldestPriceDate = priceRow.Date
			}
			if priceRow.Date.After(newestPriceDate) {
				newestPriceDate = priceRow.Date
			}

			mvEUR := valueEUR(holding)
			hwp.MarketValue = &mvEUR
			hwp.ValueEUR = mvEUR
			totalValue += mvEUR

			pl := mvEUR - hwp.CostBasisEUR
			hwp.UnrealizedPL = &pl
		} else {
			normHoldCur, _ := normalizeCurrencyAndScale(holding.Currency)
			hwp.PriceCurrency = normHoldCur
			hwp.ValueEUR = valueEUR(holding)
			hwp.PriceMissing = true
			totalValue += hwp.ValueEUR
		}

		// Compute FX impact from country weights → currency mapping
		if sec, ok := secMap[holding.SecurityISIN]; ok {
			countryWeights := analytics.ParseWeights(sec.CountryWeights)
			if len(countryWeights) > 0 {
				// Find dominant non-EUR currency
				maxCur, maxPct := "", 0.0
				fxImpact := 0.0
				for country, pct := range countryWeights {
					cur := analytics.CountryToCurrency[country]
					if cur == "" || cur == "EUR" || cur == "OTHER" {
						continue
					}
					if pct > maxPct {
						maxCur = cur
						maxPct = pct
					}
					if change, ok := fxChanges[cur]; ok {
						fxImpact += (pct / 100) * change
					}
				}
				if maxCur != "" {
					hwp.FXExposure = maxCur
				}
				if hwp.UnrealizedPL != nil && hwp.MarketValue != nil {
					// total_return % = unrealized_pl_EUR / cost_basis_EUR × 100,
					// both EUR. Asset return = total return − FX-attributable %.
					totalReturn := 0.0
					if hwp.CostBasisEUR > 0 {
						totalReturn = (*hwp.UnrealizedPL / hwp.CostBasisEUR) * 100
					}
					assetReturn := totalReturn - fxImpact
					fxRound := math.Round(fxImpact*10) / 10
					assetRound := math.Round(assetReturn*10) / 10
					hwp.FXImpactPct = &fxRound
					hwp.AssetReturnPct = &assetRound
				}
			}
		}

		result = append(result, hwp)
	}

	// Compute weight percentages
	if totalValue > 0 {
		for i := range result {
			w := (result[i].ValueEUR / totalValue) * 100
			result[i].WeightPct = &w
		}
	}

	if result == nil {
		result = []holdingWithPrice{}
	}
	resp := map[string]any{"holdings": result}
	// "Prices as of" is the oldest price among priced holdings: the page is
	// only as fresh as its stalest quote. price_newest is the latest one.
	if !oldestPriceDate.IsZero() {
		resp["price_as_of"] = oldestPriceDate.Format("2006-01-02")
		resp["price_newest"] = newestPriceDate.Format("2006-01-02")
	}
	writeJSON(w, http.StatusOK, resp)
}

// maxNetWorthSnapshotRows bounds how many daily snapshots one request reads:
// 100 years, so "All" (days=9999) is never truncated.
const maxNetWorthSnapshotRows = 36600

func (h *PortfolioHandler) HandleNetWorth(w http.ResponseWriter, r *http.Request) {
	limit := int32(365)
	if l := r.URL.Query().Get("days"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 {
			if n > maxNetWorthSnapshotRows {
				n = maxNetWorthSnapshotRows
			}
			limit = int32(n)
		}
	}

	snapshots, err := h.queries.ListNetWorthSnapshots(r.Context(), limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list snapshots: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"snapshots": snapshots})
}

func (h *PortfolioHandler) HandleNetWorthIntraday(w http.ResponseWriter, r *http.Request) {
	since := time.Now().Add(-24 * time.Hour)

	rows, err := h.queries.ListNetWorthIntraday(r.Context(), since)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list intraday: "+err.Error())
		return
	}

	type point struct {
		RecordedAt          time.Time `json:"recorded_at"`
		Total               float64   `json:"total"`
		CashComponent       float64   `json:"cash_component"`
		InvestmentComponent float64   `json:"investment_component"`
		PhysicalComponent   float64   `json:"physical_component"`
		DebtComponent       float64   `json:"debt_component"`
	}
	points := make([]point, 0, len(rows))
	for _, r := range rows {
		points = append(points, point{
			RecordedAt:          r.RecordedAt,
			Total:               numericToFloat(r.Total),
			CashComponent:       numericToFloat(r.CashComponent),
			InvestmentComponent: numericToFloat(r.InvestmentComponent),
			PhysicalComponent:   numericToFloat(r.PhysicalComponent),
			DebtComponent:       numericToFloat(r.DebtComponent),
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{"points": points})
}

func (h *PortfolioHandler) HandleAccounts(w http.ResponseWriter, r *http.Request) {
	accounts, err := h.queries.ListAccounts(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list accounts: "+err.Error())
		return
	}
	// Filter by user if authenticated
	if allowedIDs := userAccountIDs(r.Context(), h.queries.DB()); allowedIDs != nil {
		allowed := make(map[uuid.UUID]bool, len(allowedIDs))
		for _, id := range allowedIDs { allowed[id] = true }
		filtered := accounts[:0]
		for _, a := range accounts {
			if allowed[a.ID] { filtered = append(filtered, a) }
		}
		accounts = filtered
	}

	// Compute balance for each account
	// For brokerage accounts: cash + holdings market value
	// For checking/savings: cash only
	type accountWithBalance struct {
		db.Account
		Balance       float64 `json:"balance"`
		CashBalance   float64 `json:"cash_balance"`
		HoldingsValue float64 `json:"holdings_value"`
	}
	// Batch-load prices, FX rates, cash balances, and holdings once (eliminates N+1)
	prices := h.loadPriceMap(r.Context())
	fxRates := loadFXRatesMap(r.Context(), h.queries)
	allHoldings, _ := h.queries.ListCurrentHoldings(r.Context())
	cashByAcct, _ := computeAllAccountCashEUR(r.Context(), h.queries, accounts, fxRates)

	var result []accountWithBalance
	for _, acc := range accounts {
		cashEUR, ok := cashByAcct[acc.ID]
		if !ok {
			if c, err := computeSingleAccountCashEUR(r.Context(), h.queries, acc, fxRates); err == nil {
				cashEUR = c
			}
		}

		holdingsEUR := 0.0
		for _, holding := range allHoldings {
			if holding.AccountID == acc.ID {
				holdingsEUR += holdingValueEURFromMap(holding, prices, fxRates)
			}
		}

		result = append(result, accountWithBalance{
			Account:       acc,
			Balance:       cashEUR + holdingsEUR,
			CashBalance:   cashEUR,
			HoldingsValue: holdingsEUR,
		})
	}

	if result == nil {
		result = []accountWithBalance{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"accounts": result})
}

// performanceScope resolves the accounts the Portfolio page's performance
// figures cover: one account when account_id is given, otherwise every active
// investment account (brokerage, crypto, pension, precious metals — the
// accounts the page's selector lists). Checking and savings cash is not
// invested capital, so salary and spending never count as capital flows.
func (h *PortfolioHandler) performanceScope(ctx context.Context, accountID string) ([]db.Account, error) {
	accounts, err := h.queries.ListAccounts(ctx)
	if err != nil {
		return nil, err
	}
	if allowedIDs := userAccountIDs(ctx, h.queries.DB()); allowedIDs != nil {
		allowed := make(map[uuid.UUID]bool, len(allowedIDs))
		for _, id := range allowedIDs {
			allowed[id] = true
		}
		filtered := accounts[:0]
		for _, a := range accounts {
			if allowed[a.ID] {
				filtered = append(filtered, a)
			}
		}
		accounts = filtered
	}
	if accountID != "" {
		id, err := uuid.Parse(accountID)
		if err != nil {
			return nil, errInvalidAccount
		}
		for _, a := range accounts {
			if a.ID == id {
				return []db.Account{a}, nil
			}
		}
		return nil, errInvalidAccount
	}
	var scope []db.Account
	for _, a := range accounts {
		if isInvestmentAccount(a) {
			scope = append(scope, a)
		}
	}
	return scope, nil
}

var errInvalidAccount = fmt.Errorf("invalid account_id")

// performanceSeries replays the scope's transactions into a daily EUR
// valuation series up to today.
func (h *PortfolioHandler) performanceSeries(ctx context.Context, scope []db.Account, fx *fxTable) ([]db.ListTransactionsRow, []valuationPoint, error) {
	all, err := listAllTransactionsRows(ctx, h.queries)
	if err != nil {
		return nil, nil, err
	}
	inScope := make(map[uuid.UUID]bool, len(scope))
	for _, a := range scope {
		inScope[a.ID] = true
	}
	var txns []db.ListTransactionsRow
	isinSet := make(map[string]bool)
	for _, t := range chronological(all) {
		if !inScope[t.AccountID] {
			continue
		}
		txns = append(txns, t)
		if t.SecurityISIN.Valid {
			isinSet[t.SecurityISIN.String] = true
		}
	}
	if len(txns) == 0 {
		return nil, nil, nil
	}
	isins := make([]string, 0, len(isinSet))
	for isin := range isinSet {
		isins = append(isins, isin)
	}
	series := buildValuationSeries(valuationRequest{
		Txns:     txns,
		Accounts: scope,
		Prices:   loadPriceBook(ctx, h.queries, isins),
		FX:       fx,
		End:      assets.CivilDate(time.Now()),
	})
	return txns, series, nil
}

// seriesTWRSteps prepends a zero-value step so the first day's inflows are
// part of the chain.
func seriesTWRSteps(series []valuationPoint) []analytics.TWRStep {
	if len(series) == 0 {
		return nil
	}
	steps := make([]analytics.TWRStep, 0, len(series)+1)
	steps = append(steps, analytics.TWRStep{Date: series[0].Date.AddDate(0, 0, -1)})
	return append(steps, twrSteps(series)...)
}

// trailingTWR chains the TWR over the window that ends at the last step and
// starts at the last step on or before `from` (or the first step when the
// history is shorter).
func trailingTWR(steps []analytics.TWRStep, from time.Time) analytics.TWRChain {
	i0 := 0
	for i, s := range steps {
		if s.Date.After(from) {
			break
		}
		i0 = i
	}
	return analytics.ChainTWR(steps[i0:])
}

func datePtr(t time.Time) *string {
	if t.IsZero() {
		return nil
	}
	s := t.Format("2006-01-02")
	return &s
}

func dateList(ts []time.Time) []string {
	out := make([]string, 0, len(ts))
	for _, t := range ts {
		out = append(out, t.Format("2006-01-02"))
	}
	return out
}

func emptyPerformanceResponse() map[string]any {
	return map[string]any{
		"irr":              nil,
		"twr":              nil,
		"total_invested":   0,
		"total_withdrawn":  0,
		"transferred_in":   0,
		"transferred_out":  0,
		"current_value":    0,
		"total_return":     0,
		"total_return_pct": nil,
		"realized_pl":      0,
		"unrealized_pl":    0,
		"twr_resets":       []string{},
	}
}

func (h *PortfolioHandler) HandlePerformance(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	scope, err := h.performanceScope(ctx, r.URL.Query().Get("account_id"))
	if err == errInvalidAccount {
		writeError(w, http.StatusBadRequest, "invalid account_id")
		return
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, "list accounts: "+err.Error())
		return
	}

	fxRates := loadFXRatesMap(ctx, h.queries)
	fx := newFXTable(loadHistoricalFXRates(ctx, h.queries), fxRates)
	txns, series, err := h.performanceSeries(ctx, scope, fx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list transactions: "+err.Error())
		return
	}
	if len(series) == 0 {
		writeJSON(w, http.StatusOK, emptyPerformanceResponse())
		return
	}

	// External flows of the scope (deposits, withdrawals, transfers in and
	// out of it, RSU vests at their vest-date value, implicit funding of
	// securities-only accounts), EUR at each flow date's ECB rate.
	var cashDeposited, cashWithdrawn, transferredIn, transferredOut float64
	var flows []analytics.CashFlow // + in, − out
	for _, p := range series {
		cashDeposited += p.CashIn
		cashWithdrawn += p.CashOut
		transferredIn += p.InKindIn
		transferredOut += p.InKindOut
		if f := p.Flow(); math.Abs(f) > 1e-9 {
			flows = append(flows, analytics.CashFlow{Date: p.Date, Amount: f})
		}
	}

	// Current value: the scope's cash (clamped, latest ECB rate) plus its
	// holdings at the latest price, matching the Holdings tab.
	inScope := make(map[uuid.UUID]bool, len(scope))
	for _, a := range scope {
		inScope[a.ID] = true
	}
	currentValue := 0.0
	if cashByAcct, err := computeAllAccountCashEUR(ctx, h.queries, scope, fxRates); err == nil {
		for id, c := range cashByAcct {
			if inScope[id] {
				currentValue += c
			}
		}
	}
	prices := h.loadPriceMap(ctx)
	costs := replayPositionCosts(txns, fx)
	if holdings, err := h.queries.ListCurrentHoldings(ctx); err == nil {
		for _, hold := range holdings {
			if !inScope[hold.AccountID] {
				continue
			}
			qty := numericToFloat(hold.Quantity)
			if p, ok := prices[hold.SecurityISIN]; ok && p.Close.Valid {
				currentValue += convertAmountToEUR(qty*numericToFloat(p.Close), p.Currency, fxRates)
			} else if c := costs[positionKey{AccountID: hold.AccountID, ISIN: hold.SecurityISIN}]; c != nil {
				currentValue += c.AvgCostEUR() * qty
			}
		}
	}

	today := assets.CivilDate(time.Now())

	// Money-weighted return (IRR) on the scope's external flows.
	irrFlows := make([]analytics.CashFlow, 0, len(flows)+1)
	for _, f := range flows {
		irrFlows = append(irrFlows, analytics.CashFlow{Date: f.Date, Amount: -f.Amount})
	}
	irrFlows = append(irrFlows, analytics.CashFlow{Date: today, Amount: currentValue})
	sortCashFlows(irrFlows)
	var irrPct, irrPeriodPct *float64
	irrStart := irrFlows[0].Date
	irrYears := today.Sub(irrStart).Hours() / (24 * 365.25)
	if irr, ok := analytics.ComputeIRR(irrFlows, 0.05); ok {
		v := math.Round(irr*1e6) / 1e4
		irrPct = &v
		pr := math.Round((math.Pow(1+irr, irrYears)-1)*1e6) / 1e4
		irrPeriodPct = &pr
	}

	// Realized P&L from sells, average-cost method, EUR at each date's rate.
	var plTxns []analytics.TransactionForPL
	for _, txn := range txns {
		if !txn.SecurityISIN.Valid {
			continue
		}
		amt := fx.toEUR(numericToFloat(txn.Amount), txn.Currency, txn.Date)
		fee := fx.toEUR(numericToFloat(txn.Fee), txn.Currency, txn.Date)
		adjAmt := amt
		switch txn.Type {
		case "buy", "savings_plan", "transfer":
			adjAmt = amt + fee
		case "sell":
			adjAmt = amt - fee
		}
		plTxns = append(plTxns, analytics.TransactionForPL{
			Type: txn.Type,
			ISIN: txn.SecurityISIN.String,
			Qty:  numericToFloat(txn.Quantity),
			Amt:  adjAmt,
		})
	}
	realizedResult := analytics.CalculateRealizedPL(plTxns)

	// Total gain = value − net capital. As a rate it is divided by the
	// average capital invested (Modified Dietz), not by net capital, which
	// shrinks towards zero after large withdrawals and makes the ratio explode.
	netCapitalInvested := cashDeposited + transferredIn - cashWithdrawn - transferredOut
	gains := currentValue - netCapitalInvested
	var gainsPct *float64
	if md, ok := analytics.ModifiedDietz(0, currentValue, flows, series[0].Date.AddDate(0, 0, -1), today); ok {
		v := math.Round(md*1e6) / 1e4
		gainsPct = &v
	}
	unrealizedPL := gains - realizedResult.TotalRealizedPL

	// Trailing 12-month time-weighted return over the scope's own daily
	// valuation series (not the household net-worth snapshots), so the
	// account filter and the investment-only scope apply to it too.
	oneYearAgo := today.AddDate(-1, 0, 0)
	chain := trailingTWR(seriesTWRSteps(series), oneYearAgo)
	var twrPct *float64
	if chain.OK {
		v := math.Round(chain.Return*1e6) / 1e4
		twrPct = &v
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"irr":               irrPct,
		"irr_start":         datePtr(irrStart),
		"irr_years":         math.Round(irrYears*100) / 100,
		"irr_period_return": irrPeriodPct,
		"twr":               twrPct,
		"twr_start":         datePtr(chain.Start),
		"twr_end":           datePtr(chain.End),
		"twr_resets":        dateList(chain.Resets),
		"total_invested":    cashDeposited,
		"total_withdrawn":   cashWithdrawn,
		"transferred_in":    transferredIn,
		"transferred_out":   transferredOut,
		"current_value":     currentValue,
		"total_return":      gains,
		// total_return_pct: Modified Dietz return since inception, i.e. the
		// total gain ÷ average capital invested (null without capital).
		"total_return_pct": gainsPct,
		"realized_pl":      realizedResult.TotalRealizedPL,
		"unrealized_pl":    unrealizedPL,
		"account_count":    len(scope),
	})
}

// loadAllTransactions loads ALL transactions without the 10000-row limit that
// ListTransactions uses. Optionally filters by account_id for per-account performance.
func (h *PortfolioHandler) loadAllTransactions(ctx context.Context, accountID uuid.UUID) ([]db.ListTransactionsRow, error) {
	var query string
	var args []any
	if accountID != uuid.Nil {
		query = `SELECT t.id, t.account_id, t.date, t.type, t.security_isin, t.quantity, t.price, t.amount, t.fee, t.tax, t.currency, t.counterparty, t.reference, t.category, t.import_hash, a.name as account_name, a.institution
			FROM transactions t JOIN accounts a ON t.account_id = a.id
			WHERE t.account_id = $1
			ORDER BY t.date ASC, CASE WHEN t.type IN ('buy', 'savings_plan', 'transfer', 'deposit', 'cash_transfer_in') THEN 0 ELSE 1 END ASC, t.id ASC`
		args = []any{accountID}
	} else {
		query = `SELECT t.id, t.account_id, t.date, t.type, t.security_isin, t.quantity, t.price, t.amount, t.fee, t.tax, t.currency, t.counterparty, t.reference, t.category, t.import_hash, a.name as account_name, a.institution
			FROM transactions t JOIN accounts a ON t.account_id = a.id
			WHERE a.is_active = true
			ORDER BY t.date ASC, CASE WHEN t.type IN ('buy', 'savings_plan', 'transfer', 'deposit', 'cash_transfer_in') THEN 0 ELSE 1 END ASC, t.id ASC`
	}
	rows, err := h.queries.DB().Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []db.ListTransactionsRow
	for rows.Next() {
		var t db.ListTransactionsRow
		if err := rows.Scan(
			&t.ID, &t.AccountID, &t.Date, &t.Type, &t.SecurityISIN,
			&t.Quantity, &t.Price, &t.Amount, &t.Fee, &t.Tax,
			&t.Currency, &t.Counterparty, &t.Reference, &t.Category,
			&t.ImportHash, &t.AccountName, &t.Institution,
		); err != nil {
			return nil, err
		}
		items = append(items, t)
	}
	return items, rows.Err()
}

// HandlePerformanceHistory returns the scope's daily value, capital invested
// and chained time-weighted return since inception, plus the MSCI World
// benchmark's price return over the same dates. Both return series are
// cumulative from the first point; the page re-bases them to the start of the
// selected period. Accepts ?account_id= like /portfolio/performance.
func (h *PortfolioHandler) HandlePerformanceHistory(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	scope, err := h.performanceScope(ctx, r.URL.Query().Get("account_id"))
	if err == errInvalidAccount {
		writeError(w, http.StatusBadRequest, "invalid account_id")
		return
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, "list accounts: "+err.Error())
		return
	}
	fx := loadFXTable(ctx, h.queries)
	_, series, err := h.performanceSeries(ctx, scope, fx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list transactions: "+err.Error())
		return
	}

	type historyPoint struct {
		Date           string  `json:"date"`
		PortfolioValue float64 `json:"portfolio_value"`
		CashInvested   float64 `json:"cash_invested"`
		InKindInvested float64 `json:"in_kind_invested"`
		// ReturnPct is the chained time-weighted return since the first day
		// with capital, in % (null before that).
		ReturnPct    *float64 `json:"return_pct"`
		BenchmarkPct *float64 `json:"benchmark_pct,omitempty"`
	}
	history := make([]historyPoint, 0, len(series))
	if len(series) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{"history": history, "twr_resets": []string{}})
		return
	}

	steps := seriesTWRSteps(series)
	chain := analytics.ChainTWR(steps)

	// Benchmark (default MSCI World via IWDA.AS): latest close on or before
	// each date, ignored when older than 10 days.
	bench := loadBenchmarkPrices(ctx, h.queries, h.benchmarkISIN, h.benchmarkTicker)
	sort.Slice(bench, func(i, j int) bool { return bench[i].date.Before(bench[j].date) })
	benchAt := func(d time.Time) float64 {
		i := sort.Search(len(bench), func(i int) bool { return civilDay(bench[i].date).After(d) })
		if i == 0 || d.Sub(civilDay(bench[i-1].date)) > 10*24*time.Hour {
			return 0
		}
		return bench[i-1].price
	}
	benchBase := 0.0

	cashInvested, inKindInvested := 0.0, 0.0
	started := false
	for i, p := range series {
		cashInvested += p.CashIn - p.CashOut
		inKindInvested += p.InKindIn - p.InKindOut
		if !started {
			if p.Value <= 0 && math.Abs(cashInvested)+math.Abs(inKindInvested) < 1e-9 {
				continue // nothing held or invested yet
			}
			started = true
		}
		pt := historyPoint{
			Date:           p.Date.Format("2006-01-02"),
			PortfolioValue: math.Round(p.Value*100) / 100,
			CashInvested:   math.Round(cashInvested*100) / 100,
			InKindInvested: math.Round(inKindInvested*100) / 100,
		}
		if idx := chain.Index[i+1]; !math.IsNaN(idx) {
			v := math.Round((idx-1)*1e6) / 1e4
			pt.ReturnPct = &v
		}
		if bp := benchAt(p.Date); bp > 0 {
			if benchBase == 0 {
				benchBase = bp
			}
			v := math.Round((bp/benchBase-1)*1e6) / 1e4
			pt.BenchmarkPct = &v
		}
		history = append(history, pt)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"history":    history,
		"twr_resets": dateList(chain.Resets),
	})
}

// dividendPayment is one dividend transaction in EUR at its payment date's
// ECB rate. QtyHeld is the ISIN's quantity across all accounts when it was
// paid, used to scale past payouts to today's position.
type dividendPayment struct {
	Date    time.Time
	ISIN    string
	Name    string
	Gross   float64
	Net     float64 // after withheld tax and fees
	QtyHeld float64
}

type dividendMonth struct {
	Month  string  `json:"month"`
	Amount float64 `json:"amount"`
}

type dividendYear struct {
	Year   string  `json:"year"`
	Amount float64 `json:"amount"`
}

type dividendCumulative struct {
	Month      string  `json:"month"`
	Amount     float64 `json:"amount"`
	Cumulative float64 `json:"cumulative"`
}

type dividendCalendarEntry struct {
	Month    string  `json:"month"`
	ISIN     string  `json:"isin"`
	Name     string  `json:"name"`
	Expected float64 `json:"expected"`
}

type dividendSummary struct {
	Total          float64
	TotalNet       float64
	Monthly        []dividendMonth
	Yearly         []dividendYear
	Cumulative     []dividendCumulative
	Trailing12m    float64
	Trailing12mNet float64
	Prior12m       float64
	// Trailing12mHeld counts only ISINs still held, the numerator of the
	// yield on cost (whose denominator is the cost of current holdings).
	Trailing12mHeld float64
	Growth          *float64 // nil when the prior 12 months paid nothing
	Calendar        []dividendCalendarEntry
}

// summarizeDividends builds the Dividends tab figures. heldQty is today's
// quantity per ISIN across accounts; names labels the calendar.
//
//   - Monthly and yearly series run from the first payout to `now` with zero
//     months and years filled in, so the chart's time axis is continuous.
//   - The cumulative line is the running net sum: a reversal lowers it.
//   - The calendar projects each held ISIN's payouts in the same calendar
//     month of the last three years, per share (month total ÷ quantity held
//     then), times today's quantity.
func summarizeDividends(payments []dividendPayment, heldQty map[string]float64, names map[string]string, now time.Time) dividendSummary {
	var s dividendSummary
	if len(payments) == 0 {
		s.Monthly, s.Yearly, s.Cumulative, s.Calendar = []dividendMonth{}, []dividendYear{}, []dividendCumulative{}, []dividendCalendarEntry{}
		return s
	}
	sort.SliceStable(payments, func(i, j int) bool { return payments[i].Date.Before(payments[j].Date) })

	monthly := make(map[string]float64)
	cutoff12m := now.AddDate(-1, 0, 0)
	cutoff24m := now.AddDate(-2, 0, 0)
	for _, p := range payments {
		s.Total += p.Gross
		s.TotalNet += p.Net
		monthly[p.Date.Format("2006-01")] += p.Gross
		if p.Date.After(cutoff12m) {
			s.Trailing12m += p.Gross
			s.Trailing12mNet += p.Net
			if heldQty[p.ISIN] > 1e-9 {
				s.Trailing12mHeld += p.Gross
			}
		} else if p.Date.After(cutoff24m) {
			s.Prior12m += p.Gross
		}
	}
	if s.Prior12m > 0 {
		g := (s.Trailing12m - s.Prior12m) / s.Prior12m * 100
		s.Growth = &g
	}

	first := time.Date(payments[0].Date.Year(), payments[0].Date.Month(), 1, 0, 0, 0, 0, time.UTC)
	last := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	if lp := payments[len(payments)-1].Date; lp.After(last) {
		last = time.Date(lp.Year(), lp.Month(), 1, 0, 0, 0, 0, time.UTC)
	}
	yearly := make(map[int]float64)
	cum := 0.0
	for m := first; !m.After(last); m = m.AddDate(0, 1, 0) {
		key := m.Format("2006-01")
		amt := math.Round(monthly[key]*100) / 100
		s.Monthly = append(s.Monthly, dividendMonth{Month: key, Amount: amt})
		yearly[m.Year()] += monthly[key]
		cum += monthly[key]
		s.Cumulative = append(s.Cumulative, dividendCumulative{Month: key, Amount: amt, Cumulative: math.Round(cum*100) / 100})
	}
	for y := first.Year(); y <= last.Year(); y++ {
		s.Yearly = append(s.Yearly, dividendYear{Year: strconv.Itoa(y), Amount: math.Round(yearly[y]*100) / 100})
	}

	// Calendar: per-share payout per (ISIN, year, calendar month).
	type ym struct {
		isin  string
		year  int
		month time.Month
	}
	perShare := make(map[ym]float64)
	for _, p := range payments {
		if p.QtyHeld <= 1e-9 || heldQty[p.ISIN] <= 1e-9 || p.Date.Before(now.AddDate(-3, 0, 0)) {
			continue
		}
		perShare[ym{p.ISIN, p.Date.Year(), p.Date.Month()}] += p.Gross / p.QtyHeld
	}
	isins := make([]string, 0, len(heldQty))
	for isin, q := range heldQty {
		if q > 1e-9 {
			isins = append(isins, isin)
		}
	}
	sort.Strings(isins)
	for m := 0; m < 12; m++ {
		future := addMonthsFirstOfMonth(now, m+1)
		for _, isin := range isins {
			sum, n := 0.0, 0
			for k, v := range perShare {
				if k.isin == isin && k.month == future.Month() {
					sum += v
					n++
				}
			}
			if n == 0 {
				continue
			}
			name := names[isin]
			if name == "" {
				name = isin
			}
			s.Calendar = append(s.Calendar, dividendCalendarEntry{
				Month:    future.Format("2006-01"),
				ISIN:     isin,
				Name:     name,
				Expected: math.Round(sum/float64(n)*heldQty[isin]*100) / 100,
			})
		}
	}
	if s.Calendar == nil {
		s.Calendar = []dividendCalendarEntry{}
	}
	return s
}

func (h *PortfolioHandler) HandleDividends(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	txns, err := listAllTransactionsRows(ctx, h.queries)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list transactions: "+err.Error())
		return
	}
	fx := loadFXTable(ctx, h.queries)
	secs, _ := h.queries.ListSecurities(ctx)
	secNames := make(map[string]string, len(secs))
	for _, s := range secs {
		secNames[s.ISIN] = s.Name
	}

	type dividendBySecurity struct {
		ISIN   string  `json:"isin"`
		Name   string  `json:"name"`
		Amount float64 `json:"amount"`
	}

	// Replay quantities so each payout knows how many units it was paid on.
	qty := make(map[string]float64)
	securityMap := make(map[string]float64)
	securityNames := make(map[string]string)
	var payments []dividendPayment
	for _, txn := range chronological(txns) {
		isin := ""
		if txn.SecurityISIN.Valid {
			isin = txn.SecurityISIN.String
		}
		q := math.Abs(numericToFloat(txn.Quantity))
		switch {
		case isin != "" && isAcquisitionType(txn.Type):
			qty[isin] += q
		case isin != "" && isDisposalType(txn.Type):
			qty[isin] = math.Max(0, qty[isin]-q)
		case txn.Type == "dividend":
			gross := fx.toEUR(numericToFloat(txn.Amount), txn.Currency, txn.Date)
			withheld := fx.toEUR(math.Abs(numericToFloat(txn.Tax))+math.Abs(numericToFloat(txn.Fee)), txn.Currency, txn.Date)
			name := secNames[isin]
			if name == "" && txn.Counterparty.Valid {
				name = txn.Counterparty.String
			}
			payments = append(payments, dividendPayment{
				Date: txn.Date, ISIN: isin, Name: name, Gross: gross, Net: gross - withheld, QtyHeld: qty[isin],
			})
			if isin != "" {
				securityMap[isin] += gross
				if name != "" {
					securityNames[isin] = name
				}
			}
		}
	}

	var bySecurity []dividendBySecurity
	for isin, amt := range securityMap {
		name := securityNames[isin]
		if name == "" {
			name = isin
		}
		bySecurity = append(bySecurity, dividendBySecurity{ISIN: isin, Name: name, Amount: amt})
	}
	sort.Slice(bySecurity, func(i, j int) bool { return bySecurity[i].Amount > bySecurity[j].Amount })
	if bySecurity == nil {
		bySecurity = []dividendBySecurity{}
	}

	// Current holdings (active accounts) and their EUR cost at purchase-date
	// rates: the yield on cost divides the trailing payouts of these ISINs by
	// this cost, so sold positions are in neither numerator nor denominator.
	heldQty := make(map[string]float64)
	totalCostBasis := 0.0
	costs := replayPositionCosts(chronological(txns), fx)
	if holdings, err := h.queries.ListCurrentHoldings(ctx); err == nil {
		for _, hold := range holdings {
			q := numericToFloat(hold.Quantity)
			heldQty[hold.SecurityISIN] += q
			if c := costs[positionKey{AccountID: hold.AccountID, ISIN: hold.SecurityISIN}]; c != nil {
				totalCostBasis += c.AvgCostEUR() * q
			}
		}
	}

	sum := summarizeDividends(payments, heldQty, secNames, time.Now())
	var yieldOnCostPct *float64
	if totalCostBasis > 0 {
		v := math.Round(sum.Trailing12mHeld/totalCostBasis*10000) / 100
		yieldOnCostPct = &v
	}
	var growth *float64
	if sum.Growth != nil {
		v := math.Round(*sum.Growth*100) / 100
		growth = &v
	}

	writeJSON(w, http.StatusOK, map[string]any{
		// Amounts are gross (before withheld tax and fees), EUR at each
		// payment date's ECB rate; *_net subtract the withheld amounts.
		"total":            sum.Total,
		"total_net":        math.Round(sum.TotalNet*100) / 100,
		"monthly":          sum.Monthly,
		"yearly":           sum.Yearly,
		"by_security":      bySecurity,
		"cumulative":       sum.Cumulative,
		"trailing_12m":     math.Round(sum.Trailing12m*100) / 100,
		"trailing_12m_net": math.Round(sum.Trailing12mNet*100) / 100,
		"yield_on_cost":    yieldOnCostPct,
		"dividend_growth":  growth,
		"calendar":         sum.Calendar,
	})
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}


// holdingValueEURFromMap computes a holding's EUR market value using the
// batch-loaded price map and FX rates (converting from priceRow.Currency or
// holding.Currency, never the account's currency).
func holdingValueEURFromMap(h db.ListCurrentHoldingsRow, prices priceMap, fxRates map[string]float64) float64 {
	qty := 0.0
	if h.Quantity.Valid {
		f, _ := h.Quantity.Float64Value()
		qty = f.Float64
	}
	if qty <= 0 {
		return 0
	}
	if p, ok := prices[h.SecurityISIN]; ok && p.Close.Valid {
		f, _ := p.Close.Float64Value()
		return convertAmountToEUR(qty*f.Float64, p.Currency, fxRates)
	}
	if h.AvgCostBasis.Valid {
		f, _ := h.AvgCostBasis.Float64Value()
		return convertAmountToEUR(qty*f.Float64, h.Currency, fxRates)
	}
	return 0
}

// holdingValueFromMap computes holding market value using batch-loaded price map.
func holdingValueFromMap(h db.ListCurrentHoldingsRow, prices priceMap) float64 {
	qty := 0.0
	if h.Quantity.Valid {
		f, _ := h.Quantity.Float64Value()
		qty = f.Float64
	}
	if qty <= 0 {
		return 0
	}
	if p, ok := prices[h.SecurityISIN]; ok && p.Close.Valid {
		f, _ := p.Close.Float64Value()
		return qty * f.Float64
	}
	if h.AvgCostBasis.Valid {
		f, _ := h.AvgCostBasis.Float64Value()
		return qty * f.Float64
	}
	return 0
}

func sortCashFlows(cfs []analytics.CashFlow) {
	for i := 1; i < len(cfs); i++ {
		for j := i; j > 0 && cfs[j].Date.Before(cfs[j-1].Date); j-- {
			cfs[j], cfs[j-1] = cfs[j-1], cfs[j]
		}
	}
}

// benchmarkPrice holds a date-price pair for benchmark return calculation.
type benchmarkPrice struct {
	date  time.Time
	price float64
}

// loadBenchmarkPrices loads benchmark prices in EUR (each converted at its
// date's ECB rate) from DB, falling back to weekly Yahoo Finance closes.
func loadBenchmarkPrices(ctx context.Context, q *db.Queries, isin, ticker string) []benchmarkPrice {
	if isin == "" {
		isin = benchmarkISIN
	}
	if ticker == "" {
		ticker = benchmarkTicker
	}
	fxRates := loadFXRatesMap(ctx, q)
	histFX := loadHistoricalFXRates(ctx, q)
	rows, err := q.ListPriceHistory(ctx, isin)
	if err == nil && len(rows) > 0 {
		var prices []benchmarkPrice
		for _, r := range rows {
			if r.Close.Valid {
				f, _ := r.Close.Float64Value()
				if p := convertAmountToEURAtDate(f.Float64, r.Currency, r.Date, histFX, fxRates); p > 0 {
					prices = append(prices, benchmarkPrice{date: r.Date, price: p})
				}
			}
		}
		if len(prices) > 5 {
			return prices
		}
	}

	// Fetch from Yahoo Finance and store
	yahoo := market.NewYahooClient()
	hist, err := yahoo.FetchHistoricalPrices(ctx, ticker)
	if err != nil {
		log.Printf("benchmark fetch failed: %v", err)
		return nil
	}

	var prices []benchmarkPrice
	for _, h := range hist {
		if p := convertAmountToEURAtDate(h.Close, h.Currency, h.Date, histFX, fxRates); p > 0 {
			prices = append(prices, benchmarkPrice{date: h.Date, price: p})
		}
	}
	return prices
}

// benchmarkReturnAt computes the benchmark return % from startDate to targetDate.
func benchmarkReturnAt(prices []benchmarkPrice, startDate, targetDate time.Time) *float64 {
	if len(prices) < 2 {
		return nil
	}

	// Find price closest to startDate
	startPrice := findClosestPrice(prices, startDate)
	if startPrice <= 0 {
		return nil
	}

	// Find price closest to targetDate
	targetPrice := findClosestPrice(prices, targetDate)
	if targetPrice <= 0 {
		return nil
	}

	ret := ((targetPrice - startPrice) / startPrice) * 100
	return &ret
}

// findClosestPrice finds the price closest to the given date.
func findClosestPrice(prices []benchmarkPrice, target time.Time) float64 {
	best := prices[0]
	bestDiff := absDuration(prices[0].date.Sub(target))

	for _, p := range prices[1:] {
		diff := absDuration(p.date.Sub(target))
		if diff < bestDiff {
			best = p
			bestDiff = diff
		}
	}

	// Only use if within 45 days
	if bestDiff > 45*24*time.Hour {
		return 0
	}
	return best.price
}

func absDuration(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}

// HandleTargetAllocation returns current holdings with target allocation and drift.
//
// Drift uses the shared definition in allocationMaxDrift: once any target is
// set, a held ISIN without a target has target 0 (the rebalancer sells it),
// and targets that do not add up to 100 % are scaled to 100 %.
func (h *PortfolioHandler) HandleTargetAllocation(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	holdings, err := h.queries.ListCurrentHoldings(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list holdings: "+err.Error())
		return
	}

	targets, err := h.queries.ListTargetAllocations(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list targets: "+err.Error())
		return
	}

	pm := h.loadPriceMap(ctx)
	fxRates := loadFXRatesMap(ctx, h.queries)

	// Aggregate holdings by ISIN (ListCurrentHoldings returns per-account rows)
	totalValue := 0.0
	type holdingInfo struct {
		ISIN        string
		Name        string
		MarketValue float64
	}
	aggregated := make(map[string]*holdingInfo)
	var order []string
	for _, hold := range holdings {
		if numericToFloat(hold.Quantity) <= 0 {
			continue
		}
		value := holdingValueEURFromMap(hold, pm, fxRates)
		if existing, ok := aggregated[hold.SecurityISIN]; ok {
			existing.MarketValue += value
		} else {
			name := hold.Name
			if name == "" {
				name = hold.SecurityISIN
			}
			aggregated[hold.SecurityISIN] = &holdingInfo{ISIN: hold.SecurityISIN, Name: name, MarketValue: value}
			order = append(order, hold.SecurityISIN)
		}
		totalValue += value
	}
	valByISIN := make(map[string]float64, len(aggregated))
	for isin, hi := range aggregated {
		valByISIN[isin] = hi.MarketValue
	}

	// Build target map
	targetMap := make(map[string]float64)
	for _, t := range targets {
		targetMap[t.SecurityISIN] = numericToFloat(t.TargetWeightPct)
	}
	normTargets, targetSum, normalised := normalisedTargets(targetMap)
	hasTargets := len(normTargets) > 0

	type allocationEntry struct {
		ISIN      string  `json:"isin"`
		Name      string  `json:"name"`
		ActualPct float64 `json:"actual_pct"`
		TargetPct float64 `json:"target_pct"`
		// EffectiveTargetPct is the target drift is measured against: the
		// target scaled to a 100 % total, 0 for a holding without a target.
		EffectiveTargetPct float64 `json:"effective_target_pct"`
		DriftPct           float64 `json:"drift_pct"`
		Value              float64 `json:"value"`
		Status             string  `json:"status"` // "on_target", "underweight", "overweight"
		Untargeted         bool    `json:"untargeted"`
	}

	statusFor := func(drift float64) string {
		if !hasTargets {
			return "on_target"
		}
		if drift > 2 {
			return "overweight"
		} else if drift < -2 {
			return "underweight"
		}
		return "on_target"
	}

	var entries []allocationEntry
	for _, isin := range order {
		hi := aggregated[isin]
		actualPct := 0.0
		if totalValue > 0 {
			actualPct = (hi.MarketValue / totalValue) * 100
		}
		drift := 0.0
		if hasTargets {
			drift = actualPct - normTargets[isin]
		}
		entries = append(entries, allocationEntry{
			ISIN:               hi.ISIN,
			Name:               hi.Name,
			ActualPct:          math.Round(actualPct*10) / 10,
			TargetPct:          targetMap[isin],
			EffectiveTargetPct: math.Round(normTargets[isin]*10) / 10,
			DriftPct:           math.Round(drift*10) / 10,
			Value:              math.Round(hi.MarketValue*100) / 100,
			Status:             statusFor(drift),
			Untargeted:         hasTargets && normTargets[isin] == 0,
		})
	}

	// Target ISINs not in holdings (target but no position)
	for _, t := range targets {
		if _, held := aggregated[t.SecurityISIN]; held {
			continue
		}
		eff := normTargets[t.SecurityISIN]
		entries = append(entries, allocationEntry{
			ISIN:               t.SecurityISIN,
			Name:               t.SecurityName,
			ActualPct:          0,
			TargetPct:          numericToFloat(t.TargetWeightPct),
			EffectiveTargetPct: math.Round(eff*10) / 10,
			DriftPct:           -math.Round(eff*10) / 10,
			Status:             statusFor(-eff),
		})
	}

	if entries == nil {
		entries = []allocationEntry{}
	}
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].Value > entries[j].Value })

	maxDrift := 0.0
	if hasTargets {
		maxDrift = allocationMaxDrift(valByISIN, targetMap)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"allocations":        entries,
		"has_targets":        hasTargets,
		"max_drift":          math.Round(maxDrift*10) / 10,
		"total_value":        math.Round(totalValue*100) / 100,
		"target_sum":         math.Round(targetSum*100) / 100,
		"targets_normalised": normalised,
	})
}

// HandleSetTargetAllocation sets or updates target allocations.
func (h *PortfolioHandler) HandleSetTargetAllocation(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Allocations []struct {
			ISIN      string  `json:"isin"`
			TargetPct float64 `json:"target_pct"`
		} `json:"allocations"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	// Validate total doesn't exceed 100%
	total := 0.0
	for _, a := range req.Allocations {
		if strings.TrimSpace(a.ISIN) == "" {
			writeError(w, http.StatusBadRequest, "isin is required")
			return
		}
		if math.IsNaN(a.TargetPct) || math.IsInf(a.TargetPct, 0) || a.TargetPct < 0 || a.TargetPct > 100 {
			writeError(w, http.StatusBadRequest, "target_pct must be between 0 and 100")
			return
		}
		total += a.TargetPct
	}
	if math.IsNaN(total) || math.IsInf(total, 0) || total > 100.5 { // small tolerance for rounding
		writeError(w, http.StatusBadRequest, "total target allocation exceeds 100%")
		return
	}

	ctx := r.Context()
	for _, a := range req.Allocations {
		if a.TargetPct == 0 {
			if err := h.queries.DeleteTargetAllocation(ctx, a.ISIN); err != nil {
				writeError(w, http.StatusInternalServerError, "delete allocation: "+err.Error())
				return
			}
		} else {
			var pct pgtype.Numeric
			pct.Scan(fmt.Sprintf("%.2f", a.TargetPct))
			if err := h.queries.UpsertTargetAllocation(ctx, db.UpsertTargetAllocationParams{SecurityISIN: a.ISIN, TargetWeightPct: pct}); err != nil {
				writeError(w, http.StatusInternalServerError, "upsert allocation: "+err.Error())
				return
			}
		}
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// HandleGoalsProgress returns financial goals with current progress and on-track status.
// Progress is measured against each goal's funding account, or a priority-
// ordered share of liquid wealth (see computeGoalsProgress) — never total net
// worth, which would count the same euros, the pension and the property
// towards every goal.
func (h *PortfolioHandler) HandleGoalsProgress(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	goals, err := h.queries.ListFinancialGoals(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list goals: "+err.Error())
		return
	}
	if len(goals) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{"goals": []any{}})
		return
	}

	// Account balances exactly as /portfolio/accounts reports them: cash plus
	// holdings at the latest price, in EUR.
	accounts, err := h.queries.ListAccounts(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list accounts: "+err.Error())
		return
	}
	fxRates := loadFXRatesMap(ctx, h.queries)
	cashByAcct, err := computeAllAccountCashEUR(ctx, h.queries, accounts, fxRates)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "account cash: "+err.Error())
		return
	}
	holdings, err := h.queries.ListCurrentHoldings(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list holdings: "+err.Error())
		return
	}
	prices := h.loadPriceMap(ctx)
	balances := make(map[uuid.UUID]goalAccount, len(accounts))
	for _, a := range accounts {
		balances[a.ID] = goalAccount{Type: a.Type, Balance: cashByAcct[a.ID]}
	}
	for _, hld := range holdings {
		if b, ok := balances[hld.AccountID]; ok {
			b.Balance += holdingValueEURFromMap(hld, prices, fxRates)
			balances[hld.AccountID] = b
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{"goals": computeGoalsProgress(goals, balances, time.Now())})
}

// rebalanceHolding is the minimal view computeRebalanceTrades needs from a
// holding — extracted so the pure rebalance algorithm can be unit-tested
// without touching the DB.
type rebalanceHolding struct {
	ISIN  string
	Name  string
	Value float64
	Price float64
}

// rebalanceTrade is the trade-suggestion record shared between the handler
// JSON shape and the pure algorithm.
type rebalanceTrade struct {
	ISIN       string  `json:"isin"`
	Name       string  `json:"name"`
	Action     string  `json:"action"` // "buy" or "sell"
	Amount     float64 `json:"amount"` // EUR value
	Shares     float64 `json:"shares,omitempty"`
	CurrentPct float64 `json:"current_pct"`
	TargetPct  float64 `json:"target_pct"`
}

// rebalanceMinTrade drops suggestions too small to be worth an order.
const rebalanceMinTrade = 10.0

// rebalancePlan is computeRebalanceTrades' result with the bookkeeping the
// page shows next to the trades.
type rebalancePlan struct {
	Trades []rebalanceTrade
	// Unallocated is money the trades leave over: in deposit mode the part
	// of the deposit not placed, in full mode sells minus buys (≈ 0; only
	// trades under the minimum are left out).
	Unallocated       float64
	TargetSum         float64 // targets as stored
	TargetsNormalised bool    // targets did not add up to 100 % and were scaled
}

// computeRebalanceTrades suggests trades to bring the portfolio toward its
// target allocation; see planRebalance.
func computeRebalanceTrades(holdings map[string]rebalanceHolding, targets map[string]float64, totalValue, deposit float64) []rebalanceTrade {
	return planRebalance(holdings, targets, totalValue, deposit).Trades
}

// planRebalance suggests trades toward the target allocation.
//
//   - Every held ISIN takes part; one without a target has target 0, so a
//     full rebalance sells it.
//   - Targets that do not add up to 100 % are scaled to 100 %.
//   - deposit > 0: buys only. The deposit fills each position's gap to its
//     target (on the portfolio including the deposit) pro rata; whatever is
//     left once every gap is filled is spread by target weight.
//   - deposit == 0: buys and sells that net to zero.
//   - Amounts are EUR; Shares is the matching fractional unit estimate (whole
//     share rounding depends on the broker). Trades under 10 € are dropped
//     and their money is reported as Unallocated.
//
// Invariant the handler / tests rely on: deposit > 0 → no Action == "sell".
func planRebalance(holdings map[string]rebalanceHolding, targets map[string]float64, totalValue, deposit float64) rebalancePlan {
	norm, sum, normalised := normalisedTargets(targets)
	plan := rebalancePlan{TargetSum: sum, TargetsNormalised: normalised}
	if len(norm) == 0 {
		plan.Unallocated = math.Max(0, deposit)
		return plan
	}
	universe := make([]string, 0, len(holdings)+len(norm))
	seen := make(map[string]bool)
	for isin := range holdings {
		if !seen[isin] {
			seen[isin] = true
			universe = append(universe, isin)
		}
	}
	for isin := range norm {
		if !seen[isin] {
			seen[isin] = true
			universe = append(universe, isin)
		}
	}
	sort.Strings(universe)

	trade := func(isin, action string, amount float64) rebalanceTrade {
		h := holdings[isin]
		name := h.Name
		if name == "" {
			name = isin
		}
		currentPct := 0.0
		if totalValue > 0 {
			currentPct = h.Value / totalValue * 100
		}
		shares := 0.0
		if h.Price > 0 {
			shares = math.Round(amount/h.Price*1e4) / 1e4
		}
		return rebalanceTrade{
			ISIN:       isin,
			Name:       name,
			Action:     action,
			Amount:     math.Round(amount*100) / 100,
			Shares:     shares,
			CurrentPct: math.Round(currentPct*10) / 10,
			TargetPct:  math.Round(norm[isin]*10) / 10,
		}
	}

	if deposit > 0 {
		effectiveTotal := totalValue + deposit
		gaps := make(map[string]float64)
		totalGap := 0.0
		for _, isin := range universe {
			if gap := effectiveTotal*norm[isin]/100 - holdings[isin].Value; gap > 0 {
				gaps[isin] = gap
				totalGap += gap
			}
		}
		alloc := make(map[string]float64)
		if totalGap >= deposit {
			for isin, gap := range gaps {
				alloc[isin] = deposit * gap / totalGap
			}
		} else {
			// Every gap fits: fill them, spread the rest by target weight.
			rest := deposit - totalGap
			for _, isin := range universe {
				alloc[isin] = gaps[isin] + rest*norm[isin]/100
			}
		}
		placed := 0.0
		for _, isin := range universe {
			a := alloc[isin]
			if a < rebalanceMinTrade {
				continue
			}
			plan.Trades = append(plan.Trades, trade(isin, "buy", a))
			placed += math.Round(a*100) / 100
		}
		plan.Unallocated = math.Round((deposit-placed)*100) / 100
		sort.SliceStable(plan.Trades, func(i, j int) bool { return plan.Trades[i].Amount > plan.Trades[j].Amount })
		return plan
	}

	// Full rebalance: target values on today's total; buys and sells net out.
	net := 0.0
	for _, isin := range universe {
		diff := totalValue*norm[isin]/100 - holdings[isin].Value
		if math.Abs(diff) < rebalanceMinTrade {
			continue
		}
		if diff > 0 {
			plan.Trades = append(plan.Trades, trade(isin, "buy", diff))
			net -= math.Round(diff*100) / 100
		} else {
			plan.Trades = append(plan.Trades, trade(isin, "sell", -diff))
			net += math.Round(-diff*100) / 100
		}
	}
	plan.Unallocated = math.Round(net*100) / 100
	sort.SliceStable(plan.Trades, func(i, j int) bool {
		if plan.Trades[i].Action != plan.Trades[j].Action {
			return plan.Trades[i].Action == "sell" // sells first: they fund the buys
		}
		return plan.Trades[i].Amount > plan.Trades[j].Amount
	})
	return plan
}

// HandleRebalance computes suggested trades to restore target allocation.
// Supports ?deposit=2000 to allocate a deposit to underweight positions only.
func (h *PortfolioHandler) HandleRebalance(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	holdings, err := h.queries.ListCurrentHoldings(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list holdings: "+err.Error())
		return
	}
	targets, err := h.queries.ListTargetAllocations(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list targets: "+err.Error())
		return
	}
	if len(targets) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{"trades": []any{}, "message": "No target allocation set"})
		return
	}

	pm := h.loadPriceMap(ctx)
	fxRates := loadFXRatesMap(ctx, h.queries)
	var costs map[positionKey]*positionCost // loaded on demand for unpriced holdings

	// Aggregate holdings by ISIN (in EUR)
	agg := make(map[string]rebalanceHolding)
	totalValue := 0.0
	for _, hold := range holdings {
		qty := numericToFloat(hold.Quantity)
		if qty <= 0 {
			continue
		}
		price := 0.0
		if p, ok := pm[hold.SecurityISIN]; ok && p.Close.Valid {
			price = convertAmountToEUR(numericToFloat(p.Close), p.Currency, fxRates)
		}
		value := qty * price
		if price == 0 {
			if costs == nil {
				costs = loadPositionCosts(ctx, h.queries, loadFXTable(ctx, h.queries))
			}
			if c := costs[positionKey{AccountID: hold.AccountID, ISIN: hold.SecurityISIN}]; c != nil {
				value = c.AvgCostEUR() * qty
			}
		}
		e := agg[hold.SecurityISIN]
		e.ISIN = hold.SecurityISIN
		if e.Name == "" {
			e.Name = hold.Name
		}
		e.Value += value
		e.Price = price
		agg[hold.SecurityISIN] = e
		totalValue += value
	}

	// Build target map; targets without a position still need a name/price.
	targetMap := make(map[string]float64)
	for _, t := range targets {
		targetMap[t.SecurityISIN] = numericToFloat(t.TargetWeightPct)
		if _, ok := agg[t.SecurityISIN]; !ok {
			price := 0.0
			if p, ok := pm[t.SecurityISIN]; ok && p.Close.Valid {
				price = convertAmountToEUR(numericToFloat(p.Close), p.Currency, fxRates)
			}
			agg[t.SecurityISIN] = rebalanceHolding{ISIN: t.SecurityISIN, Name: t.SecurityName, Price: price}
		}
	}

	// Parse deposit amount (buy-only mode)
	depositAmount := 0.0
	if d := r.URL.Query().Get("deposit"); d != "" {
		if n, err := parseFiniteFloat(d); err == nil && n > 0 && n <= 1e12 {
			depositAmount = n
		}
	}

	plan := planRebalance(agg, targetMap, totalValue, depositAmount)
	trades := plan.Trades
	if trades == nil {
		trades = []rebalanceTrade{}
	}

	msg := "Portfolio is within target allocation"
	if len(trades) > 0 {
		if depositAmount > 0 {
			msg = fmt.Sprintf("Allocate %s EUR deposit to underweight positions", fmtEUR(depositAmount))
		} else {
			msg = fmt.Sprintf("%d trades needed to restore target allocation", len(trades))
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"trades":             trades,
		"message":            msg,
		"deposit":            depositAmount,
		"unallocated":        plan.Unallocated,
		"target_sum":         math.Round(plan.TargetSum*100) / 100,
		"targets_normalised": plan.TargetsNormalised,
		"min_trade":          rebalanceMinTrade,
	})
}

// HandleProjection returns year-by-year projection data for charting.
// Query params: contribution (€/month), return_pct, horizon_years,
// contrib_growth (%). The FIRE drawdown is added when expenses > 0:
// expenses (€/yr in today's money, the spending the portfolio and the pension
// must cover), swr (%), pension (€/yr after income tax), current_age,
// pension_age, inflation (%), church_rate (0–0.09) and joint=1.
func (h *PortfolioHandler) HandleProjection(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := r.URL.Query()

	// Get goals
	goals, _ := h.queries.ListFinancialGoals(ctx)

	// Get current net worth
	snaps, err := h.queries.ListNetWorthSnapshots(ctx, 1)
	if err != nil || len(snaps) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{
			"projection":    []any{},
			"history":       []any{},
			"current_value": 0,
			"contribution":  0,
			"return_pct":    analytics.PlanningDefaultReturnPct,
			"target_amount": 0,
			"target_date":   "",
		})
		return
	}
	// Projections compound at a market return, which a depreciating car
	// doesn't earn — project investable wealth only.
	currentValue := investableNetWorth(snaps[0])
	now := time.Now()
	startMonth := addMonthsFirstOfMonth(now, 0).Format("2006-01")

	// Historical investable wealth, one point per month. The current month is
	// the projection's first point, so history stops before it.
	allSnaps, _ := h.queries.ListNetWorthSnapshots(ctx, 5000)
	type histPoint struct {
		Date  string  `json:"date"`
		Value float64 `json:"value"`
	}
	history := make([]histPoint, 0)
	lastMonth := ""
	// Snapshots are newest-first; reverse for chronological
	for i := len(allSnaps) - 1; i >= 0; i-- {
		s := allSnaps[i]
		month := s.Date.Format("2006-01")
		if month >= startMonth {
			break
		}
		if month != lastMonth {
			history = append(history, histPoint{Date: month, Value: math.Round(investableNetWorth(s))})
			lastMonth = month
		}
	}

	// Default contribution: trailing 12-month net external contribution.
	fxRates := loadFXRatesMap(ctx, h.queries)
	histFX := loadHistoricalFXRates(ctx, h.queries)
	txns, _ := listAllTransactionsRows(ctx, h.queries)
	acctTypes := h.accountTypesByID(ctx)
	physHoldings, _ := assets.Load(ctx, h.queries)
	trailing := trailingNetContribution(txns, acctTypes, now, histFX, fxRates, physHoldings)

	monthlyContrib := trailing.Monthly
	annualReturn := analytics.PlanningDefaultReturnPct
	// The earliest goal is only drawn as a labelled reference line; it no
	// longer replaces the contribution, return or horizon.
	targetAmount := 0.0
	targetDate := ""
	targetGoalName := ""
	if len(goals) > 0 {
		targetAmount = numericToFloat(goals[0].TargetAmount)
		targetDate = goals[0].TargetDate.Format("2006-01-02")
		targetGoalName = goals[0].Name
	}

	if c := q.Get("contribution"); c != "" {
		if v, err := parseFiniteFloat(c); err == nil && v >= -1e9 && v <= 1e9 {
			monthlyContrib = v
		}
	}
	if rr := q.Get("return_pct"); rr != "" {
		if v, err := parseFiniteFloat(rr); err == nil && v >= -50 && v <= 30 {
			annualReturn = v
		}
	}

	horizonYears := 30
	if hy := q.Get("horizon_years"); hy != "" {
		if v, err := strconv.Atoi(hy); err == nil && v >= 1 && v <= 50 {
			horizonYears = v
		}
	}
	contribGrowth := 0.0
	if cg := q.Get("contrib_growth"); cg != "" {
		if v, err := parseFiniteFloat(cg); err == nil && v >= 0 && v <= 20 {
			contribGrowth = v / 100
		}
	}

	monthlyReturn := planMonthlyRate(annualReturn)
	maxMonths := horizonYears * 12

	// Compute historical monthly volatility for confidence bands.
	// allSnaps are calendar-daily snapshots (newest-first); scale the daily
	// return std dev by sqrt(30.44 days per month), or use it as-is when the
	// snapshots are monthly, and cap to [0, 0.25].
	monthlyVol := 0.0
	if len(allSnaps) > 60 {
		var obsReturns []float64
		lastVal := 0.0
		var firstDate, lastDate time.Time
		for i := len(allSnaps) - 1; i >= 0; i-- {
			val := investableNetWorth(allSnaps[i])
			if val > 0 {
				if firstDate.IsZero() {
					firstDate = allSnaps[i].Date
				}
				lastDate = allSnaps[i].Date
			}
			if lastVal > 0 && val > 0 {
				ret := (val - lastVal) / lastVal
				if ret > -0.15 && ret < 0.15 { // skip cashflow/deposit step-jumps
					obsReturns = append(obsReturns, ret)
				}
			}
			lastVal = val
		}
		if len(obsReturns) > 12 {
			avg := 0.0
			for _, ret := range obsReturns {
				avg += ret
			}
			avg /= float64(len(obsReturns))
			sumSq := 0.0
			for _, ret := range obsReturns {
				d := ret - avg
				sumSq += d * d
			}
			obsStd := math.Sqrt(sumSq / float64(len(obsReturns)-1))
			avgGapDays := 1.0
			if !lastDate.Before(firstDate) && len(allSnaps) > 1 {
				avgGapDays = lastDate.Sub(firstDate).Hours() / 24.0 / float64(len(allSnaps)-1)
			}
			if avgGapDays < 5 {
				monthlyVol = obsStd * math.Sqrt(30.44)
			} else {
				monthlyVol = obsStd
			}
			if monthlyVol > 0.25 {
				monthlyVol = 0.25
			}
		}
	}

	type projPointFull struct {
		Date  string  `json:"date"`
		Value float64 `json:"value"`
		P10   float64 `json:"p10,omitempty"`
		P90   float64 `json:"p90,omitempty"`
	}

	projection := make([]projPointFull, 0)
	projMedian := currentValue
	projP10 := currentValue
	projP90 := currentValue

	for m := 0; m <= maxMonths; m++ {
		if m > 0 {
			projMedian = math.Max(0, projMedian*(1+monthlyReturn)+contribForMonth(monthlyContrib, contribGrowth, m))
		}

		// Confidence bands: log-normal spread around projMedian so growing
		// savings scale proportionally and P10 stays strictly in [0, Value].
		if monthlyVol > 0 && m > 0 {
			sigmaT := monthlyVol * math.Sqrt(float64(m))
			projP10 = math.Max(0, projMedian*math.Exp(-1.28*sigmaT))
			projP90 = projMedian * math.Exp(1.28*sigmaT)
		}

		if m%3 == 0 || m == maxMonths {
			date := addMonthsFirstOfMonth(now, m)
			pt := projPointFull{
				Date:  date.Format("2006-01"),
				Value: math.Round(projMedian),
			}
			if monthlyVol > 0 && m > 0 {
				pt.P10 = math.Round(projP10)
				pt.P90 = math.Round(projP90)
			}
			projection = append(projection, pt)
		}
	}

	// Sensitivity analysis: vary one parameter at a time, project to end.
	// planProjectFV uses the same contribution timing as the chart.
	projectFV := func(cv, mc, ar float64, months int) float64 {
		return math.Max(0, planProjectFV(cv, mc, ar, contribGrowth, months))
	}
	baselineFV := projectFV(currentValue, monthlyContrib, annualReturn, maxMonths)
	lowContrib := monthlyContrib - 500
	if monthlyContrib >= 0 {
		lowContrib = math.Max(lowContrib, 0)
	}

	type sensitivityBar struct {
		Label string  `json:"label"`
		Low   float64 `json:"low"`
		High  float64 `json:"high"`
		Base  float64 `json:"base"`
	}
	sensitivity := []sensitivityBar{
		{
			Label: "Return rate",
			Low:   math.Round(projectFV(currentValue, monthlyContrib, math.Max(annualReturn-3, 0), maxMonths)),
			High:  math.Round(projectFV(currentValue, monthlyContrib, annualReturn+3, maxMonths)),
			Base:  math.Round(baselineFV),
		},
		{
			Label: "Monthly contribution",
			Low:   math.Round(projectFV(currentValue, lowContrib, annualReturn, maxMonths)),
			High:  math.Round(projectFV(currentValue, monthlyContrib+500, annualReturn, maxMonths)),
			Base:  math.Round(baselineFV),
		},
		{
			Label: "Starting value",
			Low:   math.Round(projectFV(currentValue*0.8, monthlyContrib, annualReturn, maxMonths)),
			High:  math.Round(projectFV(currentValue*1.2, monthlyContrib, annualReturn, maxMonths)),
			Base:  math.Round(baselineFV),
		},
	}

	// Milestone table: key years with projected value, contributions, and growth.
	// Contributions are the money paid in after today; the starting value is
	// reported separately, so value = starting value + contributions + growth.
	type milestone struct {
		Year           int     `json:"year"`
		YearsFromNow   int     `json:"years_from_now"`
		ProjectedValue float64 `json:"projected_value"`
		StartingValue  float64 `json:"starting_value"`
		Contributions  float64 `json:"contributions"`
		Growth         float64 `json:"growth"`
		RealValue      float64 `json:"real_value"` // inflation-adjusted at 2%
	}
	milestoneYears := []int{1, 5, 10, 15, 20, 25, 30, 40, 50}
	milestones := make([]milestone, 0)
	currentYear := now.Year()
	for _, y := range milestoneYears {
		months := y * 12
		if months > maxMonths {
			break
		}
		fv := projectFV(currentValue, monthlyContrib, annualReturn, months)
		// Sum of contribForMonth over y years: monthlyContrib*12 per year,
		// growing by g each year → monthlyContrib*12*((1+g)^y − 1)/g.
		var totalDeposits float64
		if contribGrowth > 0 {
			totalDeposits = monthlyContrib * 12 * (math.Pow(1+contribGrowth, float64(y)) - 1) / contribGrowth
		} else {
			totalDeposits = monthlyContrib * float64(months)
		}
		growth := fv - currentValue - totalDeposits
		realValue := fv / math.Pow(1+analytics.PlanningDefaultInflationPct/100, float64(y))
		milestones = append(milestones, milestone{
			Year:           currentYear + y,
			YearsFromNow:   y,
			ProjectedValue: math.Round(fv),
			StartingValue:  math.Round(currentValue),
			Contributions:  math.Round(totalDeposits),
			Growth:         math.Round(growth),
			RealValue:      math.Round(realValue),
		})
	}

	// FIRE: accumulate until the portfolio covers the tax-grossed-up spending
	// gap at the withdrawal rate, then draw an inflation-indexed withdrawal.
	var drawdown map[string]any
	annualExpenses := 0.0
	if e := q.Get("expenses"); e != "" {
		if v, err := parseFiniteFloat(e); err == nil && v > 0 && v <= 1e9 {
			annualExpenses = v
		}
	}
	if annualExpenses > 0 {
		fp := fireParams{
			CurrentValue:   currentValue,
			MonthlyContrib: monthlyContrib,
			ContribGrowth:  contribGrowth,
			AnnualReturn:   annualReturn / 100,
			Inflation:      analytics.PlanningDefaultInflationPct / 100,
			Expenses:       annualExpenses,
			SWR:            0.035,
			MaxMonths:      600,
		}
		if s := q.Get("swr"); s != "" {
			if v, err := parseFiniteFloat(s); err == nil && v > 0 && v <= 10 {
				fp.SWR = v / 100
			}
		}
		if pv := q.Get("pension"); pv != "" {
			if v, err := parseFiniteFloat(pv); err == nil && v >= 0 && v <= 1e9 {
				fp.Pension = v
			}
		}
		if ca := q.Get("current_age"); ca != "" {
			if v, err := strconv.Atoi(ca); err == nil && v > 0 && v < 120 {
				fp.CurrentAge = v
			}
		}
		if pa := q.Get("pension_age"); pa != "" {
			if v, err := strconv.Atoi(pa); err == nil && v > 0 && v < 120 {
				fp.PensionAge = v
			}
		}
		if inf := q.Get("inflation"); inf != "" {
			if v, err := parseFiniteFloat(inf); err == nil && v >= -5 && v <= 20 {
				fp.Inflation = v / 100
			}
		}
		churchRate := 0.0
		if cr := q.Get("church_rate"); cr != "" {
			if v, err := parseFiniteFloat(cr); err == nil && v >= 0 && v <= 0.09 {
				churchRate = v
			}
		}
		joint := q.Get("joint") == "1"
		fp.TaxRate = analytics.CapitalIncomeTaxRate(churchRate)
		fp.TFS = h.portfolioTFS(ctx, fxRates)
		fp.Allowance = analytics.SparerPauschbetragForYear(now.Year(), joint)
		drawdown = fireDrawdownJSON(fp, simulateFIRE(fp, now), now)
	}

	// Projection accuracy: what the default projection would have shown 12
	// months ago — the investable value then, the trailing net contribution
	// as of then and the default return — against today's value. Today's
	// slider settings play no part.
	type projAccuracy struct {
		MonthsAgo           int     `json:"months_ago"`
		PastValue           float64 `json:"past_value"`
		ProjectedValue      float64 `json:"projected_value"`
		ActualValue         float64 `json:"actual_value"`
		DiffEUR             float64 `json:"diff_eur"`
		DiffPct             float64 `json:"diff_pct"`
		Basis               string  `json:"basis"`
		AssumedContribution float64 `json:"assumed_contribution"`
		AssumedReturnPct    float64 `json:"assumed_return_pct"`
	}
	var accuracy *projAccuracy
	pastCutoff := assets.CivilDate(now).AddDate(-1, 0, 0)
	for _, s := range allSnaps { // newest first: the first on or before the cutoff
		if s.Date.After(pastCutoff) {
			continue
		}
		pastVal := investableNetWorth(s)
		if pastVal <= 0 {
			break
		}
		then := trailingNetContribution(txns, acctTypes, s.Date, histFX, fxRates, physHoldings)
		projected := math.Max(0, planProjectFV(pastVal, then.Monthly, analytics.PlanningDefaultReturnPct, 0, 12))
		diffEUR := currentValue - projected
		diffPct := 0.0
		if projected > 0 {
			diffPct = (diffEUR / projected) * 100
		}
		accuracy = &projAccuracy{
			MonthsAgo:           12,
			PastValue:           math.Round(pastVal),
			ProjectedValue:      math.Round(projected),
			ActualValue:         math.Round(currentValue),
			DiffEUR:             math.Round(diffEUR),
			DiffPct:             math.Round(diffPct*10) / 10,
			Basis:               "default_projection_then",
			AssumedContribution: math.Round(then.Monthly),
			AssumedReturnPct:    analytics.PlanningDefaultReturnPct,
		}
		break
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"history":              history,
		"projection":           projection,
		"current_value":        math.Round(currentValue),
		"target_amount":        targetAmount,
		"target_date":          targetDate,
		"target_goal_name":     targetGoalName,
		"contribution":         monthlyContrib,
		"default_contribution": math.Round(trailing.Monthly*100) / 100,
		"contribution_basis": map[string]any{
			"kind":           "trailing_12m_net",
			"months_covered": math.Round(trailing.MonthsCovered*10) / 10,
			"deposits":       math.Round(trailing.Deposits),
			"withdrawals":    math.Round(trailing.Withdrawals),
			"in_kind":        math.Round(trailing.InKind),
			// One-off payments into physical assets, left out of the rate.
			"asset_payments": math.Round(trailing.AssetPayments),
			// Withdrawals that look like an asset's payment but aren't linked.
			"unlinked_asset_payments": nonNilHints(trailing.UnlinkedAssetPayments),
		},
		"return_pct":          annualReturn,
		"horizon_years":       horizonYears,
		"contrib_growth_pct":  math.Round(contribGrowth*1000) / 10, // back to %, 1 decimal
		"has_confidence":      monthlyVol > 0,
		"sensitivity":         sensitivity,
		"projection_end":      math.Round(baselineFV),
		"milestones":          milestones,
		"drawdown":            drawdown,
		"projection_accuracy": accuracy,
	})
}

// HandleSavingsRate computes monthly savings rate from deposit/withdrawal transactions.
func (h *PortfolioHandler) HandleSavingsRate(w http.ResponseWriter, r *http.Request) {
	txns, err := listAllTransactionsRows(r.Context(), h.queries)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list transactions: "+err.Error())
		return
	}
	fxRates := loadFXRatesMap(r.Context(), h.queries)
	histFX := loadHistoricalFXRates(r.Context(), h.queries)
	holdings, _ := assets.Load(r.Context(), h.queries)

	type monthData struct {
		Deposits    float64
		Withdrawals float64
	}
	monthly := make(map[string]*monthData)

	for _, txn := range txns {
		amt := 0.0
		if txn.Amount.Valid {
			f, _ := txn.Amount.Float64Value()
			amt = convertAmountToEURAtDate(f.Float64, txn.Currency, txn.Date, histFX, fxRates)
		}
		// Auto-wires from MS RSU sales are model artifacts (the cash never sat in
		// the account); excluding them keeps the invested/savings metric honest.
		if txn.Counterparty.Valid && strings.HasPrefix(txn.Counterparty.String, "Morgan Stanley (auto-wire of") {
			continue
		}
		// Paying for a tracked asset converts cash into an asset; it is not
		// dissaving. The real cost shows up as depreciation instead. Of a loan
		// instalment only the interest is spent; the principal is saved.
		if isAssetPurchase(txn.Category) {
			continue
		}
		if isLoanPayment(txn.Category) {
			interest, ok := loanInterestPart(holdings, txn.ID)
			if !ok {
				continue
			}
			amt = math.Copysign(interest, amt) // keep the import's sign convention
		}
		month := txn.Date.Format("2006-01")
		if monthly[month] == nil {
			monthly[month] = &monthData{}
		}
		switch txn.Type {
		case "deposit", "cash_transfer_in":
			monthly[month].Deposits += amt
		case "withdrawal", "cash_transfer_out":
			monthly[month].Withdrawals += amt
		}
	}

	type ratePoint struct {
		Month       string  `json:"month"`
		Deposits    float64 `json:"deposits"`
		Withdrawals float64 `json:"withdrawals"`
		NetSavings  float64 `json:"net_savings"`
		Rate        float64 `json:"rate"` // net savings / deposits (proxy for savings rate)
	}

	var months []string
	for m := range monthly {
		months = append(months, m)
	}
	sort.Strings(months)

	var points []ratePoint
	var totalDeposits, totalWithdrawals float64
	trailing12mNet := 0.0
	trailing12mDeposits := 0.0

	for i, m := range months {
		d := monthly[m]
		net := d.Deposits - d.Withdrawals
		rate := 0.0
		if d.Deposits > 0 {
			rate = (net / d.Deposits) * 100
		}
		totalDeposits += d.Deposits
		totalWithdrawals += d.Withdrawals

		// Trailing 12 months
		if i >= len(months)-12 {
			trailing12mNet += net
			trailing12mDeposits += d.Deposits
		}

		points = append(points, ratePoint{
			Month:       m,
			Deposits:    math.Round(d.Deposits*100) / 100,
			Withdrawals: math.Round(d.Withdrawals*100) / 100,
			NetSavings:  math.Round(net*100) / 100,
			Rate:        math.Round(rate*10) / 10,
		})
	}

	avgRate := 0.0
	if totalDeposits > 0 {
		avgRate = ((totalDeposits - totalWithdrawals) / totalDeposits) * 100
	}
	trailing12mRate := 0.0
	if trailing12mDeposits > 0 {
		trailing12mRate = (trailing12mNet / trailing12mDeposits) * 100
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"monthly":           points,
		"avg_savings_rate":  math.Round(avgRate*10) / 10,
		"trailing_12m_rate": math.Round(trailing12mRate*10) / 10,
		"total_deposits":    math.Round(totalDeposits*100) / 100,
		"total_withdrawals": math.Round(totalWithdrawals*100) / 100,
		"total_net_savings": math.Round((totalDeposits-totalWithdrawals)*100) / 100,
	})
}

// HandleAttribution explains the change between the two latest net-worth
// snapshots (the hero's "today"). Each holding is priced at both snapshot
// dates, each at that date's FX, which is how the snapshots themselves value
// it; flows are the transactions dated in between; the rest — FX on foreign
// cash, property revaluation, loan steps — is "other". The four parts add up
// to the snapshot change by construction.
func (h *PortfolioHandler) HandleAttribution(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	holdings, err := h.queries.ListCurrentHoldings(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list holdings: "+err.Error())
		return
	}
	snaps, err := h.queries.ListNetWorthSnapshots(ctx, 2)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list snapshots: "+err.Error())
		return
	}
	contribs := []attributionContribution{} // never nil: serialises as [] so clients can read .length
	if len(snaps) < 2 {
		writeJSON(w, http.StatusOK, map[string]any{"contributions": contribs, "total_change": 0, "summary": ""})
		return
	}
	to, from := snaps[0], snaps[1]

	secs, _ := h.queries.ListSecurities(ctx)
	secNames := make(map[string]string)
	for _, s := range secs {
		secNames[s.ISIN] = s.Name
	}

	// Aggregate by ISIN
	agg := make(map[string]float64) // ISIN -> qty
	for _, h := range holdings {
		agg[h.SecurityISIN] += numericToFloat(h.Quantity)
	}

	fxRates := loadFXRatesMap(ctx, h.queries)
	histFX := loadHistoricalFXRates(ctx, h.queries)
	marketChange := 0.0
	for isin, qty := range agg {
		if qty <= 0 {
			continue
		}
		priceRows, err := h.queries.ListPriceHistory(ctx, isin)
		if err != nil {
			continue
		}
		change, ok := holdingMoveEUR(priceRows, qty, from.Date, to.Date, histFX, fxRates)
		if !ok {
			continue
		}
		marketChange += change
		name := secNames[isin]
		if name == "" {
			name = isin
		}
		contribs = append(contribs, attributionContribution{
			ISIN: isin, Name: name, Change: math.Round(change*100) / 100,
		})
	}

	// Sort by absolute contribution descending
	sort.Slice(contribs, func(i, j int) bool {
		return math.Abs(contribs[i].Change) > math.Abs(contribs[j].Change)
	})

	// Real flows between the two snapshots, classified as in the waterfall.
	flows, income := 0.0, 0.0
	if txns, err := listAllTransactionsRows(ctx, h.queries); err == nil {
		accts, _ := h.queries.ListAccounts(ctx)
		chrono := make([]db.ListTransactionsRow, len(txns))
		for i := range txns {
			chrono[i] = txns[len(txns)-1-i]
		}
		physical := h.loadPhysicalAttribution(ctx, txns, histFX, fxRates)
		implicit := computeImplicitCashClampEvents(accts, chrono, histFX, fxRates)
		flows, income = netWorthFlows(txns, inKindWashSet(txns), physical.linked, implicit, from.Date, to.Date, histFX, fxRates)
	}

	netChange := numericToFloat(to.Total) - numericToFloat(from.Total)
	round := func(v float64) float64 { return math.Round(v*100) / 100 }
	writeJSON(w, http.StatusOK, map[string]any{
		"contributions": contribs,
		// total_change is the market move on held securities (price and FX).
		"total_change":  round(marketChange),
		"market_change": round(marketChange),
		"flows":         round(flows),
		"income":        round(income),
		"other":         round(netChange - marketChange - flows - income),
		"net_change":    round(netChange),
		"from_date":     from.Date.Format("2006-01-02"),
		"to_date":       to.Date.Format("2006-01-02"),
		"summary":       attributionSummary(contribs),
	})
}

// HandleSecurityDetail returns a comprehensive fact sheet for a single security.
func (h *PortfolioHandler) HandleSecurityDetail(w http.ResponseWriter, r *http.Request) {
	isin := chi.URLParam(r, "isin")
	if isin == "" {
		writeError(w, http.StatusBadRequest, "ISIN required")
		return
	}
	ctx := r.Context()

	// Security metadata
	secs, _ := h.queries.ListSecurities(ctx)
	var sec *db.Security
	for _, s := range secs {
		if s.ISIN == isin {
			sec = &s
			break
		}
	}
	if sec == nil {
		writeError(w, http.StatusNotFound, "security not found")
		return
	}

	// Holdings across accounts
	holdings, _ := h.queries.ListCurrentHoldings(ctx)
	accts, _ := h.queries.ListAccounts(ctx)
	acctNames := make(map[string]string)
	for _, a := range accts {
		acctNames[a.ID.String()] = a.Name
	}

	type positionByAccount struct {
		Account   string  `json:"account"`
		Quantity  float64 `json:"quantity"`
		CostBasis float64 `json:"cost_basis"`
		Value     float64 `json:"value"`
	}
	var positions []positionByAccount
	totalQty, totalCost, totalValue := 0.0, 0.0, 0.0
	pm := h.loadPriceMap(ctx)
	fxRates := loadFXRatesMap(ctx, h.queries)
	histFX := loadHistoricalFXRates(ctx, h.queries)
	fx := newFXTable(histFX, fxRates)
	price := convertAmountToEUR(numericToFloat(pm[isin].Close), pm[isin].Currency, fxRates)
	// Cost basis per account: moving-average cost, each purchase at the ECB
	// rate of its date (as on the Holdings tab).
	costs := loadPositionCosts(ctx, h.queries, fx)
	paidCur, paidCost, paidMixed := "", 0.0, false

	for _, hld := range holdings {
		if hld.SecurityISIN != isin {
			continue
		}
		qty := numericToFloat(hld.Quantity)
		cost := convertAmountToEUR(numericToFloat(hld.AvgCostBasis)*qty, hld.Currency, fxRates)
		if pc := costs[positionKey{AccountID: hld.AccountID, ISIN: isin}]; pc != nil && pc.Qty > 0 {
			cost = pc.AvgCostEUR() * qty
			if avg, cur, ok := pc.AvgCostNative(); ok && (paidCur == "" || paidCur == cur) {
				paidCur = cur
				paidCost += avg * qty
			} else {
				paidMixed = true
			}
		} else {
			paidMixed = true
		}
		val := qty * price
		if price == 0 {
			val = cost
		}
		positions = append(positions, positionByAccount{
			Account:   acctNames[hld.AccountID.String()],
			Quantity:  math.Round(qty*1e8) / 1e8,
			CostBasis: math.Round(cost*100) / 100,
			Value:     math.Round(val*100) / 100,
		})
		totalQty += qty
		totalCost += cost
		totalValue += val
	}

	// Price history (90 days for sparkline)
	priceHistory, _ := h.queries.ListPriceHistory(ctx, isin)
	type pricePoint struct {
		Date  string  `json:"date"`
		Price float64 `json:"price"`
	}
	var sparkline []pricePoint
	for i := len(priceHistory) - 1; i >= 0 && len(sparkline) < 90; i-- {
		// Each close at the ECB rate of its own date, not today's.
		sparkline = append([]pricePoint{{
			Date:  priceHistory[i].Date.Format("2006-01-02"),
			Price: fx.toEUR(numericToFloat(priceHistory[i].Close), priceHistory[i].Currency, priceHistory[i].Date),
		}}, sparkline...)
	}

	// Transactions for this ISIN
	txns, _ := listAllTransactionsRows(ctx, h.queries)
	type txnEntry struct {
		Date   string  `json:"date"`
		Type   string  `json:"type"`
		Qty    float64 `json:"quantity"`
		Amount float64 `json:"amount"`
		RunQty float64 `json:"running_qty"`
	}
	var txnHistory []txnEntry
	runQty := 0.0
	// Reverse for chronological
	for i := len(txns) - 1; i >= 0; i-- {
		t := txns[i]
		if !t.SecurityISIN.Valid || t.SecurityISIN.String != isin {
			continue
		}
		qty, amt := 0.0, 0.0
		if t.Quantity.Valid {
			f, _ := t.Quantity.Float64Value()
			qty = f.Float64
		}
		if t.Amount.Valid {
			f, _ := t.Amount.Float64Value()
			amt = fx.toEUR(f.Float64, t.Currency, t.Date)
		}
		switch t.Type {
		case "buy", "savings_plan", "transfer":
			runQty += qty
		case "sell", "transfer_out":
			runQty -= qty
		}
		txnHistory = append(txnHistory, txnEntry{
			Date:   t.Date.Format("2006-01-02"),
			Type:   t.Type,
			Qty:    math.Round(qty*1e8) / 1e8,
			Amount: math.Round(amt*100) / 100,
			RunQty: math.Round(runQty*1e8) / 1e8,
		})
	}

	// First acquisition: purchases and incoming transfers (RSU vests, depot
	// transfers) alike.
	firstBuy := ""
	for _, t := range txnHistory {
		if isAcquisitionType(t.Type) {
			firstBuy = t.Date
			break
		}
	}
	var avgCostPaid *float64
	if !paidMixed && paidCur != "" && totalQty > 0 {
		v := paidCost / totalQty
		avgCostPaid = &v
	}

	// Metadata
	ter := numericToFloat(sec.TER)
	symbol := ""
	if sec.Symbol.Valid {
		symbol = sec.Symbol.String
	}

	unrealizedPL := totalValue - totalCost
	weightPct := 0.0
	// Get total portfolio value for weight
	for _, hld := range holdings {
		weightPct += holdingValueEURFromMap(hld, pm, fxRates)
	}
	if weightPct > 0 {
		weightPct = totalValue / weightPct * 100
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"isin":           isin,
		"name":           sec.Name,
		"symbol":         symbol,
		"asset_class":    sec.AssetClass,
		"ter":            ter,
		"price":          price,
		"positions":      positions,
		"total_quantity": math.Round(totalQty*1e8) / 1e8,
		"total_cost":     math.Round(totalCost*100) / 100,
		"total_value":    math.Round(totalValue*100) / 100,
		"unrealized_pl":  math.Round(unrealizedPL*100) / 100,
		"weight_pct":     math.Round(weightPct*10) / 10,
		"first_buy":      firstBuy,
		"sparkline":      sparkline,
		"transactions":   txnHistory,
		// Average cost per unit in the currency the units were paid in
		// (null when accounts paid in different currencies); total_cost is
		// the EUR moving-average cost at purchase-date ECB rates.
		"avg_cost_paid":     avgCostPaid,
		"avg_cost_currency": paidCur,
	})
}

// HandleNextActions returns prioritized portfolio action recommendations.
// Scans tax lots, FSA usage, allocation drift, loss pots, dividends,
// concentration, costs, and cash drag to generate ranked actions.
func (h *PortfolioHandler) HandleNextActions(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	type action struct {
		Title    string  `json:"title"`
		Detail   string  `json:"detail"`
		Impact   float64 `json:"impact_eur"`
		Urgency  string  `json:"urgency"`  // now, this-month, this-quarter
		Category string  `json:"category"` // tax, rebalance, savings, cost, risk
		Link     string  `json:"link"`
		// ImpactKind says what impact_eur is: "saving" (money gained or tax
		// avoided) or "cost" (money that will leave, e.g. a tax debit).
		ImpactKind string `json:"impact_kind"`
	}
	var actions []action

	now := time.Now()
	monthsElapsed := float64(now.Month())
	if monthsElapsed < 1 {
		monthsElapsed = 1
	}

	// Tax settings live in the browser (Tax tools); the page passes them on.
	churchTax := 0.0
	if v, err := strconv.ParseFloat(r.URL.Query().Get("church_tax"), 64); err == nil && !math.IsNaN(v) {
		churchTax = v
	}
	joint := r.URL.Query().Get("joint") == "1"
	taxRate := analytics.CapitalIncomeTaxRate(churchTax)
	allowance := analytics.SparerPauschbetragForYear(now.Year(), joint)

	// --- Batch-load all data upfront ---
	holdings, _ := h.queries.ListCurrentHoldings(ctx)
	pm := h.loadPriceMap(ctx)
	fxRates := loadFXRatesMap(ctx, h.queries)
	histFX := loadHistoricalFXRates(ctx, h.queries)
	secs, _ := h.queries.ListSecurities(ctx)
	txns, _ := listAllTransactionsRows(ctx, h.queries)
	targets, _ := h.queries.ListTargetAllocations(ctx)
	accts, _ := h.queries.ListAccounts(ctx)

	secMap := make(map[string]db.Security, len(secs))
	names := make(map[string]string, len(secs))
	equityMap := make(map[string]bool, len(secs))
	tfsMap := make(map[string]float64, len(secs))
	terMap := make(map[string]float64, len(secs))
	for _, s := range secs {
		secMap[s.ISIN] = s
		names[s.ISIN] = s.Name
		tfs := securityTFSRate(s)
		tfsMap[s.ISIN] = tfs
		equityMap[s.ISIN] = tfs >= analytics.TeilfreistellungEquity
		terMap[s.ISIN] = numericToFloat(s.TER)
	}

	// Compute total portfolio value and per-holding values (in EUR)
	type holdingVal struct {
		isin  string
		name  string
		qty   float64
		avg   float64
		price float64
		value float64
		ter   float64
	}
	var hvs []holdingVal
	totalVal := 0.0
	for _, hld := range holdings {
		qty := numericToFloat(hld.Quantity)
		avg := convertAmountToEUR(numericToFloat(hld.AvgCostBasis), hld.Currency, fxRates)
		price := convertAmountToEUR(numericToFloat(pm[hld.SecurityISIN].Close), pm[hld.SecurityISIN].Currency, fxRates)
		if qty <= 0 {
			continue
		}
		val := qty * price
		if price <= 0 {
			val = qty * avg
		}
		name := names[hld.SecurityISIN]
		if name == "" {
			name = hld.SecurityISIN
		}
		hvs = append(hvs, holdingVal{
			isin: hld.SecurityISIN, name: name,
			qty: qty, avg: avg, price: price, value: val,
			ter: terMap[hld.SecurityISIN],
		})
		totalVal += val
	}

	// --- 1. Tax-Loss Harvesting (FIFO-based) ---
	taxTxns, _, _, _ := buildTaxTransactions(txns, secs, histFX, fxRates)
	taxTxns = dropPensionWrapperTxns(ctx, h.queries, taxTxns)

	prices := make(map[string]float64)
	for _, hv := range hvs {
		if hv.price > 0 {
			prices[hv.isin] = hv.price
		}
	}
	lots := analytics.ComputeTaxLots(taxTxns, prices, names, equityMap)

	// Aggregate unrealized loss per ISIN from FIFO lots
	type lossInfo struct {
		name      string
		totalPL   float64
		costBasis float64
		value     float64
	}
	lossByISIN := make(map[string]*lossInfo)
	for _, lot := range lots {
		if lot.Quantity <= 1e-9 || prices[lot.ISIN] <= 0 {
			continue
		}
		li, ok := lossByISIN[lot.ISIN]
		if !ok {
			li = &lossInfo{name: lot.Name}
			lossByISIN[lot.ISIN] = li
		}
		li.totalPL += lot.UnrealizedPL
		li.costBasis += lot.CostBasis
		li.value += lot.CurrentValue
	}
	// A realised loss saves tax this year only against gains realised this
	// year in a pot it may offset, and only above the Sparer-Pauschbetrag.
	// §23 assets (crypto, gold) are outside capital income and skipped.
	lossPots := analytics.ComputeLossPots(taxTxns)
	incomeYTD := capitalIncomeFromPots(lossPots, now.Year())
	var harvest []harvestCandidate
	harvestPct := make(map[string]float64)
	for isin, li := range lossByISIN {
		if li.totalPL >= -100 || li.costBasis <= 0 {
			continue
		}
		lossPct := li.totalPL / li.costBasis * 100
		if lossPct > -5 || analytics.IsPrivateSaleAssetClass(secMap[isin].AssetClass) {
			continue
		}
		harvestPct[isin] = lossPct
		harvest = append(harvest, harvestCandidate{
			ISIN: isin, Name: li.name, Loss: -li.totalPL, TFS: tfsMap[isin],
			Share: analytics.IsShareAssetClass(secMap[isin].AssetClass),
		})
	}
	sort.Slice(harvest, func(i, j int) bool {
		if harvest[i].Loss != harvest[j].Loss {
			return harvest[i].Loss > harvest[j].Loss
		}
		return harvest[i].ISIN < harvest[j].ISIN
	})
	for i, saving := range harvestSavings(harvest, incomeYTD, allowance, taxRate) {
		if saving < 1 {
			continue // nothing realised this year for it to offset
		}
		c := harvest[i]
		offsets := "this year's realised gains"
		if c.Share {
			offsets = "this year's realised share gains (share losses offset nothing else)"
		}
		actions = append(actions, action{
			Title:      "Harvest tax loss: " + c.Name,
			Detail:     fmt.Sprintf("Down %s EUR (%.1f%%). Selling now offsets %s and saves about %s EUR in tax; the position can be bought back.", fmtEUR(-c.Loss), harvestPct[c.ISIN], offsets, fmtEUR(saving)),
			Impact:     math.Round(saving),
			ImpactKind: "saving",
			Urgency:    "this-quarter",
			Category:   "tax",
			Link:       "/analysis",
		})
	}

	// --- 2. Loss Pot Netting (carry-forward losses that can offset future gains) ---
	// Using the pot now rather than later mostly moves tax in time (the losses
	// never expire, and the gains' cost basis steps up), so no saving is shown.
	if len(lossPots) > 0 {
		latest := lossPots[len(lossPots)-1]
		unusedLoss := 0.0
		if latest.CarryForwardEquity < 0 {
			unusedLoss += math.Abs(latest.CarryForwardEquity)
		}
		if latest.CarryForwardGeneral < 0 {
			unusedLoss += math.Abs(latest.CarryForwardGeneral)
		}
		if unusedLoss > 100 {
			actions = append(actions, action{
				Title:      "Use loss carry-forward",
				Detail:     fmt.Sprintf("%s EUR in carry-forward losses available. Gains realised up to this amount are tax-free now; this shifts tax to later rather than saving it.", fmtEUR(unusedLoss)),
				Impact:     0,
				ImpactKind: "saving",
				Urgency:    "this-quarter",
				Category:   "tax",
				Link:       "/analysis",
			})
		}
	}

	// --- 3. Allocation Drift ---
	if len(targets) > 0 && totalVal > 0 {
		maxDrift := 0.0
		driftName := ""
		for _, t := range targets {
			for _, hv := range hvs {
				if hv.isin == t.SecurityISIN {
					actual := hv.value / totalVal * 100
					target := numericToFloat(t.TargetWeightPct)
					drift := math.Abs(actual - target)
					if drift > maxDrift {
						maxDrift = drift
						driftName = hv.name
					}
				}
			}
		}
		if maxDrift > 3 {
			urgency := "this-month"
			if maxDrift > 7 {
				urgency = "now"
			}
			actions = append(actions, action{
				Title:    "Rebalance portfolio",
				Detail:   fmt.Sprintf("Max drift %.1f pp from target (%s). Consider rebalancing to reduce risk.", maxDrift, driftName),
				Impact:   0,
				Urgency:  urgency,
				Category: "rebalance",
				Link:     "/portfolio",
			})
		}
	}

	// --- 4. FSA Utilization ---
	// Dividends and interest so far this year, extrapolated to December on
	// top of everything realised (incomeYTD already holds them once).
	ytdIncome := 0.0
	for _, txn := range txns {
		if txn.Date.Year() != now.Year() {
			continue
		}
		if txn.Type == "dividend" || txn.Type == "interest" {
			if txn.Amount.Valid {
				f, _ := txn.Amount.Float64Value()
				amt := convertAmountToEURAtDate(f.Float64, txn.Currency, txn.Date, histFX, fxRates)
				isin := ""
				if txn.SecurityISIN.Valid {
					isin = txn.SecurityISIN.String
				}
				if txn.Type == "dividend" {
					amt *= (1 - tfsMap[isin])
				}
				ytdIncome += amt
			}
		}
	}
	expected := incomeYTD
	expected.otherRaw += ytdIncome * (12/monthsElapsed - 1)
	projected := expected.taxable() // realised gains (not extrapolated) + dividends and interest
	if projected < allowance*0.8 {
		// The unused allowance is worth something only if there are gains to
		// realise into it: those become tax-free for good.
		shareGains, otherGains := 0.0, 0.0
		for _, lot := range lots {
			if lot.UnrealizedPL <= 0 || prices[lot.ISIN] <= 0 {
				continue
			}
			ac := secMap[lot.ISIN].AssetClass
			switch {
			case analytics.IsPrivateSaleAssetClass(ac):
			case analytics.IsShareAssetClass(ac):
				shareGains += lot.UnrealizedPL
			default:
				otherGains += lot.UnrealizedPL * (1 - tfsMap[lot.ISIN])
			}
		}
		room := allowanceGainRoom(expected, shareGains, otherGains, allowance)
		if worth := room * taxRate; worth >= 1 {
			urgency := "this-quarter"
			if now.Month() >= 10 {
				urgency = "this-month" // year-end approaching
			}
			actions = append(actions, action{
				Title:      "Use the rest of your Sparer-Pauschbetrag",
				Detail:     fmt.Sprintf("Projected %s EUR of your %s EUR allowance used in %d. Realising gains worth about %s EUR of taxable income before 31 December would use the rest tax-free.", fmtEUR(projected), fmtEUR(allowance), now.Year(), fmtEUR(room)),
				Impact:     math.Round(worth),
				ImpactKind: "saving",
				Urgency:    urgency,
				Category:   "tax",
				Link:       "/analysis",
			})
		}
	}

	// --- 5. Concentration Risk ---
	if totalVal > 0 {
		for _, hv := range hvs {
			pct := hv.value / totalVal * 100
			if pct > 25 && len(hvs) > 1 {
				actions = append(actions, action{
					Title:    "High concentration: " + hv.name,
					Detail:   fmt.Sprintf("%.0f%% of portfolio in a single position (%s EUR). Consider diversifying to reduce risk.", pct, fmtEUR(hv.value)),
					Impact:   0,
					Urgency:  "this-month",
					Category: "risk",
					Link:     "/analysis",
				})
			}
		}
	}

	// --- 6. Cost Optimization (high TER) ---
	for _, hv := range hvs {
		if hv.ter > 0.5 && hv.value > 1000 {
			annualCost := hv.value * hv.ter / 100
			actions = append(actions, action{
				Title:    "High cost: " + hv.name,
				Detail:   fmt.Sprintf("TER %.2f%% costs ~%s EUR/year. Consider a cheaper alternative.", hv.ter, fmtEUR(annualCost)),
				Impact:   math.Round(annualCost * 0.5), // assume half could be saved
				Urgency:  "this-quarter",
				Category: "cost",
				Link:     "/analysis",
			})
		}
	}

	// --- 7. Cash Drag ---
	totalCash := 0.0
	if cashByAcct, err := computeAllAccountCashEUR(ctx, h.queries, accts, fxRates); err == nil {
		for _, c := range cashByAcct {
			if c > 0 {
				totalCash += c
			}
		}
	}
	if totalCash > 0 && totalVal > 0 {
		cashPct := totalCash / (totalVal + totalCash) * 100
		// Recommend if cash exceeds 15% of total wealth and is > 5000 EUR
		if cashPct > 15 && totalCash > 5000 {
			// Opportunity cost: excess cash × portfolio return estimate (7% nominal)
			excessCash := totalCash - (totalVal+totalCash)*0.10 // keep 10% as reserve
			if excessCash > 1000 {
				opportunityCost := excessCash * 0.07
				actions = append(actions, action{
					Title:    "Excess cash drag",
					Detail:   fmt.Sprintf("%s EUR in cash (%.0f%% of wealth). Investing excess ~%s EUR could earn ~%s EUR/year.", fmtEUR(totalCash), cashPct, fmtEUR(excessCash), fmtEUR(opportunityCost)),
					Impact:   math.Round(opportunityCost),
					Urgency:  "this-month",
					Category: "savings",
					Link:     "/portfolio",
				})
			}
		}
	}

	// --- 8. Vorabpauschale Preparation (Oct-Dec) ---
	// Same § 18 InvStG computation as the Tax page: 1 January value, pro-rated
	// mid-year buys, capped at the year's actual increase plus distributions.
	if now.Month() >= 10 {
		curVal := make(map[string]float64)
		curQty := make(map[string]float64)
		for _, hv := range hvs {
			curVal[hv.isin] += hv.value
			curQty[hv.isin] += hv.qty
		}
		entries := (&AnalysisHandler{queries: h.queries}).computeYearVorabpauschale(ctx, now.Year(), txns, secMap, tfsMap, curVal, curQty, histFX, fxRates)
		nextAllowance := analytics.SparerPauschbetragForYear(now.Year()+1, joint)
		vpTaxable, tax := vorabpauschaleTax(entries, tfsMap, nextAllowance, taxRate)
		if tax > 10 {
			actions = append(actions, action{
				Title:      "Prepare for Vorabpauschale",
				Detail:     fmt.Sprintf("Your funds' Vorabpauschale for %d is about %s EUR taxable. Beyond the %s EUR Sparer-Pauschbetrag for %d, about %s EUR tax will be debited in early January. Keep that much cash in the brokerage account.", now.Year(), fmtEUR(vpTaxable), fmtEUR(nextAllowance), now.Year()+1, fmtEUR(tax)),
				Impact:     math.Round(tax),
				ImpactKind: "cost",
				Urgency:    "this-month",
				Category:   "tax",
				Link:       "/analysis",
			})
		}
	}

	// --- Sort by impact descending, then by urgency ---
	urgencyOrder := map[string]int{"now": 0, "this-month": 1, "this-quarter": 2}
	sort.Slice(actions, func(i, j int) bool {
		if actions[i].Impact != actions[j].Impact {
			return actions[i].Impact > actions[j].Impact
		}
		return urgencyOrder[actions[i].Urgency] < urgencyOrder[actions[j].Urgency]
	})

	if len(actions) > 8 {
		actions = actions[:8]
	}
	if actions == nil {
		actions = []action{}
	}

	writeJSON(w, http.StatusOK, map[string]any{"actions": actions, "count": len(actions)})
}

// planBuy is one purchase of an ISIN in EUR at its date's ECB rate.
type planBuy struct {
	Date   time.Time
	Amount float64
	Qty    float64
	IsPlan bool // recorded as a savings_plan execution by the broker
}

// savingsPlanStats describes a detected savings plan.
type savingsPlanStats struct {
	// MonthlyAmount is the plan's current run rate: the median monthly total
	// of its last three executed months within the trailing 12 months.
	MonthlyAmount float64
	// Trailing12mMonthly is the plan's purchases over the trailing 12 months
	// ÷ 12 (lower than the run rate for a plan started or resized recently).
	Trailing12mMonthly float64
	Active             bool // executed within the last ~2 months
	Executions         int
	Invested           float64 // plan purchases only (lump sums excluded)
	Qty                float64
	FirstPrice         float64 // EUR per unit of the first execution
	LumpSums           int
	LumpSumAmount      float64
	FirstDate          time.Time
	LastDate           time.Time
	MonthsSpan         int // calendar months from first to last execution, inclusive
	MonthsWithPurchase int
}

const savingsPlanActiveDays = 62

// analyseSavingsPlan separates regular plan executions from one-off lump
// sums and measures the plan. Broker-tagged savings_plan rows are the plan
// when present; otherwise a buy above twice the median purchase is a lump
// sum. ok is false when fewer than three executions in three different
// months remain.
func analyseSavingsPlan(buys []planBuy, now time.Time) (savingsPlanStats, bool) {
	var s savingsPlanStats
	if len(buys) == 0 {
		return s, false
	}
	sort.SliceStable(buys, func(i, j int) bool { return buys[i].Date.Before(buys[j].Date) })
	tagged := false
	for _, b := range buys {
		if b.IsPlan {
			tagged = true
			break
		}
	}
	median := func(xs []float64) float64 {
		if len(xs) == 0 {
			return 0
		}
		c := append([]float64(nil), xs...)
		sort.Float64s(c)
		if n := len(c); n%2 == 1 {
			return c[n/2]
		} else {
			return (c[n/2-1] + c[n/2]) / 2
		}
	}
	var amounts []float64
	for _, b := range buys {
		amounts = append(amounts, b.Amount)
	}
	lumpCap := 2 * median(amounts)

	var plan []planBuy
	for _, b := range buys {
		if (tagged && !b.IsPlan) || (!tagged && b.Amount > lumpCap) {
			s.LumpSums++
			s.LumpSumAmount += b.Amount
			continue
		}
		plan = append(plan, b)
	}
	months := make(map[string]float64)
	var monthKeys []string
	for _, b := range plan {
		k := b.Date.Format("2006-01")
		if _, ok := months[k]; !ok {
			monthKeys = append(monthKeys, k)
		}
		months[k] += b.Amount
	}
	if len(plan) < 3 || len(months) < 3 {
		return s, false
	}

	s.Executions = len(plan)
	s.MonthsWithPurchase = len(months)
	s.FirstDate = plan[0].Date
	s.LastDate = plan[len(plan)-1].Date
	s.MonthsSpan = (s.LastDate.Year()-s.FirstDate.Year())*12 + int(s.LastDate.Month()-s.FirstDate.Month()) + 1
	s.Active = !s.LastDate.Before(now.AddDate(0, 0, -savingsPlanActiveDays))
	if plan[0].Qty > 0 {
		s.FirstPrice = plan[0].Amount / plan[0].Qty
	}
	cutoff := now.AddDate(-1, 0, 0)
	t12 := 0.0
	for _, b := range plan {
		s.Invested += b.Amount
		s.Qty += b.Qty
		if b.Date.After(cutoff) {
			t12 += b.Amount
		}
	}
	s.Trailing12mMonthly = t12 / 12

	// Run rate: median of the last three executed months in the trailing year.
	var recent []float64
	for i := len(monthKeys) - 1; i >= 0 && len(recent) < 3; i-- {
		if m, err := time.Parse("2006-01", monthKeys[i]); err == nil && m.AddDate(0, 1, 0).After(cutoff) {
			recent = append(recent, months[monthKeys[i]])
		}
	}
	s.MonthlyAmount = median(recent)
	return s, true
}

// HandleSavingsPlans detects recurring buy patterns and computes DCA metrics.
// Only plans that executed within the last ~2 months are listed and counted
// in the monthly total; their monthly amount is the current run rate.
func (h *PortfolioHandler) HandleSavingsPlans(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	txns, err := listAllTransactionsRows(ctx, h.queries)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list transactions: "+err.Error())
		return
	}
	fx := loadFXTable(ctx, h.queries)
	fxRates := fx.latest

	secs, _ := h.queries.ListSecurities(ctx)
	secNames := make(map[string]string)
	for _, s := range secs {
		secNames[s.ISIN] = s.Name
	}

	// Group buy/savings_plan transactions by ISIN
	isinBuys := make(map[string][]planBuy)
	for _, txn := range txns {
		if txn.Type != "buy" && txn.Type != "savings_plan" {
			continue
		}
		if !txn.SecurityISIN.Valid {
			continue
		}
		amt := fx.toEUR(math.Abs(numericToFloat(txn.Amount)), txn.Currency, txn.Date)
		isinBuys[txn.SecurityISIN.String] = append(isinBuys[txn.SecurityISIN.String], planBuy{
			Date: txn.Date, Amount: amt, Qty: math.Abs(numericToFloat(txn.Quantity)), IsPlan: txn.Type == "savings_plan",
		})
	}

	// Load current prices and positions for the value today
	pm := h.loadPriceMap(ctx)
	heldQty := make(map[string]float64)
	if holdings, err := h.queries.ListCurrentHoldings(ctx); err == nil {
		for _, hold := range holdings {
			heldQty[hold.SecurityISIN] += numericToFloat(hold.Quantity)
		}
	}

	type sparplan struct {
		ISIN          string  `json:"isin"`
		Name          string  `json:"name"`
		MonthlyAmount float64 `json:"monthly_amount"` // current run rate
		// Trailing12mMonthly: plan purchases in the trailing 12 months ÷ 12.
		Trailing12mMonthly float64 `json:"trailing_12m_monthly"`
		Executions         int     `json:"executions"`
		TotalInvested      float64 `json:"total_invested"` // plan purchases, lump sums excluded
		TotalShares        float64 `json:"total_shares"`   // units bought by the plan
		AvgCostBasis       float64 `json:"avg_cost_basis"`
		FirstDate          string  `json:"first_date"`
		LastDate           string  `json:"last_date"`
		MonthsActive       int     `json:"months_active"` // calendar months from first to last execution
		MonthsWithPurchase int     `json:"months_with_purchase"`
		LumpSumCount       int     `json:"lump_sum_count"`
		LumpSumAmount      float64 `json:"lump_sum_amount"`
		// CurrentValue is the position held today (after any sales), EUR.
		CurrentValue float64 `json:"current_value"`
		// DCA analysis on the plan's own purchases, sales ignored on both sides.
		PlanValue     float64 `json:"plan_value"`     // plan units × today's price
		DCAReturn     float64 `json:"dca_return_pct"` // plan value vs plan invested
		LumpSumValue  float64 `json:"lump_sum_value"` // value if all invested on first date
		LumpSumReturn float64 `json:"lump_sum_return_pct"`
		DCAAdvantage  float64 `json:"dca_advantage_eur"` // plan value - lump sum value
	}

	var plans []sparplan
	totalMonthly := 0.0
	inactive := 0
	now := time.Now()

	for isin, buys := range isinBuys {
		st, ok := analyseSavingsPlan(buys, now)
		if !ok {
			continue
		}
		if !st.Active {
			inactive++
			continue
		}

		name := secNames[isin]
		if name == "" {
			name = isin
		}
		avgCost := 0.0
		if st.Qty > 0 {
			avgCost = st.Invested / st.Qty
		}

		currentPrice := convertAmountToEUR(numericToFloat(pm[isin].Close), pm[isin].Currency, fxRates)
		planValue := st.Qty * currentPrice
		dcaReturnPct := 0.0
		if st.Invested > 0 && currentPrice > 0 {
			dcaReturnPct = ((planValue - st.Invested) / st.Invested) * 100
		}
		// Lump-sum: if all plan money had been invested at the first price
		lumpSumValue := 0.0
		if st.FirstPrice > 0 {
			lumpSumValue = st.Invested / st.FirstPrice * currentPrice
		}
		lumpSumReturnPct := 0.0
		if st.Invested > 0 && lumpSumValue > 0 {
			lumpSumReturnPct = ((lumpSumValue - st.Invested) / st.Invested) * 100
		}

		plans = append(plans, sparplan{
			ISIN:               isin,
			Name:               name,
			MonthlyAmount:      math.Round(st.MonthlyAmount*100) / 100,
			Trailing12mMonthly: math.Round(st.Trailing12mMonthly*100) / 100,
			Executions:         st.Executions,
			TotalInvested:      math.Round(st.Invested*100) / 100,
			TotalShares:        math.Round(st.Qty*1e8) / 1e8,
			AvgCostBasis:       math.Round(avgCost*100) / 100,
			FirstDate:          st.FirstDate.Format("2006-01-02"),
			LastDate:           st.LastDate.Format("2006-01-02"),
			MonthsActive:       st.MonthsSpan,
			MonthsWithPurchase: st.MonthsWithPurchase,
			LumpSumCount:       st.LumpSums,
			LumpSumAmount:      math.Round(st.LumpSumAmount*100) / 100,
			CurrentValue:       math.Round(heldQty[isin]*currentPrice*100) / 100,
			PlanValue:          math.Round(planValue*100) / 100,
			DCAReturn:          math.Round(dcaReturnPct*10) / 10,
			LumpSumValue:       math.Round(lumpSumValue*100) / 100,
			LumpSumReturn:      math.Round(lumpSumReturnPct*10) / 10,
			DCAAdvantage:       math.Round((planValue-lumpSumValue)*100) / 100,
		})
		totalMonthly += st.MonthlyAmount
	}

	sort.Slice(plans, func(i, j int) bool { return plans[i].TotalInvested > plans[j].TotalInvested })
	if plans == nil {
		plans = []sparplan{}
	}

	// Rebalancing suggestion: align Sparplan amounts with target allocation
	type rebalSuggestion struct {
		ISIN          string  `json:"isin"`
		Name          string  `json:"name"`
		CurrentAmount float64 `json:"current_amount"`
		TargetAmount  float64 `json:"target_amount"`
		Change        float64 `json:"change"`
	}
	var suggestions []rebalSuggestion
	targets, _ := h.queries.ListTargetAllocations(ctx)
	if len(targets) > 0 && totalMonthly > 0 {
		targetMap := make(map[string]float64)
		for _, t := range targets {
			targetMap[t.SecurityISIN] = numericToFloat(t.TargetWeightPct)
		}
		for _, p := range plans {
			targetPct := targetMap[p.ISIN]
			if targetPct <= 0 {
				continue
			}
			targetAmt := totalMonthly * targetPct / 100
			change := targetAmt - p.MonthlyAmount
			if math.Abs(change) > 10 { // only suggest meaningful changes
				suggestions = append(suggestions, rebalSuggestion{
					ISIN: p.ISIN, Name: p.Name,
					CurrentAmount: p.MonthlyAmount,
					TargetAmount:  math.Round(targetAmt*100) / 100,
					Change:        math.Round(change*100) / 100,
				})
			}
		}
	}
	if suggestions == nil {
		suggestions = []rebalSuggestion{}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"plans":          plans,
		"total_monthly":  math.Round(totalMonthly*100) / 100,
		"plan_count":     len(plans),
		"inactive_count": inactive,
		"rebalance":      suggestions,
	})
}

// HandleLifeEvents simulates life events' impact on the investable-wealth
// projection (property bought and loans taken are not part of it).
// POST body: { "events": [...], "contribution": 1000, "return_pct": 7,
// "expenses": 36000, "swr": 3.5, "pension": 12000, "current_age": 35,
// "pension_age": 67, "inflation_pct": 2 }. contribution and return_pct may be
// omitted (trailing 12-month net contribution, 7 %); 0 is a valid value.
// The independence shift uses the FIRE calculator's target and is only
// reported when expenses are given.
func (h *PortfolioHandler) HandleLifeEvents(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	type lifeEvent struct {
		Type           string  `json:"type"`            // home_purchase, sabbatical, marriage, child, job_change, inheritance, early_retirement
		Date           string  `json:"date"`            // YYYY-MM
		CashImpact     float64 `json:"cash_impact"`     // negative = outflow, positive = inflow
		ContribChange  float64 `json:"contrib_change"`  // change in monthly contribution from this point
		RecurringCost  float64 `json:"recurring_cost"`  // monthly recurring cost (e.g. mortgage, childcare)
		DurationMonths int     `json:"duration_months"` // how long recurring costs last (0 = permanent)
		TaxImpact      float64 `json:"tax_impact"`      // one-time tax (positive = tax due, negative = benefit)
		Label          string  `json:"label"`           // display name
	}

	var req struct {
		Events       []lifeEvent `json:"events"`
		Contribution *float64    `json:"contribution"`
		ReturnPct    *float64    `json:"return_pct"`
		Expenses     float64     `json:"expenses"`
		SWR          float64     `json:"swr"`
		Pension      float64     `json:"pension"`
		CurrentAge   int         `json:"current_age"`
		PensionAge   int         `json:"pension_age"`
		InflationPct *float64    `json:"inflation_pct"`
		ChurchRate   float64     `json:"church_rate"`
		Joint        bool        `json:"joint"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request: "+err.Error())
		return
	}
	isFinite := func(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }
	finitePtr := func(p *float64) bool { return p == nil || isFinite(*p) }
	if !finitePtr(req.Contribution) || !finitePtr(req.ReturnPct) || !finitePtr(req.InflationPct) ||
		!isFinite(req.Expenses) || !isFinite(req.SWR) || !isFinite(req.Pension) || !isFinite(req.ChurchRate) {
		writeError(w, http.StatusBadRequest, "contribution, return_pct and the FIRE inputs must be finite numbers")
		return
	}
	for _, ev := range req.Events {
		if !isFinite(ev.CashImpact) || !isFinite(ev.ContribChange) || !isFinite(ev.RecurringCost) || !isFinite(ev.TaxImpact) {
			writeError(w, http.StatusBadRequest, "event values must be finite numbers")
			return
		}
	}

	// Get current net worth
	snaps, err := h.queries.ListNetWorthSnapshots(ctx, 1)
	if err != nil || len(snaps) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{"baseline": []any{}, "with_events": []any{}})
		return
	}
	currentValue := investableNetWorth(snaps[0])
	fxRates := loadFXRatesMap(ctx, h.queries)
	histFX := loadHistoricalFXRates(ctx, h.queries)
	txns, _ := listAllTransactionsRows(ctx, h.queries)
	now := time.Now()

	monthlyContrib := 0.0
	if req.Contribution != nil {
		monthlyContrib = *req.Contribution
	} else {
		monthlyContrib = trailingNetContribution(txns, h.accountTypesByID(ctx), now, histFX, fxRates, h.physicalHoldings(ctx)).Monthly
	}
	annualReturn := analytics.PlanningDefaultReturnPct
	if req.ReturnPct != nil {
		annualReturn = math.Max(-99.99, math.Min(50, *req.ReturnPct))
	}

	const chartMonths = 360 // 30 years on the chart
	const fireMonths = 600  // independence is searched for up to 50 years
	monthlyReturn := planMonthlyRate(annualReturn)

	// Event dates ("YYYY-MM") become whole calendar-month offsets from this month.
	var parsed []lifeEventSim
	for _, e := range req.Events {
		t, err := time.Parse("2006-01", e.Date)
		if err != nil {
			continue
		}
		parsed = append(parsed, lifeEventSim{
			MonthOffset:    monthsBetween(now, t),
			CashImpact:     e.CashImpact,
			TaxImpact:      e.TaxImpact,
			ContribChange:  e.ContribChange,
			RecurringCost:  e.RecurringCost,
			DurationMonths: e.DurationMonths,
		})
	}

	baseVals, baseBasis := simulateLifeEvents(currentValue, monthlyContrib, monthlyReturn, nil, fireMonths)
	evVals, evBasis := simulateLifeEvents(currentValue, monthlyContrib, monthlyReturn, parsed, fireMonths)

	type projPoint struct {
		Date  string  `json:"date"`
		Value float64 `json:"value"`
	}
	baseline := make([]projPoint, 0, chartMonths/3+1)
	withEvents := make([]projPoint, 0, chartMonths/3+1)
	for m := 0; m <= chartMonths; m += 3 {
		date := addMonthsFirstOfMonth(now, m).Format("2006-01")
		baseline = append(baseline, projPoint{Date: date, Value: math.Round(baseVals[m])})
		withEvents = append(withEvents, projPoint{Date: date, Value: math.Round(evVals[m])})
	}

	// Independence shift against the FIRE calculator's target.
	fireKnown := req.Expenses > 0
	baselineFIRE, eventsFIRE, fireShift := -1, -1, 0
	fireTarget := 0.0
	if fireKnown {
		fp := fireParams{
			CurrentValue: currentValue,
			AnnualReturn: annualReturn / 100,
			Inflation:    analytics.PlanningDefaultInflationPct / 100,
			Expenses:     req.Expenses,
			Pension:      math.Max(0, req.Pension),
			CurrentAge:   req.CurrentAge,
			PensionAge:   req.PensionAge,
			SWR:          0.035,
			TaxRate:      analytics.CapitalIncomeTaxRate(req.ChurchRate),
			TFS:          h.portfolioTFS(ctx, fxRates),
			Allowance:    analytics.SparerPauschbetragForYear(now.Year(), req.Joint),
		}
		if req.SWR > 0 && req.SWR <= 10 {
			fp.SWR = req.SWR / 100
		}
		if req.InflationPct != nil && *req.InflationPct >= -5 && *req.InflationPct <= 20 {
			fp.Inflation = *req.InflationPct / 100
		}
		baselineFIRE = fireMonthOnPath(fp, baseVals, baseBasis)
		eventsFIRE = fireMonthOnPath(fp, evVals, evBasis)
		if baselineFIRE >= 0 && eventsFIRE >= 0 {
			fireShift = eventsFIRE - baselineFIRE // positive = delayed
		}
		if baselineFIRE >= 0 {
			t, _ := fp.targetAt(baselineFIRE, baseVals[baselineFIRE], baseBasis[baselineFIRE])
			fireTarget = t / fp.inflator(float64(baselineFIRE))
		}
	}

	// Impact summary
	finalBaseline := 0.0
	finalEvents := 0.0
	if len(baseline) > 0 {
		finalBaseline = baseline[len(baseline)-1].Value
	}
	if len(withEvents) > 0 {
		finalEvents = withEvents[len(withEvents)-1].Value
	}

	// Event templates for the frontend
	type eventTemplate struct {
		Type            string  `json:"type"`
		Label           string  `json:"label"`
		Description     string  `json:"description"`
		DefaultCash     float64 `json:"default_cash_impact"`
		DefaultContrib  float64 `json:"default_contrib_change"`
		DefaultRecur    float64 `json:"default_recurring_cost"`
		DefaultDuration int     `json:"default_duration_months"`
		Icon            string  `json:"icon"`
	}
	templates := []eventTemplate{
		{Type: "home_purchase", Label: "Home Purchase", Description: "Down payment + mortgage", DefaultCash: -80000, DefaultContrib: 0, DefaultRecur: 1500, DefaultDuration: 300, Icon: "🏠"},
		{Type: "sabbatical", Label: "Sabbatical", Description: "Career break with reduced income", DefaultCash: 0, DefaultContrib: -2000, DefaultRecur: 500, DefaultDuration: 12, Icon: "🏖️"},
		{Type: "marriage", Label: "Marriage", Description: "Wedding costs + tax benefit", DefaultCash: -15000, DefaultContrib: 0, DefaultRecur: 0, DefaultDuration: 0, Icon: "💍"},
		{Type: "child", Label: "Child", Description: "Kindergeld + childcare costs", DefaultCash: -5000, DefaultContrib: -250, DefaultRecur: 600, DefaultDuration: 216, Icon: "👶"},
		{Type: "job_change", Label: "Job Change", Description: "Salary increase or decrease", DefaultCash: 0, DefaultContrib: 500, DefaultRecur: 0, DefaultDuration: 0, Icon: "💼"},
		{Type: "inheritance", Label: "Inheritance", Description: "Lump-sum windfall", DefaultCash: 100000, DefaultContrib: 0, DefaultRecur: 0, DefaultDuration: 0, Icon: "🏛️"},
		{Type: "early_retirement", Label: "Early Retirement", Description: "Stop working, begin drawdown", DefaultCash: 0, DefaultContrib: -999999, DefaultRecur: 3000, DefaultDuration: 0, Icon: "🌅"},
	}

	// --- Execution Plan ---
	// For cash-need events: FIFO-respecting, tax-minimised liquidation order
	// For windfall events: lump-sum vs DCA comparison
	type executionPlan struct {
		Type          string            `json:"type"` // "liquidation" or "invest"
		EventLabel    string            `json:"event_label"`
		CashNeeded    float64           `json:"cash_needed,omitempty"`
		SellOrder     []liquidationStep `json:"sell_order,omitempty"`
		TotalTax      float64           `json:"total_tax"`
		TotalProceeds float64           `json:"total_proceeds"`
		// For windfall invest comparison
		WindfallAmount float64 `json:"windfall_amount,omitempty"`
		LumpSum10Y     float64 `json:"lump_sum_10y,omitempty"`
		DCA10Y         float64 `json:"dca_10y,omitempty"`
		LumpSumEdge    float64 `json:"lump_sum_edge_pct,omitempty"`
	}
	var plans []executionPlan

	// Load tax lots for liquidation planning
	secs, _ := h.queries.ListSecurities(ctx)
	assetClass := make(map[string]string, len(secs))
	for _, s := range secs {
		assetClass[s.ISIN] = s.AssetClass
	}
	taxTxns, names, _, equityMap := buildTaxTransactions(txns, secs, histFX, fxRates)
	taxTxns = dropPensionWrapperTxns(ctx, h.queries, taxTxns)
	holdings, _ := h.queries.ListCurrentHoldings(ctx)
	pm := h.loadPriceMap(ctx)
	lotPrices := make(map[string]float64)
	for _, hld := range holdings {
		qty := numericToFloat(hld.Quantity)
		p := convertAmountToEUR(numericToFloat(pm[hld.SecurityISIN].Close), pm[hld.SecurityISIN].Currency, fxRates)
		if qty > 0 && p > 0 {
			lotPrices[hld.SecurityISIN] = p
		}
	}
	allLots := analytics.ComputeTaxLots(taxTxns, lotPrices, names, equityMap)
	capRate := analytics.CapitalIncomeTaxRate(req.ChurchRate)

	for _, ev := range req.Events {
		if ev.CashImpact < -1000 {
			cashNeeded := math.Abs(ev.CashImpact)
			plan := planLiquidation(allLots, assetClass, cashNeeded, capRate, now)
			plans = append(plans, executionPlan{
				Type: "liquidation", EventLabel: ev.Label,
				CashNeeded: cashNeeded, SellOrder: plan.Steps,
				TotalTax: math.Round(plan.TotalTax), TotalProceeds: math.Round(plan.TotalProceeds),
			})
		} else if ev.CashImpact > 1000 {
			// Windfall event: lump-sum vs DCA comparison
			amount := ev.CashImpact
			mr := planMonthlyRate(annualReturn)
			months := 120 // 10 year comparison

			// Lump sum: invest all immediately
			lumpSum := amount
			for m := 0; m < months; m++ {
				lumpSum *= (1 + mr)
			}

			// DCA: spread over 12 months
			dca := 0.0
			monthly := amount / 12
			for m := 0; m < months; m++ {
				if m < 12 {
					dca += monthly // add this month's portion
				}
				dca *= (1 + mr) // grow everything invested so far
			}

			edge := 0.0
			if dca > 0 {
				edge = (lumpSum - dca) / dca * 100
			}

			plans = append(plans, executionPlan{
				Type: "invest", EventLabel: ev.Label,
				WindfallAmount: amount,
				LumpSum10Y:     math.Round(lumpSum), DCA10Y: math.Round(dca),
				LumpSumEdge: math.Round(edge*10) / 10,
			})
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"baseline":               baseline,
		"with_events":            withEvents,
		"current_value":          math.Round(currentValue),
		"final_baseline":         finalBaseline,
		"final_with_events":      finalEvents,
		"net_impact":             math.Round(finalEvents - finalBaseline),
		"fire_shift_months":      fireShift,
		"fire_target_known":      fireKnown,
		"fire_baseline_month":    baselineFIRE,
		"fire_with_events_month": eventsFIRE,
		"fire_target":            math.Round(fireTarget),
		"scope":                  "investable",
		"templates":              templates,
		"contribution":           monthlyContrib,
		"return_pct":             annualReturn,
		"execution_plans":        plans,
	})
}

// HandlePensionGap computes the Rentenlücke (pension gap) from configured sources.
// POST body: { sources: [...], retirement_age: 67, current_age: 35,
// monthly_need: 3000, monthly_contrib: 0, return_pct: 7, swr: 3.5,
// inflation_pct: 2, children: 1, church_rate: 0, joint: false }.
// Every amount in the response is monthly and in today's euros: the portfolio
// is projected nominally and deflated, pensions and the need are entered in
// today's money and taxed with the 2026 tariff (assumed to rise with prices).
func (h *PortfolioHandler) HandlePensionGap(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req struct {
		Sources        []pensionSourceInput `json:"sources"`
		RetirementAge  int                  `json:"retirement_age"`
		MonthlyNeed    float64              `json:"monthly_need"` // desired monthly net income in retirement
		CurrentAge     int                  `json:"current_age"`
		MonthlyContrib float64              `json:"monthly_contrib"` // > 0 overrides the trailing net contribution
		ReturnPct      *float64             `json:"return_pct"`      // nominal; default 7
		SWR            float64              `json:"swr"`             // %; default 3.5
		InflationPct   *float64             `json:"inflation_pct"`   // default 2
		Children       *int                 `json:"children"`        // 0 = childless (higher Pflege rate)
		ChurchRate     float64              `json:"church_rate"`     // 0, 0.08 or 0.09
		Joint          bool                 `json:"joint"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request: "+err.Error())
		return
	}
	isFinite := func(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }
	if !isFinite(req.MonthlyNeed) || !isFinite(req.MonthlyContrib) || !isFinite(req.SWR) || !isFinite(req.ChurchRate) ||
		(req.ReturnPct != nil && !isFinite(*req.ReturnPct)) || (req.InflationPct != nil && !isFinite(*req.InflationPct)) {
		writeError(w, http.StatusBadRequest, "monthly_need, monthly_contrib and the rates must be finite numbers")
		return
	}
	for _, src := range req.Sources {
		if !isFinite(src.MonthlyGross) || !isFinite(src.Rentenpunkte) || !isFinite(src.TaxPortion) {
			writeError(w, http.StatusBadRequest, "source amounts must be finite numbers")
			return
		}
	}

	if req.RetirementAge <= 0 {
		req.RetirementAge = analytics.Regelaltersgrenze
	}
	if req.CurrentAge <= 0 {
		req.CurrentAge = 35
	}
	if req.MonthlyNeed <= 0 {
		req.MonthlyNeed = 3000
	}
	annualReturn := analytics.PlanningDefaultReturnPct / 100
	if req.ReturnPct != nil && *req.ReturnPct >= -50 && *req.ReturnPct <= 30 {
		annualReturn = *req.ReturnPct / 100
	}
	swr := 0.035
	if req.SWR > 0 && req.SWR <= 10 {
		swr = req.SWR / 100
	}
	inflation := analytics.PlanningDefaultInflationPct / 100
	if req.InflationPct != nil && *req.InflationPct >= -5 && *req.InflationPct <= 20 {
		inflation = *req.InflationPct / 100
	}
	churchRate := math.Min(0.09, math.Max(0, req.ChurchRate))
	now := time.Now()
	pc := pensionContext{
		CurrentAge: req.CurrentAge,
		NowYear:    now.Year(),
		Joint:      req.Joint,
		ChurchRate: churchRate,
		Childless:  req.Children != nil && *req.Children == 0,
	}

	// Pensions paid at the retirement age, and once every source has started.
	results, totalMonthlyGross, totalMonthlyNet := pensionsAt(req.Sources, req.RetirementAge, req.RetirementAge, false, pc)
	_, _, netAllStarted := pensionsAt(req.Sources, req.RetirementAge, 200, false, pc)

	// Portfolio withdrawal component
	currentValue := 0.0
	if snaps, _ := h.queries.ListNetWorthSnapshots(ctx, 1); len(snaps) > 0 {
		currentValue = investableNetWorth(snaps[0])
	}
	fxRates := loadFXRatesMap(ctx, h.queries)
	monthlyContrib := req.MonthlyContrib // user override
	if monthlyContrib <= 0 {
		histFX := loadHistoricalFXRates(ctx, h.queries)
		txns, _ := listAllTransactionsRows(ctx, h.queries)
		monthlyContrib = trailingNetContribution(txns, h.accountTypesByID(ctx), now, histFX, fxRates, h.physicalHoldings(ctx)).Monthly
	}
	taxRate := analytics.CapitalIncomeTaxRate(churchRate)
	tfs := h.portfolioTFS(ctx, fxRates)
	allowance := analytics.SparerPauschbetragForYear(now.Year(), req.Joint)
	monthsTo := func(age int) int { return int(math.Max(0, float64(age-req.CurrentAge))) * 12 }

	draw := portfolioAtRetirement(currentValue, monthlyContrib, annualReturn, inflation, swr, taxRate, tfs, allowance, monthsTo(req.RetirementAge))

	// Gap calculation
	gap := req.MonthlyNeed - totalMonthlyNet - draw.NetMonthly
	gapAll := req.MonthlyNeed - netAllStarted - draw.NetMonthly

	// Extra monthly saving (after tax on its own gains) that closes the gap.
	sparplanMonthly := extraSavingsToClose(gap, annualReturn, inflation, swr, taxRate, tfs, monthsTo(req.RetirementAge))

	// Retirement age sensitivity. The statutory pension is claimed at the
	// retirement age with the § 77 SGB VI factor, but never before 63; other
	// sources pay from their own start age.
	type ageSensitivity struct {
		Age          int     `json:"age"`
		PensionNet   float64 `json:"pension_net"`
		PortfolioNet float64 `json:"portfolio_net"`
		TotalNet     float64 `json:"total_net"`
		Gap          float64 `json:"gap"`
	}
	sensitivity := make([]ageSensitivity, 0)
	for age := 55; age <= 70; age++ {
		if age < req.CurrentAge {
			continue
		}
		_, _, pNet := pensionsAt(req.Sources, age, age, true, pc)
		d := portfolioAtRetirement(currentValue, monthlyContrib, annualReturn, inflation, swr, taxRate, tfs, allowance, monthsTo(age))
		total := pNet + d.NetMonthly
		sensitivity = append(sensitivity, ageSensitivity{
			Age: age, PensionNet: math.Round(pNet),
			PortfolioNet: math.Round(d.NetMonthly), TotalNet: math.Round(total),
			Gap: math.Round(req.MonthlyNeed - total),
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"sources":                     results,
		"total_monthly_gross":         math.Round(totalMonthlyGross*100) / 100,
		"total_monthly_net":           math.Round(totalMonthlyNet*100) / 100,
		"portfolio_monthly":           math.Round(draw.GrossMonthly*100) / 100,
		"portfolio_net":               math.Round(draw.NetMonthly*100) / 100,
		"projected_portfolio":         math.Round(draw.ValueReal),
		"projected_portfolio_nominal": math.Round(draw.ValueNominal),
		"monthly_contrib":             math.Round(monthlyContrib),
		"monthly_need":                req.MonthlyNeed,
		"gap":                         math.Round(gap*100) / 100,
		"gap_annual":                  math.Round(gap * 12),
		"gap_after_all_pensions":      math.Round(gapAll*100) / 100,
		"sparplan_to_close":           math.Round(sparplanMonthly),
		"retirement_age":              req.RetirementAge,
		"current_age":                 req.CurrentAge,
		"sensitivity":                 sensitivity,
		"rentenwert":                  analytics.AktuellerRentenwert,
		"return_pct":                  math.Round(annualReturn*10000) / 100,
		"swr_pct":                     math.Round(swr*10000) / 100,
		"inflation_pct":               math.Round(inflation*10000) / 100,
		"teilfreistellung_pct":        math.Round(tfs*1000) / 10,
		"in_todays_euros":             true,
	})
}

// HandleSparplanStreak computes Sparplan consistency metrics.
func (h *PortfolioHandler) HandleSparplanStreak(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	txns, err := listAllTransactionsRows(ctx, h.queries)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list transactions: "+err.Error())
		return
	}
	fxRates := loadFXRatesMap(ctx, h.queries)
	histFX := loadHistoricalFXRates(ctx, h.queries)

	st := computeSparplanStreak(sparplanMonthlyAmounts(txns, histFX, fxRates), time.Now().UTC())
	if st.TotalMonths == 0 {
		writeJSON(w, http.StatusOK, map[string]any{
			"current_streak":  0,
			"longest_streak":  0,
			"total_months":    0,
			"consistency_pct": 0,
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"current_streak":     st.CurrentStreak,
		"longest_streak":     st.LongestStreak,
		"total_months":       st.TotalMonths,
		"active_months":      st.ActiveMonths,
		"missed_months":      st.MissedMonths,
		"consistency_pct":    math.Round(st.ConsistencyPct*10) / 10,
		"avg_monthly":        math.Round(st.AvgMonthly*100) / 100,
		"missed_cost":        math.Round(st.MissedCost),
		"assumed_return_pct": sparplanAssumedReturn * 100,
		"total_invested":     math.Round(st.TotalInvested*100) / 100,
		"first_month":        st.FirstMonth,
		"last_month":         st.LastMonth,
		"history":            st.History,
	})
}

// sparplanAssumedReturn is the annual return used to estimate what missed
// Sparplan months would have earned. It is an assumption, shown as such.
const sparplanAssumedReturn = 0.07

// sparplanMonthlyAmounts sums savings-plan executions per month (YYYY-MM) in
// EUR. One-off buys are not part of a savings plan and are not counted.
func sparplanMonthlyAmounts(txns []db.ListTransactionsRow, histFX []market.HistoricalFXRate, fxRates map[string]float64) map[string]float64 {
	monthly := make(map[string]float64)
	for _, txn := range txns {
		if txn.Type != "savings_plan" {
			continue
		}
		amt := math.Abs(convertAmountToEURAtDate(numericToFloat(txn.Amount), txn.Currency, txn.Date, histFX, fxRates))
		monthly[txn.Date.Format("2006-01")] += amt
	}
	return monthly
}

type sparplanMonth struct {
	Month  string  `json:"month"`
	Amount float64 `json:"amount"`
	Active bool    `json:"active"`
}

type sparplanStreak struct {
	CurrentStreak, LongestStreak            int
	TotalMonths, ActiveMonths, MissedMonths int
	ConsistencyPct                          float64
	AvgMonthly, MissedCost, TotalInvested   float64
	FirstMonth, LastMonth                   string
	History                                 []sparplanMonth
}

// computeSparplanStreak measures a savings plan from its first execution up
// to today: the current month counts once it has an execution, otherwise the
// range ends with the last complete month, so a plan that stopped shows its
// missed months and a current streak of 0. AvgMonthly is the average
// execution over the active months of the last 12 months of the range (the
// whole history if none fall in it), and MissedCost is what that amount
// would have earned at sparplanAssumedReturn from each missed month to now.
func computeSparplanStreak(monthly map[string]float64, now time.Time) sparplanStreak {
	var st sparplanStreak
	var months []string
	for m, amt := range monthly {
		if amt > 0 {
			months = append(months, m)
		}
	}
	if len(months) == 0 {
		return st
	}
	sort.Strings(months)

	firstMonth, _ := time.Parse("2006-01", months[0])
	curMonth := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	endMonth := addMonthsFirstOfMonth(curMonth, -1)
	if monthly[curMonth.Format("2006-01")] > 0 || endMonth.Before(firstMonth) {
		endMonth = curMonth
	}
	if lastExec, _ := time.Parse("2006-01", months[len(months)-1]); lastExec.After(endMonth) {
		endMonth = lastExec // executions dated in the future
	}

	var allMonths []string
	for m := firstMonth; !m.After(endMonth); m = addMonthsFirstOfMonth(m, 1) {
		allMonths = append(allMonths, m.Format("2006-01"))
	}

	streak := 0
	for _, m := range allMonths {
		amt := monthly[m]
		if amt > 0 {
			streak++
			st.ActiveMonths++
			st.TotalInvested += amt
			if streak > st.LongestStreak {
				st.LongestStreak = streak
			}
		} else {
			streak = 0
		}
		st.History = append(st.History, sparplanMonth{Month: m, Amount: math.Round(amt*100) / 100, Active: amt > 0})
	}
	st.CurrentStreak = streak
	st.TotalMonths = len(allMonths)
	st.MissedMonths = st.TotalMonths - st.ActiveMonths
	st.ConsistencyPct = float64(st.ActiveMonths) / float64(st.TotalMonths) * 100
	st.FirstMonth, st.LastMonth = allMonths[0], allMonths[len(allMonths)-1]

	recent := allMonths
	if len(recent) > 12 {
		recent = recent[len(recent)-12:]
	}
	sum, n := 0.0, 0
	for _, m := range recent {
		if monthly[m] > 0 {
			sum += monthly[m]
			n++
		}
	}
	if n == 0 && st.ActiveMonths > 0 {
		sum, n = st.TotalInvested, st.ActiveMonths
	}
	if n > 0 {
		st.AvgMonthly = sum / float64(n)
	}

	for _, m := range allMonths {
		if monthly[m] > 0 {
			continue
		}
		missed, _ := time.Parse("2006-01", m)
		years := now.Sub(missed).Hours() / 24 / 365.25
		if years > 0 {
			st.MissedCost += st.AvgMonthly * (math.Pow(1+sparplanAssumedReturn, years) - 1)
		}
	}
	return st
}

// HandleWealthWaterfall computes a waterfall decomposition of current net worth.
func (h *PortfolioHandler) HandleWealthWaterfall(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	txns, err := listAllTransactionsRows(ctx, h.queries)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list transactions: "+err.Error())
		return
	}
	fxRates := loadFXRatesMap(ctx, h.queries)
	histFX := loadHistoricalFXRates(ctx, h.queries)
	accts, _ := h.queries.ListAccounts(ctx)

	// Wash broker-to-broker in-kind pairs at portfolio scope so they don't
	// inflate contributions. Standalone in-kind transfers (e.g. RSU vests)
	// stay as Contribution and are valued at FMV.
	var inkind []analytics.InKindTransfer
	for _, t := range txns {
		if !t.SecurityISIN.Valid {
			continue
		}
		if t.Type != "transfer" && t.Type != "transfer_out" {
			continue
		}
		qty := 0.0
		if t.Quantity.Valid {
			f, _ := t.Quantity.Float64Value()
			qty = f.Float64
		}
		inkind = append(inkind, analytics.InKindTransfer{
			ID:       t.ID,
			Date:     t.Date,
			Type:     t.Type,
			ISIN:     t.SecurityISIN.String,
			Quantity: qty,
		})
	}
	washed := analytics.MatchInKindTransferPairs(inkind)
	physical := h.loadPhysicalAttribution(ctx, txns, histFX, fxRates)

	txnsChrono := make([]db.ListTransactionsRow, len(txns))
	for i := range txns {
		txnsChrono[i] = txns[len(txns)-1-i]
	}
	impEvents := computeImplicitCashClampEvents(accts, txnsChrono, histFX, fxRates)

	// Aggregate transaction components (in EUR)
	deposits := 0.0
	withdrawals := 0.0
	dividends := 0.0
	interest := 0.0
	fees := 0.0
	taxes := 0.0

	for _, ev := range impEvents {
		if ev.DeltaEUR >= 0 {
			deposits += ev.DeltaEUR
		} else {
			withdrawals += -ev.DeltaEUR
		}
	}

	for _, txn := range txns {
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
		tax := 0.0
		if txn.Tax.Valid {
			f, _ := txn.Tax.Float64Value()
			tax = convertAmountToEURAtDate(f.Float64, txn.Currency, txn.Date, histFX, fxRates)
		}
		fees += fee
		taxes += tax

		if _, isWash := washed[txn.ID]; isWash {
			continue
		}
		if physical.linked[txn.ID] {
			continue // cash → car swap; the car's value change is its own bar
		}
		switch analytics.ClassifyForAttribution(txn.Type, txn.SecurityISIN.Valid, analytics.ScopePortfolio) {
		case analytics.BucketContribution:
			deposits += amt
		case analytics.BucketWithdrawal:
			withdrawals += amt
		case analytics.BucketDividend:
			dividends += amt
		case analytics.BucketInterest:
			interest += amt
		case analytics.BucketFee:
			fees += amt
		case analytics.BucketTax:
			taxes += amt
		}
	}

	// Get current net worth (used for the final waterfall bar only — the
	// market-return bar is now derived from explicit holdings price-deltas).
	snaps, _ := h.queries.ListNetWorthSnapshots(ctx, 1)
	currentNW := 0.0
	currentPhysical := 0.0
	if len(snaps) > 0 && snaps[0].Total.Valid {
		f, _ := snaps[0].Total.Float64Value()
		currentNW = f.Float64
		currentPhysical = numericToFloat(snaps[0].PhysicalComponent)
	}

	// Net contributions = deposits - withdrawals, adjusted for physical
	// assets and loans (see physicalAttribution).
	pt := physical.window(time.Time{}, assets.CivilDate(time.Now()))
	netContributions := deposits - withdrawals + pt.contrib
	// Physical assets: modelled value minus what was paid (price and
	// improvements), i.e. accumulated depreciation or appreciation.
	assetValueChange := currentPhysical - pt.capital
	purchaseCosts := pt.costs
	loanInterest := pt.interest

	// Build monthly NW + classifier-based contribution / dividend / interest
	// maps. NW is the LATEST snapshot inside each month (ListNetWorthSnapshots
	// returns DESC date order, so the first hit per month is end-of-month).
	// Using max here previously caused the cumulative time-series to ratchet
	// upward on intra-month peaks, breaking the reconciliation identity.
	allSnaps, _ := h.queries.ListNetWorthSnapshots(ctx, maxNetWorthSnapshotRows)
	monthNW := make(map[string]float64)
	monthPhysical := make(map[string]float64)
	for _, s := range allSnaps {
		m := s.Date.Format("2006-01")
		val := 0.0
		if s.Total.Valid {
			f, _ := s.Total.Float64Value()
			val = f.Float64
		}
		if _, exists := monthNW[m]; !exists {
			monthNW[m] = val
			monthPhysical[m] = numericToFloat(s.PhysicalComponent)
		}
	}

	monthContrib := make(map[string]float64)
	monthDiv := make(map[string]float64)
	monthInt := make(map[string]float64)
	// Fees and taxes per month, built exactly like the totals above so the
	// monthly market residual has the same definition as market_returns.
	monthFees := make(map[string]float64)
	monthTaxes := make(map[string]float64)
	monthPhysicalEvents := physical.monthly()
	for m, t := range monthPhysicalEvents {
		monthContrib[m] += t.contrib
	}
	for _, ev := range impEvents {
		monthContrib[ev.Date.Format("2006-01")] += ev.DeltaEUR
	}
	for _, txn := range txns {
		m := txn.Date.Format("2006-01")
		monthFees[m] += convertAmountToEURAtDate(numericToFloat(txn.Fee), txn.Currency, txn.Date, histFX, fxRates)
		monthTaxes[m] += convertAmountToEURAtDate(numericToFloat(txn.Tax), txn.Currency, txn.Date, histFX, fxRates)
		if _, isWash := washed[txn.ID]; isWash {
			continue
		}
		if physical.linked[txn.ID] {
			continue
		}
		amt := 0.0
		if txn.Amount.Valid {
			f, _ := txn.Amount.Float64Value()
			amt = convertAmountToEURAtDate(f.Float64, txn.Currency, txn.Date, histFX, fxRates)
		}
		switch analytics.ClassifyForAttribution(txn.Type, txn.SecurityISIN.Valid, analytics.ScopePortfolio) {
		case analytics.BucketContribution:
			monthContrib[m] += amt
		case analytics.BucketWithdrawal:
			monthContrib[m] -= amt
		case analytics.BucketDividend:
			monthDiv[m] += amt
		case analytics.BucketInterest:
			monthInt[m] += amt
		case analytics.BucketFee:
			monthFees[m] += amt
		case analytics.BucketTax:
			monthTaxes[m] += amt
		}
	}

	monthSet := make(map[string]bool)
	for _, set := range []map[string]float64{monthNW, monthContrib, monthDiv, monthInt, monthFees, monthTaxes} {
		for m := range set {
			monthSet[m] = true
		}
	}
	var months []string
	for m := range monthSet {
		months = append(months, m)
	}
	sort.Strings(months)

	// Market returns are derived as the algebraic residual that closes the
	// identity `NW = contributions + market + div + int - fees - taxes`. The
	// prior explicit per-holding price-delta approach is sound when every
	// ISIN has continuous price history back to its first purchase, but in
	// practice early months frequently have missing price history (a freshly-
	// purchased ISIN's `priceAt` returned 0, which produced phantom negative
	// market returns mirroring the contribution amount). The residual is
	// robust by construction: the time-series stacked area always sums to the
	// actual NW snapshot at month-end.
	marketReturns := currentNW - netContributions - dividends - interest + math.Abs(fees) + math.Abs(taxes) -
		assetValueChange + purchaseCosts + loanInterest

	// Waterfall bars
	type waterfallBar struct {
		Label string  `json:"label"`
		Value float64 `json:"value"`
		Type  string  `json:"type"` // positive, negative, total
	}
	mrType := "positive"
	if marketReturns < 0 {
		mrType = "negative"
	}
	feeType := "negative"
	taxType := "negative"
	feeVal := -math.Abs(fees)
	taxVal := -math.Abs(taxes)
	waterfall := []waterfallBar{
		{"Contributions", math.Round(netContributions), "positive"},
		{"Market Returns", math.Round(marketReturns), mrType},
	}
	if physical.hasAssets() {
		vType := "negative"
		if assetValueChange >= 0 {
			vType = "positive"
		}
		waterfall = append(waterfall, waterfallBar{"Physical Assets", math.Round(assetValueChange), vType})
		if purchaseCosts > 0 {
			waterfall = append(waterfall, waterfallBar{"Purchase Costs", -math.Round(purchaseCosts), "negative"})
		}
	}
	if physical.hasLoans() {
		waterfall = append(waterfall, waterfallBar{"Loan Interest", -math.Round(loanInterest), "negative"})
	}
	waterfall = append(waterfall,
		waterfallBar{"Dividends", math.Round(dividends), "positive"},
		waterfallBar{"Interest", math.Round(interest), "positive"},
		waterfallBar{"Fees", math.Round(feeVal), feeType},
		waterfallBar{"Taxes", math.Round(taxVal), taxType},
		waterfallBar{"Net Worth", math.Round(currentNW), "total"},
	)

	// Attribution over time — monthly stacked area
	type monthAttrib struct {
		Month         string  `json:"month"`
		Contributions float64 `json:"contributions"`
		MarketReturn  float64 `json:"market_return"`
		Dividends     float64 `json:"dividends"`
		Interest      float64 `json:"interest"`
		Assets        float64 `json:"assets"`         // physical asset value change
		PropertyCosts float64 `json:"property_costs"` // purchase costs and loan interest (negative)
		Fees          float64 `json:"fees"`           // cumulative fees (negative)
		Taxes         float64 `json:"taxes"`          // cumulative taxes (negative)
		Total         float64 `json:"total"`
	}

	timeSeries := make([]monthAttrib, 0)
	cumContrib := 0.0
	cumDiv := 0.0
	cumInt := 0.0
	cumCapital := 0.0
	cumCosts := 0.0
	cumFees := 0.0
	cumTaxes := 0.0
	crossoverMonth := ""
	for _, m := range months {
		cumContrib += monthContrib[m]
		cumDiv += monthDiv[m]
		cumInt += monthInt[m]
		cumCapital += monthPhysicalEvents[m].capital
		cumCosts += monthPhysicalEvents[m].costs + monthPhysicalEvents[m].interest
		cumFees += monthFees[m]
		cumTaxes += monthTaxes[m]
		nw := monthNW[m]
		if nw == 0 {
			continue
		}
		cumAssets := monthPhysical[m] - cumCapital
		// Market return is the residual that closes the identity, with fees
		// and taxes taken out as in market_returns above, so a period's market
		// return (the difference of two months) means the same as the
		// all-time figure. It still holds FX moves on foreign cash. This
		// guarantees the stacked area sums to the actual NW snapshot, instead
		// of drifting due to missing price data.
		cumMR := nw - cumContrib - cumDiv - cumInt - cumAssets + cumCosts + math.Abs(cumFees) + math.Abs(cumTaxes)
		timeSeries = append(timeSeries, monthAttrib{
			Month: m, Contributions: math.Round(cumContrib),
			MarketReturn: math.Round(cumMR), Dividends: math.Round(cumDiv),
			Interest: math.Round(cumInt), Assets: math.Round(cumAssets),
			PropertyCosts: -math.Round(cumCosts), Total: math.Round(nw),
			Fees: -math.Round(math.Abs(cumFees)), Taxes: -math.Round(math.Abs(cumTaxes)),
		})
		// Crossover: cumulative returns surpass cumulative contributions
		// (gate on >10k contributed to avoid early-month artifacts).
		if crossoverMonth == "" && cumMR > cumContrib && cumContrib > 10000 {
			crossoverMonth = m
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"waterfall":       waterfall,
		"time_series":     timeSeries,
		"crossover_month": crossoverMonth,
		"net_contributions": math.Round(netContributions),
		"market_returns":   math.Round(marketReturns),
		"dividends":        math.Round(dividends),
		"interest":         math.Round(interest),
		"fees":             math.Round(fees),
		"taxes":            math.Round(taxes),
		"asset_value_change": math.Round(assetValueChange),
		"purchase_costs":     -math.Round(purchaseCosts),
		"loan_interest":      -math.Round(loanInterest),
		"current_nw":       math.Round(currentNW),
		// Reconciliation: the identity is NW = contrib + market + div + int -
		// fees - taxes. expected_nw is what the components sum to; the gap is
		// the residual between that and the snapshot total. A non-zero gap
		// usually means a stale snapshot, an FX swing on idle cash, or a fee/
		// tax-at-source the classifier doesn't see — surfaced here so the
		// frontend (or QA) can flag drift that exceeds the EUR 1 tolerance.
		"expected_nw":        math.Round(netContributions + marketReturns + assetValueChange - purchaseCosts - loanInterest + dividends + interest - math.Abs(fees) - math.Abs(taxes)),
		"reconciliation_gap": math.Round(currentNW - (netContributions + marketReturns + assetValueChange - purchaseCosts - loanInterest + dividends + interest - math.Abs(fees) - math.Abs(taxes))),
	})
}

// HandleTimeMachine reconstructs portfolio state at a historical date.
func (h *PortfolioHandler) HandleTimeMachine(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	dateStr := r.URL.Query().Get("date")
	if dateStr == "" {
		writeError(w, http.StatusBadRequest, "date parameter required (YYYY-MM-DD)")
		return
	}
	targetDate, err := time.Parse("2006-01-02", dateStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid date format, use YYYY-MM-DD")
		return
	}

	// Get all transactions without row cap to reconstruct holdings accurately
	txns, err := listAllTransactionsRows(ctx, h.queries)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list transactions: "+err.Error())
		return
	}
	fxRates := loadFXRatesMap(ctx, h.queries)
	histFX := loadHistoricalFXRates(ctx, h.queries)
	accts, _ := h.queries.ListAccounts(ctx)
	secs, _ := h.queries.ListSecurities(ctx)
	secMap := make(map[string]db.Security)
	for _, s := range secs {
		secMap[s.ISIN] = s
	}

	txnsChrono := make([]db.ListTransactionsRow, len(txns))
	for i := range txns {
		txnsChrono[i] = txns[len(txns)-1-i]
	}
	impEvents := computeImplicitCashClampEvents(accts, txnsChrono, histFX, fxRates)

	// Reconstruct holdings at target date
	type holding struct {
		isin      string
		qty       float64
		costBasis float64
	}
	holdings := make(map[string]*holding)
	totalDeposits := 0.0
	totalWithdrawals := 0.0
	totalDividends := 0.0
	totalInterest := 0.0

	for _, ev := range impEvents {
		if !ev.Date.After(targetDate) {
			if ev.DeltaEUR >= 0 {
				totalDeposits += ev.DeltaEUR
			} else {
				totalWithdrawals += -ev.DeltaEUR
			}
		}
	}

	// Process transactions chronologically (txns are newest-first)
	for i := len(txns) - 1; i >= 0; i-- {
		txn := txns[i]
		if txn.Date.After(targetDate) {
			continue
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

		// Holdings reconstruction uses the raw qty switch (buys add, sells
		// remove). For the cash-side attribution totals we route through the
		// classifier so internal cash_transfer_* rows don't double-count at
		// portfolio scope.
		switch txn.Type {
		case "buy", "savings_plan", "transfer":
			if isin != "" && qty > 0 {
				hld, ok := holdings[isin]
				if !ok {
					hld = &holding{isin: isin}
					holdings[isin] = hld
				}
				hld.qty += qty
				hld.costBasis += amt
			}
		case "sell", "transfer_out":
			if isin != "" {
				hld := holdings[isin]
				if hld != nil && hld.qty > 0 {
					avg := hld.costBasis / hld.qty
					sold := math.Min(qty, hld.qty)
					hld.qty -= sold
					hld.costBasis -= sold * avg
				}
			}
		}
		switch analytics.ClassifyForAttribution(txn.Type, txn.SecurityISIN.Valid, analytics.ScopePortfolio) {
		case analytics.BucketContribution:
			if !txn.SecurityISIN.Valid {
				// Cash deposit. In-kind contributions (RSU vests) already
				// move qty + cost via the holdings switch above.
				totalDeposits += amt
			}
		case analytics.BucketWithdrawal:
			totalWithdrawals += amt
		case analytics.BucketDividend:
			totalDividends += amt
		case analytics.BucketInterest:
			totalInterest += amt
		}
	}

	// Get prices closest to the target date
	type holdingSnapshot struct {
		ISIN       string  `json:"isin"`
		Name       string  `json:"name"`
		Quantity   float64 `json:"quantity"`
		CostBasis  float64 `json:"cost_basis"`
		Price      float64 `json:"price"`
		Value      float64 `json:"value"`
		Weight     float64 `json:"weight_pct"`
		AssetClass string  `json:"asset_class"`
	}
	var snapHoldings []holdingSnapshot
	totalValue := 0.0

	for isin, hld := range holdings {
		if hld.qty <= 1e-9 {
			continue
		}
		// Get price closest to target date
		priceRows, _ := h.queries.ListPriceHistory(ctx, isin)
		price := hld.costBasis / math.Max(hld.qty, 1e-9) // fallback to avg cost
		for _, pr := range priceRows {
			if !pr.Date.After(targetDate) && pr.Close.Valid {
				f, _ := pr.Close.Float64Value()
				price = convertAmountToEURAtDate(f.Float64, pr.Currency, pr.Date, histFX, fxRates)
			}
		}
		value := hld.qty * price
		totalValue += value
		sec := secMap[isin]
		name := sec.Name
		if name == "" {
			name = isin
		}
		snapHoldings = append(snapHoldings, holdingSnapshot{
			ISIN: isin, Name: name, Quantity: math.Round(hld.qty*1e8) / 1e8,
			CostBasis: math.Round(hld.costBasis*100) / 100, Price: math.Round(price*100) / 100,
			Value: math.Round(value*100) / 100, AssetClass: sec.AssetClass,
		})
	}
	// Compute weights
	for i := range snapHoldings {
		if totalValue > 0 {
			snapHoldings[i].Weight = math.Round(snapHoldings[i].Value/totalValue*1000) / 10
		}
	}
	sort.Slice(snapHoldings, func(i, j int) bool {
		return snapHoldings[i].Value > snapHoldings[j].Value
	})

	// Get net worth snapshot closest to target date
	allSnaps, _ := h.queries.ListNetWorthSnapshots(ctx, 5000)
	historicalNW := totalValue
	historicalPhysical := 0.0
	for _, s := range allSnaps {
		if !s.Date.After(targetDate) && s.Total.Valid {
			f, _ := s.Total.Float64Value()
			historicalNW = f.Float64
			historicalPhysical = numericToFloat(s.PhysicalComponent)
			break // newest first, so first match is closest
		}
	}

	// Current state for comparison
	currentHoldings, _ := h.queries.ListCurrentHoldings(ctx)
	pm := h.loadPriceMap(ctx)
	currentValue := 0.0
	for _, ch := range currentHoldings {
		qty := numericToFloat(ch.Quantity)
		if qty > 0 {
			currentValue += holdingValueEURFromMap(ch, pm, fxRates)
		}
	}
	currentSnaps, _ := h.queries.ListNetWorthSnapshots(ctx, 1)
	currentNW := currentValue
	currentPhysical := 0.0
	if len(currentSnaps) > 0 && currentSnaps[0].Total.Valid {
		f, _ := currentSnaps[0].Total.Float64Value()
		currentNW = f.Float64
		currentPhysical = numericToFloat(currentSnaps[0].PhysicalComponent)
	}

	// Attribution of change (target_date → now) via the same classifier
	// + pair-matcher used in HandleWealthWaterfall. Internal cash_transfer_*
	// rows wash at portfolio scope; broker-to-broker in-kind transfer pairs
	// (same ISIN + qty within +-5 days) wash via MatchInKindTransferPairs.
	var inkind []analytics.InKindTransfer
	for _, t := range txns {
		if !t.SecurityISIN.Valid {
			continue
		}
		if t.Type != "transfer" && t.Type != "transfer_out" {
			continue
		}
		qty := 0.0
		if t.Quantity.Valid {
			f, _ := t.Quantity.Float64Value()
			qty = f.Float64
		}
		inkind = append(inkind, analytics.InKindTransfer{
			ID: t.ID, Date: t.Date, Type: t.Type, ISIN: t.SecurityISIN.String, Quantity: qty,
		})
	}
	washed := analytics.MatchInKindTransferPairs(inkind)

	// Physical assets: linked purchase payments are swaps, unlinked
	// purchases are in-kind contributions, and the rest of the physical
	// component's change is depreciation.
	physical := h.loadPhysicalAttribution(ctx, txns, histFX, fxRates)
	since := physical.window(targetDate, assets.CivilDate(time.Now()))
	assetChangeSince := currentPhysical - historicalPhysical - since.capital

	netContribSince := since.contrib
	for _, ev := range impEvents {
		if ev.Date.After(targetDate) {
			netContribSince += ev.DeltaEUR
		}
	}
	dividendsSince := 0.0
	interestSince := 0.0
	feesSince := 0.0
	taxesSince := 0.0
	for i := len(txns) - 1; i >= 0; i-- {
		txn := txns[i]
		if !txn.Date.After(targetDate) {
			continue
		}
		amt := 0.0
		if txn.Amount.Valid {
			f, _ := txn.Amount.Float64Value()
			amt = convertAmountToEURAtDate(f.Float64, txn.Currency, txn.Date, histFX, fxRates)
		}
		feeAmt := 0.0
		if txn.Fee.Valid {
			f, _ := txn.Fee.Float64Value()
			feeAmt = convertAmountToEURAtDate(f.Float64, txn.Currency, txn.Date, histFX, fxRates)
		}
		taxAmt := 0.0
		if txn.Tax.Valid {
			f, _ := txn.Tax.Float64Value()
			taxAmt = convertAmountToEURAtDate(f.Float64, txn.Currency, txn.Date, histFX, fxRates)
		}
		feesSince += feeAmt
		taxesSince += taxAmt
		if _, isWash := washed[txn.ID]; isWash {
			continue
		}
		if physical.linked[txn.ID] {
			continue
		}
		switch analytics.ClassifyForAttribution(txn.Type, txn.SecurityISIN.Valid, analytics.ScopePortfolio) {
		case analytics.BucketContribution:
			netContribSince += amt
		case analytics.BucketWithdrawal:
			netContribSince -= amt
		case analytics.BucketDividend:
			dividendsSince += amt
		case analytics.BucketInterest:
			interestSince += amt
		case analytics.BucketFee:
			feesSince += amt
		case analytics.BucketTax:
			taxesSince += amt
		}
	}
	nwChange := currentNW - historicalNW
	attr := timeMachineAttribution(timeMachineParts{
		HistoricalNW:  historicalNW,
		CurrentNW:     currentNW,
		Contributions: netContribSince,
		Dividends:     dividendsSince,
		Interest:      interestSince,
		Fees:          feesSince,
		Taxes:         taxesSince,
		Assets:        assetChangeSince,
		PurchaseCosts: since.costs,
		LoanInterest:  since.interest,
	})

	writeJSON(w, http.StatusOK, map[string]any{
		"date":             dateStr,
		"holdings":         snapHoldings,
		"holdings_count":   len(snapHoldings),
		"portfolio_value":  math.Round(totalValue*100) / 100,
		"net_worth":        math.Round(historicalNW),
		"total_cost_basis": math.Round((totalDeposits-totalWithdrawals)*100) / 100,
		"current": map[string]any{
			"net_worth":       math.Round(currentNW),
			"portfolio_value": math.Round(currentValue*100) / 100,
		},
		"change": map[string]any{
			"net_worth_change": attr.NetWorthChange,
			"net_worth_pct":    math.Round(nwChange/math.Max(historicalNW, 1)*10000) / 100,
			"contributions":    attr.Contributions,
			"market_return":    attr.MarketReturn,
			"assets":           attr.Assets,
			"purchase_costs":   attr.PurchaseCosts,
			"loan_interest":    attr.LoanInterest,
			"dividends":        attr.Dividends,
			"interest":         attr.Interest,
			// fees and taxes are magnitudes; they reduce net worth.
			"fees":  attr.Fees,
			"taxes": attr.Taxes,
		},
	})
}

// timeMachineParts are the unrounded attribution components (EUR).
// PurchaseCosts and LoanInterest are positive amounts spent.
type timeMachineParts struct {
	HistoricalNW, CurrentNW                         float64
	Contributions, Dividends, Interest, Fees, Taxes float64
	Assets, PurchaseCosts, LoanInterest             float64
}

// timeMachineRows is the rounded attribution the page lists. The rows add up
// exactly to NetWorthChange:
//
//	contributions + market + dividends + interest − fees − taxes
//	  + assets + purchase_costs + loan_interest = net_worth_change
//
// with fees and taxes as magnitudes and purchase_costs / loan_interest as
// negative amounts. Market return is the residual of the rounded rows, so
// per-row rounding never leaves the list a euro off the headline.
type timeMachineRows struct {
	NetWorthChange, Contributions, MarketReturn, Dividends, Interest float64
	Fees, Taxes, Assets, PurchaseCosts, LoanInterest                 float64
}

func timeMachineAttribution(p timeMachineParts) timeMachineRows {
	r := timeMachineRows{
		NetWorthChange: math.Round(p.CurrentNW) - math.Round(p.HistoricalNW),
		Contributions:  math.Round(p.Contributions),
		Dividends:      math.Round(p.Dividends),
		Interest:       math.Round(p.Interest),
		Fees:           math.Round(math.Abs(p.Fees)),
		Taxes:          math.Round(math.Abs(p.Taxes)),
		Assets:         math.Round(p.Assets),
		// 0 − x rather than −x, so nothing spent is 0, not −0, in JSON.
		PurchaseCosts: 0 - math.Round(p.PurchaseCosts),
		LoanInterest:  0 - math.Round(p.LoanInterest),
	}
	r.MarketReturn = r.NetWorthChange - r.Contributions - r.Dividends - r.Interest + r.Fees + r.Taxes -
		r.Assets - r.PurchaseCosts - r.LoanInterest
	return r
}

// switchTaxCost computes the tax bite of selling out of a position with
// the given unrealizedGain, applying:
//
//	taxable_after_TFS = unrealized_gain × (1 − teilfreistellung)
//	taxable_after_FSA = max(0, taxable_after_TFS − remaining_freibetrag)
//	tax               = taxable_after_FSA × 26.375 %
//
// teilfreistellung is the asset-class fraction (0.30 for equity, 0.15 for
// mixed, 0 for bonds). remainingFreibetrag is the Sparerpauschbetrag balance
// for the year. Loss pots and church tax are handled by switchTax.
func switchTaxCost(unrealizedGain, teilfreistellung, remainingFreibetrag float64) float64 {
	return switchTax(switchTaxInput{
		Gain:     unrealizedGain,
		TFS:      teilfreistellung,
		Position: yearTaxPosition{AllowanceLeft: remainingFreibetrag},
		Rate:     analytics.CapitalIncomeTaxRate(0),
	}).Tax
}

// yearTaxPosition is the current tax year's realised capital income after
// the loss-offset rules of §20 Abs. 6 EStG: share losses only offset share
// gains (Aktienverlusttopf), all other losses offset any capital income.
// Fund gains, losses and distributions count after Teilfreistellung.
// Private sales (§23: crypto, physical gold) are left out. Loss carry-forwards
// from earlier years are not included.
type yearTaxPosition struct {
	Year          int
	Allowance     float64 // Sparer-Pauschbetrag for the year
	AllowanceLeft float64 // allowance not used by this year's income
	// IncomeAboveAllowance is this year's taxable income left after loss
	// offsets and the allowance, i.e. the income tax has been withheld on.
	IncomeAboveAllowance float64
	// StockIncome is the part of this year's income that comes from share
	// gains (after share losses); share losses can only offset this.
	StockIncome     float64
	StockLossLeft   float64 // unused share losses (offset future share gains)
	GeneralLossLeft float64 // unused general losses (offset any income)
}

// computeYearTaxPosition nets one tax year's realised income. txnsChrono are
// all active-account transactions, oldest first; sells use per-depot FIFO at
// purchase-date ECB rates.
func computeYearTaxPosition(txnsChrono []db.ListTransactionsRow, secs map[string]db.Security, fx *fxTable, year int, joint bool) yearTaxPosition {
	pos := yearTaxPosition{Year: year, Allowance: analytics.SparerPauschbetragForYear(year, joint)}
	lots := make(map[positionKey][]costLot)
	stockGains, stockLosses, other := 0.0, 0.0, 0.0
	for _, t := range txnsChrono {
		isin := ""
		if t.SecurityISIN.Valid {
			isin = t.SecurityISIN.String
		}
		sec := secs[isin]
		private := isin != "" && analytics.IsPrivateSaleAssetClass(sec.AssetClass)
		tfs := 0.0
		if isin != "" {
			tfs = securityTFSRate(sec)
		}
		k := positionKey{AccountID: t.AccountID, ISIN: isin}
		qty := math.Abs(numericToFloat(t.Quantity))
		switch {
		case isin != "" && isAcquisitionType(t.Type):
			if qty <= 0 {
				continue
			}
			cost := fx.toEUR(math.Abs(numericToFloat(t.Amount))+math.Abs(numericToFloat(t.Fee)), t.Currency, t.Date)
			lots[k] = append(lots[k], costLot{Date: t.Date, Qty: qty, CostEUR: cost})
		case isin != "" && t.Type == "transfer_out":
			lots[k], _ = consumeFIFO(lots[k], qty)
		case isin != "" && t.Type == "sell":
			held := 0.0
			for _, l := range lots[k] {
				held += l.Qty
			}
			var costSold float64
			lots[k], costSold = consumeFIFO(lots[k], qty)
			soldQty := math.Min(qty, held)
			if t.Date.Year() != year || soldQty <= 1e-12 || private {
				continue
			}
			proceeds := fx.toEUR(math.Abs(numericToFloat(t.Amount))-math.Abs(numericToFloat(t.Fee)), t.Currency, t.Date)
			if qty > soldQty {
				proceeds *= soldQty / qty
			}
			gain := proceeds - costSold
			if analytics.IsShareAssetClass(sec.AssetClass) {
				if gain >= 0 {
					stockGains += gain
				} else {
					stockLosses -= gain
				}
			} else {
				other += gain * (1 - tfs)
			}
		case t.Type == "dividend":
			if t.Date.Year() == year && !private {
				other += fx.toEUR(numericToFloat(t.Amount), t.Currency, t.Date) * (1 - tfs)
			}
		case t.Type == "interest":
			if t.Date.Year() == year {
				other += fx.toEUR(numericToFloat(t.Amount), t.Currency, t.Date)
			}
		}
	}
	stockNet := stockGains - stockLosses
	if stockNet < 0 {
		pos.StockLossLeft = -stockNet
		stockNet = 0
	}
	total := stockNet + other
	if total < 0 {
		pos.GeneralLossLeft = -total
		total = 0
	}
	// General losses offset share gains too: what is left of the share
	// income is bounded by the total.
	pos.StockIncome = math.Min(stockNet, total)
	pos.AllowanceLeft = math.Max(0, pos.Allowance-total)
	pos.IncomeAboveAllowance = math.Max(0, total-pos.Allowance)
	return pos
}

// switchTaxInput describes selling a whole position today.
type switchTaxInput struct {
	Gain     float64 // EUR, FIFO cost at purchase-date rates
	TFS      float64 // Teilfreistellung of the security
	IsStock  bool    // single share: losses go to the Aktienverlusttopf
	Position yearTaxPosition
	Rate     float64 // Abgeltungsteuer + Soli (+ church tax)
}

type switchTaxResult struct {
	TFSAmount       float64 // part of the gain (or loss) exempt by Teilfreistellung
	TaxableAfterTFS float64
	LossOffset      float64 // gain offset by this year's unused losses
	AllowanceUsed   float64
	Taxable         float64
	Tax             float64
	// For a loss: the loss that counts for tax (after Teilfreistellung),
	// the part that offsets income already taxed this year (tax refunded
	// now), and that refund. The rest carries forward.
	LossAfterTFS float64
	LossUsedNow  float64
	TaxSavedNow  float64
}

// switchTax applies German capital-income rules to the sale of a position:
// Teilfreistellung on gains and losses of funds; this year's unused losses
// (share losses only against share gains); the remaining Sparer-Pauschbetrag;
// then the rate. A loss is never taxed; it offsets income already taxed this
// year (shares: only share gains) and otherwise carries forward.
func switchTax(in switchTaxInput) switchTaxResult {
	var r switchTaxResult
	if in.Gain >= 0 {
		r.TFSAmount = in.Gain * in.TFS
		r.TaxableAfterTFS = in.Gain - r.TFSAmount
		left := r.TaxableAfterTFS
		if in.IsStock {
			s := math.Min(left, in.Position.StockLossLeft)
			left -= s
			r.LossOffset += s
		}
		g := math.Min(left, in.Position.GeneralLossLeft)
		left -= g
		r.LossOffset += g
		r.AllowanceUsed = math.Min(left, in.Position.AllowanceLeft)
		r.Taxable = left - r.AllowanceUsed
		r.Tax = r.Taxable * in.Rate
		return r
	}
	loss := -in.Gain
	r.TFSAmount = loss * in.TFS
	r.LossAfterTFS = loss - r.TFSAmount
	room := in.Position.IncomeAboveAllowance
	if in.IsStock {
		room = math.Min(room, in.Position.StockIncome)
	}
	r.LossUsedNow = math.Min(r.LossAfterTFS, math.Max(0, room))
	r.TaxSavedNow = r.LossUsedNow * in.Rate
	return r
}

// parseChurchTaxRate reads ?church_tax= as 0, 0.08 or 0.09 (or 8 / 9).
func parseChurchTaxRate(v string) float64 {
	f, err := parseFiniteFloat(v)
	if err != nil || f <= 0 {
		return 0
	}
	if f > 1 {
		f /= 100
	}
	return math.Min(f, 0.09)
}

// switchLot is a remaining FIFO lot of the current holding.
type switchLot struct {
	Date    time.Time
	Qty     float64
	CostEUR float64
}

// switchAlternativeValue replays the remaining lots into the alternative:
// each lot's purchase money buys the alternative at its EUR price on the lot
// date. A lot older than the first date both securities have a price for
// (compareFrom) is switched on that date instead, at the current holding's
// value then. Returns the alternative's value today, the number of lots
// shifted to compareFrom and the earliest date compared from.
func switchAlternativeValue(lots []switchLot, curPriceAt, altPriceAt func(time.Time) float64, compareFrom time.Time, altPriceNow float64) (value float64, shifted int, from time.Time) {
	for _, l := range lots {
		d := l.Date
		money := l.CostEUR
		if d.Before(compareFrom) {
			d = compareFrom
			money = l.Qty * curPriceAt(compareFrom)
			shifted++
		}
		alt := altPriceAt(d)
		if alt <= 0 || money <= 0 {
			continue
		}
		value += money / alt * altPriceNow
		if from.IsZero() || d.Before(from) {
			from = d
		}
	}
	return value, shifted, from
}

// HandleSwitchCompare compares the current holding with an alternative ISIN.
//
// "This holding" is the position actually held today (active accounts) at
// the latest price; its gain is value minus the cost of the remaining
// per-depot FIFO lots at purchase-date ECB rates, including RSU vests and
// depot transfers. "Alternative" puts the money of those same remaining lots
// into the other security (see switchAlternativeValue). Accepts ?church_tax=
// (0.08 / 0.09) and ?joint=1 like the tax pages.
func (h *PortfolioHandler) HandleSwitchCompare(w http.ResponseWriter, r *http.Request) {
	currentISIN := r.URL.Query().Get("current")
	altISIN := r.URL.Query().Get("alternative")
	if currentISIN == "" || altISIN == "" {
		writeError(w, http.StatusBadRequest, "current and alternative ISIN required")
		return
	}

	ctx := r.Context()

	// Get current holding details
	currentSec, err := h.queries.GetSecurity(ctx, currentISIN)
	if err != nil {
		writeError(w, http.StatusNotFound, "current security not found")
		return
	}

	fxRates := loadFXRatesMap(ctx, h.queries)
	fx := newFXTable(loadHistoricalFXRates(ctx, h.queries), fxRates)

	// Fetch historical prices from DB for both ISINs (EUR at each date's rate)
	type priceEntry struct {
		Date  time.Time
		Close float64
	}
	var lastAltRaw pricePoint
	fetchPrices := func(isin string, keepLast *pricePoint) []priceEntry {
		rows, err := h.queries.ListPriceHistory(ctx, isin)
		if err != nil {
			return nil
		}
		var result []priceEntry
		for _, r := range rows {
			c := numericToFloat(r.Close)
			if c <= 0 {
				continue
			}
			if eurPrice := fx.toEUR(c, r.Currency, r.Date); eurPrice > 0 {
				result = append(result, priceEntry{Date: civilDay(r.Date), Close: eurPrice})
				if keepLast != nil {
					*keepLast = pricePoint{date: r.Date, close: c, currency: r.Currency}
				}
			}
		}
		return result
	}

	currentPrices := fetchPrices(currentISIN, nil)
	if len(currentPrices) == 0 {
		writeError(w, http.StatusNotFound, "no price history for current ISIN")
		return
	}
	altPrices := fetchPrices(altISIN, &lastAltRaw)
	if len(altPrices) == 0 {
		writeError(w, http.StatusNotFound, "no price history for alternative ISIN")
		return
	}
	priceAt := func(prices []priceEntry) func(time.Time) float64 {
		return func(d time.Time) float64 {
			day := civilDay(d)
			i := sort.Search(len(prices), func(i int) bool { return prices[i].Date.After(day) })
			if i == 0 {
				return 0
			}
			return prices[i-1].Close
		}
	}
	curPriceAt, altPriceAt := priceAt(currentPrices), priceAt(altPrices)

	// The position held today (active accounts only) and its FIFO lots.
	all, err := listAllTransactionsRows(ctx, h.queries)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list transactions: "+err.Error())
		return
	}
	txnsChrono := chronological(all)
	var isinTxns []db.ListTransactionsRow
	for _, t := range txnsChrono {
		if t.SecurityISIN.Valid && t.SecurityISIN.String == currentISIN {
			isinTxns = append(isinTxns, t)
		}
	}
	positions := replayPositionCosts(isinTxns, fx)
	pm := h.loadPriceMap(ctx)
	currentValue, costBasis, heldQty := 0.0, 0.0, 0.0
	var lots []switchLot
	if holdings, err := h.queries.ListCurrentHoldings(ctx); err == nil {
		for _, hold := range holdings {
			if hold.SecurityISIN != currentISIN {
				continue
			}
			qty := numericToFloat(hold.Quantity)
			heldQty += qty
			pc := positions[positionKey{AccountID: hold.AccountID, ISIN: currentISIN}]
			if p, ok := pm[currentISIN]; ok && p.Close.Valid {
				currentValue += convertAmountToEUR(qty*numericToFloat(p.Close), p.Currency, fxRates)
			} else if pc != nil {
				currentValue += pc.AvgCostEUR() * qty
			}
			if pc != nil {
				costBasis += pc.FIFOCostEUR()
				for _, l := range pc.Lots {
					lots = append(lots, switchLot{Date: l.Date, Qty: l.Qty, CostEUR: l.CostEUR})
				}
			}
		}
	}
	if heldQty <= 1e-9 {
		writeError(w, http.StatusNotFound, "no current position in this security")
		return
	}
	sort.Slice(lots, func(i, j int) bool { return lots[i].Date.Before(lots[j].Date) })

	compareFrom := currentPrices[0].Date
	if altPrices[0].Date.After(compareFrom) {
		compareFrom = altPrices[0].Date
	}
	altPriceNow := convertAmountToEUR(lastAltRaw.close, lastAltRaw.currency, fxRates)
	altValue, shiftedLots, comparedFrom := switchAlternativeValue(lots, curPriceAt, altPriceAt, compareFrom, altPriceNow)

	// Correlation aligned on common trading calendar dates (avoids holiday drift)
	curPriceMap := make(map[string]float64, len(currentPrices))
	for _, p := range currentPrices {
		curPriceMap[p.Date.Format("2006-01-02")] = p.Close
	}
	altPriceMap := make(map[string]float64, len(altPrices))
	for _, p := range altPrices {
		altPriceMap[p.Date.Format("2006-01-02")] = p.Close
	}
	var commonDates []string
	for _, p := range currentPrices {
		d := p.Date.Format("2006-01-02")
		if altPriceMap[d] > 0 && p.Close > 0 {
			commonDates = append(commonDates, d)
		}
	}
	correlation := 0.0
	if len(commonDates) > 10 {
		var curReturns, altReturns []float64
		step := len(commonDates) / 52
		if step < 1 {
			step = 1
		}
		for i := step; i < len(commonDates); i += step {
			prevCur := curPriceMap[commonDates[i-step]]
			prevAlt := altPriceMap[commonDates[i-step]]
			if prevCur > 0 && prevAlt > 0 {
				cr := (curPriceMap[commonDates[i]] - prevCur) / prevCur
				ar := (altPriceMap[commonDates[i]] - prevAlt) / prevAlt
				curReturns = append(curReturns, cr)
				altReturns = append(altReturns, ar)
			}
		}
		if len(curReturns) > 5 {
			correlation = computeCorrelation(curReturns, altReturns)
		}
	}

	// Tax-aware switch cost: InvStG Teilfreistellung (0 % for single shares
	// and bond funds, 15 % mixed, 30 % equity funds), this year's unused
	// losses in the right pot, the remaining Sparer-Pauschbetrag, church tax.
	unrealizedGain := currentValue - costBasis
	teilfreistellungRate := securityTFSRate(currentSec)
	isEquity := teilfreistellungRate >= analytics.TeilfreistellungEquity
	isStock := analytics.IsShareAssetClass(currentSec.AssetClass)
	privateSale := analytics.IsPrivateSaleAssetClass(currentSec.AssetClass)
	churchTax := parseChurchTaxRate(r.URL.Query().Get("church_tax"))
	joint := r.URL.Query().Get("joint") == "1"
	taxRate := analytics.CapitalIncomeTaxRate(churchTax)
	secs, _ := h.queries.ListSecurities(ctx)
	secMap := make(map[string]db.Security, len(secs))
	for _, s := range secs {
		secMap[s.ISIN] = s
	}
	position := computeYearTaxPosition(txnsChrono, secMap, fx, time.Now().Year(), joint)
	tax := switchTax(switchTaxInput{
		Gain: unrealizedGain, TFS: teilfreistellungRate, IsStock: isStock, Position: position, Rate: taxRate,
	})
	if privateSale {
		// §23 EStG: not capital income. Lots held over a year are tax-free;
		// shorter ones are taxed at the personal income-tax rate, which the
		// app does not know — no Abgeltungsteuer estimate.
		tax = switchTaxResult{}
	}
	taxOnSwitch := tax.Tax
	netProceeds := currentValue - taxOnSwitch

	// Break-even: how many years until the alternative's better performance
	// recovers the tax cost of switching, over the compared period.
	annualAdvantage := 0.0
	if altValue > currentValue && currentValue > 0 && !comparedFrom.IsZero() {
		years := time.Since(comparedFrom).Hours() / (24 * 365.25)
		if years > 0 {
			annualAdvantage = (altValue - currentValue) / years
		}
	}
	breakEvenYears := 0.0
	if annualAdvantage > 0 && taxOnSwitch > 0 {
		breakEvenYears = math.Ceil(taxOnSwitch / annualAdvantage)
	}

	diffPct := 0.0
	if currentValue != 0 {
		diffPct = math.Round((altValue-currentValue)/currentValue*1000) / 10
	}
	lossPot := "general"
	if isStock {
		lossPot = "stock"
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"current_isin":            currentISIN,
		"current_name":            currentSec.Name,
		"alternative_isin":        altISIN,
		"total_invested":          math.Round(costBasis),
		"current_value":           math.Round(currentValue),
		"alternative_value":       math.Round(altValue),
		"difference_eur":          math.Round(altValue - currentValue),
		"difference_pct":          diffPct,
		"compare_from":            datePtr(comparedFrom),
		"lots_shifted":            shiftedLots,
		"lots":                    len(lots),
		"correlation":             math.Round(correlation*100) / 100,
		"low_correlation_warning": correlation < 0.85,
		// Tax-aware switch cost
		"unrealized_gain":      math.Round(unrealizedGain),
		"teilfreistellung":     math.Round(tax.TFSAmount),
		"teilfreistellung_pct": math.Round(teilfreistellungRate * 100),
		"loss_offset":          math.Round(tax.LossOffset),
		"taxable_gain":         math.Round(tax.Taxable),
		"freibetrag_used":      math.Round(tax.AllowanceUsed),
		"freibetrag_remaining": math.Round(position.AllowanceLeft),
		"allowance":            position.Allowance,
		"tax_rate":             taxRate,
		"tax_on_switch":        math.Round(taxOnSwitch),
		"net_proceeds":         math.Round(netProceeds),
		"break_even_years":     breakEvenYears,
		"is_equity":            isEquity,
		"is_stock":             isStock,
		"private_sale":         privateSale,
		// Selling at a loss: the loss that counts for tax, the part that
		// offsets income already taxed this year and the tax that refunds.
		"loss_pot":              lossPot,
		"harvest_loss":          math.Round(tax.LossAfterTFS),
		"harvest_loss_now":      math.Round(tax.LossUsedNow),
		"harvest_tax_now":       math.Round(tax.TaxSavedNow),
		"stock_gains_to_offset": math.Round(position.StockIncome),
	})
}

func computeCorrelation(x, y []float64) float64 {
	n := len(x)
	if n != len(y) || n == 0 {
		return 0
	}
	var sumX, sumY, sumXY, sumX2, sumY2 float64
	for i := 0; i < n; i++ {
		sumX += x[i]
		sumY += y[i]
		sumXY += x[i] * y[i]
		sumX2 += x[i] * x[i]
		sumY2 += y[i] * y[i]
	}
	nf := float64(n)
	num := nf*sumXY - sumX*sumY
	varX := math.Max(0, nf*sumX2-sumX*sumX)
	varY := math.Max(0, nf*sumY2-sumY*sumY)
	den := math.Sqrt(varX * varY)
	if den <= 0 || math.IsNaN(den) || math.IsInf(den, 0) {
		return 0
	}
	r := num / den
	if math.IsNaN(r) || math.IsInf(r, 0) {
		return 0
	}
	return math.Max(-1, math.Min(1, r))
}

// --- Unvested RSU vests ---

type UnvestedVest struct {
	VestDate         string  `json:"vest_date"`
	GrantNumber      string  `json:"grant_number"`
	GrossQuantity    float64 `json:"gross_quantity"`
	NetEstimate      float64 `json:"net_estimate"`
	ValueEstimate    float64 `json:"value_estimate"`
	ValueEstimateEUR float64 `json:"value_estimate_eur"`
}

type UnvestedAccount struct {
	AccountID            string         `json:"account_id"`
	AccountName          string         `json:"account_name"`
	SecurityISIN         string         `json:"security_isin"`
	Symbol               string         `json:"symbol,omitempty"`
	Currency             string         `json:"currency"`
	Ratio                float64        `json:"ratio"`
	DefaultRatioUsed     bool           `json:"default_ratio_used"`
	TotalGross           float64        `json:"total_gross"`
	TotalNetEstimate     float64        `json:"total_net_estimate"`
	CurrentPrice         float64        `json:"current_price"`
	CurrentPriceCurrency string         `json:"current_price_currency"`
	TotalValueEstimate   float64        `json:"total_value_estimate"`
	TotalValueEUR        float64        `json:"total_value_eur"`
	ByVest               []UnvestedVest `json:"by_vest"`
}

type UnvestedResponse struct {
	Accounts             []UnvestedAccount  `json:"accounts"`
	TotalValueByCurrency map[string]float64 `json:"total_value_by_currency"`
	TotalValueEUR        float64            `json:"total_value_eur"`
}

const defaultPostTaxRatio = 0.50

func computePostTaxRatio(ctx context.Context, q *db.Queries, accountID uuid.UUID) (ratio float64, defaultUsed bool) {
	row, err := q.GetPostTaxRatio(ctx, accountID)
	if err != nil {
		return defaultPostTaxRatio, true
	}
	gross := numericToFloat(row.GrossTotal)
	net := numericToFloat(row.NetTotal)
	if gross <= 0 || net <= 0 {
		return defaultPostTaxRatio, true
	}
	return net / gross, false
}

// vestIsPast reports whether a vest date (a DB date, i.e. UTC midnight) lies
// before today's local calendar date. A vest dated today is still upcoming.
func vestIsPast(vestDate, now time.Time) bool {
	y, m, d := now.Date()
	return vestDate.Before(time.Date(y, m, d, 0, 0, 0, 0, time.UTC))
}

func (h *PortfolioHandler) HandleUnvested(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	accounts, err := h.queries.ListAccounts(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list accounts: "+err.Error())
		return
	}

	// Filter by user if auth is enabled
	if allowedIDs := userAccountIDs(ctx, h.queries.DB()); allowedIDs != nil {
		allowed := make(map[uuid.UUID]bool, len(allowedIDs))
		for _, id := range allowedIDs {
			allowed[id] = true
		}
		filtered := accounts[:0]
		for _, a := range accounts {
			if allowed[a.ID] {
				filtered = append(filtered, a)
			}
		}
		accounts = filtered
	}

	resp := UnvestedResponse{
		Accounts:             []UnvestedAccount{},
		TotalValueByCurrency: map[string]float64{},
	}

	for _, acc := range accounts {
		if acc.Institution != "morgan_stanley" {
			continue
		}

		vests, err := h.queries.ListUnvestedRSUVests(ctx, acc.ID)
		if err != nil || len(vests) == 0 {
			continue
		}

		ratio, defaultUsed := computePostTaxRatio(ctx, h.queries, acc.ID)

		isin := ""
		if acc.ImportSecurityISIN.Valid {
			isin = acc.ImportSecurityISIN.String
		}

		var symbol, priceCurrency string
		var currentPrice float64
		if isin != "" {
			if sec, err := h.queries.GetSecurity(ctx, isin); err == nil {
				if sec.Symbol.Valid {
					symbol = sec.Symbol.String
				}
			}
			if p, err := h.queries.GetLatestPrice(ctx, isin); err == nil {
				currentPrice = numericToFloat(p.Close)
				priceCurrency = p.Currency
			}
		}
		if priceCurrency == "" {
			priceCurrency = acc.Currency
		}

		out := UnvestedAccount{
			AccountID:            acc.ID.String(),
			AccountName:          acc.Name,
			SecurityISIN:         isin,
			Symbol:               symbol,
			Currency:             acc.Currency,
			Ratio:                ratio,
			DefaultRatioUsed:     defaultUsed,
			CurrentPrice:         currentPrice,
			CurrentPriceCurrency: priceCurrency,
			ByVest:               make([]UnvestedVest, 0, len(vests)),
		}

		now := time.Now()
		for _, v := range vests {
			// Rows stay vested=false until a Releases report flips them, but the
			// vest itself has already landed as an in-kind transfer — counting
			// it here would double it against holdings.
			if vestIsPast(v.VestDate, now) {
				continue
			}
			gross := numericToFloat(v.GrossQuantity)
			if gross <= 0 {
				continue
			}
			netEstimate := gross * ratio
			valueEstimate := netEstimate * currentPrice
			valueEstimateEUR := convertToEUR(ctx, h.queries, valueEstimate, priceCurrency)
			out.TotalGross += gross
			out.TotalNetEstimate += netEstimate
			out.TotalValueEstimate += valueEstimate
			out.TotalValueEUR += valueEstimateEUR
			grant := ""
			if v.GrantNumber.Valid {
				grant = v.GrantNumber.String
			}
			out.ByVest = append(out.ByVest, UnvestedVest{
				VestDate:         v.VestDate.Format("2006-01-02"),
				GrantNumber:      grant,
				GrossQuantity:    gross,
				NetEstimate:      netEstimate,
				ValueEstimate:    valueEstimate,
				ValueEstimateEUR: valueEstimateEUR,
			})
		}

		if len(out.ByVest) == 0 {
			continue
		}
		resp.Accounts = append(resp.Accounts, out)
		resp.TotalValueEUR += out.TotalValueEUR
		if priceCurrency != "" {
			resp.TotalValueByCurrency[priceCurrency] += out.TotalValueEstimate
		}
	}

	writeJSON(w, http.StatusOK, resp)
}
