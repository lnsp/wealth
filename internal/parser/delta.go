package parser

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// DeltaParser handles CSV exports from the Delta portfolio-tracker app. Delta
// is multi-asset (CRYPTO + STOCK + FIAT) and emits one row per ledger event.
// For trades it also emits a "Sync Base Holding" mirror row representing the
// cash leg — we skip those (their Notes start with SYNC-BASE-HOLDINGS_) so we
// don't double-count.
type DeltaParser struct {
	warnings            []string
	detectedAccountType string
}

func (p *DeltaParser) Warnings() []string          { return p.warnings }
func (p *DeltaParser) DetectedAccountType() string { return p.detectedAccountType }
func (p *DeltaParser) Institution() string         { return "delta" }

func (p *DeltaParser) Detect(header []string) bool {
	has := make(map[string]bool, len(header))
	for _, h := range normalizeHeader(header) {
		has[h] = true
	}
	// "Way" + "Base amount" + "Base currency (name)" are unique to Delta.
	return has["way"] && has["base amount"] && has["base currency (name)"]
}

func (p *DeltaParser) Parse(records [][]string, accountID uuid.UUID) ([]Transaction, []RSUVest, error) {
	if len(records) < 2 {
		return nil, nil, fmt.Errorf("no data rows")
	}
	p.warnings = nil
	p.detectedAccountType = "brokerage"

	idx := headerIndex(records[0])
	dateCol := findColumn(idx, "date")
	wayCol := findColumn(idx, "way")
	baseAmountCol := findColumn(idx, "base amount")
	baseCurrencyCol := findColumn(idx, "base currency (name)")
	baseTypeCol := findColumn(idx, "base type")
	quoteAmountCol := findColumn(idx, "quote amount")
	quoteCurrencyCol := findColumn(idx, "quote currency")
	feeAmountCol := findColumn(idx, "fee amount")
	feeCurrencyCol := findColumn(idx, "fee currency (name)")
	exchangeCol := findColumn(idx, "exchange")
	notesCol := findColumn(idx, "notes")
	syncCol := findColumn(idx, "sync base holding")

	if dateCol < 0 || wayCol < 0 || baseAmountCol < 0 || baseCurrencyCol < 0 {
		return nil, nil, fmt.Errorf("missing required Delta columns")
	}

	var txns []Transaction
	for i, record := range records[1:] {
		rowNum := i + 2
		if len(record) == 0 || (len(record) == 1 && record[0] == "") {
			continue
		}

		// Auto-generated cash-leg rows have Notes like
		// "SYNC-BASE-HOLDINGS_BUY_EUNM.DE/EUR" and Sync Base Holding=false.
		// They mirror an asset row in the same statement; importing both
		// would double-count.
		notes := getField(record, notesCol)
		if strings.HasPrefix(notes, "SYNC-BASE-HOLDINGS") {
			p.warnings = append(p.warnings, fmt.Sprintf("row %d: skipped (auto sync cash leg)", rowNum))
			continue
		}
		if syncCol >= 0 {
			sync := strings.ToLower(getField(record, syncCol))
			if sync == "false" {
				p.warnings = append(p.warnings, fmt.Sprintf("row %d: skipped (sync_base_holding=false)", rowNum))
				continue
			}
		}

		dateStr := getField(record, dateCol)
		date, err := parseDeltaDateTime(dateStr)
		if err != nil {
			p.warnings = append(p.warnings, fmt.Sprintf("row %d: skipped (invalid date %q)", rowNum, dateStr))
			continue
		}

		baseAmount, err := parseStandardDecimal(getField(record, baseAmountCol))
		if err != nil {
			p.warnings = append(p.warnings, fmt.Sprintf("row %d: invalid base amount", rowNum))
			continue
		}

		baseType := strings.ToUpper(getField(record, baseTypeCol))
		baseSymbol, baseName := splitDeltaSymbol(getField(record, baseCurrencyCol))

		// FIAT rows that aren't sync legs are user-recorded cash movements;
		// without a securities-pipeline analog they would create orphan
		// "buy"s in the holdings view. Surface them as warnings rather than
		// silently dropping so the user can re-enter manually if needed.
		if baseType == "FIAT" {
			p.warnings = append(p.warnings,
				fmt.Sprintf("row %d: skipped FIAT %s — record cash moves on the linked checking account", rowNum, baseSymbol))
			continue
		}

		isin := deltaSyntheticISIN(baseType, baseSymbol)
		if isin == "" {
			p.warnings = append(p.warnings, fmt.Sprintf("row %d: unsupported base type %q", rowNum, baseType))
			continue
		}

		way := strings.ToUpper(getField(record, wayCol))
		quoteAmount, _ := parseStandardDecimal(getField(record, quoteAmountCol))
		quoteCurrency := getField(record, quoteCurrencyCol)
		feeAmount, _ := parseStandardDecimal(getField(record, feeAmountCol))
		_, feeCurName := splitDeltaSymbol(getField(record, feeCurrencyCol))
		_ = feeCurName // currently surfaced only via the fee amount

		var (
			txnType  string
			quantity float64
			amount   float64
			price    float64
			currency string
		)

		switch way {
		case "BUY":
			txnType = "buy"
			quantity = baseAmount
			amount = quoteAmount
			currency = quoteCurrency
			if quantity > 0 {
				price = quoteAmount / quantity
			}
		case "SELL":
			txnType = "sell"
			quantity = baseAmount
			amount = quoteAmount
			currency = quoteCurrency
			if quantity > 0 {
				price = quoteAmount / quantity
			}
		case "DEPOSIT":
			// Wallet receipt — record the position, no cost basis recorded.
			txnType = "transfer"
			quantity = baseAmount
			currency = quoteCurrency
			if currency == "" {
				currency = "EUR"
			}
		case "WITHDRAW", "WITHDRAWAL":
			txnType = "transfer_out"
			quantity = baseAmount
			currency = quoteCurrency
			if currency == "" {
				currency = "EUR"
			}
		default:
			p.warnings = append(p.warnings, fmt.Sprintf("row %d: unsupported Way %q", rowNum, way))
			continue
		}

		if currency == "" {
			currency = "EUR"
		}

		// Custom hash includes the full ISO timestamp + Way + ISIN + base
		// amount so multiple same-day trades on the same asset dedupe by
		// the original event's timestamp.
		hashData := fmt.Sprintf("%s|%s|%s|%s|%.8f|%.4f",
			accountID.String(), dateStr, way, isin, baseAmount, quoteAmount)
		sum := sha256.Sum256([]byte(hashData))

		exchange := getField(record, exchangeCol)
		reference := way
		if exchange != "" {
			reference = way + " @ " + exchange
		}

		txns = append(txns, Transaction{
			AccountID:    accountID,
			Date:         date,
			Type:         txnType,
			SecurityISIN: isin,
			Quantity:     quantity,
			Price:        price,
			Amount:       amount,
			Fee:          feeAmount,
			Currency:     currency,
			Counterparty: baseName,
			Reference:    reference,
			ImportHash:   fmt.Sprintf("%x", sum),
		})
	}
	return txns, nil, nil
}

