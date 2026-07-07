package analytics

import "sort"

// CountryToCurrency maps country names (as stored in justETF country weights)
// to their primary currency ISO code.
var CountryToCurrency = map[string]string{
	"United States":  "USD",
	"USA":            "USD",
	"US":             "USD",
	"Japan":          "JPY",
	"United Kingdom": "GBP",
	"UK":             "GBP",
	"China":          "CNY",
	"Hong Kong":      "HKD",
	"Canada":         "CAD",
	"Switzerland":    "CHF",
	"Australia":      "AUD",
	"India":          "INR",
	"Taiwan":         "TWD",
	"South Korea":    "KRW",
	"Korea":          "KRW",
	"Brazil":         "BRL",
	"Sweden":         "SEK",
	"Denmark":        "DKK",
	"Norway":         "NOK",
	"Singapore":      "SGD",
	"South Africa":   "ZAR",
	"Mexico":         "MXN",
	"Israel":         "ILS",
	"Thailand":       "THB",
	"Indonesia":      "IDR",
	"Malaysia":       "MYR",
	"Poland":         "PLN",
	"Turkey":         "TRY",
	"Saudi Arabia":   "SAR",
	"Philippines":    "PHP",
	"New Zealand":    "NZD",
	"Chile":          "CLP",
	"Czech Republic": "CZK",
	"Hungary":        "HUF",
	"Colombia":       "COP",
	"Peru":           "PEN",
	"Romania":        "RON",
	"Argentina":      "ARS",
	// Eurozone countries → EUR
	"Germany":       "EUR",
	"France":        "EUR",
	"Netherlands":   "EUR",
	"Italy":         "EUR",
	"Spain":         "EUR",
	"Belgium":       "EUR",
	"Finland":       "EUR",
	"Ireland":       "EUR",
	"Austria":       "EUR",
	"Portugal":      "EUR",
	"Greece":        "EUR",
	"Luxembourg":    "EUR",
	"Estonia":       "EUR",
	"Latvia":        "EUR",
	"Lithuania":     "EUR",
	"Slovakia":      "EUR",
	"Slovenia":      "EUR",
	"Cyprus":        "EUR",
	"Malta":         "EUR",
	"Croatia":       "EUR",
	"Eurozone":      "EUR",
	"Europe":        "EUR",
}

// CurrencyExposureEntry represents one currency's share of the portfolio.
type CurrencyExposureEntry struct {
	Currency string  `json:"currency"`
	Value    float64 `json:"value"`
	Pct      float64 `json:"pct"`
}

// HoldingForCurrency represents a holding with its value and country weights.
type HoldingForCurrency struct {
	ISIN           string
	Currency       string             // trading currency of the security
	MarketValue    float64
	CountryWeights map[string]float64 // country name -> weight % (0-100)
}

// ComputeCurrencyExposure calculates portfolio-level currency exposure
// by mapping country weights to currencies.
// For holdings without country weights (e.g., individual stocks), the
// security's trading currency is used.
func ComputeCurrencyExposure(holdings []HoldingForCurrency) []CurrencyExposureEntry {
	totalValue := 0.0
	for _, h := range holdings {
		totalValue += h.MarketValue
	}
	if totalValue == 0 {
		return nil
	}

	exposure := make(map[string]float64)
	for _, h := range holdings {
		if len(h.CountryWeights) == 0 {
			// No country breakdown: use the security's trading currency
			cur := h.Currency
			if cur == "" {
				cur = "EUR"
			}
			exposure[cur] += h.MarketValue
			continue
		}

		// Distribute value by country weights → currency
		weightSum := 0.0
		for _, pct := range h.CountryWeights {
			weightSum += pct
		}
		if weightSum == 0 {
			exposure[h.Currency] += h.MarketValue
			continue
		}

		for country, pct := range h.CountryWeights {
			cur := CountryToCurrency[country]
			if cur == "" {
				cur = "OTHER"
			}
			exposure[cur] += h.MarketValue * pct / weightSum
		}
	}

	// Convert to sorted slice with percentages
	var entries []CurrencyExposureEntry
	for cur, val := range exposure {
		entries = append(entries, CurrencyExposureEntry{
			Currency: cur,
			Value:    val,
			Pct:      val / totalValue * 100,
		})
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Value > entries[j].Value
	})

	return entries
}
