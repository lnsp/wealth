package handler

// Tax filing views: Anlage KAP / KAP-INV preparation, Freistellungsauftrag status
// and the tax calendar. The handlers in analysis.go load data and call the pure
// functions here, so tests exercise the same code the endpoints run.

import (
	"context"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/lnsp/wealth/internal/analytics"
	db "github.com/lnsp/wealth/internal/database/generated"
	"github.com/lnsp/wealth/internal/market"
)

/* ───────────── Account domicile ───────────── */

// institutionDomestic says whether an institution is a German paying agent
// (auszahlende Stelle, § 44 Abs. 1 S. 4 Nr. 1 EStG): it withholds
// Kapitalertragsteuer, issues a Steuerbescheinigung and accepts a
// Freistellungsauftrag (§ 44a EStG). Its income belongs on Anlage KAP
// lines 7-17 and its withheld tax on lines 37-39, copied from the certificate.
//
// Everything else is foreign: no German withholding, income goes on lines
// 18-26 (funds on Anlage KAP-INV), foreign withholding on lines 40-42, and no
// Freistellungsauftrag can be filed there.
//
// Institutions missing from this map (manual, other, delta) are assumed
// foreign and flagged, because the app cannot know who holds the assets.
var institutionDomestic = map[string]bool{
	"sparkasse":        true,
	"ing":              true,  // ING-DiBa AG
	"dkb":              true,  // Deutsche Kreditbank AG
	"comdirect":        true,  // Commerzbank AG
	"scalable_capital": true,  // Scalable Capital GmbH, custody at Baader Bank / Scalable Capital Bank
	"trade_republic":   true,  // Trade Republic Bank GmbH
	"n26":              true,  // N26 Bank AG
	"morgan_stanley":   false, // US broker (StockPlan Connect); no German withholding, US tax only via W-8BEN
	// Revolut Securities Europe UAB (Lithuania) does not withhold German
	// Kapitalertragsteuer; customers declare income from Revolut's annual
	// report on Anlage KAP. Source: capitalo.de/anbieter/revolut/produkte/depot
	// ("Die Kapitalertragsteuer wird nicht automatisch einbehalten"), checked
	// 2026-11-18. Revisit if Revolut starts issuing a Steuerbescheinigung.
	"revolut": false,
}

var institutionNames = map[string]string{
	"sparkasse":        "Sparkasse",
	"ing":              "ING",
	"dkb":              "DKB",
	"comdirect":        "comdirect",
	"scalable_capital": "Scalable Capital",
	"trade_republic":   "Trade Republic",
	"n26":              "N26",
	"morgan_stanley":   "Morgan Stanley",
	"revolut":          "Revolut",
}

// taxBank is the unit a German bank nets losses and applies a
// Freistellungsauftrag for: one institution. Accounts without a known
// institution are treated as a bank of their own.
type taxBank struct {
	Key      string
	Name     string
	Domestic bool
	Assumed  bool // domicile not known for this institution; assumed foreign
}

// accountTaxBank resolves the bank an account belongs to for filing purposes.
func accountTaxBank(a db.Account) taxBank {
	inst := strings.ToLower(strings.TrimSpace(a.Institution))
	domestic, known := institutionDomestic[inst]
	if !known {
		name := a.Name
		if name == "" {
			name = "Unknown account"
		}
		return taxBank{Key: "account:" + a.ID.String(), Name: name, Domestic: false, Assumed: true}
	}
	return taxBank{Key: inst, Name: institutionNames[inst], Domestic: domestic}
}

// accountOutsideKAP reports whether an account is a pension wrapper. bAV,
// Riester and Rürup contracts are taxed on payout under § 22 EStG, never as
// § 20 capital income, so they stay off Anlage KAP and the allowance.
func accountOutsideKAP(a db.Account) bool {
	switch a.TaxTreatment {
	case "bav", "riester", "rurup":
		return true
	}
	return false
}

/* ───────────── Classification ───────────── */

// Realised sales and income come from analytics.RealizedTaxEvents, the same
// FIFO ledger behind the Overview: per-depot lots, in-kind depot transfers
// keeping the original cost and date (§ 43a Abs. 2 S. 3 EStG), the
// Vorabpauschale as January income of the next year (§ 18 Abs. 3 InvStG) and
// deducted from later sale gains (§ 19 Abs. 1 S. 3 InvStG), and § 23 assets
// flagged as private sales.

// isFundAssetClass: investment funds under the InvStG (Anlage KAP-INV, Teilfreistellung).
func isFundAssetClass(assetClass string, tfs float64) bool {
	if assetClass == "" {
		return tfs > 0
	}
	return assetClass == "etf" || assetClass == "fund"
}

// fundType is the InvStG fund category that Anlage KAP-INV and the
// Teilfreistellung (§ 20 InvStG) distinguish.
type fundType int

const (
	fundAktien fundType = iota
	fundMisch
	fundImmobilien
	fundAuslandsImmobilien
	fundSonstige
	fundTypeCount
)

var fundTypeNames = [fundTypeCount]string{"Aktienfonds", "Mischfonds", "Immobilienfonds", "Auslands-Immobilienfonds", "sonstigen Investmentfonds"}

// fundTypeForTFS maps a Teilfreistellung rate back to its fund category
// (§ 20 Abs. 1-3 InvStG: 30 % equity, 15 % mixed, 60 % / 80 % real estate).
func fundTypeForTFS(tfs float64) fundType {
	switch {
	case tfs >= 0.79:
		return fundAuslandsImmobilien
	case tfs >= 0.59:
		return fundImmobilien
	case tfs >= 0.29:
		return fundAktien
	case tfs >= 0.14:
		return fundMisch
	}
	return fundSonstige
}

/* ───────────── Per-bank capital income ───────────── */

// withheldTax is the tax column of a transaction, in EUR. At a German bank it
// is the Kapitalertragsteuer withheld including Soli and church tax; at a
// foreign broker it is foreign withholding tax.
type withheldTax struct {
	AccountID string
	Year      int
	Kind      string // transaction type
	Fund      bool
	Amount    float64
}

type fundIncome struct {
	// Before Teilfreistellung (what Anlage KAP-INV asks for).
	Dist, VP, SaleGains, SaleLosses float64 // SaleLosses as a positive magnitude
	// After Teilfreistellung (what enters the tax base).
	DistNet, VPNet, SaleGainsNet, SaleLossesNet float64
}

// bankIncome is one bank's capital income for one tax year.
type bankIncome struct {
	taxBank
	AccountIDs   []string
	AccountNames []string

	// Securities other than funds; no Teilfreistellung applies.
	Dividends   float64
	Interest    float64
	ShareGains  float64 // sales of shares (§ 20 Abs. 2 S. 1 Nr. 1 EStG)
	ShareLosses float64 // magnitude; only offsettable against share gains
	OtherGains  float64 // bonds, certificates, derivatives
	OtherLosses float64 // magnitude; general pot

	Funds [fundTypeCount]fundIncome

	GermanTax  float64 // withheld KapESt + Soli + church tax (domestic banks)
	ForeignTax float64 // creditable foreign withholding on non-fund income (foreign brokers)
}

func (b *bankIncome) generalIncome() float64 {
	v := b.Dividends + b.Interest + b.OtherGains
	for _, f := range b.Funds {
		v += f.DistNet + f.VPNet + f.SaleGainsNet
	}
	return v
}

func (b *bankIncome) generalLosses() float64 {
	v := b.OtherLosses
	for _, f := range b.Funds {
		v += f.SaleLossesNet
	}
	return v
}

