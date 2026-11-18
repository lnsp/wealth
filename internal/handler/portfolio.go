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
		MarketValue    *float64 `json:"market_value"`
		UnrealizedPL   *float64 `json:"unrealized_pl"`
		WeightPct      *float64 `json:"weight_pct"`
		FXExposure     string   `json:"fx_exposure,omitempty"`      // dominant currency
		FXImpactPct    *float64 `json:"fx_impact_pct,omitempty"`    // estimated FX contribution to return
		AssetReturnPct *float64 `json:"asset_return_pct,omitempty"` // return excluding FX
		// ValueEUR is what this position contributes to account balances and
		// net worth: market value, or EUR cost basis when no price is known
		// (same fallback as holdingValueFromMap). PriceMissing flags the latter.
		ValueEUR     float64 `json:"value_eur"`
		PriceMissing bool    `json:"price_missing"`
	}

	// Batch-load all latest prices and FX rates in one query (eliminates N+1)
	prices := h.loadPriceMap(r.Context())
	fxRates := loadFXRatesMap(r.Context(), h.queries)

	// Load securities for country weights → FX exposure
	secs, _ := h.queries.ListSecurities(r.Context())
	secMap := make(map[string]db.Security, len(secs))
	for _, s := range secs {
		secMap[s.ISIN] = s
	}

	// Get EUR/USD 1-year change for FX impact estimate
	fxChanges := make(map[string]float64) // currency → 1yr change %
	for _, cur := range []string{"USD", "GBP", "JPY", "CHF"} {
		rates, err := h.queries.ListExchangeRateHistory(r.Context(), cur)
		if err == nil && len(rates) > 252 {
			oldRate := numericToFloat(rates[len(rates)-252].Rate) // ~1yr ago
			newRate := numericToFloat(rates[len(rates)-1].Rate)
			if oldRate > 0 {
				// EUR strengthening = negative FX impact for foreign holdings
				fxChanges[cur] = (newRate/oldRate - 1) * -100
			}
		}
	}

	var result []holdingWithPrice
	totalValue := 0.0
	var latestPriceDate time.Time
	for _, holding := range holdings {
		// Apply account filter
		if filterAccountID != "" && holding.AccountID.String() != filterAccountID {
			continue
		}

		hwp := holdingWithPrice{
			ListCurrentHoldingsRow: holding,
			AccountName:       acctNames[holding.AccountID.String()],
		}

		// Look up price from batch-loaded map. Prices are stored in the
		// security's native currency; market_value and unrealized_pl must be
		// EUR-converted so the frontend formatter (locked to EUR) and
		// /api/portfolio/performance.current_value (also EUR-summed)
		// reconcile within FP tolerance.
		if priceRow, ok := prices[holding.SecurityISIN]; ok && priceRow.Close.Valid {
			pf, _ := priceRow.Close.Float64Value()
			rawPrice := pf.Float64
			normPriceCur, priceScale := normalizeCurrencyAndScale(priceRow.Currency)
			price := rawPrice * priceScale
			hwp.CurrentPrice = &price
			hwp.PriceCurrency = normPriceCur
			if priceRow.Date.After(latestPriceDate) {
				latestPriceDate = priceRow.Date
			}

			qf, _ := holding.Quantity.Float64Value()
			qty := qf.Float64
			mvEUR := convertAmountToEUR(qty*rawPrice, priceRow.Currency, fxRates)
			hwp.MarketValue = &mvEUR
			hwp.ValueEUR = mvEUR
			totalValue += mvEUR

			cf, _ := holding.AvgCostBasis.Float64Value()
			costBasis := cf.Float64
			costEUR := convertAmountToEUR(qty*costBasis, holding.Currency, fxRates)
			pl := mvEUR - costEUR
			hwp.UnrealizedPL = &pl
		} else {
			// Fallback to cost basis for weight calculation (also EUR).
			qf, _ := holding.Quantity.Float64Value()
			cf, _ := holding.AvgCostBasis.Float64Value()
			normHoldCur, _ := normalizeCurrencyAndScale(holding.Currency)
			hwp.PriceCurrency = normHoldCur
			hwp.ValueEUR = convertAmountToEUR(qf.Float64*cf.Float64, holding.Currency, fxRates)
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
					// total_return % = unrealized_pl_EUR / cost_basis_EUR × 100.
					// Previously the denominator was qty × avg_cost in the
					// security's NATIVE currency, while the numerator (post
					// the recent HandleHoldings EUR-conversion fix) is EUR —
					// the ratio was nonsense for any non-EUR holding.
					// Asset return = total return − FX-attributable %.
					totalReturn := 0.0
					qf, _ := holding.Quantity.Float64Value()
					cf, _ := holding.AvgCostBasis.Float64Value()
					costTotalEUR := convertAmountToEUR(qf.Float64*cf.Float64, holding.Currency, fxRates)
					if costTotalEUR > 0 {
						totalReturn = (*hwp.UnrealizedPL / costTotalEUR) * 100
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
	if !latestPriceDate.IsZero() {
		resp["price_as_of"] = latestPriceDate.Format("2006-01-02")
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *PortfolioHandler) HandleNetWorth(w http.ResponseWriter, r *http.Request) {
	limit := int32(365)
	if l := r.URL.Query().Get("days"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 {
			if n > 5000 {
				n = 5000
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

func (h *PortfolioHandler) HandlePerformance(w http.ResponseWriter, r *http.Request) {
	// Optional account filter — allows per-account performance comparison with broker apps
	filterAccountID := r.URL.Query().Get("account_id")
	var filterAccUUID uuid.UUID
	if filterAccountID != "" {
		var err error
		filterAccUUID, err = uuid.Parse(filterAccountID)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid account_id")
			return
		}
	}

	// Load ALL transactions (no limit) to avoid truncating old deposits/withdrawals
	// that would skew the gain calculation. Use raw query for unlimited results.
	txns, err := h.loadAllTransactions(r.Context(), filterAccUUID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list transactions: "+err.Error())
		return
	}

	if len(txns) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{
			"irr":              0,
			"total_invested":   0,
			"total_withdrawn":  0,
			"current_value":    0,
			"total_return":     0,
			"total_return_pct": 0,
		})
		return
	}

	scope := analytics.ScopePortfolio
	if filterAccUUID != uuid.Nil {
		scope = analytics.ScopeAccount
	}

	// At portfolio scope, broker-to-broker in-kind moves appear as a matched
	// `transfer` / `transfer_out` pair (same ISIN + qty within ±5d). Wash them
	// so they don't inflate transferredIn. A true RSU vest has no offsetting
	// transfer_out and stays as a Contribution.
	washed := map[uuid.UUID]struct{}{}
	if scope == analytics.ScopePortfolio {
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
		washed = analytics.MatchInKindTransferPairs(inkind)
	}

	fxRates := loadFXRatesMap(r.Context(), h.queries)
	histFX := loadHistoricalFXRates(r.Context(), h.queries)

	var cashflows []analytics.CashFlow
	var cashDeposited, cashWithdrawn, transferredIn, transferredOut float64

	for _, txn := range txns {
		if _, isWash := washed[txn.ID]; isWash {
			continue
		}
		amt := 0.0
		if txn.Amount.Valid {
			f, _ := txn.Amount.Float64Value()
			amt = convertAmountToEURAtDate(f.Float64, txn.Currency, txn.Date, histFX, fxRates)
		}
		hasISIN := txn.SecurityISIN.Valid

		if scope == analytics.ScopeAccount && txn.Type == "transfer_out" && hasISIN {
			// Outbound share transfer from this specific account removes capital.
			transferredOut += amt
			cashflows = append(cashflows, analytics.CashFlow{Date: txn.Date, Amount: amt})
			continue
		}

		switch analytics.ClassifyForAttribution(txn.Type, hasISIN, scope) {
		case analytics.BucketContribution:
			if hasISIN {
				// In-kind grant (e.g. RSU vest): record in transferred_in and as a
				// dated contribution in cashflows so both IRR and single-account
				// performance measure market movement after vest rather than
				// fabricating 100% of vested shares as investment return.
				transferredIn += amt
				cashflows = append(cashflows, analytics.CashFlow{Date: txn.Date, Amount: -amt})
			} else {
				cashflows = append(cashflows, analytics.CashFlow{Date: txn.Date, Amount: -amt})
				cashDeposited += amt
			}
		case analytics.BucketWithdrawal:
			cashflows = append(cashflows, analytics.CashFlow{Date: txn.Date, Amount: amt})
			cashWithdrawn += amt
		}
	}

	// Compute current portfolio value (clamped multi-currency cash + holdings)
	// and reconcile implicit cash funding on securities-only accounts without
	// explicit cash transactions.
	currentValue := 0.0
	var targetAccounts []db.Account
	if filterAccUUID != uuid.Nil {
		if acc, err := h.queries.GetAccount(r.Context(), filterAccUUID); err == nil {
			targetAccounts = []db.Account{acc}
			if cashEUR, err := computeSingleAccountCashEUR(r.Context(), h.queries, acc, fxRates); err == nil {
				currentValue += cashEUR
			}
		}
	} else {
		if accounts, err := h.queries.ListAccounts(r.Context()); err == nil {
			targetAccounts = accounts
			if cashByAcct, err := computeAllAccountCashEUR(r.Context(), h.queries, accounts, fxRates); err == nil {
				for _, c := range cashByAcct {
					currentValue += c
				}
			}
		}
	}

	impEvents := computeImplicitCashClampEvents(targetAccounts, txns, histFX, fxRates)
	for _, ev := range impEvents {
		if ev.DeltaEUR > 0 {
			cashDeposited += ev.DeltaEUR
			cashflows = append(cashflows, analytics.CashFlow{Date: ev.Date, Amount: -ev.DeltaEUR})
		} else if ev.DeltaEUR < 0 {
			w := -ev.DeltaEUR
			cashWithdrawn += w
			cashflows = append(cashflows, analytics.CashFlow{Date: ev.Date, Amount: w})
		}
	}

	prices := h.loadPriceMap(r.Context())

	// Load holdings — filter by account if specified
	var perfHoldings []db.ListCurrentHoldingsRow
	if filterAccUUID != uuid.Nil {
		all, err := h.queries.ListCurrentHoldings(r.Context())
		if err == nil {
			for _, h := range all {
				if h.AccountID == filterAccUUID {
					perfHoldings = append(perfHoldings, h)
				}
			}
		}
	} else {
		perfHoldings, _ = h.queries.ListCurrentHoldings(r.Context())
	}
	for _, holding := range perfHoldings {
		currentValue += holdingValueEURFromMap(holding, prices, fxRates)
	}

	today := assets.CivilDate(time.Now())
	cashflows = append(cashflows, analytics.CashFlow{Date: today, Amount: currentValue})

	// Sort by date for IRR
	sortCashFlows(cashflows)

	// Compute IRR on capital deployed (cash + dated in-kind grants)
	irr := analytics.CalculateIRR(cashflows, 0.05)

	// Compute realized P&L from sell transactions using average cost method (in EUR)
	var plTxns []analytics.TransactionForPL
	for _, txn := range txns {
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
		if isin != "" {
			adjAmt := amt
			switch txn.Type {
			case "buy", "savings_plan", "transfer":
				adjAmt = amt + fee
			case "sell":
				adjAmt = amt - fee
			}
			plTxns = append(plTxns, analytics.TransactionForPL{
				Type: txn.Type,
				ISIN: isin,
				Qty:  qty,
				Amt:  adjAmt,
			})
		}
	}
	realizedResult := analytics.CalculateRealizedPL(plTxns)

	// Unrealized P&L = total gains - realized gains
	netCapitalInvested := cashDeposited + transferredIn - cashWithdrawn - transferredOut
	gains := currentValue - netCapitalInvested
	gainsPct := 0.0
	if netCapitalInvested > 0 {
		gainsPct = (gains / netCapitalInvested) * 100
	}
	unrealizedPL := gains - realizedResult.TotalRealizedPL

	// Compute trailing 12-month TWR using net worth snapshots
	oneYearAgo := today.AddDate(-1, 0, 0)
	snapshots, _ := h.queries.ListNetWorthSnapshots(r.Context(), 1000)
	var valuations []analytics.DailyValuation
	for i := len(snapshots) - 1; i >= 0; i-- {
		if snapshots[i].Date.Before(oneYearAgo) {
			continue
		}
		// Physical assets are excluded: their purchase stays a withdrawal in
		// twrCashflows, so depreciation must not read as a portfolio loss.
		val := investableNetWorth(snapshots[i])
		if val < 100 {
			continue
		}
		valuations = append(valuations, analytics.DailyValuation{Date: snapshots[i].Date, Value: val})
	}
	// TWR valuations come from portfolio-wide net-worth snapshots, so its
	// cash-flow adjustments must use portfolio scope across all active accounts
	// regardless of the request filter.
	twrSourceTxns := txns
	twrWashed := washed
	twrImpEvents := impEvents
	if filterAccUUID != uuid.Nil {
		if allPortfolioTxns, err := h.loadAllTransactions(r.Context(), uuid.Nil); err == nil {
			twrSourceTxns = allPortfolioTxns
			var inkindAll []analytics.InKindTransfer
			for _, t := range twrSourceTxns {
				if t.SecurityISIN.Valid && (t.Type == "transfer" || t.Type == "transfer_out") {
					inkindAll = append(inkindAll, analytics.InKindTransfer{
						ID: t.ID, Date: t.Date, Type: t.Type, ISIN: t.SecurityISIN.String, Quantity: numericToFloat(t.Quantity),
					})
				}
			}
			twrWashed = analytics.MatchInKindTransferPairs(inkindAll)
			if allAccts, err := h.queries.ListAccounts(r.Context()); err == nil {
				twrImpEvents = computeImplicitCashClampEvents(allAccts, twrSourceTxns, histFX, fxRates)
			}
		}
	}
	var twrCashflows []analytics.CashFlow
	for _, txn := range twrSourceTxns {
		if txn.Date.Before(oneYearAgo) {
			continue
		}
		if _, isWash := twrWashed[txn.ID]; isWash {
			continue
		}
		amt := 0.0
		if txn.Amount.Valid {
			f, _ := txn.Amount.Float64Value()
			amt = convertAmountToEURAtDate(f.Float64, txn.Currency, txn.Date, histFX, fxRates)
		}
		hasISIN := txn.SecurityISIN.Valid
		switch analytics.ClassifyForAttribution(txn.Type, hasISIN, analytics.ScopePortfolio) {
		case analytics.BucketContribution:
			twrCashflows = append(twrCashflows, analytics.CashFlow{Date: txn.Date, Amount: amt})
		case analytics.BucketWithdrawal:
			twrCashflows = append(twrCashflows, analytics.CashFlow{Date: txn.Date, Amount: -amt})
		}
	}
	for _, ev := range twrImpEvents {
		if !ev.Date.Before(oneYearAgo) {
			twrCashflows = append(twrCashflows, analytics.CashFlow{Date: ev.Date, Amount: ev.DeltaEUR})
		}
	}
	twr := analytics.CalculateTWR(valuations, twrCashflows) * 100

	writeJSON(w, http.StatusOK, map[string]any{
		"irr":              irr * 100,
		"twr":              twr,
		"total_invested":   cashDeposited,
		"total_withdrawn":  cashWithdrawn,
		"transferred_in":   transferredIn,
		"current_value":    currentValue,
		"total_return":     gains,
		"total_return_pct": gainsPct,
		"realized_pl":      realizedResult.TotalRealizedPL,
		"unrealized_pl":    unrealizedPL,
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

func (h *PortfolioHandler) HandlePerformanceHistory(w http.ResponseWriter, r *http.Request) {
	// Get net worth snapshots for portfolio value over time
	snapshots, err := h.queries.ListNetWorthSnapshots(r.Context(), 1000)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list snapshots: "+err.Error())
		return
	}
	if len(snapshots) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{"history": []any{}})
		return
	}

	// Get all transactions to compute cumulative cash invested at each date
	txns, err := listAllTransactionsRows(r.Context(), h.queries)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list transactions: "+err.Error())
		return
	}
	fxRates := loadFXRatesMap(r.Context(), h.queries)
	histFX := loadHistoricalFXRates(r.Context(), h.queries)
	accts, _ := h.queries.ListAccounts(r.Context())

	// Build sorted list of capital events at portfolio scope. The classifier
	// handles deposits/withdrawals/internal cash moves uniformly; the
	// pair-matcher washes in-kind transfers that have an offsetting leg
	// (broker-to-broker moves) so only true contributions (e.g. RSU vests)
	// remain.
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

	// Each capital event carries whether it was a cash flow (deposit / cash
	// withdrawal) or an in-kind move (RSU vest at FMV) so the chart can stack
	// the two as separate legend-toggleable series.
	type capitalEvent struct {
		date   time.Time
		amount float64 // positive = capital in, negative = out
		inKind bool
	}
	var events []capitalEvent
	for _, txn := range txns {
		if _, isWash := washed[txn.ID]; isWash {
			continue
		}
		amt := 0.0
		if txn.Amount.Valid {
			f, _ := txn.Amount.Float64Value()
			amt = convertAmountToEURAtDate(f.Float64, txn.Currency, txn.Date, histFX, fxRates)
		}
		hasISIN := txn.SecurityISIN.Valid
		switch analytics.ClassifyForAttribution(txn.Type, hasISIN, analytics.ScopePortfolio) {
		case analytics.BucketContribution:
			events = append(events, capitalEvent{date: txn.Date, amount: amt, inKind: hasISIN})
		case analytics.BucketWithdrawal:
			events = append(events, capitalEvent{date: txn.Date, amount: -amt, inKind: false})
		}
	}
	txnsChrono := make([]db.ListTransactionsRow, len(txns))
	for i := range txns {
		txnsChrono[i] = txns[len(txns)-1-i]
	}
	for _, ev := range computeImplicitCashClampEvents(accts, txnsChrono, histFX, fxRates) {
		events = append(events, capitalEvent{date: ev.Date, amount: ev.DeltaEUR, inKind: false})
	}

	// Sort events by date ascending
	for i := 1; i < len(events); i++ {
		for j := i; j > 0 && events[j].date.Before(events[j-1].date); j-- {
			events[j], events[j-1] = events[j-1], events[j]
		}
	}

	type historyPoint struct {
		Date           string   `json:"date"`
		PortfolioValue float64  `json:"portfolio_value"`
		CashInvested   float64  `json:"cash_invested"`
		InKindInvested float64  `json:"in_kind_invested"`
		ReturnPct      float64  `json:"return_pct"`
		BenchmarkPct   *float64 `json:"benchmark_pct,omitempty"`
	}

	// Snapshots are returned newest-first; reverse for chronological processing
	reversed := make([]db.NetWorthSnapshot, len(snapshots))
	for i, s := range snapshots {
		reversed[len(snapshots)-1-i] = s
	}

	// Load benchmark price history (default MSCI World via IWDA.AS)
	benchmarkPrices := loadBenchmarkPrices(r.Context(), h.queries, h.benchmarkISIN, h.benchmarkTicker)

	history := make([]historyPoint, 0)
	eventIdx := 0
	cashInvested := 0.0
	inKindInvested := 0.0

	for _, snap := range reversed {
		snapDate := snap.Date

		// Accumulate capital events up to this snapshot date
		for eventIdx < len(events) && !events[eventIdx].date.After(snapDate) {
			if events[eventIdx].inKind {
				inKindInvested += events[eventIdx].amount
			} else {
				cashInvested += events[eventIdx].amount
			}
			eventIdx++
		}

		total := investableNetWorth(snap)

		// Return is on total capital deployed (cash + in-kind at FMV) — RSU
		// vests are real wealth the user received, not market gains.
		capitalInvested := cashInvested + inKindInvested
		returnPct := 0.0
		if capitalInvested > 0 {
			returnPct = ((total - capitalInvested) / capitalInvested) * 100
		}

		point := historyPoint{
			Date:           snapDate.Format("2006-01-02"),
			PortfolioValue: total,
			CashInvested:   cashInvested,
			InKindInvested: inKindInvested,
			ReturnPct:      returnPct,
		}

		// Add benchmark return if available
		if len(benchmarkPrices) > 0 {
			bPct := benchmarkReturnAt(benchmarkPrices, reversed[0].Date, snapDate)
			if bPct != nil {
				point.BenchmarkPct = bPct
			}
		}

		history = append(history, point)
	}

	if history == nil {
		history = []historyPoint{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"history": history})
}

func (h *PortfolioHandler) HandleDividends(w http.ResponseWriter, r *http.Request) {
	txns, err := listAllTransactionsRows(r.Context(), h.queries)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list transactions: "+err.Error())
		return
	}
	fxRates := loadFXRatesMap(r.Context(), h.queries)
	histFX := loadHistoricalFXRates(r.Context(), h.queries)

	type monthlyDividend struct {
		Month  string  `json:"month"`
		Amount float64 `json:"amount"`
	}

	type dividendBySecurity struct {
		ISIN   string  `json:"isin"`
		Name   string  `json:"name"`
		Amount float64 `json:"amount"`
	}

	monthlyMap := make(map[string]float64)
	securityMap := make(map[string]float64)
	securityNames := make(map[string]string)
	var totalDividends float64

	for _, txn := range txns {
		if txn.Type != "dividend" {
			continue
		}
		amt := 0.0
		if txn.Amount.Valid {
			f, _ := txn.Amount.Float64Value()
			amt = convertAmountToEURAtDate(f.Float64, txn.Currency, txn.Date, histFX, fxRates)
		}

		month := txn.Date.Format("2006-01")
		monthlyMap[month] += amt
		totalDividends += amt

		isin := ""
		name := ""
		if txn.SecurityISIN.Valid {
			isin = txn.SecurityISIN.String
		}
		if txn.Counterparty.Valid {
			name = txn.Counterparty.String
		}
		if isin != "" {
			securityMap[isin] += amt
			if name != "" {
				securityNames[isin] = name
			}
		}
	}

	// Sort months chronologically
	var months []string
	for m := range monthlyMap {
		months = append(months, m)
	}
	sortStrings(months)

	var monthly []monthlyDividend
	for _, m := range months {
		monthly = append(monthly, monthlyDividend{Month: m, Amount: monthlyMap[m]})
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

	// Build yearly aggregation from monthly data
	type yearlyDividend struct {
		Year   string  `json:"year"`
		Amount float64 `json:"amount"`
	}
	yearlyMap := make(map[string]float64)
	for m, amt := range monthlyMap {
		year := m[:4] // "2024-03" -> "2024"
		yearlyMap[year] += amt
	}
	var years []string
	for y := range yearlyMap {
		years = append(years, y)
	}
	sortStrings(years)
	var yearly []yearlyDividend
	for _, y := range years {
		yearly = append(yearly, yearlyDividend{Year: y, Amount: yearlyMap[y]})
	}

	if monthly == nil {
		monthly = []monthlyDividend{}
	}
	if yearly == nil {
		yearly = []yearlyDividend{}
	}
	if bySecurity == nil {
		bySecurity = []dividendBySecurity{}
	}

	// Compute advanced dividend metrics
	now := time.Now()
	trailing12m := 0.0
	prior12m := 0.0
	cutoff12m := now.AddDate(-1, 0, 0)
	cutoff24m := now.AddDate(-2, 0, 0)
	for _, txn := range txns {
		if txn.Type != "dividend" {
			continue
		}
		amt := 0.0
		if txn.Amount.Valid {
			f, _ := txn.Amount.Float64Value()
			amt = convertAmountToEURAtDate(f.Float64, txn.Currency, txn.Date, histFX, fxRates)
		}
		if txn.Date.After(cutoff12m) {
			trailing12m += amt
		} else if txn.Date.After(cutoff24m) {
			prior12m += amt
		}
	}

	// Yield on cost: trailing 12m dividends / total EUR cost basis
	totalCostBasis := 0.0
	holdings, _ := h.queries.ListCurrentHoldings(r.Context())
	for _, hold := range holdings {
		if hold.AvgCostBasis.Valid && hold.Quantity.Valid {
			cb, _ := hold.AvgCostBasis.Float64Value()
			qty, _ := hold.Quantity.Float64Value()
			totalCostBasis += convertAmountToEUR(cb.Float64*qty.Float64, hold.Currency, fxRates)
		}
	}
	yieldOnCost := 0.0
	if totalCostBasis > 0 {
		yieldOnCost = (trailing12m / totalCostBasis) * 100
	}

	// Dividend growth YoY
	dividendGrowth := 0.0
	if prior12m > 0 {
		dividendGrowth = ((trailing12m - prior12m) / prior12m) * 100
	}

	// Cumulative series for the chart — must be monotonic non-decreasing so
	// the line never visually dips. Negative monthly amounts (rare: a
	// dividend reversal or import correction) keep the monthly bar as
	// recorded but don't pull the cumulative back. The cumulative reflects
	// "total dividends received to date" — a payback in one month doesn't
	// erase the history of payouts before it.
	type cumulativePoint struct {
		Month      string  `json:"month"`
		Amount     float64 `json:"amount"`
		Cumulative float64 `json:"cumulative"`
	}
	cumTotal := 0.0
	var cumulative []cumulativePoint
	for _, m := range monthly {
		if m.Amount > 0 {
			cumTotal += m.Amount
		}
		cumulative = append(cumulative, cumulativePoint{
			Month:      m.Month,
			Amount:     m.Amount,
			Cumulative: math.Round(cumTotal*100) / 100,
		})
	}
	if cumulative == nil {
		cumulative = []cumulativePoint{}
	}

	// Dividend calendar: project upcoming payments based on historical pattern
	type calendarEntry struct {
		Month    string  `json:"month"`
		ISIN     string  `json:"isin"`
		Name     string  `json:"name"`
		Expected float64 `json:"expected"` // average historical amount for this month
	}
	// Build per-security monthly payment history
	type secMonth struct {
		isin  string
		month int // 1-12
	}
	secMonthAmounts := make(map[secMonth][]float64)
	for _, txn := range txns {
		if txn.Type != "dividend" || !txn.SecurityISIN.Valid {
			continue
		}
		amt := 0.0
		if txn.Amount.Valid {
			f, _ := txn.Amount.Float64Value()
			amt = convertAmountToEURAtDate(f.Float64, txn.Currency, txn.Date, histFX, fxRates)
		}
		key := secMonth{isin: txn.SecurityISIN.String, month: int(txn.Date.Month())}
		secMonthAmounts[key] = append(secMonthAmounts[key], amt)
	}
	// Only project dividends for currently-held securities
	currentHoldings, _ := h.queries.ListCurrentHoldings(r.Context())
	heldISINs := make(map[string]bool)
	for _, hld := range currentHoldings {
		heldISINs[hld.SecurityISIN] = true
	}

	// Project next 12 months (normalized to day 1 to avoid March-31 overflow)
	calNow := time.Now()
	var calendar []calendarEntry
	for m := 0; m < 12; m++ {
		futureDate := addMonthsFirstOfMonth(calNow, m+1)
		monthLabel := futureDate.Format("2006-01")
		monthNum := int(futureDate.Month())
		for _, sec := range bySecurity {
			if !heldISINs[sec.ISIN] {
				continue // skip sold positions
			}
			key := secMonth{isin: sec.ISIN, month: monthNum}
			amounts := secMonthAmounts[key]
			if len(amounts) == 0 {
				continue
			}
			avg := 0.0
			for _, a := range amounts { avg += a }
			avg /= float64(len(amounts))
			calendar = append(calendar, calendarEntry{
				Month:    monthLabel,
				ISIN:     sec.ISIN,
				Name:     sec.Name,
				Expected: math.Round(avg*100) / 100,
			})
		}
	}
	if calendar == nil {
		calendar = []calendarEntry{}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"total":           totalDividends,
		"monthly":         monthly,
		"yearly":          yearly,
		"by_security":     bySecurity,
		"cumulative":      cumulative,
		"trailing_12m":    math.Round(trailing12m*100) / 100,
		"yield_on_cost":   math.Round(yieldOnCost*100) / 100,
		"dividend_growth": math.Round(dividendGrowth*100) / 100,
		"calendar":        calendar,
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

// loadBenchmarkPrices loads benchmark prices from DB, falling back to Yahoo Finance.
func loadBenchmarkPrices(ctx context.Context, q *db.Queries, isin, ticker string) []benchmarkPrice {
	if isin == "" {
		isin = benchmarkISIN
	}
	if ticker == "" {
		ticker = benchmarkTicker
	}
	rows, err := q.ListPriceHistory(ctx, isin)
	if err == nil && len(rows) > 0 {
		var prices []benchmarkPrice
		for _, r := range rows {
			if r.Close.Valid {
				f, _ := r.Close.Float64Value()
				prices = append(prices, benchmarkPrice{date: r.Date, price: f.Float64})
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
		prices = append(prices, benchmarkPrice{date: h.Date, price: h.Close})
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
	for _, hold := range holdings {
		qty := 0.0
		if hold.Quantity.Valid {
			f, _ := hold.Quantity.Float64Value()
			qty = f.Float64
		}
		if qty <= 0 {
			continue
		}
		value := holdingValueEURFromMap(hold, pm, fxRates)
		if existing, ok := aggregated[hold.SecurityISIN]; ok {
			existing.MarketValue += value
		} else {
			name := hold.SecurityISIN
			if sec, err := h.queries.GetSecurity(ctx, hold.SecurityISIN); err == nil {
				name = sec.Name
			}
			aggregated[hold.SecurityISIN] = &holdingInfo{
				ISIN:        hold.SecurityISIN,
				Name:        name,
				MarketValue: value,
			}
		}
		totalValue += value
	}
	var holdingInfos []holdingInfo
	for _, hi := range aggregated {
		holdingInfos = append(holdingInfos, *hi)
	}

	// Build target map
	targetMap := make(map[string]float64)
	for _, t := range targets {
		targetMap[t.SecurityISIN] = numericToFloat(t.TargetWeightPct)
	}

	type allocationEntry struct {
		ISIN      string  `json:"isin"`
		Name      string  `json:"name"`
		ActualPct float64 `json:"actual_pct"`
		TargetPct float64 `json:"target_pct"`
		DriftPct  float64 `json:"drift_pct"`
		Value     float64 `json:"value"`
		Status    string  `json:"status"` // "on_target", "underweight", "overweight"
	}

	var entries []allocationEntry
	for _, hi := range holdingInfos {
		actualPct := 0.0
		if totalValue > 0 {
			actualPct = (hi.MarketValue / totalValue) * 100
		}
		targetPct := targetMap[hi.ISIN]
		drift := actualPct - targetPct

		status := "on_target"
		if targetPct > 0 {
			if drift > 2 {
				status = "overweight"
			} else if drift < -2 {
				status = "underweight"
			}
		}

		entries = append(entries, allocationEntry{
			ISIN:      hi.ISIN,
			Name:      hi.Name,
			ActualPct: math.Round(actualPct*10) / 10,
			TargetPct: targetPct,
			DriftPct:  math.Round(drift*10) / 10,
			Value:     math.Round(hi.MarketValue*100) / 100,
			Status:    status,
		})
	}

	// Check for target ISINs not in holdings (target but no position)
	holdingISINs := make(map[string]bool)
	for _, hi := range holdingInfos {
		holdingISINs[hi.ISIN] = true
	}
	for _, t := range targets {
		if !holdingISINs[t.SecurityISIN] {
			entries = append(entries, allocationEntry{
				ISIN:      t.SecurityISIN,
				Name:      t.SecurityName,
				ActualPct: 0,
				TargetPct: numericToFloat(t.TargetWeightPct),
				DriftPct:  -numericToFloat(t.TargetWeightPct),
				Status:    "underweight",
			})
		}
	}

	if entries == nil {
		entries = []allocationEntry{}
	}

	hasTargets := len(targets) > 0
	maxDrift := 0.0
	for _, e := range entries {
		if math.Abs(e.DriftPct) > math.Abs(maxDrift) {
			maxDrift = e.DriftPct
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"allocations": entries,
		"has_targets": hasTargets,
		"max_drift":   math.Round(maxDrift*10) / 10,
		"total_value": math.Round(totalValue*100) / 100,
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

	// Get current net worth
	snaps, err := h.queries.ListNetWorthSnapshots(ctx, 1)
	if err != nil || len(snaps) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{"goals": []any{}})
		return
	}
	currentValue := 0.0
	if snaps[0].Total.Valid {
		f, _ := snaps[0].Total.Float64Value()
		currentValue = f.Float64
	}

	now := time.Now()

	type goalProgress struct {
		ID                  string  `json:"id"`
		Name                string  `json:"name"`
		TargetAmount        float64 `json:"target_amount"`
		TargetDate          string  `json:"target_date"`
		CurrentValue        float64 `json:"current_value"`
		ProgressPct         float64 `json:"progress_pct"`
		ProjectedValue      float64 `json:"projected_value"`
		MonthlyContribution float64 `json:"monthly_contribution"`
		AssumedReturnPct    float64 `json:"assumed_return_pct"`
		Status              string  `json:"status"` // "on_track", "behind", "complete", "ahead"
		MonthsRemaining     int     `json:"months_remaining"`
	}

	var results []goalProgress
	for _, g := range goals {
		target := numericToFloat(g.TargetAmount)
		monthly := numericToFloat(g.MonthlyContribution)
		annualReturn := numericToFloat(g.AssumedReturnPct) / 100.0

		// Months until target date
		months := int(g.TargetDate.Sub(now).Hours() / (24 * 30.44))
		if months < 0 {
			months = 0
		}

		// Project future value: compound growth + monthly contributions
		// FV = PV × (1+r)^n + PMT × ((1+r)^n - 1) / r
		if annualReturn <= -0.9999 {
			annualReturn = -0.9999
		}
		monthlyReturn := math.Pow(1+annualReturn, 1.0/12.0) - 1
		n := float64(months)
		projected := currentValue
		if math.Abs(monthlyReturn) > 1e-9 && n > 0 {
			growthFactor := math.Pow(1+monthlyReturn, n)
			if !math.IsNaN(growthFactor) && !math.IsInf(growthFactor, 0) {
				projected = currentValue*growthFactor + monthly*(growthFactor-1)/monthlyReturn
			}
		} else if n > 0 {
			projected = currentValue + monthly*n
		}
		if math.IsNaN(projected) || math.IsInf(projected, 0) {
			projected = currentValue
		}

		progressPct := 0.0
		if target > 0 {
			progressPct = (currentValue / target) * 100
		}

		status := "on_track"
		if currentValue >= target {
			status = "complete"
		} else if months == 0 {
			status = "behind"
		} else if projected >= target*1.1 {
			status = "ahead"
		} else if projected < target*0.8 {
			status = "behind"
		}

		results = append(results, goalProgress{
			ID:                  g.ID.String(),
			Name:                g.Name,
			TargetAmount:        target,
			TargetDate:          g.TargetDate.Format("2006-01-02"),
			CurrentValue:        math.Round(currentValue*100) / 100,
			ProgressPct:         math.Round(progressPct*10) / 10,
			ProjectedValue:      math.Round(projected*100) / 100,
			MonthlyContribution: monthly,
			AssumedReturnPct:    numericToFloat(g.AssumedReturnPct),
			Status:              status,
			MonthsRemaining:     months,
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{"goals": results})
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

// computeRebalanceTrades suggests trades to bring the portfolio toward its
// target allocation. With deposit > 0, only buys are emitted — every trade
// flows new cash into an underweight position, proportional to the EUR gap
// to target. With deposit == 0, both buys and sells are emitted to reduce
// max drift across all weighted positions.
//
// Invariants the handler / tests rely on:
//   - deposit > 0 → no Action == "sell" in the output (never sells when
//     allocating fresh cash).
//   - Trades are only emitted above the EUR 50 (full) / EUR 10 (deposit-
//     proportional, after share rounding) thresholds.
func computeRebalanceTrades(holdings map[string]rebalanceHolding, targets map[string]float64, totalValue, deposit float64) []rebalanceTrade {
	var trades []rebalanceTrade

	if deposit > 0 {
		effectiveTotal := totalValue + deposit
		type deficit struct {
			isin, name string
			price, gap float64
		}
		var deficits []deficit
		totalGap := 0.0
		for isin, targetPct := range targets {
			targetValue := effectiveTotal * targetPct / 100
			currentValue := 0.0
			name := isin
			price := 0.0
			if h, ok := holdings[isin]; ok {
				currentValue = h.Value
				name = h.Name
				price = h.Price
			}
			gap := targetValue - currentValue
			if gap > 0 {
				deficits = append(deficits, deficit{isin: isin, name: name, price: price, gap: gap})
				totalGap += gap
			}
		}
		for _, d := range deficits {
			if totalGap <= 0 {
				continue
			}
			allocation := deposit * (d.gap / totalGap)
			if allocation < 1 {
				continue
			}
			shares := 0.0
			if d.price > 0 {
				shares = math.Floor(allocation / d.price)
				allocation = shares * d.price
			}
			currentPct := 0.0
			if h, ok := holdings[d.isin]; ok && totalValue > 0 {
				currentPct = (h.Value / totalValue) * 100
			}
			trades = append(trades, rebalanceTrade{
				ISIN:       d.isin,
				Name:       d.name,
				Action:     "buy",
				Amount:     math.Round(allocation*100) / 100,
				Shares:     shares,
				CurrentPct: math.Round(currentPct*10) / 10,
				TargetPct:  targets[d.isin],
			})
		}
		return trades
	}

	// Full rebalance — emit both buys and sells to drag drift toward zero.
	for isin, targetPct := range targets {
		targetValue := totalValue * targetPct / 100
		currentValue := 0.0
		name := isin
		price := 0.0
		if h, ok := holdings[isin]; ok {
			currentValue = h.Value
			name = h.Name
			price = h.Price
		}
		diff := targetValue - currentValue
		currentPct := 0.0
		if totalValue > 0 {
			currentPct = (currentValue / totalValue) * 100
		}
		if math.Abs(diff) < 50 {
			continue
		}
		action := "buy"
		if diff < 0 {
			action = "sell"
			diff = -diff
		}
		shares := 0.0
		if price > 0 {
			shares = math.Floor(diff / price)
			diff = shares * price
		}
		if diff < 10 {
			continue
		}
		trades = append(trades, rebalanceTrade{
			ISIN:       isin,
			Name:       name,
			Action:     action,
			Amount:     math.Round(diff*100) / 100,
			Shares:     shares,
			CurrentPct: math.Round(currentPct*10) / 10,
			TargetPct:  targetPct,
		})
	}
	return trades
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

	// Aggregate holdings by ISIN (in EUR)
	type holdInfo struct {
		isin, name string
		value      float64
		price      float64
	}
	agg := make(map[string]*holdInfo)
	totalValue := 0.0
	for _, hold := range holdings {
		qty := 0.0
		if hold.Quantity.Valid {
			f, _ := hold.Quantity.Float64Value()
			qty = f.Float64
		}
		if qty <= 0 {
			continue
		}
		price := 0.0
		if p, ok := pm[hold.SecurityISIN]; ok && p.Close.Valid {
			f, _ := p.Close.Float64Value()
			price = convertAmountToEUR(f.Float64, p.Currency, fxRates)
		}
		value := qty * price
		if price == 0 {
			if hold.AvgCostBasis.Valid {
				f, _ := hold.AvgCostBasis.Float64Value()
				value = convertAmountToEUR(qty*f.Float64, hold.Currency, fxRates)
			}
		}
		if existing, ok := agg[hold.SecurityISIN]; ok {
			existing.value += value
		} else {
			name := hold.SecurityISIN
			if sec, err := h.queries.GetSecurity(ctx, hold.SecurityISIN); err == nil {
				name = sec.Name
			}
			agg[hold.SecurityISIN] = &holdInfo{isin: hold.SecurityISIN, name: name, value: value, price: price}
		}
		totalValue += value
	}

	// Build target map
	targetMap := make(map[string]float64)
	for _, t := range targets {
		targetMap[t.SecurityISIN] = numericToFloat(t.TargetWeightPct)
	}

	// Parse deposit amount (buy-only mode)
	depositAmount := 0.0
	if d := r.URL.Query().Get("deposit"); d != "" {
		if n, err := parseFiniteFloat(d); err == nil && n > 0 && n <= 1e12 {
			depositAmount = n
		}
	}

	// Convert the local aggregation map into the algorithm's input shape.
	algorithmHoldings := make(map[string]rebalanceHolding, len(agg))
	for isin, h := range agg {
		algorithmHoldings[isin] = rebalanceHolding{ISIN: h.isin, Name: h.name, Value: h.value, Price: h.price}
	}
	trades := computeRebalanceTrades(algorithmHoldings, targetMap, totalValue, depositAmount)
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
		"trades":  trades,
		"message": msg,
		"deposit": depositAmount,
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
	trailing := trailingNetContribution(txns, acctTypes, now, histFX, fxRates)

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
		then := trailingNetContribution(txns, acctTypes, s.Date, histFX, fxRates)
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

// HandleAttribution computes per-holding contribution to daily change.
func (h *PortfolioHandler) HandleAttribution(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	holdings, err := h.queries.ListCurrentHoldings(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list holdings: "+err.Error())
		return
	}

	secs, _ := h.queries.ListSecurities(ctx)
	secNames := make(map[string]string)
	for _, s := range secs {
		secNames[s.ISIN] = s.Name
	}

	type contribution struct {
		ISIN   string  `json:"isin"`
		Name   string  `json:"name"`
		Change float64 `json:"change"`
	}

	// Aggregate by ISIN
	agg := make(map[string]float64) // ISIN -> qty
	for _, h := range holdings {
		if h.Quantity.Valid {
			f, _ := h.Quantity.Float64Value()
			agg[h.SecurityISIN] += f.Float64
		}
	}

	// Estimate daily change per holding from price history (converted to EUR)
	fxRates := loadFXRatesMap(ctx, h.queries)
	contribs := []contribution{} // never nil: serialises as [] so clients can read .length
	totalChange := 0.0
	for isin, qty := range agg {
		priceRows, err := h.queries.ListPriceHistory(ctx, isin)
		if err != nil || len(priceRows) < 2 {
			continue
		}
		latest := priceRows[len(priceRows)-1]
		prev := priceRows[len(priceRows)-2]
		if !latest.Close.Valid || !prev.Close.Valid {
			continue
		}
		lf, _ := latest.Close.Float64Value()
		pf, _ := prev.Close.Float64Value()
		latestEUR := convertAmountToEUR(lf.Float64, latest.Currency, fxRates)
		prevEUR := convertAmountToEUR(pf.Float64, prev.Currency, fxRates)
		change := (latestEUR - prevEUR) * qty
		totalChange += change
		name := secNames[isin]
		if name == "" {
			name = isin
		}
		contribs = append(contribs, contribution{
			ISIN: isin, Name: name, Change: math.Round(change*100) / 100,
		})
	}

	// Sort by absolute contribution descending
	sort.Slice(contribs, func(i, j int) bool {
		return math.Abs(contribs[i].Change) > math.Abs(contribs[j].Change)
	})

	// Generate summary sentence
	summary := ""
	if len(contribs) > 0 {
		top := contribs[0]
		if len(contribs) >= 2 {
			second := contribs[1]
			if top.Change >= 0 && second.Change >= 0 {
				summary = fmt.Sprintf("Driven by %s (+%s EUR) and %s (+%s EUR)", top.Name, fmtEUR(top.Change), second.Name, fmtEUR(second.Change))
			} else if top.Change >= 0 && second.Change < 0 {
				summary = fmt.Sprintf("Driven by %s (+%s EUR), offset by %s (-%s EUR)", top.Name, fmtEUR(top.Change), second.Name, fmtEUR(-second.Change))
			} else {
				summary = fmt.Sprintf("Dragged by %s (-%s EUR) and %s (-%s EUR)", top.Name, fmtEUR(-top.Change), second.Name, fmtEUR(-second.Change))
			}
		} else {
			summary = fmt.Sprintf("Driven by %s (%+.0f EUR)", top.Name, top.Change)
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"contributions": contribs,
		"total_change":  math.Round(totalChange*100) / 100,
		"summary":       summary,
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
	price := convertAmountToEUR(numericToFloat(pm[isin].Close), pm[isin].Currency, fxRates)

	for _, hld := range holdings {
		if hld.SecurityISIN != isin {
			continue
		}
		qty := numericToFloat(hld.Quantity)
		cost := convertAmountToEUR(numericToFloat(hld.AvgCostBasis)*qty, hld.Currency, fxRates)
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
		sparkline = append([]pricePoint{{
			Date:  priceHistory[i].Date.Format("2006-01-02"),
			Price: convertAmountToEUR(numericToFloat(priceHistory[i].Close), priceHistory[i].Currency, fxRates),
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
			amt = convertAmountToEURAtDate(f.Float64, t.Currency, t.Date, histFX, fxRates)
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

	// First buy date
	firstBuy := ""
	for _, t := range txnHistory {
		if t.Type == "buy" || t.Type == "savings_plan" {
			firstBuy = t.Date
			break
		}
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
		"total_cost":    math.Round(totalCost*100) / 100,
		"total_value":   math.Round(totalValue*100) / 100,
		"unrealized_pl": math.Round(unrealizedPL*100) / 100,
		"weight_pct":    math.Round(weightPct*10) / 10,
		"first_buy":     firstBuy,
		"sparkline":     sparkline,
		"transactions":  txnHistory,
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
	}
	var actions []action

	now := time.Now()
	monthsElapsed := float64(now.Month())
	if monthsElapsed < 1 {
		monthsElapsed = 1
	}

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
	for isin, li := range lossByISIN {
		if li.totalPL >= -100 || li.costBasis <= 0 {
			continue
		}
		lossPct := li.totalPL / li.costBasis * 100
		if lossPct > -5 {
			continue
		}
		taxableGain := math.Abs(li.totalPL) * (1 - tfsMap[isin])
		saving := taxableGain * analytics.EffectiveTaxRate
		actions = append(actions, action{
			Title:    "Harvest tax loss: " + li.name,
			Detail:   fmt.Sprintf("Down %s EUR (%.1f%%). Selling and rebuying could save ~%s EUR in tax.", fmtEUR(li.totalPL), lossPct, fmtEUR(saving)),
			Impact:   math.Round(saving),
			Urgency:  "this-quarter",
			Category: "tax",
			Link:     "/analysis",
		})
	}

	// --- 2. Loss Pot Netting (carry-forward losses that can offset future gains) ---
	lossPots := analytics.ComputeLossPots(taxTxns)
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
			taxSaved := unusedLoss * analytics.EffectiveTaxRate
			actions = append(actions, action{
				Title:    "Use loss carry-forward",
				Detail:   fmt.Sprintf("%s EUR in carry-forward losses available. Realizing gains up to this amount would be tax-free (saves ~%s EUR).", fmtEUR(unusedLoss), fmtEUR(taxSaved)),
				Impact:   math.Round(taxSaved),
				Urgency:  "this-quarter",
				Category: "tax",
				Link:     "/analysis",
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
	projected := ytdIncome / monthsElapsed * 12
	if projected < analytics.Sparerpauschbetrag*0.8 {
		unused := analytics.Sparerpauschbetrag - projected
		forfeited := unused * analytics.EffectiveTaxRate
		urgency := "this-quarter"
		if now.Month() >= 10 {
			urgency = "this-month" // year-end approaching
		}
		actions = append(actions, action{
			Title:    "Optimize Freistellungsauftrag",
			Detail:   fmt.Sprintf("Projected %s EUR of 1.000 EUR used. ~%s EUR in tax-free allowance may be wasted.", fmtEUR(projected), fmtEUR(unused)),
			Impact:   math.Round(forfeited),
			Urgency:  urgency,
			Category: "tax",
			Link:     "/analysis",
		})
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
	if now.Month() >= 10 {
		fundValue := 0.0
		basiszins, hasBasiszins := analytics.BasiszinsByYear[now.Year()]
		if !hasBasiszins {
			basiszins = 2.5
		}
		tax := 0.0
		if basiszins > 0 {
			for _, hv := range hvs {
				sec := secMap[hv.isin]
				if sec.AssetClass == "etf" || sec.AssetClass == "fund" {
					fundValue += hv.value
					basisertrag := hv.value * basiszins / 100 * 0.7
					tax += basisertrag * (1 - tfsMap[hv.isin]) * analytics.EffectiveTaxRate
				}
			}
		}
		if fundValue > 10000 && tax > 10 {
			actions = append(actions, action{
				Title:    "Prepare for Vorabpauschale",
				Detail:   fmt.Sprintf("~%s EUR in Vorabpauschale tax due Jan 2. Ensure sufficient cash in brokerage account.", fmtEUR(tax)),
				Impact:   math.Round(tax),
				Urgency:  "this-month",
				Category: "tax",
				Link:     "/analysis",
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

// HandleSavingsPlans detects recurring buy patterns and computes DCA metrics.
func (h *PortfolioHandler) HandleSavingsPlans(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	txns, err := listAllTransactionsRows(ctx, h.queries)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list transactions: "+err.Error())
		return
	}
	fxRates := loadFXRatesMap(ctx, h.queries)
	histFX := loadHistoricalFXRates(ctx, h.queries)

	secs, _ := h.queries.ListSecurities(ctx)
	secNames := make(map[string]string)
	for _, s := range secs {
		secNames[s.ISIN] = s.Name
	}

	// Group buy/savings_plan transactions by ISIN and month
	type monthBuy struct {
		month  string
		amount float64
		qty    float64
		date   time.Time
	}
	isinBuys := make(map[string][]monthBuy)
	for _, txn := range txns {
		if txn.Type != "buy" && txn.Type != "savings_plan" {
			continue
		}
		if !txn.SecurityISIN.Valid {
			continue
		}
		isin := txn.SecurityISIN.String
		amt, qty := 0.0, 0.0
		if txn.Amount.Valid {
			f, _ := txn.Amount.Float64Value()
			amt = convertAmountToEURAtDate(f.Float64, txn.Currency, txn.Date, histFX, fxRates)
		}
		if txn.Quantity.Valid {
			f, _ := txn.Quantity.Float64Value()
			qty = f.Float64
		}
		isinBuys[isin] = append(isinBuys[isin], monthBuy{
			month: txn.Date.Format("2006-01"), amount: amt, qty: qty, date: txn.Date,
		})
	}

	// Load current prices for DCA comparison
	pm := h.loadPriceMap(ctx)

	type sparplan struct {
		ISIN          string  `json:"isin"`
		Name          string  `json:"name"`
		MonthlyAmount float64 `json:"monthly_amount"`
		Executions    int     `json:"executions"`
		TotalInvested float64 `json:"total_invested"`
		TotalShares   float64 `json:"total_shares"`
		AvgCostBasis  float64 `json:"avg_cost_basis"`
		FirstDate     string  `json:"first_date"`
		LastDate      string  `json:"last_date"`
		MonthsActive  int     `json:"months_active"`
		// DCA analysis
		CurrentValue  float64 `json:"current_value"`
		DCAReturn     float64 `json:"dca_return_pct"`    // actual DCA return %
		LumpSumValue  float64 `json:"lump_sum_value"`    // value if all invested on first date
		LumpSumReturn float64 `json:"lump_sum_return_pct"`
		DCAAdvantage  float64 `json:"dca_advantage_eur"` // DCA value - lump sum value
	}

	var plans []sparplan
	totalMonthly := 0.0

	for isin, buys := range isinBuys {
		if len(buys) < 3 {
			continue // need at least 3 buys to detect a pattern
		}

		// Sort chronologically
		sort.Slice(buys, func(i, j int) bool { return buys[i].date.Before(buys[j].date) })

		// Count unique months
		months := make(map[string]bool)
		totalAmt, totalQty := 0.0, 0.0
		for _, b := range buys {
			months[b.month] = true
			totalAmt += b.amount
			totalQty += b.qty
		}

		if len(months) < 3 {
			continue
		}

		// Compute monthly average
		avgMonthly := totalAmt / float64(len(months))
		avgCost := 0.0
		if totalQty > 0 {
			avgCost = totalAmt / totalQty
		}

		name := secNames[isin]
		if name == "" {
			name = isin
		}

		// DCA comparison: current value vs lump-sum
		currentPrice := convertAmountToEUR(numericToFloat(pm[isin].Close), pm[isin].Currency, fxRates)
		currentValue := totalQty * currentPrice
		dcaReturnPct := 0.0
		if totalAmt > 0 && currentPrice > 0 {
			dcaReturnPct = ((currentValue - totalAmt) / totalAmt) * 100
		}

		// Lump-sum: if all money invested at first buy price
		firstPrice := 0.0
		if len(buys) > 0 && buys[0].qty > 0 {
			firstPrice = buys[0].amount / buys[0].qty
		}
		lumpSumShares := 0.0
		if firstPrice > 0 {
			lumpSumShares = totalAmt / firstPrice
		}
		lumpSumValue := lumpSumShares * currentPrice
		lumpSumReturnPct := 0.0
		if totalAmt > 0 && lumpSumValue > 0 {
			lumpSumReturnPct = ((lumpSumValue - totalAmt) / totalAmt) * 100
		}

		plans = append(plans, sparplan{
			ISIN:          isin,
			Name:          name,
			MonthlyAmount: math.Round(avgMonthly*100) / 100,
			Executions:    len(buys),
			TotalInvested: math.Round(totalAmt*100) / 100,
			TotalShares:   math.Round(totalQty*1e8) / 1e8,
			AvgCostBasis:  math.Round(avgCost*100) / 100,
			FirstDate:     buys[0].date.Format("2006-01-02"),
			LastDate:      buys[len(buys)-1].date.Format("2006-01-02"),
			MonthsActive:  len(months),
			CurrentValue:  math.Round(currentValue*100) / 100,
			DCAReturn:     math.Round(dcaReturnPct*10) / 10,
			LumpSumValue:  math.Round(lumpSumValue*100) / 100,
			LumpSumReturn: math.Round(lumpSumReturnPct*10) / 10,
			DCAAdvantage:  math.Round((currentValue-lumpSumValue)*100) / 100,
		})
		totalMonthly += avgMonthly
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
			if targetPct <= 0 { continue }
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
	if suggestions == nil { suggestions = []rebalSuggestion{} }

	writeJSON(w, http.StatusOK, map[string]any{
		"plans":          plans,
		"total_monthly":  math.Round(totalMonthly*100) / 100,
		"plan_count":     len(plans),
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
		monthlyContrib = trailingNetContribution(txns, h.accountTypesByID(ctx), now, histFX, fxRates).Monthly
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
		monthlyContrib = trailingNetContribution(txns, h.accountTypesByID(ctx), now, histFX, fxRates).Monthly
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

	// Find months with savings_plan or regular deposit transactions
	monthlyInvest := make(map[string]float64) // YYYY-MM -> total invested
	for _, txn := range txns {
		if txn.Type != "savings_plan" && txn.Type != "buy" {
			continue
		}
		amt := 0.0
		if txn.Amount.Valid {
			f, _ := txn.Amount.Float64Value()
			amt = convertAmountToEURAtDate(f.Float64, txn.Currency, txn.Date, histFX, fxRates)
		}
		month := txn.Date.Format("2006-01")
		monthlyInvest[month] += amt
	}

	if len(monthlyInvest) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{
			"current_streak":  0,
			"longest_streak":  0,
			"total_months":    0,
			"consistency_pct": 0,
		})
		return
	}

	// Build sorted month list from first to last
	var months []string
	for m := range monthlyInvest {
		months = append(months, m)
	}
	sort.Strings(months)

	firstMonth, _ := time.Parse("2006-01", months[0])
	lastMonth, _ := time.Parse("2006-01", months[len(months)-1])

	// Generate all months in range
	var allMonths []string
	for m := firstMonth; !m.After(lastMonth); m = addMonthsFirstOfMonth(m, 1) {
		allMonths = append(allMonths, m.Format("2006-01"))
	}

	// Compute streaks
	currentStreak := 0
	longestStreak := 0
	streak := 0
	activeMonths := 0
	for _, m := range allMonths {
		if monthlyInvest[m] > 0 {
			streak++
			activeMonths++
			if streak > longestStreak {
				longestStreak = streak
			}
		} else {
			streak = 0
		}
	}
	currentStreak = streak // streak at the end of the range

	// Consistency percentage
	consistencyPct := 0.0
	if len(allMonths) > 0 {
		consistencyPct = float64(activeMonths) / float64(len(allMonths)) * 100
	}

	// Compute cost of missed months (opportunity cost)
	totalInvested := 0.0
	for _, amt := range monthlyInvest {
		totalInvested += amt
	}
	avgMonthly := 0.0
	if activeMonths > 0 {
		avgMonthly = totalInvested / float64(activeMonths)
	}
	missedMonths := len(allMonths) - activeMonths

	// Opportunity cost: missed_months × avg_monthly × portfolio_return_since
	missedCost := 0.0
	if missedMonths > 0 {
		annualReturn := 0.07
		monthlyReturn := math.Pow(1+annualReturn, 1.0/12.0) - 1
		// Each missed month's contribution would have grown
		for i := 0; i < missedMonths; i++ {
			// Average remaining months after a missed investment
			remainingMonths := float64(len(allMonths)) / 2
			missedCost += avgMonthly * (math.Pow(1+monthlyReturn, remainingMonths) - 1)
		}
	}

	// Monthly investment history for chart
	type monthPoint struct {
		Month  string  `json:"month"`
		Amount float64 `json:"amount"`
		Active bool    `json:"active"`
	}
	history := make([]monthPoint, 0)
	for _, m := range allMonths {
		amt := monthlyInvest[m]
		history = append(history, monthPoint{Month: m, Amount: math.Round(amt*100) / 100, Active: amt > 0})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"current_streak":   currentStreak,
		"longest_streak":   longestStreak,
		"total_months":     len(allMonths),
		"active_months":    activeMonths,
		"missed_months":    missedMonths,
		"consistency_pct":  math.Round(consistencyPct*10) / 10,
		"avg_monthly":      math.Round(avgMonthly*100) / 100,
		"missed_cost":      math.Round(missedCost),
		"total_invested":   math.Round(totalInvested*100) / 100,
		"history":          history,
	})
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
	allSnaps, _ := h.queries.ListNetWorthSnapshots(ctx, 5000)
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
	monthPhysicalEvents := physical.monthly()
	for m, t := range monthPhysicalEvents {
		monthContrib[m] += t.contrib
	}
	for _, ev := range impEvents {
		monthContrib[ev.Date.Format("2006-01")] += ev.DeltaEUR
	}
	for _, txn := range txns {
		if _, isWash := washed[txn.ID]; isWash {
			continue
		}
		if physical.linked[txn.ID] {
			continue
		}
		m := txn.Date.Format("2006-01")
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
		}
	}

	monthSet := make(map[string]bool)
	for m := range monthNW {
		monthSet[m] = true
	}
	for m := range monthContrib {
		monthSet[m] = true
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
		Total         float64 `json:"total"`
	}

	timeSeries := make([]monthAttrib, 0)
	cumContrib := 0.0
	cumDiv := 0.0
	cumInt := 0.0
	cumCapital := 0.0
	cumCosts := 0.0
	crossoverMonth := ""
	for _, m := range months {
		cumContrib += monthContrib[m]
		cumDiv += monthDiv[m]
		cumInt += monthInt[m]
		cumCapital += monthPhysicalEvents[m].capital
		cumCosts += monthPhysicalEvents[m].costs + monthPhysicalEvents[m].interest
		nw := monthNW[m]
		if nw == 0 {
			continue
		}
		cumAssets := monthPhysical[m] - cumCapital
		// Market return is the residual that closes the identity: whatever
		// portion of NW isn't explained by contributions/dividends/interest is
		// market+fees+taxes (folded together because fees/taxes aren't broken
		// out per month here). This guarantees the stacked area sums to the
		// actual NW snapshot, instead of drifting due to missing price data.
		cumMR := nw - cumContrib - cumDiv - cumInt - cumAssets + cumCosts
		timeSeries = append(timeSeries, monthAttrib{
			Month: m, Contributions: math.Round(cumContrib),
			MarketReturn: math.Round(cumMR), Dividends: math.Round(cumDiv),
			Interest: math.Round(cumInt), Assets: math.Round(cumAssets),
			PropertyCosts: -math.Round(cumCosts), Total: math.Round(nw),
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
	// Identity: nwChange = contrib + market + div + int - |fees| - |taxes|.
	// market_return is the residual; locked to satisfy this identity within
	// FP tolerance so the response self-reconciles.
	marketReturn := nwChange - netContribSince - dividendsSince - interestSince + math.Abs(feesSince) + math.Abs(taxesSince) -
		assetChangeSince + since.costs + since.interest

	writeJSON(w, http.StatusOK, map[string]any{
		"date":             dateStr,
		"holdings":         snapHoldings,
		"holdings_count":   len(snapHoldings),
		"portfolio_value":  math.Round(totalValue*100) / 100,
		"net_worth":        math.Round(historicalNW),
		"total_cost_basis": math.Round((totalDeposits - totalWithdrawals) * 100) / 100,
		"current": map[string]any{
			"net_worth":       math.Round(currentNW),
			"portfolio_value": math.Round(currentValue*100) / 100,
		},
		"change": map[string]any{
			"net_worth_change": math.Round(nwChange),
			"net_worth_pct":    math.Round(nwChange/math.Max(historicalNW, 1)*10000) / 100,
			"contributions":    math.Round(netContribSince),
			"market_return":    math.Round(marketReturn),
			"assets":           math.Round(assetChangeSince),
			"purchase_costs":   -math.Round(since.costs),
			"loan_interest":    -math.Round(since.interest),
			"dividends":        math.Round(dividendsSince),
			"interest":         math.Round(interestSince),
			"fees":             math.Round(feesSince),
			"taxes":            math.Round(taxesSince),
		},
	})
}

// switchTaxCost computes the tax bite of selling out of a position with
// the given unrealizedGain, applying:
//
//   taxable_after_TFS = unrealized_gain × (1 − teilfreistellung)
//   taxable_after_FSA = max(0, taxable_after_TFS − remaining_freibetrag)
//   tax               = taxable_after_FSA × 26.375 %
//
// teilfreistellung is the asset-class fraction (0.30 for equity, 0.15 for
// mixed, 0 for bonds). remainingFreibetrag is the Sparerpauschbetrag balance
// for the year (1000 EUR minus already-used by dividends + interest +
// realized gains). Locked in a helper so the formula has a unit test that
// catches drift in any of the three multipliers.
func switchTaxCost(unrealizedGain, teilfreistellung, remainingFreibetrag float64) float64 {
	if unrealizedGain <= 0 {
		return 0
	}
	taxable := unrealizedGain * (1 - teilfreistellung)
	taxable = math.Max(0, taxable-remainingFreibetrag)
	return taxable * 0.26375
}

// remainingFSA returns the user's Sparerpauschbetrag balance for the current
// year — the 1000 EUR allowance minus already-realized taxable income from
// dividends, interest, and realized gains. Fund dividends and gains are
// multiplied by (1 − TFS) before counting using per-depot FIFO cost basis.
func (h *PortfolioHandler) remainingFSA(ctx context.Context) float64 {
	year := time.Now().Year()
	txns, _ := listAllTransactionsRows(ctx, h.queries)
	fxRates := loadFXRatesMap(ctx, h.queries)
	histFX := loadHistoricalFXRates(ctx, h.queries)
	secs, _ := h.queries.ListSecurities(ctx)
	tfsMap := make(map[string]float64, len(secs))
	for _, s := range secs {
		tfsMap[s.ISIN] = securityTFSRate(s)
	}
	type depotKey struct {
		accountID uuid.UUID
		isin      string
	}
	type fifoLot struct {
		qty     float64
		costPer float64
	}
	lots := make(map[depotKey][]fifoLot)
	income := 0.0
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
		dk := depotKey{accountID: txn.AccountID, isin: isin}
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
			rem := qty
			q := lots[dk]
			for len(q) > 0 && rem > 1e-9 {
				if q[0].qty <= rem+1e-9 {
					rem -= q[0].qty
					q = q[1:]
				} else {
					q[0].qty -= rem
					rem = 0
				}
			}
			lots[dk] = q
		case "sell":
			if isin == "" || qty <= 0 {
				continue
			}
			rem := qty
			costSold := 0.0
			q := lots[dk]
			for len(q) > 0 && rem > 1e-9 {
				if q[0].qty <= rem+1e-9 {
					costSold += q[0].qty * q[0].costPer
					rem -= q[0].qty
					q = q[1:]
				} else {
					costSold += rem * q[0].costPer
					q[0].qty -= rem
					rem = 0
				}
			}
			lots[dk] = q
			soldQty := qty - rem
			if txn.Date.Year() != year || soldQty <= 1e-9 {
				continue
			}
			proceeds := amt - fee
			if rem > 1e-9 {
				proceeds *= soldQty / qty
			}
			gain := proceeds - costSold
			if gain > 0 {
				income += gain * (1 - tfsMap[isin])
			}
		case "dividend":
			if txn.Date.Year() != year {
				continue
			}
			income += amt * (1 - tfsMap[isin])
		case "interest":
			if txn.Date.Year() != year {
				continue
			}
			income += amt
		}
	}
	used := math.Min(income, analytics.Sparerpauschbetrag)
	return analytics.Sparerpauschbetrag - used
}

// HandleSwitchCompare compares current holding vs alternative ISIN.
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
	histFX := loadHistoricalFXRates(ctx, h.queries)

	// Fetch historical prices from DB for both ISINs (converted to EUR)
	type priceEntry struct {
		Date  time.Time
		Close float64
	}
	fetchPrices := func(isin string) []priceEntry {
		rows, err := h.queries.ListPriceHistory(ctx, isin)
		if err != nil {
			return nil
		}
		var result []priceEntry
		for _, r := range rows {
			if r.Close.Valid {
				f, _ := r.Close.Float64Value()
				eurPrice := convertAmountToEURAtDate(f.Float64, r.Currency, r.Date, histFX, fxRates)
				if eurPrice > 0 {
					result = append(result, priceEntry{Date: r.Date, Close: eurPrice})
				}
			}
		}
		return result
	}

	currentPrices := fetchPrices(currentISIN)
	if len(currentPrices) == 0 {
		writeError(w, http.StatusNotFound, "no price history for current ISIN")
		return
	}
	altPrices := fetchPrices(altISIN)
	if len(altPrices) == 0 {
		writeError(w, http.StatusNotFound, "no price history for alternative ISIN")
		return
	}

	// Get transactions for current holding
	txns, _ := h.queries.ListTransactionsByISIN(ctx, pgtype.Text{String: currentISIN, Valid: true})

	// Build price maps for both ISINs
	curPriceMap := make(map[string]float64, len(currentPrices))
	for _, p := range currentPrices {
		curPriceMap[p.Date.Format("2006-01-02")] = p.Close
	}
	altPriceMap := make(map[string]float64, len(altPrices))
	for _, p := range altPrices {
		altPriceMap[p.Date.Format("2006-01-02")] = p.Close
	}

	numFloat := func(n pgtype.Numeric) float64 {
		if !n.Valid {
			return 0
		}
		f, _ := n.Float64Value()
		return f.Float64
	}

	// Find nearest price on or before a date
	findPrice := func(prices []priceEntry, date string) float64 {
		var best float64
		for _, p := range prices {
			if p.Date.Format("2006-01-02") <= date {
				best = p.Close
			}
		}
		return best
	}

	// Replay transactions: compute both current and alternative using
	// consistent EUR amt/price methodology for fair comparison
	var totalInvested, curQty, altQty float64
	var firstBuyDate time.Time
	for _, txn := range txns {
		amt := convertAmountToEURAtDate(numFloat(txn.Amount), txn.Currency, txn.Date, histFX, fxRates)
		txnDate := txn.Date.Format("2006-01-02")

		curPrice := curPriceMap[txnDate]
		if curPrice == 0 {
			curPrice = findPrice(currentPrices, txnDate)
		}
		altPrice := altPriceMap[txnDate]
		if altPrice == 0 {
			altPrice = findPrice(altPrices, txnDate)
		}

		if txn.Type == "buy" || txn.Type == "savings_plan" {
			if firstBuyDate.IsZero() || txn.Date.Before(firstBuyDate) {
				firstBuyDate = txn.Date
			}
			totalInvested += amt
			if curPrice > 0 {
				curQty += amt / curPrice
			}
			if altPrice > 0 {
				altQty += amt / altPrice
			}
		} else if txn.Type == "sell" {
			totalInvested -= amt
			if curPrice > 0 {
				curQty -= amt / curPrice
			}
			if altPrice > 0 {
				altQty -= amt / altPrice
			}
		}
	}

	latestCurPrice := currentPrices[len(currentPrices)-1].Close
	currentValue := curQty * latestCurPrice
	latestAltPrice := altPrices[len(altPrices)-1].Close
	altValue := altQty * latestAltPrice

	// Correlation aligned on common trading calendar dates (avoids holiday drift)
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

	// Tax-aware switch cost — uses InvStG Teilfreistellung (0% for individual
	// stocks and bond ETFs, 15% for mixed funds, 30% for equity ETFs) and the
	// REMAINING Sparerpauschbetrag for the year.
	unrealizedGain := currentValue - totalInvested
	teilfreistellungRate := securityTFSRate(currentSec)
	isEquity := teilfreistellungRate >= analytics.TeilfreistellungEquity
	fsaRemaining := h.remainingFSA(ctx)
	taxOnSwitch := switchTaxCost(unrealizedGain, teilfreistellungRate, fsaRemaining)
	netProceeds := currentValue - taxOnSwitch

	// Response intermediates the frontend still surfaces in the tax breakdown.
	teilfreistellungEUR := 0.0
	taxableAfterTFS := 0.0
	freibetragUsed := 0.0
	taxableAfterFSA := 0.0
	if unrealizedGain > 0 {
		teilfreistellungEUR = unrealizedGain * teilfreistellungRate
		taxableAfterTFS = unrealizedGain - teilfreistellungEUR
		freibetragUsed = math.Min(taxableAfterTFS, fsaRemaining)
		taxableAfterFSA = math.Max(0, taxableAfterTFS-freibetragUsed)
	}

	// Break-even: how many years until the alternative's better performance
	// recovers the tax cost of switching, using actual elapsed calendar time.
	annualAdvantage := 0.0
	if altValue > currentValue && currentValue > 0 {
		startDate := currentPrices[0].Date
		if !firstBuyDate.IsZero() && firstBuyDate.After(startDate) {
			startDate = firstBuyDate
		}
		endDate := currentPrices[len(currentPrices)-1].Date
		years := endDate.Sub(startDate).Hours() / (24 * 365.25)
		if years > 0 {
			annualAdvantage = (altValue - currentValue) / years
		}
	}
	breakEvenYears := 0.0
	if annualAdvantage > 0 && taxOnSwitch > 0 {
		breakEvenYears = math.Ceil(taxOnSwitch / annualAdvantage)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"current_isin":     currentISIN,
		"current_name":     currentSec.Name,
		"alternative_isin": altISIN,
		"total_invested":   math.Round(totalInvested),
		"current_value":    math.Round(currentValue),
		"alternative_value": math.Round(altValue),
		"difference_eur":   math.Round(altValue - currentValue),
		"difference_pct": func() float64 {
			if currentValue == 0 {
				return 0
			}
			return math.Round((altValue-currentValue)/currentValue*1000) / 10
		}(),
		"correlation":             math.Round(correlation*100) / 100,
		"low_correlation_warning": correlation < 0.85,
		// Tax-aware switch cost
		"unrealized_gain":      math.Round(unrealizedGain),
		"teilfreistellung":     math.Round(teilfreistellungEUR),
		"taxable_gain":         math.Round(taxableAfterFSA),
		"freibetrag_used":      math.Round(freibetragUsed),
		"freibetrag_remaining": math.Round(fsaRemaining),
		"tax_on_switch":        math.Round(taxOnSwitch),
		"net_proceeds":         math.Round(netProceeds),
		"break_even_years":     breakEvenYears,
		"is_equity":            isEquity,
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
