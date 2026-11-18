package analytics

import (
	"encoding/binary"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/google/uuid"
)

// The tax ledger is the single FIFO engine behind the tax summary, the loss
// pots, the open tax lots and the sell simulator. It replays chronologically
// ordered TaxTransactions per depot (AccountID, ISIN) and records:
//
//   - realised sales with cost basis, the Vorabpauschalen already taxed on the
//     sold units (§19 Abs. 1 S. 3 InvStG) and, for §23 assets, the split
//     between lots held up to one year and longer;
//   - capital income events (dividends, interest, Vorabpauschale), dated to
//     the year they are taxed in;
//   - the open lots that remain, with their original acquisition dates.
//
// Matched in-kind depot transfers (transfer_out → transfer) move the original
// lots — quantity, cost and acquisition date — to the receiving depot
// (§43a Abs. 2 S. 3 EStG); they never realise a gain or loss.

// RealizedSale is one sell transaction matched against per-depot FIFO lots.
type RealizedSale struct {
	Year                   int
	Date                   string
	AccountID              string
	ISIN                   string
	Name                   string
	AssetClass             string
	Quantity               float64
	Proceeds               float64
	CostBasis              float64
	VorabpauschaleDeducted float64 // Vorabpauschalen already taxed on the sold units (§19 Abs. 1 S. 3 InvStG)
	PL                     float64 // Proceeds − CostBasis − VorabpauschaleDeducted, before Teilfreistellung
	TFS                    float64
	IsEquityFund           bool
	PrivateSale            bool    // §23 EStG asset (crypto, physically backed commodity)
	ShortTermPL            float64 // §23 only: P/L on lots held one year or less (taxable)
	LongTermPL             float64 // §23 only: P/L on lots held more than one year (tax-free)
}

// TaxablePL is the sale's P/L after Teilfreistellung (applied to gains and losses alike, §20 InvStG).
func (s RealizedSale) TaxablePL() float64 { return s.PL * (1 - s.TFS) }

// IncomeEvent is a dividend, interest payment or Vorabpauschale, dated to the
// year it is taxed in.
type IncomeEvent struct {
	Year               int // year of receipt (Zufluss); for a Vorabpauschale the year after it was computed
	AccountID          string
	ISIN               string
	Name               string
	AssetClass         string
	Kind               string  // "dividend", "interest" or "vorabpauschale"
	Amount             float64 // gross, before Teilfreistellung
	TFS                float64
	PrivateSale        bool // paid on a §23 asset (e.g. staking rewards): not §20 capital income
	VorabpauschaleYear int  // Kind "vorabpauschale": the calendar year it was computed for
}

// TaxableAmount is the income after Teilfreistellung.
func (e IncomeEvent) TaxableAmount() float64 { return e.Amount * (1 - e.TFS) }

type ledgerKey struct{ acct, isin string }

type ledgerLot struct {
	acqDate    time.Time
	qty        float64
	costPer    float64
	vpPerUnit  float64 // Vorabpauschalen already taxed per unit
	tfs        float64
	hasTFS     bool
	assetClass string
}

type ledgerVP struct {
	year  int
	entry VorabpauschaleEntry
}

type taxLedger struct {
	lots        map[ledgerKey][]ledgerLot
	keyOrder    []ledgerKey
	seen        map[ledgerKey]bool
	sales       []RealizedSale
	income      []IncomeEvent
	vp          []ledgerVP
	names       map[string]string
	distPerUnit map[string]map[string]float64 // ISIN → date → distribution per unit
}

// RealizedTaxEvents replays the transactions and returns every realised sale
// and income event. Use it for per-account or per-year breakdowns that must
// agree with ComputeTaxSummary and ComputeLossPots.
func RealizedTaxEvents(txns []TaxTransaction) ([]RealizedSale, []IncomeEvent) {
	l := runTaxLedger(txns)
	return l.sales, l.income
}

