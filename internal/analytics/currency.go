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
	Currency       string // trading currency of the security
	MarketValue    float64
	CountryWeights map[string]float64 // country name -> weight % (0-100)
	// IsFund marks ETFs and other funds. A fund's trading currency says
	// nothing about what it holds, so without country data its exposure is
	// "Unknown" rather than its listing currency.
	IsFund bool
}

// CurrencyUnknown is the bucket for value whose currency exposure is not on
// record: funds without country data and the unlisted part of partial
// country breakdowns.
const CurrencyUnknown = "Unknown"

// ComputeCurrencyExposure calculates portfolio-level currency exposure
// by mapping country weights to currencies.
//   - Holdings with country weights are split by country to currency. Weights
//     that sum to less than 100 % leave the remainder in "Unknown" instead of
//     being scaled up; sums slightly above 100 % (rounding) are normalised.
//   - Funds without country weights go to "Unknown".
//   - Other holdings without country weights (single stocks) use the
//     security's trading currency.
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
		weightSum := 0.0
		for _, pct := range h.CountryWeights {
			if pct > 0 {
				weightSum += pct
			}
		}
		if weightSum <= 0 {
			if h.IsFund {
				exposure[CurrencyUnknown] += h.MarketValue
				continue
			}
			// No country breakdown: use the security's trading currency
			cur := h.Currency
			if cur == "" {
				cur = "EUR"
			}
			exposure[cur] += h.MarketValue
			continue
		}

		// Distribute value by country weights to currency.
		scale := 100.0
		if weightSum > scale {
			scale = weightSum
		}
		for country, pct := range h.CountryWeights {
			if pct <= 0 {
				continue
			}
			cur := CountryToCurrency[country]
			if cur == "" {
				cur = "OTHER"
			}
			exposure[cur] += h.MarketValue * pct / scale
		}
		if weightSum < scale {
			exposure[CurrencyUnknown] += h.MarketValue * (scale - weightSum) / scale
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
