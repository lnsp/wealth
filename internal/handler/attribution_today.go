package handler

import (
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"

	"github.com/lnsp/wealth/internal/analytics"
	db "github.com/lnsp/wealth/internal/database/generated"
	"github.com/lnsp/wealth/internal/market"
)

// attributionContribution is one holding's share of the market move between
// two snapshots.
type attributionContribution struct {
	ISIN   string  `json:"isin"`
	Name   string  `json:"name"`
	Change float64 `json:"change"`
}

// signedEUR formats an amount with an explicit sign: "+1.234 EUR", "-40 EUR".
func signedEUR(v float64) string {
	if math.Round(v) < 0 {
		return "-" + fmtEUR(-v) + " EUR"
	}
	return "+" + fmtEUR(v) + " EUR"
}

// attributionSummary is the "Why?" sentence for the two largest movers,
// which must be sorted by absolute change. Signs are printed explicitly so a
// negative amount never gets a second minus.
func attributionSummary(contribs []attributionContribution) string {
	if len(contribs) == 0 {
		return ""
	}
	top := contribs[0]
	if math.Round(top.Change) == 0 {
		return "No notable price moves on your holdings."
	}
	verb := "Driven by"
	if top.Change < 0 {
		verb = "Dragged by"
	}
	first := fmt.Sprintf("%s %s (%s)", verb, top.Name, signedEUR(top.Change))
	if len(contribs) < 2 || math.Round(contribs[1].Change) == 0 {
		return first
	}
	second := contribs[1]
	if (second.Change < 0) == (top.Change < 0) {
		return fmt.Sprintf("%s and %s (%s)", first, second.Name, signedEUR(second.Change))
	}
	return fmt.Sprintf("%s, offset by %s (%s)", first, second.Name, signedEUR(second.Change))
}

// priceOnOrBefore returns the last close dated on or before d from an
// ascending price history.
func priceOnOrBefore(rows []db.ListPriceHistoryRow, d time.Time) (float64, string, bool) {
	day := civilDay(d)
	for i := len(rows) - 1; i >= 0; i-- {
		if !rows[i].Close.Valid || civilDay(rows[i].Date).After(day) {
			continue
		}
		return numericToFloat(rows[i].Close), rows[i].Currency, true
	}
	return 0, "", false
}

func civilDay(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// holdingMoveEUR is qty × (price on `to` at `to`'s FX − price on `from` at
// `from`'s FX): the price and currency move a snapshot pair sees on a
// holding. ok is false when either date has no price.
func holdingMoveEUR(rows []db.ListPriceHistoryRow, qty float64, from, to time.Time, histFX []market.HistoricalFXRate, fxRates map[string]float64) (float64, bool) {
	p0, c0, ok0 := priceOnOrBefore(rows, from)
	p1, c1, ok1 := priceOnOrBefore(rows, to)
	if !ok0 || !ok1 {
		return 0, false
	}
	eur0 := convertAmountToEURAtDate(p0, c0, from, histFX, fxRates)
	eur1 := convertAmountToEURAtDate(p1, c1, to, histFX, fxRates)
	return qty * (eur1 - eur0), true
}

// netWorthFlows sums what entered or left net worth from outside between two
// snapshots, i.e. transactions dated in (from, to]:
//
//   - contributions: deposits and RSU vests minus withdrawals, plus the
//     implicit funding of securities-only accounts (as in the waterfall);
//   - income: dividends and interest minus fees and taxes.
//
// Internal moves (buys, sells, transfers between own accounts, matched
// broker-to-broker transfers, asset purchases and loan instalments linked to
// a physical asset) are swaps inside net worth and are skipped. Amounts are
// magnitudes signed by type, as in txnSignedCashDelta.
func netWorthFlows(txns []db.ListTransactionsRow, washed map[uuid.UUID]struct{}, linked map[uuid.UUID]bool, implicit []implicitCashFlowEvent, from, to time.Time, histFX []market.HistoricalFXRate, fxRates map[string]float64) (contrib, income float64) {
	from, to = civilDay(from), civilDay(to)
	in := func(d time.Time) bool {
		d = civilDay(d)
		return d.After(from) && !d.After(to)
	}
	for _, ev := range implicit {
		if in(ev.Date) {
			contrib += ev.DeltaEUR
		}
	}
	for _, t := range txns {
		if !in(t.Date) {
			continue
		}
		eur := func(n float64) float64 {
			return math.Abs(convertAmountToEURAtDate(n, t.Currency, t.Date, histFX, fxRates))
		}
		income -= eur(numericToFloat(t.Fee)) + eur(numericToFloat(t.Tax))
		if _, ok := washed[t.ID]; ok || linked[t.ID] {
			continue
		}
		amt := eur(numericToFloat(t.Amount))
		switch analytics.ClassifyForAttribution(t.Type, t.SecurityISIN.Valid, analytics.ScopePortfolio) {
		case analytics.BucketContribution:
			contrib += amt
		case analytics.BucketWithdrawal:
			contrib -= amt
		case analytics.BucketDividend, analytics.BucketInterest:
			income += amt
		case analytics.BucketFee, analytics.BucketTax:
			income -= amt
		}
	}
	return contrib, income
}

// inKindWashSet matches broker-to-broker security transfers so both legs can
// be skipped, exactly as the wealth waterfall does.
func inKindWashSet(txns []db.ListTransactionsRow) map[uuid.UUID]struct{} {
	var inkind []analytics.InKindTransfer
	for _, t := range txns {
		if !t.SecurityISIN.Valid || (t.Type != "transfer" && t.Type != "transfer_out") {
			continue
		}
		inkind = append(inkind, analytics.InKindTransfer{
			ID:       t.ID,
			Date:     t.Date,
			Type:     t.Type,
			ISIN:     t.SecurityISIN.String,
			Quantity: numericToFloat(t.Quantity),
		})
	}
	return analytics.MatchInKindTransferPairs(inkind)
}
