package parser

import (
	"crypto/sha256"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/google/uuid"
)

// RevolutParser handles CSV exports from Revolut. It supports two formats:
//   - Current/checking account: Type,Product,Started Date,Completed Date,
//     Description,Amount,Fee,Currency,State,Balance
//   - Instant Access Savings vault: Date,Description,Gross interest,
//     Money in,Money out,Balance
type RevolutParser struct {
	warnings            []string
	detectedAccountType string
}

func (p *RevolutParser) Warnings() []string          { return p.warnings }
func (p *RevolutParser) DetectedAccountType() string { return p.detectedAccountType }
func (p *RevolutParser) Institution() string         { return "revolut" }

func (p *RevolutParser) Detect(header []string) bool {
	has := make(map[string]bool, len(header))
	for _, h := range normalizeHeader(header) {
		has[h] = true
	}
	if has["money in"] && has["money out"] && has["gross interest"] {
		return true
	}
	if has["started date"] && has["completed date"] && has["state"] {
		return true
	}
	return false
}

func (p *RevolutParser) Parse(records [][]string, accountID uuid.UUID) ([]Transaction, []RSUVest, error) {
	if len(records) < 2 {
		return nil, nil, fmt.Errorf("no data rows")
	}
	// Reset state — the registered parser is a singleton reused across imports.
	p.warnings = nil
	p.detectedAccountType = ""
	idx := headerIndex(records[0])
	if _, ok := idx["money in"]; ok {
		return p.parseSavings(records, accountID, idx)
	}
	return p.parseCurrent(records, accountID, idx)
}

func (p *RevolutParser) parseSavings(records [][]string, accountID uuid.UUID, idx map[string]int) ([]Transaction, []RSUVest, error) {
	p.detectedAccountType = "savings"
	dateCol := findColumn(idx, "date")
	descCol := findColumn(idx, "description")
	inCol := findColumn(idx, "money in")
	outCol := findColumn(idx, "money out")
	if dateCol < 0 || inCol < 0 || outCol < 0 {
		return nil, nil, fmt.Errorf("missing required savings columns (date, money in, money out)")
	}

	var txns []Transaction
	for i, record := range records[1:] {
		rowNum := i + 2
		if len(record) == 0 || (len(record) == 1 && record[0] == "") {
			continue
		}
		dateStr := getField(record, dateCol)
		if dateStr == "" {
			continue
		}
		date, err := parseRevolutShortDate(dateStr)
		if err != nil {
			// Repeated header row or other non-data row — skip silently.
			continue
		}

		desc := getField(record, descCol)
		inAmt, inCur, err := parseRevolutAmount(getField(record, inCol))
		if err != nil {
			p.warnings = append(p.warnings, fmt.Sprintf("row %d: invalid money in %q", rowNum, getField(record, inCol)))
			continue
		}
		outAmt, outCur, err := parseRevolutAmount(getField(record, outCol))
		if err != nil {
			p.warnings = append(p.warnings, fmt.Sprintf("row %d: invalid money out %q", rowNum, getField(record, outCol)))
			continue
		}

		var amount float64
		var currency, txnType string
		switch {
		case inAmt > 0 && outAmt == 0:
			amount = inAmt
			currency = inCur
			txnType = classifyRevolutSavings(desc, true)
		case outAmt > 0 && inAmt == 0:
			amount = outAmt
			currency = outCur
			txnType = classifyRevolutSavings(desc, false)
		default:
			continue
		}
		if currency == "" {
			currency = "EUR"
		}

		txns = append(txns, Transaction{
			AccountID:    accountID,
			Date:         date,
			Type:         txnType,
			Amount:       amount,
			Currency:     currency,
			Counterparty: "Revolut",
			Reference:    desc,
		})
	}
	return txns, nil, nil
}

func classifyRevolutSavings(desc string, moneyIn bool) string {
	d := strings.ToLower(desc)
	switch {
	case strings.Contains(d, "interest"):
		return "interest"
	case strings.Contains(d, "deposit"):
		return "deposit"
	case strings.Contains(d, "withdrawal"):
		return "withdrawal"
	}
	if moneyIn {
		return "deposit"
	}
	return "withdrawal"
}

