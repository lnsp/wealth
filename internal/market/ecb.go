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
		httpClient: &http.Client{Timeout: 10 * time.Second},
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
