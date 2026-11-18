package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-pdf/fpdf"
	"github.com/google/uuid"

	"github.com/lnsp/wealth/internal/analytics"
	"github.com/lnsp/wealth/internal/assets"
	db "github.com/lnsp/wealth/internal/database/generated"
	"github.com/lnsp/wealth/internal/market"
)

type ReportsHandler struct {
	queries *db.Queries
}

func NewReportsHandler(q *db.Queries) *ReportsHandler {
	return &ReportsHandler{queries: q}
}

// ReportData is the JSON structure of a wealth report. The figures are
// recomputed from the current snapshots and transactions every time a report
// is opened or downloaded, so a rebuilt net worth history or a corrected
// import shows up in old reports too; wealth_reports.data keeps the copy from
// the last (re)generation for the digest and as a fallback.
type ReportData struct {
	// HasNetWorth is false when no net worth snapshot (after the first
	// non-zero one) falls in the period; the net worth figures are then 0.
	HasNetWorth bool `json:"has_net_worth"`
	// StartDate is the snapshot used as the opening value: the last one
	// before the period (the previous period's close). Empty when there is
	// no earlier history, in which case the opening value is 0.
	StartDate         string  `json:"start_date,omitempty"`
	EndDate           string  `json:"end_date,omitempty"`
	NetWorthStart     float64 `json:"net_worth_start"`
	NetWorthEnd       float64 `json:"net_worth_end"`
	NetWorthChange    float64 `json:"net_worth_change"`
	NetWorthChangePct float64 `json:"net_worth_change_pct"`
	// NetDeposits is money and shares that entered net worth from outside
	// (deposits, RSU vests and other in-kind receipts, minus withdrawals);
	// MarketChange is the rest of the change: price moves, income, fees,
	// taxes and revaluations of physical assets.
	NetDeposits  float64 `json:"net_deposits"`
	MarketChange float64 `json:"market_change"`
	// TotalDividends is net: what was credited after tax and fees withheld
	// at source. DividendsGross is the booked dividend amount and
	// DividendsWithheld the tax and fees booked on the same rows. Foreign
	// dividends use the ECB rate of the payment date.
	TotalDividends    float64         `json:"total_dividends"`
	DividendsGross    float64         `json:"dividends_gross"`
	DividendsWithheld float64         `json:"dividends_withheld"`
	NewTransactions   int             `json:"new_transactions"`
	Holdings          []ReportHolding `json:"holdings"`
	// TopGainer and TopLoser are only set with at least two holdings.
	TopGainer *ReportHolding `json:"top_gainer,omitempty"`
	TopLoser  *ReportHolding `json:"top_loser,omitempty"`
	Warnings  []string       `json:"warnings,omitempty"`
}

type ReportHolding struct {
	ISIN      string  `json:"isin"`
	Name      string  `json:"name"`
	Value     float64 `json:"value"`
	Weight    float64 `json:"weight"`
	ReturnPct float64 `json:"return_pct,omitempty"`
}

func (h *ReportsHandler) HandleListReports(w http.ResponseWriter, r *http.Request) {
	reports, err := h.queries.ListWealthReports(r.Context(), 240)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list reports: "+err.Error())
		return
	}

	type reportSummary struct {
		ID          string `json:"id"`
		ReportType  string `json:"report_type"`
		PeriodLabel string `json:"period_label"`
		GeneratedAt string `json:"generated_at"`
	}

	var items []reportSummary
	for _, rpt := range reports {
		items = append(items, reportSummary{
			ID:          rpt.ID.String(),
			ReportType:  rpt.ReportType,
			PeriodLabel: rpt.PeriodLabel,
			GeneratedAt: rpt.GeneratedAt.Format(time.RFC3339),
		})
	}
	if items == nil {
		items = []reportSummary{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"reports": items})
}

