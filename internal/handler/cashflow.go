package handler

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/lnsp/wealth/internal/assets"
	"github.com/lnsp/wealth/internal/cashflow"
	db "github.com/lnsp/wealth/internal/database/generated"
	"github.com/lnsp/wealth/internal/market"
)

type CashflowHandler struct {
	queries *db.Queries
}

func NewCashflowHandler(q *db.Queries) *CashflowHandler {
	return &CashflowHandler{queries: q}
}

// monthlyBucket is one month × one category cell.
type monthlyBucket struct {
	Month    string  `json:"month"` // YYYY-MM
	Category string  `json:"category"`
	Bucket   string  `json:"bucket"`
	Amount   float64 `json:"amount"` // positive magnitude for fixed/variable; signed for income, transfer and unclassified
	Count    int     `json:"count"`
}

type monthlyTotals struct {
	Month    string  `json:"month"`
	Income   float64 `json:"income"`
	Salary   float64 `json:"salary"` // take-home pay and pensions, part of Income
	Fixed    float64 `json:"fixed"`
	Variable float64 `json:"variable"`
	Transfer float64 `json:"transfer"`
	// Unclassified is money in that no rule recognised. It is neither income
	// nor spending until the user assigns a category.
	Unclassified float64 `json:"unclassified"`
	Net          float64 `json:"net"` // Income - Fixed - Variable
	// Partial marks the running month. It is shown but left out of the medians.
	Partial bool `json:"partial"`
	// HasData is true when at least one cash transaction was imported for the month.
	HasData bool `json:"has_data"`
}

type cashflowResponse struct {
	From    string          `json:"from"`
	To      string          `json:"to"`
	Months  []monthlyTotals `json:"months"`  // newest first; the first is the running month
	Buckets []monthlyBucket `json:"buckets"` // detail per (month, category)
	Medians medianSummary   `json:"medians"` // for "Apply to Planning"
	// UnconvertedCurrencies lists currencies with no EUR rate. Their
	// transactions are left out rather than counted 1:1 as euros.
	UnconvertedCurrencies []string `json:"unconverted_currencies"`
	// Scope says which accounts the income/spending view reads. Without a
	// current account, salary and day-to-day spending run through a bank that
	// isn't imported and cannot be measured here.
	Scope cashflowScope `json:"scope"`
	// External is money crossing the boundary of all tracked accounts per
	// month (same months as Months), and ExternalTrailing its 12-month net
	// per month — the figure Planning uses as the default contribution.
	External         []externalMonth     `json:"external"`
	ExternalTrailing externalTrailingRate `json:"external_trailing"`
}

// cashflowScope describes the accounts behind the income/spending figures.
type cashflowScope struct {
	CashAccounts      []string `json:"cash_accounts"` // checking and savings accounts read for income and spending
	HasCurrentAccount bool     `json:"has_current_account"`
}

// externalMonth is money that entered or left the tracked accounts in one
// month: deposits from and withdrawals to untracked accounts, shares granted
// (RSU vests) and payments into linked physical assets.
type externalMonth struct {
	Month      string  `json:"month"`
	In         float64 `json:"in"`
	InKind     float64 `json:"in_kind"`
	Out        float64 `json:"out"`
	IntoAssets float64 `json:"into_assets"`
	Net        float64 `json:"net"` // In + InKind − Out − IntoAssets
}

// externalTrailingRate is trailingNetContribution in JSON form.
type externalTrailingRate struct {
	Monthly       float64 `json:"monthly"`
	MonthsCovered float64 `json:"months_covered"`
	Deposits      float64 `json:"deposits"`
	InKind        float64 `json:"in_kind"`
	Withdrawals   float64 `json:"withdrawals"`
	AssetPayments float64 `json:"asset_payments"`
}

