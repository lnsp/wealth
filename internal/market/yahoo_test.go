package market

import (
	"context"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestYahooClient_FetchPrice(t *testing.T) {
	tests := []struct {
		name       string
		response   string
		statusCode int
		wantErr    bool
		wantPrice  float64
		wantCurr   string
	}{
		{
			name: "valid response with indicators",
			response: `{
				"chart": {
					"result": [{
						"meta": {"currency": "EUR", "regularMarketPrice": 119.50},
						"timestamp": [1711234800],
						"indicators": {
							"quote": [{"close": [118.0, 119.50]}]
						}
					}],
					"error": null
				}
			}`,
			statusCode: http.StatusOK,
			wantPrice:  119.50,
			wantCurr:   "EUR",
		},
		{
			name: "falls back to meta price when indicators empty",
			response: `{
				"chart": {
					"result": [{
						"meta": {"currency": "USD", "regularMarketPrice": 250.75},
						"timestamp": [1711234800],
						"indicators": {"quote": [{"close": []}]}
					}],
					"error": null
				}
			}`,
			statusCode: http.StatusOK,
			wantPrice:  250.75,
			wantCurr:   "USD",
		},
		{
			name: "skips zero close prices",
			response: `{
				"chart": {
					"result": [{
						"meta": {"currency": "EUR", "regularMarketPrice": 100.0},
						"timestamp": [1711234800, 1711321200],
						"indicators": {"quote": [{"close": [95.0, 0]}]}
					}],
					"error": null
				}
			}`,
			statusCode: http.StatusOK,
			wantPrice:  95.0,
			wantCurr:   "EUR",
		},
		{
			name:       "HTTP error",
			response:   "Not Found",
			statusCode: http.StatusNotFound,
			wantErr:    true,
		},
		{
			name: "API error in response",
			response: `{
				"chart": {
					"result": null,
					"error": {"code": "Not Found", "description": "No data found"}
				}
			}`,
			statusCode: http.StatusOK,
			wantErr:    true,
		},
		{
			name: "empty result",
			response: `{
				"chart": {"result": [], "error": null}
			}`,
			statusCode: http.StatusOK,
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.statusCode)
				w.Write([]byte(tt.response))
			}))
			defer server.Close()

			client := &YahooClient{httpClient: server.Client()}

			// Override URL by calling fetchPrice with the test server URL path
			price, currency, _, err := client.fetchPriceFromURL(context.Background(), server.URL)

			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if math.Abs(price-tt.wantPrice) > 0.01 {
				t.Errorf("price = %f, want %f", price, tt.wantPrice)
			}
			if currency != tt.wantCurr {
				t.Errorf("currency = %q, want %q", currency, tt.wantCurr)
			}
		})
	}
}

func TestYahooClient_FetchPriceFromURL_Concurrent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{
			"chart": {
				"result": [{
					"meta": {"currency": "EUR", "regularMarketPrice": 100.0},
					"timestamp": [1711234800],
					"indicators": {"quote": [{"close": [100.0]}]}
				}],
				"error": null
			}
		}`))
	}))
	defer server.Close()

	client := &YahooClient{httpClient: server.Client()}

	// Test multiple concurrent calls directly
	urls := []string{server.URL + "/a", server.URL + "/b", server.URL + "/c"}
	type result struct {
		price float64
		err   error
	}
	results := make(chan result, len(urls))

	for _, u := range urls {
		go func(url string) {
			price, _, _, err := client.fetchPriceFromURL(context.Background(), url)
			results <- result{price, err}
		}(u)
	}

	for range urls {
		r := <-results
		if r.err != nil {
			t.Errorf("unexpected error: %v", r.err)
		}
		if r.price != 100.0 {
			t.Errorf("price = %f, want 100.0", r.price)
		}
	}
}