func txnTime(t TaxTransaction) time.Time {
	if t.Date != "" {
		if d, err := time.Parse("2006-01-02", t.Date); err == nil {
			return d
		}
	}
	y := t.Year
	if y == 0 {
		y = 1970
	}
	return time.Date(y, 1, 1, 0, 0, 0, 0, time.UTC)
}

// HeldOverOneYear reports whether a sale on `sale` of an asset acquired on
// `acq` falls outside the one-year speculation period of §23 Abs. 1 Nr. 2
// EStG. The period ends on the anniversary of the acquisition (§108 AO,
// §188 Abs. 2 BGB), so a sale on the anniversary itself is still taxable;
// an acquisition on 29 February ends on 28 February (§188 Abs. 3 BGB).
func HeldOverOneYear(acq, sale time.Time) bool {
	return sale.After(privateSaleAnniversary(acq))
}

// PrivateSaleTaxFreeFrom is the first day a §23 asset acquired on acq can be sold tax-free.
func PrivateSaleTaxFreeFrom(acq time.Time) time.Time {
	return privateSaleAnniversary(acq).AddDate(0, 0, 1)
}

func privateSaleAnniversary(acq time.Time) time.Time {
	y, m, d := acq.Date()
	if m == time.February && d == 29 {
		return time.Date(y+1, time.February, 28, 0, 0, 0, 0, acq.Location())
	}
	return time.Date(y+1, m, d, 0, 0, 0, 0, acq.Location())
}

// vorabpauschaleProRata is the share of a full year's Vorabpauschale due on a
// unit acquired on acq (§18 Abs. 2 InvStG: minus 1/12 for every full month
// before the month of acquisition).
func vorabpauschaleProRata(acq time.Time, year int) float64 {
	switch {
	case acq.Year() < year:
		return 1
	case acq.Year() == year:
		return float64(13-int(acq.Month())) / 12
	default:
		return 0
	}
}

// pairInKindTransfers matches depot-to-depot moves with MatchInKindTransferPairs
// and returns, for every matched row index, the index of its counterpart.
func pairInKindTransfers(txns []TaxTransaction) map[int]int {
	idxID := func(i int) uuid.UUID {
		var u uuid.UUID
		binary.BigEndian.PutUint64(u[8:], uint64(i)+1)
		return u
	}
	var candidates []InKindTransfer
	for i, t := range txns {
		if t.ISIN == "" || t.Quantity <= 0 || (t.Type != "transfer" && t.Type != "transfer_out") {
			continue
		}
		candidates = append(candidates, InKindTransfer{ID: idxID(i), Date: txnTime(t), Type: t.Type, ISIN: t.ISIN, Quantity: t.Quantity})
	}
	if len(candidates) == 0 {
		return nil
	}
	matched := MatchInKindTransferPairs(candidates)
	if len(matched) == 0 {
		return nil
	}

	// MatchInKindTransferPairs returns the matched set; pair within it using
	// the same rule (same ISIN and quantity, ±5 days, closest first),
	// preferring a counterpart in a different depot.
	var outs, ins []int
	for i, t := range txns {
		if t.ISIN == "" || t.Quantity <= 0 {
			continue
		}
		if _, ok := matched[idxID(i)]; !ok {
			continue
		}
		if t.Type == "transfer_out" {
			outs = append(outs, i)
		} else if t.Type == "transfer" {
			ins = append(ins, i)
		}
	}
	sort.SliceStable(outs, func(a, b int) bool { return txnTime(txns[outs[a]]).Before(txnTime(txns[outs[b]])) })

	partner := make(map[int]int)
	used := make(map[int]bool)
	for _, o := range outs {
		out := txns[o]
		best, bestGap, bestOther := -1, time.Duration(math.MaxInt64), false
		for _, in := range ins {
			if used[in] {
				continue
			}
			t := txns[in]
			if t.ISIN != out.ISIN || math.Abs(t.Quantity-out.Quantity) > 1e-4 {
				continue
			}
			gap := txnTime(t).Sub(txnTime(out))
			if gap < 0 {
				gap = -gap
			}
			if gap > 5*24*time.Hour {
				continue
			}
			other := t.AccountID != out.AccountID
			if best < 0 || (other && !bestOther) || (other == bestOther && gap < bestGap) {
				best, bestGap, bestOther = in, gap, other
			}
		}
		if best >= 0 {
			used[best] = true
			partner[o] = best
			partner[best] = o
		}
	}
	return partner
}

