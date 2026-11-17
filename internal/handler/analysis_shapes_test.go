package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The benchmark replay must answer 200 with a machine-readable "insufficient data"
// body (never 400) when the benchmark has too few price rows, and that body must
// keep every field of the normal response so existing clients do not break.
func TestBenchmarkInsufficientResponse_Shape(t *testing.T) {
	rec := httptest.NewRecorder()
	writeJSON(rec, http.StatusOK, benchmarkInsufficientResponse("XX0000000001", "Test Benchmark", 7))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["available"] != false {
		t.Errorf("available = %v, want false", body["available"])
	}
	if body["reason"] != "insufficient_data" {
		t.Errorf("reason = %v, want insufficient_data", body["reason"])
	}
	if body["have"] != float64(7) {
		t.Errorf("have = %v, want 7", body["have"])
	}
	if body["need"] != float64(benchmarkMinPrices) {
		t.Errorf("need = %v, want %d", body["need"], benchmarkMinPrices)
	}
	for _, key := range []string{"benchmark_name", "benchmark_isin", "actual_value", "benchmark_value", "difference"} {
		if _, ok := body[key]; !ok {
			t.Errorf("missing existing field %q", key)
		}
	}
	comparison, ok := body["comparison"].([]any)
	if !ok {
		t.Fatalf("comparison = %v (%T), want an empty array, never null", body["comparison"], body["comparison"])
	}
	if len(comparison) != 0 {
		t.Errorf("comparison has %d points, want 0", len(comparison))
	}
}

func TestBenchmarkInsufficientResponse_NegativeHaveClampsToZero(t *testing.T) {
	body := benchmarkInsufficientResponse("XX0000000001", "Test Benchmark", -3)
	if body["have"] != 0 {
		t.Errorf("have = %v, want 0", body["have"])
	}
}

// Regression for the Costs crash: an empty (non-nil) slice must serialise as [],
// which is what the handlers now initialise their response slices to.
func TestWriteJSON_EmptySliceIsArrayNotNull(t *testing.T) {
	type costEntry struct {
		ISIN string `json:"isin"`
	}
	rec := httptest.NewRecorder()
	writeJSON(rec, http.StatusOK, map[string]any{"holdings": []costEntry{}, "projection": []map[string]any{}})
	got := rec.Body.String()
	if !strings.Contains(got, `"holdings":[]`) || !strings.Contains(got, `"projection":[]`) {
		t.Errorf("body = %s, want empty arrays", got)
	}
	if strings.Contains(got, "null") {
		t.Errorf("body = %s, must not contain null", got)
	}
}

// The calendar title is a plain string, so it must carry exactly one percent sign.
func TestTaxCalendarFSATitle_SinglePercent(t *testing.T) {
	if strings.Contains(taxCalendarFSATitle, "%%") {
		t.Errorf("title %q contains a double percent sign", taxCalendarFSATitle)
	}
	if strings.Count(taxCalendarFSATitle, "%") != 1 {
		t.Errorf("title %q should contain exactly one percent sign", taxCalendarFSATitle)
	}
}
