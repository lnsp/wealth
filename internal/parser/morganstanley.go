package parser

import (
	"fmt"
	"math/big"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

// MorganStanleyParser handles four CSV exports from Morgan Stanley At Work
// (Google RSU plan): Releases Report, Releases Net Shares Report, Withdrawals
// Report, and Unvested GSUs As At Date.
//
// Releases and Net Shares both describe the same vest events: the Releases
// Report carries gross + net shares per grant, the Net Shares Report carries
// only the net count. Each vest emits a transfer (in-kind share grant — no
// cash leg, since RSUs are granted, not bought). The shared Reference value
// lets the hash match so re-imports across the two files are no-ops.
//
// The Withdrawals Report contains two row flavours: GSU sales (negative
// quantity, positive net amount) and Cash wire-outs (negative quantity, plan
// "Cash"). Each share sale emits a sell (gross proceeds plus the deducted fee,
// see msSaleGrossAndFee) + a withdrawal of the net amount so the cash
// proceeds are immediately treated as auto-wired (MS RSU accounts are modeled
// as securities-only — no cash sits here). Cash wire-out rows are skipped to
// avoid double-counting the same flow.
//
// Unvested rows write to rsu_vests with vested=false; no transaction is
// emitted until the share actually vests.
type MorganStanleyParser struct{}

func (p *MorganStanleyParser) Institution() string { return "morgan_stanley" }

func (p *MorganStanleyParser) Detect(header []string) bool {
	norm := normalizeHeader(header)
	counts := map[string]int{}
	for _, h := range norm {
		counts[h]++
	}
	// Releases / Net Shares / Withdrawals all share these columns.
	if counts["plan"] > 0 && counts["order number"] > 0 && counts["tax payment method"] > 0 {
		return true
	}
	// Unvested schedule.
	if counts["plan name"] > 0 && counts["employee grant number"] > 0 && counts["vesting date"] > 0 {
		return true
	}
	return false
}

func (p *MorganStanleyParser) Parse(records [][]string, accountID uuid.UUID) ([]Transaction, []RSUVest, error) {
	if len(records) < 2 {
		return nil, nil, fmt.Errorf("no data rows")
	}
	norm := normalizeHeader(records[0])
	counts := map[string]int{}
	for _, h := range norm {
		counts[h]++
	}
	switch {
	case counts["plan name"] > 0 && counts["employee grant number"] > 0:
		return p.parseUnvested(records, accountID)
	case headerContains(norm, "execution date"):
		return p.parseWithdrawals(records, accountID)
	case headerContains(norm, "vest date"):
		return p.parseReleases(records, accountID, true)
	case counts["net share proceeds"] >= 2:
		return p.parseReleases(records, accountID, false)
	default:
		return nil, nil, fmt.Errorf("unsupported morgan stanley CSV variant: %v", records[0])
	}
}

type pendingRelease struct {
	date     time.Time
	netQty   float64
	grossQty float64 // 0 when not present (Net Shares Report)
	price    float64
	plan     string
	order    string
}

// parseReleases handles both "Releases Report" (hasGross=true) and "Releases
// Net Shares Report" (hasGross=false). Both produce buy transactions for the
// net share count; only hasGross=true emits RSUVest rows.
func (p *MorganStanleyParser) parseReleases(records [][]string, accountID uuid.UUID, hasGross bool) ([]Transaction, []RSUVest, error) {
	idx := headerIndex(records[0])

	dateCol := findColumn(idx, "vest date", "date")
	orderCol := findColumn(idx, "order number")
	planCol := findColumn(idx, "plan")
	statusCol := findColumn(idx, "status", "order status")
	priceCol := findColumn(idx, "price")

	if dateCol < 0 || priceCol < 0 {
		return nil, nil, fmt.Errorf("missing required columns: date and/or price")
	}

	var grossQtyCol, netQtyCol int
	if hasGross {
		grossQtyCol = findColumn(idx, "quantity")
		netQtyCol = findColumn(idx, "net share proceeds")
	} else {
		// Net Shares Report has Quantity = net shares; the duplicate "Net Share
		// Proceeds" columns are zero cash leftovers.
		netQtyCol = findColumn(idx, "quantity")
		grossQtyCol = -1
	}
	if netQtyCol < 0 {
		return nil, nil, fmt.Errorf("missing share-count column")
	}

	var pending []pendingRelease
	for i, record := range records[1:] {
		if isBlankOrFooter(record) {
			continue
		}
		if statusCol >= 0 {
			status := strings.ToLower(getField(record, statusCol))
			if status != "" && status != "completed" && status != "complete" {
				continue
			}
		}
		dateStr := getField(record, dateCol)
		if dateStr == "" {
			continue
		}
		date, err := parseMSDate(dateStr)
		if err != nil {
			return nil, nil, fmt.Errorf("row %d: parse date %q: %w", i+2, dateStr, err)
		}
		netQty := parseMoneyOrFloat(getField(record, netQtyCol))
		if netQty == 0 {
			continue
		}
		var grossQty float64
		if grossQtyCol >= 0 {
			grossQty = parseMoneyOrFloat(getField(record, grossQtyCol))
		}
		price := parseMoneyOrFloat(getField(record, priceCol))
		pending = append(pending, pendingRelease{
			date: date, netQty: netQty, grossQty: grossQty,
			price: price,
			plan:  normalizePlan(getField(record, planCol)),
			order: getField(record, orderCol),
		})
	}

	// Sort by (date, netQty, grossQty) so the position counter is deterministic
	// regardless of source file row order. The Releases Report lists vests
	// chronologically while the Net Shares Report lists them in reverse; this
	// sort makes the per-row Reference (and therefore the import_hash) match.
	sort.SliceStable(pending, func(a, b int) bool {
		if !pending[a].date.Equal(pending[b].date) {
			return pending[a].date.Before(pending[b].date)
		}
		if pending[a].netQty != pending[b].netQty {
			return pending[a].netQty < pending[b].netQty
		}
		return pending[a].grossQty < pending[b].grossQty
	})

	dateCount := map[string]int{}
	var txns []Transaction
	var vests []RSUVest
	for _, r := range pending {
		dateKey := r.date.Format("2006-01-02")
		dateCount[dateKey]++
		pos := dateCount[dateKey]
		ref := fmt.Sprintf("ms-release-%s-%d", dateKey, pos)
		txns = append(txns, Transaction{
			AccountID:    accountID,
			Date:         r.date,
			Type:         "transfer",
			Quantity:     r.netQty,
			Price:        r.price,
			Amount:       r.netQty * r.price,
			Currency:     "USD",
			Counterparty: r.plan,
			Reference:    ref,
		})
		if r.grossQty > 0 {
			vests = append(vests, RSUVest{
				AccountID:           accountID,
				VestDate:            r.date,
				GrantNumber:         r.order,
				GrossQuantity:       r.grossQty,
				NetQuantity:         r.netQty,
				Price:               r.price,
				Currency:            "USD",
				Vested:              true,
				LinkTransactionHash: ref,
				ImportHash:          fmt.Sprintf("ms-vest-%s-%s-%d", accountID, dateKey, pos),
			})
		}
	}
	return txns, vests, nil
}

func (p *MorganStanleyParser) parseWithdrawals(records [][]string, accountID uuid.UUID) ([]Transaction, []RSUVest, error) {
	idx := headerIndex(records[0])
	dateCol := findColumn(idx, "execution date")
	orderCol := findColumn(idx, "order number")
	planCol := findColumn(idx, "plan")
	statusCol := findColumn(idx, "order status", "status")
	priceCol := findColumn(idx, "price")
	qtyCol := findColumn(idx, "quantity")
	amountCol := findColumn(idx, "net amount", "net cash proceeds")
	if dateCol < 0 || amountCol < 0 {
		return nil, nil, fmt.Errorf("missing required columns: execution date and/or net amount")
	}

	var txns []Transaction
	for i, record := range records[1:] {
		if isBlankOrFooter(record) {
			continue
		}
		if statusCol >= 0 {
			status := strings.ToLower(getField(record, statusCol))
			if status != "" && status != "complete" && status != "completed" {
				continue
			}
		}
		dateStr := getField(record, dateCol)
		if dateStr == "" {
			continue
		}
		date, err := parseMSDate(dateStr)
		if err != nil {
			return nil, nil, fmt.Errorf("row %d: parse date %q: %w", i+2, dateStr, err)
		}
		order := getField(record, orderCol)
		plan := normalizePlan(getField(record, planCol))
		amount := abs(parseMoneyOrFloat(getField(record, amountCol)))

		if strings.EqualFold(plan, "cash") {
			// Cash wire-outs are modeled implicitly as auto-wires of each sale (below);
			// importing the explicit cash rows would double-count.
			continue
		}

		price := parseMoneyOrFloat(getField(record, priceCol))
		qty := abs(parseMoneyOrFloat(getField(record, qtyCol)))
		gross, fee := msSaleGrossAndFee(qty, price, amount)

		txns = append(txns, Transaction{
			AccountID:    accountID,
			Date:         date,
			Type:         "sell",
			Quantity:     qty,
			Price:        price,
			Amount:       gross,
			Fee:          fee,
			Currency:     "USD",
			Counterparty: plan,
			Reference:    order,
			ImportHash:   fmt.Sprintf("ms-sell-%s-%s", accountID, order),
		})
		// Auto-wire the net proceeds so the MS account stays cash-neutral
		// (sell gross − fee − wire = 0). The wire carries no quantity or price;
		// the importer stores both as NULL.
		txns = append(txns, Transaction{
			AccountID:    accountID,
			Date:         date,
			Type:         "withdrawal",
			Amount:       amount,
			Currency:     "USD",
			Counterparty: "Morgan Stanley (auto-wire of " + plan + " proceeds)",
			Reference:    order + "-wire",
			ImportHash:   fmt.Sprintf("ms-wire-%s-%s", accountID, order),
		})
		_ = i
	}
	return txns, nil, nil
}

func (p *MorganStanleyParser) parseUnvested(records [][]string, accountID uuid.UUID) ([]Transaction, []RSUVest, error) {
	idx := headerIndex(records[0])
	grantCol := findColumn(idx, "employee grant number")
	dateCol := findColumn(idx, "vesting date")
	qtyCol := findColumn(idx, "total quantity")
	if grantCol < 0 || dateCol < 0 || qtyCol < 0 {
		return nil, nil, fmt.Errorf("missing required unvested columns")
	}
	var vests []RSUVest
	for i, record := range records[1:] {
		if isBlankOrFooter(record) {
			continue
		}
		grant := getField(record, grantCol)
		if grant == "" {
			continue
		}
		dateStr := getField(record, dateCol)
		date, err := parseMSDate(dateStr)
		if err != nil {
			return nil, nil, fmt.Errorf("row %d: parse date %q: %w", i+2, dateStr, err)
		}
		gross := parseMoneyOrFloat(getField(record, qtyCol))
		if gross == 0 {
			continue
		}
		vests = append(vests, RSUVest{
			AccountID:     accountID,
			VestDate:      date,
			GrantNumber:   grant,
			GrossQuantity: gross,
			Currency:      "USD",
			Vested:        false,
			ImportHash: fmt.Sprintf("ms-unvested-%s-%s-%s",
				accountID, grant, date.Format("2006-01-02")),
		})
	}
	return nil, vests, nil
}

// msSaleGrossAndFee splits a Withdrawals Report sale into gross proceeds
// (quantity × price, rounded to cents) and the fee Morgan Stanley deducted
// (gross − net amount). The report only carries the net amount; storing it
// as the sale amount with no fee hid the commission and made "N shares at
// price" disagree with the amount shown next to it.
//
// The product is computed exactly on the 8-decimal quantity and price the
// database stores and rounded half away from zero, so it equals
// ROUND(quantity * price, 2) in SQL (migration 032 rewrites rows imported
// before this split the same way, which keeps similarity-based dedup working
// for re-imports). When the gross does not exceed the net amount (rounding
// noise in the export) the sale keeps the net amount and no fee.
func msSaleGrossAndFee(qty, price, net float64) (gross, fee float64) {
	q, okQ := new(big.Rat).SetString(fmt.Sprintf("%.8f", qty))
	p, okP := new(big.Rat).SetString(fmt.Sprintf("%.8f", price))
	n, okN := new(big.Rat).SetString(fmt.Sprintf("%.4f", net))
	if !okQ || !okP || !okN || qty <= 0 || price <= 0 {
		return net, 0
	}
	g := roundRatHalfAway(new(big.Rat).Mul(q, p), 2)
	if g.Cmp(n) <= 0 {
		return net, 0
	}
	gf, _ := g.Float64()
	ff, _ := new(big.Rat).Sub(g, n).Float64()
	return gf, ff
}

// roundRatHalfAway rounds r to `places` decimals, half away from zero (the
// rounding PostgreSQL's ROUND(numeric, n) uses).
func roundRatHalfAway(r *big.Rat, places int) *big.Rat {
	scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(places)), nil)
	scaled := new(big.Rat).Mul(r, new(big.Rat).SetInt(scale))
	num := new(big.Int).Abs(scaled.Num())
	den := scaled.Denom()
	twoNum := new(big.Int).Add(new(big.Int).Lsh(num, 1), den)
	q := new(big.Int).Quo(twoNum, new(big.Int).Lsh(den, 1))
	if scaled.Sign() < 0 {
		q.Neg(q)
	}
	return new(big.Rat).SetFrac(q, scale)
}

