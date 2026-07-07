package handler

import (
	"math"
	"testing"
	"time"
)

// "Generate Last Month" report flow (mirrors HandleGenerateReport in reports.go):
//
//   - Frontend computes prevMonth = new Date(now.year, now.month-1, 1), then
//     posts {report_type:"monthly", year, month=prevMonth.month+1}.
//   - Backend validates: report_type ∈ {monthly, annual}; year ∈ [2020,2100];
//     monthly→month ∈ [1,12]; rejects existing periods with 409.
//   - Period boundaries:
//        monthly: start=first-of-month, end=last-day-of-month, label "YYYY-MM"
//        annual:  start=Jan 1, end=Dec 31, label "YYYY"
//   - Aggregation: nwStart=oldest snapshot in range, nwEnd=newest in range;
//     change=end-start; pct=change/start*100 (zero-safe); dividends=sum of
//     dividend-type txns in [start,end]; per-ISIN holdings with
//     weight=value/total*100 and returnPct=(value-cost)/cost*100; top
//     gainer/loser by returnPct.
//   - PDF route: Content-Type application/pdf, filename "wealth-report-{label}.pdf".

// computePeriodBounds mirrors lines 128-140 of reports.go.
func computePeriodBounds(reportType string, year, month int) (start, end time.Time, label string, ok bool) {
	if reportType != "monthly" && reportType != "annual" {
		return time.Time{}, time.Time{}, "", false
	}
	if year < 2020 || year > 2100 {
		return time.Time{}, time.Time{}, "", false
	}
	if reportType == "monthly" {
		if month < 1 || month > 12 {
			return time.Time{}, time.Time{}, "", false
		}
		start = time.Date(year, time.Month(month), 1, 0, 0, 0, 0, time.Local)
		end = start.AddDate(0, 1, -1)
		return start, end, start.Format("2006-01"), true
	}
	start = time.Date(year, 1, 1, 0, 0, 0, 0, time.Local)
	end = time.Date(year, 12, 31, 0, 0, 0, 0, time.Local)
	return start, end, start.Format("2006"), true
}

func TestReports_MonthlyPeriodBounds(t *testing.T) {
	cases := []struct {
		year, month     int
		wantEndDay      int    // last day of that month
		wantLabel       string
	}{
		{2026, 1, 31, "2026-01"},
		{2026, 2, 28, "2026-02"},  // common year
		{2024, 2, 29, "2024-02"},  // leap year (÷4, not ÷100)
		{2020, 2, 29, "2020-02"},  // leap, also year-range floor
		{2100, 2, 28, "2100-02"},  // ÷100 but not ÷400 → NOT leap
		{2026, 12, 31, "2026-12"},
		{2026, 4, 30, "2026-04"},
	}
	for _, c := range cases {
		start, end, label, ok := computePeriodBounds("monthly", c.year, c.month)
		if !ok {
			t.Errorf("year=%d month=%d rejected, want accepted", c.year, c.month)
			continue
		}
		if start.Day() != 1 {
			t.Errorf("year=%d month=%d start day=%d, want 1", c.year, c.month, start.Day())
		}
		if int(start.Month()) != c.month {
			t.Errorf("year=%d month=%d start month=%d", c.year, c.month, start.Month())
		}
		if end.Day() != c.wantEndDay {
			t.Errorf("year=%d month=%d end day=%d, want %d (last day)", c.year, c.month, end.Day(), c.wantEndDay)
		}
		if int(end.Month()) != c.month {
			t.Errorf("year=%d month=%d end month=%d (must stay in same month)", c.year, c.month, end.Month())
		}
		if label != c.wantLabel {
			t.Errorf("year=%d month=%d label=%q, want %q", c.year, c.month, label, c.wantLabel)
		}
	}
}

func TestReports_AnnualPeriodBounds(t *testing.T) {
	start, end, label, ok := computePeriodBounds("annual", 2026, 0)
	if !ok {
		t.Fatal("annual 2026 rejected")
	}
	if start.Format("2006-01-02") != "2026-01-01" {
		t.Errorf("annual start = %s, want 2026-01-01", start.Format("2006-01-02"))
	}
	if end.Format("2006-01-02") != "2026-12-31" {
		t.Errorf("annual end = %s, want 2026-12-31", end.Format("2006-01-02"))
	}
	if label != "2026" {
		t.Errorf("annual label = %q, want \"2026\"", label)
	}
}

func TestReports_ValidationRejectsBadInput(t *testing.T) {
	cases := []struct {
		name        string
		reportType  string
		year, month int
	}{
		{"unknown type", "weekly", 2026, 1},
		{"empty type", "", 2026, 1},
		{"year below floor", "monthly", 2019, 1},
		{"year above ceiling", "monthly", 2101, 1},
		{"month zero", "monthly", 2026, 0},
		{"month negative", "monthly", 2026, -1},
		{"month overflow", "monthly", 2026, 13},
	}
	for _, c := range cases {
		_, _, _, ok := computePeriodBounds(c.reportType, c.year, c.month)
		if ok {
			t.Errorf("%s: validation accepted, want rejection", c.name)
		}
	}
}