func (p *RevolutParser) parseCurrent(records [][]string, accountID uuid.UUID, idx map[string]int) ([]Transaction, []RSUVest, error) {
	p.detectedAccountType = "checking"
	typeCol := findColumn(idx, "type")
	startedCol := findColumn(idx, "started date")
	completedCol := findColumn(idx, "completed date")
	descCol := findColumn(idx, "description")
	amountCol := findColumn(idx, "amount")
	feeCol := findColumn(idx, "fee")
	currencyCol := findColumn(idx, "currency")
	stateCol := findColumn(idx, "state")

	if startedCol < 0 || amountCol < 0 {
		return nil, nil, fmt.Errorf("missing required current-account columns (started date, amount)")
	}

	var txns []Transaction
	for i, record := range records[1:] {
		rowNum := i + 2
		if len(record) == 0 || (len(record) == 1 && record[0] == "") {
			continue
		}

		if stateCol >= 0 {
			state := strings.ToUpper(getField(record, stateCol))
			if state != "" && state != "COMPLETED" {
				p.warnings = append(p.warnings, fmt.Sprintf("row %d: skipped (state=%s)", rowNum, state))
				continue
			}
		}

		dateStr := getField(record, completedCol)
		if dateStr == "" {
			dateStr = getField(record, startedCol)
		}
		date, err := parseRevolutDateTime(dateStr)
		if err != nil {
			p.warnings = append(p.warnings, fmt.Sprintf("row %d: skipped (invalid date %q)", rowNum, dateStr))
			continue
		}

		amountStr := getField(record, amountCol)
		signedAmount, err := parseStandardDecimal(amountStr)
		if err != nil {
			p.warnings = append(p.warnings, fmt.Sprintf("row %d: invalid amount %q", rowNum, amountStr))
			continue
		}
		fee, _ := parseStandardDecimal(getField(record, feeCol))
		fee = math.Abs(fee)

		currency := getField(record, currencyCol)
		if currency == "" {
			currency = "EUR"
		}

		rawType := getField(record, typeCol)
		desc := getField(record, descCol)
		txnType := classifyRevolutCurrent(rawType, signedAmount)

		// Dedup hash includes the started timestamp and signed amount so two
		// same-day card payments to the same merchant produce distinct hashes.
		startedRaw := getField(record, startedCol)
		hashData := fmt.Sprintf("%s|%s|%.4f|%s|%s|%s",
			accountID.String(), startedRaw, signedAmount, desc, currency, rawType)
		sum := sha256.Sum256([]byte(hashData))

		txns = append(txns, Transaction{
			AccountID:    accountID,
			Date:         date,
			Type:         txnType,
			Amount:       math.Abs(signedAmount),
			Fee:          fee,
			Currency:     currency,
			Counterparty: desc,
			Reference:    rawType,
			ImportHash:   fmt.Sprintf("%x", sum),
		})
	}
	return txns, nil, nil
}

func classifyRevolutCurrent(rawType string, amount float64) string {
	// Revolut exports the Type field in two forms depending on locale and
	// export source: "Card Payment" (title case) and "CARD_PAYMENT" (upper
	// snake case from the Business API). Normalize both to space-separated
	// lowercase before matching.
	t := strings.ToLower(strings.TrimSpace(rawType))
	t = strings.ReplaceAll(t, "_", " ")
	switch t {
	case "fee", "tax":
		return "fee"
	case "interest":
		return "interest"
	case "card payment", "atm", "topup return":
		return "withdrawal"
	case "topup", "card refund", "card chargeback", "card credit",
		"refund", "tax refund", "reward", "cashback":
		return "deposit"
	}
	if amount > 0 {
		return "deposit"
	}
	return "withdrawal"
}

// parseRevolutShortDate parses Revolut's "Apr 25, 2025" savings date format.
func parseRevolutShortDate(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, fmt.Errorf("empty date")
	}
	return time.Parse("Jan 2, 2006", s)
}

// parseRevolutDateTime parses Revolut's "2026-05-16 12:42:21" datetime format,
// truncating to a date. Falls back to plain ISO date if no time component.
func parseRevolutDateTime(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, fmt.Errorf("empty date")
	}
	if t, err := time.Parse("2006-01-02 15:04:05", s); err == nil {
		return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC), nil
	}
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return t, nil
	}
	return time.Time{}, fmt.Errorf("invalid Revolut date %q", s)
}

// parseRevolutAmount strips a leading currency symbol from values like
// "€2,000.00" and returns the parsed amount plus the detected currency
// ("" when the input is blank).
func parseRevolutAmount(s string) (float64, string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, "", nil
	}
	currency := ""
	runes := []rune(s)
	switch runes[0] {
	case '€':
		currency = "EUR"
		s = strings.TrimSpace(string(runes[1:]))
	case '$':
		currency = "USD"
		s = strings.TrimSpace(string(runes[1:]))
	case '£':
		currency = "GBP"
		s = strings.TrimSpace(string(runes[1:]))
	}
	v, err := parseStandardDecimal(s)
	return v, currency, err
}
