package parser

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// WealthExportParser handles CSVs produced by /api/settings/export-transactions.
// Format: UTF-8 (with BOM), semicolon-delimited, header:
//
//	date;type;account;counterparty;security_isin;quantity;price;amount;fee;tax;currency;reference
//
// This is the canonical round-trip: re-importing an exported CSV into the
// same account produces identical ImportHash values (computeHash hashes
// account_id + date + amount + reference + counterparty, all of which
// round-trip cleanly through the export format).
type WealthExportParser struct{}

func (p *WealthExportParser) Institution() string { return "wealth_export" }

func (p *WealthExportParser) Detect(header []string) bool {
	norm := normalizeHeader(header)
	// Detection key: the exact column set in the exact order from
	// HandleExportTransactions. Looking for the combination of "date" +
	// "type" + "amount" + "security_isin" is enough to be unambiguous —
	// no other supported parser uses those four together as plain
	// English column names.
	hasDate, hasType, hasAmount, hasISIN := false, false, false, false
	for _, h := range norm {
		switch h {
		case "date":
			hasDate = true
		case "type":
			hasType = true
		case "amount":
			hasAmount = true
		case "security_isin":
			hasISIN = true
		}
	}
	return hasDate && hasType && hasAmount && hasISIN
}

func (p *WealthExportParser) Parse(records [][]string, accountID uuid.UUID) ([]Transaction, []RSUVest, error) {
	if len(records) < 2 {
		return nil, nil, fmt.Errorf("no data rows")
	}
	idx := headerIndex(records[0])

	dateCol, hasDate := idx["date"]
	typeCol, hasType := idx["type"]
	amountCol, hasAmount := idx["amount"]
	if !hasDate || !hasType || !hasAmount {
		return nil, nil, fmt.Errorf("missing required columns: date / type / amount")
	}
	counterpartyCol := findColumn(idx, "counterparty")
	isinCol := findColumn(idx, "security_isin")
	qtyCol := findColumn(idx, "quantity")
	priceCol := findColumn(idx, "price")
	feeCol := findColumn(idx, "fee")
	taxCol := findColumn(idx, "tax")
	currencyCol := findColumn(idx, "currency")
	refCol := findColumn(idx, "reference")

	// The exporter writes floats with `%.8f` / `%.4f` (US locale, dot
	// decimal). Use plain strconv.ParseFloat rather than the German
	// parser used elsewhere, rejecting malformed or non-finite values.
	parseAmount := func(field, s string) (float64, error) {
		s = strings.TrimSpace(s)
		if s == "" {
			return 0, nil
		}
		v, err := strconv.ParseFloat(s, 64)
		if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
			return 0, fmt.Errorf("invalid %s %q", field, s)
		}
		return v, nil
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
		date, err := time.Parse("2006-01-02", dateStr)
		if err != nil {
			return nil, nil, fmt.Errorf("row %d: parse date %q: %w", i+2, dateStr, err)
		}
		amt, err := parseAmount("amount", getField(record, amountCol))
		if err != nil {
			return nil, nil, fmt.Errorf("row %d: %w", i+2, err)
		}
		qty, err := parseAmount("quantity", getField(record, qtyCol))
		if err != nil {
			return nil, nil, fmt.Errorf("row %d: %w", i+2, err)
		}
		price, err := parseAmount("price", getField(record, priceCol))
		if err != nil {
			return nil, nil, fmt.Errorf("row %d: %w", i+2, err)
		}
		fee, err := parseAmount("fee", getField(record, feeCol))
		if err != nil {
			return nil, nil, fmt.Errorf("row %d: %w", i+2, err)
		}
		tax, err := parseAmount("tax", getField(record, taxCol))
		if err != nil {
			return nil, nil, fmt.Errorf("row %d: %w", i+2, err)
		}

		txn := Transaction{
			AccountID:    accountID,
			Date:         date,
			Type:         strings.TrimSpace(getField(record, typeCol)),
			Amount:       amt,
			Quantity:     qty,
			Price:        price,
			Fee:          fee,
			Tax:          tax,
			Currency:     strings.TrimSpace(getField(record, currencyCol)),
			Counterparty: strings.TrimSpace(getField(record, counterpartyCol)),
			SecurityISIN: strings.TrimSpace(getField(record, isinCol)),
			Reference:    strings.TrimSpace(getField(record, refCol)),
		}
		if txn.Currency == "" {
			txn.Currency = "EUR"
		}
		txns = append(txns, txn)
	}
	return txns, nil, nil
}