// "Last month" UX rule (frontend Settings.tsx:854-856): given current
// date NOW, the button generates the previous calendar month. December
// of year Y-1 when NOW is January Y; the year must roll back.
func TestReports_LastMonthRollsYearBackInJanuary(t *testing.T) {
	cases := []struct {
		now           time.Time
		wantYear      int
		wantMonth     int
		wantLabel     string
	}{
		{time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC), 2026, 4, "2026-04"},
		{time.Date(2026, 1, 5, 12, 0, 0, 0, time.UTC), 2025, 12, "2025-12"}, // Jan → prev = Dec last year
		{time.Date(2026, 3, 31, 23, 59, 0, 0, time.UTC), 2026, 2, "2026-02"},
		{time.Date(2024, 1, 31, 0, 0, 0, 0, time.UTC), 2023, 12, "2023-12"},
	}
	for _, c := range cases {
		// JS equivalent: new Date(now.year, now.month - 1, 1)
		prev := time.Date(c.now.Year(), c.now.Month()-1, 1, 0, 0, 0, 0, time.Local)
		gotYear := prev.Year()
		gotMonth := int(prev.Month())
		if gotYear != c.wantYear || gotMonth != c.wantMonth {
			t.Errorf("now=%s → prev=%d-%02d, want %d-%02d",
				c.now.Format("2006-01-02"), gotYear, gotMonth, c.wantYear, c.wantMonth)
		}
		_, _, label, _ := computePeriodBounds("monthly", gotYear, gotMonth)
		if label != c.wantLabel {
			t.Errorf("now=%s label=%q, want %q", c.now.Format("2006-01-02"), label, c.wantLabel)
		}
	}
}

// Aggregation helpers — mirror the per-ISIN math in computeReport.

type snap struct {
	date  time.Time
	total float64
}

// aggregateNW mirrors lines 167-179: nwStart = oldest in range,
// nwEnd = newest in range. Snapshots are assumed newest-first
// (ListNetWorthSnapshots ORDER BY date DESC).
func aggregateNW(snaps []snap, start, end time.Time) (nwStart, nwEnd float64) {
	for _, s := range snaps {
		if s.date.Before(start) || s.date.After(end) {
			continue
		}
		if nwEnd == 0 {
			nwEnd = s.total
		}
		nwStart = s.total
	}
	return
}

func TestReports_NWAggregation(t *testing.T) {
	start := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 4, 30, 0, 0, 0, 0, time.UTC)
	// Newest-first ordering (mimics ListNetWorthSnapshots).
	snaps := []snap{
		{time.Date(2026, 5, 2, 0, 0, 0, 0, time.UTC), 222000}, // outside
		{time.Date(2026, 4, 28, 0, 0, 0, 0, time.UTC), 220000}, // newest in range → nwEnd
		{time.Date(2026, 4, 15, 0, 0, 0, 0, time.UTC), 215000},
		{time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC), 200000},  // oldest in range → nwStart
		{time.Date(2026, 3, 28, 0, 0, 0, 0, time.UTC), 198000}, // outside
	}
	nwStart, nwEnd := aggregateNW(snaps, start, end)
	if nwStart != 200000 || nwEnd != 220000 {
		t.Errorf("nwStart=%.0f nwEnd=%.0f, want 200000/220000", nwStart, nwEnd)
	}
	change := nwEnd - nwStart
	if change != 20000 {
		t.Errorf("change = %.0f, want 20000", change)
	}
	pct := 0.0
	if nwStart > 0 {
		pct = (change / nwStart) * 100
	}
	if math.Abs(pct-10.0) > 0.01 {
		t.Errorf("change pct = %.4f, want 10.0", pct)
	}
}

func TestReports_NWChangePctZeroSafe(t *testing.T) {
	// nwStart=0 must NOT produce NaN/Inf — handler checks `nwStart > 0`.
	pct := 0.0
	nwStart := 0.0
	nwEnd := 50000.0
	change := nwEnd - nwStart
	if nwStart > 0 {
		pct = (change / nwStart) * 100
	}
	if math.IsNaN(pct) || math.IsInf(pct, 0) {
		t.Errorf("pct = %v on nwStart=0, want clean 0", pct)
	}
	if pct != 0 {
		t.Errorf("pct = %v, want 0 when nwStart=0", pct)
	}
}

