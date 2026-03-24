package parser

import (
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// SparkasseParser handles CSV exports from Sparkasse online banking.
// Expected encoding: ISO-8859-1, delimiter: semicolon, numbers: German format.
type SparkasseParser struct{}

func (p *SparkasseParser) Institution() string { return "sparkasse" }

func (p *SparkasseParser) Detect(header []string) bool {
	norm := normalizeHeader(header)
	// Sparkasse CSVs contain "auftragskonto" and "buchungstag"
	hasAuftragskonto := false
	hasBuchungstag := false
	for _, h := range norm {
		if strings.Contains(h, "auftragskonto") {
			hasAuftragskonto = true
		}
		if strings.Contains(h, "buchungstag") {
			hasBuchungstag = true
		}
	}
	return hasAuftragskonto && hasBuchungstag
}

func (p *SparkasseParser) Parse(records [][]string, accountID uuid.UUID) ([]Transaction, error) {
	if len(records) < 2 {
		return nil, fmt.Errorf("no data rows")
	}

	idx := headerIndex(records[0])

	// Required columns
	dateCol, hasDate := idx["buchungstag"]
	amountCol, hasAmount := idx["betrag"]
	if !hasDate || !hasAmount {
		return nil, fmt.Errorf("missing required columns: buchungstag and/or betrag")
	}

	// Optional columns
	currencyCol := idx["währung"]
	counterpartyCol := findColumn(idx, "begünstigter/zahlungspflichtiger", "beguenstigter/zahlungspflichtiger", "begünstigter", "beguenstigter")
	referenceCol := findColumn(idx, "verwendungszweck")
	typeCol := findColumn(idx, "buchungstext")

	var txns []Transaction
	for i, record := range records[1:] {
		if len(record) == 0 || (len(record) == 1 && record[0] == "") {
			continue
		}

		dateStr := getField(record, dateCol)
		if dateStr == "" {
			continue
		}

		date, err := parseGermanDate(dateStr)
		if err != nil {
			return nil, fmt.Errorf("row %d: %w", i+2, err)
		}

		amountStr := getField(record, amountCol)
		amount, err := parseGermanDecimal(amountStr)
		if err != nil {
			return nil, fmt.Errorf("row %d: parse amount %q: %w", i+2, amountStr, err)
		}

		currency := "EUR"
		if c := getField(record, currencyCol); c != "" {
			currency = c
		}

		counterparty := getField(record, counterpartyCol)
		reference := getField(record, referenceCol)
		buchungstext := getField(record, typeCol)

		txnType := classifySparkasseTransaction(amount, buchungstext)

		txn := Transaction{
			AccountID:    accountID,
			Date:         date,
			Type:         txnType,
			Amount:       amount,
			Currency:     currency,
			Counterparty: counterparty,
			Reference:    reference,
			Category:     categorizeByBuchungstext(buchungstext),
		}

		// Normalize: amount should be positive for deposits, negative for withdrawals
		// But we store absolute amounts with type indicating direction
		if amount < 0 && txnType == "withdrawal" {
			txn.Amount = -amount
		} else if amount > 0 && txnType == "deposit" {
			txn.Amount = amount
		}

		txns = append(txns, txn)
	}

	return txns, nil
}

// findColumn returns the index of the first matching column name.
func findColumn(idx map[string]int, names ...string) int {
	for _, name := range names {
		if i, ok := idx[name]; ok {
			return i
		}
	}
	return -1
}

func classifySparkasseTransaction(amount float64, buchungstext string) string {
	bt := strings.ToLower(buchungstext)

	switch {
	case strings.Contains(bt, "zinsen"):
		return "interest"
	case strings.Contains(bt, "gebühr") || strings.Contains(bt, "gebuehr") || strings.Contains(bt, "entgelt"):
		return "fee"
	case strings.Contains(bt, "umbuchung") || strings.Contains(bt, "übertrag") || strings.Contains(bt, "uebertrag"):
		return "transfer"
	case amount > 0:
		return "deposit"
	default:
		return "withdrawal"
	}
}

func categorizeByBuchungstext(buchungstext string) string {
	bt := strings.ToLower(buchungstext)
	switch {
	case strings.Contains(bt, "gehalt") || strings.Contains(bt, "lohn"):
		return "income"
	case strings.Contains(bt, "miete"):
		return "rent"
	case strings.Contains(bt, "versicherung"):
		return "insurance"
	case strings.Contains(bt, "strom") || strings.Contains(bt, "gas") || strings.Contains(bt, "wasser"):
		return "utilities"
	default:
		return ""
	}
}
