package parser

import (
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// N26Parser handles CSV exports from N26.
// Expected encoding: UTF-8, delimiter: comma, numbers: standard format, dates: YYYY-MM-DD.
type N26Parser struct{}

func (p *N26Parser) Institution() string { return "n26" }

func (p *N26Parser) Detect(header []string) bool {
	norm := normalizeHeader(header)
	// N26 CSVs contain "payee" or "empfänger" in English/German respectively
	for _, h := range norm {
		if h == "payee" || h == "empfänger" || h == "empfaenger" {
			return true
		}
	}
	return false
}

func (p *N26Parser) Parse(records [][]string, accountID uuid.UUID) ([]Transaction, error) {
	if len(records) < 2 {
		return nil, fmt.Errorf("no data rows")
	}

	idx := headerIndex(records[0])

	// Detect language from header
	dateCol := findColumn(idx, "date", "datum")
	payeeCol := findColumn(idx, "payee", "empfänger", "empfaenger")
	typeCol := findColumn(idx, "transaction type", "transaktionstyp")
	referenceCol := findColumn(idx, "payment reference", "verwendungszweck", "zahlungsreferenz")
	amountCol := findColumn(idx, "amount (eur)", "betrag (eur)", "amount")

	if dateCol < 0 || amountCol < 0 {
		return nil, fmt.Errorf("missing required columns: date and/or amount")
	}

	var txns []Transaction
	for i, record := range records[1:] {
		if len(record) == 0 || (len(record) == 1 && record[0] == "") {
			continue
		}

		dateStr := getField(record, dateCol)
		if dateStr == "" {
			continue
		}

		date, err := parseISODate(dateStr)
		if err != nil {
			// Try German date format as fallback
			date, err = parseGermanDate(dateStr)
			if err != nil {
				return nil, fmt.Errorf("row %d: parse date %q: %w", i+2, dateStr, err)
			}
		}

		amountStr := getField(record, amountCol)
		amount, err := parseStandardDecimal(amountStr)
		if err != nil {
			return nil, fmt.Errorf("row %d: parse amount %q: %w", i+2, amountStr, err)
		}

		payee := getField(record, payeeCol)
		txnType := getField(record, typeCol)
		reference := getField(record, referenceCol)

		normalizedType := classifyN26Transaction(amount, txnType)

		txn := Transaction{
			AccountID:    accountID,
			Date:         date,
			Type:         normalizedType,
			Amount:       amount,
			Currency:     "EUR",
			Counterparty: payee,
			Reference:    reference,
		}

		if amount < 0 && normalizedType == "withdrawal" {
			txn.Amount = -amount
		}

		txns = append(txns, txn)
	}

	return txns, nil
}

func classifyN26Transaction(amount float64, txnType string) string {
	tt := strings.ToLower(txnType)

	switch {
	case strings.Contains(tt, "interest"):
		return "interest"
	case strings.Contains(tt, "zinsen"):
		return "interest"
	case amount > 0:
		return "deposit"
	default:
		return "withdrawal"
	}
}
