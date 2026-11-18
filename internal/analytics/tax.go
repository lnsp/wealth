package analytics

import (
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
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
	// Vanguard LifeStrategy funds carry their equity share in the name
	// ("LifeStrategy 20% Equity"): 20 % → Sonstige (0), 40 % → Mischfonds
	// (15 %), 60 % / 80 % → Aktienfonds (30 %).
	if m := lifeStrategyEquityRe.FindStringSubmatch(lowerName); m != nil {
		if n, err := strconv.Atoi(m[1]); err == nil && n <= 100 {
			return TeilfreistellungRate(float64(n) / 100)
		}
	}
	for _, kw := range []string{
		"money market", "overnight", "geldmarkt", "xeon",
		"staatsanleihe", "govt bond", "government bond",
		"corporate bond", "aggregate bond", "eur bond", "usd bond",
		"global bond", "anleihen", "pfandbrief", "ultrashort bond",
		"short duration", "physical gold", "gold etc",
	} {
		if strings.Contains(lowerName, kw) {
			return TeilfreistellungBond
		}
	}
	// Whole-word bond markers: "EUR Corp Bond", "Prime Euro Govies",
	// "Treasury 1-3", "Inflation Linked Govt".
	for _, tok := range strings.FieldsFunc(lowerName, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		switch tok {
		case "bond", "bonds", "govies", "govt", "government", "treasury", "treasuries", "corp", "rentenfonds":
			return TeilfreistellungBond
		}
	}
	for _, kw := range []string{"mischfonds", "multi-asset"} {
		if strings.Contains(lowerName, kw) {
			return TeilfreistellungMixed
		}
	}
	return SecurityTeilfreistellungRate(assetClass, nil)
}

var lifeStrategyEquityRe = regexp.MustCompile(`lifestrategy\s*(\d{1,3})\s*%?`)

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

// Basiszins published by the BMF from Bundesbank data (§18 Abs. 4 InvStG),
// used for the Vorabpauschale. Negative years (2021, 2022) yield none.
// Sources: BMF 9.1.2019 (2019), 29.1.2020 (2020), 6.1.2021 (2021),
// 7.1.2022 (2022), 13.1.2026 (2026); Haufe table of Basiszinsen 2018-2025.
var BasiszinsByYear = map[int]float64{
	2018: 0.87, 2019: 0.52, 2020: 0.07, 2021: -0.45,
	2022: -0.05, 2023: 2.55, 2024: 2.29, 2025: 2.53, 2026: 3.20, // 2026: BMF 13.01.2026
}

// VorabpauschaleEntry holds per-fund Vorabpauschale data for one calendar
// year. It is deemed received on the first working day of the next year
// (§18 Abs. 3 InvStG), so the entry for year Y is taxed in January Y+1.
type VorabpauschaleEntry struct {
	Year           int     `json:"year,omitempty"`
	ISIN           string  `json:"isin"`
	Name           string  `json:"name"`
	Units          float64 `json:"units,omitempty"`          // units held at year end
	Jan1Price      float64 `json:"jan1_price,omitempty"`     // redemption price at the start of the year
	YearEndPrice   float64 `json:"year_end_price,omitempty"` // redemption price at the end of the year
	Jan1Value      float64 `json:"jan1_value"`               // year-end units valued at the start-of-year price
	YearEndValue   float64 `json:"year_end_value"`
	Basiszins      float64 `json:"basiszins"`
	Basisertrag    float64 `json:"basisertrag"` // full-year Basisertrag before the cap and pro-rating
	Distributions  float64 `json:"distributions,omitempty"`
	Vorabpauschale float64 `json:"vorabpauschale"` // after the cap, distributions and 1/12 pro-rating
	TFSRate        float64 `json:"tfs_rate,omitempty"`
	TaxableVP      float64 `json:"taxable_vp,omitempty"` // after Teilfreistellung
	TaxOnVP        float64 `json:"tax_on_vp"`            // after Teilfreistellung + Abgeltungssteuer
}

