package handler

// Historical valuation helpers shared by the Portfolio page handlers.
//
// Everything here works in EUR at the ECB rate of the relevant day:
//   - purchase costs at the rate of the purchase date (so a gain includes the
//     currency move since purchase, as it does for tax),
//   - historical prices at the rate of the price date,
//   - external flows at the rate of the flow date.
//
// The replay mirrors the current_holdings view (migration 029): buys, savings
// plans and in-kind transfers add units and cost; sells and outgoing transfers
// remove units and a proportional share of the cost.

import (
	"context"
	"math"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/lnsp/wealth/internal/analytics"
	db "github.com/lnsp/wealth/internal/database/generated"
	"github.com/lnsp/wealth/internal/market"
)

func civilDay(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// ---------------------------------------------------------------- FX

type fxPoint struct {
	date time.Time
	rate float64
}

// fxTable answers "units of a currency per EUR on a date" with a binary
// search. Fallbacks match convertAmountToEURAtDate: the latest rate on or
// before the date, else the latest known rate.
type fxTable struct {
	series map[string][]fxPoint
	latest map[string]float64
}

func newFXTable(hist []market.HistoricalFXRate, latest map[string]float64) *fxTable {
	t := &fxTable{series: map[string][]fxPoint{}, latest: latest}
	for _, r := range hist {
		if r.Rate > 0 {
			t.series[r.Currency] = append(t.series[r.Currency], fxPoint{date: civilDay(r.Date), rate: r.Rate})
		}
	}
	for c, s := range t.series {
		sort.SliceStable(s, func(i, j int) bool { return s[i].date.Before(s[j].date) })
		t.series[c] = s
	}
	return t
}

func loadFXTable(ctx context.Context, q *db.Queries) *fxTable {
	return newFXTable(loadHistoricalFXRates(ctx, q), loadFXRatesMap(ctx, q))
}

// rate returns units of `cur` (an ISO code, already normalised) per EUR.
func (t *fxTable) rate(cur string, date time.Time) float64 {
	if cur == "EUR" {
		return 1
	}
	if t == nil {
		return 0
	}
	if s := t.series[cur]; len(s) > 0 {
		d := civilDay(date)
		i := sort.Search(len(s), func(i int) bool { return s[i].date.After(d) })
		if i > 0 {
			return s[i-1].rate
		}
	}
	if t.latest != nil {
		if r := t.latest[cur]; r > 0 {
			return r
		}
	}
	return 0
}

func (t *fxTable) toEUR(amount float64, currency string, date time.Time) float64 {
	if math.IsNaN(amount) || math.IsInf(amount, 0) || amount == 0 {
		return 0
	}
	norm, scale := normalizeCurrencyAndScale(currency)
	scaled := amount * scale
	if norm == "EUR" {
		return scaled
	}
	if r := t.rate(norm, date); r > 0 {
		return scaled / r
	}
	return scaled
}

// ---------------------------------------------------------------- costs

type positionKey struct {
	AccountID uuid.UUID
	ISIN      string
}

// costLot is a FIFO lot of a position (EUR at the purchase date's ECB rate).
type costLot struct {
	Date    time.Time
	Qty     float64
	CostEUR float64 // cost of the remaining Qty
}

// positionCost is one (account, ISIN) position replayed from its transactions.
type positionCost struct {
	Qty float64
	// CostEUR is the moving-average cost of the remaining units, each purchase
	// converted at the ECB rate of its own date. This is the cost method of the
	// current_holdings view; tax uses FIFO (Lots).
	CostEUR float64
	// CostNative is the same cost in the currency the purchases were paid in;
	// NativeCurrency is "" when the open purchases were paid in more than one.
	CostNative     float64
	NativeCurrency string
	Lots           []costLot
	FirstAcquired  time.Time
	// Dividends on this position, EUR at the payment date: gross amount and
	// withheld tax + fees.
	DividendsEUR   float64
	DividendTaxEUR float64
	nativeSet      bool
	nativeMixed    bool
}

func (p *positionCost) AvgCostEUR() float64 {
	if p == nil || p.Qty <= 1e-12 {
		return 0
	}
	return p.CostEUR / p.Qty
}

// AvgCostNative returns the average cost per unit in the currency it was paid
// in, or ok=false when the open purchases mix currencies.
func (p *positionCost) AvgCostNative() (avg float64, currency string, ok bool) {
	if p == nil || p.Qty <= 1e-12 || !p.nativeSet || p.nativeMixed {
		return 0, "", false
	}
	return p.CostNative / p.Qty, p.NativeCurrency, true
}

// FIFOCostEUR is the cost of the remaining FIFO lots.
func (p *positionCost) FIFOCostEUR() float64 {
	if p == nil {
		return 0
	}
	c := 0.0
	for _, l := range p.Lots {
		c += l.CostEUR
	}
	return c
}

func isAcquisitionType(t string) bool {
	return t == "buy" || t == "savings_plan" || t == "transfer"
}

func isDisposalType(t string) bool {
	return t == "sell" || t == "transfer_out"
}

// consumeFIFO removes qty units from the front of lots and returns the lots
// left and the EUR cost of the units removed.
func consumeFIFO(lots []costLot, qty float64) ([]costLot, float64) {
	cost := 0.0
	rem := qty
	for len(lots) > 0 && rem > 1e-12 {
		l := lots[0]
		if l.Qty <= rem+1e-12 {
			cost += l.CostEUR
			rem -= l.Qty
			lots = lots[1:]
			continue
		}
		part := l.CostEUR * rem / l.Qty
		cost += part
		lots[0] = costLot{Date: l.Date, Qty: l.Qty - rem, CostEUR: l.CostEUR - part}
		rem = 0
	}
	return lots, cost
}

// replayPositionCosts replays chronological transactions into per-(account,
// ISIN) positions with EUR cost at each transaction date's ECB rate.
func replayPositionCosts(txnsChrono []db.ListTransactionsRow, fx *fxTable) map[positionKey]*positionCost {
	out := make(map[positionKey]*positionCost)
	get := func(k positionKey) *positionCost {
		p := out[k]
		if p == nil {
			p = &positionCost{}
			out[k] = p
		}
		return p
	}
	for _, t := range txnsChrono {
		if !t.SecurityISIN.Valid || t.SecurityISIN.String == "" {
			continue
		}
		k := positionKey{AccountID: t.AccountID, ISIN: t.SecurityISIN.String}
		qty := math.Abs(numericToFloat(t.Quantity))
		switch {
		case isAcquisitionType(t.Type):
			p := get(k)
			native := math.Abs(numericToFloat(t.Amount)) + math.Abs(numericToFloat(t.Fee))
			costEUR := fx.toEUR(native, t.Currency, t.Date)
			if p.Qty <= 1e-12 {
				// A fresh position: forget the currency of fully sold lots.
				p.nativeSet, p.nativeMixed, p.CostNative, p.CostEUR, p.Lots = false, false, 0, 0, nil
				p.FirstAcquired = t.Date
			}
			p.Qty += qty
			p.CostEUR += costEUR
			p.CostNative += native
			if !p.nativeSet {
				p.NativeCurrency, p.nativeSet = t.Currency, true
			} else if p.NativeCurrency != t.Currency {
				p.nativeMixed = true
			}
			if qty > 0 {
				p.Lots = append(p.Lots, costLot{Date: t.Date, Qty: qty, CostEUR: costEUR})
			}
		case isDisposalType(t.Type):
			p := get(k)
			if p.Qty <= 1e-12 {
				continue
			}
			if qty >= p.Qty-1e-12 {
				p.Qty, p.CostEUR, p.CostNative, p.Lots = 0, 0, 0, nil
				continue
			}
			frac := (p.Qty - qty) / p.Qty
			p.CostEUR *= frac
			p.CostNative *= frac
			p.Qty -= qty
			p.Lots, _ = consumeFIFO(p.Lots, qty)
		case t.Type == "dividend":
			p := get(k)
			amt := fx.toEUR(numericToFloat(t.Amount), t.Currency, t.Date)
			p.DividendsEUR += amt
			p.DividendTaxEUR += fx.toEUR(math.Abs(numericToFloat(t.Tax))+math.Abs(numericToFloat(t.Fee)), t.Currency, t.Date)
		}
	}
	return out
}

// chronological returns ListAllTransactions rows (newest first, same-day buys
// after sells) in replay order: oldest first, same-day buys before sells.
func chronological(txnsDesc []db.ListTransactionsRow) []db.ListTransactionsRow {
	out := make([]db.ListTransactionsRow, len(txnsDesc))
	for i := range txnsDesc {
		out[i] = txnsDesc[len(txnsDesc)-1-i]
	}
	return out
}

// loadPositionCosts replays every active-account transaction (the same set
// the current_holdings view aggregates).
func loadPositionCosts(ctx context.Context, q *db.Queries, fx *fxTable) map[positionKey]*positionCost {
	txns, err := listAllTransactionsRows(ctx, q)
	if err != nil {
		return map[positionKey]*positionCost{}
	}
	return replayPositionCosts(chronological(txns), fx)
}

// ---------------------------------------------------------------- prices

type pricePoint struct {
	date     time.Time
	close    float64
	currency string
}

// priceBook holds stored closing prices per ISIN, oldest first.
type priceBook map[string][]pricePoint

func loadPriceBook(ctx context.Context, q *db.Queries, isins []string) priceBook {
	pb := make(priceBook, len(isins))
	for _, isin := range isins {
		if _, done := pb[isin]; done || isin == "" {
			continue
		}
		rows, err := q.ListPriceHistory(ctx, isin)
		if err != nil {
			pb[isin] = nil
			continue
		}
		pts := make([]pricePoint, 0, len(rows))
		for _, r := range rows {
			if c := numericToFloat(r.Close); c > 0 {
				pts = append(pts, pricePoint{date: civilDay(r.Date), close: c, currency: r.Currency})
			}
		}
		pb[isin] = pts
	}
	return pb
}

// at returns the latest stored price on or before d.
func (pb priceBook) at(isin string, d time.Time) (pricePoint, bool) {
	pts := pb[isin]
	day := civilDay(d)
	i := sort.Search(len(pts), func(i int) bool { return pts[i].date.After(day) })
	if i == 0 {
		return pricePoint{}, false
	}
	return pts[i-1], true
}

// ---------------------------------------------------------------- series

// valuationPoint is the scope's value at the end of Date plus the external
// flows since the previous emitted point. All amounts are EUR.
type valuationPoint struct {
	Date       time.Time
	Value      float64 // securities + cash
	Securities float64
	Cash       float64
	CashIn     float64 // deposits, transfers in from outside the scope, implicit funding
	CashOut    float64 // withdrawals, transfers out of the scope (positive magnitude)
	InKindIn   float64 // shares arriving from outside the scope (RSU vests, depot transfers in)
	InKindOut  float64 // shares leaving the scope
	ByISIN     map[string]float64
}

// Flow is the net external flow (+ in, − out).
func (p valuationPoint) Flow() float64 {
	return p.CashIn - p.CashOut + p.InKindIn - p.InKindOut
}

type valuationRequest struct {
	Txns     []db.ListTransactionsRow // chronological; rows outside Accounts are ignored
	Accounts []db.Account             // the scope
	Prices   priceBook
	FX       *fxTable
	End      time.Time
	ByISIN   bool
	// Emit selects the days that produce a point (nil: every day). Flows of
	// skipped days roll into the next emitted point; End is always emitted.
	Emit func(d time.Time) bool
}

// isInvestmentAccount is the Portfolio page's scope: the same account types
// its account selector lists. Checking and savings cash is not invested.
func isInvestmentAccount(a db.Account) bool {
	return a.IsActive && isSecuritiesOnlyAccountType(a.Type)
}

// buildValuationSeries replays the scope's transactions day by day and values
// it at each day's price (stored close, or the latest trade price when that
// is more recent or no close is stored yet) and ECB rate.
//
// External flows use per-account semantics: deposits, withdrawals and cash
// transfers in or out of a scope account count, and so do in-kind transfers
// unless both legs are inside the scope (MatchInKindTransferPairs). Synthetic
// negative cash on securities-only accounts without cash records is clamped
// to zero, and the clamp shows up as implicit funding, as in
// computeImplicitCashClampEvents.
func buildValuationSeries(req valuationRequest) []valuationPoint {
	inScope := make(map[uuid.UUID]db.Account, len(req.Accounts))
	for _, a := range req.Accounts {
		inScope[a.ID] = a
	}
	var txns []db.ListTransactionsRow
	for _, t := range req.Txns {
		if _, ok := inScope[t.AccountID]; ok {
			txns = append(txns, t)
		}
	}
	if len(txns) == 0 {
		return nil
	}
	explicit := make(map[uuid.UUID]bool)
	var inkind []analytics.InKindTransfer
	for _, t := range txns {
		if isExplicitCashTxnType(t.Type) {
			explicit[t.AccountID] = true
		}
		if t.SecurityISIN.Valid && (t.Type == "transfer" || t.Type == "transfer_out") {
			inkind = append(inkind, analytics.InKindTransfer{
				ID: t.ID, Date: t.Date, Type: t.Type, ISIN: t.SecurityISIN.String, Quantity: math.Abs(numericToFloat(t.Quantity)),
			})
		}
	}
	washed := analytics.MatchInKindTransferPairs(inkind)

	start := civilDay(txns[0].Date)
	end := civilDay(req.End)
	if end.Before(start) {
		end = start
	}

	qty := make(map[positionKey]float64)
	trade := make(map[string]pricePoint)
	cash := make(map[uuid.UUID]map[string]float64)
	prevOffset := make(map[uuid.UUID]float64)

	mark := func(isin string, d time.Time) float64 {
		sp, okS := req.Prices.at(isin, d)
		tp, okT := trade[isin]
		var p pricePoint
		switch {
		case okS && okT:
			p = sp
			if tp.date.After(sp.date) {
				p = tp
			}
		case okS:
			p = sp
		case okT:
			p = tp
		default:
			return 0
		}
		return req.FX.toEUR(p.close, p.currency, d)
	}

	var acc valuationPoint
	var out []valuationPoint
	ti := 0
	for d := start; !d.After(end); d = d.AddDate(0, 0, 1) {
		for ti < len(txns) && !civilDay(txns[ti].Date).After(d) {
			t := txns[ti]
			ti++
			isin := ""
			if t.SecurityISIN.Valid {
				isin = t.SecurityISIN.String
			}
			q := math.Abs(numericToFloat(t.Quantity))
			amt := math.Abs(numericToFloat(t.Amount))
			if isin != "" {
				k := positionKey{AccountID: t.AccountID, ISIN: isin}
				// Trades mark the position between stored closes. An in-kind
				// transfer may carry the original cost instead of today's
				// value, so it only marks when no close from the last week
				// exists (an RSU vest's FMV fills a gap in the price history).
				useAsMark := (isAcquisitionType(t.Type) || t.Type == "sell") && q > 0
				if useAsMark && t.Type == "transfer" {
					if sp, ok := req.Prices.at(isin, d); ok && d.Sub(sp.date) <= 7*24*time.Hour {
						useAsMark = false
					}
				}
				if useAsMark {
					px := 0.0
					if amt > 0 {
						px = amt / q
					} else if p := numericToFloat(t.Price); p > 0 {
						px = p
					}
					if px > 0 {
						trade[isin] = pricePoint{date: d, close: px, currency: t.Currency}
					}
				}
				if isAcquisitionType(t.Type) {
					qty[k] += q
				} else if isDisposalType(t.Type) {
					qty[k] = math.Max(0, qty[k]-q)
				}
			}
			if delta := txnSignedCashDelta(t.Type, isin != "", numericToFloat(t.Amount), numericToFloat(t.Fee), numericToFloat(t.Tax)); delta != 0 {
				if cash[t.AccountID] == nil {
					cash[t.AccountID] = make(map[string]float64)
				}
				cash[t.AccountID][t.Currency] += delta
			}
			if _, w := washed[t.ID]; w {
				continue
			}
			amtEUR := req.FX.toEUR(amt, t.Currency, d)
			switch t.Type {
			case "deposit", "cash_transfer_in":
				acc.CashIn += amtEUR
			case "withdrawal", "cash_transfer_out":
				acc.CashOut += amtEUR
			case "transfer":
				if isin == "" {
					acc.CashOut += amtEUR
				} else {
					if amtEUR <= 0 {
						amtEUR = q * mark(isin, d)
					}
					acc.InKindIn += amtEUR
				}
			case "transfer_out":
				if isin != "" {
					if amtEUR <= 0 {
						amtEUR = q * mark(isin, d)
					}
					acc.InKindOut += amtEUR
				}
			}
		}

		cashEUR := 0.0
		for id, byCur := range cash {
			raw := 0.0
			for cur, a := range byCur {
				raw += req.FX.toEUR(a, cur, d)
			}
			clamped := raw
			if raw < 0 && !explicit[id] && isSecuritiesOnlyAccountType(inScope[id].Type) {
				clamped = 0
			}
			off := clamped - raw
			if delta := off - prevOffset[id]; math.Abs(delta) > 1e-9 {
				if delta > 0 {
					acc.CashIn += delta
				} else {
					acc.CashOut -= delta
				}
			}
			prevOffset[id] = off
			cashEUR += clamped
		}

		if req.Emit != nil && !req.Emit(d) && !d.Equal(end) {
			continue
		}
		sec := 0.0
		var by map[string]float64
		if req.ByISIN {
			by = make(map[string]float64)
		}
		for k, q := range qty {
			if q <= 1e-9 {
				continue
			}
			v := q * mark(k.ISIN, d)
			sec += v
			if by != nil {
				by[k.ISIN] += v
			}
		}
		p := acc
		p.Date = d
		p.Securities = sec
		p.Cash = cashEUR
		p.Value = sec + cashEUR
		p.ByISIN = by
		out = append(out, p)
		acc = valuationPoint{}
	}
	return out
}

// isMonthEnd reports whether d is the last calendar day of its month.
func isMonthEnd(d time.Time) bool {
	return d.AddDate(0, 0, 1).Month() != d.Month()
}

// twrSteps turns a valuation series into TWR steps.
func twrSteps(points []valuationPoint) []analytics.TWRStep {
	steps := make([]analytics.TWRStep, len(points))
	for i, p := range points {
		steps[i] = analytics.TWRStep{Date: p.Date, Value: p.Value, Flow: p.Flow()}
	}
	return steps
}

// ---------------------------------------------------------------- allocation

// normalisedTargets returns targets scaled to sum to 100 % (when they sum to
// anything else), the original sum, and whether scaling happened.
func normalisedTargets(targets map[string]float64) (map[string]float64, float64, bool) {
	sum := 0.0
	for _, v := range targets {
		if v > 0 {
			sum += v
		}
	}
	out := make(map[string]float64, len(targets))
	if sum <= 0 {
		return out, 0, false
	}
	scale := 1.0
	normalised := math.Abs(sum-100) > 0.01
	if normalised {
		scale = 100 / sum
	}
	for k, v := range targets {
		if v > 0 {
			out[k] = v * scale
		}
	}
	return out, sum, normalised
}

// allocationMaxDrift is the one drift definition shared by the allocation
// view, the rebalancer and the health score: over every held or targeted
// ISIN, actual weight minus target weight, where a held ISIN without a target
// has target 0 (the rebalancer sells it). Targets are normalised to 100 %.
// Returns the signed drift with the largest magnitude.
func allocationMaxDrift(valueByISIN map[string]float64, targets map[string]float64) float64 {
	total := 0.0
	for _, v := range valueByISIN {
		total += v
	}
	norm, _, _ := normalisedTargets(targets)
	if total <= 0 || len(norm) == 0 {
		return 0
	}
	worst := 0.0
	seen := make(map[string]bool, len(valueByISIN)+len(norm))
	check := func(isin string) {
		if seen[isin] {
			return
		}
		seen[isin] = true
		d := valueByISIN[isin]/total*100 - norm[isin]
		if math.Abs(d) > math.Abs(worst) {
			worst = d
		}
	}
	for isin := range valueByISIN {
		check(isin)
	}
	for isin := range norm {
		check(isin)
	}
	return worst
}
