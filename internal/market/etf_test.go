package market

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

const testJustETFHTML = `<html><body>
<div data-testid="etf-holdings_top-holdings_row">
  <span data-testid="tl_etf-holdings_top-holdings_link_name"><a href="/en/etf-profile.html?isin=US0378331005">Apple Inc.</a></span>
  <span data-testid="tl_etf-holdings_top-holdings_value_percentage">7.23%</span>
</div>
<div data-testid="etf-holdings_top-holdings_row">
  <span data-testid="tl_etf-holdings_top-holdings_link_name"><a href="/en/etf-profile.html?isin=US5949181045&foo=bar">Microsoft Corp.</a></span>
  <span data-testid="tl_etf-holdings_top-holdings_value_percentage">6,50%</span>
</div>
<div data-testid="etf-holdings_top-holdings_row">
  <span data-testid="tl_etf-holdings_top-holdings_value_name">NVIDIA Corp.</span>
  <span data-testid="tl_etf-holdings_top-holdings_value_percentage">5.10%</span>
</div>
<div data-testid="etf-holdings_top-holdings_row">
  <span data-testid="tl_etf-holdings_top-holdings_value_name"></span>
  <span data-testid="tl_etf-holdings_top-holdings_value_percentage">3.00%</span>
</div>
</body></html>`

func TestFetchHoldings(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(testJustETFHTML))
	}))
	defer srv.Close()

	client := &JustETFClient{httpClient: srv.Client()}

	// Override the URL by making the request directly to the test server
	holdings, err := fetchHoldingsFromURL(client, context.Background(), srv.URL, "IE00B5BMR087")
	if err != nil {
		t.Fatalf("FetchHoldings failed: %v", err)
	}

	// Should parse 3 valid rows (4th has empty name, skipped)
	if len(holdings) != 3 {
		t.Fatalf("expected 3 holdings, got %d", len(holdings))
	}

	// First: Apple with ISIN extracted from link
	if holdings[0].Name != "Apple Inc." {
		t.Errorf("first holding name = %q, want 'Apple Inc.'", holdings[0].Name)
	}
	if holdings[0].ISIN != "US0378331005" {
		t.Errorf("first holding ISIN = %q, want 'US0378331005'", holdings[0].ISIN)
	}
	if holdings[0].Weight != 7.23 {
		t.Errorf("first holding weight = %f, want 7.23", holdings[0].Weight)
	}

	// Second: Microsoft with ISIN extracted (with & in URL)
	if holdings[1].ISIN != "US5949181045" {
		t.Errorf("second holding ISIN = %q, want 'US5949181045'", holdings[1].ISIN)
	}
	if holdings[1].Weight != 6.50 {
		t.Errorf("second holding weight = %f, want 6.50", holdings[1].Weight)
	}

	// Third: NVIDIA without link gets synthetic ISIN
	if holdings[2].Name != "NVIDIA Corp." {
		t.Errorf("third holding name = %q, want 'NVIDIA Corp.'", holdings[2].Name)
	}
	if holdings[2].ISIN == "" {
		t.Error("third holding should have synthetic ISIN")
	}
}

func TestFetchHoldings_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	client := &JustETFClient{httpClient: srv.Client()}
	_, err := fetchHoldingsFromURL(client, context.Background(), srv.URL, "XX")
	if err == nil {
		t.Error("expected error for 500 response")
	}
}

func TestFetchHoldings_EmptyPage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("<html><body></body></html>"))
	}))
	defer srv.Close()

	client := &JustETFClient{httpClient: srv.Client()}
	holdings, err := fetchHoldingsFromURL(client, context.Background(), srv.URL, "XX")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(holdings) != 0 {
		t.Errorf("expected 0 holdings, got %d", len(holdings))
	}
}