// VorabpauschaleLotInput describes a mid-year fund purchase for 1/12 pro-rating (§ 18 Abs. 2 InvStG).
type VorabpauschaleLotInput struct {
	BuyMonth int     // 1..12
	ValueJan float64 // these units at the start-of-year redemption price; purchase cost is an approximation when that price is unknown
	Quantity float64 // units still held at year end
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
						Quantity: ml.qty,
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

// ComputeVorabpauschaleDetailed computes the § 18 InvStG Vorabpauschale per
// fund from aggregate values. All units held at year end share the same
// per-unit prices, so the law's per-unit rule aggregates to:
//
//	base        = value of the year-end units at the start-of-year price
//	              (Jan1Value + Σ MidYearBuys.ValueJan)
//	Basisertrag = base × Basiszins × 0.7                     (Abs. 1 S. 2)
//	cap         = max(0, YearEndValue − base + Distributions) (Abs. 1 S. 3)
//	full-year   = max(0, min(Basisertrag, cap) − Distributions)
//	VP          = full-year × Σ(unit share × (13 − buy month)/12)  (Abs. 2)
//
// The 1/12 reduction applies to the Vorabpauschale after the cap, not to
// the Basisertrag, and to the units' start-of-year value, not their
// purchase cost.
func ComputeVorabpauschaleDetailed(year int, inputs []VorabpauschaleInput) []VorabpauschaleEntry {
	basiszins, ok := BasiszinsByYear[year]
	if !ok || basiszins <= 0 {
		return nil // negative Basiszins = no Vorabpauschale
	}

	var entries []VorabpauschaleEntry
	for _, in := range inputs {
		base := math.Max(0, in.Jan1Value)
		weighted := base
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
			base += b.ValueJan
			weighted += b.ValueJan * float64(13-m) / 12.0
		}
		if base <= 0 || in.YearEndValue <= 0 {
			continue
		}
		dist := math.Max(0, in.Distributions)

		basisertrag := base * basiszins / 100 * 0.7
		capAmt := math.Max(0, in.YearEndValue-base+dist)
		fullYear := math.Max(0, math.Min(basisertrag, capAmt)-dist)
		vp := fullYear * weighted / base

		taxableVP := vp * (1 - in.TFSRate)
		tax := taxableVP * EffectiveTaxRate

		entries = append(entries, VorabpauschaleEntry{
			Year:           year,
			ISIN:           in.ISIN,
			Name:           in.Name,
			Jan1Value:      math.Round(base*100) / 100,
			YearEndValue:   math.Round(in.YearEndValue*100) / 100,
			Basiszins:      basiszins,
			Basisertrag:    math.Round(basisertrag*100) / 100,
			Distributions:  math.Round(dist*100) / 100,
			Vorabpauschale: math.Round(vp*100) / 100,
			TFSRate:        in.TFSRate,
			TaxableVP:      math.Round(taxableVP*100) / 100,
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
	BuyDate       string  `json:"buy_date"` // original acquisition date (kept across in-kind depot transfers)
	Quantity      float64 `json:"quantity"`
	CostBasis     float64 `json:"cost_basis"`
	CurrentValue  float64 `json:"current_value"`
	UnrealizedPL  float64 `json:"unrealized_pl"`
	IsEquityFund  bool    `json:"is_equity_fund"`
	TFSRate       float64 `json:"tfs_rate,omitempty"`
	TaxIfSold     float64 `json:"tax_if_sold"`    // this lot alone, after Teilfreistellung, before allowance and loss pots (0 for §23 assets)
	NetProceeds   float64 `json:"net_proceeds"`   // current value minus TaxIfSold
	EffectiveRate float64 `json:"effective_rate"` // tax / gain %

	AssetClass          string  `json:"asset_class,omitempty"`
	Pot                 string  `json:"pot"`                            // "share" (Aktienverlusttopf), "general" or "private_sale" (§23)
	PrivateSale         bool    `json:"private_sale,omitempty"`         // §23 EStG: crypto or physically backed commodity
	TaxFreeFrom         string  `json:"tax_free_from,omitempty"`        // §23: first day a sale is tax-free
	VorabpauschaleTaxed float64 `json:"vorabpauschale_taxed,omitempty"` // Vorabpauschalen already taxed on these units, deducted from the gain on sale
	// FIFOTaxIfSold is the tax (after Teilfreistellung, before allowance and
	// loss pots) of selling this lot together with every older lot of the
	// same security in the same depot, which FIFO forces out first.
	FIFOTaxIfSold float64 `json:"fifo_tax_if_sold"`
}

// taxablePL is the lot's P/L on sale at current value, after the Vorabpauschale deduction and Teilfreistellung.
func (l TaxLot) taxablePL() float64 {
	return (l.CurrentValue - l.CostBasis - l.VorabpauschaleTaxed) * (1 - l.TFSRate)
}

func lotPot(assetClass string) string {
	switch {
	case IsPrivateSaleAssetClass(assetClass):
		return "private_sale"
	case IsShareAssetClass(assetClass):
		return "share"
	default:
		return "general"
	}
}

// ComputeTaxLots reconstructs per-depot FIFO lots from transactions and computes tax impact.
// Matched in-kind transfers keep the original cost basis and acquisition date.
func ComputeTaxLots(txns []TaxTransaction, prices map[string]float64, names map[string]string, equityMap map[string]bool) []TaxLot {
	l := runTaxLedger(txns)

	var result []TaxLot
	for _, k := range l.keyOrder {
		isin := k.isin
		price := prices[isin]
		isEquity := equityMap[isin]
		name := names[isin]
		if name == "" {
			name = l.names[isin]
		}
		if name == "" {
			name = isin
		}

		cumTaxable := 0.0 // running taxable P/L of selling every lot up to this one (FIFO)
		for _, lot := range l.lots[k] {
			if lot.qty <= 1e-9 {
				continue
			}
			costBasis := lot.qty * lot.costPer
			currentValue := lot.qty * price
			if price == 0 {
				currentValue = costBasis
			}
			unrealizedPL := currentValue - costBasis
			vpTaxed := lot.qty * lot.vpPerUnit

			tfs := 0.0
			if lot.hasTFS {
				tfs = lot.tfs
			} else if isEquity {
				tfs = TeilfreistellungEquity
			}

			private := IsPrivateSaleAssetClass(lot.assetClass)
			taxablePL := (unrealizedPL - vpTaxed) * (1 - tfs)
			taxIfSold := 0.0
			fifoTax := 0.0
			if !private {
				if taxablePL > 0 {
					taxIfSold = taxablePL * EffectiveTaxRate
				}
				cumTaxable += taxablePL
				fifoTax = math.Max(0, cumTaxable) * EffectiveTaxRate
			}

			effectiveRate := 0.0
			if unrealizedPL > 0 {
				effectiveRate = (taxIfSold / unrealizedPL) * 100
			}

			tl := TaxLot{
				AccountID:           k.acct,
				ISIN:                isin,
				Name:                name,
				BuyDate:             lot.acqDate.Format("2006-01-02"),
				Quantity:            math.Round(lot.qty*1e8) / 1e8,
				CostBasis:           round2(costBasis),
				CurrentValue:        round2(currentValue),
				UnrealizedPL:        round2(unrealizedPL),
				IsEquityFund:        isEquity || tfs >= TeilfreistellungEquity,
				TFSRate:             tfs,
				TaxIfSold:           round2(taxIfSold),
				NetProceeds:         round2(currentValue - taxIfSold),
				EffectiveRate:       math.Round(effectiveRate*10) / 10,
				AssetClass:          lot.assetClass,
				Pot:                 lotPot(lot.assetClass),
				PrivateSale:         private,
				VorabpauschaleTaxed: round2(vpTaxed),
				FIFOTaxIfSold:       round2(fifoTax),
			}
			if private {
				tl.TaxFreeFrom = PrivateSaleTaxFreeFrom(lot.acqDate).Format("2006-01-02")
			}
			result = append(result, tl)
		}
	}
	return result
}

// SellAllSummary is the tax of selling every open lot today, netted per pot.
type SellAllSummary struct {
	SharePL              float64 `json:"share_pl"`                // stock lots (Aktienverlusttopf)
	GeneralPL            float64 `json:"general_pl"`              // other §20 lots, after Teilfreistellung
	TaxableGain          float64 `json:"taxable_gain"`            // after netting the pots, before allowance and carried losses
	GrossTax             float64 `json:"gross_tax"`               // before the Sparer-Pauschbetrag and loss carry-forwards
	TaxThisYear          float64 `json:"tax_this_year"`           // increase in this year's tax after allowance and loss pots (set by the handler)
	PrivateSaleTaxablePL float64 `json:"private_sale_taxable_pl"` // §23 lots held ≤ 1 year: personal income-tax rate
	PrivateSaleTaxFreePL float64 `json:"private_sale_tax_free_pl"`
	TaxRate              float64 `json:"tax_rate"`
}

// SummarizeSellAll nets the P/L of selling every lot per pot: share losses
// only offset share gains, other losses offset anything, Teilfreistellung
// applies to fund gains and losses alike, and §23 assets stay outside.
func SummarizeSellAll(lots []TaxLot, asOf time.Time, churchTaxRate float64) SellAllSummary {
	rate := CapitalIncomeTaxRate(churchTaxRate)
	var s SellAllSummary
	for _, l := range lots {
		if l.PrivateSale {
			pl := l.CurrentValue - l.CostBasis
			acq, err := time.Parse("2006-01-02", l.BuyDate)
			if err == nil && HeldOverOneYear(acq, asOf) {
				s.PrivateSaleTaxFreePL += pl
			} else {
				s.PrivateSaleTaxablePL += pl
			}
			continue
		}
		if IsShareAssetClass(l.AssetClass) {
			s.SharePL += l.taxablePL()
		} else {
			s.GeneralPL += l.taxablePL()
		}
	}
	share, general := s.SharePL, s.GeneralPL
	if share > 0 && general < 0 {
		o := math.Min(share, -general)
		share -= o
		general += o
	}
	s.TaxableGain = math.Max(0, share) + math.Max(0, general)
	s.GrossTax = s.TaxableGain * rate
	s.TaxRate = rate
	s.SharePL = round2(s.SharePL)
	s.GeneralPL = round2(s.GeneralPL)
	s.TaxableGain = round2(s.TaxableGain)
	s.GrossTax = round2(s.GrossTax)
	s.PrivateSaleTaxablePL = round2(s.PrivateSaleTaxablePL)
	s.PrivateSaleTaxFreePL = round2(s.PrivateSaleTaxFreePL)
	return s
}

// HypotheticalSellTxns returns one sell transaction per depot and security
// that liquidates the given lots at their current value on asOf. Append them
// to the transaction history to price a sale through the full tax engine.
func HypotheticalSellTxns(lots []TaxLot, asOf time.Time) []TaxTransaction {
	takes := make([]lotTake, 0, len(lots))
	for _, l := range lots {
		takes = append(takes, lotTake{lot: l, value: l.CurrentValue, fraction: 1})
	}
	return sellTxnsFromTakes(takes, asOf)
}

// SimulatedSellTxns converts sell requests into sell transactions dated asOf,
// consuming lots in the same order as SimulateSell.
func SimulatedSellTxns(lots []TaxLot, requests []SellRequest, asOf time.Time) []TaxTransaction {
	lotsByISIN := make(map[string][]TaxLot)
	for _, l := range lots {
		lotsByISIN[l.ISIN] = append(lotsByISIN[l.ISIN], l)
	}
	var takes []lotTake
	for _, req := range requests {
		if req.AmountEUR <= 0 || math.IsNaN(req.AmountEUR) || math.IsInf(req.AmountEUR, 0) {
			continue
		}
		takes = append(takes, walkSellRequest(lotsByISIN[req.ISIN], req.AmountEUR)...)
	}
	return sellTxnsFromTakes(takes, asOf)
}

type lotTake struct {
	lot      TaxLot
	value    float64 // EUR sold from this lot
	fraction float64 // share of the lot sold
}

// walkSellRequest sells `amount` EUR of a security's lots, oldest first.
func walkSellRequest(isinLots []TaxLot, amount float64) []lotTake {
	var takes []lotTake
	remaining := amount
	for _, lot := range isinLots {
		if remaining <= 0 {
			break
		}
		if lot.CurrentValue <= 0 || math.IsNaN(lot.CurrentValue) || math.IsInf(lot.CurrentValue, 0) {
			continue
		}
		sellFromLot := math.Min(remaining, lot.CurrentValue)
		takes = append(takes, lotTake{lot: lot, value: sellFromLot, fraction: sellFromLot / lot.CurrentValue})
		remaining -= sellFromLot
	}
	return takes
}

func sellTxnsFromTakes(takes []lotTake, asOf time.Time) []TaxTransaction {
	type key struct{ acct, isin string }
	agg := make(map[key]*TaxTransaction)
	var order []key
	for _, t := range takes {
		if t.fraction <= 0 {
			continue
		}
		k := key{t.lot.AccountID, t.lot.ISIN}
		tx, ok := agg[k]
		if !ok {
			tx = &TaxTransaction{
				AccountID:      t.lot.AccountID,
				Date:           asOf.Format("2006-01-02"),
				Year:           asOf.Year(),
				Type:           "sell",
				ISIN:           t.lot.ISIN,
				Name:           t.lot.Name,
				IsEquityFund:   t.lot.IsEquityFund,
				AssetClass:     t.lot.AssetClass,
				TFS:            t.lot.TFSRate,
				HasExplicitTFS: true,
			}
			agg[k] = tx
			order = append(order, k)
		}
		tx.Quantity += t.lot.Quantity * t.fraction
		tx.Amount += t.value
	}
	out := make([]TaxTransaction, 0, len(order))
	for _, k := range order {
		out = append(out, *agg[k])
	}
	return out
}

// MarginalSaleTax prices hypothetical sales through the full engine: it
// returns the year's estimated tax without and with them, so the difference
// accounts for Teilfreistellung, both loss pots, carried-forward losses and
// the remaining Sparer-Pauschbetrag.
func MarginalSaleTax(txns, sales []TaxTransaction, year int, opts TaxOptions) (without, with float64) {
	base := ComputeTaxSummaryWithOptions(txns, year, nil, opts)
	all := make([]TaxTransaction, 0, len(txns)+len(sales))
	all = append(all, txns...)
	all = append(all, sales...)
	withSales := ComputeTaxSummaryWithOptions(all, year, nil, opts)
	return base.EstimatedTax, withSales.EstimatedTax
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
	RealizedGain     float64 `json:"realized_gain"`    // after deducting Vorabpauschalen already taxed
	Teilfreistellung float64 `json:"teilfreistellung"` // exempt part of the gain (negative on a loss)
	TaxableGain      float64 `json:"taxable_gain"`     // after Teilfreistellung (negative on a loss)
	EstimatedTax     float64 `json:"estimated_tax"`    // this sale alone, before allowance and loss pots
	NetProceeds      float64 `json:"net_proceeds"`
	LotsConsumed     int     `json:"lots_consumed"`
	IsEquityFund     bool    `json:"is_equity_fund"`

	TFSRate                float64 `json:"tfs_rate"`
	Pot                    string  `json:"pot"`
	VorabpauschaleDeducted float64 `json:"vorabpauschale_deducted,omitempty"`
	PrivateSale            bool    `json:"private_sale,omitempty"`            // §23: not Abgeltungsteuer
	PrivateSaleTaxablePL   float64 `json:"private_sale_taxable_pl,omitempty"` // lots held ≤ 1 year: personal income-tax rate
	PrivateSaleTaxFreePL   float64 `json:"private_sale_tax_free_pl,omitempty"`
}

// SellTaxRate returns the effective rate on a taxable capital gain given an
// optional church-tax rate (KiSt). Kirchensteuer is deductible from the
// Abgeltungsteuer base (§32d Abs. 1 EStG), see CapitalIncomeTaxRate:
//
//	no church: 26.375 %, 8 % (BY/BW): 27.8186 %, 9 %: 27.9951 %
//
// churchTaxRate is silently clamped to [0, 0.09].
func SellTaxRate(churchTaxRate float64) float64 {
	return CapitalIncomeTaxRate(churchTaxRate)
}

// SimulateSell computes the tax impact of selling specified EUR amounts using
// FIFO lot order. churchTaxRate is the Kirchensteuer rate (typically 0, 0.08
// BY/BW, or 0.09 other Bundesländer).
//
// Each result's EstimatedTax is that sale alone. The returned total nets all
// results per loss pot (share losses only against share gains), so a loss in
// one position lowers the tax on a gain in another. Both are before the
// Sparer-Pauschbetrag and carried-forward losses; see MarginalSaleTax for
// the figure after them. §23 assets (crypto, physical gold) are not taxed
// here; their short- and long-term P/L is reported instead.
func SimulateSell(lots []TaxLot, requests []SellRequest, churchTaxRate float64) ([]SellSimResult, float64, float64) {
	return SimulateSellAt(lots, requests, churchTaxRate, time.Now())
}

// SimulateSellAt is SimulateSell with an explicit sale date (for the §23 holding period).
func SimulateSellAt(lots []TaxLot, requests []SellRequest, churchTaxRate float64, asOf time.Time) ([]SellSimResult, float64, float64) {
	effectiveRate := SellTaxRate(churchTaxRate)
	// Group lots by ISIN in order
	lotsByISIN := make(map[string][]TaxLot)
	for _, l := range lots {
		lotsByISIN[l.ISIN] = append(lotsByISIN[l.ISIN], l)
	}

	var results []SellSimResult
	totalSold := 0.0
	shareNet, generalNet := 0.0, 0.0

	for _, req := range requests {
		if req.AmountEUR <= 0 || math.IsNaN(req.AmountEUR) || math.IsInf(req.AmountEUR, 0) {
			continue
		}
		isinLots := lotsByISIN[req.ISIN]
		if len(isinLots) == 0 {
			continue
		}
		takes := walkSellRequest(isinLots, req.AmountEUR)

		first := isinLots[0]
		sold, cost, vp, shortPL, longPL := 0.0, 0.0, 0.0, 0.0, 0.0
		for _, t := range takes {
			c := t.fraction * t.lot.CostBasis
			v := t.fraction * t.lot.VorabpauschaleTaxed
			sold += t.value
			cost += c
			vp += v
			if t.lot.PrivateSale {
				acq, err := time.Parse("2006-01-02", t.lot.BuyDate)
				if err == nil && HeldOverOneYear(acq, asOf) {
					longPL += t.value - c
				} else {
					shortPL += t.value - c
				}
			}
		}

		gain := sold - cost - vp
		tfsRate := first.TFSRate
		if tfsRate == 0 && first.IsEquityFund {
			tfsRate = TeilfreistellungEquity
		}
		r := SellSimResult{
			ISIN:         req.ISIN,
			Name:         first.Name,
			SellAmount:   round2(sold),
			CostBasis:    round2(cost),
			RealizedGain: round2(gain),
			LotsConsumed: len(takes),
			IsEquityFund: first.IsEquityFund,
			Pot:          lotPot(first.AssetClass),
		}
		if first.Pot != "" {
			r.Pot = first.Pot
		}
		tax := 0.0
		if first.PrivateSale {
			r.PrivateSale = true
			r.PrivateSaleTaxablePL = round2(shortPL)
			r.PrivateSaleTaxFreePL = round2(longPL)
		} else {
			tf := 0.0 // stays +0 (not −0) on a loss without exemption
			if tfsRate > 0 {
				tf = gain * tfsRate
			}
			taxable := gain - tf
			if taxable > 0 {
				tax = taxable * effectiveRate
			}
			r.TFSRate = tfsRate
			r.Teilfreistellung = round2(tf)
			r.TaxableGain = round2(taxable)
			r.VorabpauschaleDeducted = round2(vp)
			if r.Pot == "share" {
				shareNet += taxable
			} else {
				generalNet += taxable
			}
		}
		r.EstimatedTax = round2(tax)
		r.NetProceeds = round2(sold - tax)
		results = append(results, r)
		totalSold += sold
	}

	if shareNet > 0 && generalNet < 0 {
		o := math.Min(shareNet, -generalNet)
		shareNet -= o
		generalNet += o
	}
	totalTax := (math.Max(0, shareNet) + math.Max(0, generalNet)) * effectiveRate
	return results, round2(totalTax), round2(totalSold - totalTax)
}

// LossPotYear tracks the two German loss offset pots for a single tax year
// (§20 Abs. 6 EStG). The Equity* fields are the Aktienverlusttopf, which
// holds gains and losses from selling shares (stocks) only; everything else —
// fund/ETF and bond sales after Teilfreistellung, dividends, interest and
// Vorabpauschalen — is the general pot. §23 assets are excluded.
type LossPotYear struct {
	Year                     int     `json:"year"`
	EquityLosses             float64 `json:"equity_losses"`        // share-sale losses this year
	EquityGains              float64 `json:"equity_gains"`         // share-sale gains this year
	EquityBalance            float64 `json:"equity_balance"`       // this year's share gains + losses
	GeneralLosses            float64 `json:"general_losses"`       // other sale losses this year (after Teilfreistellung)
	GeneralGains             float64 `json:"general_gains"`        // other sale gains + dividends + interest + Vorabpauschale (after Teilfreistellung)
	GeneralBalance           float64 `json:"general_balance"`      // this year's general gains + losses
	CarryForwardEquity       float64 `json:"carry_forward_equity"` // share losses carried to next year
	CarryForwardGeneral      float64 `json:"carry_forward_general"`
	GeneralUsedAgainstShares float64 `json:"general_used_against_shares"` // general losses that offset share gains
	TaxableIncome            float64 `json:"taxable_income"`              // after both pots and carry-forwards, before the Sparer-Pauschbetrag
}

// ComputeLossPots calculates the German loss offset pots per year from transactions
// using per-depot FIFO cost basis tracking (§ 20 Abs. 4 Satz 7 EStG).
func ComputeLossPots(txns []TaxTransaction) []LossPotYear {
	l := runTaxLedger(txns)
	years := l.potYears()
	results := netLossPots(years)

	var sortedYears []int
	for y := range years {
		sortedYears = append(sortedYears, y)
	}
	sort.Ints(sortedYears)

	var result []LossPotYear
	for _, year := range sortedYears {
		p := years[year]
		r := results[year]
		genGain := p.generalIncome()
		result = append(result, LossPotYear{
			Year:                     year,
			EquityLosses:             round2(p.shareLosses),
			EquityGains:              round2(p.shareGains),
			EquityBalance:            round2(p.shareGains + p.shareLosses),
			GeneralLosses:            round2(p.genSaleLosses),
			GeneralGains:             round2(genGain),
			GeneralBalance:           round2(genGain + p.genSaleLosses),
			CarryForwardEquity:       round2(r.carryOutShare),
			CarryForwardGeneral:      round2(r.carryOutGeneral),
			GeneralUsedAgainstShares: round2(r.generalUsedAgainstShares),
			TaxableIncome:            round2(r.taxable),
		})
	}
	return result
}

// TaxOptions carries the user's filing settings.
type TaxOptions struct {
	Joint         bool    // Zusammenveranlagung: doubles the Sparer-Pauschbetrag
	ChurchTaxRate float64 // Kirchensteuer rate (0, 0.08 or 0.09)
}

// TaxSummary contains the computed tax data for a given year.
type TaxSummary struct {
	Year                int             `json:"year"`
	RealizedGains       float64         `json:"realized_gains"`  // §20 sale gains, after the Vorabpauschale deduction, before Teilfreistellung
	RealizedLosses      float64         `json:"realized_losses"` // §20 sale losses (negative)
	NetGain             float64         `json:"net_gain"`
	TeilfreistellungAmt float64         `json:"teilfreistellung_amt"` // net exempt amount on sales, dividends and Vorabpauschale (negative when fund losses dominate)
	TaxableGain         float64         `json:"taxable_gain"`         // after Teilfreistellung, both loss pots and carried losses; before the allowance
	FreistellungUsed    float64         `json:"freistellung_used"`
	FreistellungRemain  float64         `json:"freistellung_remaining"`
	EstimatedTax        float64         `json:"estimated_tax"`
	EffectiveRate       float64         `json:"effective_rate"` // estimated tax / net capital income, in percent
	DividendIncome      float64         `json:"dividend_income"`
	BySecurity          []TaxBySecurity `json:"by_security"`
	TaxLossHints        []TaxLossHint   `json:"tax_loss_hints,omitempty"`

	InterestIncome       float64 `json:"interest_income"`
	VorabpauschaleIncome float64 `json:"vorabpauschale_income"` // Vorabpauschale for Year−1, deemed received in January of Year (before Teilfreistellung)
	VorabpauschaleYear   int     `json:"vorabpauschale_year"`   // Year−1
	ShareGains           float64 `json:"share_gains"`           // Aktienverlusttopf: stock-sale gains
	ShareLosses          float64 `json:"share_losses"`
	GeneralGains         float64 `json:"general_gains"` // other §20 income after Teilfreistellung
	GeneralLosses        float64 `json:"general_losses"`
	LossCarryInShare     float64 `json:"loss_carry_in_share"` // losses carried in from earlier years (≤ 0)
	LossCarryInGeneral   float64 `json:"loss_carry_in_general"`
	LossCarryOutShare    float64 `json:"loss_carry_out_share"` // losses carried into next year (≤ 0)
	LossCarryOutGeneral  float64 `json:"loss_carry_out_general"`
	Allowance            float64 `json:"allowance"` // Sparer-Pauschbetrag for the year and filing status
	Joint                bool    `json:"joint"`
	ChurchTaxRate        float64 `json:"church_tax_rate"`
	TaxRate              float64 `json:"tax_rate"` // Abgeltungsteuer + Soli (+ KiSt), as a fraction

	PrivateSales *PrivateSaleSummary `json:"private_sales,omitempty"` // §23 EStG, taxed at the personal rate
}

// TaxBySecurity shows tax impact per security.
type TaxBySecurity struct {
	ISIN                   string  `json:"isin"`
	Name                   string  `json:"name"`
	RealizedPL             float64 `json:"realized_pl"`
	Teilfreistellung       float64 `json:"teilfreistellung"`
	TaxablePL              float64 `json:"taxable_pl"`
	IsEquityFund           bool    `json:"is_equity_fund"`
	Pot                    string  `json:"pot"` // "share" or "general"
	VorabpauschaleDeducted float64 `json:"vorabpauschale_deducted,omitempty"`
	AccountID              string  `json:"account_id,omitempty"`
	Date                   string  `json:"date,omitempty"`
}

// TaxLossHint suggests positions that could be sold to offset gains.
type TaxLossHint struct {
	ISIN            string  `json:"isin"`
	Name            string  `json:"name"`
	UnrealizedPL    float64 `json:"unrealized_pl"`
	PotentialSaving float64 `json:"potential_saving"` // reduction in this year's tax if the depot position were sold today

	AccountID   string  `json:"account_id,omitempty"`
	Account     string  `json:"account,omitempty"`
	TaxableLoss float64 `json:"taxable_loss"` // after Teilfreistellung
	Pot         string  `json:"pot"`          // "share" (offsets only share gains) or "general"
}

// TaxTransaction represents a transaction relevant for tax computation.
type TaxTransaction struct {
	AccountID      string // optional: scopes FIFO lots per depot
	Date           string // optional: YYYY-MM-DD purchase/trade date
	Year           int
	Type           string // "buy", "sell", "dividend", "savings_plan", "transfer", "transfer_out", "interest", "vorabpauschale"
	ISIN           string
	Name           string
	Quantity       float64
	Amount         float64
	IsEquityFund   bool
	AssetClass     string  // securities.asset_class; drives loss pot (IsShareAssetClass) and §23 exclusion (IsPrivateSaleAssetClass)
	TFS            float64 // optional explicit Teilfreistellung rate (0.30, 0.15, 0.00)
	HasExplicitTFS bool    // true when TFS was explicitly resolved from EquityPct

	// Type "vorabpauschale" pseudo-events (one per fund and calendar year,
	// dated 31 December, see MergeVorabpauschaleEvents): EUR redemption
	// price at the start of Year (the last price of Year−1) and at its end.
	VPJan1Price    float64
	VPYearEndPrice float64
}

// ComputeTaxSummary calculates the German tax summary for a given year for a
// single filer without church tax. See ComputeTaxSummaryWithOptions.
func ComputeTaxSummary(allTxns []TaxTransaction, year int, unrealizedPositions []TaxLossHint) *TaxSummary {
	return ComputeTaxSummaryWithOptions(allTxns, year, unrealizedPositions, TaxOptions{})
}

// ComputeTaxSummaryWithOptions calculates the German tax summary for a year.
// It replays all transactions (per-depot FIFO, in-kind transfers keeping
// their original lots) and then, for the target year:
//   - applies Teilfreistellung to fund gains, losses, distributions and
//     Vorabpauschalen (§20 InvStG);
//   - nets the share pot and the general pot with carry-forwards from earlier
//     years (§20 Abs. 6 EStG);
//   - adds interest and the previous year's Vorabpauschale (§18 Abs. 3 InvStG);
//   - deducts the year's Sparer-Pauschbetrag (801/1,000 €, doubled when joint);
//   - applies Abgeltungsteuer + Soli + church tax (§32d Abs. 1 EStG).
//
// Crypto and physically backed commodities are reported separately under
// PrivateSales (§23 EStG) and never enter the §20 figures.
func ComputeTaxSummaryWithOptions(allTxns []TaxTransaction, year int, unrealizedPositions []TaxLossHint, opts TaxOptions) *TaxSummary {
	l := runTaxLedger(allTxns)
	years := l.potYears()
	pots := potResultFor(netLossPots(years), year)
	p := years[year]
	if p == nil {
		p = &potYear{}
	}

	var bySec []TaxBySecurity
	for _, s := range l.sales {
		if s.Year != year || s.PrivateSale {
			continue
		}
		taxable := s.TaxablePL()
		pot := "general"
		if IsShareAssetClass(s.AssetClass) {
			pot = "share"
		}
		bySec = append(bySec, TaxBySecurity{
			ISIN:                   s.ISIN,
			Name:                   s.Name,
			RealizedPL:             round2(s.PL),
			Teilfreistellung:       round2(s.PL - taxable),
			TaxablePL:              round2(taxable),
			IsEquityFund:           s.IsEquityFund,
			Pot:                    pot,
			VorabpauschaleDeducted: round2(s.VorabpauschaleDeducted),
			AccountID:              s.AccountID,
			Date:                   s.Date,
		})
	}

	netGain := p.rawGains + p.rawLosses
	tfsTotal := p.saleTFS + p.dividendTFS + p.vpTFS
	taxableGain := pots.taxable

	allowance := SparerPauschbetragForYear(year, opts.Joint)
	freiUsed := math.Min(taxableGain, allowance)
	freiRemain := allowance - freiUsed
	rate := CapitalIncomeTaxRate(opts.ChurchTaxRate)
	estimatedTax := math.Max(0, taxableGain-freiUsed) * rate

	grossIncome := netGain + p.dividends + p.interest + p.vpIncome
	effectiveRate := 0.0
	if grossIncome > 0 {
		effectiveRate = estimatedTax / grossIncome * 100
	}

	church := opts.ChurchTaxRate
	if church < 0 {
		church = 0
	} else if church > 0.09 {
		church = 0.09
	}

	return &TaxSummary{
		Year:                year,
		RealizedGains:       round2(p.rawGains),
		RealizedLosses:      round2(p.rawLosses),
		NetGain:             round2(netGain),
		TeilfreistellungAmt: round2(tfsTotal),
		TaxableGain:         round2(taxableGain),
		FreistellungUsed:    round2(freiUsed),
		FreistellungRemain:  round2(freiRemain),
		EstimatedTax:        round2(estimatedTax),
		EffectiveRate:       round2(effectiveRate),
		DividendIncome:      round2(p.dividends),
		BySecurity:          bySec,
		TaxLossHints:        unrealizedPositions,

		InterestIncome:       round2(p.interest),
		VorabpauschaleIncome: round2(p.vpIncome),
		VorabpauschaleYear:   year - 1,
		ShareGains:           round2(p.shareGains),
		ShareLosses:          round2(p.shareLosses),
		GeneralGains:         round2(p.generalIncome()),
		GeneralLosses:        round2(p.genSaleLosses),
		LossCarryInShare:     round2(pots.carryInShare),
		LossCarryInGeneral:   round2(pots.carryInGeneral),
		LossCarryOutShare:    round2(pots.carryOutShare),
		LossCarryOutGeneral:  round2(pots.carryOutGeneral),
		Allowance:            allowance,
		Joint:                opts.Joint,
		ChurchTaxRate:        church,
		TaxRate:              math.Round(rate*1e6) / 1e6,

		PrivateSales: l.privateSales(year),
	}
}