// currentReportData recomputes a stored report's figures from today's data,
// falling back to the stored copy if that fails.
func (h *ReportsHandler) currentReportData(ctx context.Context, rpt db.WealthReport) ReportData {
	data, err := h.loadReportData(ctx, rpt.PeriodStart, rpt.PeriodEnd)
	if err == nil {
		return data
	}
	log.Printf("WARNING: recompute report %s: %v; using stored figures", rpt.PeriodLabel, err)
	var stored ReportData
	_ = json.Unmarshal(rpt.Data, &stored)
	return stored
}

func (h *ReportsHandler) HandleGetReport(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid report id")
		return
	}

	rpt, err := h.queries.GetWealthReport(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "report not found")
		return
	}

	data := h.currentReportData(r.Context(), rpt)

	writeJSON(w, http.StatusOK, map[string]any{
		"id":           rpt.ID.String(),
		"report_type":  rpt.ReportType,
		"period_label": rpt.PeriodLabel,
		"period_start": rpt.PeriodStart.Format("2006-01-02"),
		"period_end":   rpt.PeriodEnd.Format("2006-01-02"),
		"generated_at": rpt.GeneratedAt.Format(time.RFC3339),
		"computed_at":  time.Now().UTC().Format(time.RFC3339),
		"data":         data,
	})
}

// HandleGenerateReport generates the report for a period, replacing the
// stored figures when the period already has a report (regenerate).
func (h *ReportsHandler) HandleGenerateReport(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ReportType string `json:"report_type"` // "monthly" or "annual"
		Year       int    `json:"year"`
		Month      int    `json:"month"` // 1-12 for monthly, 0 for annual
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.ReportType != "monthly" && req.ReportType != "annual" {
		writeError(w, http.StatusBadRequest, "report_type must be 'monthly' or 'annual'")
		return
	}
	if req.Year < 2020 || req.Year > 2100 {
		writeError(w, http.StatusBadRequest, "invalid year")
		return
	}

	ctx := r.Context()

	var periodLabel string
	var periodStart, periodEnd time.Time

	if req.ReportType == "monthly" {
		if req.Month < 1 || req.Month > 12 {
			writeError(w, http.StatusBadRequest, "month must be 1-12")
			return
		}
		periodStart = time.Date(req.Year, time.Month(req.Month), 1, 0, 0, 0, 0, time.UTC)
		periodEnd = time.Date(req.Year, time.Month(req.Month)+1, 0, 0, 0, 0, 0, time.UTC)
		periodLabel = periodStart.Format("2006-01")
	} else {
		periodStart = time.Date(req.Year, 1, 1, 0, 0, 0, 0, time.UTC)
		periodEnd = time.Date(req.Year, 12, 31, 0, 0, 0, 0, time.UTC)
		periodLabel = periodStart.Format("2006")
	}

	data, err := h.loadReportData(ctx, periodStart, periodEnd)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "compute report: "+err.Error())
		return
	}
	dataJSON, err := json.Marshal(data)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "encode report: "+err.Error())
		return
	}

	replaced, err := h.queries.UpdateWealthReportForPeriod(ctx, db.UpdateWealthReportForPeriodParams{
		ReportType: req.ReportType, PeriodLabel: periodLabel, Data: dataJSON,
		PeriodStart: periodStart, PeriodEnd: periodEnd,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "save report: "+err.Error())
		return
	}
	if replaced > 0 {
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "period": periodLabel, "regenerated": true})
		return
	}
	if err := h.queries.InsertWealthReport(ctx, db.InsertWealthReportParams{
		ReportType: req.ReportType, PeriodLabel: periodLabel, PeriodStart: periodStart, PeriodEnd: periodEnd, Data: dataJSON,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "save report: "+err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{"status": "ok", "period": periodLabel, "regenerated": false})
}

// ComputeReportData computes a report for the calendar period [start, end]
// exactly as the report pages do. The scheduler's report generation, backfill
// and digests use it so stored copies and digests match the pages.
func (h *ReportsHandler) ComputeReportData(ctx context.Context, start, end time.Time) (ReportData, error) {
	return h.loadReportData(ctx, start, end)
}

