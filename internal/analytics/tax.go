package analytics

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

const (
	// German tax constants
	Abgeltungssteuer     = 0.25    // 25% flat tax on capital gains
	Solidaritaetszuschlag = 0.055   // 5.5% surcharge on Abgeltungssteuer
	EffectiveTaxRate      = Abgeltungssteuer * (1 + Solidaritaetszuschlag) // 26.375%
	Sparerpauschbetrag    = 1000.0  // Annual tax-free allowance (since 2023)

	// Teilfreistellung rates
	TeilfreistellungEquity = 0.30 // 30% tax-free for equity funds (>51% equities)
	TeilfreistellungMixed  = 0.15 // 15% tax-free for mixed funds (>25% equities)
	TeilfreistellungBond   = 0.00 // 0% tax-free for bond / money-market / Sonstige Fonds
)

// TeilfreistellungRate returns the InvStG 2018 partial-exemption rate for a
// fund classified by equity exposure (from the prospectus or fact sheet).
// Raw `asset_class == "etf"` is NOT sufficient — it lumps equity ETFs and
// bond ETFs together, which would over-claim the 30% exemption on bond
// funds. Use this function with the fund's actual equity percentage.
//
//   - equityPct ≥ 0.51 → 0.30 (Aktienfonds)
//   - equityPct ≥ 0.25 → 0.15 (Mischfonds)
//   - otherwise        → 0.00 (Sonstige Investmentfonds — bonds, money market, gold ETC)
//
// Real-estate funds (60% domestic / 80% foreign Teilfreistellung) and
// stocks held directly (no Teilfreistellung at all — full 26.375% on
// gains) are out of scope; callers must route those separately.
func TeilfreistellungRate(equityPct float64) float64 {
	if equityPct >= 0.51 {
		return TeilfreistellungEquity
	}
	if equityPct >= 0.25 {
		return TeilfreistellungMixed
	}
	return TeilfreistellungBond
}

// SecurityTeilfreistellungRate resolves a security's Teilfreistellung rate from
// its asset_class and optional equity_pct. Individual stocks, crypto, bonds,
// derivatives, and commodities receive 0%. Funds/ETFs use equity_pct when known
// (e.g. bond ETFs with equity_pct=0 -> 0%, mixed funds -> 15%, equity ETFs -> 30%),
// defaulting to 30% for ETFs when equity_pct has not yet been populated.
func SecurityTeilfreistellungRate(assetClass string, equityPct *float64) float64 {
	if assetClass != "etf" && assetClass != "fund" {
		return 0
	}
	if equityPct != nil {
		return TeilfreistellungRate(*equityPct)
	}
	if assetClass == "etf" {
		return TeilfreistellungEquity
	}
	return 0
}

// ResolveSecurityTFSRate resolves a security's InvStG Teilfreistellung rate (0.30 equity,
// 0.15 mixed, 0.00 bond/money-market/stock/crypto). Accepts equityPct in either 0..1
// fraction or >1 percentage form, and falls back to fund-name heuristics when nil.
func ResolveSecurityTFSRate(assetClass, name string, equityPct *float64) float64 {
	if assetClass != "etf" && assetClass != "fund" {
		return 0
	}
	if equityPct != nil {
		v := *equityPct
		if v > 1.0 {
			v /= 100.0
		}
		return SecurityTeilfreistellungRate(assetClass, &v)
	}
	lowerName := strings.ToLower(name)
	for _, kw := range []string{
		"money market", "overnight", "geldmarkt", "xeon",
		"staatsanleihe", "treasury", "govt bond", "government bond",
		"corporate bond", "aggregate bond", "eur bond", "usd bond",
		"global bond", "anleihen", "pfandbrief", "ultrashort bond",
		"short duration", "physical gold", "gold etc",
	} {
		if strings.Contains(lowerName, kw) {
			return TeilfreistellungBond
		}
	}
	for _, kw := range []string{"mischfonds", "lifestrategy 40", "multi-asset"} {
		if strings.Contains(lowerName, kw) {
			return TeilfreistellungMixed
		}
	}
	return SecurityTeilfreistellungRate(assetClass, nil)
}

func txnTFSRate(txn TaxTransaction) float64 {
	if txn.HasExplicitTFS {
		return txn.TFS
	}
	if txn.TFS > 0 {
		return txn.TFS
	}
	if txn.IsEquityFund {
		return TeilfreistellungEquity
	}
	return 0
}

// Basiszins published by Deutsche Bundesbank (used for Vorabpauschale)
var BasiszinsByYear = map[int]float64{
	2022: -0.05, 2023: 2.55, 2024: 2.29, 2025: 2.53, 2026: 2.53,
}