// buildExternalMonths sums externalFlowOf per calendar month for the given
// months ("2006-01", newest first), so the Cashflow page can show what is
// visible when salary and spending run through an untracked bank.
func buildExternalMonths(txns []db.ListTransactionsRow, acctTypes map[uuid.UUID]string, holdings assets.Holdings, histFX []market.HistoricalFXRate, fxRates map[string]float64, months []string) []externalMonth {
	idx := make(map[string]int, len(months))
	out := make([]externalMonth, len(months))
	for i, m := range months {
		idx[m] = i
		out[i].Month = m
	}
	washed := washedInKindIDs(txns, acctTypes)
	oneOff := oneOffAssetPaymentIDs(holdings)
	for _, t := range txns {
		i, ok := idx[t.Date.Format("2006-01")]
		if !ok {
			continue
		}
		kind := externalFlowOf(t, acctTypes, washed, oneOff)
		if kind == extNone {
			continue
		}
		amt := math.Abs(convertAmountToEURAtDate(numericToFloat(t.Amount), t.Currency, t.Date, histFX, fxRates))
		switch kind {
		case extDeposit:
			out[i].In += amt
		case extInKind:
			out[i].InKind += amt
		case extWithdrawal:
			out[i].Out += amt
		case extAssetPayment:
			out[i].IntoAssets += amt
		}
	}
	for i := range out {
		e := &out[i]
		e.In, e.InKind, e.Out, e.IntoAssets = math.Round(e.In*100)/100, math.Round(e.InKind*100)/100, math.Round(e.Out*100)/100, math.Round(e.IntoAssets*100)/100
		e.Net = math.Round((e.In+e.InKind-e.Out-e.IntoAssets)*100) / 100
	}
	return out
}

// medianSummary describes a typical month over the last 12 complete months
// that have income. The running month is never part of it.
type medianSummary struct {
	MonthlyIncome  float64 `json:"monthly_income"`  // median monthly income
	MonthlySpend   float64 `json:"monthly_spend"`   // median monthly Fixed + Variable
	MonthlySurplus float64 `json:"monthly_surplus"` // median monthly net (not median income − median spend)
	// SavingsRatePct is Σnet ÷ Σincome × 100 over the same months; null without income.
	SavingsRatePct *float64 `json:"savings_rate_pct"`
	// AnnualTakeHome is salary (net pay) per month × 12. It is after tax and
	// social contributions, so it is not gross income.
	AnnualTakeHome float64 `json:"annual_take_home"`
	Months         int     `json:"months"`        // months the figures use
	WindowMonths   int     `json:"window_months"` // complete months searched
}

// cashRow is one cash-account transaction with its amounts in EUR.
type cashRow struct {
	ID           uuid.UUID
	Date         time.Time
	Type         string
	Amount       float64 // as stored: imports store magnitudes, some legacy rows carry a sign
	Fee          float64
	Tax          float64
	Counterparty string
	Reference    string
	Category     string // override from the user or the import, "" if none
	AccountType  string
}

// loanSplitFunc returns the interest part of a matched loan instalment and the
// kind of asset the loan financed.
type loanSplitFunc func(txnID uuid.UUID) (interest float64, kind string, ok bool)

