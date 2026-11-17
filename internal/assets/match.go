package assets

import (
	"context"
	"math"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/lnsp/wealth/internal/database/generated"
	"github.com/lnsp/wealth/internal/market"
)

// PaymentCandidate is a bank transaction that might be a loan instalment.
type PaymentCandidate struct {
	ID        uuid.UUID
	AccountID uuid.UUID
	Date      time.Time
	Amount    float64
}

// matchWindow is how far a booking may be from its due date (weekends,
// holidays, banks booking on the next working day).
const matchWindow = 7 * 24 * time.Hour

// MatchPayments pairs a loan's due instalments (up to today) with withdrawals
// of exactly the scheduled amount within a week of the due date, from the
// loan's payment account when one is set. Each due date and transaction is
// used once; taken holds transactions already linked anywhere.
func MatchPayments(l Loan, candidates []PaymentCandidate, taken map[uuid.UUID]bool, today time.Time) []db.InsertLoanPaymentLinkParams {
	linkedDue := make(map[time.Time]bool)
	for _, p := range l.Links {
		linkedDue[p.DueDate] = true
	}
	used := make(map[uuid.UUID]bool)
	var out []db.InsertLoanPaymentLinkParams
	for _, p := range l.Schedule {
		if p.Due.After(today) {
			break
		}
		if linkedDue[p.Due] {
			continue
		}
		amount := p.Interest + p.Principal
		var best *PaymentCandidate
		for i := range candidates {
			c := &candidates[i]
			if taken[c.ID] || used[c.ID] {
				continue
			}
			if l.PaymentAccountID != nil && c.AccountID != *l.PaymentAccountID {
				continue
			}
			if math.Abs(math.Abs(c.Amount)-amount) > 0.01 {
				continue
			}
			gap := c.Date.Sub(p.Due)
			if gap < 0 {
				gap = -gap
			}
			if gap > matchWindow {
				continue
			}
			if best == nil || gap < absDuration(best.Date.Sub(p.Due)) {
				best = c
			}
		}
		if best != nil {
			used[best.ID] = true
			out = append(out, db.InsertLoanPaymentLinkParams{TransactionID: best.ID, LoanID: l.ID, DueDate: p.Due})
		}
	}
	return out
}

func absDuration(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}

// MatchAllLoanPayments links new instalments for every loan and tags them
// with CategoryLoanPayment. It returns how many links were added.
func MatchAllLoanPayments(ctx context.Context, q *db.Queries) (int, error) {
	h, err := Load(ctx, q)
	if err != nil || len(h.Loans) == 0 {
		return 0, err
	}
	txns, err := q.ListAllTransactions(ctx)
	if err != nil {
		return 0, err
	}
	latestFX := map[string]float64{"EUR": 1.0}
	if rows, fxErr := q.ListLatestExchangeRates(ctx); fxErr == nil {
		for _, r := range rows {
			if rate := Numeric(r.Rate); rate > 0 {
				latestFX[strings.ToUpper(strings.TrimSpace(r.Currency))] = rate
			}
		}
	}
	var histFX []market.HistoricalFXRate
	if rows, fxErr := q.ListAllExchangeRates(ctx); fxErr == nil {
		for _, r := range rows {
			if rate := Numeric(r.Rate); rate > 0 {
				histFX = append(histFX, market.HistoricalFXRate{
					Date:     CivilDate(r.Date),
					Currency: strings.ToUpper(strings.TrimSpace(r.Currency)),
					Rate:     rate,
				})
			}
		}
	}
	taken := h.LinkedIDs()
	var candidates []PaymentCandidate
	for _, t := range txns {
		if t.Type != "withdrawal" || t.SecurityISIN.Valid {
			continue
		}
		amt := Numeric(t.Amount) + Numeric(t.Fee)
		cur := strings.TrimSpace(t.Currency)
		if cur != "" && !strings.EqualFold(cur, "EUR") {
			scale := 1.0
			norm := strings.ToUpper(cur)
			if cur == "GBp" || cur == "GBX" {
				norm = "GBP"
				scale = 0.01
			}
			scaled := amt * scale
			if rate := market.LookupRateForDate(histFX, norm, t.Date); rate > 0 {
				amt = scaled / rate
			} else if rate := latestFX[norm]; rate > 0 {
				amt = scaled / rate
			} else {
				amt = scaled
			}
		}
		candidates = append(candidates, PaymentCandidate{ID: t.ID, AccountID: t.AccountID, Date: CivilDate(t.Date), Amount: amt})
	}
	added := 0
	today := CivilDate(time.Now())
	for _, l := range h.Loans {
		for _, link := range MatchPayments(l, candidates, taken, today) {
			if err := q.InsertLoanPaymentLink(ctx, link); err != nil {
				return added, err
			}
			if err := q.UpdateTransactionCategory(ctx, db.UpdateTransactionCategoryParams{
				ID: link.TransactionID, Category: pgtype.Text{String: CategoryLoanPayment, Valid: true},
			}); err != nil {
				return added, err
			}
			taken[link.TransactionID] = true
			added++
		}
	}
	return added, nil
}