// VorabpauschaleEntry holds per-ETF Vorabpauschale data.
type VorabpauschaleEntry struct {
	ISIN          string  `json:"isin"`
	Name          string  `json:"name"`
	Jan1Value     float64 `json:"jan1_value"`
	YearEndValue  float64 `json:"year_end_value"`
	Basiszins     float64 `json:"basiszins"`
	Basisertrag   float64 `json:"basisertrag"`
	Vorabpauschale float64 `json:"vorabpauschale"`
	TaxOnVP       float64 `json:"tax_on_vp"` // after Teilfreistellung + Abgeltungssteuer
}

// VorabpauschaleLotInput describes a mid-year fund purchase for 1/12 pro-rating (§ 18 Abs. 2 InvStG).
type VorabpauschaleLotInput struct {
	BuyMonth int     // 1..12
	ValueJan float64 // initial purchase cost / base value for the partial year
}

// VorabpauschaleInput holds complete per-fund data for § 18 InvStG Vorabpauschale calculation.
type VorabpauschaleInput struct {
	ISIN          string
	Name          string
	Jan1Value     float64
	YearEndValue  float64
	Distributions float64                  // actual cash distributions paid during the year
	MidYearBuys   []VorabpauschaleLotInput // acquisitions during `year` (pro-rated by (13 - month)/12)
	TFSRate       float64                  // e.g. 0.30 equity, 0.15 mixed, 0.00 bond
}

// ComputeVorabpauschale calculates the Vorabpauschale for accumulating ETFs.
// jan1Holdings: ISIN → value on Jan 1 of the year
// yearEndHoldings: ISIN → value at year end (or current)
// names: ISIN → fund name
func ComputeVorabpauschale(year int, jan1Holdings, yearEndHoldings map[string]float64, names map[string]string) []VorabpauschaleEntry {
	var inputs []VorabpauschaleInput
	for isin, jan1Val := range jan1Holdings {
		inputs = append(inputs, VorabpauschaleInput{
			ISIN:         isin,
			Name:         names[isin],
			Jan1Value:    jan1Val,
			YearEndValue: yearEndHoldings[isin],
			TFSRate:      TeilfreistellungEquity,
		})
	}
	return ComputeVorabpauschaleDetailed(year, inputs)
}

// VorabpauschaleTxn is a chronologically ordered security transaction used to
// reconstruct Jan 1 and surviving mid-year FIFO positions for § 18 InvStG.
type VorabpauschaleTxn struct {
	AccountID string
	ISIN      string
	Year      int
	Month     int // 1..12
	Type      string
	Quantity  float64
	AmountEUR float64
}

// VorabpauschaleYearPosition holds reconstructed year-end surviving share
// quantities, surviving pro-rated mid-year buy lots, and in-year distributions
// for a single fund ISIN after FIFO-deducting sales/transfers per depot.
type VorabpauschaleYearPosition struct {
	ISIN          string
	Jan1Qty       float64
	Jan1CostEUR   float64
	YearEndQty    float64
	MidYearBuys   []VorabpauschaleLotInput
	Distributions float64
}

