package market

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/PuerkitoBio/goquery"
)

const (
	maxJustETFHTMLBytes   = 5 << 20
	defaultJustETFCacheTTL = 5 * time.Minute
	defaultJustETFNegTTL   = 6 * time.Hour
)

type justETFCacheEntry struct {
	html      []byte
	expiresAt time.Time
}

type justETFNegEntry struct {
	errMsg    string
	expiresAt time.Time
}

// JustETFClient scrapes ETF metadata from justETF with rate limiting,
// short-lived document caching (avoiding duplicate fetches per ISIN),
// and negative caching on bot-blocks/rate-limits/missing profiles.
type JustETFClient struct {
	httpClient  *http.Client
	mu          sync.Mutex
	minInterval time.Duration
	cacheTTL    time.Duration
	negTTL      time.Duration
	lastReq     time.Time
	docCache    map[string]justETFCacheEntry
	negCache    map[string]justETFNegEntry
}

func NewJustETFClient() *JustETFClient {
	return &JustETFClient{
		httpClient:  &http.Client{Timeout: 15 * time.Second},
		minInterval: 2 * time.Second,
		cacheTTL:    defaultJustETFCacheTTL,
		negTTL:      defaultJustETFNegTTL,
		docCache:    make(map[string]justETFCacheEntry),
		negCache:    make(map[string]justETFNegEntry),
	}
}

// SetMinInterval configures the minimum delay between live HTTP requests to justETF.
func (c *JustETFClient) SetMinInterval(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.minInterval = d
}

func (c *JustETFClient) recordNegative(url, msg string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.negCache == nil {
		c.negCache = make(map[string]justETFNegEntry)
	}
	if c.docCache != nil {
		delete(c.docCache, url)
	}
	ttl := c.negTTL
	if ttl <= 0 {
		ttl = defaultJustETFNegTTL
	}
	c.negCache[url] = justETFNegEntry{
		errMsg:    msg,
		expiresAt: time.Now().Add(ttl),
	}
}

func (c *JustETFClient) fetchDocument(ctx context.Context, url, isin string) (*goquery.Document, error) {
	now := time.Now()
	c.mu.Lock()
	if c.negCache != nil {
		if neg, ok := c.negCache[url]; ok {
			if now.Before(neg.expiresAt) {
				c.mu.Unlock()
				return nil, fmt.Errorf("%s (cached)", neg.errMsg)
			}
			delete(c.negCache, url)
		}
	}
	if c.docCache != nil {
		if entry, ok := c.docCache[url]; ok {
			if now.Before(entry.expiresAt) {
				htmlBytes := entry.html
				c.mu.Unlock()
				return goquery.NewDocumentFromReader(bytes.NewReader(htmlBytes))
			}
			delete(c.docCache, url)
		}
	}

	// Enforce minimum request pacing across callers (serialized by reservation)
	var wait time.Duration
	if c.minInterval > 0 && !c.lastReq.IsZero() {
		nextAllowed := c.lastReq.Add(c.minInterval)
		if now.Before(nextAllowed) {
			wait = nextAllowed.Sub(now)
		}
	}
	c.lastReq = now.Add(wait)
	c.mu.Unlock()

	if wait > 0 {
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; finance-tracker/1.0)")

	client := c.httpClient
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		msg := fmt.Sprintf("justETF returned %d for %s", resp.StatusCode, isin)
		if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusTooManyRequests {
			c.recordNegative(url, msg)
		}
		return nil, fmt.Errorf("%s", msg)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxJustETFHTMLBytes))
	if err != nil {
		return nil, fmt.Errorf("read justETF HTML: %w", err)
	}

	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("parse justETF HTML: %w", err)
	}

	c.mu.Lock()
	if c.docCache == nil {
		c.docCache = make(map[string]justETFCacheEntry)
	}
	ttl := c.cacheTTL
	if ttl <= 0 {
		ttl = defaultJustETFCacheTTL
	}
	c.docCache[url] = justETFCacheEntry{
		html:      body,
		expiresAt: time.Now().Add(ttl),
	}
	c.mu.Unlock()

	return doc, nil
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
	return fetchMetadataFromURL(c, ctx, url, isin)
}