// recurring is the income that repeats through the year (dividends,
// distributions, interest), after Teilfreistellung.
func (b *bankIncome) recurring() float64 {
	v := b.Dividends + b.Interest
	for _, f := range b.Funds {
		v += f.DistNet
	}
	return v
}

func (b *bankIncome) distributionsNet() float64 {
	v := 0.0
	for _, f := range b.Funds {
		v += f.DistNet
	}
	return v
}

func (b *bankIncome) vorabpauschaleNet() float64 {
	v := 0.0
	for _, f := range b.Funds {
		v += f.VPNet
	}
	return v
}

// saleGainsNet / saleLossesNet: all sales after Teilfreistellung (losses as magnitude).
func (b *bankIncome) saleGainsNet() float64 {
	v := b.ShareGains + b.OtherGains
	for _, f := range b.Funds {
		v += f.SaleGainsNet
	}
	return v
}

func (b *bankIncome) saleLossesNet() float64 {
	v := b.ShareLosses + b.OtherLosses
	for _, f := range b.Funds {
		v += f.SaleLossesNet
	}
	return v
}

// teilfreistellung is the net partially exempt amount: exemption on income
// and gains minus the matching share of losses (§ 21 InvStG cuts losses too).
func (b *bankIncome) teilfreistellung() float64 {
	v := 0.0
	for _, f := range b.Funds {
		v += (f.Dist - f.DistNet) + (f.VP - f.VPNet) + (f.SaleGains - f.SaleGainsNet) - (f.SaleLosses - f.SaleLossesNet)
	}
	return v
}

func (b *bankIncome) netting() capitalNetting {
	return netCapitalIncome(b.generalIncome(), b.generalLosses(), b.ShareGains, b.ShareLosses)
}

// projectedNet annualises the recurring income (months elapsed → 12) and keeps
// one-off items (sales, Vorabpauschale) as they are, then nets like the bank.
func (b *bankIncome) projectedNet(months int) float64 {
	if months < 1 {
		months = 1
	}
	if months > 12 {
		months = 12
	}
	rec := b.recurring()
	general := b.generalIncome() - rec + rec*12/float64(months)
	return netCapitalIncome(general, b.generalLosses(), b.ShareGains, b.ShareLosses).Net
}

func (b *bankIncome) empty() bool {
	if b.Dividends != 0 || b.Interest != 0 || b.ShareGains != 0 || b.ShareLosses != 0 || b.OtherGains != 0 || b.OtherLosses != 0 || b.GermanTax != 0 || b.ForeignTax != 0 {
		return false
	}
	for _, f := range b.Funds {
		if f.Dist != 0 || f.VP != 0 || f.SaleGains != 0 || f.SaleLosses != 0 {
			return false
		}
	}
	return true
}

// capitalNetting is the result of German loss offsetting for one pool of income.
type capitalNetting struct {
	Net           float64 // income left after offsetting (≥ 0); what the allowance and tax apply to
	ShareGain     float64 // part of Net that is share-sale gains
	UnusedGeneral float64 // general losses left over (carried forward)
	UnusedShare   float64 // share losses left over (Aktienverlusttopf, carried forward)
}

// netCapitalIncome applies the German loss pots (§ 20 Abs. 6 EStG, § 43a Abs. 3
// EStG): share losses offset only share gains; general losses offset any
// capital income, including share gains that are left after the share pot.
// All inputs are magnitudes after Teilfreistellung.
func netCapitalIncome(generalIncome, generalLosses, shareGains, shareLosses float64) capitalNetting {
	share := shareGains - shareLosses
	general := generalIncome - generalLosses
	res := capitalNetting{}
	if share < 0 {
		res.UnusedShare = -share
		share = 0
	}
	if general < 0 {
		offset := math.Min(share, -general)
		share -= offset
		res.UnusedGeneral = -general - offset
		general = 0
	}
	res.ShareGain = share
	res.Net = share + general
	return res
}

type capitalIncomeInput struct {
	Year int
	// Ledger is the chronological tax ledger: buildTaxTransactions output with
	// the Vorabpauschale events merged in (analytics.MergeVorabpauschaleEvents),
	// as the Overview uses it.
	Ledger   []analytics.TaxTransaction
	Withheld []withheldTax
	Accounts []db.Account
}

// filingLedger drops the transactions of pension wrappers. bAV, Riester and
// Rürup contracts are taxed on payout under § 22 EStG, so their trades never
// realise § 20 income; removing them before the replay keeps them out of the
// lines, the loss pots and the allowance alike.
func (in capitalIncomeInput) filingLedger() []analytics.TaxTransaction {
	excluded := make(map[string]bool)
	for _, a := range in.Accounts {
		if accountOutsideKAP(a) {
			excluded[a.ID.String()] = true
		}
	}
	if len(excluded) == 0 {
		return in.Ledger
	}
	out := make([]analytics.TaxTransaction, 0, len(in.Ledger))
	for _, t := range in.Ledger {
		if t.AccountID == "" || !excluded[t.AccountID] {
			out = append(out, t)
		}
	}
	return out
}

