package parser

import (
	"crypto/sha256"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// INGParser handles the two ING-DiBa CSV exports a retail brokerage customer
// can pull from "Ordermanager" (executed orders) and "Depotübersicht" (current
// holdings snapshot). Both are ISO-8859-1 + semicolon-delimited and have the
// real column header preceded by a few title and blank rows; the package-level
// ParseCSV widens its header-detection window to accommodate that.
type INGParser struct {
	warnings            []string
	detectedAccountType string
}

func (p *INGParser) Warnings() []string          { return p.warnings }
func (p *INGParser) DetectedAccountType() string { return p.detectedAccountType }
func (p *INGParser) Institution() string         { return "ing" }

func (p *INGParser) Detect(header []string) bool {
	has := make(map[string]bool, len(header))
	for _, h := range normalizeHeader(header) {
		has[h] = true
	}
	// Ordermanager: order type + execution date columns are unique to this file.
	if has["orderart"] && has["ausführungsdatum"] && has["isin"] {
		return true
	}
	// Depotübersicht: cost basis + market value snapshot columns.
	if has["isin"] && has["einstandskurs"] && has["bewertungskurs"] {
		return true
	}
	return false
}

func (p *INGParser) Parse(records [][]string, accountID uuid.UUID) ([]Transaction, []RSUVest, error) {
	if len(records) < 2 {
		return nil, nil, fmt.Errorf("no data rows")
	}
	p.warnings = nil
	p.detectedAccountType = ""

	idx := headerIndex(records[0])
	if _, ok := idx["orderart"]; ok {
		return p.parseOrders(records, accountID, idx)
	}
	return p.parseHoldings(records, accountID)
}

func (p *INGParser) parseOrders(records [][]string, accountID uuid.UUID, idx map[string]int) ([]Transaction, []RSUVest, error) {
	p.detectedAccountType = "brokerage"

	orderTypeCol := findColumn(idx, "orderart")
	dateCol := findColumn(idx, "ausführungsdatum")
	orderNoCol := findColumn(idx, "ordernummer")
	isinCol := findColumn(idx, "isin")
	nameCol := findColumn(idx, "wertpapiername")
	qtyCol := findColumn(idx, "stück/nominale", "stueck/nominale", "stück", "stueck")
	priceCol := findColumn(idx, "ausführungskurs", "ausfuehrungskurs")
	currencyCol := findColumn(idx, "währung", "waehrung")
	statusCol := findColumn(idx, "status")

	if dateCol < 0 || isinCol < 0 || qtyCol < 0 || priceCol < 0 {
		return nil, nil, fmt.Errorf("missing required Ordermanager columns")
	}

	var txns []Transaction
	for i, record := range records[1:] {
		rowNum := i + 2
		if len(record) == 0 || (len(record) == 1 && record[0] == "") {
			continue
		}

		isin := getField(record, isinCol)
		if isin == "" {
			continue
		}

		if statusCol >= 0 {
			status := strings.ToLower(getField(record, statusCol))
			if status != "" && status != "ausgeführt" && status != "ausgefuehrt" {
				p.warnings = append(p.warnings, fmt.Sprintf("row %d: skipped (status=%s)", rowNum, status))
				continue
			}
		}

		dateStr := getField(record, dateCol)
		date, err := parseGermanDate(dateStr)
		if err != nil {
			p.warnings = append(p.warnings, fmt.Sprintf("row %d: skipped (invalid date %q)", rowNum, dateStr))
			continue
		}

		quantity, err := parseGermanDecimal(getField(record, qtyCol))
		if err != nil {
			p.warnings = append(p.warnings, fmt.Sprintf("row %d: invalid quantity", rowNum))
			continue
		}
		price, err := parseGermanDecimal(getField(record, priceCol))
		if err != nil {
			p.warnings = append(p.warnings, fmt.Sprintf("row %d: invalid price", rowNum))
			continue
		}

		currency := "EUR"
		if c := getField(record, currencyCol); c != "" {
			currency = c
		}

		txnType := classifyINGOrder(getField(record, orderTypeCol))
		orderNo := getField(record, orderNoCol)
		name := getField(record, nameCol)

		txns = append(txns, Transaction{
			AccountID:    accountID,
			Date:         date,
			Type:         txnType,
			SecurityISIN: isin,
			Quantity:     quantity,
			Price:        price,
			Amount:       quantity * price,
			Currency:     currency,
			Counterparty: name,
			Reference:    orderNo,
		})
	}
	return txns, nil, nil
}

func classifyINGOrder(orderart string) string {
	o := strings.ToLower(strings.TrimSpace(orderart))
	switch {
	case strings.HasPrefix(o, "kauf"), strings.Contains(o, "buy"):
		return "buy"
	case strings.HasPrefix(o, "verkauf"), strings.HasPrefix(o, "sell"):
		return "sell"
	}
	return "buy"
}

// parseHoldings synthesizes a buy transaction per holding at the cost basis,
// matching the convention of HoldingsTemplateParser. The Depotübersicht has
// four "Währung" columns at fixed offsets, so we read columns positionally
// rather than by name to avoid collisions in the header map.
func (p *INGParser) parseHoldings(records [][]string, accountID uuid.UUID) ([]Transaction, []RSUVest, error) {
	p.detectedAccountType = "brokerage"

	// Column layout for ING Depotübersicht (positions 0..16):
	//   0 ISIN, 1 Wertpapiername, 2 Stück/Nominale, 3 Einheitskennzeichen,
	//   4 Einstandskurs, 5 Währung, 6 Einstandswert, 7 Währung,
	//   8 Bewertungskurs, 9 Währung, 10 Zeit, 11 Handelsplatz,
	//   12 Kurswert, 13 Währung, 14 Gewinn/Verlust, 15 Währung, 16 Gewinn/Verlust (%)
	const (
		colISIN          = 0
		colName          = 1
		colQuantity      = 2
		colCostPrice     = 4
		colCostCurrency  = 5
		colCostValue     = 6
		colSnapshotDate  = 10
	)

	var txns []Transaction
	for i, record := range records[1:] {
		rowNum := i + 2
		if len(record) == 0 || (len(record) == 1 && record[0] == "") {
			continue
		}

		isin := getField(record, colISIN)
		if isin == "" {
			// Footer row "Depot-Gesamtwert" leaves ISIN empty; skip silently.
			continue
		}

		name := getField(record, colName)
		quantityStr := getField(record, colQuantity)
		costPriceStr := getField(record, colCostPrice)
		costValueStr := getField(record, colCostValue)

		// Positions without a cost basis (e.g. transferred-in shares marked
		// "n.a.") can't be seeded as buys — skip with a warning so the user
		// knows to enter them manually.
		if strings.EqualFold(costPriceStr, "n.a.") || costPriceStr == "" {
			p.warnings = append(p.warnings,
				fmt.Sprintf("row %d (%s): skipped (no cost basis)", rowNum, isin))
			continue
		}

		quantity, err := parseGermanDecimal(quantityStr)
		if err != nil || quantity == 0 {
			p.warnings = append(p.warnings, fmt.Sprintf("row %d (%s): invalid quantity", rowNum, isin))
			continue
		}
		price, err := parseGermanDecimal(costPriceStr)
		if err != nil {
			p.warnings = append(p.warnings, fmt.Sprintf("row %d (%s): invalid cost price", rowNum, isin))
			continue
		}
		amount, err := parseGermanDecimal(costValueStr)
		if err != nil || amount == 0 {
			// Fall back to quantity × price when the Einstandswert column is missing.
			amount = quantity * price
		}

		currency := getField(record, colCostCurrency)
		if currency == "" {
			currency = "EUR"
		}

		// Use the snapshot's "Zeit" as the synthetic acquisition date so
		// re-importing the same statement produces the same import_hash.
		date := defaultDate()
		if d, err := parseGermanDate(getField(record, colSnapshotDate)); err == nil {
			date = d
		}

		// Custom hash includes ISIN + quantity to keep two same-day snapshots
		// of different positions distinct.
		hashData := fmt.Sprintf("%s|%s|%s|%.8f|%.4f",
			accountID.String(), date.Format("2006-01-02"), isin, quantity, amount)
		sum := sha256.Sum256([]byte(hashData))

		txns = append(txns, Transaction{
			AccountID:    accountID,
			Date:         date,
			Type:         "buy",
			SecurityISIN: isin,
			Quantity:     quantity,
			Price:        price,
			Amount:       amount,
			Currency:     currency,
			Counterparty: name,
			Reference:    "ING Depotübersicht",
			ImportHash:   fmt.Sprintf("%x", sum),
		})
	}
	return txns, nil, nil
}