// ReconstructVorabpauschaleYear replays chronologically ordered fund/ETF
// transactions up to Dec 31 of `year`, FIFO-deducting pre-year and in-year
// sells/transfer_outs per (AccountID, ISIN) so only shares still held at
// year-end contribute to Jan 1 value or pro-rated mid-year buy lots (§ 18 Abs. 3 InvStG).
func ReconstructVorabpauschaleYear(year int, txns []VorabpauschaleTxn) map[string]VorabpauschaleYearPosition {
	type lotKey struct {
		acct string
		isin string
	}
	type preLot struct {
		qty      float64
		unitCost float64
	}
	type midLot struct {
		buyMonth int
		qty      float64
		unitCost float64
	}

	preByKey := make(map[lotKey][]preLot)
	midByKey := make(map[lotKey][]midLot)
	var keyOrder []lotKey
	seenKey := make(map[lotKey]bool)
	distByISIN := make(map[string]float64)

	for _, t := range txns {
		if t.ISIN == "" || t.Year > year {
			continue
		}
		k := lotKey{acct: t.AccountID, isin: t.ISIN}
		if !seenKey[k] {
			seenKey[k] = true
			keyOrder = append(keyOrder, k)
		}
		switch t.Type {
		case "buy", "savings_plan", "transfer":
			if t.Quantity <= 0 {
				continue
			}
			unitCost := 0.0
			if t.AmountEUR > 0 {
				unitCost = t.AmountEUR / t.Quantity
			}
			if t.Year < year {
				preByKey[k] = append(preByKey[k], preLot{qty: t.Quantity, unitCost: unitCost})
			} else {
				preMonth := t.Month
				if preMonth < 1 {
					preMonth = 1
				} else if preMonth > 12 {
					preMonth = 12
				}
				midByKey[k] = append(midByKey[k], midLot{buyMonth: preMonth, qty: t.Quantity, unitCost: unitCost})
			}
		case "sell", "transfer_out":
			if t.Quantity <= 0 {
				continue
			}
			rem := t.Quantity
			for rem > 1e-9 && len(preByKey[k]) > 0 {
				l := &preByKey[k][0]
				if l.qty <= rem+1e-9 {
					rem -= l.qty
					preByKey[k] = preByKey[k][1:]
				} else {
					l.qty -= rem
					rem = 0
				}
			}
			if t.Year == year {
				for rem > 1e-9 && len(midByKey[k]) > 0 {
					l := &midByKey[k][0]
					if l.qty <= rem+1e-9 {
						rem -= l.qty
						midByKey[k] = midByKey[k][1:]
					} else {
						l.qty -= rem
						rem = 0
					}
				}
			}
		case "dividend":
			if t.Year == year && t.AmountEUR > 0 {
				distByISIN[t.ISIN] += t.AmountEUR
			}
		}
	}

	out := make(map[string]VorabpauschaleYearPosition)
	for _, k := range keyOrder {
		pos := out[k.isin]
		pos.ISIN = k.isin
		for _, pl := range preByKey[k] {
			if pl.qty > 1e-9 {
				pos.Jan1Qty += pl.qty
				pos.Jan1CostEUR += pl.qty * pl.unitCost
				pos.YearEndQty += pl.qty
			}
		}
		for _, ml := range midByKey[k] {
			if ml.qty > 1e-9 {
				pos.YearEndQty += ml.qty
				if ml.unitCost > 0 {
					pos.MidYearBuys = append(pos.MidYearBuys, VorabpauschaleLotInput{
						BuyMonth: ml.buyMonth,
						ValueJan: ml.qty * ml.unitCost,
					})
				}
			}
		}
		out[k.isin] = pos
	}

	for isin, pos := range out {
		if pos.YearEndQty <= 1e-9 {
			delete(out, isin)
			continue
		}
		pos.Distributions = distByISIN[isin]
		out[isin] = pos
	}
	return out
}

// ComputeVorabpauschaleDetailed computes § 18 InvStG Vorabpauschale subtracting
// actual distributions, pro-rating mid-year purchases by (13 - buyMonth)/12,
// and applying each fund's actual Teilfreistellung rate.
func ComputeVorabpauschaleDetailed(year int, inputs []VorabpauschaleInput) []VorabpauschaleEntry {
	basiszins, ok := BasiszinsByYear[year]
	if !ok || basiszins <= 0 {
		return nil // negative Basiszins = no Vorabpauschale
	}

	var entries []VorabpauschaleEntry
	for _, in := range inputs {
		baseCost := in.Jan1Value
		weightedBase := in.Jan1Value
		for _, b := range in.MidYearBuys {
			if b.ValueJan <= 0 {
				continue
			}
			m := b.BuyMonth
			if m < 1 {
				m = 1
			}
			if m > 12 {
				m = 12
			}
			baseCost += b.ValueJan
			weightedBase += b.ValueJan * float64(13-m) / 12.0
		}
		if weightedBase <= 0 || in.YearEndValue <= 0 {
			continue
		}

		// Basisertrag = weighted base × Basiszins × 0.7 (§ 18 Abs. 1 Satz 2, Abs. 2 InvStG)
		basisertrag := weightedBase * basiszins / 100 * 0.7
		if basisertrag <= 0 {
			continue
		}

		// Value appreciation + distributions cap: max(0, YearEnd - BaseCost) + Distributions
		wertsteigerung := math.Max(0, in.YearEndValue-baseCost) + math.Max(0, in.Distributions)
		capped := math.Min(basisertrag, wertsteigerung)

		// Subtract actual distributions paid during the year (§ 18 Abs. 1 Satz 3 InvStG)
		vp := math.Max(0, capped-math.Max(0, in.Distributions))

		taxableVP := vp * (1 - in.TFSRate)
		tax := taxableVP * EffectiveTaxRate

		entries = append(entries, VorabpauschaleEntry{
			ISIN:           in.ISIN,
			Name:           in.Name,
			Jan1Value:      math.Round(baseCost*100) / 100,
			YearEndValue:   math.Round(in.YearEndValue*100) / 100,
			Basiszins:      basiszins,
			Basisertrag:    math.Round(basisertrag*100) / 100,
			Vorabpauschale: math.Round(vp*100) / 100,
			TaxOnVP:        math.Round(tax*100) / 100,
		})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].ISIN < entries[j].ISIN })
	return entries
}

