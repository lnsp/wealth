package parser

import (
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// ScalableCapitalParser handles CSV exports from Scalable Capital (native or bookmarklet).
// Expected encoding: UTF-8, delimiter: semicolon, numbers: standard format.
type ScalableCapitalParser struct{}

func (p *ScalableCapitalParser) Institution() string { return "scalable_capital" }

func (p *ScalableCapitalParser) Detect(header []string) bool {
	norm := normalizeHeader(header)
	hasISIN := false
	hasShares := false
	hasType := false
	for _, h := range norm {
		if h == "isin" {
			hasISIN = true
		}
		if h == "shares" || h == "stück" || h == "stueck" {
			hasShares = true
		}
		if h == "type" || h == "typ" {
			hasType = true
		}
	}
	return hasISIN && (hasShares || hasType)
}

func (p *ScalableCapitalParser) Parse(records [][]string, accountID uuid.UUID) ([]Transaction, error) {
	if len(records) < 2 {
		return nil, fmt.Errorf("no data rows")
	}

	idx := headerIndex(records[0])

	dateCol := findColumn(idx, "date", "datum")
	statusCol := findColumn(idx, "status")
	typeCol := findColumn(idx, "type", "typ")
	isinCol := findColumn(idx, "isin")
	nameCol := findColumn(idx, "name", "titel")
	sharesCol := findColumn(idx, "shares", "stück", "stueck")
	priceCol := findColumn(idx, "price", "preis", "kurs")
	amountCol := findColumn(idx, "amount", "betrag")
	feeCol := findColumn(idx, "fee", "gebühr", "gebuehr")
	taxCol := findColumn(idx, "tax", "steuer")
	currencyCol := findColumn(idx, "currency", "währung", "waehrung")

	if dateCol < 0 || isinCol < 0 {
		return nil, fmt.Errorf("missing required columns: date and/or isin")
	}

	var txns []Transaction
	for i, record := range records[1:] {
		if len(record) == 0 || (len(record) == 1 && record[0] == "") {
			continue
		}

		// Filter by status if present
		if statusCol >= 0 {
			status := strings.ToLower(getField(record, statusCol))
			if status != "" && status != "executed" && status != "ausgeführt" && status != "ausgefuehrt" {
				continue
			}
		}

		dateStr := getField(record, dateCol)
		if dateStr == "" {
			continue
		}

		date, err := parseISODate(dateStr)
		if err != nil {
			date, err = parseGermanDate(dateStr)
			if err != nil {
				return nil, fmt.Errorf("row %d: parse date %q: %w", i+2, dateStr, err)
			}
		}

		isin := getField(record, isinCol)
		name := getField(record, nameCol)
		txnTypeRaw := strings.ToLower(getField(record, typeCol))

		shares, _ := parseStandardDecimal(getField(record, sharesCol))
		price, _ := parseStandardDecimal(getField(record, priceCol))
		amount, _ := parseStandardDecimal(getField(record, amountCol))
		fee, _ := parseStandardDecimal(getField(record, feeCol))
		tax, _ := parseStandardDecimal(getField(record, taxCol))

		currency := "EUR"
		if c := getField(record, currencyCol); c != "" {
			currency = c
		}

		txnType := classifyScalableTransaction(txnTypeRaw)

		txn := Transaction{
			AccountID:    accountID,
			Date:         date,
			Type:         txnType,
			SecurityISIN: isin,
			Quantity:     shares,
			Price:        price,
			Amount:       abs(amount),
			Fee:          abs(fee),
			Tax:          abs(tax),
			Currency:     currency,
			Counterparty: name,
		}

		txns = append(txns, txn)
	}

	return txns, nil
}

func classifyScalableTransaction(txnType string) string {
	switch {
	case strings.Contains(txnType, "buy") || strings.Contains(txnType, "kauf"):
		return "buy"
	case strings.Contains(txnType, "sell") || strings.Contains(txnType, "verkauf"):
		return "sell"
	case strings.Contains(txnType, "dividend") || strings.Contains(txnType, "dividende") || strings.Contains(txnType, "ausschüttung"):
		return "dividend"
	case strings.Contains(txnType, "savings") || strings.Contains(txnType, "sparplan"):
		return "savings_plan"
	case strings.Contains(txnType, "fee") || strings.Contains(txnType, "gebühr") || strings.Contains(txnType, "gebuehr"):
		return "fee"
	default:
		return "buy"
	}
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}