// aggregateCapitalIncome groups a tax year's capital income by bank, from the
// tax ledger's realised sales and income events. Pension wrappers and § 23 EStG
// private sales (crypto, physical gold) are left out: neither belongs on
// Anlage KAP or uses the saver's allowance.
func aggregateCapitalIncome(in capitalIncomeInput) []*bankIncome {
	acctByID := make(map[string]db.Account, len(in.Accounts))
	for _, a := range in.Accounts {
		acctByID[a.ID.String()] = a
	}
	banks := make(map[string]*bankIncome)
	var order []string
	get := func(acctID string) *bankIncome {
		a, ok := acctByID[acctID]
		if ok && accountOutsideKAP(a) {
			return nil
		}
		var tb taxBank
		if ok {
			tb = accountTaxBank(a)
		} else {
			tb = taxBank{Key: "account:" + acctID, Name: "Unknown account", Assumed: true}
		}
		b := banks[tb.Key]
		if b == nil {
			b = &bankIncome{taxBank: tb}
			banks[tb.Key] = b
			order = append(order, tb.Key)
		}
		for _, id := range b.AccountIDs {
			if id == acctID {
				return b
			}
		}
		b.AccountIDs = append(b.AccountIDs, acctID)
		name := a.Name
		if name == "" {
			name = "Unknown account"
		}
		b.AccountNames = append(b.AccountNames, name)
		return b
	}

	sales, income := analytics.RealizedTaxEvents(in.filingLedger())
	for _, s := range sales {
		if s.Year != in.Year || s.PrivateSale {
			continue
		}
		b := get(s.AccountID)
		if b == nil {
			continue
		}
		// PL is after the Vorabpauschale deduction (§ 19 Abs. 1 S. 3 InvStG)
		// and before Teilfreistellung.
		if isFundAssetClass(s.AssetClass, s.TFS) {
			f := &b.Funds[fundTypeForTFS(s.TFS)]
			if s.PL >= 0 {
				f.SaleGains += s.PL
				f.SaleGainsNet += s.TaxablePL()
			} else {
				f.SaleLosses -= s.PL
				f.SaleLossesNet -= s.TaxablePL()
			}
			continue
		}
		share := analytics.IsShareAssetClass(s.AssetClass)
		switch {
		case s.PL >= 0 && share:
			b.ShareGains += s.PL
		case s.PL >= 0:
			b.OtherGains += s.PL
		case share:
			b.ShareLosses -= s.PL
		default:
			b.OtherLosses -= s.PL
		}
	}
	for _, e := range income {
		if e.Year != in.Year || e.PrivateSale {
			continue
		}
		b := get(e.AccountID)
		if b == nil {
			continue
		}
		switch {
		case e.Kind == "interest":
			b.Interest += e.Amount
		case e.Kind == "vorabpauschale":
			f := &b.Funds[fundTypeForTFS(e.TFS)]
			f.VP += e.Amount
			f.VPNet += e.TaxableAmount()
		case isFundAssetClass(e.AssetClass, e.TFS) && e.ISIN != "":
			f := &b.Funds[fundTypeForTFS(e.TFS)]
			f.Dist += e.Amount
			f.DistNet += e.TaxableAmount()
		default:
			b.Dividends += e.Amount
		}
	}

	for _, wt := range in.Withheld {
		if wt.Year != in.Year || wt.Amount == 0 {
			continue
		}
		b := get(wt.AccountID)
		if b == nil {
			continue
		}
		if b.Domestic {
			if wt.Kind == "sell" || wt.Kind == "dividend" || wt.Kind == "interest" {
				b.GermanTax += wt.Amount
			}
			continue
		}
		// Foreign withholding is creditable (§ 32d Abs. 5 EStG) on dividends and
		// interest, but not on fund income: since the 2018 InvStG reform the
		// Teilfreistellung replaces any credit for taxes paid at fund level.
		if (wt.Kind == "dividend" || wt.Kind == "interest") && !wt.Fund {
			b.ForeignTax += wt.Amount
		}
	}

	out := make([]*bankIncome, 0, len(order))
	for _, k := range order {
		if !banks[k].empty() {
			out = append(out, banks[k])
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

/* ───────────── Anlage KAP ───────────── */

// kapLine is one line of Anlage KAP or KAP-INV. Description carries the
// official wording so a figure stays recognisable if a form renumbers lines.
type kapLine struct {
	Line        int     `json:"line"`
	Description string  `json:"description"`
	Hint        string  `json:"hint,omitempty"`
	Amount      float64 `json:"amount"`
	Section     string  `json:"section"`            // domestic, foreign, taxes, inv
	Estimate    bool    `json:"estimate,omitempty"` // belongs to the bank's Steuerbescheinigung; the amount is this app's estimate
	Blank       bool    `json:"blank,omitempty"`    // the app cannot know this figure; leave empty unless a certificate shows one
}

type kapBroker struct {
	BrokerName      string  `json:"broker_name"`
	AccountName     string  `json:"account_name"`
	Domestic        bool    `json:"domestic"`
	DomicileAssumed bool    `json:"domicile_assumed"`
	Dividends       float64 `json:"dividends"` // incl. fund distributions, after Teilfreistellung
	Interest        float64 `json:"interest"`
	RealizedGains   float64 `json:"realized_gains"`  // after Teilfreistellung
	RealizedLoss    float64 `json:"realized_losses"` // after Teilfreistellung, negative
	ShareGains      float64 `json:"share_gains"`
	ShareLosses     float64 `json:"share_losses"` // negative
	Vorabpauschale  float64 `json:"vorabpauschale"`
	TeilfreiAmt     float64 `json:"teilfreistellung"`
	WithheldTax     float64 `json:"withheld_tax"` // German tax withheld (domestic banks)
	ForeignTax      float64 `json:"foreign_tax"`  // foreign withholding (foreign brokers)
	NetIncome       float64 `json:"net_income"`   // after this bank's own loss offsetting
}

type anlageKAPInput struct {
	capitalIncomeInput
	Joint      bool
	ChurchRate float64
}

type anlageKAPResult struct {
	Year             int
	Joint            bool
	ChurchRate       float64
	Allowance        float64
	Brokers          []kapBroker
	Lines            []kapLine
	InvLines         []kapLine
	TotalIncome      float64
	TotalLosses      float64
	TotalTeilfrei    float64
	NetTaxable       float64 // after loss pots and losses carried in, before the allowance
	LossCarryIn      float64 // losses carried in from earlier years (≤ 0), both pots
	AllowanceApplied float64
	EstimatedTax     float64
	TotalWithheld    float64
	BalanceDue       float64
	CrossBrokerNote  string
	LayoutNote       string
}

// Official line wording (Anlage KAP 2024 and 2025; lines 21 and 24 were
// dropped from 2025 and are never emitted). Checked against the line list on
// lohnsteuer-kompakt.de/texte/2024/404/texte and /texte/2025/404/texte.
const (
	kapDescLine7  = "Kapitalerträge (lt. Steuerbescheinigung)"
	kapDescLine8  = "In Zeile 7 enthaltene Gewinne aus Aktienveräußerungen i. S. d. § 20 Abs. 2 Satz 1 Nr. 1 EStG"
	kapDescLine12 = "Nicht ausgeglichene Verluste ohne Verluste aus der Veräußerung von Aktien"
	kapDescLine13 = "Nicht ausgeglichene Verluste aus der Veräußerung von Aktien i. S. d. § 20 Abs. 2 Satz 1 Nr. 1 EStG"
	kapDescLine16 = "In Anspruch genommener Sparer-Pauschbetrag, der auf die in den Zeilen 7 bis 15 erklärten Kapitalerträge entfällt"
	kapDescLine19 = "Ausländische Kapitalerträge (ohne Investmenterträge)"
	kapDescLine20 = "In den Zeilen 18 und 19 enthaltene Gewinne aus Aktienveräußerungen i. S. d. § 20 Abs. 2 Satz 1 Nr. 1 EStG"
	kapDescLine22 = "In den Zeilen 18 und 19 enthaltene Verluste ohne Verluste aus der Veräußerung von Aktien"
	kapDescLine23 = "In den Zeilen 18 und 19 enthaltene Verluste aus der Veräußerung von Aktien i. S. d. § 20 Abs. 2 Satz 1 Nr. 1 EStG"
	kapDescLine37 = "Kapitalertragsteuer"
	kapDescLine38 = "Solidaritätszuschlag"
	kapDescLine39 = "Kirchensteuer zur Kapitalertragsteuer"
	kapDescLine41 = "Anrechenbare noch nicht angerechnete ausländische Steuern"
)

// Anlage KAP-INV line numbers per fund type: distributions 4-8, Vorabpauschalen
// 9-13, sale gains/losses 14/17/20/23/26 (each followed by two lines for
// grandfathered units). Amounts are before Teilfreistellung; the tax office
// applies it.
var (
	kapInvDistLine = [fundTypeCount]int{4, 5, 6, 7, 8}
	kapInvVPLine   = [fundTypeCount]int{9, 10, 11, 12, 13}
	kapInvSaleLine = [fundTypeCount]int{14, 17, 20, 23, 26}
)

func round2c(v float64) float64 {
	r := math.Round(v*100) / 100
	if r == 0 {
		return 0 // no "-0"
	}
	return r
}

func nonZero(v float64) bool { return math.Abs(v) >= 0.005 }

// splitGermanWithholding splits the tax a German bank withheld into
// Kapitalertragsteuer, Soli and church tax. With church tax k the KapESt is
// reduced so that total = KapESt × (1 + 5.5 % + k) (§ 32d Abs. 1 S. 3-4 EStG).
func splitGermanWithholding(total, churchRate float64) (kapESt, soli, church float64) {
	k := math.Min(math.Max(churchRate, 0), 0.09)
	kapESt = total / (1 + analytics.Solidaritaetszuschlag + k)
	soli = kapESt * analytics.Solidaritaetszuschlag
	church = kapESt * k
	return
}

// computeAnlageKAP maps a tax year's capital income onto Anlage KAP and
// Anlage KAP-INV.
//
// Domestic banks (lines 7-17, 37-39) report on their Steuerbescheinigung; the
// app shows its estimate to check the certificate against. Foreign brokers have
// no German withholding: their income goes on lines 19-23, funds on KAP-INV,
// and foreign withholding on line 41. Line 16 is the allowance a bank actually
// applied under a Freistellungsauftrag; the app never fills it in.
func computeAnlageKAP(in anlageKAPInput) anlageKAPResult {
	banks := aggregateCapitalIncome(in.capitalIncomeInput)
	res := anlageKAPResult{
		Year:       in.Year,
		Joint:      in.Joint,
		ChurchRate: in.ChurchRate,
		Allowance:  analytics.SparerPauschbetragForYear(in.Year, in.Joint),
		Brokers:    []kapBroker{},
		Lines:      []kapLine{},
		InvLines:   []kapLine{},
	}

	var l7, l8, l12, l13, l19, l20, l22, l23, germanTax, foreignTax float64
	hasDomestic, hasForeignDirect := false, false
	var inv [fundTypeCount]struct{ dist, vp, sale float64 }
	hasInv := [fundTypeCount][3]bool{}

	for _, b := range banks {
		n := b.netting()
		res.Brokers = append(res.Brokers, kapBroker{
			BrokerName:      b.Name,
			AccountName:     strings.Join(b.AccountNames, ", "),
			Domestic:        b.Domestic,
			DomicileAssumed: b.Assumed,
			Dividends:       round2c(b.Dividends + b.distributionsNet()),
			Interest:        round2c(b.Interest),
			RealizedGains:   round2c(b.saleGainsNet()),
			RealizedLoss:    round2c(-b.saleLossesNet()),
			ShareGains:      round2c(b.ShareGains),
			ShareLosses:     round2c(-b.ShareLosses),
			Vorabpauschale:  round2c(b.vorabpauschaleNet()),
			TeilfreiAmt:     round2c(b.teilfreistellung()),
			WithheldTax:     round2c(b.GermanTax),
			ForeignTax:      round2c(b.ForeignTax),
			NetIncome:       round2c(n.Net),
		})
		res.TotalIncome += b.generalIncome() + b.ShareGains
		res.TotalLosses -= b.generalLosses() + b.ShareLosses
		res.TotalTeilfrei += b.teilfreistellung()

		if b.Domestic {
			hasDomestic = true
			l7 += n.Net
			l8 += n.ShareGain
			l12 += n.UnusedGeneral
			l13 += n.UnusedShare
			germanTax += b.GermanTax
			continue
		}
		direct := b.Dividends + b.Interest + b.ShareGains + b.OtherGains - b.ShareLosses - b.OtherLosses
		if nonZero(b.Dividends) || nonZero(b.Interest) || nonZero(b.ShareGains) || nonZero(b.OtherGains) || nonZero(b.ShareLosses) || nonZero(b.OtherLosses) {
			hasForeignDirect = true
		}
		l19 += direct
		l20 += b.ShareGains
		l22 += b.OtherLosses
		l23 += b.ShareLosses
		foreignTax += b.ForeignTax
		for t, f := range b.Funds {
			inv[t].dist += f.Dist
			inv[t].vp += f.VP
			inv[t].sale += f.SaleGains - f.SaleLosses
			hasInv[t][0] = hasInv[t][0] || nonZero(f.Dist)
			hasInv[t][1] = hasInv[t][1] || nonZero(f.VP)
			hasInv[t][2] = hasInv[t][2] || nonZero(f.SaleGains) || nonZero(f.SaleLosses)
		}
	}

	const certHint = "From your bank's Steuerbescheinigung. The amount shown is this app's estimate to check the certificate against."
	add := func(line int, desc, hint string, amount float64, section string, estimate bool) {
		res.Lines = append(res.Lines, kapLine{Line: line, Description: desc, Hint: hint, Amount: round2c(amount), Section: section, Estimate: estimate})
	}
	if hasDomestic && (nonZero(l7) || nonZero(l8) || nonZero(l12) || nonZero(l13)) {
		add(7, kapDescLine7, certHint+" It is the bank's figure after its own loss offsetting.", l7, "domestic", true)
		if nonZero(l8) {
			add(8, kapDescLine8, "Share-sale gains contained in line 7. "+certHint, l8, "domestic", true)
		}
		lossHint := "Only with a Verlustbescheinigung (loss certificate), which you must request from the bank by 15 December (§ 43a Abs. 3 S. 4 EStG). Without one the bank carries the loss forward and this line stays empty."
		if nonZero(l12) {
			add(12, kapDescLine12, lossHint, l12, "domestic", true)
		}
		if nonZero(l13) {
			add(13, kapDescLine13, lossHint+" Share losses only offset share gains.", l13, "domestic", true)
		}
	}
	res.Lines = append(res.Lines, kapLine{
		Line: 16, Description: kapDescLine16, Section: "domestic", Blank: true,
		Hint: "The allowance your bank actually applied under your Freistellungsauftrag, as shown on its Steuerbescheinigung. Enter 0 or leave it empty if no bank applied one. This app cannot know it, so it never fills this line in; the tax office grants the rest of the Sparer-Pauschbetrag itself.",
	})
	if hasForeignDirect {
		add(19, kapDescLine19, "Income at brokers that withhold no German tax, with gains and losses already netted in; lines 20 to 23 break it down. Funds go on Anlage KAP-INV instead.", l19, "foreign", false)
		if nonZero(l20) {
			add(20, kapDescLine20, "Gains from selling shares, contained in line 19.", l20, "foreign", false)
		}
		if nonZero(l22) {
			add(22, kapDescLine22, "Losses from selling anything other than shares or funds, contained in line 19. Enter as a positive amount.", l22, "foreign", false)
		}
		if nonZero(l23) {
			add(23, kapDescLine23, "Losses from selling shares, contained in line 19. Enter as a positive amount. They only offset share gains.", l23, "foreign", false)
		}
	}
	if hasDomestic && nonZero(germanTax) {
		kapESt, soli, church := splitGermanWithholding(germanTax, in.ChurchRate)
		taxHint := "From your Steuerbescheinigung. Estimate: the tax column of your German bank transactions"
		if in.ChurchRate > 0 {
			taxHint += fmt.Sprintf(", split at a %.0f %% church-tax rate.", in.ChurchRate*100)
		} else {
			taxHint += ", with no church tax."
		}
		add(37, kapDescLine37, taxHint, kapESt, "taxes", true)
		add(38, kapDescLine38, taxHint, soli, "taxes", true)
		if nonZero(church) {
			add(39, kapDescLine39, taxHint, church, "taxes", true)
		}
	}
	if nonZero(foreignTax) {
		add(41, kapDescLine41, "Foreign tax withheld on dividends and interest at foreign brokers. Creditable only up to the treaty rate (15 % on US dividends with a W-8BEN); keep the broker's statement as proof.", foreignTax, "taxes", false)
	}

	for t := fundType(0); t < fundTypeCount; t++ {
		name := fundTypeNames[t]
		if hasInv[t][0] {
			res.InvLines = append(res.InvLines, kapLine{Line: kapInvDistLine[t], Section: "inv", Amount: round2c(inv[t].dist),
				Description: "Ausschüttungen aus " + name,
				Hint:        "Distributions received, before Teilfreistellung; the tax office applies the partial exemption."})
		}
		if hasInv[t][1] {
			res.InvLines = append(res.InvLines, kapLine{Line: kapInvVPLine[t], Section: "inv", Amount: round2c(inv[t].vp),
				Description: "Vorabpauschalen aus " + name,
				Hint:        fmt.Sprintf("Vorabpauschale for %d, deemed received in January %d, before Teilfreistellung. Check against the calculation on page 2 of the form.", in.Year-1, in.Year)})
		}
		if hasInv[t][2] {
			res.InvLines = append(res.InvLines, kapLine{Line: kapInvSaleLine[t], Section: "inv", Amount: round2c(inv[t].sale),
				Description: "Gewinne/Verluste aus der Veräußerung von Anteilen an " + name,
				Hint:        "Gain or loss from selling fund units, before Teilfreistellung, after subtracting the Vorabpauschalen already taxed while you held them (§ 19 Abs. 1 S. 3 InvStG)."})
		}
	}
	sort.SliceStable(res.InvLines, func(i, j int) bool { return res.InvLines[i].Line < res.InvLines[j].Line })

	// The tax office's view: the same engine as the Overview, pooling every
	// bank (as if each bank's losses were declared) with losses carried in.
	sum := analytics.ComputeTaxSummaryWithOptions(in.filingLedger(), in.Year, nil, analytics.TaxOptions{Joint: in.Joint, ChurchTaxRate: in.ChurchRate})
	res.NetTaxable = sum.TaxableGain
	res.LossCarryIn = sum.LossCarryInShare + sum.LossCarryInGeneral
	res.AllowanceApplied = sum.FreistellungUsed
	res.EstimatedTax = sum.EstimatedTax
	res.TotalWithheld = germanTax + foreignTax
	res.BalanceDue = res.EstimatedTax - res.TotalWithheld
	res.CrossBrokerNote = crossBrokerNote(banks)
	if in.Year != 2024 && in.Year != 2025 {
		res.LayoutNote = fmt.Sprintf("Line numbers follow the 2024 and 2025 forms. The %d form may number some lines differently, so match each figure by its description.", in.Year)
	}
	return res
}

// crossBrokerNote explains when income at several banks needs Anlage KAP to be
// netted. A German bank only offsets losses within itself; moving its losses to
// another bank's gains needs a Verlustbescheinigung requested by 15 December
// (§ 43a Abs. 3 S. 4 EStG). Foreign brokers' losses are declared on lines 22/23
// and offset by the tax office directly.
func crossBrokerNote(banks []*bankIncome) string {
	if len(banks) < 2 {
		return ""
	}
	var lossBanks, gainBanks []string
	for _, b := range banks {
		n := b.netting()
		if b.Domestic && (n.UnusedGeneral > 0.005 || n.UnusedShare > 0.005) {
			lossBanks = append(lossBanks, b.Name)
		}
		if n.Net > 0.005 {
			gainBanks = append(gainBanks, b.Name)
		}
	}
	if len(lossBanks) > 0 && len(gainBanks) > 0 {
		return fmt.Sprintf("Losses at %s can only offset gains at %s through your tax return. Ask %s for a Verlustbescheinigung (loss certificate) by 15 December (§ 43a Abs. 3 S. 4 EStG) and file Anlage KAP; otherwise the bank carries the loss forward.",
			joinNames(lossBanks), joinNames(gainBanks), joinNames(lossBanks))
	}
	return fmt.Sprintf("You have capital income at %d banks. Each bank only offsets losses and applies your Freistellungsauftrag within itself, so filing Anlage KAP lets the tax office apply the Sparer-Pauschbetrag (saver's allowance) and losses across all of them. Losses at a German bank need a Verlustbescheinigung requested by 15 December.", len(banks))
}

func joinNames(names []string) string {
	switch len(names) {
	case 0:
		return ""
	case 1:
		return names[0]
	}
	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}

func (r anlageKAPResult) response() map[string]any {
	return map[string]any{
		"year":              r.Year,
		"joint":             r.Joint,
		"church_tax_rate":   r.ChurchRate,
		"allowance":         r.Allowance,
		"brokers":           r.Brokers,
		"anlage_kap":        r.Lines,
		"anlage_kap_inv":    r.InvLines,
		"layout_note":       r.LayoutNote,
		"total_income":      round2c(r.TotalIncome),
		"total_losses":      round2c(r.TotalLosses),
		"total_teilfrei":    round2c(r.TotalTeilfrei),
		"net_taxable":       round2c(r.NetTaxable),
		"loss_carry_in":     round2c(r.LossCarryIn),
		"allowance_applied": round2c(r.AllowanceApplied),
		"estimated_tax":     round2c(r.EstimatedTax),
		"total_withheld":    round2c(r.TotalWithheld),
		"balance_due":       round2c(r.BalanceDue),
		"cross_broker_note": r.CrossBrokerNote,
		"broker_count":      len(r.Brokers),
	}
}

/* ───────────── Freistellungsauftrag status ───────────── */

type fsaBankRow struct {
	AccountID      string   `json:"account_id"`
	AccountName    string   `json:"account_name"` // the bank: a Freistellungsauftrag is filed per bank
	Accounts       []string `json:"accounts"`
	AcceptsFSA     bool     `json:"accepts_fsa"`
	Dividends      float64  `json:"dividends"` // incl. fund distributions, after Teilfreistellung
	Interest       float64  `json:"interest"`
	Gains          float64  `json:"realized_gains"` // sale gains minus losses after Teilfreistellung; may be negative
	Vorabpauschale float64  `json:"vorabpauschale"` // previous year's, deemed received in January
	Total          float64  `json:"total_income"`   // after the bank's loss offsetting, ≥ 0: what uses the allowance
}

type fsaRecommendation struct {
	Account         string  `json:"account"`
	ProjectedIncome float64 `json:"projected_income"`
	RecommendedFSA  float64 `json:"recommended_fsa"`
}

type fsaInput struct {
	capitalIncomeInput
	Joint  bool
	Months int // months of the year elapsed, for annualising recurring income
}

type fsaResult struct {
	Year           int
	Joint          bool
	Allowance      float64
	Banks          []fsaBankRow
	TotalIncome    float64
	ForeignIncome  float64
	Used           float64
	Remaining      float64
	Utilization    float64
	Recommendation []fsaRecommendation
}

// computeFSAStatus works out how much of the Sparer-Pauschbetrag the year's
// income uses. Each bank nets gains and losses before applying the
// Freistellungsauftrag (§ 43a Abs. 3 EStG), with Teilfreistellung on both
// sides, so a bank's contribution is floored at 0. Income at foreign brokers
// uses the allowance too, but only through Anlage KAP.
func computeFSAStatus(in fsaInput) fsaResult {
	banks := aggregateCapitalIncome(in.capitalIncomeInput)
	res := fsaResult{
		Year:           in.Year,
		Joint:          in.Joint,
		Allowance:      analytics.SparerPauschbetragForYear(in.Year, in.Joint),
		Banks:          []fsaBankRow{},
		Recommendation: []fsaRecommendation{},
	}
	for _, b := range banks {
		n := b.netting()
		res.Banks = append(res.Banks, fsaBankRow{
			AccountID:      b.AccountIDs[0],
			AccountName:    b.Name,
			Accounts:       b.AccountNames,
			AcceptsFSA:     b.Domestic,
			Dividends:      round2c(b.Dividends + b.distributionsNet()),
			Interest:       round2c(b.Interest),
			Gains:          round2c(b.saleGainsNet() - b.saleLossesNet()),
			Vorabpauschale: round2c(b.vorabpauschaleNet()),
			Total:          round2c(n.Net),
		})
		res.TotalIncome += n.Net
		if !b.Domestic {
			res.ForeignIncome += n.Net
		}
	}
	sort.SliceStable(res.Banks, func(i, j int) bool { return res.Banks[i].Total > res.Banks[j].Total })
	res.Used = math.Min(res.TotalIncome, res.Allowance)
	res.Remaining = res.Allowance - res.Used
	if res.Allowance > 0 {
		res.Utilization = math.Round(res.Used/res.Allowance*10000) / 100
	}

	// Split for next year: only German banks can take a Freistellungsauftrag.
	// Recurring income is annualised; sales and the Vorabpauschale are one-offs.
	type proj struct {
		name string
		v    float64
	}
	var projs []proj
	total := 0.0
	for _, b := range banks {
		if !b.Domestic {
			continue
		}
		if v := b.projectedNet(in.Months); v > 0.005 {
			projs = append(projs, proj{b.Name, v})
			total += v
		}
	}
	if total > 0 {
		sort.SliceStable(projs, func(i, j int) bool { return projs[i].v > projs[j].v })
		assigned := 0.0
		for _, p := range projs {
			rec := math.Round(res.Allowance * p.v / total)
			assigned += rec
			res.Recommendation = append(res.Recommendation, fsaRecommendation{Account: p.name, ProjectedIncome: round2c(p.v), RecommendedFSA: rec})
		}
		// Rounding remainder goes to the bank with the most income.
		res.Recommendation[0].RecommendedFSA += res.Allowance - assigned
	}
	return res
}

func (r fsaResult) response() map[string]any {
	return map[string]any{
		"year":            r.Year,
		"accounts":        r.Banks,
		"total_income":    round2c(r.TotalIncome),
		"foreign_income":  round2c(r.ForeignIncome),
		"allowance":       r.Allowance,
		"joint":           r.Joint,
		"used":            round2c(r.Used),
		"remaining":       round2c(r.Remaining),
		"utilization_pct": r.Utilization,
		"recommendation":  r.Recommendation,
	}
}

/* ───────────── Tax calendar helpers ───────────── */

// isGermanWorkingDay: Monday to Friday and not a nationwide public holiday on
// a fixed date. Movable feasts (Easter, Ascension, Whit Monday) never fall on
// the dates this calendar rolls (January, end of February, 31 July).
func isGermanWorkingDay(d time.Time) bool {
	switch d.Weekday() {
	case time.Saturday, time.Sunday:
		return false
	}
	switch {
	case d.Month() == time.January && d.Day() == 1,
		d.Month() == time.May && d.Day() == 1,
		d.Month() == time.October && d.Day() == 3,
		d.Month() == time.December && (d.Day() == 25 || d.Day() == 26):
		return false
	}
	return true
}

// nextWorkingDay rolls a deadline that ends on a weekend or public holiday to
// the next working day (§ 108 Abs. 3 AO).
func nextWorkingDay(d time.Time) time.Time {
	for !isGermanWorkingDay(d) {
		d = d.AddDate(0, 0, 1)
	}
	return d
}

// firstWorkingDayOfYear is when banks debit the tax on the previous year's
// Vorabpauschale (deemed received on the first working day, § 18 Abs. 3 InvStG).
func firstWorkingDayOfYear(year int) time.Time {
	return nextWorkingDay(time.Date(year, 1, 1, 0, 0, 0, 0, time.UTC))
}

// incomeTaxReturnDeadlines returns the filing deadlines for a tax year, after
// rolling weekends and holidays forward (§ 108 Abs. 3 AO).
//
//   - Without an adviser: 31 July of the following year (§ 149 Abs. 2 AO);
//     extended for 2020-2023 by Art. 97 § 36 Abs. 3 EGAO.
//   - With an adviser: last day of February of the second following year
//     (§ 149 Abs. 3 AO); extended for 2020-2024 by Art. 97 § 36 Abs. 3 EGAO
//     (2024 → 30.04.2026). From 2025 the regular rule applies again, so 2025 →
//     28.02.2027, a Sunday, → 01.03.2027; 2026 → 29.02.2028.
//     Source: lxgesetze.de/egao/97-36, checked 2026-11-18.
func incomeTaxReturnDeadlines(taxYear int) (unadvised, advised time.Time) {
	d := func(y int, m time.Month, day int) time.Time { return time.Date(y, m, day, 0, 0, 0, 0, time.UTC) }
	switch taxYear {
	case 2020:
		unadvised = d(2021, time.October, 31)
	case 2021:
		unadvised = d(2022, time.October, 31)
	case 2022:
		unadvised = d(2023, time.September, 30)
	case 2023:
		unadvised = d(2024, time.August, 31)
	default:
		unadvised = d(taxYear+1, time.July, 31)
	}
	switch taxYear {
	case 2020:
		advised = d(2022, time.August, 31)
	case 2021:
		advised = d(2023, time.August, 31)
	case 2022:
		advised = d(2024, time.July, 31)
	case 2023:
		advised = d(2025, time.May, 31)
	case 2024:
		advised = d(2026, time.April, 30)
	default:
		advised = d(taxYear+2, time.March, 1).AddDate(0, 0, -1) // last day of February
	}
	return nextWorkingDay(unadvised), nextWorkingDay(advised)
}

// harvestResult describes realising every depot position that is at a loss.
type harvestResult struct {
	Positions    int
	GrossLoss    float64 // sell-all loss after the Vorabpauschale deduction, before Teilfreistellung
	ShareLoss    float64 // after Teilfreistellung, Aktienverlusttopf
	GeneralLoss  float64 // after Teilfreistellung, general pot
	Saving       float64 // tax saved this year
	CarryForward float64 // part of the loss that would only carry forward
}

// harvestAnalysis sells every depot position (account, ISIN) that a sale
// today would close at a loss across all its FIFO lots — winners included, as
// a real sell-all would — and prices the sales through the tax engine
// (analytics.MarginalSaleTax). The saving therefore respects Teilfreistellung,
// both loss pots, losses carried in and the remaining Sparer-Pauschbetrag:
// a share loss saves nothing in a year without share gains, it only carries
// forward. § 23 assets and pension wrappers are excluded; pass priced lots only.
func harvestAnalysis(ledger []analytics.TaxTransaction, lots []analytics.TaxLot, excludedAccounts map[string]bool, year int, opts analytics.TaxOptions, asOf time.Time) harvestResult {
	type depot struct{ acct, isin string }
	byDepot := make(map[depot][]analytics.TaxLot)
	var order []depot
	for _, l := range lots {
		if excludedAccounts[l.AccountID] || l.PrivateSale {
			continue
		}
		k := depot{l.AccountID, l.ISIN}
		if _, ok := byDepot[k]; !ok {
			order = append(order, k)
		}
		byDepot[k] = append(byDepot[k], l)
	}
	var res harvestResult
	var losing []analytics.TaxLot
	for _, k := range order {
		gross, taxable := 0.0, 0.0
		for _, l := range byDepot[k] {
			pl := l.CurrentValue - l.CostBasis - l.VorabpauschaleTaxed
			gross += pl
			taxable += pl * (1 - l.TFSRate)
		}
		if taxable >= -0.005 {
			continue
		}
		res.Positions++
		res.GrossLoss -= gross
		if analytics.IsShareAssetClass(byDepot[k][0].AssetClass) {
			res.ShareLoss -= taxable
		} else {
			res.GeneralLoss -= taxable
		}
		losing = append(losing, byDepot[k]...)
	}
	if res.Positions == 0 {
		return res
	}
	sales := analytics.HypotheticalSellTxns(losing, asOf)
	without, with := analytics.MarginalSaleTax(ledger, sales, year, opts)
	res.Saving = math.Max(0, without-with)

	all := append(append(make([]analytics.TaxTransaction, 0, len(ledger)+len(sales)), ledger...), sales...)
	base := analytics.ComputeTaxSummaryWithOptions(ledger, year, nil, opts)
	after := analytics.ComputeTaxSummaryWithOptions(all, year, nil, opts)
	res.CarryForward = math.Max(0, (base.LossCarryOutShare+base.LossCarryOutGeneral)-(after.LossCarryOutShare+after.LossCarryOutGeneral))
	return res
}

// projectedAllowanceUse is the income expected to use the allowance this
// year: recurring income annualised, realised gains and the Vorabpauschale as
// they are, netted per bank.
func projectedAllowanceUse(banks []*bankIncome, months int) float64 {
	v := 0.0
	for _, b := range banks {
		v += b.projectedNet(months)
	}
	return v
}

/* ───────────── Loading ───────────── */

// taxFilingData is what the filing views read from the database.
type taxFilingData struct {
	rows     []db.ListTransactionsRow
	secMap   map[string]db.Security
	accounts []db.Account
	histFX   []market.HistoricalFXRate
	fxRates  map[string]float64
	taxTxns  []analytics.TaxTransaction
	names    map[string]string
	tfsMap   map[string]float64
	equity   map[string]bool
	// ledger is taxTxns with the Vorabpauschale events of every completed
	// year merged in, exactly as HandleTax builds it for the Overview.
	ledger []analytics.TaxTransaction
}

func (h *AnalysisHandler) loadTaxFilingData(ctx context.Context) (*taxFilingData, error) {
	rows, err := listAllTransactionsRows(ctx, h.queries)
	if err != nil {
		return nil, err
	}
	d := &taxFilingData{rows: rows}
	d.fxRates = loadFXRatesMap(ctx, h.queries)
	d.histFX = loadHistoricalFXRates(ctx, h.queries)
	secs, _ := h.queries.ListSecurities(ctx)
	d.accounts, _ = h.queries.ListAllAccounts(ctx)
	d.secMap = make(map[string]db.Security, len(secs))
	for _, s := range secs {
		d.secMap[s.ISIN] = s
	}
	d.taxTxns, d.names, d.tfsMap, d.equity = buildTaxTransactions(rows, secs, d.histFX, d.fxRates)
	// Vorabpauschalen of completed years: income in the following January
	// (§ 18 Abs. 3 InvStG), deducted from later sale gains (§ 19 Abs. 1 S. 3).
	vpEvents := h.vorabpauschaleEvents(ctx, d.taxTxns, d.secMap, d.tfsMap, d.histFX, d.fxRates, 0, time.Now().Year()-1, nil)
	d.ledger = analytics.MergeVorabpauschaleEvents(d.taxTxns, vpEvents)
	return d, nil
}

// withheld converts the transactions' tax column to EUR.
func (d *taxFilingData) withheld() []withheldTax {
	var out []withheldTax
	for _, r := range d.rows {
		if !r.Tax.Valid {
			continue
		}
		amt := numericToFloat(r.Tax)
		if amt == 0 {
			continue
		}
		fund := false
		if r.SecurityISIN.Valid {
			s := d.secMap[r.SecurityISIN.String]
			fund = isFundAssetClass(s.AssetClass, d.tfsMap[s.ISIN])
		}
		out = append(out, withheldTax{
			AccountID: r.AccountID.String(),
			Year:      r.Date.Year(),
			Kind:      r.Type,
			Fund:      fund,
			Amount:    convertAmountToEURAtDate(amt, r.Currency, r.Date, d.histFX, d.fxRates),
		})
	}
	return out
}

// excludedAccounts: pension wrappers, which never appear on Anlage KAP.
func (d *taxFilingData) excludedAccounts() map[string]bool {
	out := make(map[string]bool)
	for _, a := range d.accounts {
		if accountOutsideKAP(a) {
			out[a.ID.String()] = true
		}
	}
	return out
}

// capitalIncome assembles the inputs for one tax year.
func (d *taxFilingData) capitalIncome(year int) capitalIncomeInput {
	return capitalIncomeInput{
		Year:     year,
		Ledger:   d.ledger,
		Withheld: d.withheld(),
		Accounts: d.accounts,
	}
}

/* ───────────── CSV export ───────────── */

// exportTaxReport computes the CSV export's figures: the Overview's summary
// (same ledger with the Vorabpauschale events merged, same options) and the
// Anlage KAP lines.
func exportTaxReport(d *taxFilingData, year int, opts analytics.TaxOptions) (*analytics.TaxSummary, anlageKAPResult) {
	summary := analytics.ComputeTaxSummaryWithOptions(d.ledger, year, nil, opts)
	kap := computeAnlageKAP(anlageKAPInput{
		capitalIncomeInput: d.capitalIncome(year),
		Joint:              opts.Joint,
		ChurchRate:         opts.ChurchTaxRate,
	})
	return summary, kap
}

func writeTaxReportCSV(w io.Writer, year int, summary *analytics.TaxSummary, kap anlageKAPResult) {
	yesNo := func(b bool) string {
		if b {
			return "Ja"
		}
		return "Nein"
	}
	rate := strings.Replace(strconv.FormatFloat(summary.TaxRate*100, 'f', 3, 64), ".", ",", 1)
	fmt.Fprintf(w, "Steuerreport %d\n\n", year)
	fmt.Fprintf(w, "Zusammenfassung\n")
	fmt.Fprintf(w, "Zusammenveranlagung;%s\n", yesNo(summary.Joint))
	fmt.Fprintf(w, "Kirchensteuersatz;%.0f%%\n", summary.ChurchTaxRate*100)
	fmt.Fprintf(w, "Realisierte Gewinne;%.2f EUR\n", summary.RealizedGains)
	fmt.Fprintf(w, "Realisierte Verluste;%.2f EUR\n", summary.RealizedLosses)
	fmt.Fprintf(w, "Netto-Gewinn;%.2f EUR\n", summary.NetGain)
	fmt.Fprintf(w, "Dividendeneinkuenfte;%.2f EUR\n", summary.DividendIncome)
	fmt.Fprintf(w, "Zinseinkuenfte;%.2f EUR\n", summary.InterestIncome)
	fmt.Fprintf(w, "Vorabpauschale %d (zugeflossen im Januar %d);%.2f EUR\n", summary.VorabpauschaleYear, year, summary.VorabpauschaleIncome)
	fmt.Fprintf(w, "Teilfreistellung;%.2f EUR\n", summary.TeilfreistellungAmt)
	fmt.Fprintf(w, "Verlustvortrag Aktien aus Vorjahren;%.2f EUR\n", summary.LossCarryInShare)
	fmt.Fprintf(w, "Verlustvortrag allgemein aus Vorjahren;%.2f EUR\n", summary.LossCarryInGeneral)
	fmt.Fprintf(w, "Steuerpflichtiger Gewinn;%.2f EUR\n", summary.TaxableGain)
	fmt.Fprintf(w, "Sparerpauschbetrag;%.2f EUR\n", summary.Allowance)
	fmt.Fprintf(w, "Sparerpauschbetrag genutzt;%.2f EUR\n", summary.FreistellungUsed)
	fmt.Fprintf(w, "Sparerpauschbetrag verbleibend;%.2f EUR\n", summary.FreistellungRemain)
	fmt.Fprintf(w, "Geschaetzte Steuer (%s %%);%.2f EUR\n", rate, summary.EstimatedTax)
	fmt.Fprintf(w, "Effektiver Steuersatz;%.1f%%\n", summary.EffectiveRate)
	fmt.Fprintf(w, "Verlustvortrag Aktien ins Folgejahr;%.2f EUR\n", summary.LossCarryOutShare)
	fmt.Fprintf(w, "Verlustvortrag allgemein ins Folgejahr;%.2f EUR\n", summary.LossCarryOutGeneral)
	if ps := summary.PrivateSales; ps != nil {
		fmt.Fprintf(w, "Private Veraeusserungsgeschaefte (§ 23 EStG), steuerpflichtig;%.2f EUR\n", ps.TaxableAmount)
		fmt.Fprintf(w, "Private Veraeusserungsgeschaefte (§ 23 EStG), steuerfrei nach 1 Jahr;%.2f EUR\n", ps.TaxFreePL)
	}

	if len(summary.BySecurity) > 0 {
		fmt.Fprintf(w, "\nRealisierte Gewinne/Verluste nach Wertpapier\n")
		fmt.Fprintf(w, "Datum;ISIN;Name;Realisierter G/V;Vorabpauschale abgezogen;Teilfreistellung;Steuerpflichtiger G/V;Topf;Aktienfonds\n")
		for _, s := range summary.BySecurity {
			pot := "allgemein"
			if s.Pot == "share" {
				pot = "Aktien"
			}
			fmt.Fprintf(w, "%s;%s;%s;%.2f;%.2f;%.2f;%.2f;%s;%s\n",
				s.Date, csvField(s.ISIN), csvField(s.Name), s.RealizedPL, s.VorabpauschaleDeducted, s.Teilfreistellung, s.TaxablePL, pot, yesNo(s.IsEquityFund))
		}
	}

	fmt.Fprintf(w, "\nAnlage KAP / KAP-INV %d\n", year)
	fmt.Fprintf(w, "Formular;Zeile;Bezeichnung;Betrag;Hinweis\n")
	writeKAPLines := func(form string, lines []kapLine) {
		for _, l := range lines {
			note := ""
			amount := fmt.Sprintf("%.2f", l.Amount)
			switch {
			case l.Blank:
				note = "Nur lt. Steuerbescheinigung eintragen, sonst leer lassen"
				amount = ""
			case l.Estimate:
				note = "Schätzung, mit der Steuerbescheinigung abgleichen"
			}
			fmt.Fprintf(w, "%s;%d;%s;%s;%s\n", form, l.Line, csvField(l.Description), amount, csvField(note))
		}
	}
	writeKAPLines("KAP", kap.Lines)
	writeKAPLines("KAP-INV", kap.InvLines)
	if kap.LayoutNote != "" {
		fmt.Fprintf(w, "%s\n", csvField("Hinweis: Zeilennummern nach den Vordrucken 2024/2025; bitte anhand der Bezeichnung zuordnen."))
	}
}

// csvField quotes a semicolon-separated CSV field when it needs it.
func csvField(s string) string {
	if strings.ContainsAny(s, ";\"\n\r") {
		return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
	}
	return s
}

/* ───────────── DATEV export ───────────── */

// datevRow is one booking line of the DATEV Buchungsstapel.
type datevRow struct {
	Amount     float64 // EUR, magnitude
	SollHaben  string  // "S" or "H"
	Konto      string
	Gegenkonto string
	Belegdatum string // DDMM
	Text       string
	Beleg      string
}

// datevRows books the year's capital income, sales, fees and withheld taxes
// (SKR03). Amounts are magnitudes (migration 032); the type gives the
// direction. Kapitalertragsteuer that arrives as its own "tax" row (Scalable)
// is booked against 1810 Privatsteuern.
func datevRows(txns []db.ListTransactionsRow, year int, secNames, acctNames map[string]string, histFX []market.HistoricalFXRate, fxRates map[string]float64) []datevRow {
	var out []datevRow
	for _, txn := range txns {
		if txn.Date.Year() != year {
			continue
		}
		gegenkonto := ""
		sollHaben := "H"
		switch txn.Type {
		case "dividend", "interest", "sell":
			gegenkonto = "2650"
		case "fee":
			gegenkonto, sollHaben = "4900", "S"
		case "tax":
			gegenkonto, sollHaben = "1810", "S"
		default:
			continue
		}
		amt := math.Abs(convertAmountToEURAtDate(numericToFloat(txn.Amount), txn.Currency, txn.Date, histFX, fxRates))
		isin := ""
		if txn.SecurityISIN.Valid {
			isin = txn.SecurityISIN.String
		}
		name := secNames[isin]
		if name == "" {
			name = isin
		}
		if name == "" && txn.Counterparty.Valid {
			name = txn.Counterparty.String
		}
		text := []rune(fmt.Sprintf("%s: %s", txn.Type, name))
		if len(text) > 60 {
			text = text[:60]
		}
		out = append(out, datevRow{
			Amount:     amt,
			SollHaben:  sollHaben,
			Konto:      "1800",
			Gegenkonto: gegenkonto,
			Belegdatum: txn.Date.Format("0201"),
			Text:       string(text),
			Beleg:      acctNames[txn.AccountID.String()],
		})
	}
	return out
}

func writeDATEV(w io.Writer, year int, rows []datevRow) {
	quote := func(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }
	// DATEV header
	fmt.Fprintf(w, "\"EXTF\";510;21;\"Buchungsstapel\";7;%d0101;4;\"%d\";\"%d\";;;;\"\";\"\";1;\n", year, year, year)
	// Column headers
	fmt.Fprintf(w, "Umsatz;Soll/Haben;WKZ;Konto;Gegenkonto;Belegdatum;Buchungstext;Belegfeld1\n")
	for _, r := range rows {
		fmt.Fprintf(w, "%.2f;%s;EUR;%s;%s;%s;%s;%s\n", r.Amount, r.SollHaben, r.Konto, r.Gegenkonto, r.Belegdatum, quote(r.Text), quote(r.Beleg))
	}
}

/* ───────────── Shared with background jobs ───────────── */

// TaxLedger is the tax engine input the Tax pages work from, for background
// jobs (scheduler reminders and alerts) that must show the same figures.
type TaxLedger struct {
	// Txns is chronological, with the Vorabpauschale events of completed
	// years merged in and pension-wrapper accounts removed.
	Txns   []analytics.TaxTransaction
	Names  map[string]string
	Equity map[string]bool
	// Prices is today's EUR price per unit of every held security.
	Prices map[string]float64
}

// LoadTaxLedger loads the tax ledger exactly as the filing views build it.
func LoadTaxLedger(ctx context.Context, q *db.Queries) (*TaxLedger, error) {
	h := NewAnalysisHandler(q)
	d, err := h.loadTaxFilingData(ctx)
	if err != nil {
		return nil, err
	}
	return &TaxLedger{
		Txns:   capitalIncomeInput{Ledger: d.ledger, Accounts: d.accounts}.filingLedger(),
		Names:  d.names,
		Equity: d.equity,
		Prices: h.currentUnitPricesEUR(ctx),
	}, nil
}
