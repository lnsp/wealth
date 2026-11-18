package parser

import (
	"strings"
	"unicode"
)

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
	// Broker abbreviations of bond funds: "XTR2-EUROZ.GOV.BD 1C",
	// "AM.PR.EURO GOVIES". Whole words only.
	tokens := nameTokens(nameLower)
	for _, tok := range tokens {
		if tok == "bd" || tok == "bds" || tok == "govies" {
			return "bond"
		}
	}

	// ETF indicators (brand names and keywords)
	etfKeywords := []string{"ucits", "ishares", "vanguard", "xtrackers", "amundi", "lyxor", "spdr", "invesco"}
	for _, kw := range etfKeywords {
		if strings.Contains(nameLower, kw) {
			return "etf"
		}
	}
	if hasETFToken(tokens) {
		return "etf"
	}

	// Fund indicators (non-ETF)
	fundKeywords := []string{"fonds", "fund", "deka-", "deka "}
	for _, kw := range fundKeywords {
		if strings.Contains(nameLower, kw) {
			return "fund"
		}
	}

	// Commodity indicators. "gold" and "silver" must be whole words so
	// "Goldman Sachs" stays a stock (commodities are taxed under §23 EStG).
	commodityKeywords := []string{"commodity", "rohstoff", "euwax"}
	for _, kw := range commodityKeywords {
		if strings.Contains(nameLower, kw) {
			return "commodity"
		}
	}
	for _, tok := range tokens {
		if tok == "gold" || tok == "silver" {
			return "commodity"
		}
	}

	// Default: assume individual stock
	return "stock"
}

// nameTokens splits a lower-cased security name into alphanumeric words.
func nameTokens(nameLower string) []string {
	return strings.FieldsFunc(nameLower, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
}

// hasETFToken matches whole-word ETF markers and the issuer abbreviations
// German brokers print in fund names: "ISHSIII-MSCI EM USD(ACC)",
// "XTR.MSCI WORLD 1C", "VANG.FTSE ALL-WLD U.ETF", "AIS-AM.MSCI WORLD",
// "LYX.MSCI WORLD". Whole words, so "Netflix" no longer matches "etf" and
// "Evangelische" not "vang".
func hasETFToken(tokens []string) bool {
	for i, tok := range tokens {
		switch tok {
		case "etf", "etfs", "xtr", "xtr2", "vang", "lyx":
			return true
		case "ais":
			// Amundi Index Solutions: "AIS-AMUNDI ...", "AIS-AM.MSCI ...".
			if i+1 < len(tokens) && strings.HasPrefix(tokens[i+1], "am") {
				return true
			}
		}
		// iShares plc umbrella abbreviations: ISHS, ISHSII ... ISHSVII.
		if rest, ok := strings.CutPrefix(tok, "ishs"); ok && strings.Trim(rest, "ivx") == "" {
			return true
		}
	}
	return false
}