// splitDeltaSymbol turns Delta's "BTC (Bitcoin)" or "ASME.DE (ASML Holding NV)"
// representation into (symbol, name).
func splitDeltaSymbol(s string) (symbol, name string) {
	s = strings.TrimSpace(s)
	open := strings.Index(s, "(")
	close := strings.LastIndex(s, ")")
	if open <= 0 || close <= open {
		return s, ""
	}
	return strings.TrimSpace(s[:open]), strings.TrimSpace(s[open+1 : close])
}

// deltaSyntheticISIN builds the synthetic identifier used in the securities
// table for assets that don't have a real ISIN exposed by Delta. Stocks are
// keyed by their Yahoo-style ticker (e.g. "ASME.DE") and crypto by a
// CRYPTO:<SYMBOL> prefix that the asset classifier recognises.
func deltaSyntheticISIN(baseType, symbol string) string {
	symbol = strings.TrimSpace(symbol)
	if symbol == "" {
		return ""
	}
	switch strings.ToUpper(baseType) {
	case "CRYPTO":
		return "CRYPTO:" + strings.ToUpper(symbol)
	case "STOCK":
		return symbol
	}
	return ""
}

// parseDeltaDateTime parses Delta's ISO-8601 with milliseconds
// (e.g. "2016-08-25T14:55:00.000Z") and truncates to a date.
func parseDeltaDateTime(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, fmt.Errorf("empty date")
	}
	for _, layout := range []string{
		"2006-01-02T15:04:05.000Z",
		"2006-01-02T15:04:05Z",
		time.RFC3339,
		"2006-01-02",
	} {
		if t, err := time.Parse(layout, s); err == nil {
			return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC), nil
		}
	}
	return time.Time{}, fmt.Errorf("invalid Delta date %q", s)
}