// TaxLot represents an open FIFO lot with tax implications.
type TaxLot struct {
	AccountID     string  `json:"account_id,omitempty"`
	ISIN          string  `json:"isin"`
	Name          string  `json:"name"`
	BuyDate       string  `json:"buy_date"`
	Quantity      float64 `json:"quantity"`
	CostBasis     float64 `json:"cost_basis"`
	CurrentValue  float64 `json:"current_value"`
	UnrealizedPL  float64 `json:"unrealized_pl"`
	IsEquityFund  bool    `json:"is_equity_fund"`
	TFSRate       float64 `json:"tfs_rate,omitempty"`
	TaxIfSold     float64 `json:"tax_if_sold"`     // estimated tax after Teilfreistellung
	NetProceeds   float64 `json:"net_proceeds"`    // current value minus tax
	EffectiveRate float64 `json:"effective_rate"`  // tax / gain %
}

// ComputeTaxLots reconstructs per-depot FIFO lots from transactions and computes tax impact.
func ComputeTaxLots(txns []TaxTransaction, prices map[string]float64, names map[string]string, equityMap map[string]bool) []TaxLot {
	type lotKey struct {
		acct string
		isin string
	}
	type lot struct {
		buyDate string
		qty     float64
		costPer float64
		tfsRate float64
		hasTFS  bool
	}
	lots := make(map[lotKey][]lot) // (AccountID, ISIN) -> FIFO queue
	var keyOrder []lotKey
	seenKey := make(map[lotKey]bool)

	// Sort chronologically (assume already sorted)
	for _, txn := range txns {
		if txn.ISIN == "" {
			continue
		}
		k := lotKey{acct: txn.AccountID, isin: txn.ISIN}
		if !seenKey[k] {
			seenKey[k] = true
			keyOrder = append(keyOrder, k)
		}
		switch txn.Type {
		case "buy", "savings_plan", "transfer":
			if txn.Quantity > 0 {
				costPer := txn.Amount / txn.Quantity
				buyDate := txn.Date
				if buyDate == "" {
					buyDate = fmt.Sprintf("%d-%02d-%02d", txn.Year, 1, 1)
				}
				lots[k] = append(lots[k], lot{
					buyDate: buyDate,
					qty:     txn.Quantity,
					costPer: costPer,
					tfsRate: txn.TFS,
					hasTFS:  txn.HasExplicitTFS || txn.TFS > 0,
				})
			}
		case "sell", "transfer_out":
			remaining := txn.Quantity
			for remaining > 1e-9 && len(lots[k]) > 0 {
				l := &lots[k][0]
				if l.qty <= remaining+1e-9 {
					remaining -= l.qty
					lots[k] = lots[k][1:]
				} else {
					l.qty -= remaining
					remaining = 0
				}
			}
		}
	}

	// Build tax lot entries
	var result []TaxLot
	for _, k := range keyOrder {
		isin := k.isin
		isinLots := lots[k]
		price := prices[isin]
		isEquity := equityMap[isin]
		name := names[isin]
		if name == "" {
			name = isin
		}

		for _, l := range isinLots {
			if l.qty <= 1e-9 {
				continue
			}
			costBasis := l.qty * l.costPer
			currentValue := l.qty * price
			if price == 0 {
				currentValue = costBasis
			}
			unrealizedPL := currentValue - costBasis

			tfs := 0.0
			if l.hasTFS {
				tfs = l.tfsRate
			} else if isEquity {
				tfs = TeilfreistellungEquity
			}

			// Tax computation
			taxIfSold := 0.0
			if unrealizedPL > 0 {
				taxable := unrealizedPL * (1 - tfs)
				taxIfSold = taxable * EffectiveTaxRate
			}

			effectiveRate := 0.0
			if unrealizedPL > 0 {
				effectiveRate = (taxIfSold / unrealizedPL) * 100
			}

			result = append(result, TaxLot{
				AccountID:     k.acct,
				ISIN:          isin,
				Name:          name,
				BuyDate:       l.buyDate,
				Quantity:      math.Round(l.qty*1e8) / 1e8,
				CostBasis:     math.Round(costBasis*100) / 100,
				CurrentValue:  math.Round(currentValue*100) / 100,
				UnrealizedPL:  math.Round(unrealizedPL*100) / 100,
				IsEquityFund:  isEquity || tfs >= TeilfreistellungEquity,
				TFSRate:       tfs,
				TaxIfSold:     math.Round(taxIfSold*100) / 100,
				NetProceeds:   math.Round((currentValue-taxIfSold)*100) / 100,
				EffectiveRate: math.Round(effectiveRate*10) / 10,
			})
		}
	}
	return result
}

