package market

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"sync"
	"time"
)

// YahooClient fetches price data from Yahoo Finance v8 API.
type YahooClient struct {
	httpClient  *http.Client
	concurrency int
}

func NewYahooClient() *YahooClient {
	return &YahooClient{
		httpClient:  &http.Client{Timeout: 10 * time.Second},
		concurrency: 10,
	}
}

// SetConcurrency configures the maximum number of concurrent Yahoo Finance requests.
func (c *YahooClient) SetConcurrency(n int) {
	if n > 0 {
		c.concurrency = n
	}
}

// normalizeYahooCurrencyAndPrice converts minor-unit currencies (e.g. LSE GBp/GBX pence)
// into major ISO 4217 currency units.
func normalizeYahooCurrencyAndPrice(price float64, currency string) (float64, string) {
	c := strings.TrimSpace(currency)
	switch c {
	case "GBp", "GBX":
		return price / 100.0, "GBP"
	case "":
		return price, "EUR"
	default:
		return price, strings.ToUpper(c)
	}
}

// PriceResult contains the latest price for a symbol.
type PriceResult struct {
	Symbol   string
	Price    float64
	Currency string
	Date     time.Time
	Err      error
}

// FetchPrices concurrently fetches prices for multiple symbols.
func (c *YahooClient) FetchPrices(ctx context.Context, symbols map[string]string) []PriceResult {
	conc := c.concurrency
	if conc <= 0 {
		conc = 10
	}
	var (
		mu      sync.Mutex
		results []PriceResult
		wg      sync.WaitGroup
		sem     = make(chan struct{}, conc)
	)

	for isin, symbol := range symbols {
		wg.Add(1)
		go func(isin, symbol string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			price, currency, date, err := c.fetchPrice(ctx, symbol)
			mu.Lock()
			results = append(results, PriceResult{
				Symbol:   isin,
				Price:    price,
				Currency: currency,
				Date:     date,
				Err:      err,
			})
			mu.Unlock()
		}(isin, symbol)
	}

	wg.Wait()
	return results
}

func (c *YahooClient) fetchPrice(ctx context.Context, symbol string) (float64, string, time.Time, error) {
	url := fmt.Sprintf("https://query1.finance.yahoo.com/v8/finance/chart/%s?range=5d&interval=1d", symbol)
	return c.fetchPriceFromURL(ctx, url)
}

// HistoricalPrice represents a single historical price point.
type HistoricalPrice struct {
	Date     time.Time
	Close    float64
	Currency string
}

// FetchHistoricalPrices fetches weekly historical prices for a symbol (5 years).
func (c *YahooClient) FetchHistoricalPrices(ctx context.Context, symbol string) ([]HistoricalPrice, error) {
	url := fmt.Sprintf("https://query1.finance.yahoo.com/v8/finance/chart/%s?range=5y&interval=1wk", symbol)
	return c.fetchHistoricalPricesFromURL(ctx, url, symbol)
}

func (c *YahooClient) fetchHistoricalPricesFromURL(ctx context.Context, url, symbol string) ([]HistoricalPrice, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "finance-tracker/1.0")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("yahoo API returned %d", resp.StatusCode)
	}

	var result struct {
		Chart struct {
			Result []struct {
				Meta struct {
					Currency string `json:"currency"`
				} `json:"meta"`
				Timestamp  []int64 `json:"timestamp"`
				Indicators struct {
					Quote []struct {
						Close []float64 `json:"close"`
					} `json:"quote"`
				} `json:"indicators"`
			} `json:"result"`
			Error *struct {
				Code        string `json:"code"`
				Description string `json:"description"`
			} `json:"error"`
		} `json:"chart"`
	}

	if err := json.NewDecoder(io.LimitReader(resp.Body, 5<<20)).Decode(&result); err != nil {
		return nil, err
	}
	if result.Chart.Error != nil {
		return nil, fmt.Errorf("yahoo API error: %s", result.Chart.Error.Description)
	}
	if len(result.Chart.Result) == 0 {
		return nil, fmt.Errorf("no data for %s", symbol)
	}

	r := result.Chart.Result[0]
	if len(r.Indicators.Quote) == 0 {
		return nil, fmt.Errorf("no quote indicators for %s", symbol)
	}
	closes := r.Indicators.Quote[0].Close

	var prices []HistoricalPrice
	for i, ts := range r.Timestamp {
		if i < len(closes) {
			rawClose := closes[i]
			if rawClose > 0 && !math.IsNaN(rawClose) && !math.IsInf(rawClose, 0) {
				normClose, normCur := normalizeYahooCurrencyAndPrice(rawClose, r.Meta.Currency)
				prices = append(prices, HistoricalPrice{
					Date:     time.Unix(ts, 0).UTC(),
					Close:    normClose,
					Currency: normCur,
				})
			}
		}
	}
	return prices, nil
}

func (c *YahooClient) fetchPriceFromURL(ctx context.Context, url string) (float64, string, time.Time, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return 0, "", time.Time{}, err
	}
	req.Header.Set("User-Agent", "finance-tracker/1.0")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, "", time.Time{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, "", time.Time{}, fmt.Errorf("yahoo API returned %d for %s", resp.StatusCode, url)
	}

	var result struct {
		Chart struct {
			Result []struct {
				Meta struct {
					Currency           string  `json:"currency"`
					RegularMarketPrice float64 `json:"regularMarketPrice"`
				} `json:"meta"`
				Timestamp  []int64 `json:"timestamp"`
				Indicators struct {
					Quote []struct {
						Close []float64 `json:"close"`
					} `json:"quote"`
				} `json:"indicators"`
			} `json:"result"`
			Error *struct {
				Code        string `json:"code"`
				Description string `json:"description"`
			} `json:"error"`
		} `json:"chart"`
	}

	if err := json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&result); err != nil {
		return 0, "", time.Time{}, fmt.Errorf("decode yahoo response: %w", err)
	}

	if result.Chart.Error != nil {
		return 0, "", time.Time{}, fmt.Errorf("yahoo API error: %s", result.Chart.Error.Description)
	}

	if len(result.Chart.Result) == 0 {
		return 0, "", time.Time{}, fmt.Errorf("no data for %s", url)
	}

	r := result.Chart.Result[0]
	price := r.Meta.RegularMarketPrice
	currency := r.Meta.Currency

	// Get the latest timestamp
	date := time.Now().UTC()
	if len(r.Timestamp) > 0 {
		date = time.Unix(r.Timestamp[len(r.Timestamp)-1], 0).UTC()
	}

	// Prefer closing price from indicators if available
	if len(r.Indicators.Quote) > 0 && len(r.Indicators.Quote[0].Close) > 0 {
		closes := r.Indicators.Quote[0].Close
		for i := len(closes) - 1; i >= 0; i-- {
			if closes[i] > 0 && !math.IsNaN(closes[i]) && !math.IsInf(closes[i], 0) {
				price = closes[i]
				if i < len(r.Timestamp) {
					date = time.Unix(r.Timestamp[i], 0).UTC()
				}
				break
			}
		}
	}

	price, currency = normalizeYahooCurrencyAndPrice(price, currency)
	if price <= 0 || math.IsNaN(price) || math.IsInf(price, 0) {
		return 0, "", time.Time{}, fmt.Errorf("invalid non-positive price for %s", url)
	}

	return price, currency, date, nil
}