// loadReportData loads everything a report needs and computes it for the
// calendar period [start, end].
func (h *ReportsHandler) loadReportData(ctx context.Context, start, end time.Time) (ReportData, error) {
	snaps, err := h.queries.ListNetWorthSnapshots(ctx, 100000)
	if err != nil {
		return ReportData{}, fmt.Errorf("list snapshots: %w", err)
	}
	txns, err := listAllTransactionsRows(ctx, h.queries)
	if err != nil {
		return ReportData{}, fmt.Errorf("list transactions: %w", err)
	}
	accounts, err := h.queries.ListAccounts(ctx)
	if err != nil {
		return ReportData{}, fmt.Errorf("list accounts: %w", err)
	}
	secs, err := h.queries.ListSecurities(ctx)
	if err != nil {
		log.Printf("WARNING: list securities for report: %v; holdings show ISINs", err)
	}
	hist := loadHistoricalFXRates(ctx, h.queries)
	latest := loadFXRatesMap(ctx, h.queries)

	holdings, err := assets.Load(ctx, h.queries)
	if err != nil {
		log.Printf("WARNING: load assets for report: %v", err)
	}
	physical := buildPhysicalAttributionWithFX(holdings, txns, assets.CivilDate(time.Now()), hist, latest)

	chrono := make([]db.ListTransactionsRow, len(txns))
	for i := range txns {
		chrono[i] = txns[len(txns)-1-i]
	}

	secNames := make(map[string]string, len(secs))
	for _, s := range secs {
		secNames[s.ISIN] = s.Name
	}

	return buildReportData(reportInputs{
		start:    start,
		end:      end,
		snaps:    snaps,
		txns:     txns,
		implicit: computeImplicitCashClampEvents(accounts, chrono, hist, latest),
		physical: physical,
		fx:       newReportFX(hist, latest),
		secNames: secNames,
		priceAt: func(isin string, at time.Time) (float64, string, bool) {
			row, err := h.queries.GetPriceAtDate(ctx, db.GetPriceAtDateParams{SecurityISIN: isin, Date: at})
			if err != nil || !row.Close.Valid {
				return 0, "", false
			}
			return numericToFloat(row.Close), row.Currency, true
		},
	}), nil
}

// reportInputs is everything buildReportData reads; it touches no database.
type reportInputs struct {
	start, end time.Time // calendar period, both days inclusive
	snaps      []db.NetWorthSnapshot
	txns       []db.ListTransactionsRow // newest first, as ListAllTransactions returns them
	implicit   []implicitCashFlowEvent
	physical   physicalAttribution
	fx         reportFX
	secNames   map[string]string
	priceAt    func(isin string, at time.Time) (close float64, currency string, ok bool)
}

// reportFX converts to EUR at the ECB rate of a given day.
type reportFX struct {
	byCur  map[string][]market.HistoricalFXRate // per currency, date ascending
	latest map[string]float64
}

func newReportFX(hist []market.HistoricalFXRate, latest map[string]float64) reportFX {
	byCur := make(map[string][]market.HistoricalFXRate)
	for _, r := range hist {
		if r.Rate > 0 {
			byCur[r.Currency] = append(byCur[r.Currency], r)
		}
	}
	for _, pts := range byCur {
		sort.SliceStable(pts, func(i, j int) bool { return pts[i].Date.Before(pts[j].Date) })
	}
	return reportFX{byCur: byCur, latest: latest}
}