// SellRequest represents a user's intent to sell a certain EUR amount of a security.
type SellRequest struct {
	ISIN      string  `json:"isin"`
	AmountEUR float64 `json:"amount_eur"`
}

// SellSimResult represents the tax impact of simulating a sell.
type SellSimResult struct {
	ISIN             string  `json:"isin"`
	Name             string  `json:"name"`
	SellAmount       float64 `json:"sell_amount"`
	CostBasis        float64 `json:"cost_basis"`
	RealizedGain     float64 `json:"realized_gain"`
	Teilfreistellung float64 `json:"teilfreistellung"`
	TaxableGain      float64 `json:"taxable_gain"`
	EstimatedTax     float64 `json:"estimated_tax"`
	NetProceeds      float64 `json:"net_proceeds"`
	LotsConsumed     int     `json:"lots_consumed"`
	IsEquityFund     bool    `json:"is_equity_fund"`
}

// SellTaxRate returns the effective rate on a taxable capital gain given an
// optional church-tax rate (KiSt). Formula:
//
//	rate = Abgeltungssteuer × (1 + Soli + KiSt)
//
// Defaults (no church): 0.25 × 1.055 = 0.26375 (= EffectiveTaxRate).
// 8% church (BY/BW): 0.25 × 1.135 = 0.28375.
// 9% church (other Bundesländer): 0.25 × 1.145 = 0.28625.
// churchTaxRate is silently clamped to [0, 0.09] to keep the planner sane.
func SellTaxRate(churchTaxRate float64) float64 {
	if churchTaxRate < 0 {
		churchTaxRate = 0
	}
	if churchTaxRate > 0.09 {
		churchTaxRate = 0.09
	}
	return Abgeltungssteuer * (1 + Solidaritaetszuschlag + churchTaxRate)
}

// SimulateSell computes the tax impact of selling specified EUR amounts using
// FIFO lot order. churchTaxRate is the Kirchensteuer surcharge applied on top
// of Abgeltungssteuer (typically 0, 0.08 BY/BW, or 0.09 other Bundesländer).
func SimulateSell(lots []TaxLot, requests []SellRequest, churchTaxRate float64) ([]SellSimResult, float64, float64) {
	effectiveRate := SellTaxRate(churchTaxRate)
	// Group lots by ISIN in order
	lotsByISIN := make(map[string][]TaxLot)
	for _, l := range lots {
		lotsByISIN[l.ISIN] = append(lotsByISIN[l.ISIN], l)
	}

	var results []SellSimResult
	totalTax := 0.0
	totalProceeds := 0.0

	for _, req := range requests {
		if req.AmountEUR <= 0 || math.IsNaN(req.AmountEUR) || math.IsInf(req.AmountEUR, 0) {
			continue
		}
		isinLots := lotsByISIN[req.ISIN]
		if len(isinLots) == 0 {
			continue
		}

		remaining := req.AmountEUR
		totalCost := 0.0
		totalSold := 0.0
		lotsUsed := 0
		name := isinLots[0].Name
		isEquity := isinLots[0].IsEquityFund

		for _, lot := range isinLots {
			if remaining <= 0 {
				break
			}
			if lot.CurrentValue <= 0 || math.IsNaN(lot.CurrentValue) || math.IsInf(lot.CurrentValue, 0) {
				continue
			}
			// How much of this lot to sell (by value)
			sellFromLot := math.Min(remaining, lot.CurrentValue)
			fraction := sellFromLot / lot.CurrentValue
			costOfSold := fraction * lot.CostBasis

			totalSold += sellFromLot
			totalCost += costOfSold
			remaining -= sellFromLot
			lotsUsed++
		}

		gain := totalSold - totalCost
		tfsRate := isinLots[0].TFSRate
		if tfsRate == 0 && isEquity {
			tfsRate = TeilfreistellungEquity
		}
		tf := 0.0
		if tfsRate > 0 && gain > 0 {
			tf = gain * tfsRate
		}
		taxableGain := gain - tf
		if taxableGain < 0 {
			taxableGain = 0
		}
		tax := taxableGain * effectiveRate
		if gain < 0 {
			tax = 0
		}

		results = append(results, SellSimResult{
			ISIN:             req.ISIN,
			Name:             name,
			SellAmount:       math.Round(totalSold*100) / 100,
			CostBasis:        math.Round(totalCost*100) / 100,
			RealizedGain:     math.Round(gain*100) / 100,
			Teilfreistellung: math.Round(tf*100) / 100,
			TaxableGain:      math.Round(taxableGain*100) / 100,
			EstimatedTax:     math.Round(tax*100) / 100,
			NetProceeds:      math.Round((totalSold-tax)*100) / 100,
			LotsConsumed:     lotsUsed,
			IsEquityFund:     isEquity,
		})
		totalTax += tax
		totalProceeds += totalSold - tax
	}

	return results, math.Round(totalTax*100) / 100, math.Round(totalProceeds*100) / 100
}

