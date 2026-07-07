package analytics

import (
	"fmt"
	"math"
	"sort"
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

// ComputeVorabpauschale calculates the Vorabpauschale for accumulating ETFs.
// jan1Holdings: ISIN → value on Jan 1 of the year
// yearEndHoldings: ISIN → value at year end (or current)
// names: ISIN → fund name
func ComputeVorabpauschale(year int, jan1Holdings, yearEndHoldings map[string]float64, names map[string]string) []VorabpauschaleEntry {
	basiszins, ok := BasiszinsByYear[year]
	if !ok || basiszins <= 0 {
		return nil // negative Basiszins = no Vorabpauschale
	}

	var entries []VorabpauschaleEntry
	for isin, jan1Val := range jan1Holdings {
		if jan1Val <= 0 {
			continue
		}
		yearEndVal := yearEndHoldings[isin]
		priceGain := yearEndVal - jan1Val

		// Basisertrag = Jan1 value × Basiszins × 0.7
		basisertrag := jan1Val * basiszins / 100 * 0.7
		if basisertrag <= 0 {
			continue
		}

		// Vorabpauschale = min(Basisertrag, actual price gain)
		// If price dropped, Vorabpauschale = 0
		vp := basisertrag
		if priceGain < basisertrag {
			vp = math.Max(priceGain, 0)
		}

		// Tax: apply Teilfreistellung (30% for equity) then Abgeltungssteuer
		taxableVP := vp * (1 - TeilfreistellungEquity)
		tax := taxableVP * EffectiveTaxRate

		entries = append(entries, VorabpauschaleEntry{
			ISIN:           isin,
			Name:           names[isin],
			Jan1Value:      math.Round(jan1Val*100) / 100,
			YearEndValue:   math.Round(yearEndVal*100) / 100,
			Basiszins:      basiszins,
			Basisertrag:    math.Round(basisertrag*100) / 100,
			Vorabpauschale: math.Round(vp*100) / 100,
			TaxOnVP:        math.Round(tax*100) / 100,
		})
	}
	return entries
}

// TaxLot represents an open FIFO lot with tax implications.
type TaxLot struct {
	ISIN          string  `json:"isin"`
	Name          string  `json:"name"`
	BuyDate       string  `json:"buy_date"`
	Quantity      float64 `json:"quantity"`
	CostBasis     float64 `json:"cost_basis"`
	CurrentValue  float64 `json:"current_value"`
	UnrealizedPL  float64 `json:"unrealized_pl"`
	IsEquityFund  bool    `json:"is_equity_fund"`
	TaxIfSold     float64 `json:"tax_if_sold"`     // estimated tax after Teilfreistellung
	NetProceeds   float64 `json:"net_proceeds"`     // current value minus tax
	EffectiveRate float64 `json:"effective_rate"`    // tax / gain %
}

// ComputeTaxLots reconstructs FIFO lots from transactions and computes tax impact.
func ComputeTaxLots(txns []TaxTransaction, prices map[string]float64, names map[string]string, equityMap map[string]bool) []TaxLot {
	type lot struct {
		buyDate  string
		qty      float64
		costPer  float64
	}
	lots := make(map[string][]lot) // ISIN -> FIFO queue

	// Sort chronologically (assume already sorted)
	for _, txn := range txns {
		if txn.ISIN == "" {
			continue
		}
		switch txn.Type {
		case "buy", "savings_plan", "transfer":
			if txn.Quantity > 0 {
				costPer := txn.Amount / txn.Quantity
				lots[txn.ISIN] = append(lots[txn.ISIN], lot{
					buyDate: fmt.Sprintf("%d-%02d-%02d", txn.Year, 1, 1), // approximate
					qty:     txn.Quantity,
					costPer: costPer,
				})
			}
		case "sell", "transfer_out":
			remaining := txn.Quantity
			for remaining > 0 && len(lots[txn.ISIN]) > 0 {
				l := &lots[txn.ISIN][0]
				if l.qty <= remaining {
					remaining -= l.qty
					lots[txn.ISIN] = lots[txn.ISIN][1:]
				} else {
					l.qty -= remaining
					remaining = 0
				}
			}
		}
	}

	// Build tax lot entries
	var result []TaxLot
	for isin, isinLots := range lots {
		price := prices[isin]
		isEquity := equityMap[isin]
		name := names[isin]
		if name == "" {
			name = isin
		}

		for _, l := range isinLots {
			if l.qty <= 0.001 {
				continue
			}
			costBasis := l.qty * l.costPer
			currentValue := l.qty * price
			if price == 0 {
				currentValue = costBasis
			}
			unrealizedPL := currentValue - costBasis

			// Tax computation
			taxIfSold := 0.0
			if unrealizedPL > 0 {
				taxable := unrealizedPL
				if isEquity {
					taxable *= (1 - TeilfreistellungEquity)
				}
				taxIfSold = taxable * EffectiveTaxRate
			}

			effectiveRate := 0.0
			if unrealizedPL > 0 {
				effectiveRate = (taxIfSold / unrealizedPL) * 100
			}

			result = append(result, TaxLot{
				ISIN:          isin,
				Name:          name,
				BuyDate:       l.buyDate,
				Quantity:      math.Round(l.qty*1000) / 1000,
				CostBasis:     math.Round(costBasis*100) / 100,
				CurrentValue:  math.Round(currentValue*100) / 100,
				UnrealizedPL:  math.Round(unrealizedPL*100) / 100,
				IsEquityFund:  isEquity,
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
		if req.AmountEUR <= 0 {
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
		tf := 0.0
		if isEquity && gain > 0 {
			tf = gain * TeilfreistellungEquity
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

// ComputeLossPots calculates the German loss offset pots per year from transactions.
func ComputeLossPots(txns []TaxTransaction) []LossPotYear {
	type lot struct {
		qty     float64
		costPer float64
	}
	holdings := make(map[string]*struct{ qty, totalCost float64 })

	yearEquityLosses := make(map[int]float64)
	yearEquityGains := make(map[int]float64)
	yearGeneralLosses := make(map[int]float64)
	yearGeneralGains := make(map[int]float64)
	years := make(map[int]bool)

	for _, txn := range txns {
		years[txn.Year] = true
		switch txn.Type {
		case "buy", "savings_plan":
			if txn.ISIN == "" { continue }
			h, ok := holdings[txn.ISIN]
			if !ok {
				h = &struct{ qty, totalCost float64 }{}
				holdings[txn.ISIN] = h
			}
			h.qty += txn.Quantity
			h.totalCost += txn.Amount

		case "transfer":
			// Transfers update holdings for cost basis tracking but are NOT taxable events
			if txn.ISIN == "" { continue }
			h, ok := holdings[txn.ISIN]
			if !ok {
				h = &struct{ qty, totalCost float64 }{}
				holdings[txn.ISIN] = h
			}
			h.qty += txn.Quantity
			h.totalCost += txn.Amount

		case "transfer_out":
			// Transfer out: reduce holdings but NO realized gain/loss
			if txn.ISIN == "" { continue }
			h, ok := holdings[txn.ISIN]
			if !ok || h.qty <= 0 { continue }
			avgCost := h.totalCost / h.qty
			h.qty -= txn.Quantity
			h.totalCost -= txn.Quantity * avgCost
			if h.qty <= 0.001 { delete(holdings, txn.ISIN) }

		case "sell":
			if txn.ISIN == "" { continue }
			h, ok := holdings[txn.ISIN]
			if !ok || h.qty <= 0 { continue }
			avgCost := h.totalCost / h.qty
			costBasis := txn.Quantity * avgCost
			pl := txn.Amount - costBasis
			h.qty -= txn.Quantity
			h.totalCost -= costBasis
			if h.qty <= 0.001 { delete(holdings, txn.ISIN) }

			// Apply Teilfreistellung to equity gains/losses
			if txn.IsEquityFund {
				pl *= (1 - TeilfreistellungEquity) // only 70% is taxable
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
			amt := txn.Amount
			if txn.IsEquityFund {
				amt *= (1 - TeilfreistellungEquity)
			}
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
	Year         int
	Type         string  // "buy", "sell", "dividend", "savings_plan", "transfer"
	ISIN         string
	Name         string
	Quantity     float64
	Amount       float64
	IsEquityFund bool
}

// ComputeTaxSummary calculates the German tax summary for a given year.
// It takes all transactions (for cost basis tracking) and filters results to the target year.
func ComputeTaxSummary(allTxns []TaxTransaction, year int, unrealizedPositions []TaxLossHint) *TaxSummary {
	type lot struct {
		quantity  float64
		totalCost float64
	}
	holdings := make(map[string]*lot)

	var gains, losses, dividends, equityDividends float64
	var bySec []TaxBySecurity
	secNames := make(map[string]string)
	secEquity := make(map[string]bool)

	for _, txn := range allTxns {
		secNames[txn.ISIN] = txn.Name
		secEquity[txn.ISIN] = txn.IsEquityFund

		switch txn.Type {
		case "buy", "savings_plan", "transfer":
			if txn.ISIN == "" {
				continue
			}
			l, ok := holdings[txn.ISIN]
			if !ok {
				l = &lot{}
				holdings[txn.ISIN] = l
			}
			l.quantity += txn.Quantity
			l.totalCost += txn.Amount

		case "transfer_out":
			// Transfer out: reduce holdings but NO realized gain/loss
			if txn.ISIN == "" { continue }
			l, ok := holdings[txn.ISIN]
			if !ok || l.quantity <= 0 { continue }
			avgCost := l.totalCost / l.quantity
			l.quantity -= txn.Quantity
			l.totalCost -= txn.Quantity * avgCost
			if l.quantity <= 0.001 { delete(holdings, txn.ISIN) }

		case "sell":
			if txn.ISIN == "" {
				continue
			}
			l, ok := holdings[txn.ISIN]
			if !ok || l.quantity <= 0 {
				continue
			}
			avgCost := l.totalCost / l.quantity
			costBasis := txn.Quantity * avgCost
			realizedPL := txn.Amount - costBasis

			l.quantity -= txn.Quantity
			l.totalCost -= costBasis
			if l.quantity <= 0.001 {
				delete(holdings, txn.ISIN)
			}

			// Only count gains/losses for the target year
			if txn.Year == year {
				if realizedPL >= 0 {
					gains += realizedPL
				} else {
					losses += realizedPL
				}
				bySec = append(bySec, TaxBySecurity{
					ISIN:         txn.ISIN,
					Name:         txn.Name,
					RealizedPL:   math.Round(realizedPL*100) / 100,
					IsEquityFund: txn.IsEquityFund,
				})
			}

		case "dividend":
			if txn.Year == year {
				dividends += txn.Amount
				if txn.IsEquityFund {
					equityDividends += txn.Amount
				}
			}
		}
	}

	netGain := gains + losses // losses are negative

	// Apply Teilfreistellung per security
	teilfreistellungTotal := 0.0
	for i, s := range bySec {
		if s.IsEquityFund && s.RealizedPL > 0 {
			tf := s.RealizedPL * TeilfreistellungEquity
			bySec[i].Teilfreistellung = math.Round(tf*100) / 100
			bySec[i].TaxablePL = math.Round((s.RealizedPL-tf)*100) / 100
			teilfreistellungTotal += tf
		} else {
			bySec[i].TaxablePL = s.RealizedPL
		}
	}

	// Apply Teilfreistellung only to dividends from equity funds (30%), not bonds (0%)
	dividendTeilfreistellung := equityDividends * TeilfreistellungEquity
	taxableDividends := dividends - dividendTeilfreistellung
	teilfreistellungTotal += dividendTeilfreistellung

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