// toEUR converts amount at the rate of the last ECB business day on or before
// date (the earliest stored rate for dates before the history starts, the
// latest known rate when the currency has no history). With no rate at all it
// returns ok=false instead of counting 1 USD as 1 €.
func (f reportFX) toEUR(amount float64, currency string, date time.Time) (float64, bool) {
	norm, scale := normalizeCurrencyAndScale(currency)
	scaled := amount * scale
	if norm == "EUR" || amount == 0 {
		return scaled, true
	}
	if pts := f.byCur[norm]; len(pts) > 0 {
		day := time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, time.UTC)
		i := sort.Search(len(pts), func(i int) bool { return pts[i].Date.After(day) })
		if i > 0 {
			return scaled / pts[i-1].Rate, true
		}
		return scaled / pts[0].Rate, true
	}
	if rate := f.latest[norm]; rate > 0 {
		return scaled / rate, true
	}
	return 0, false
}

// buildReportData computes a report for the calendar period [start, end].
//
// Net worth runs from the previous period's close (the last snapshot before
// start) to the last snapshot in the period, so consecutive reports chain and
// a month's first-day move is not lost. Snapshots before the first non-zero
// one are placeholders written before any data existed. The change splits
// into net deposits (external flows between the two snapshots, classified
// like the attribution waterfall) and the market residual.
func buildReportData(in reportInputs) ReportData {
	out := ReportData{Holdings: []ReportHolding{}}
	missingFX := map[string]bool{}
	toEUR := func(amount float64, currency string, at time.Time) float64 {
		v, ok := in.fx.toEUR(amount, currency, at)
		if !ok {
			norm, _ := normalizeCurrencyAndScale(currency)
			missingFX[norm] = true
		}
		return v
	}

	// --- Net worth: previous close → period close.
	snaps := append([]db.NetWorthSnapshot(nil), in.snaps...)
	sort.Slice(snaps, func(i, j int) bool { return snaps[i].Date.Before(snaps[j].Date) })
	var firstNonZero time.Time
	for _, s := range snaps {
		if numericToFloat(s.Total) != 0 {
			firstNonZero = s.Date
			break
		}
	}
	var startSnap, endSnap *db.NetWorthSnapshot
	for i := range snaps {
		s := &snaps[i]
		if s.Date.Before(in.start) {
			startSnap = s
		} else if !s.Date.After(in.end) {
			endSnap = s
		}
	}
	if endSnap != nil && !firstNonZero.IsZero() && !endSnap.Date.Before(firstNonZero) {
		out.HasNetWorth = true
		nwStart, from := 0.0, time.Time{}
		if startSnap != nil {
			nwStart = numericToFloat(startSnap.Total)
			from = startSnap.Date
			out.StartDate = startSnap.Date.Format("2006-01-02")
		}
		nwEnd := numericToFloat(endSnap.Total)
		to := endSnap.Date
		out.EndDate = to.Format("2006-01-02")

		deposits := reportNetDeposits(in, from, to, toEUR)
		change := nwEnd - nwStart
		out.NetWorthStart = round2(nwStart)
		out.NetWorthEnd = round2(nwEnd)
		out.NetWorthChange = round2(change)
		if nwStart > 0 {
			out.NetWorthChangePct = math.Round(change/nwStart*1000) / 10
		}
		out.NetDeposits = round2(deposits)
		out.MarketChange = round2(change - deposits)
	}

	// --- Period activity and dividends (calendar period).
	gross, withheld := 0.0, 0.0
	for _, t := range in.txns {
		if t.Date.Before(in.start) || t.Date.After(in.end) {
			continue
		}
		out.NewTransactions++
		if t.Type != "dividend" {
			continue
		}
		gross += toEUR(math.Abs(numericToFloat(t.Amount)), t.Currency, t.Date)
		withheld += toEUR(math.Abs(numericToFloat(t.Fee))+math.Abs(numericToFloat(t.Tax)), t.Currency, t.Date)
	}
	out.DividendsGross = round2(gross)
	out.DividendsWithheld = round2(withheld)
	out.TotalDividends = round2(gross - withheld)

	// --- Holdings at period end: replay chronologically up to `end` so a
	// historical report shows the portfolio as it was, at average cost.
	type replayHold struct{ qty, cost float64 }
	replayed := make(map[string]*replayHold)
	for i := len(in.txns) - 1; i >= 0; i-- {
		t := in.txns[i]
		if t.Date.After(in.end) || !t.SecurityISIN.Valid || t.SecurityISIN.String == "" {
			continue
		}
		isin := t.SecurityISIN.String
		qty := numericToFloat(t.Quantity)
		switch t.Type {
		case "buy", "savings_plan", "transfer":
			if qty <= 0 {
				continue
			}
			rh := replayed[isin]
			if rh == nil {
				rh = &replayHold{}
				replayed[isin] = rh
			}
			rh.qty += qty
			cost := toEUR(math.Abs(numericToFloat(t.Amount)), t.Currency, t.Date)
			if t.Type != "transfer" {
				cost += toEUR(math.Abs(numericToFloat(t.Fee)), t.Currency, t.Date)
			}
			rh.cost += cost
		case "sell", "transfer_out":
			if rh := replayed[isin]; rh != nil && rh.qty > 0 && qty > 0 {
				sold := math.Min(qty, rh.qty)
				rh.cost -= rh.cost / rh.qty * sold
				rh.qty -= sold
				if rh.qty <= 1e-9 {
					delete(replayed, isin)
				}
			}
		}
	}

	type holdAgg struct {
		isin, name  string
		value, cost float64
	}
	var aggs []holdAgg
	totalValue := 0.0
	for isin, rh := range replayed {
		if rh.qty <= 1e-9 {
			continue
		}
		value := rh.cost
		if in.priceAt != nil {
			if px, cur, ok := in.priceAt(isin, in.end); ok && px > 0 {
				if v, ok := in.fx.toEUR(rh.qty*px, cur, in.end); ok {
					value = v
				} else {
					norm, _ := normalizeCurrencyAndScale(cur)
					missingFX[norm] = true
				}
			}
		}
		name := in.secNames[isin]
		if name == "" {
			name = isin
		}
		aggs = append(aggs, holdAgg{isin: isin, name: name, value: value, cost: rh.cost})
		totalValue += value
	}

	for _, a := range aggs {
		weight := 0.0
		if totalValue > 0 {
			weight = a.value / totalValue * 100
		}
		retPct := 0.0
		if a.cost > 0 {
			retPct = (a.value - a.cost) / a.cost * 100
		}
		out.Holdings = append(out.Holdings, ReportHolding{
			ISIN: a.isin, Name: a.name, Value: round2(a.value),
			Weight: math.Round(weight*10) / 10, ReturnPct: math.Round(retPct*10) / 10,
		})
	}
	sort.Slice(out.Holdings, func(i, j int) bool {
		if out.Holdings[i].Value != out.Holdings[j].Value {
			return out.Holdings[i].Value > out.Holdings[j].Value
		}
		return out.Holdings[i].ISIN < out.Holdings[j].ISIN
	})

	// Best and weakest only mean something with two or more holdings.
	if len(out.Holdings) >= 2 {
		best, worst := out.Holdings[0], out.Holdings[0]
		for _, hld := range out.Holdings[1:] {
			if hld.ReturnPct > best.ReturnPct {
				best = hld
			}
			if hld.ReturnPct < worst.ReturnPct {
				worst = hld
			}
		}
		if best.ISIN != worst.ISIN {
			out.TopGainer = &best
			out.TopLoser = &worst
		}
	}

	if len(missingFX) > 0 {
		curs := make([]string, 0, len(missingFX))
		for c := range missingFX {
			curs = append(curs, c)
		}
		sort.Strings(curs)
		out.Warnings = append(out.Warnings, fmt.Sprintf(
			"No exchange rate for %s, so amounts in %s are left out of the totals.",
			strings.Join(curs, ", "), strings.Join(curs, ", ")))
	}
	return out
}