func fetchMetadataFromURL(c *JustETFClient, ctx context.Context, url, isin string) (*ETFMetadata, error) {
	doc, err := c.fetchDocument(ctx, url, isin)
	if err != nil {
		return nil, err
	}

	meta := &ETFMetadata{
		ISIN:           isin,
		SectorWeights:  make(map[string]float64),
		CountryWeights: make(map[string]float64),
	}

	// Parse TER from the basics section (supports both "." and "," decimals)
	doc.Find("[data-testid='tl_etf-basics_value_ter']").Each(func(i int, s *goquery.Selection) {
		text := strings.TrimSpace(s.Text())
		text = strings.TrimSuffix(text, "% p.a.")
		text = strings.TrimSuffix(text, "%")
		text = strings.ReplaceAll(text, ",", ".")
		text = strings.TrimSpace(text)
		if v, err := strconv.ParseFloat(text, 64); err == nil && !math.IsNaN(v) && !math.IsInf(v, 0) && v >= 0 {
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
			if w, err := strconv.ParseFloat(pctStr, 64); err == nil && !math.IsNaN(w) && !math.IsInf(w, 0) && w >= 0 {
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
			if w, err := strconv.ParseFloat(pctStr, 64); err == nil && !math.IsNaN(w) && !math.IsInf(w, 0) && w >= 0 {
				meta.CountryWeights[name] = w
			}
		}
	})

	// Guard against HTTP 200 bot-challenge pages or DOM selector breakage so callers
	// never overwrite valid stored ETF metadata with empty maps and zero TER.
	if len(meta.SectorWeights) == 0 && len(meta.CountryWeights) == 0 && meta.TER == 0 {
		msg := fmt.Sprintf("justETF returned empty metadata for %s", isin)
		c.recordNegative(url, msg)
		return nil, fmt.Errorf("%s", msg)
	}

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
	doc, err := c.fetchDocument(ctx, url, isin)
	if err != nil {
		return nil, err
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
		if err != nil || math.IsNaN(weight) || math.IsInf(weight, 0) || weight <= 0 {
			return
		}

		// Extract ISIN from link if available
		holdingISIN := ""
		s.Find("a").Each(func(_ int, a *goquery.Selection) {
			if href, exists := a.Attr("href"); exists && holdingISIN == "" {
				holdingISIN = isinFromHref(href)
			}
		})

		// Without an ISIN, derive a synthetic ID from the normalised name only,
		// so the same company gets the same ID in every ETF and overlap and
		// look-through can match it across funds.
		if holdingISIN == "" {
			holdingISIN = SyntheticConstituentID(name)
		}

		holdings = append(holdings, ETFHolding{
			Name:   name,
			ISIN:   holdingISIN,
			Weight: weight,
		})
	})

	return holdings, nil
}

// SyntheticConstituentPrefix marks constituent IDs derived from the name
// because justETF did not link an ISIN. Rows stored before 2026-11 used an
// ETF-specific hash or an "XX_" prefix; consumers must key all of them by
// ConstituentNameKey rather than by the ID itself.
const SyntheticConstituentPrefix = "SYNTH:"

// SyntheticConstituentID returns the fund-independent synthetic ID for a
// constituent without an ISIN: a short hash of ConstituentNameKey(name).
func SyntheticConstituentID(name string) string {
	sum := sha256.Sum256([]byte(ConstituentNameKey(name)))
	return fmt.Sprintf("%s%x", SyntheticConstituentPrefix, sum[:6])
}

// IsSyntheticConstituentID reports whether id is not a real ISIN: empty, the
// current "SYNTH:" form or the legacy "XX_" form.
func IsSyntheticConstituentID(id string) bool {
	return id == "" || strings.HasPrefix(id, SyntheticConstituentPrefix) || strings.HasPrefix(id, "XX_")
}

// legalFormTokens are trailing company-form words dropped when matching
// constituent names, so "NVIDIA Corp." and "NVIDIA Corporation" match.
var legalFormTokens = map[string]bool{
	"inc": true, "incorporated": true, "corp": true, "corporation": true,
	"co": true, "company": true, "plc": true, "ltd": true, "limited": true,
	"ag": true, "se": true, "sa": true, "nv": true, "asa": true, "ab": true,
	"spa": true, "oyj": true, "as": true,
}

// ConstituentNameKey normalises a constituent name for matching across
// funds: lower case, punctuation to spaces, whitespace collapsed and
// trailing legal-form words removed. Share-class letters are kept, so
// "Alphabet A" and "Alphabet C" stay distinct.
func ConstituentNameKey(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '&' {
			b.WriteRune(r)
		} else {
			b.WriteByte(' ')
		}
	}
	fields := strings.Fields(b.String())
	for len(fields) > 1 && legalFormTokens[fields[len(fields)-1]] {
		fields = fields[:len(fields)-1]
	}
	return strings.Join(fields, " ")
}

// isinFromHref extracts a constituent ISIN from a justETF link, either an
// "isin=" query parameter or a "/stock-profiles/<ISIN>" path.
func isinFromHref(href string) string {
	candidate := ""
	if idx := strings.Index(href, "isin="); idx >= 0 {
		candidate = href[idx+5:]
	} else if idx := strings.Index(href, "stock-profiles/"); idx >= 0 {
		candidate = href[idx+len("stock-profiles/"):]
	}
	if end := strings.IndexAny(candidate, "&/?#"); end >= 0 {
		candidate = candidate[:end]
	}
	candidate = strings.ToUpper(strings.TrimSpace(candidate))
	if !looksLikeISIN(candidate) {
		return ""
	}
	return candidate
}

// looksLikeISIN checks the ISIN shape: 2 letters, 9 alphanumerics, 1 digit.
func looksLikeISIN(s string) bool {
	if len(s) != 12 {
		return false
	}
	for i, r := range s {
		switch {
		case i < 2:
			if r < 'A' || r > 'Z' {
				return false
			}
		case i == 11:
			if r < '0' || r > '9' {
				return false
			}
		default:
			if !(r >= 'A' && r <= 'Z') && !(r >= '0' && r <= '9') {
				return false
			}
		}
	}
	return true
}
