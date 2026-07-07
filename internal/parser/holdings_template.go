package parser

import (
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// HoldingsTemplateParser handles the app's own holdings CSV template.
// Used for manual entry of positions (e.g., Sparkasse depot without CSV export).
// Generates synthetic buy transactions to establish positions.
type HoldingsTemplateParser struct{}

func (p *HoldingsTemplateParser) Institution() string { return "holdings_template" }

func (p *HoldingsTemplateParser) Detect(header []string) bool {
	norm := normalizeHeader(header)
	hasISIN := false
	hasQuantity := false
	hasMarketValue := false
	for _, h := range norm {
		if h == "isin" {
			hasISIN = true
		}
		if h == "quantity" {
			hasQuantity = true
		}
		if h == "market_value" {
			hasMarketValue = true
		}
	}
	return hasISIN && hasQuantity && hasMarketValue
}

func (p *HoldingsTemplateParser) Parse(records [][]string, accountID uuid.UUID) ([]Transaction, []RSUVest, error) {
	if len(records) < 2 {
		return nil, nil, fmt.Errorf("no data rows")
	}

	idx := headerIndex(records[0])

	isinCol := findColumn(idx, "isin")
	nameCol := findColumn(idx, "name")
	quantityCol := findColumn(idx, "quantity")
	marketValueCol := findColumn(idx, "market_value")
	currencyCol := findColumn(idx, "currency")
	dateCol := findColumn(idx, "date")

	if isinCol < 0 || quantityCol < 0 || marketValueCol < 0 {
		return nil, nil, fmt.Errorf("missing required columns: isin, quantity, market_value")
	}

	var txns []Transaction
	for i, record := range records[1:] {
		if len(record) == 0 || (len(record) == 1 && record[0] == "") {
			continue
		}

		isin := strings.TrimSpace(getField(record, isinCol))
		if isin == "" {
			continue
		}

		name := getField(record, nameCol)
		quantityStr := getField(record, quantityCol)
		marketValueStr := getField(record, marketValueCol)

		quantity, err := parseStandardDecimal(quantityStr)
		if err != nil {
			return nil, nil, fmt.Errorf("row %d: parse quantity %q: %w", i+2, quantityStr, err)
		}

		marketValue, err := parseStandardDecimal(marketValueStr)
		if err != nil {
			return nil, nil, fmt.Errorf("row %d: parse market_value %q: %w", i+2, marketValueStr, err)
		}

		currency := "EUR"
		if c := getField(record, currencyCol); c != "" {
			currency = c
		}

		date := defaultDate()
		if dateStr := getField(record, dateCol); dateStr != "" {
			if d, err := parseISODate(dateStr); err == nil {
				date = d
			} else if d, err := parseGermanDate(dateStr); err == nil {
				date = d
			}
		}

		price := 0.0
		if quantity > 0 {
			price = marketValue / quantity
		}

		txn := Transaction{
			AccountID:    accountID,
			Date:         date,
			Type:         "buy",
			SecurityISIN: isin,
			Quantity:     quantity,
			Price:        price,
			Amount:       marketValue,
			Currency:     currency,
			Counterparty: name,
			Reference:    "holdings template import",
		}

		txns = append(txns, txn)
	}

	return txns, nil, nil
}