// normalizePlan trims surrounding whitespace and parentheses so the same plan
// renders identically regardless of cosmetic export drift (MS exports the same
// plan as "GSU Class C" in some reports and "(GSU Class C)" in others).
func normalizePlan(s string) string {
	s = strings.TrimSpace(s)
	for strings.HasPrefix(s, "(") && strings.HasSuffix(s, ")") {
		s = strings.TrimSpace(s[1 : len(s)-1])
	}
	return s
}

// parseMSDate parses Morgan Stanley's DD-Mon-YYYY format (e.g. "25-Dec-2025").
func parseMSDate(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	t, err := time.Parse("02-Jan-2006", s)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse MS date %q: %w", s, err)
	}
	return t, nil
}

// parseMoneyOrFloat strips $ and parentheses-as-negative notation, then defers to
// parseStandardDecimal (which already strips thousands commas).
func parseMoneyOrFloat(s string) float64 {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "$", "")
	if strings.HasPrefix(s, "(") && strings.HasSuffix(s, ")") {
		s = "-" + strings.TrimSuffix(strings.TrimPrefix(s, "("), ")")
	}
	f, _ := parseStandardDecimal(s)
	return f
}

func isBlankOrFooter(record []string) bool {
	nonBlank := 0
	var first string
	for _, c := range record {
		v := strings.TrimSpace(c)
		if v != "" {
			nonBlank++
			if first == "" {
				first = v
			}
		}
	}
	if nonBlank == 0 {
		return true
	}
	// Footer disclaimer lines may contain commas and split into many "fields";
	// detect them by first-field prefix instead of relying on cell count.
	lower := strings.ToLower(first)
	if strings.HasPrefix(lower, "please note") || strings.HasPrefix(lower, "the numbers") {
		return true
	}
	return false
}

func headerContains(norm []string, target string) bool {
	for _, h := range norm {
		if h == target {
			return true
		}
	}
	return false
}