// LossPotYear tracks the two German loss offset pots for a single tax year.
type LossPotYear struct {
	Year               int     `json:"year"`
	EquityLosses       float64 `json:"equity_losses"`       // Aktienverlusttopf additions
	EquityGains        float64 `json:"equity_gains"`        // equity gains that offset
	EquityBalance      float64 `json:"equity_balance"`      // running balance (negative = losses available)
	GeneralLosses      float64 `json:"general_losses"`      // allgemeiner Verlusttopf additions
	GeneralGains       float64 `json:"general_gains"`       // all other gains + dividends + interest
	GeneralBalance     float64 `json:"general_balance"`     // running balance
	CarryForwardEquity float64 `json:"carry_forward_equity"` // carried to next year
	CarryForwardGeneral float64 `json:"carry_forward_general"`
}

// ComputeLossPots calculates the German loss offset pots per year from transactions
// using per-depot FIFO cost basis tracking (§ 20 Abs. 4 Satz 7 EStG).
func ComputeLossPots(txns []TaxTransaction) []LossPotYear {
	type fifoKey struct {
		acct string
		isin string
	}
	type lot struct {
		qty     float64
		costPer float64
	}
	lots := make(map[fifoKey][]lot)

	yearEquityLosses := make(map[int]float64)
	yearEquityGains := make(map[int]float64)
	yearGeneralLosses := make(map[int]float64)
	yearGeneralGains := make(map[int]float64)
	years := make(map[int]bool)

	consumeFIFO := func(k fifoKey, qty float64) (soldQty, costBasis float64) {
		rem := qty
		for rem > 1e-9 && len(lots[k]) > 0 {
			l := &lots[k][0]
			if l.qty <= rem+1e-9 {
				soldQty += l.qty
				costBasis += l.qty * l.costPer
				rem -= l.qty
				lots[k] = lots[k][1:]
			} else {
				soldQty += rem
				costBasis += rem * l.costPer
				l.qty -= rem
				rem = 0
			}
		}
		return soldQty, costBasis
	}

	for _, txn := range txns {
		years[txn.Year] = true
		k := fifoKey{acct: txn.AccountID, isin: txn.ISIN}
		switch txn.Type {
		case "buy", "savings_plan", "transfer":
			if txn.ISIN == "" || txn.Quantity <= 0 {
				continue
			}
			lots[k] = append(lots[k], lot{
				qty:     txn.Quantity,
				costPer: txn.Amount / txn.Quantity,
			})

		case "transfer_out":
			// Transfer out: reduce FIFO lots but NO realized gain/loss
			if txn.ISIN == "" || txn.Quantity <= 0 {
				continue
			}
			consumeFIFO(k, txn.Quantity)

		case "sell":
			if txn.ISIN == "" || txn.Quantity <= 0 {
				continue
			}
			soldQty, costBasis := consumeFIFO(k, txn.Quantity)
			if soldQty <= 1e-9 {
				continue
			}
			proceeds := txn.Amount
			if soldQty < txn.Quantity {
				proceeds = txn.Amount * (soldQty / txn.Quantity)
			}
			pl := proceeds - costBasis

			tfs := txnTFSRate(txn)
			if tfs > 0 {
				pl *= (1 - tfs)
			}

			if txn.IsEquityFund {
				if pl < 0 {
					yearEquityLosses[txn.Year] += pl
				} else {
					yearEquityGains[txn.Year] += pl
				}
			} else {
				if pl < 0 {
					yearGeneralLosses[txn.Year] += pl
				} else {
					yearGeneralGains[txn.Year] += pl
				}
			}

		case "dividend":
			// Dividends go to general gains (can be offset by general losses)
			amt := txn.Amount * (1 - txnTFSRate(txn))
			yearGeneralGains[txn.Year] += amt

		case "interest":
			yearGeneralGains[txn.Year] += txn.Amount
		}
	}

	// Sort years
	var sortedYears []int
	for y := range years { sortedYears = append(sortedYears, y) }
	sort.Ints(sortedYears)

	var result []LossPotYear
	carryEquity, carryGeneral := 0.0, 0.0

	for _, year := range sortedYears {
		eqLoss := yearEquityLosses[year] // negative
		eqGain := yearEquityGains[year]
		genLoss := yearGeneralLosses[year] // negative
		genGain := yearGeneralGains[year]

		// Net equity: gains offset by losses + carry-forward
		eqNet := eqGain + eqLoss + carryEquity // carryEquity is negative
		newCarryEquity := 0.0
		if eqNet < 0 {
			newCarryEquity = eqNet
			eqNet = 0
		}

		// Net general: gains offset by losses + carry-forward
		genNet := genGain + genLoss + carryGeneral
		newCarryGeneral := 0.0
		if genNet < 0 {
			newCarryGeneral = genNet
			genNet = 0
		}

		result = append(result, LossPotYear{
			Year:               year,
			EquityLosses:       math.Round(eqLoss*100) / 100,
			EquityGains:        math.Round(eqGain*100) / 100,
			EquityBalance:      math.Round((eqLoss+eqGain)*100) / 100,
			GeneralLosses:      math.Round(genLoss*100) / 100,
			GeneralGains:       math.Round(genGain*100) / 100,
			GeneralBalance:     math.Round((genLoss+genGain)*100) / 100,
			CarryForwardEquity: math.Round(newCarryEquity*100) / 100,
			CarryForwardGeneral: math.Round(newCarryGeneral*100) / 100,
		})

		carryEquity = newCarryEquity
		carryGeneral = newCarryGeneral
	}

	return result
}