func runTaxLedger(txns []TaxTransaction) *taxLedger {
	l := &taxLedger{
		lots:        make(map[ledgerKey][]ledgerLot),
		seen:        make(map[ledgerKey]bool),
		names:       make(map[string]string),
		distPerUnit: make(map[string]map[string]float64),
	}
	partner := pairInKindTransfers(txns)
	handled := make(map[int]bool)

	for i, txn := range txns {
		if txn.ISIN != "" && txn.Name != "" && l.names[txn.ISIN] == "" {
			l.names[txn.ISIN] = txn.Name
		}
		k := ledgerKey{acct: txn.AccountID, isin: txn.ISIN}
		switch txn.Type {
		case "buy", "savings_plan", "transfer":
			if txn.ISIN == "" || txn.Quantity <= 0 {
				continue
			}
			if j, ok := partner[i]; ok {
				if !handled[i] {
					l.moveInKind(txns[j], txn)
					handled[i], handled[j] = true, true
				}
				continue
			}
			// Purchases and unmatched inbound transfers (e.g. RSU vests at
			// fair market value) open a new lot.
			l.register(k)
			l.insertByDate(k, ledgerLot{
				acqDate:    txnTime(txn),
				qty:        txn.Quantity,
				costPer:    txn.Amount / txn.Quantity,
				tfs:        txn.TFS,
				hasTFS:     txn.HasExplicitTFS || txn.TFS > 0,
				assetClass: txn.AssetClass,
			})

		case "transfer_out":
			if txn.ISIN == "" || txn.Quantity <= 0 {
				continue
			}
			if j, ok := partner[i]; ok {
				if !handled[i] {
					l.moveInKind(txn, txns[j])
					handled[i], handled[j] = true, true
				}
				continue
			}
			// Unmatched outbound transfer (gift, untracked depot): the lots
			// leave the ledger without realising anything.
			l.consume(k, txn.Quantity)

		case "sell":
			if txn.ISIN == "" || txn.Quantity <= 0 {
				continue
			}
			l.sell(txn)

		case "dividend":
			l.income = append(l.income, IncomeEvent{
				Year: txn.Year, AccountID: txn.AccountID, ISIN: txn.ISIN, Name: txn.Name, AssetClass: txn.AssetClass,
				Kind: "dividend", Amount: txn.Amount, TFS: txnTFSRate(txn),
				PrivateSale: IsPrivateSaleAssetClass(txn.AssetClass),
			})
			if txn.ISIN != "" {
				if held := l.heldQty(k); held > 1e-9 {
					if l.distPerUnit[txn.ISIN] == nil {
						l.distPerUnit[txn.ISIN] = make(map[string]float64)
					}
					// The same distribution per unit applies in every depot; keep one value per date.
					l.distPerUnit[txn.ISIN][txnTime(txn).Format("2006-01-02")] = txn.Amount / held
				}
			}

		case "interest":
			// Interest paid (overdraft, negative deposit rates) is not
			// deductible (§20 Abs. 9 S. 1 EStG), so only receipts count.
			if txn.Amount <= 0 {
				continue
			}
			l.income = append(l.income, IncomeEvent{
				Year: txn.Year, AccountID: txn.AccountID, ISIN: txn.ISIN, Name: txn.Name, AssetClass: txn.AssetClass,
				Kind: "interest", Amount: txn.Amount,
				PrivateSale: IsPrivateSaleAssetClass(txn.AssetClass),
			})

		case "vorabpauschale":
			l.applyVorabpauschale(txn)
		}
	}
	return l
}

