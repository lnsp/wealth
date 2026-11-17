package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"sort"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-pdf/fpdf"
	"github.com/google/uuid"

	db "github.com/lnsp/wealth/internal/database/generated"
)

type ReportsHandler struct {
	queries *db.Queries
}

func NewReportsHandler(q *db.Queries) *ReportsHandler {
	return &ReportsHandler{queries: q}
}

// ReportData is the JSON structure stored in wealth_reports.data.
type ReportData struct {
	NetWorthStart    float64          `json:"net_worth_start"`
	NetWorthEnd      float64          `json:"net_worth_end"`
	NetWorthChange   float64          `json:"net_worth_change"`
	NetWorthChangePct float64         `json:"net_worth_change_pct"`
	TotalDividends   float64          `json:"total_dividends"`
	NewTransactions  int              `json:"new_transactions"`
	Holdings         []ReportHolding  `json:"holdings"`
	TopGainer        *ReportHolding   `json:"top_gainer,omitempty"`
	TopLoser         *ReportHolding   `json:"top_loser,omitempty"`
}

type ReportHolding struct {
	ISIN       string  `json:"isin"`
	Name       string  `json:"name"`
	Value      float64 `json:"value"`
	Weight     float64 `json:"weight"`
	ReturnPct  float64 `json:"return_pct,omitempty"`
}

func (h *ReportsHandler) HandleListReports(w http.ResponseWriter, r *http.Request) {
	reports, err := h.queries.ListWealthReports(r.Context(), 24)
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

	var data ReportData
	json.Unmarshal(rpt.Data, &data)

	writeJSON(w, http.StatusOK, map[string]any{
		"id":           rpt.ID.String(),
		"report_type":  rpt.ReportType,
		"period_label": rpt.PeriodLabel,
		"period_start": rpt.PeriodStart.Format("2006-01-02"),
		"period_end":   rpt.PeriodEnd.Format("2006-01-02"),
		"generated_at": rpt.GeneratedAt.Format(time.RFC3339),
		"data":         data,
	})
}

// HandleGenerateReport triggers generation of a report for a given period.
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

	// Check if report already exists
	exists, _ := h.queries.ReportExistsForPeriod(ctx, db.ReportExistsForPeriodParams{ReportType: req.ReportType, PeriodLabel: periodLabel})
	if exists {
		writeError(w, http.StatusConflict, "report already exists for "+periodLabel)
		return
	}

	data := h.computeReport(ctx, periodStart, periodEnd)
	dataJSON, _ := json.Marshal(data)

	if err := h.queries.InsertWealthReport(ctx, db.InsertWealthReportParams{
		ReportType: req.ReportType, PeriodLabel: periodLabel, PeriodStart: periodStart, PeriodEnd: periodEnd, Data: dataJSON,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "save report: "+err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{"status": "ok", "period": periodLabel})
}

