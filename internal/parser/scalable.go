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
	p.warnings = nil
	p.detectedAccountType = ""

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
	var nonTrade []bool // parallel to txns: row is a non-cancelled non_trade_security_transaction
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

		quantity, qtyErr := parseStandardDecimal(getField(record, quantityCol))
		if qtyErr != nil {
			p.warnings = append(p.warnings, fmt.Sprintf("row %d: skipped (invalid quantity)", i+2))
			continue
		}
		// amount is signed in the CSV: negative = money out, positive = money in
		signedAmount, amtErr := parseStandardDecimal(getField(record, amountCol))
		if amtErr != nil {
			p.warnings = append(p.warnings, fmt.Sprintf("row %d: skipped (invalid amount)", i+2))
			continue
		}

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
		if signed, ok := scalableSignedType(typeRaw, subType, side, txnType, signedAmount, isCancellation); ok {
			// The booked sign already says which way the cash moved, for
			// cancellations too, so a sign-derived type is never reversed.
			txnType = signed
		} else if isCancellation {
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
		isCloseout := false
		if typeRaw == "non_trade_security_transaction" && !isCancellation {
			switch subType {
			case "knock_out", "knockout", "redemption", "tilgung", "maturity", "liquidation", "close_out", "worthless", "ausbuchung":
				isCloseout = true
			case "transfer_out":
				isCloseout = false
			default:
				// Scalable books knock-out/certificate close-outs under TRANSFER_IN or blank sub_type
				// with a negative nominal booking amount (e.g. -0.001 * qty) or on a derivative ISIN.
				// Genuine inbound ETF/stock transfers have signedAmount >= 0 and non-derivative class.
				if signedAmount < 0 || ClassifyAsset(isin, desc) == "derivative" {
					isCloseout = true
				}
			}
		}
		nonTrade = append(nonTrade, isCloseout)
	}

	return p.mergeCloseouts(txns, nonTrade), nil, nil
}

// mergeCloseouts folds knock-outs, redemptions and similar close-outs into a
// single sell. Scalable books these as a non-trade security row (unsigned
// quantity, nominal amount — indistinguishable from an inbound transfer) plus
// a same-day cash distribution for the same ISIN carrying the payout. Left
// alone they import as a phantom incoming transfer and a dividend, doubling
// the position and turning the realised loss into dividend income.
//
// The payout row becomes the sell and keeps its import hash, so re-importing
// over previously misclassified rows reclassifies it in place.
func (p *ScalableCapitalParser) mergeCloseouts(txns []Transaction, nonTrade []bool) []Transaction {
	type key struct {
		date string
		isin string
	}
	payouts := make(map[key][]int)
	for j, t := range txns {
		if t.Type == "dividend" && t.SecurityISIN != "" && t.Amount > 0 {
			k := key{t.Date.Format("2006-01-02"), t.SecurityISIN}
			payouts[k] = append(payouts[k], j)
		}
	}
	if len(payouts) == 0 {
		return txns
	}

	drop := make(map[int]bool)
	for i, t := range txns {
		if !nonTrade[i] || t.SecurityISIN == "" || t.Quantity <= 0 {
			continue
		}
		k := key{t.Date.Format("2006-01-02"), t.SecurityISIN}
		cands := payouts[k]
		if len(cands) == 0 {
			continue
		}
		j := cands[0]
		payouts[k] = cands[1:]

		txns[j].Type = "sell"
		txns[j].Quantity = t.Quantity
		txns[j].Price = txns[j].Amount / t.Quantity
		drop[i] = true
		p.warnings = append(p.warnings, fmt.Sprintf("%s %s: close-out (knock-out/redemption) of %g units booked as a sale for %.2f %s",
			k.date, t.SecurityISIN, t.Quantity, txns[j].Amount, txns[j].Currency))
	}
	if len(drop) == 0 {
		return txns
	}

	out := txns[:0]
	for i, t := range txns {
		if !drop[i] {
			out = append(out, t)
		}
	}
	return out
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

// scalableSignedType returns the stored type for rows whose direction only
// the sign of the booked amount carries (amounts are stored as magnitudes,
// so a type that ignores the sign turns a refund into a charge or back).
//
//   - TAX rows: a positive amount is a tax refund (deposit), a negative one a
//     tax payment (tax). Mirrors the Sparkasse fee/interest handling.
//   - Unknown cash sub-types, unknown row types and PRIME+ rows without a
//     side: money in is a deposit, money out a withdrawal.
//   - Regular cash rows whose sign contradicts the sub-type (not
//     cancellations, which flip the sign on purpose): negative interest or
//     distributions are charges (fee), a negative deposit is a withdrawal
//     and a positive withdrawal a deposit.
//
// ok is false when the sub-type alone decides the type.
func scalableSignedType(typeRaw, subType, side, base string, signed float64, cancellation bool) (string, bool) {
	bySign := func() string {
		if signed < 0 {
			return "withdrawal"
		}
		return "deposit"
	}
	taxBySign := func() string {
		if signed > 0 {
			return "deposit"
		}
		return "tax"
	}

	if typeRaw == "" && subType == "" {
		// PRIME+ native format: classified from the side column.
		switch side {
		case "tax", "steuer":
			return taxBySign(), true
		case "":
			return bySign(), true
		}
		return "", false
	}

	switch typeRaw {
	case "security_transaction", "non_trade_security_transaction":
		return "", false
	case "cash_transaction":
		switch subType {
		case "tax", "steuer":
			return taxBySign(), true
		case "deposit", "withdrawal", "distribution", "dividend", "dividende", "interest", "zinsen",
			"cash_transfer_out", "cash_transfer_in":
			if cancellation {
				return "", false
			}
			switch {
			case (base == "interest" || base == "dividend") && signed < 0:
				return "fee", true
			case base == "deposit" && signed < 0:
				return "withdrawal", true
			case base == "withdrawal" && signed > 0:
				return "deposit", true
			}
			return "", false
		default:
			return bySign(), true
		}
	default:
		return bySign(), true
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
