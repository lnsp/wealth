package market

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
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

	// Audit C3: the synthetic ID must not depend on the ETF, or the same
	// company never matches across funds.
	other, err := fetchHoldingsFromURL(client, context.Background(), srv.URL, "IE00BJ0KDQ92")
	if err != nil {
		t.Fatalf("FetchHoldings failed: %v", err)
	}
	if other[2].ISIN != holdings[2].ISIN || holdings[2].ISIN != SyntheticConstituentID("NVIDIA Corp.") {
		t.Errorf("synthetic ids %q / %q differ between ETFs", holdings[2].ISIN, other[2].ISIN)
	}
}

func TestISINFromHref(t *testing.T) {
	cases := map[string]string{
		"/en/etf-profile.html?isin=US0378331005":                     "US0378331005",
		"/en/etf-profile.html?isin=US5949181045&foo=1":               "US5949181045",
		"/en/stock-profiles/US67066G1040":                            "US67066G1040",
		"https://www.justetf.com/de/stock-profiles/us02079k1079?x=1": "US02079K1079",
		"/en/stock-profiles/":                                        "",
		"/en/news":                                                   "",
		"/en/etf-profile.html?isin=NOTANISIN":                        "",
	}
	for href, want := range cases {
		if got := isinFromHref(href); got != want {
			t.Errorf("isinFromHref(%q) = %q, want %q", href, got, want)
		}
	}
}

func TestConstituentNameKey(t *testing.T) {
	cases := map[string]string{
		"NVIDIA Corp.":          "nvidia",
		"  NVIDIA Corporation ": "nvidia",
		"Alphabet Inc. A":       "alphabet inc a",
		"Alphabet, Inc. C":      "alphabet inc c",
		"Novo Nordisk A/S":      "novo nordisk a s",
		"Inc.":                  "inc",
		"AT&T Inc.":             "at&t",
	}
	for in, want := range cases {
		if got := ConstituentNameKey(in); got != want {
			t.Errorf("ConstituentNameKey(%q) = %q, want %q", in, got, want)
		}
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

func TestJustETFClient_SingleFetchCacheAndBotBlockGuards(t *testing.T) {
	const fullProfileHTML = `<html><body>
<div data-testid="tl_etf-basics_value_ter">0.20% p.a.</div>
<div data-testid="etf-holdings_sectors_row">
  <span data-testid="tl_etf-holdings_sectors_value_name">Technology</span>
  <span data-testid="tl_etf-holdings_sectors_value_percentage">28,5%</span>
</div>
<div data-testid="etf-holdings_countries_row">
  <span data-testid="tl_etf-holdings_countries_value_name">United States</span>
  <span data-testid="tl_etf-holdings_countries_value_percentage">65.0%</span>
</div>
<div data-testid="etf-holdings_top-holdings_row">
  <span data-testid="tl_etf-holdings_top-holdings_link_name"><a href="/en/etf-profile.html?isin=US0378331005">Apple Inc.</a></span>
  <span data-testid="tl_etf-holdings_top-holdings_value_percentage">4.50%</span>
</div>
</body></html>`

	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		switch r.URL.Path {
		case "/ok":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(fullProfileHTML))
		case "/challenge":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("<html><body>Please enable JS</body></html>"))
		case "/ratelimit":
			w.WriteHeader(http.StatusTooManyRequests)
		}
	}))
	defer srv.Close()

	client := NewJustETFClient()
	client.httpClient = srv.Client()
	client.SetMinInterval(0)

	// 1. FetchMetadata + FetchHoldings for the same URL must issue only 1 HTTP GET.
	meta, err := fetchMetadataFromURL(client, context.Background(), srv.URL+"/ok", "IE00B4L5Y983")
	if err != nil {
		t.Fatalf("fetchMetadataFromURL(/ok): %v", err)
	}
	if meta.TER != 0.20 || meta.SectorWeights["Technology"] != 28.5 || meta.CountryWeights["United States"] != 65.0 {
		t.Fatalf("unexpected parsed metadata: %+v", meta)
	}
	holdings, err := fetchHoldingsFromURL(client, context.Background(), srv.URL+"/ok", "IE00B4L5Y983")
	if err != nil || len(holdings) != 1 {
		t.Fatalf("fetchHoldingsFromURL(/ok): holdings=%v err=%v", holdings, err)
	}
	if got := hits.Load(); got != 1 {
		t.Fatalf("expected 1 HTTP GET across FetchMetadata+FetchHoldings, got %d", got)
	}

	// 2. HTTP 200 bot-challenge page with zero TER/sectors/countries must error and negative-cache.
	if _, err := fetchMetadataFromURL(client, context.Background(), srv.URL+"/challenge", "IE00CHALL001"); err == nil {
		t.Fatalf("expected error on empty HTTP 200 challenge page")
	}
	hitsAfterChallenge := hits.Load()
	if _, err := fetchMetadataFromURL(client, context.Background(), srv.URL+"/challenge", "IE00CHALL001"); err == nil {
		t.Fatalf("expected cached error on second call to challenge URL")
	}
	if got := hits.Load(); got != hitsAfterChallenge {
		t.Fatalf("expected negative cache to suppress second HTTP request, hits %d -> %d", hitsAfterChallenge, got)
	}

	// 3. HTTP 429 must negative-cache and suppress immediate retry.
	if _, err := fetchMetadataFromURL(client, context.Background(), srv.URL+"/ratelimit", "IE00RATE0001"); err == nil {
		t.Fatalf("expected error on 429")
	}
	hitsAfter429 := hits.Load()
	if _, err := fetchHoldingsFromURL(client, context.Background(), srv.URL+"/ratelimit", "IE00RATE0001"); err == nil {
		t.Fatalf("expected cached error after 429")
	}
	if got := hits.Load(); got != hitsAfter429 {
		t.Fatalf("expected 429 negative cache to suppress retry, hits %d -> %d", hitsAfter429, got)
	}
}
