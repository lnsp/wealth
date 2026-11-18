package scheduler

// Background jobs that must agree with the pages: wealth reports and digests
// (the report pages' buildReportData) and the tax reminders and alerts (the
// Tax pages' FIFO ledger).

import (
	"context"
	"fmt"
	"log"
	"math"
	"strings"
	"time"

	"github.com/lnsp/wealth/internal/analytics"
	db "github.com/lnsp/wealth/internal/database/generated"
	"github.com/lnsp/wealth/internal/handler"
)

/* ───────────── Reports and digests ───────────── */

// computeReportData computes a report for [start, end] with the report pages'
// logic: the opening value is the last snapshot before the period, the change
// splits into net deposits and market, and dividends are converted at the
// payment date's ECB rate, net of tax and fees withheld.
func (s *Scheduler) computeReportData(ctx context.Context, start, end time.Time) (handler.ReportData, error) {
	return handler.NewReportsHandler(s.queries).ComputeReportData(ctx, start, end)
}

// firstNonZeroSnapshot is the date of the first snapshot with a non-zero net
// worth. Earlier snapshots are placeholders written before any data existed.
func firstNonZeroSnapshot(snaps []db.NetWorthSnapshot) (time.Time, bool) {
	var first time.Time
	found := false
	for _, s := range snaps {
		if numericToFloat(s.Total) != 0 && (!found || s.Date.Before(first)) {
			first, found = s.Date, true
		}
	}
	return first, found
}

// digestLines renders a monthly report as digest text.
func digestLines(data handler.ReportData) []string {
	signed := func(v float64) string {
		if v > 0 {
			return "+" + fmtEUR(v)
		}
		return fmtEUR(v)
	}
	var lines []string
	if data.HasNetWorth {
		lines = append(lines, fmt.Sprintf("Net Worth: %s EUR", fmtEUR(data.NetWorthEnd)))
		if data.NetWorthChange != 0 {
			line := fmt.Sprintf("Monthly Change: %s EUR", signed(data.NetWorthChange))
			if data.NetWorthStart > 0 {
				line += fmt.Sprintf(" (%+.1f%%)", data.NetWorthChangePct)
			}
			lines = append(lines, line)
			lines = append(lines, fmt.Sprintf("  Deposits: %s EUR · Market: %s EUR", signed(data.NetDeposits), signed(data.MarketChange)))
		}
	} else {
		lines = append(lines, "No net worth snapshot for this period.")
	}
	if data.DividendsGross > 0 {
		line := fmt.Sprintf("Dividend Income: %s EUR", fmtEUR(data.TotalDividends))
		if data.DividendsWithheld > 0 {
			line += fmt.Sprintf(" net (%s EUR gross, %s EUR withheld)", fmtEUR(data.DividendsGross), fmtEUR(data.DividendsWithheld))
		}
		lines = append(lines, line)
	}
	if data.TopGainer != nil {
		lines = append(lines, fmt.Sprintf("Top Gainer: %s (%+.1f%%)", data.TopGainer.Name, data.TopGainer.ReturnPct))
	}
	if data.TopLoser != nil {
		lines = append(lines, fmt.Sprintf("Top Loser: %s (%+.1f%%)", data.TopLoser.Name, data.TopLoser.ReturnPct))
	}
	lines = append(lines, data.Warnings...)
	return lines
}

/* ───────────── Tax reminders and alerts ───────────── */

// vorabpauschaleTaxFromLedger is the tax on the Vorabpauschale computed for
// taxYear (debited in January of taxYear+1), from the ledger's Vorabpauschale
// events: units held with their original acquisition dates (transfers keep
// them), the price on 1 January, the cap and the 1/12 reduction (§ 18 InvStG).
// Single filer without church tax, before the Freistellungsauftrag.
func vorabpauschaleTaxFromLedger(ledger []analytics.TaxTransaction, taxYear int) float64 {
	total := 0.0
	for _, e := range analytics.VorabpauschaleForYear(ledger, taxYear, analytics.TaxOptions{}) {
		total += e.TaxOnVP
	}
	return total
}

// harvestSavingFromLedger is the tax this year that selling every depot
// position in isin at today's price (all FIFO lots) would save, priced through
// the tax engine with analytics.MarginalSaleTax: Teilfreistellung, both loss
// pots, losses carried in and the remaining Sparer-Pauschbetrag all count. ok
// is false when no taxable lot of isin is held (e.g. only in a pension wrapper).
func harvestSavingFromLedger(l *handler.TaxLedger, isin string, now time.Time) (saving float64, ok bool) {
	if l == nil || l.Prices[isin] <= 0 {
		return 0, false
	}
	var lots []analytics.TaxLot
	for _, lot := range analytics.ComputeTaxLots(l.Txns, l.Prices, l.Names, l.Equity) {
		if lot.ISIN == isin && !lot.PrivateSale {
			lots = append(lots, lot)
		}
	}
	if len(lots) == 0 {
		return 0, false
	}
	without, with := analytics.MarginalSaleTax(l.Txns, analytics.HypotheticalSellTxns(lots, now), now.Year(), analytics.TaxOptions{})
	return math.Max(0, without-with), true
}

// computeSchedulerVorabpauschaleTax is the tax on the § 18 InvStG
// Vorabpauschale for taxYear (debited in January of taxYear+1), from the same
// ledger as the Tax page: inbound depot transfers keep their original lots and
// acquisition dates, and each fund's base is units × the price on 1 January.
func (s *Scheduler) computeSchedulerVorabpauschaleTax(ctx context.Context, taxYear int) float64 {
	if basiszins, ok := analytics.BasiszinsByYear[taxYear]; !ok || basiszins <= 0 {
		return 0
	}
	ledger, err := handler.LoadTaxLedger(ctx, s.queries)
	if err != nil {
		return 0
	}
	return vorabpauschaleTaxFromLedger(ledger.Txns, taxYear)
}

// newHarvestSaver returns a function that prices a loss-harvesting sale of
// one security through the tax engine, loading the ledger on first use.
func (s *Scheduler) newHarvestSaver(ctx context.Context) func(isin string) (saving float64, ok bool) {
	var ledger *handler.TaxLedger
	loaded := false
	return func(isin string) (float64, bool) {
		if !loaded {
			loaded = true
			if l, err := handler.LoadTaxLedger(ctx, s.queries); err == nil {
				ledger = l
			}
		}
		return harvestSavingFromLedger(ledger, isin, time.Now())
	}
}

// sendMonthlyDigest creates and dispatches a digest to configured channels.
func (s *Scheduler) sendMonthlyDigest(ctx context.Context, data handler.ReportData, label string) {
	subject := fmt.Sprintf("Wealth Monthly Digest — %s", label)
	body := strings.Join(digestLines(data), "\n")
	// Dispatch to monthly channels
	s.dispatchMessage(ctx, "digest", subject, body, "monthly")
	// Also dispatch to quarterly channels on Jan/Apr/Jul/Oct
	month := time.Now().UTC().Month()
	if month == time.January || month == time.April || month == time.July || month == time.October {
		s.dispatchMessage(ctx, "digest", subject, body, "quarterly")
	}
	log.Printf("monthly digest dispatched for %s", label)
}