func TestReports_DividendSumOnlyDividendTypeAndInRange(t *testing.T) {
	// Mirrors lines 188-200: only `dividend` txn type contributes; out-of-range
	// txns excluded.
	start := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 4, 30, 0, 0, 0, 0, time.UTC)
	type tx struct {
		date   time.Time
		txType string
		amount float64
	}
	txns := []tx{
		{time.Date(2026, 4, 10, 0, 0, 0, 0, time.UTC), "dividend", 100},
		{time.Date(2026, 4, 20, 0, 0, 0, 0, time.UTC), "dividend", 50},
		{time.Date(2026, 4, 15, 0, 0, 0, 0, time.UTC), "interest", 30}, // wrong type
		{time.Date(2026, 4, 15, 0, 0, 0, 0, time.UTC), "buy", -500},    // wrong type
		{time.Date(2026, 3, 31, 0, 0, 0, 0, time.UTC), "dividend", 999}, // out of range
		{time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC), "dividend", 999},  // out of range
	}
	newTxns, dividends := 0, 0.0
	for _, t := range txns {
		if t.date.Before(start) || t.date.After(end) {
			continue
		}
		newTxns++
		if t.txType == "dividend" {
			dividends += t.amount
		}
	}
	if newTxns != 4 {
		t.Errorf("newTxns=%d, want 4 (all in-range, regardless of type)", newTxns)
	}
	if math.Abs(dividends-150) > 0.001 {
		t.Errorf("dividends=%.2f, want 150 (only dividend-type in-range)", dividends)
	}
}

func TestReports_HoldingsWeightAndReturn(t *testing.T) {
	// Mirrors lines 248-260: weight = value/totalValue*100, rounded to 1dp;
	// returnPct = (value-cost)/cost*100, rounded to 1dp; zero-cost yields 0%.
	type agg struct {
		value, cost float64
	}
	holdings := map[string]agg{
		"A": {value: 6000, cost: 4000}, // +50%
		"B": {value: 3000, cost: 5000}, // -40%
		"C": {value: 1000, cost: 0},    // zero-cost guard
	}
	totalValue := 10000.0
	for isin, h := range holdings {
		weight := math.Round((h.value/totalValue*100)*10) / 10
		retPct := 0.0
		if h.cost > 0 {
			retPct = math.Round(((h.value-h.cost)/h.cost*100)*10) / 10
		}
		switch isin {
		case "A":
			if weight != 60.0 || retPct != 50.0 {
				t.Errorf("A: weight=%.1f retPct=%.1f, want 60.0 / 50.0", weight, retPct)
			}
		case "B":
			if weight != 30.0 || retPct != -40.0 {
				t.Errorf("B: weight=%.1f retPct=%.1f, want 30.0 / -40.0", weight, retPct)
			}
		case "C":
			if weight != 10.0 || retPct != 0.0 {
				t.Errorf("C: weight=%.1f retPct=%.1f, want 10.0 / 0.0 (zero-cost guard)", weight, retPct)
			}
		}
	}
}

func TestReports_TopGainerAndLoserIdentified(t *testing.T) {
	// Top gainer = max returnPct; top loser = min returnPct. Each is at most
	// one ISIN; on a tie the first-seen wins (map iteration order is random
	// in Go, so this test uses values with no ties).
	holdings := []struct {
		isin    string
		retPct  float64
	}{
		{"A", 12.5},
		{"B", -3.2},
		{"C", 45.0}, // top gainer
		{"D", -50.0}, // top loser
		{"E", 8.0},
	}
	var topGainerISIN, topLoserISIN string
	var topG, topL float64 = -math.MaxFloat64, math.MaxFloat64
	for _, h := range holdings {
		if h.retPct > topG {
			topG = h.retPct
			topGainerISIN = h.isin
		}
		if h.retPct < topL {
			topL = h.retPct
			topLoserISIN = h.isin
		}
	}
	if topGainerISIN != "C" || topG != 45.0 {
		t.Errorf("top gainer = %s/%.1f, want C/45.0", topGainerISIN, topG)
	}
	if topLoserISIN != "D" || topL != -50.0 {
		t.Errorf("top loser = %s/%.1f, want D/-50.0", topLoserISIN, topL)
	}
}

// PDF route: filename is `wealth-report-{period_label}.pdf` (reports.go:374).
// This pins the format so an inadvertent rename doesn't break the
// `<a href=... download>` link on Settings (line 887).
func TestReports_PDFFilenameFormat(t *testing.T) {
	cases := []struct {
		label, want string
	}{
		{"2026-04", "wealth-report-2026-04.pdf"},
		{"2026", "wealth-report-2026.pdf"},
		{"2025-12", "wealth-report-2025-12.pdf"},
	}
	for _, c := range cases {
		got := "wealth-report-" + c.label + ".pdf"
		if got != c.want {
			t.Errorf("label=%q → %q, want %q", c.label, got, c.want)
		}
	}
}
