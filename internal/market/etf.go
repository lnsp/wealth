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

	// Parse TER from the basics section
	doc.Find("[data-testid='tl_etf-basics_value_ter']").Each(func(i int, s *goquery.Selection) {
		text := strings.TrimSpace(s.Text())
		text = strings.TrimSuffix(text, "% p.a.")
		text = strings.TrimSpace(text)
		if v, err := strconv.ParseFloat(text, 64); err == nil {
			meta.TER = v
		}
	})

	// Parse sector allocations
	doc.Find("[data-testid='etf-holdings_sectors_row']").Each(func(i int, s *goquery.Selection) {
		name := strings.TrimSpace(s.Find("[data-testid='tl_etf-holdings_sectors_value_name']").Text())
		pctStr := strings.TrimSpace(s.Find("[data-testid='tl_etf-holdings_sectors_value_percentage']").Text())
		pctStr = strings.TrimSuffix(pctStr, "%")
		pctStr = strings.ReplaceAll(pctStr, ",", ".")
		pctStr = strings.TrimSpace(pctStr)
		if name != "" && pctStr != "" {
			if w, err := strconv.ParseFloat(pctStr, 64); err == nil {
				meta.SectorWeights[name] = w
			}
		}
	})

	// Parse country allocations
	doc.Find("[data-testid='etf-holdings_countries_row']").Each(func(i int, s *goquery.Selection) {
		name := strings.TrimSpace(s.Find("[data-testid='tl_etf-holdings_countries_value_name']").Text())
		pctStr := strings.TrimSpace(s.Find("[data-testid='tl_etf-holdings_countries_value_percentage']").Text())
		pctStr = strings.TrimSuffix(pctStr, "%")
		pctStr = strings.ReplaceAll(pctStr, ",", ".")
		pctStr = strings.TrimSpace(pctStr)
		if name != "" && pctStr != "" {
			if w, err := strconv.ParseFloat(pctStr, 64); err == nil {
				meta.CountryWeights[name] = w
			}
		}
	})

	return meta, nil
}

// ETFHolding represents an individual constituent holding of an ETF.
type ETFHolding struct {
	Name    string
	ISIN    string
	Weight  float64
	Sector  string
	Country string
}

// FetchHoldings scrapes individual constituent holdings from justETF.
func (c *JustETFClient) FetchHoldings(ctx context.Context, isin string) ([]ETFHolding, error) {
	url := fmt.Sprintf("https://www.justetf.com/en/etf-profile.html?isin=%s", isin)
	return fetchHoldingsFromURL(c, ctx, url, isin)
}

func fetchHoldingsFromURL(c *JustETFClient, ctx context.Context, url, isin string) ([]ETFHolding, error) {
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

	var holdings []ETFHolding

	// Parse top holdings table rows (justETF uses "top-holdings" selectors)
	doc.Find("[data-testid='etf-holdings_top-holdings_row']").Each(func(i int, s *goquery.Selection) {
		name := strings.TrimSpace(s.Find("[data-testid='tl_etf-holdings_top-holdings_link_name']").Text())
		if name == "" {
			name = strings.TrimSpace(s.Find("[data-testid='tl_etf-holdings_top-holdings_value_name']").Text())
		}
		pctStr := strings.TrimSpace(s.Find("[data-testid='tl_etf-holdings_top-holdings_value_percentage']").Text())
		pctStr = strings.TrimSuffix(pctStr, "%")
		pctStr = strings.ReplaceAll(pctStr, ",", ".")
		pctStr = strings.TrimSpace(pctStr)

		if name == "" || pctStr == "" {
			return
		}

		weight, err := strconv.ParseFloat(pctStr, 64)
		if err != nil {
			return
		}

		// Extract ISIN from link if available
		holdingISIN := ""
		s.Find("a").Each(func(_ int, a *goquery.Selection) {
			if href, exists := a.Attr("href"); exists {
				if idx := strings.Index(href, "isin="); idx >= 0 {
					holdingISIN = href[idx+5:]
					if ampIdx := strings.IndexByte(holdingISIN, '&'); ampIdx >= 0 {
						holdingISIN = holdingISIN[:ampIdx]
					}
				}
			}
		})

		// Generate a synthetic ISIN if not found (use name hash)
		if holdingISIN == "" {
			holdingISIN = fmt.Sprintf("XX_%s_%d", isin[:6], i)
		}

		holdings = append(holdings, ETFHolding{
			Name:   name,
			ISIN:   holdingISIN,
			Weight: weight,
		})
	})

	return holdings, nil
}
