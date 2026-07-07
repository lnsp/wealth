package market

import (
	"context"
	"encoding/xml"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

// ECBClient fetches FX rates from the European Central Bank.
type ECBClient struct {
	httpClient *http.Client
}

func NewECBClient() *ECBClient {
	return &ECBClient{
		httpClient: &http.Client{Timeout: 20 * time.Second},
	}
}

// FXRate represents a single currency exchange rate.
type FXRate struct {
	Currency string
	Rate     float64
}

// FetchDailyRates fetches the latest daily FX rates from ECB.
func (c *ECBClient) FetchDailyRates(ctx context.Context) ([]FXRate, time.Time, error) {
	return c.fetchRates(ctx, "https://www.ecb.europa.eu/stats/eurofxref/eurofxref-daily.xml")
}

// HistoricalFXRate represents an FX rate for a specific date.
type HistoricalFXRate struct {
	Date     time.Time
	Currency string
	Rate     float64
}

// LookupRateForDate returns the ECB reference rate for `currency` valid on
// or before `target`. ECB does NOT publish on weekends or TARGET holidays,
// so a Saturday/Sunday lookup must walk back to the previous business
// day's rate (typically Friday). Same for holiday Mondays.
//
// Returns 0 if no rate ≤ target exists for the currency (caller decides
// whether to treat that as "skip conversion" or surface an error).
//
// ECB convention: rate is units-per-EUR (e.g. USD rate of 1.0834 means
// 1 EUR = 1.0834 USD; amountEUR = amountUSD / rate).
func LookupRateForDate(rates []HistoricalFXRate, currency string, target time.Time) float64 {
	target = target.UTC()
	var bestDate time.Time
	var bestRate float64
	found := false
	for _, r := range rates {
		if r.Currency != currency {
			continue
		}
		if r.Date.After(target) {
			continue
		}
		if !found || r.Date.After(bestDate) {
			bestDate = r.Date
			bestRate = r.Rate
			found = true
		}
	}
	if !found {
		return 0
	}
	return bestRate
}

// FetchHistoricalRates fetches full FX rate history from ECB (since 1999).
func (c *ECBClient) FetchHistoricalRates(ctx context.Context) ([]HistoricalFXRate, error) {
	// Use a longer timeout for the ~6MB historical file
	client := &http.Client{Timeout: 60 * time.Second}
	req, err := http.NewRequestWithContext(ctx, "GET", "https://www.ecb.europa.eu/stats/eurofxref/eurofxref-hist.xml", nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ECB historical API returned %d", resp.StatusCode)
	}

	var envelope struct {
		XMLName xml.Name `xml:"Envelope"`
		Cube    struct {
			Cube []struct {
				Time string `xml:"time,attr"`
				Cube []struct {
					Currency string `xml:"currency,attr"`
					Rate     string `xml:"rate,attr"`
				} `xml:"Cube"`
			} `xml:"Cube"`
		} `xml:"Cube"`
	}

	if err := xml.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return nil, fmt.Errorf("decode ECB historical XML: %w", err)
	}

	var rates []HistoricalFXRate
	for _, dayCube := range envelope.Cube.Cube {
		date, err := time.Parse("2006-01-02", dayCube.Time)
		if err != nil {
			continue
		}
		for _, cube := range dayCube.Cube {
			rate, err := strconv.ParseFloat(cube.Rate, 64)
			if err != nil {
				continue
			}
			rates = append(rates, HistoricalFXRate{Date: date, Currency: cube.Currency, Rate: rate})
		}
	}
	return rates, nil
}

func (c *ECBClient) fetchRates(ctx context.Context, url string) ([]FXRate, time.Time, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, time.Time{}, err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, time.Time{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, time.Time{}, fmt.Errorf("ECB API returned %d", resp.StatusCode)
	}

	var envelope struct {
		XMLName xml.Name `xml:"Envelope"`
		Cube    struct {
			Cube []struct {
				Time string `xml:"time,attr"`
				Cube []struct {
					Currency string `xml:"currency,attr"`
					Rate     string `xml:"rate,attr"`
				} `xml:"Cube"`
			} `xml:"Cube"`
		} `xml:"Cube"`
	}

	if err := xml.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return nil, time.Time{}, fmt.Errorf("decode ECB XML: %w", err)
	}

	if len(envelope.Cube.Cube) == 0 {
		return nil, time.Time{}, fmt.Errorf("no FX data in ECB response")
	}

	latest := envelope.Cube.Cube[0]
	date, err := time.Parse("2006-01-02", latest.Time)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("parse ECB date %q: %w", latest.Time, err)
	}

	var rates []FXRate
	for _, cube := range latest.Cube {
		rate, err := strconv.ParseFloat(cube.Rate, 64)
		if err != nil {
			continue
		}
		rates = append(rates, FXRate{Currency: cube.Currency, Rate: rate})
	}

	return rates, date, nil
}