func (l *taxLedger) register(k ledgerKey) {
	if !l.seen[k] {
		l.seen[k] = true
		l.keyOrder = append(l.keyOrder, k)
	}
}

// insertByDate appends a lot, keeping the depot's queue ordered by acquisition
// date so transferred-in lots are sold in FIFO order of their original purchase.
func (l *taxLedger) insertByDate(k ledgerKey, lot ledgerLot) {
	q := l.lots[k]
	pos := len(q)
	for pos > 0 && q[pos-1].acqDate.After(lot.acqDate) {
		pos--
	}
	q = append(q, ledgerLot{})
	copy(q[pos+1:], q[pos:])
	q[pos] = lot
	l.lots[k] = q
}

// consume removes qty units from the depot's oldest lots and returns the
// removed portions.
func (l *taxLedger) consume(k ledgerKey, qty float64) []ledgerLot {
	var taken []ledgerLot
	rem := qty
	q := l.lots[k]
	for rem > 1e-9 && len(q) > 0 {
		lot := q[0]
		if lot.qty <= rem+1e-9 {
			taken = append(taken, lot)
			rem -= lot.qty
			q = q[1:]
		} else {
			part := lot
			part.qty = rem
			taken = append(taken, part)
			q[0].qty -= rem
			rem = 0
		}
	}
	l.lots[k] = q
	return taken
}

func (l *taxLedger) heldQty(k ledgerKey) float64 {
	sum := 0.0
	for _, lot := range l.lots[k] {
		sum += lot.qty
	}
	return sum
}

// moveInKind moves the original lots from the sending depot to the receiving
// one. If the sending depot's history is incomplete, the missing units are
// booked at the receiving row's recorded value and date.
func (l *taxLedger) moveInKind(out, in TaxTransaction) {
	src := ledgerKey{acct: out.AccountID, isin: out.ISIN}
	dst := ledgerKey{acct: in.AccountID, isin: in.ISIN}
	if src == dst {
		return
	}
	l.register(dst)
	moved := 0.0
	for _, lot := range l.consume(src, out.Quantity) {
		l.insertByDate(dst, lot)
		moved += lot.qty
	}
	if short := in.Quantity - moved; short > 1e-6 {
		costPer := 0.0
		if in.Quantity > 0 {
			costPer = in.Amount / in.Quantity
		}
		l.insertByDate(dst, ledgerLot{
			acqDate:    txnTime(in),
			qty:        short,
			costPer:    costPer,
			tfs:        in.TFS,
			hasTFS:     in.HasExplicitTFS || in.TFS > 0,
			assetClass: in.AssetClass,
		})
	}
}

func (l *taxLedger) sell(txn TaxTransaction) {
	k := ledgerKey{acct: txn.AccountID, isin: txn.ISIN}
	taken := l.consume(k, txn.Quantity)
	soldQty, cost, vpDed := 0.0, 0.0, 0.0
	for _, lot := range taken {
		soldQty += lot.qty
		cost += lot.qty * lot.costPer
		vpDed += lot.qty * lot.vpPerUnit
	}
	if soldQty <= 1e-9 {
		return
	}
	proceeds := txn.Amount
	if soldQty < txn.Quantity {
		proceeds = txn.Amount * (soldQty / txn.Quantity)
	}
	tfs := txnTFSRate(txn)
	sale := RealizedSale{
		Year:                   txn.Year,
		Date:                   txn.Date,
		AccountID:              txn.AccountID,
		ISIN:                   txn.ISIN,
		Name:                   txn.Name,
		AssetClass:             txn.AssetClass,
		Quantity:               soldQty,
		Proceeds:               proceeds,
		CostBasis:              cost,
		VorabpauschaleDeducted: vpDed,
		PL:                     proceeds - cost - vpDed,
		TFS:                    tfs,
		IsEquityFund:           txn.IsEquityFund || tfs >= TeilfreistellungEquity,
		PrivateSale:            IsPrivateSaleAssetClass(txn.AssetClass),
	}
	if sale.PrivateSale {
		saleDate := txnTime(txn)
		for _, lot := range taken {
			pl := proceeds*lot.qty/soldQty - lot.qty*lot.costPer - lot.qty*lot.vpPerUnit
			if HeldOverOneYear(lot.acqDate, saleDate) {
				sale.LongTermPL += pl
			} else {
				sale.ShortTermPL += pl
			}
		}
	}
	l.sales = append(l.sales, sale)
}

