package parser

import (
	"crypto/sha256"
	"fmt"
	"math"
	"strings"

	"github.com/google/uuid"
)

// ScalableCapitalParser handles CSV exports from Scalable Capital.
// Format: UTF-8 (with BOM), semicolon-delimited.
// Columns: date;status;type;sub_type;side;isin;description;quantity;amount;currency;is_cancellation
type ScalableCapitalParser struct {
	warnings            []string
	detectedAccountType string
}

func (p *ScalableCapitalParser) Warnings() []string          { return p.warnings }
func (p *ScalableCapitalParser) DetectedAccountType() string { return p.detectedAccountType }

func (p *ScalableCapitalParser) Institution() string { return "scalable_capital" }

func (p *ScalableCapitalParser) Detect(header []string) bool {
	norm := normalizeHeader(header)
	has := make(map[string]bool, len(norm))
	for _, h := range norm {
		has[h] = true
	}
	// Bookmarklet format: has sub_type + is_cancellation columns
	if has["isin"] && has["sub_type"] && has["is_cancellation"] {
		return true
	}
	// PRIME+ native format: has isin + shares/stück but NOT sub_type
	if has["isin"] && (has["shares"] || has["stück"] || has["stueck"]) && !has["sub_type"] {
		return true
	}
	return false
}

func (p *ScalableCapitalParser) Parse(records [][]string, accountID uuid.UUID) ([]Transaction, []RSUVest, error) {
	if len(records) < 2 {
		return nil, nil, fmt.Errorf("no data rows")
	}

	idx := headerIndex(records[0])

	dateCol := findColumn(idx, "date", "datum")
	statusCol := findColumn(idx, "status")
	typeCol := findColumn(idx, "type", "typ")
	subTypeCol := findColumn(idx, "sub_type")
	sideCol := findColumn(idx, "side")
	isinCol := findColumn(idx, "isin")
	descCol := findColumn(idx, "description", "name", "titel")
	quantityCol := findColumn(idx, "quantity", "shares", "stück", "stueck")
	amountCol := findColumn(idx, "amount", "betrag")
	currencyCol := findColumn(idx, "currency", "währung", "waehrung")
	cancelCol := findColumn(idx, "is_cancellation")
	accountCol := findColumn(idx, "account")

	if dateCol < 0 || isinCol < 0 {
		return nil, nil, fmt.Errorf("missing required columns: date and/or isin")
	}

	// Detect account type from the CSV's "account" column if present
	if accountCol >= 0 && len(records) > 1 {
		acctVal := strings.ToLower(strings.TrimSpace(getField(records[1], accountCol)))
		switch acctVal {
		case "broker":
			p.detectedAccountType = "brokerage"
		case "savings":
			p.detectedAccountType = "savings"
		}
	}

	var txns []Transaction
	p.warnings = nil
	for i, record := range records[1:] {
		if len(record) == 0 || (len(record) == 1 && record[0] == "") {
			continue
		}

		// Only process SETTLED rows; REJECTED/CANCELLED have amount=0 and should be skipped.
		if statusCol >= 0 {
			status := strings.ToUpper(strings.TrimSpace(getField(record, statusCol)))
			if status != "" && status != "SETTLED" {
				p.warnings = append(p.warnings, fmt.Sprintf("row %d: skipped (status=%s)", i+2, status))
				continue
			}
		}

		dateStr := getField(record, dateCol)
		if dateStr == "" {
			p.warnings = append(p.warnings, fmt.Sprintf("row %d: skipped (empty date)", i+2))
			continue
		}

		date, err := parseISODate(dateStr)
		if err != nil {
			date, err = parseGermanDate(dateStr)
			if err != nil {
				p.warnings = append(p.warnings, fmt.Sprintf("row %d: skipped (invalid date %q)", i+2, dateStr))
				continue
			}
		}

		isin := getField(record, isinCol)
		desc := getField(record, descCol)
		typeRaw := strings.ToLower(getField(record, typeCol))
		subType := strings.ToLower(getField(record, subTypeCol))
		side := strings.ToLower(getField(record, sideCol))

		quantity, _ := parseStandardDecimal(getField(record, quantityCol))
		// amount is signed in the CSV: negative = money out, positive = money in
		signedAmount, _ := parseStandardDecimal(getField(record, amountCol))

		currency := "EUR"
		if c := getField(record, currencyCol); c != "" {
			currency = c
		}

		// Derive price from amount/quantity
		var price float64
		if quantity != 0 && signedAmount != 0 {
			price = math.Abs(signedAmount) / math.Abs(quantity)
		}

		txnType := classifyScalableType(typeRaw, subType, side)

		// Cancellation rows reverse a prior transaction. Flip the type so they
		// net to zero against the original when the cash balance is computed.
		isCancellation := strings.ToLower(getField(record, cancelCol)) == "true"
		if isCancellation {
			txnType = reverseTxnType(txnType)
		}

		// Dedup hash includes signed amount so cancellations (opposite sign) get distinct hashes.
		hashData := fmt.Sprintf("%s|%s|%.4f|%s|%s|%.8f",
			accountID.String(), date.Format("2006-01-02"),
			signedAmount, isin, desc, quantity)
		hash := sha256.Sum256([]byte(hashData))

		txn := Transaction{
			AccountID:    accountID,
			Date:         date,
			Type:         txnType,
			SecurityISIN: isin,
			Quantity:     math.Abs(quantity),
			Price:        price,
			Amount:       math.Abs(signedAmount),
			Fee:          0,
			Tax:          0,
			Currency:     currency,
			Counterparty: desc,
			ImportHash:   fmt.Sprintf("%x", hash),
		}

		txns = append(txns, txn)
	}

	return txns, nil, nil
}