// HandleCashflow returns aggregated monthly cashflow for checking/savings
// accounts: the running month plus `months` complete months (default 12). The
// response is pre-computed in two shapes: a per-month totals series for the
// table, and (month × category) buckets for the breakdown.
func (h *CashflowHandler) HandleCashflow(w http.ResponseWriter, r *http.Request) {
	months := 12
	if m := r.URL.Query().Get("months"); m != "" {
		if n, err := strconv.Atoi(m); err == nil && n >= 1 && n <= 60 {
			months = n
		}
	}

	now := time.Now()
	from, to := cashflowWindow(now, months)
	rows, err := h.queries.ListCashTransactions(r.Context(), db.ListCashTransactionsParams{
		Date:   from,
		Date_2: to,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list cash transactions: "+err.Error())
		return
	}
	// Matched loan instalments are split into interest (a cost) and
	// principal (saving: it lowers the debt).
	holdings, err := assets.Load(r.Context(), h.queries)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "load loans: "+err.Error())
		return
	}
	split := func(id uuid.UUID) (float64, string, bool) {
		interest, _, kind, ok := holdings.PaymentSplit(id)
		return interest, kind, ok
	}

	// IBANs of every own account, closed ones included: a payment naming one
	// is an own-account transfer.
	var ownIBANs []string
	allAccts, _ := h.queries.ListAllAccounts(r.Context())
	acctTypes := make(map[uuid.UUID]string, len(allAccts))
	scope := cashflowScope{CashAccounts: []string{}}
	for _, a := range allAccts {
		if a.IsActive { // same account set as Planning's default contribution
			acctTypes[a.ID] = a.Type
		}
		if a.IBAN.Valid && a.IBAN.String != "" {
			ownIBANs = append(ownIBANs, a.IBAN.String)
		}
		// Closed accounts keep their history on this page, so they count too.
		if a.Type == "checking" || a.Type == "savings" {
			scope.CashAccounts = append(scope.CashAccounts, a.Name)
			if a.Type == "checking" {
				scope.HasCurrentAccount = true
			}
		}
	}

	fxRates := loadFXRatesMap(r.Context(), h.queries)
	histFX := loadHistoricalFXRates(r.Context(), h.queries)
	unconverted := map[string]bool{}
	cashRows := make([]cashRow, 0, len(rows))
	for _, row := range rows {
		if !hasEURRate(row.Currency, row.Date, histFX, fxRates) {
			unconverted[strings.ToUpper(strings.TrimSpace(row.Currency))] = true
			continue
		}
		toEUR := func(n pgtype.Numeric) float64 {
			return convertAmountToEURAtDate(numericToFloat(n), row.Currency, row.Date, histFX, fxRates)
		}
		cashRows = append(cashRows, cashRow{
			ID:           row.ID,
			Date:         row.Date,
			Type:         row.Type,
			Amount:       toEUR(row.Amount),
			Fee:          toEUR(row.Fee),
			Tax:          toEUR(row.Tax),
			Counterparty: row.Counterparty.String,
			Reference:    row.Reference.String,
			Category:     row.Category.String,
			AccountType:  row.AccountType,
		})
	}

	resp := buildCashflow(cashRows, split, ownIBANs, now, months)
	resp.Scope = scope
	if txns, err := listAllTransactionsRows(r.Context(), h.queries); err == nil {
		keys := make([]string, len(resp.Months))
		for i, m := range resp.Months {
			keys[i] = m.Month
		}
		resp.External = buildExternalMonths(txns, acctTypes, holdings, histFX, fxRates, keys)
		tr := trailingNetContribution(txns, acctTypes, now, histFX, fxRates, holdings)
		resp.ExternalTrailing = externalTrailingRate{
			Monthly:       math.Round(tr.Monthly*100) / 100,
			MonthsCovered: math.Round(tr.MonthsCovered*10) / 10,
			Deposits:      math.Round(tr.Deposits),
			InKind:        math.Round(tr.InKind),
			Withdrawals:   math.Round(tr.Withdrawals),
			AssetPayments: math.Round(tr.AssetPayments),
		}
	}
	if resp.External == nil {
		resp.External = []externalMonth{}
	}
	for c := range unconverted {
		resp.UnconvertedCurrencies = append(resp.UnconvertedCurrencies, c)
	}
	sort.Strings(resp.UnconvertedCurrencies)
	writeJSON(w, http.StatusOK, resp)
}

// cashflowWindow returns the first day of the oldest complete month and the
// last day of the running month.
func cashflowWindow(now time.Time, months int) (from, to time.Time) {
	from = time.Date(now.Year(), now.Month()-time.Month(months), 1, 0, 0, 0, 0, time.UTC)
	to = time.Date(now.Year(), now.Month()+1, 0, 0, 0, 0, 0, time.UTC)
	return from, to
}

// hasEURRate reports whether an amount in `currency` can be converted to EUR.
// convertAmountToEURAtDate falls back to 1:1 when no rate exists, which would
// count 1,000 HUF as 1,000 €.
func hasEURRate(currency string, date time.Time, hist []market.HistoricalFXRate, latest map[string]float64) bool {
	norm, _ := normalizeCurrencyAndScale(currency)
	if norm == "EUR" {
		return true
	}
	if len(hist) > 0 && market.LookupRateForDate(hist, norm, date) > 0 {
		return true
	}
	return latest[norm] > 0
}