// applyVorabpauschale books the Vorabpauschale for the calendar year evt.Year
// on every unit of evt.ISIN still held at year end (§18 InvStG):
//
//	Basisertrag/unit = redemption price at the start of the year × Basiszins × 0.7
//	capped at (price at year end − price at start + distributions), minus distributions,
//	then × (13 − month of acquisition)/12 for units bought during the year.
//
// It is deemed received on the first working day of the next year (§18 Abs. 3),
// so the income event is dated evt.Year+1, and it is remembered per lot so a
// later sale deducts it from the gain (§19 Abs. 1 S. 3).
func (l *taxLedger) applyVorabpauschale(evt TaxTransaction) {
	bz, ok := BasiszinsByYear[evt.Year]
	if !ok || bz <= 0 || evt.ISIN == "" || IsPrivateSaleAssetClass(evt.AssetClass) {
		return
	}
	var keys []ledgerKey
	totalQty, totalCost := 0.0, 0.0
	for _, k := range l.keyOrder {
		if k.isin != evt.ISIN {
			continue
		}
		keys = append(keys, k)
		for _, lot := range l.lots[k] {
			totalQty += lot.qty
			totalCost += lot.qty * lot.costPer
		}
	}
	if totalQty <= 1e-9 {
		return
	}
	p0 := evt.VPJan1Price
	if p0 <= 0 {
		p0 = totalCost / totalQty // no price at the start of the year: fall back to cost per unit
	}
	p1 := evt.VPYearEndPrice
	if p0 <= 0 || p1 <= 0 {
		return
	}
	dist := 0.0
	prefix := fmt.Sprintf("%04d-", evt.Year)
	for date, d := range l.distPerUnit[evt.ISIN] {
		if len(date) >= 5 && date[:5] == prefix {
			dist += d
		}
	}
	basisUnit := p0 * bz / 100 * 0.7
	capUnit := math.Max(0, p1-p0+dist)
	vpUnit := math.Max(0, math.Min(basisUnit, capUnit)-dist)
	tfs := txnTFSRate(evt)

	total := 0.0
	byAcct := make(map[string]float64)
	var acctOrder []string
	for _, k := range keys {
		q := l.lots[k]
		for i := range q {
			f := vorabpauschaleProRata(q[i].acqDate, evt.Year)
			if f <= 0 || q[i].qty <= 1e-9 {
				continue
			}
			amt := q[i].qty * vpUnit * f
			q[i].vpPerUnit += vpUnit * f
			total += amt
			if _, ok := byAcct[k.acct]; !ok {
				acctOrder = append(acctOrder, k.acct)
			}
			byAcct[k.acct] += amt
		}
	}
	name := evt.Name
	if name == "" {
		name = l.names[evt.ISIN]
	}
	for _, acct := range acctOrder {
		if byAcct[acct] <= 0 {
			continue
		}
		l.income = append(l.income, IncomeEvent{
			Year: evt.Year + 1, AccountID: acct, ISIN: evt.ISIN, Name: name, AssetClass: evt.AssetClass,
			Kind: "vorabpauschale", Amount: byAcct[acct], TFS: tfs, VorabpauschaleYear: evt.Year,
		})
	}
	l.vp = append(l.vp, ledgerVP{year: evt.Year, entry: VorabpauschaleEntry{
		Year:           evt.Year,
		ISIN:           evt.ISIN,
		Name:           name,
		Units:          totalQty,
		Jan1Price:      p0,
		YearEndPrice:   p1,
		Jan1Value:      totalQty * p0,
		YearEndValue:   totalQty * p1,
		Basiszins:      bz,
		Basisertrag:    totalQty * basisUnit,
		Distributions:  totalQty * dist,
		Vorabpauschale: total,
		TFSRate:        tfs,
		TaxableVP:      total * (1 - tfs),
	}})
}