// reportNetDeposits sums the external flows in (from, to] in EUR, classified
// like the attribution waterfall (HandleWealthWaterfall): deposits, RSU vests
// and other in-kind receipts in, withdrawals out; broker-to-broker in-kind
// pairs and payments linked to physical assets or loans are swaps. Implicit
// funding of securities-only accounts and asset/loan adjustments count too.
func reportNetDeposits(in reportInputs, from, to time.Time, toEUR func(float64, string, time.Time) float64) float64 {
	var inkind []analytics.InKindTransfer
	for _, t := range in.txns {
		if t.SecurityISIN.Valid && (t.Type == "transfer" || t.Type == "transfer_out") {
			inkind = append(inkind, analytics.InKindTransfer{
				ID: t.ID, Date: t.Date, Type: t.Type, ISIN: t.SecurityISIN.String,
				Quantity: numericToFloat(t.Quantity),
			})
		}
	}
	washed := analytics.MatchInKindTransferPairs(inkind)

	inWindow := func(d time.Time) bool { return d.After(from) && !d.After(to) }
	total := 0.0
	for _, t := range in.txns {
		if !inWindow(t.Date) {
			continue
		}
		if _, w := washed[t.ID]; w || in.physical.linked[t.ID] {
			continue
		}
		switch analytics.ClassifyForAttribution(t.Type, t.SecurityISIN.Valid, analytics.ScopePortfolio) {
		case analytics.BucketContribution:
			total += toEUR(math.Abs(numericToFloat(t.Amount)), t.Currency, t.Date)
		case analytics.BucketWithdrawal:
			total -= toEUR(math.Abs(numericToFloat(t.Amount)), t.Currency, t.Date)
		}
	}
	for _, ev := range in.implicit {
		if inWindow(ev.Date) {
			total += ev.DeltaEUR
		}
	}
	return total + in.physical.window(from, to).contrib
}