// cashRowDelta is the row's signed effect on cash, net of fee and withheld tax.
func cashRowDelta(r cashRow) float64 {
	if (r.Type == "interest" || r.Type == "dividend") && r.Amount < 0 {
		// Overdraft interest some imports store as a negative interest row:
		// money out, not income.
		return -math.Abs(r.Amount) - math.Abs(r.Fee) - math.Abs(r.Tax)
	}
	return txnSignedCashDelta(r.Type, false, r.Amount, r.Fee, r.Tax)
}

// buildCashflow aggregates classified cash rows into the running month plus
// `months` complete months, newest first, and the typical-month summary.
func buildCashflow(rows []cashRow, split loanSplitFunc, ownIBANs []string, now time.Time, months int) cashflowResponse {
	from, to := cashflowWindow(now, months)

	type key struct {
		month    string
		category cashflow.Category
	}
	agg := map[key]*monthlyBucket{}
	totals := map[string]*monthlyTotals{}
	monthOf := func(month string) *monthlyTotals {
		if totals[month] == nil {
			totals[month] = &monthlyTotals{Month: month}
		}
		return totals[month]
	}

	add := func(date time.Time, cat cashflow.Category, amount float64) {
		if amount == 0 {
			return
		}
		bucket := cashflow.BucketOf(cat)
		month := date.Format("2006-01")
		k := key{month: month, category: cat}
		if agg[k] == nil {
			agg[k] = &monthlyBucket{Month: month, Category: string(cat), Bucket: string(bucket)}
		}
		agg[k].Amount += amount
		agg[k].Count++

		// Spend buckets hold magnitudes so the table reads "spent 1.234,56 €".
		t := monthOf(month)
		switch bucket {
		case cashflow.BucketIncome:
			t.Income += amount
			if cat == cashflow.CatSalary {
				t.Salary += amount
			}
		case cashflow.BucketFixed:
			t.Fixed += -amount
		case cashflow.BucketVariable:
			t.Variable += -amount
		case cashflow.BucketTransfer:
			t.Transfer += amount
		case cashflow.BucketUnclassified:
			t.Unclassified += amount
		}
	}

	for _, row := range rows {
		if row.Date.Before(from) || row.Date.After(to) {
			continue
		}
		amount := cashRowDelta(row)
		if amount == 0 {
			continue
		}
		monthOf(row.Date.Format("2006-01")).HasData = true
		if split != nil {
			if interest, kind, ok := split(row.ID); ok {
				interestCat := cashflow.CatHousing
				if kind == assets.KindCar {
					interestCat = cashflow.CatTransport
				}
				interestPart := math.Copysign(math.Min(interest, math.Abs(amount)), amount)
				add(row.Date, cashflow.ForCashDelta(interestCat, interestPart, true), interestPart)
				add(row.Date, cashflow.CatLoanPrincipal, amount-interestPart)
				continue
			}
		}
		env := cashflow.Env{AccountType: row.AccountType, OwnIBANs: ownIBANs}
		cat := cashflow.ResolveCategoryWith(env, row.Category, row.Type, row.Counterparty, row.Reference)
		add(row.Date, cashflow.ForCashDelta(cat, amount, row.Category != ""), amount)
	}

	// Month list, newest first: the running month, then `months` complete
	// months, including months without activity.
	monthsList := make([]monthlyTotals, 0, months+1)
	cursor := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i <= months; i++ {
		mkey := cursor.Format("2006-01")
		t := monthlyTotals{Month: mkey}
		if v, ok := totals[mkey]; ok {
			t = *v
		}
		t.Net = t.Income - t.Fixed - t.Variable
		t.Partial = i == 0
		monthsList = append(monthsList, t)
		cursor = addMonthsFirstOfMonth(cursor, -1)
	}

	// Flatten bucket map to slice. Spend buckets hold signed (negative)
	// amounts; the frontend wants positive magnitudes.
	buckets := make([]monthlyBucket, 0, len(agg))
	for _, b := range agg {
		if b.Bucket == string(cashflow.BucketFixed) || b.Bucket == string(cashflow.BucketVariable) {
			b.Amount = -b.Amount
		}
		buckets = append(buckets, *b)
	}
	sort.Slice(buckets, func(i, j int) bool {
		if buckets[i].Month != buckets[j].Month {
			return buckets[i].Month > buckets[j].Month
		}
		return buckets[i].Category < buckets[j].Category
	})

	return cashflowResponse{
		From:                  from.Format("2006-01-02"),
		To:                    to.Format("2006-01-02"),
		Months:                monthsList,
		Buckets:               buckets,
		Medians:               computeMedians(monthsList),
		UnconvertedCurrencies: []string{},
	}
}