// MergeVorabpauschaleEvents inserts Vorabpauschale pseudo-transactions
// (Type "vorabpauschale", dated 31 December of their year) into a
// chronologically ordered transaction list, after every transaction on or
// before that date.
func MergeVorabpauschaleEvents(txns, events []TaxTransaction) []TaxTransaction {
	if len(events) == 0 {
		return txns
	}
	key := func(t TaxTransaction) string {
		if t.Date != "" {
			return t.Date
		}
		return fmt.Sprintf("%04d-00-00", t.Year)
	}
	evs := append([]TaxTransaction(nil), events...)
	for i := range evs {
		if evs[i].Date == "" {
			evs[i].Date = fmt.Sprintf("%04d-12-31", evs[i].Year)
		}
	}
	sort.SliceStable(evs, func(i, j int) bool { return evs[i].Date < evs[j].Date })
	out := make([]TaxTransaction, 0, len(txns)+len(evs))
	e := 0
	for _, t := range txns {
		k := key(t)
		for e < len(evs) && evs[e].Date < k {
			out = append(out, evs[e])
			e++
		}
		out = append(out, t)
	}
	return append(out, evs[e:]...)
}

// VorabpauschaleForYear returns the per-fund Vorabpauschale computed for the
// calendar year `year` (taxed in January of year+1) from the
// "vorabpauschale" events in txns, with TaxOnVP at the options' tax rate.
func VorabpauschaleForYear(txns []TaxTransaction, year int, opts TaxOptions) []VorabpauschaleEntry {
	l := runTaxLedger(txns)
	rate := CapitalIncomeTaxRate(opts.ChurchTaxRate)
	var out []VorabpauschaleEntry
	for _, v := range l.vp {
		if v.year != year || v.entry.Vorabpauschale <= 0 && v.entry.Basisertrag <= 0 {
			continue
		}
		e := v.entry
		e.TaxOnVP = e.TaxableVP * rate
		out = append(out, roundVPEntry(e))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ISIN < out[j].ISIN })
	return out
}

func roundVPEntry(e VorabpauschaleEntry) VorabpauschaleEntry {
	e.Units = math.Round(e.Units*1e6) / 1e6
	e.Jan1Price = round2(e.Jan1Price)
	e.YearEndPrice = round2(e.YearEndPrice)
	e.Jan1Value = round2(e.Jan1Value)
	e.YearEndValue = round2(e.YearEndValue)
	e.Basisertrag = round2(e.Basisertrag)
	e.Distributions = round2(e.Distributions)
	e.Vorabpauschale = round2(e.Vorabpauschale)
	e.TaxableVP = round2(e.TaxableVP)
	e.TaxOnVP = round2(e.TaxOnVP)
	return e
}

// ───────────── Loss pots ─────────────

// potYear collects one year's §20 capital income by pot.
type potYear struct {
	shareGains, shareLosses     float64 // stock sales (Aktienverlusttopf)
	genSaleGains, genSaleLosses float64 // all other §20 sales, after Teilfreistellung
	rawGains, rawLosses         float64 // §20 sale P/L before Teilfreistellung
	saleTFS                     float64 // Teilfreistellung on sales (negative on losses)
	dividends, dividendTFS      float64
	interest                    float64
	vpIncome, vpTFS             float64
}

func (p *potYear) generalIncome() float64 {
	return p.genSaleGains + p.dividends - p.dividendTFS + p.interest + p.vpIncome - p.vpTFS
}