func (h *ReportsHandler) computeReport(ctx context.Context, start, end time.Time) ReportData {
	// Get net worth at start and end of period
	snaps, _ := h.queries.ListNetWorthSnapshots(ctx, 5000)

	nwStart, nwEnd := 0.0, 0.0
	for _, s := range snaps {
		val := 0.0
		if s.Total.Valid {
			f, _ := s.Total.Float64Value()
			val = f.Float64
		}
		if !s.Date.Before(start) && !s.Date.After(end) {
			if nwEnd == 0 {
				nwEnd = val // first (newest) in range
			}
			nwStart = val // last (oldest) in range
		}
	}

	change := nwEnd - nwStart
	changePct := 0.0
	if nwStart > 0 {
		changePct = (change / nwStart) * 100
	}

	// Load all transactions for period stats and historical holdings replay as of period end
	txns, _ := h.queries.ListAllTransactions(ctx)
	fxRates, _ := h.queries.ListAllExchangeRates(ctx)
	type fxPt struct {
		date time.Time
		rate float64
	}
	fxByCur := make(map[string][]fxPt)
	for _, r := range fxRates {
		if r.Rate.Valid {
			f, _ := r.Rate.Float64Value()
			if f.Float64 > 0 {
				norm, _ := normalizeCurrencyAndScale(r.Currency)
				fxByCur[norm] = append(fxByCur[norm], fxPt{date: r.Date, rate: f.Float64})
			}
		}
	}
	toEURAt := func(amount float64, cur string, dt time.Time) float64 {
		if amount == 0 {
			return 0
		}
		norm, scale := normalizeCurrencyAndScale(cur)
		scaled := amount * scale
		if norm == "EUR" {
			return scaled
		}
		pts := fxByCur[norm]
		if len(pts) == 0 {
			return scaled
		}
		lo, hi, res := 0, len(pts)-1, -1
		for lo <= hi {
			mid := (lo + hi) / 2
			if !pts[mid].date.After(dt) {
				res = mid
				lo = mid + 1
			} else {
				hi = mid - 1
			}
		}
		if res >= 0 {
			return scaled / pts[res].rate
		}
		return scaled / pts[0].rate
	}

	newTxns := 0
	dividends := 0.0
	for _, txn := range txns {
		if txn.Date.Before(start) || txn.Date.After(end) {
			continue
		}
		newTxns++
		if txn.Type == "dividend" && txn.Amount.Valid {
			f, _ := txn.Amount.Float64Value()
			dividends += toEURAt(f.Float64, txn.Currency, txn.Date)
		}
	}

	// Replay holdings chronologically up to period end (`end`) so historical
	// reports reflect the portfolio at the end of that period rather than today.
	type replayHold struct {
		qty  float64
		cost float64
	}
	replayed := make(map[string]*replayHold)
	for i := len(txns) - 1; i >= 0; i-- {
		txn := txns[i]
		if txn.Date.After(end) || !txn.SecurityISIN.Valid || txn.SecurityISIN.String == "" {
			continue
		}
		isin := txn.SecurityISIN.String
		qty := numericToFloat(txn.Quantity)
		amtEUR := toEURAt(numericToFloat(txn.Amount), txn.Currency, txn.Date)
		feeEUR := toEURAt(numericToFloat(txn.Fee), txn.Currency, txn.Date)

		switch txn.Type {
		case "buy", "savings_plan":
			if qty > 0 {
				rh := replayed[isin]
				if rh == nil {
					rh = &replayHold{}
					replayed[isin] = rh
				}
				rh.qty += qty
				rh.cost += amtEUR + feeEUR
			}
		case "transfer":
			if qty > 0 {
				rh := replayed[isin]
				if rh == nil {
					rh = &replayHold{}
					replayed[isin] = rh
				}
				rh.qty += qty
				if amtEUR > 0 {
					rh.cost += amtEUR
				}
			}
		case "sell", "transfer_out":
			if rh := replayed[isin]; rh != nil && rh.qty > 0 && qty > 0 {
				sellQty := math.Min(qty, rh.qty)
				avg := rh.cost / rh.qty
				rh.qty -= sellQty
				rh.cost -= avg * sellQty
				if rh.qty <= 1e-9 {
					delete(replayed, isin)
				}
			}
		}
	}

	secs, _ := h.queries.ListSecurities(ctx)
	secNames := make(map[string]string, len(secs))
	for _, sec := range secs {
		secNames[sec.ISIN] = sec.Name
	}

	totalValue := 0.0
	type holdAgg struct {
		isin, name  string
		value, cost float64
	}
	agg := make(map[string]*holdAgg)
	for isin, rh := range replayed {
		if rh.qty <= 1e-9 {
			continue
		}
		value := rh.cost
		if priceRow, err := h.queries.GetPriceAtDate(ctx, db.GetPriceAtDateParams{
			SecurityISIN: isin,
			Date:         end,
		}); err == nil && priceRow.Close.Valid {
			f, _ := priceRow.Close.Float64Value()
			if f.Float64 > 0 {
				value = toEURAt(rh.qty*f.Float64, priceRow.Currency, end)
			}
		}
		name := secNames[isin]
		if name == "" {
			name = isin
		}
		agg[isin] = &holdAgg{isin: isin, name: name, value: value, cost: rh.cost}
		totalValue += value
	}

	reportHoldings := make([]ReportHolding, 0, len(agg))
	var topGainer, topLoser *ReportHolding
	for _, a := range agg {
		weight := 0.0
		if totalValue > 0 {
			weight = (a.value / totalValue) * 100
		}
		retPct := 0.0
		if a.cost > 0 {
			retPct = ((a.value - a.cost) / a.cost) * 100
		}
		rh := ReportHolding{
			ISIN: a.isin, Name: a.name, Value: math.Round(a.value*100) / 100,
			Weight: math.Round(weight*10) / 10, ReturnPct: math.Round(retPct*10) / 10,
		}
		reportHoldings = append(reportHoldings, rh)
		if topGainer == nil || retPct > topGainer.ReturnPct {
			g := rh
			topGainer = &g
		}
		if topLoser == nil || retPct < topLoser.ReturnPct {
			l := rh
			topLoser = &l
		}
	}
	sort.Slice(reportHoldings, func(i, j int) bool {
		if reportHoldings[i].Value != reportHoldings[j].Value {
			return reportHoldings[i].Value > reportHoldings[j].Value
		}
		return reportHoldings[i].ISIN < reportHoldings[j].ISIN
	})

	return ReportData{
		NetWorthStart:     math.Round(nwStart*100) / 100,
		NetWorthEnd:       math.Round(nwEnd*100) / 100,
		NetWorthChange:    math.Round(change*100) / 100,
		NetWorthChangePct: math.Round(changePct*10) / 10,
		TotalDividends:    math.Round(dividends*100) / 100,
		NewTransactions:   newTxns,
		Holdings:          reportHoldings,
		TopGainer:         topGainer,
		TopLoser:          topLoser,
	}
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

	var data ReportData
	if err := json.Unmarshal(rpt.Data, &data); err != nil {
		writeError(w, http.StatusInternalServerError, "decode report data: "+err.Error())
		return
	}

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

	// Summary KPIs
	pdf.SetFont("Helvetica", "B", 14)
	pdf.CellFormat(0, 8, tr("Summary"), "", 1, "", false, 0, "")
	pdf.SetFont("Helvetica", "", 11)
	pdf.CellFormat(95, 7, tr("Net Worth Start:"), "", 0, "", false, 0, "")
	pdf.CellFormat(95, 7, tr(fmtEUR(data.NetWorthStart)+" EUR"), "", 1, "R", false, 0, "")
	pdf.CellFormat(95, 7, tr("Net Worth End:"), "", 0, "", false, 0, "")
	pdf.CellFormat(95, 7, tr(fmtEUR(data.NetWorthEnd)+" EUR"), "", 1, "R", false, 0, "")
	pdf.CellFormat(95, 7, tr("Change:"), "", 0, "", false, 0, "")
	changePct := ""
	if data.NetWorthChangePct != 0 {
		changePct = fmt.Sprintf(" (%.1f%%)", data.NetWorthChangePct)
	}
	pdf.CellFormat(95, 7, tr(fmtEUR(data.NetWorthChange)+" EUR"+changePct), "", 1, "R", false, 0, "")
	pdf.CellFormat(95, 7, tr("Dividends Received:"), "", 0, "", false, 0, "")
	pdf.CellFormat(95, 7, tr(fmtEUR(data.TotalDividends)+" EUR"), "", 1, "R", false, 0, "")
	pdf.CellFormat(95, 7, tr("Transactions:"), "", 0, "", false, 0, "")
	pdf.CellFormat(95, 7, fmt.Sprintf("%d", data.NewTransactions), "", 1, "R", false, 0, "")
	pdf.Ln(4)

	// Top performers
	if data.TopGainer != nil {
		pdf.CellFormat(95, 7, tr("Top Gainer:"), "", 0, "", false, 0, "")
		pdf.CellFormat(95, 7, tr(fmt.Sprintf("%s (+%.1f%%)", truncateRunes(data.TopGainer.Name, 35), data.TopGainer.ReturnPct)), "", 1, "R", false, 0, "")
	}
	if data.TopLoser != nil {
		pdf.CellFormat(95, 7, tr("Top Loser:"), "", 0, "", false, 0, "")
		pdf.CellFormat(95, 7, tr(fmt.Sprintf("%s (%.1f%%)", truncateRunes(data.TopLoser.Name, 35), data.TopLoser.ReturnPct)), "", 1, "R", false, 0, "")
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

	// Footer
	pdf.Ln(10)
	pdf.SetFont("Helvetica", "I", 8)
	pdf.CellFormat(0, 5, tr(fmt.Sprintf("Generated %s by Wealth Tracker", rpt.GeneratedAt.Format("2006-01-02 15:04"))), "", 0, "C", false, 0, "")

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