func classifyScalableType(txnType, subType, side string) string {
	// PRIME+ native format: no sub_type/tx_type columns, classify from side or type directly
	if txnType == "" && subType == "" {
		switch side {
		case "buy", "kauf":
			return "buy"
		case "sell", "verkauf":
			return "sell"
		case "dividend", "dividende", "distribution", "ausschüttung":
			return "dividend"
		case "deposit", "einzahlung":
			return "deposit"
		case "withdrawal", "auszahlung":
			return "withdrawal"
		case "savings_plan", "sparplan":
			return "savings_plan"
		case "interest", "zinsen":
			return "interest"
		case "fee", "gebühr", "tax", "steuer":
			return "fee"
		default:
			if side != "" {
				return "buy" // security transaction default
			}
			return "deposit"
		}
	}

	switch txnType {
	case "security_transaction":
		if subType == "savings_plan" || subType == "sparplan" {
			return "savings_plan"
		}
		if side == "sell" {
			return "sell"
		}
		return "buy"

	case "cash_transaction":
		switch subType {
		case "deposit":
			return "deposit"
		case "withdrawal":
			return "withdrawal"
		case "distribution", "dividend", "dividende":
			return "dividend"
		case "interest", "zinsen":
			return "interest"
		case "tax", "steuer":
			return "fee"
		case "cash_transfer_out":
			return "cash_transfer_out"
		case "cash_transfer_in":
			return "cash_transfer_in"
		default:
			return "deposit"
		}

	case "non_trade_security_transaction":
		if subType == "transfer_out" {
			return "transfer_out"
		}
		return "transfer"

	default:
		return "deposit"
	}
}

// reverseTxnType returns the opposite transaction type for cancellation rows.
// A cancelled buy returns cash (sell), a cancelled deposit removes cash (withdrawal), etc.
func reverseTxnType(t string) string {
	switch t {
	case "buy", "savings_plan":
		return "sell"
	case "sell":
		return "buy"
	case "deposit":
		return "withdrawal"
	case "withdrawal":
		return "deposit"
	case "dividend", "interest":
		return "fee"
	case "fee":
		return "interest"
	case "transfer":
		return "transfer_out"
	case "transfer_out":
		return "transfer"
	case "cash_transfer_in":
		return "cash_transfer_out"
	case "cash_transfer_out":
		return "cash_transfer_in"
	default:
		return t
	}
}
