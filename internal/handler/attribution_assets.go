package handler

import (
	"context"
	"log"
	"math"
	"time"

	"github.com/google/uuid"

	"github.com/lnsp/wealth/internal/assets"
	db "github.com/lnsp/wealth/internal/database/generated"
)

// physicalAttribution separates physical assets and their loans from market
// returns in net-worth attribution. Net worth includes asset values minus loan
// balances, so without it every purchase, mortgage payout and repayment would
// land in the "market returns" residual.
//
// The identity, per window, is
//
//	ΔNW = contributions + market + dividends + interest − fees − taxes
//	      + asset value change − purchase costs − loan interest
//
// where linked payments (asset purchases, loan instalments) are swaps and
// skipped by the transaction classifier, and contributions are adjusted by
// the events below so that money from outside the tracked accounts (an
// unlinked purchase, a payment from an untracked account) still balances.
type physicalAttribution struct {
	holdings assets.Holdings
	linked   map[uuid.UUID]bool
	events   []physicalEvent
}

// physicalEvent is one dated effect; all amounts are positive magnitudes
// except contrib, which is signed.
type physicalEvent struct {
	date     time.Time
	contrib  float64 // adjustment to net contributions
	capital  float64 // price or improvement entering the physical component
	costs    float64 // purchase costs (lost on day one)
	interest float64 // loan interest
}

type physicalTotals struct {
	contrib, capital, costs, interest float64
}

func (t *physicalTotals) add(e physicalEvent) {
	t.contrib += e.contrib
	t.capital += e.capital
	t.costs += e.costs
	t.interest += e.interest
}

func (h *PortfolioHandler) loadPhysicalAttribution(ctx context.Context, txns []db.ListTransactionsRow) physicalAttribution {
	holdings, err := assets.Load(ctx, h.queries)
	if err != nil {
		log.Printf("WARNING: load assets for attribution: %v", err)
	}
	return buildPhysicalAttribution(holdings, txns, assets.CivilDate(time.Now()))
}

func buildPhysicalAttribution(holdings assets.Holdings, txns []db.ListTransactionsRow, today time.Time) physicalAttribution {
	pa := physicalAttribution{holdings: holdings, linked: holdings.LinkedIDs()}
	txnByID := make(map[uuid.UUID]db.ListTransactionsRow, len(txns))
	for _, t := range txns {
		txnByID[t.ID] = t
	}
	add := func(e physicalEvent) {
		if !e.date.After(today) {
			pa.events = append(pa.events, e)
		}
	}
	for _, a := range holdings.Assets {
		price, costs := a.PurchasePrice(), a.PurchaseCosts()
		add(physicalEvent{date: a.PurchaseDate, capital: price, costs: costs, contrib: price + costs})
		for _, im := range a.Improvements {
			amt := assets.Numeric(im.Amount)
			add(physicalEvent{date: im.Date, capital: amt, contrib: amt})
		}
		for _, l := range a.Links {
			if t, ok := txnByID[l.TransactionID]; ok {
				add(physicalEvent{date: t.Date, contrib: -math.Abs(assets.Numeric(t.Amount))})
			}
		}
	}
	for _, l := range holdings.Loans {
		add(physicalEvent{date: l.PayoutDate, contrib: -l.Model.Principal})
		linkedDue := make(map[time.Time]bool, len(l.Links))
		for _, p := range l.Links {
			linkedDue[p.DueDate] = true
		}
		for _, p := range l.Schedule {
			e := physicalEvent{date: p.Due, interest: p.Interest}
			if !linkedDue[p.Due] {
				e.contrib = p.Interest + p.Principal // paid from money we don't track
			}
			add(e)
		}
		for _, x := range l.Extras {
			if x.TransactionID == nil {
				add(physicalEvent{date: x.Date, contrib: assets.Numeric(x.Amount)})
			}
		}
	}
	return pa
}

// window sums the events in (from, to].
func (pa physicalAttribution) window(from, to time.Time) physicalTotals {
	var t physicalTotals
	for _, e := range pa.events {
		if e.date.After(from) && !e.date.After(to) {
			t.add(e)
		}
	}
	return t
}

// monthly sums the events per calendar month ("2006-01").
func (pa physicalAttribution) monthly() map[string]physicalTotals {
	out := make(map[string]physicalTotals)
	for _, e := range pa.events {
		m := e.date.Format("2006-01")
		t := out[m]
		t.add(e)
		out[m] = t
	}
	return out
}

func (pa physicalAttribution) hasAssets() bool { return len(pa.holdings.Assets) > 0 }
func (pa physicalAttribution) hasLoans() bool  { return len(pa.holdings.Loans) > 0 }
