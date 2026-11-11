package market

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// EurostatClient fetches statistics from the Eurostat dissemination API
// (no registration needed).
type EurostatClient struct {
	httpClient *http.Client
	baseURL    string
}

func NewEurostatClient() *EurostatClient {
	return &EurostatClient{
		httpClient: &http.Client{Timeout: 30 * time.Second},
		baseURL:    "https://ec.europa.eu/eurostat/api/dissemination/statistics/1.0/data",
	}
}

// IndexPoint is one published index value; Period is the first day of the
// quarter it covers.
type IndexPoint struct {
	Period time.Time
	Value  float64
}

// FetchHousePriceIndex returns the quarterly house price index (prc_hpi_q,
// 2015 = 100) for a country and purchase type, e.g. "DE" and "DW_EXST"
// (existing dwellings). Eurostat republishes the national statistics office's
// index — for Germany, Destatis' Häuserpreisindex.
func (c *EurostatClient) FetchHousePriceIndex(ctx context.Context, geo, purchase string) ([]IndexPoint, error) {
	q := url.Values{"geo": {geo}, "purchase": {purchase}, "unit": {"I15_Q"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/prc_hpi_q?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("eurostat prc_hpi_q: HTTP %d", resp.StatusCode)
	}
	var body jsonStat
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("eurostat prc_hpi_q: decode: %w", err)
	}
	return body.timeSeries()
}

// jsonStat is the subset of a JSON-stat 2.0 dataset needed for a single
// series: every dimension except time is fixed by the query, so the flat
// value index equals the time index.
type jsonStat struct {
	Value     map[string]float64 `json:"value"`
	Dimension struct {
		Time struct {
			Category struct {
				Index map[string]int `json:"index"`
			} `json:"category"`
		} `json:"time"`
	} `json:"dimension"`
}

func (d jsonStat) timeSeries() ([]IndexPoint, error) {
	var out []IndexPoint
	for label, idx := range d.Dimension.Time.Category.Index {
		v, ok := d.Value[strconv.Itoa(idx)]
		if !ok {
			continue // not published (yet)
		}
		period, err := parseQuarter(label)
		if err != nil {
			return nil, err
		}
		out = append(out, IndexPoint{Period: period, Value: v})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Period.Before(out[j].Period) })
	return out, nil
}

// parseQuarter turns "2026-Q2" into 2026-04-01.
func parseQuarter(s string) (time.Time, error) {
	year, q, ok := strings.Cut(s, "-Q")
	y, err1 := strconv.Atoi(year)
	n, err2 := strconv.Atoi(q)
	if !ok || err1 != nil || err2 != nil || n < 1 || n > 4 {
		return time.Time{}, fmt.Errorf("unexpected quarter %q", s)
	}
	return time.Date(y, time.Month(3*(n-1)+1), 1, 0, 0, 0, 0, time.UTC), nil
}
