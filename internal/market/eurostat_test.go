package market

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Trimmed real response (prc_hpi_q, geo=DE, purchase=DW_EXST, unit=I15_Q);
// 2026-Q3 is listed but not yet published.
const eurostatFixture = `{
  "version": "2.0", "class": "dataset", "label": "House price index - quarterly data",
  "value": {"0": 155.1, "1": 153.0, "2": 153.3, "3": 153.7},
  "id": ["freq","unit","purchase","geo","time"],
  "dimension": {
    "time": {"category": {"index": {"2023-Q1": 0, "2023-Q2": 1, "2026-Q1": 2, "2026-Q2": 3, "2026-Q3": 4}}}
  }
}`

func TestFetchHousePriceIndex(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Path + "?" + r.URL.RawQuery
		w.Write([]byte(eurostatFixture))
	}))
	defer srv.Close()
	c := &EurostatClient{httpClient: srv.Client(), baseURL: srv.URL}

	pts, err := c.FetchHousePriceIndex(context.Background(), "DE", "DW_EXST")
	if err != nil {
		t.Fatal(err)
	}
	if gotQuery != "/prc_hpi_q?geo=DE&purchase=DW_EXST&unit=I15_Q" {
		t.Errorf("query = %s", gotQuery)
	}
	if len(pts) != 4 {
		t.Fatalf("got %d points, want 4 (unpublished quarter skipped)", len(pts))
	}
	if pts[0].Period.Format("2006-01-02") != "2023-01-01" || pts[0].Value != 155.1 {
		t.Errorf("first = %+v", pts[0])
	}
	if pts[3].Period.Format("2006-01-02") != "2026-04-01" || pts[3].Value != 153.7 {
		t.Errorf("last = %+v", pts[3])
	}
}

func TestFetchHousePriceIndex_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	c := &EurostatClient{httpClient: srv.Client(), baseURL: srv.URL}
	if _, err := c.FetchHousePriceIndex(context.Background(), "DE", "DW_EXST"); err == nil {
		t.Error("expected an error on HTTP 503")
	}
}

func TestParseQuarter(t *testing.T) {
	for in, want := range map[string]string{"2026-Q1": "2026-01-01", "2025-Q4": "2025-10-01"} {
		got, err := parseQuarter(in)
		if err != nil || got.Format("2006-01-02") != want {
			t.Errorf("parseQuarter(%q) = %v, %v", in, got, err)
		}
	}
	for _, bad := range []string{"2026", "2026-Q5", "x-Q1"} {
		if _, err := parseQuarter(bad); err == nil {
			t.Errorf("parseQuarter(%q) should fail", bad)
		}
	}
}