func (l *taxLedger) potYears() map[int]*potYear {
	years := make(map[int]*potYear)
	get := func(y int) *potYear {
		p := years[y]
		if p == nil {
			p = &potYear{}
			years[y] = p
		}
		return p
	}
	for _, s := range l.sales {
		if s.PrivateSale {
			continue
		}
		p := get(s.Year)
		taxable := s.TaxablePL()
		if s.PL >= 0 {
			p.rawGains += s.PL
		} else {
			p.rawLosses += s.PL
		}
		p.saleTFS += s.PL - taxable
		switch {
		case IsShareAssetClass(s.AssetClass) && taxable >= 0:
			p.shareGains += taxable
		case IsShareAssetClass(s.AssetClass):
			p.shareLosses += taxable
		case taxable >= 0:
			p.genSaleGains += taxable
		default:
			p.genSaleLosses += taxable
		}
	}
	for _, e := range l.income {
		if e.PrivateSale {
			continue
		}
		p := get(e.Year)
		switch e.Kind {
		case "dividend":
			p.dividends += e.Amount
			p.dividendTFS += e.Amount * e.TFS
		case "interest":
			p.interest += e.Amount
		case "vorabpauschale":
			p.vpIncome += e.Amount
			p.vpTFS += e.Amount * e.TFS
		}
	}
	return years
}

// potResult is one year's loss-pot netting.
type potResult struct {
	shareNet, generalNet           float64 // this year's pot balances before carry-forward
	carryInShare, carryInGeneral   float64 // ≤ 0
	carryOutShare, carryOutGeneral float64 // ≤ 0
	generalUsedAgainstShares       float64
	taxable                        float64 // after all offsets, before the Sparer-Pauschbetrag
}

// netLossPots applies §20 Abs. 6 EStG year by year:
//   - share-sale losses (Aktienverlusttopf) offset only share-sale gains,
//     first this year's, then carried forward;
//   - all other losses (allgemeiner Verlusttopf) offset any capital income,
//     share gains included, and carry forward the same way;
//   - share gains are offset by share losses first, so the more flexible
//     general losses are kept where possible.
func netLossPots(years map[int]*potYear) map[int]potResult {
	var sorted []int
	for y := range years {
		sorted = append(sorted, y)
	}
	sort.Ints(sorted)
	out := make(map[int]potResult, len(sorted))
	carryS, carryG := 0.0, 0.0
	for _, y := range sorted {
		p := years[y]
		r := potResult{carryInShare: carryS, carryInGeneral: carryG}
		s := p.shareGains + p.shareLosses
		g := p.generalIncome() + p.genSaleLosses
		r.shareNet, r.generalNet = s, g
		if s > 0 && carryS < 0 {
			o := math.Min(s, -carryS)
			s -= o
			carryS += o
		}
		if g > 0 && carryG < 0 {
			o := math.Min(g, -carryG)
			g -= o
			carryG += o
		}
		if s > 0 && g < 0 {
			o := math.Min(s, -g)
			s -= o
			g += o
			r.generalUsedAgainstShares += o
		}
		if s > 0 && carryG < 0 {
			o := math.Min(s, -carryG)
			s -= o
			carryG += o
			r.generalUsedAgainstShares += o
		}
		carryS += math.Min(0, s)
		carryG += math.Min(0, g)
		r.taxable = math.Max(0, s) + math.Max(0, g)
		r.carryOutShare, r.carryOutGeneral = carryS, carryG
		out[y] = r
	}
	return out
}

// potResultFor returns the netting for `year`, carrying balances through
// years without activity.
func potResultFor(results map[int]potResult, year int) potResult {
	if r, ok := results[year]; ok {
		return r
	}
	best := -1
	for y := range results {
		if y < year && y > best {
			best = y
		}
	}
	if best < 0 {
		return potResult{}
	}
	prev := results[best]
	return potResult{
		carryInShare: prev.carryOutShare, carryInGeneral: prev.carryOutGeneral,
		carryOutShare: prev.carryOutShare, carryOutGeneral: prev.carryOutGeneral,
	}
}

