package handler

import (
	"math"

	"github.com/lnsp/wealth/internal/analytics"
)

// capitalIncomeState is a tax year's realised capital income (§ 20 EStG) by
// loss pot, before the Sparer-Pauschbetrag. Raw values include the losses
// carried in from earlier years and may be negative.
type capitalIncomeState struct {
	shareRaw float64 // Aktientopf: share gains and losses (§ 20 Abs. 6 S. 4)
	otherRaw float64 // funds after Teilfreistellung, bonds, dividends, interest
}

// capitalIncomeFromPots reads the year's state from analytics.ComputeLossPots:
// the carry-forward out of the latest earlier year plus this year's gains
// and losses so far. The "Equity" fields are the Aktientopf.
func capitalIncomeFromPots(pots []analytics.LossPotYear, year int) capitalIncomeState {
	var st capitalIncomeState
	for _, p := range pots {
		if p.Year < year {
			st.shareRaw, st.otherRaw = p.CarryForwardEquity, p.CarryForwardGeneral
		}
	}
	for _, p := range pots {
		if p.Year == year {
			st.shareRaw += p.EquityGains + p.EquityLosses
			st.otherRaw += p.GeneralGains + p.GeneralLosses
		}
	}
	return st
}

// taxable is the income subject to tax before the allowance: share losses
// offset only share gains, other losses offset any capital income.
func (s capitalIncomeState) taxable() float64 {
	share := math.Max(0, s.shareRaw)
	if s.otherRaw < 0 {
		return math.Max(0, share+s.otherRaw)
	}
	return share + s.otherRaw
}

// taxDue is the tax on the income above the allowance.
func (s capitalIncomeState) taxDue(allowance, rate float64) float64 {
	return math.Max(0, s.taxable()-allowance) * rate
}

// harvestCandidate is a position whose unrealised loss could be realised.
type harvestCandidate struct {
	ISIN  string
	Name  string
	Loss  float64 // unrealised loss as a positive amount
	TFS   float64 // Teilfreistellung; a fund loss counts only (1 − TFS)
	Share bool    // a share (Aktie): the loss goes to the Aktientopf
}

// harvestSavings is the tax each candidate saves this year when realised
// now, in the given order: each loss can only offset gains realised this
// year in a pot it may offset (share losses only share gains) and above the
// allowance, and gains one candidate offsets are gone for the next. A loss
// that finds nothing to offset saves nothing now — it only grows the
// carry-forward, which is timing, not a saving.
func harvestSavings(cands []harvestCandidate, st capitalIncomeState, allowance, rate float64) []float64 {
	out := make([]float64, len(cands))
	for i, c := range cands {
		before := st.taxDue(allowance, rate)
		loss := math.Max(0, c.Loss) * (1 - c.TFS)
		if c.Share {
			st.shareRaw -= loss
		} else {
			st.otherRaw -= loss
		}
		out[i] = before - st.taxDue(allowance, rate)
	}
	return out
}

// allowanceGainRoom is how much of the allowance realising the given
// unrealised gains (after Teilfreistellung) would use: the gains covered by
// the Sparer-Pauschbetrag rather than by carried losses, which are tax-free
// for good. st should already include the income still expected this year.
func allowanceGainRoom(st capitalIncomeState, shareGains, otherGains, allowance float64) float64 {
	before := math.Min(allowance, st.taxable())
	st.shareRaw += math.Max(0, shareGains)
	st.otherRaw += math.Max(0, otherGains)
	return math.Max(0, math.Min(allowance, st.taxable())-before)
}

// vorabpauschaleTax is the tax a year's Vorabpauschale triggers. It is deemed
// received on the first working day of the next year (§ 18 Abs. 3 InvStG),
// so after each fund's Teilfreistellung it is set against that year's fresh
// Sparer-Pauschbetrag (nextAllowance).
func vorabpauschaleTax(entries []analytics.VorabpauschaleEntry, tfs map[string]float64, nextAllowance, rate float64) (taxable, tax float64) {
	for _, e := range entries {
		taxable += math.Max(0, e.Vorabpauschale) * (1 - tfs[e.ISIN])
	}
	return taxable, math.Max(0, taxable-nextAllowance) * rate
}