// computeMedians summarises a typical month over the last 12 complete months
// (newest first in `months`), skipping the running month and months without
// income, which are usually months before the salary account was imported.
func computeMedians(months []monthlyTotals) medianSummary {
	var window []monthlyTotals
	for _, m := range months {
		if m.Partial {
			continue
		}
		if len(window) == 12 {
			break
		}
		window = append(window, m)
	}
	var incomes, spends, nets []float64
	var sumIncome, sumNet, sumSalary float64
	for _, m := range window {
		if m.Income <= 0 {
			continue
		}
		incomes = append(incomes, m.Income)
		spends = append(spends, m.Fixed+m.Variable)
		nets = append(nets, m.Net)
		sumIncome += m.Income
		sumNet += m.Net
		sumSalary += m.Salary
	}
	out := medianSummary{
		MonthlyIncome:  roundCents(median(incomes)),
		MonthlySpend:   roundCents(median(spends)),
		MonthlySurplus: roundCents(median(nets)),
		Months:         len(incomes),
		WindowMonths:   len(window),
	}
	if sumIncome > 0 {
		rate := math.Round(sumNet/sumIncome*10000) / 100
		out.SavingsRatePct = &rate
	}
	if n := len(incomes); n > 0 {
		out.AnnualTakeHome = roundCents(sumSalary / float64(n) * 12)
	}
	return out
}

func median(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	s := append([]float64(nil), xs...)
	sort.Float64s(s)
	mid := len(s) / 2
	if len(s)%2 == 0 {
		return (s[mid-1] + s[mid]) / 2
	}
	return s[mid]
}

func roundCents(x float64) float64 { return math.Round(x*100) / 100 }

// HandleUpdateCategory writes a user override into transactions.category.
// Empty string clears the override (heuristic takes back over).
func (h *CashflowHandler) HandleUpdateCategory(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid transaction id")
		return
	}

	var body struct {
		Category string `json:"category"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body: "+err.Error())
		return
	}

	// Validate: empty string is allowed (= clear override); otherwise must be
	// one of the known categories so the heuristic/override switch in the
	// classifier doesn't silently fall through.
	if body.Category != "" && !cashflow.IsKnown(cashflow.Category(body.Category)) {
		writeError(w, http.StatusBadRequest, "unknown category")
		return
	}

	if !h.userOwnsTransaction(r.Context(), id) {
		writeError(w, http.StatusForbidden, "not your transaction")
		return
	}

	err = h.queries.UpdateTransactionCategory(r.Context(), db.UpdateTransactionCategoryParams{
		ID:       id,
		Category: pgtype.Text{String: body.Category, Valid: body.Category != ""},
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "update category: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// userOwnsTransaction returns true when auth is disabled OR the transaction's
// account belongs to the current user.
func (h *CashflowHandler) userOwnsTransaction(ctx context.Context, txID uuid.UUID) bool {
	allowed := userAccountIDs(ctx, h.queries.DB())
	if allowed == nil {
		return true
	}
	var accountID uuid.UUID
	err := h.queries.DB().QueryRow(ctx, "SELECT account_id FROM transactions WHERE id = $1", txID).Scan(&accountID)
	if err != nil {
		return false
	}
	for _, id := range allowed {
		if id == accountID {
			return true
		}
	}
	return false
}
