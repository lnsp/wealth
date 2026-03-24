package market

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
)

// JustETFClient scrapes ETF metadata from justETF.
type JustETFClient struct {
	httpClient *http.Client
}

func NewJustETFClient() *JustETFClient {
	return &JustETFClient{
		httpClient: &http.Client{Timeout: 15 * time.Second},
	}
}

// ETFMetadata contains scraped ETF information.
type ETFMetadata struct {
	ISIN           string
	SectorWeights  map[string]float64
	CountryWeights map[string]float64
	TER            float64
}

// FetchMetadata scrapes ETF sector and country allocations from justETF.
func (c *JustETFClient) FetchMetadata(ctx context.Context, isin string) (*ETFMetadata, error) {
	url := fmt.Sprintf("https://www.justetf.com/en/etf-profile.html?isin=%s", isin)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; finance-tracker/1.0)")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("justETF returned %d for %s", resp.StatusCode, isin)
	}

	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("parse justETF HTML: %w", err)
	}

	meta := &ETFMetadata{
		ISIN:           isin,
		SectorWeights:  make(map[string]float64),
		CountryWeights: make(map[string]float64),
	}

	// Parse TER
	doc.Find("div.val span").Each(func(i int, s *goquery.Selection) {
		text := strings.TrimSpace(s.Text())
		if strings.HasSuffix(text, "% p.a.") {
			text = strings.TrimSuffix(text, "% p.a.")
			text = strings.TrimSpace(text)
			if v, err := strconv.ParseFloat(text, 64); err == nil {
				meta.TER = v
			}
		}
	})

	// Parse sector/country allocation tables
	// justETF uses specific section IDs for these
	parseAllocationTable(doc, "#sector-allocation", meta.SectorWeights)
	parseAllocationTable(doc, "#country-allocation", meta.CountryWeights)

	return meta, nil
}

func parseAllocationTable(doc *goquery.Document, selector string, target map[string]float64) {
	doc.Find(selector + " table tbody tr").Each(func(i int, s *goquery.Selection) {
		name := strings.TrimSpace(s.Find("td:first-child").Text())
		weightStr := strings.TrimSpace(s.Find("td:last-child").Text())
		weightStr = strings.TrimSuffix(weightStr, "%")
		weightStr = strings.TrimSpace(weightStr)

		if name != "" && weightStr != "" {
			if w, err := strconv.ParseFloat(weightStr, 64); err == nil {
				target[name] = w
			}
		}
	})
}
