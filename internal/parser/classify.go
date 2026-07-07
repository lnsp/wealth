package parser

import "strings"

// ClassifyAsset determines the asset class of a security based on its ISIN and name.
// Returns one of: "etf", "stock", "bond", "fund", "commodity", "derivative", "crypto".
func ClassifyAsset(isin, name string) string {
	// Synthetic crypto ISIN convention used by the Delta importer
	// (e.g. CRYPTO:BTC) — short-circuit before any name-based heuristic
	// since crypto names ("Bitcoin", "Ethereum") wouldn't match below.
	if strings.HasPrefix(isin, "CRYPTO:") {
		return "crypto"
	}

	nameLower := strings.ToLower(name)

	// Check derivative indicators first — most specific, avoids false positives
	// from brand-name matches (e.g. "Amundi Leveraged" matching "amundi" → etf)
	derivativeKeywords := []string{
		"call ", "put ", "optionsschein", "zertifikat", "faktor",
		"knock-out", "turbo", "leveraged", "leverage ",
		"mini future", "warrant", " short ",
	}
	for _, kw := range derivativeKeywords {
		if strings.Contains(nameLower, kw) {
			return "derivative"
		}
	}
	// Also check if name starts with "short" (e.g. "Short -7x...")
	if strings.HasPrefix(nameLower, "short ") {
		return "derivative"
	}

	// Bond indicators — check before ETF brands since bond ETFs exist
	// (e.g. "iShares Core Euro Govt Bond")
	bondKeywords := []string{"bond", "anleihe", "treasury", "renten"}
	for _, kw := range bondKeywords {
		if strings.Contains(nameLower, kw) {
			return "bond"
		}
	}

	// ETF indicators (brand names and keywords)
	etfKeywords := []string{"etf", "ucits", "ishares", "vanguard", "xtrackers", "amundi", "lyxor", "spdr", "invesco"}
	for _, kw := range etfKeywords {
		if strings.Contains(nameLower, kw) {
			return "etf"
		}
	}

	// Fund indicators (non-ETF)
	fundKeywords := []string{"fonds", "fund", "deka-", "deka "}
	for _, kw := range fundKeywords {
		if strings.Contains(nameLower, kw) {
			return "fund"
		}
	}

	// Commodity indicators
	commodityKeywords := []string{"gold", "silver", "commodity", "rohstoff", "euwax"}
	for _, kw := range commodityKeywords {
		if strings.Contains(nameLower, kw) {
			return "commodity"
		}
	}

	// Default: assume individual stock
	return "stock"
}