// TaxSummary contains the computed tax data for a given year.
type TaxSummary struct {
	Year               int                `json:"year"`
	RealizedGains      float64            `json:"realized_gains"`
	RealizedLosses     float64            `json:"realized_losses"`
	NetGain            float64            `json:"net_gain"`
	TeilfreistellungAmt float64           `json:"teilfreistellung_amt"`
	TaxableGain        float64            `json:"taxable_gain"`
	FreistellungUsed   float64            `json:"freistellung_used"`
	FreistellungRemain float64            `json:"freistellung_remaining"`
	EstimatedTax       float64            `json:"estimated_tax"`
	EffectiveRate      float64            `json:"effective_rate"`
	DividendIncome     float64            `json:"dividend_income"`
	BySecurity         []TaxBySecurity    `json:"by_security"`
	TaxLossHints       []TaxLossHint      `json:"tax_loss_hints,omitempty"`
}

// TaxBySecurity shows tax impact per security.
type TaxBySecurity struct {
	ISIN              string  `json:"isin"`
	Name              string  `json:"name"`
	RealizedPL        float64 `json:"realized_pl"`
	Teilfreistellung  float64 `json:"teilfreistellung"`
	TaxablePL         float64 `json:"taxable_pl"`
	IsEquityFund      bool    `json:"is_equity_fund"`
}

// TaxLossHint suggests positions that could be sold to offset gains.
type TaxLossHint struct {
	ISIN          string  `json:"isin"`
	Name          string  `json:"name"`
	UnrealizedPL  float64 `json:"unrealized_pl"`
	PotentialSaving float64 `json:"potential_saving"`
}

// TaxTransaction represents a transaction relevant for tax computation.
type TaxTransaction struct {
	AccountID      string  // optional: scopes FIFO lots per depot
	Date           string  // optional: YYYY-MM-DD purchase/trade date
	Year           int
	Type           string  // "buy", "sell", "dividend", "savings_plan", "transfer", "transfer_out", "interest"
	ISIN           string
	Name           string
	Quantity       float64
	Amount         float64
	IsEquityFund   bool
	TFS            float64 // optional explicit Teilfreistellung rate (0.30, 0.15, 0.00)
	HasExplicitTFS bool    // true when TFS was explicitly resolved from EquityPct
}