// HandleDownloadPDF generates and streams a PDF for a given report.
func (h *ReportsHandler) HandleDownloadPDF(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid report id")
		return
	}

	rpt, err := h.queries.GetWealthReport(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "report not found")
		return
	}

	data := h.currentReportData(r.Context(), rpt)

	pdf := fpdf.New("P", "mm", "A4", "")
	tr := pdf.UnicodeTranslatorFromDescriptor("")
	pdf.SetAutoPageBreak(true, 20)
	pdf.AddPage()

	// Title
	pdf.SetFont("Helvetica", "B", 20)
	pdf.CellFormat(0, 12, tr("Wealth Report"), "", 1, "C", false, 0, "")
	pdf.SetFont("Helvetica", "", 12)
	pdf.CellFormat(0, 8, tr(rpt.PeriodLabel+" ("+rpt.ReportType+")"), "", 1, "C", false, 0, "")
	pdf.Ln(8)

	row := func(label, value string) {
		pdf.CellFormat(95, 7, tr(label), "", 0, "", false, 0, "")
		pdf.CellFormat(95, 7, tr(value), "", 1, "R", false, 0, "")
	}
	signedEUR := func(v float64) string {
		if v > 0 {
			return "+" + fmtEUR(v) + " EUR"
		}
		return fmtEUR(v) + " EUR"
	}

	// Summary KPIs
	pdf.SetFont("Helvetica", "B", 14)
	pdf.CellFormat(0, 8, tr("Summary"), "", 1, "", false, 0, "")
	pdf.SetFont("Helvetica", "", 11)
	if data.HasNetWorth {
		startLabel := "Net worth at previous close:"
		if data.StartDate != "" {
			startLabel = fmt.Sprintf("Net worth at previous close (%s):", data.StartDate)
		}
		row(startLabel, fmtEUR(data.NetWorthStart)+" EUR")
		row(fmt.Sprintf("Net worth at close (%s):", data.EndDate), fmtEUR(data.NetWorthEnd)+" EUR")
		changePct := ""
		if data.NetWorthChangePct != 0 {
			changePct = fmt.Sprintf(" (%+.1f%%)", data.NetWorthChangePct)
		}
		row("Change:", signedEUR(data.NetWorthChange)+changePct)
		row("  of which net deposits:", signedEUR(data.NetDeposits))
		row("  of which market, income and other:", signedEUR(data.MarketChange))
	} else {
		row("Net worth:", "no history for this period")
	}
	row("Dividends received (net):", fmtEUR(data.TotalDividends)+" EUR")
	if data.DividendsWithheld != 0 {
		row("  gross, less withheld tax and fees:", fmtEUR(data.DividendsGross)+" - "+fmtEUR(data.DividendsWithheld)+" EUR")
	}
	row("Transactions:", fmt.Sprintf("%d", data.NewTransactions))
	pdf.Ln(4)

	// Top performers
	if data.TopGainer != nil {
		row("Best holding:", fmt.Sprintf("%s (%+.1f%%)", truncateRunes(data.TopGainer.Name, 35), data.TopGainer.ReturnPct))
	}
	if data.TopLoser != nil {
		row("Weakest holding:", fmt.Sprintf("%s (%+.1f%%)", truncateRunes(data.TopLoser.Name, 35), data.TopLoser.ReturnPct))
	}
	pdf.Ln(6)

	// Holdings table
	if len(data.Holdings) > 0 {
		pdf.SetFont("Helvetica", "B", 14)
		pdf.CellFormat(0, 8, tr("Holdings"), "", 1, "", false, 0, "")

		pdf.SetFont("Helvetica", "B", 9)
		pdf.CellFormat(70, 6, tr("Name"), "B", 0, "", false, 0, "")
		pdf.CellFormat(30, 6, tr("Value"), "B", 0, "R", false, 0, "")
		pdf.CellFormat(25, 6, tr("Weight"), "B", 0, "R", false, 0, "")
		pdf.CellFormat(25, 6, tr("Return"), "B", 1, "R", false, 0, "")

		pdf.SetFont("Helvetica", "", 9)
		for _, h := range data.Holdings {
			name := truncateRunes(h.Name, 35)
			pdf.CellFormat(70, 6, tr(name), "", 0, "", false, 0, "")
			pdf.CellFormat(30, 6, tr(fmtEUR(h.Value)), "", 0, "R", false, 0, "")
			pdf.CellFormat(25, 6, fmt.Sprintf("%.1f%%", h.Weight), "", 0, "R", false, 0, "")
			pdf.CellFormat(25, 6, fmt.Sprintf("%.1f%%", h.ReturnPct), "", 1, "R", false, 0, "")
		}
	}

	for _, warn := range data.Warnings {
		pdf.Ln(2)
		pdf.SetFont("Helvetica", "I", 9)
		pdf.MultiCell(0, 5, tr(warn), "", "", false)
	}

	// Footer
	pdf.Ln(10)
	pdf.SetFont("Helvetica", "I", 8)
	pdf.CellFormat(0, 5, tr(fmt.Sprintf("Figures as of %s, Wealth Tracker", time.Now().UTC().Format("2006-01-02 15:04"))), "", 0, "C", false, 0, "")

	if err := pdf.Error(); err != nil {
		writeError(w, http.StatusInternalServerError, "generate pdf: "+err.Error())
		return
	}

	var buf bytes.Buffer
	if err := pdf.Output(&buf); err != nil {
		writeError(w, http.StatusInternalServerError, "render pdf: "+err.Error())
		return
	}

	filename := fmt.Sprintf("wealth-report-%s.pdf", rpt.PeriodLabel)
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", "attachment; filename="+filename)
	_, _ = w.Write(buf.Bytes())
}

func truncateRunes(s string, max int) string {
	runes := []rune(s)
	if max <= 3 || len(runes) <= max {
		return s
	}
	return string(runes[:max-3]) + "..."
}