// ───────────── §23 private sales ─────────────

// PrivateSaleLine is one §23 sale.
type PrivateSaleLine struct {
	Date      string  `json:"date"`
	AccountID string  `json:"account_id,omitempty"`
	ISIN      string  `json:"isin"`
	Name      string  `json:"name"`
	Quantity  float64 `json:"quantity"`
	Proceeds  float64 `json:"proceeds"`
	CostBasis float64 `json:"cost_basis"`
	TaxablePL float64 `json:"taxable_pl"`  // lots held one year or less
	TaxFreePL float64 `json:"tax_free_pl"` // lots held more than one year
}

// PrivateSaleSummary reports a year's private sales (§23 EStG: crypto and
// physically backed commodities). They are taxed at the personal income-tax
// rate, not the Abgeltungsteuer, and never enter the loss pots or the
// Sparer-Pauschbetrag.
type PrivateSaleSummary struct {
	Year            int               `json:"year"`
	TaxableGains    float64           `json:"taxable_gains"`  // gains on lots held ≤ 1 year
	TaxableLosses   float64           `json:"taxable_losses"` // losses on lots held ≤ 1 year (≤ 0)
	NetTaxable      float64           `json:"net_taxable"`
	TaxFreePL       float64           `json:"tax_free_pl"` // P/L on lots held > 1 year, tax-free
	Freigrenze      float64           `json:"freigrenze"`
	BelowFreigrenze bool              `json:"below_freigrenze"`
	TaxableAmount   float64           `json:"taxable_amount"` // net gain taxed at the personal rate (0 below the Freigrenze)
	OtherIncome     float64           `json:"other_income"`   // staking/lending income on §23 assets (§22 Nr. 3 EStG)
	Sales           []PrivateSaleLine `json:"sales"`
}

func (l *taxLedger) privateSales(year int) *PrivateSaleSummary {
	ps := &PrivateSaleSummary{Year: year, Freigrenze: PrivateSaleFreigrenze(year), Sales: []PrivateSaleLine{}}
	found := false
	for _, s := range l.sales {
		if !s.PrivateSale || s.Year != year {
			continue
		}
		found = true
		if s.ShortTermPL >= 0 {
			ps.TaxableGains += s.ShortTermPL
		} else {
			ps.TaxableLosses += s.ShortTermPL
		}
		ps.TaxFreePL += s.LongTermPL
		ps.Sales = append(ps.Sales, PrivateSaleLine{
			Date: s.Date, AccountID: s.AccountID, ISIN: s.ISIN, Name: s.Name,
			Quantity: math.Round(s.Quantity*1e8) / 1e8, Proceeds: round2(s.Proceeds), CostBasis: round2(s.CostBasis),
			TaxablePL: round2(s.ShortTermPL), TaxFreePL: round2(s.LongTermPL),
		})
	}
	for _, e := range l.income {
		if e.PrivateSale && e.Year == year {
			found = true
			ps.OtherIncome += e.Amount
		}
	}
	if !found {
		return nil
	}
	ps.NetTaxable = ps.TaxableGains + ps.TaxableLosses
	// Freigrenze, not Freibetrag: tax-free only while the year's total gain
	// is below it (§23 Abs. 3 S. 5 EStG).
	ps.BelowFreigrenze = ps.NetTaxable < ps.Freigrenze
	if !ps.BelowFreigrenze {
		ps.TaxableAmount = ps.NetTaxable
	}
	ps.TaxableGains = round2(ps.TaxableGains)
	ps.TaxableLosses = round2(ps.TaxableLosses)
	ps.NetTaxable = round2(ps.NetTaxable)
	ps.TaxFreePL = round2(ps.TaxFreePL)
	ps.TaxableAmount = round2(ps.TaxableAmount)
	ps.OtherIncome = round2(ps.OtherIncome)
	return ps
}