// ComputeTaxSummary calculates the German tax summary for a given year.
// It takes all transactions (for per-depot FIFO cost basis tracking) and filters results to the target year.
func ComputeTaxSummary(allTxns []TaxTransaction, year int, unrealizedPositions []TaxLossHint) *TaxSummary {
	type fifoKey struct {
		acct string
		isin string
	}
	type lot struct {
		qty     float64
		costPer float64
	}
	lots := make(map[fifoKey][]lot)

	consumeFIFO := func(k fifoKey, qty float64) (soldQty, costBasis float64) {
		rem := qty
		for rem > 1e-9 && len(lots[k]) > 0 {
			l := &lots[k][0]
			if l.qty <= rem+1e-9 {
				soldQty += l.qty
				costBasis += l.qty * l.costPer
				rem -= l.qty
				lots[k] = lots[k][1:]
			} else {
				soldQty += rem
				costBasis += rem * l.costPer
				l.qty -= rem
				rem = 0
			}
		}
		return soldQty, costBasis
	}

	var gains, losses, dividends, dividendTFS float64
	var bySec []TaxBySecurity
	var bySecTFS []float64

	for _, txn := range allTxns {
		k := fifoKey{acct: txn.AccountID, isin: txn.ISIN}

		switch txn.Type {
		case "buy", "savings_plan", "transfer":
			if txn.ISIN == "" || txn.Quantity <= 0 {
				continue
			}
			lots[k] = append(lots[k], lot{
				qty:     txn.Quantity,
				costPer: txn.Amount / txn.Quantity,
			})

		case "transfer_out":
			// Transfer out: reduce FIFO lots but NO realized gain/loss
			if txn.ISIN == "" || txn.Quantity <= 0 {
				continue
			}
			consumeFIFO(k, txn.Quantity)

		case "sell":
			if txn.ISIN == "" || txn.Quantity <= 0 {
				continue
			}
			soldQty, costBasis := consumeFIFO(k, txn.Quantity)
			if soldQty <= 1e-9 {
				continue
			}
			proceeds := txn.Amount
			if soldQty < txn.Quantity {
				proceeds = txn.Amount * (soldQty / txn.Quantity)
			}
			realizedPL := proceeds - costBasis

			// Only count gains/losses for the target year
			if txn.Year == year {
				if realizedPL >= 0 {
					gains += realizedPL
				} else {
					losses += realizedPL
				}
				tfsRate := txnTFSRate(txn)
				bySec = append(bySec, TaxBySecurity{
					ISIN:         txn.ISIN,
					Name:         txn.Name,
					RealizedPL:   math.Round(realizedPL*100) / 100,
					IsEquityFund: txn.IsEquityFund || tfsRate >= TeilfreistellungEquity,
				})
				bySecTFS = append(bySecTFS, tfsRate)
			}

		case "dividend":
			if txn.Year == year {
				dividends += txn.Amount
				dividendTFS += txn.Amount * txnTFSRate(txn)
			}
		}
	}

	netGain := gains + losses // losses are negative

	// Apply Teilfreistellung per security (gains only; losses preserved at full magnitude)
	teilfreistellungTotal := 0.0
	for i, s := range bySec {
		tfsRate := bySecTFS[i]
		if tfsRate > 0 && s.RealizedPL > 0 {
			tf := s.RealizedPL * tfsRate
			bySec[i].Teilfreistellung = math.Round(tf*100) / 100
			bySec[i].TaxablePL = math.Round((s.RealizedPL-tf)*100) / 100
			teilfreistellungTotal += tf
		} else {
			bySec[i].TaxablePL = s.RealizedPL
		}
	}

	taxableDividends := dividends - dividendTFS
	teilfreistellungTotal += dividendTFS

	taxableGain := netGain - teilfreistellungTotal + taxableDividends
	if taxableGain < 0 {
		taxableGain = 0
	}

	// Freistellungsauftrag
	freiUsed := math.Min(taxableGain, Sparerpauschbetrag)
	freiRemain := Sparerpauschbetrag - freiUsed

	// Estimated tax
	taxableAfterFrei := taxableGain - freiUsed
	if taxableAfterFrei < 0 {
		taxableAfterFrei = 0
	}
	estimatedTax := taxableAfterFrei * EffectiveTaxRate

	effectiveRate := 0.0
	if netGain+dividends > 0 {
		effectiveRate = (estimatedTax / (netGain + dividends)) * 100
	}

	return &TaxSummary{
		Year:               year,
		RealizedGains:      math.Round(gains*100) / 100,
		RealizedLosses:     math.Round(losses*100) / 100,
		NetGain:            math.Round(netGain*100) / 100,
		TeilfreistellungAmt: math.Round(teilfreistellungTotal*100) / 100,
		TaxableGain:        math.Round(taxableGain*100) / 100,
		FreistellungUsed:   math.Round(freiUsed*100) / 100,
		FreistellungRemain: math.Round(freiRemain*100) / 100,
		EstimatedTax:       math.Round(estimatedTax*100) / 100,
		EffectiveRate:      math.Round(effectiveRate*10) / 10,
		DividendIncome:     math.Round(dividends*100) / 100,
		BySecurity:         bySec,
		TaxLossHints:       unrealizedPositions,
	}
}
