package handler

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/lnsp/wealth/internal/analytics"
	"github.com/lnsp/wealth/internal/assets"
	db "github.com/lnsp/wealth/internal/database/generated"
)

type loanRequest struct {
	AssetID          string   `json:"asset_id"`
	Name             string   `json:"name"`
	Lender           string   `json:"lender"`
	Principal        float64  `json:"principal"`
	PayoutDate       string   `json:"payout_date"`
	FirstPaymentDate string   `json:"first_payment_date"`
	NominalRate      float64  `json:"nominal_rate"`      // fraction, e.g. 0.0365
	MonthlyPayment   *float64 `json:"monthly_payment"`   // or derive from initial_repayment
	InitialRepayment *float64 `json:"initial_repayment"` // fraction per year, e.g. 0.02
	FixedUntil       string   `json:"fixed_until"`
	FollowupRate     *float64 `json:"followup_rate"`
	ExtraAllowance   float64  `json:"extra_allowance_pct"` // fraction of principal per year
	PaymentAccountID string   `json:"payment_account_id"`
}

func (req loanRequest) validate() (db.CreateLoanParams, error) {
	var p db.CreateLoanParams
	if req.Principal <= 0 || req.PayoutDate == "" {
		return p, errors.New("principal and payout_date are required")
	}
	payout, err := time.Parse("2006-01-02", req.PayoutDate)
	if err != nil {
		return p, errors.New("invalid payout_date (expected YYYY-MM-DD)")
	}
	first := time.Date(payout.Year(), payout.Month()+1, 1, 0, 0, 0, 0, time.UTC) // default: 1st of next month
	if req.FirstPaymentDate != "" {
		if first, err = time.Parse("2006-01-02", req.FirstPaymentDate); err != nil {
			return p, errors.New("invalid first_payment_date (expected YYYY-MM-DD)")
		}
	}
	if !first.After(payout) {
		return p, errors.New("first_payment_date must be after payout_date")
	}
	if req.NominalRate < 0 || req.NominalRate >= 1 {
		return p, errors.New("nominal_rate must be a fraction in [0, 1)")
	}
	payment := 0.0
	switch {
	case req.MonthlyPayment != nil && *req.MonthlyPayment > 0:
		payment = *req.MonthlyPayment
	case req.InitialRepayment != nil && *req.InitialRepayment > 0:
		payment = analytics.MonthlyPaymentFor(req.Principal, req.NominalRate, *req.InitialRepayment)
	default:
		return p, errors.New("monthly_payment or initial_repayment is required")
	}
	if payment <= req.Principal*req.NominalRate/12 {
		return p, errors.New("the monthly payment doesn't cover the interest")
	}
	if p.FixedUntil, err = parseOptionalDate(req.FixedUntil, "fixed_until"); err != nil {
		return p, err
	}
	if req.FollowupRate != nil {
		if *req.FollowupRate < 0 || *req.FollowupRate >= 1 {
			return p, errors.New("followup_rate must be a fraction in [0, 1)")
		}
		p.FollowupRate = numericOf(*req.FollowupRate, "%.6f")
	}
	if req.ExtraAllowance < 0 || req.ExtraAllowance > 1 {
		return p, errors.New("extra_allowance_pct must be a fraction in [0, 1]")
	}
	if req.PaymentAccountID != "" {
		id, err := uuid.Parse(req.PaymentAccountID)
		if err != nil {
			return p, errors.New("invalid payment_account_id")
		}
		p.PaymentAccountID = &id
	}
	if req.AssetID != "" {
		if p.AssetID, err = uuid.Parse(req.AssetID); err != nil {
			return p, errors.New("invalid asset_id")
		}
	}
	p.Name = req.Name
	if p.Name == "" {
		p.Name = "Loan"
	}
	p.Lender = req.Lender
	p.Principal = numericOf(req.Principal, "%.2f")
	p.PayoutDate, p.FirstPaymentDate = payout, first
	p.NominalRate = numericOf(req.NominalRate, "%.6f")
	p.MonthlyPayment = numericOf(payment, "%.2f")
	p.ExtraAllowancePct = numericOf(req.ExtraAllowance, "%.4f")
	return p, nil
}

type loanPaymentView struct {
	Due       string  `json:"due"`
	Interest  float64 `json:"interest"`
	Principal float64 `json:"principal"`
}

type loanYearView struct {
	Year      int     `json:"year"`
	Interest  float64 `json:"interest"`
	Principal float64 `json:"principal"`
	Balance   float64 `json:"balance"`
}

type matchedPaymentView struct {
	TransactionID string  `json:"transaction_id"`
	Date          string  `json:"date"`
	Due           string  `json:"due"`
	Amount        float64 `json:"amount"`
	Interest      float64 `json:"interest"`
	Principal     float64 `json:"principal"`
}

type extraRepaymentView struct {
	ID            string  `json:"id"`
	Date          string  `json:"date"`
	Amount        float64 `json:"amount"`
	TransactionID *string `json:"transaction_id"`
}

type loanView struct {
	ID                  string           `json:"id"`
	AssetID             string           `json:"asset_id"`
	AssetName           string           `json:"asset_name"`
	Name                string           `json:"name"`
	Lender              string           `json:"lender"`
	Principal           float64          `json:"principal"`
	PayoutDate          string           `json:"payout_date"`
	FirstPaymentDate    string           `json:"first_payment_date"`
	NominalRate         float64          `json:"nominal_rate"`
	InitialRepayment    float64          `json:"initial_repayment"`
	MonthlyPayment      float64          `json:"monthly_payment"`
	FixedUntil          *string          `json:"fixed_until"`
	FollowupRate        *float64         `json:"followup_rate"`
	ExtraAllowancePct   float64          `json:"extra_allowance_pct"`
	PaymentAccountID    *string          `json:"payment_account_id"`
	Balance             float64          `json:"balance"`
	Repaid              float64          `json:"repaid"`
	RepaidPct           float64          `json:"repaid_pct"`
	NextPayment         *loanPaymentView `json:"next_payment"`
	RemainingAtFixedEnd *float64         `json:"remaining_at_fixed_end"`
	MonthsLeftFixed     *int             `json:"months_left_fixed"`
	InterestPaid        float64          `json:"interest_paid"`
	InterestFixedPeriod float64          `json:"interest_fixed_period"`
	PayoffDate          string           `json:"payoff_date"`
	DueCount            int              `json:"due_count"`
	MatchedCount        int              `json:"matched_count"`
	ExtraUsedThisYear   float64          `json:"extra_used_this_year"`

	// Detail only.
	Yearly   []loanYearView       `json:"yearly,omitempty"`
	Payments []matchedPaymentView `json:"payments,omitempty"`
	Extras   []extraRepaymentView `json:"extras,omitempty"`
}

func buildLoanSummary(l assets.Loan, assetName string, now time.Time) loanView {
	today := assets.CivilDate(now)
	principal := l.Model.Principal
	balance := l.BalanceOn(today)
	v := loanView{
		ID: l.ID.String(), AssetID: l.AssetID.String(), AssetName: assetName, Name: l.Name, Lender: l.Lender,
		Principal: principal, PayoutDate: l.PayoutDate.Format("2006-01-02"),
		FirstPaymentDate: l.FirstPaymentDate.Format("2006-01-02"), NominalRate: l.Model.Rate,
		MonthlyPayment: l.Model.MonthlyPayment, FixedUntil: dateString(l.FixedUntil),
		FollowupRate: optionalNumeric(l.FollowupRate), ExtraAllowancePct: numericToFloat(l.ExtraAllowancePct),
		Balance: round2(balance), MatchedCount: len(l.Links),
	}
	if principal > 0 {
		v.InitialRepayment = math.Round((l.Model.MonthlyPayment*12/principal-l.Model.Rate)*10000) / 10000
	}
	if l.PaymentAccountID != nil {
		s := l.PaymentAccountID.String()
		v.PaymentAccountID = &s
	}
	if !today.Before(l.PayoutDate) {
		v.Repaid = round2(principal - balance)
		v.RepaidPct = round2(v.Repaid / principal * 100)
	}
	for _, p := range l.Schedule {
		if p.Due.After(today) {
			if v.NextPayment == nil {
				v.NextPayment = &loanPaymentView{Due: p.Due.Format("2006-01-02"), Interest: p.Interest, Principal: round2(p.Principal)}
			}
		} else {
			v.InterestPaid += p.Interest
			v.DueCount++
		}
		if !l.Model.FixedUntil.IsZero() && !p.Due.After(l.Model.FixedUntil) {
			v.InterestFixedPeriod += p.Interest
		}
	}
	v.InterestPaid = round2(v.InterestPaid)
	v.InterestFixedPeriod = round2(v.InterestFixedPeriod)
	if len(l.Schedule) > 0 {
		v.PayoffDate = l.Schedule[len(l.Schedule)-1].Due.Format("2006-01-02")
	}
	if !l.Model.FixedUntil.IsZero() {
		rem := round2(l.Model.BalanceAt(l.Model.FixedUntil))
		v.RemainingAtFixedEnd = &rem
		months := 0
		for d := today; d.Before(l.Model.FixedUntil); d = d.AddDate(0, 1, 0) {
			months++
		}
		v.MonthsLeftFixed = &months
	}
	for _, e := range l.Extras {
		if e.Date.Year() == today.Year() {
			v.ExtraUsedThisYear += numericToFloat(e.Amount)
		}
	}
	return v
}

func (h *AssetsHandler) buildLoanDetail(ctx context.Context, holdings assets.Holdings, l assets.Loan, now time.Time) loanView {
	v := buildLoanSummary(l, assetName(holdings, l.AssetID), now)

	// Yearly plan until the fixed period ends (or the loan is repaid).
	end := l.Model.FixedUntil
	byYear := map[int]*loanYearView{}
	var years []int
	for _, p := range l.Schedule {
		if !end.IsZero() && p.Due.After(end) {
			break
		}
		y := p.Due.Year()
		if byYear[y] == nil {
			byYear[y] = &loanYearView{Year: y}
			years = append(years, y)
		}
		byYear[y].Interest += p.Interest
		byYear[y].Principal += p.Principal
		byYear[y].Balance = p.Balance
	}
	for _, y := range years {
		yv := byYear[y]
		v.Yearly = append(v.Yearly, loanYearView{Year: y, Interest: round2(yv.Interest), Principal: round2(yv.Principal), Balance: round2(yv.Balance)})
	}

	due := make(map[time.Time]analytics.LoanPayment, len(l.Schedule))
	for _, p := range l.Schedule {
		due[p.Due] = p
	}
	for _, link := range l.Links {
		txn, err := h.queries.GetTransaction(ctx, link.TransactionID)
		if err != nil {
			continue
		}
		p := due[link.DueDate]
		v.Payments = append(v.Payments, matchedPaymentView{
			TransactionID: txn.ID.String(), Date: txn.Date.Format("2006-01-02"), Due: link.DueDate.Format("2006-01-02"),
			Amount: math.Abs(numericToFloat(txn.Amount)), Interest: p.Interest, Principal: round2(p.Principal),
		})
	}
	sort.Slice(v.Payments, func(i, j int) bool { return v.Payments[i].Due > v.Payments[j].Due })
	for _, e := range l.Extras {
		ev := extraRepaymentView{ID: e.ID.String(), Date: e.Date.Format("2006-01-02"), Amount: numericToFloat(e.Amount)}
		if e.TransactionID != nil {
			s := e.TransactionID.String()
			ev.TransactionID = &s
		}
		v.Extras = append(v.Extras, ev)
	}
	return v
}

func (h *AssetsHandler) findLoan(w http.ResponseWriter, ctx context.Context, id uuid.UUID) (assets.Holdings, assets.Loan, bool) {
	holdings, err := assets.Load(ctx, h.queries)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "load loans: "+err.Error())
		return holdings, assets.Loan{}, false
	}
	for _, l := range holdings.Loans {
		if l.ID == id {
			return holdings, l, true
		}
	}
	writeError(w, http.StatusNotFound, "loan not found")
	return holdings, assets.Loan{}, false
}

func (h *AssetsHandler) writeLoan(w http.ResponseWriter, ctx context.Context, status int, id uuid.UUID) {
	holdings, l, ok := h.findLoan(w, ctx, id)
	if !ok {
		return
	}
	writeJSON(w, status, h.buildLoanDetail(ctx, holdings, l, time.Now()))
}

// clearLoanCategories untags a loan's matched payments and extra repayments.
func (h *AssetsHandler) clearLoanCategories(ctx context.Context, l assets.Loan) error {
	for _, p := range l.Links {
		if err := h.clearCategory(ctx, p.TransactionID, assets.CategoryLoanPayment); err != nil {
			return err
		}
	}
	for _, e := range l.Extras {
		if e.TransactionID != nil {
			if err := h.clearCategory(ctx, *e.TransactionID, assets.CategoryLoanPayment); err != nil {
				return err
			}
		}
	}
	return nil
}

// HandleGetLoan returns a loan with its yearly plan and matched payments.
func (h *AssetsHandler) HandleGetLoan(w http.ResponseWriter, r *http.Request) {
	id, ok := parseIDParam(w, r, "id", "loan")
	if !ok {
		return
	}
	h.writeLoan(w, r.Context(), http.StatusOK, id)
}

// HandleCreateLoan adds a loan to an existing asset and matches its payments.
func (h *AssetsHandler) HandleCreateLoan(w http.ResponseWriter, r *http.Request) {
	var req loanRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	p, err := req.validate()
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if p.AssetID == uuid.Nil {
		writeError(w, http.StatusBadRequest, "asset_id is required")
		return
	}
	ctx := r.Context()
	if _, err := h.queries.GetAsset(ctx, p.AssetID); err != nil {
		writeError(w, http.StatusBadRequest, "asset not found")
		return
	}
	row, err := h.queries.CreateLoan(ctx, p)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "create loan: "+err.Error())
		return
	}
	if _, err := assets.MatchAllLoanPayments(ctx, h.queries); err != nil {
		writeError(w, http.StatusInternalServerError, "match loan payments: "+err.Error())
		return
	}
	h.refreshSnapshots()
	h.writeLoan(w, ctx, http.StatusCreated, row.ID)
}

// HandleUpdateLoan replaces a loan's terms. Matched payments are re-matched
// because due dates or amounts may have changed.
func (h *AssetsHandler) HandleUpdateLoan(w http.ResponseWriter, r *http.Request) {
	id, ok := parseIDParam(w, r, "id", "loan")
	if !ok {
		return
	}
	var req loanRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	p, err := req.validate()
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx := r.Context()
	if _, _, ok := h.findLoan(w, ctx, id); !ok {
		return
	}
	unlinked, err := h.queries.DeleteLoanPaymentLinksForLoan(ctx, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "reset payment links: "+err.Error())
		return
	}
	for _, txnID := range unlinked {
		if err := h.clearCategory(ctx, txnID, assets.CategoryLoanPayment); err != nil {
			writeError(w, http.StatusInternalServerError, "untag payment: "+err.Error())
			return
		}
	}
	if _, err := h.queries.UpdateLoan(ctx, db.UpdateLoanParams{
		ID: id, Name: p.Name, Lender: p.Lender, Principal: p.Principal, PayoutDate: p.PayoutDate,
		FirstPaymentDate: p.FirstPaymentDate, NominalRate: p.NominalRate, MonthlyPayment: p.MonthlyPayment,
		FixedUntil: p.FixedUntil, FollowupRate: p.FollowupRate, ExtraAllowancePct: p.ExtraAllowancePct,
		PaymentAccountID: p.PaymentAccountID,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "update loan: "+err.Error())
		return
	}
	if _, err := assets.MatchAllLoanPayments(ctx, h.queries); err != nil {
		writeError(w, http.StatusInternalServerError, "match loan payments: "+err.Error())
		return
	}
	h.refreshSnapshots()
	h.writeLoan(w, ctx, http.StatusOK, id)
}

// HandleDeleteLoan removes a loan and untags its payments.
func (h *AssetsHandler) HandleDeleteLoan(w http.ResponseWriter, r *http.Request) {
	id, ok := parseIDParam(w, r, "id", "loan")
	if !ok {
		return
	}
	ctx := r.Context()
	_, l, ok := h.findLoan(w, ctx, id)
	if !ok {
		return
	}
	if err := h.clearLoanCategories(ctx, l); err != nil {
		writeError(w, http.StatusInternalServerError, "untag payments: "+err.Error())
		return
	}
	if err := h.queries.DeleteLoan(ctx, id); err != nil {
		writeError(w, http.StatusInternalServerError, "delete loan: "+err.Error())
		return
	}
	h.refreshSnapshots()
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// HandleCreateExtraRepayment records a Sondertilgung, optionally linked to the
// transaction that paid it.
func (h *AssetsHandler) HandleCreateExtraRepayment(w http.ResponseWriter, r *http.Request) {
	id, ok := parseIDParam(w, r, "id", "loan")
	if !ok {
		return
	}
	var req struct {
		Date          string  `json:"date"`
		Amount        float64 `json:"amount"`
		TransactionID string  `json:"transaction_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	date, err := time.Parse("2006-01-02", req.Date)
	if err != nil || req.Amount <= 0 {
		writeError(w, http.StatusBadRequest, "date (YYYY-MM-DD) and a positive amount are required")
		return
	}
	ctx := r.Context()
	holdings, l, ok := h.findLoan(w, ctx, id)
	if !ok {
		return
	}
	if date.Before(l.PayoutDate) {
		writeError(w, http.StatusBadRequest, "an extra repayment can't be before the payout")
		return
	}
	var txnID *uuid.UUID
	if req.TransactionID != "" {
		tid, err := uuid.Parse(req.TransactionID)
		if err != nil || holdings.LinkedIDs()[tid] {
			writeError(w, http.StatusBadRequest, errBadPurchaseTxn.Error())
			return
		}
		txnID = &tid
	}
	if _, err := h.queries.CreateLoanExtraRepayment(ctx, db.CreateLoanExtraRepaymentParams{
		LoanID: id, Date: date, Amount: numericOf(req.Amount, "%.2f"), TransactionID: txnID,
	}); err != nil {
		writeError(w, http.StatusBadRequest, "record extra repayment: "+err.Error())
		return
	}
	if txnID != nil {
		if err := h.queries.UpdateTransactionCategory(ctx, db.UpdateTransactionCategoryParams{
			ID: *txnID, Category: pgtype.Text{String: assets.CategoryLoanPayment, Valid: true},
		}); err != nil {
			writeError(w, http.StatusInternalServerError, "tag payment: "+err.Error())
			return
		}
	}
	h.refreshSnapshots()
	h.writeLoan(w, ctx, http.StatusCreated, id)
}

// HandleDeleteExtraRepayment removes a Sondertilgung.
func (h *AssetsHandler) HandleDeleteExtraRepayment(w http.ResponseWriter, r *http.Request) {
	id, ok := parseIDParam(w, r, "id", "loan")
	if !ok {
		return
	}
	extraID, ok := parseIDParam(w, r, "extraID", "extra repayment")
	if !ok {
		return
	}
	ctx := r.Context()
	txnID, err := h.queries.DeleteLoanExtraRepayment(ctx, db.DeleteLoanExtraRepaymentParams{ID: extraID, LoanID: id})
	if err != nil {
		writeError(w, http.StatusNotFound, "extra repayment not found")
		return
	}
	if txnID != nil {
		if err := h.clearCategory(ctx, *txnID, assets.CategoryLoanPayment); err != nil {
			writeError(w, http.StatusInternalServerError, "untag payment: "+err.Error())
			return
		}
	}
	h.refreshSnapshots()
	h.writeLoan(w, ctx, http.StatusOK, id)
}

// HandleLoanScenario answers "what if I repay extra / rates change": extra
// repayments each December until the fixed period ends and a follow-up rate.
func (h *AssetsHandler) HandleLoanScenario(w http.ResponseWriter, r *http.Request) {
	id, ok := parseIDParam(w, r, "id", "loan")
	if !ok {
		return
	}
	extra, followup := 0.0, -1.0
	if s := r.URL.Query().Get("extra_per_year"); s != "" {
		v, err := strconv.ParseFloat(s, 64)
		if err != nil || v < 0 {
			writeError(w, http.StatusBadRequest, "invalid extra_per_year")
			return
		}
		extra = v
	}
	if s := r.URL.Query().Get("followup_rate"); s != "" {
		v, err := strconv.ParseFloat(s, 64)
		if err != nil || v < 0 || v >= 1 {
			writeError(w, http.StatusBadRequest, "invalid followup_rate")
			return
		}
		followup = v
	}
	_, l, ok := h.findLoan(w, r.Context(), id)
	if !ok {
		return
	}
	today := assets.CivilDate(time.Now())
	base := l.Model.Scenario(today, 0, -1)
	scenario := l.Model.Scenario(today, extra, followup)
	writeJSON(w, http.StatusOK, map[string]any{
		"base":           scenarioJSON(base),
		"scenario":       scenarioJSON(scenario),
		"interest_saved": round2(base.TotalInterest - scenario.TotalInterest),
	})
}

func scenarioJSON(s analytics.LoanScenario) map[string]any {
	return map[string]any{
		"remaining_at_fixed_end": round2(s.RemainingAtFixedEnd),
		"payoff_date":            s.PayoffDate.Format("2006-01-02"),
		"total_interest":         s.TotalInterest,
		"interest_after_fixed":   s.InterestAfterFixed,
	}
}

// HandleMatchLoanPayments re-runs payment matching for all loans.
func (h *AssetsHandler) HandleMatchLoanPayments(w http.ResponseWriter, r *http.Request) {
	n, err := assets.MatchAllLoanPayments(r.Context(), h.queries)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "match loan payments: "+err.Error())
		return
	}
	if n > 0 {
		h.refreshSnapshots()
	}
	writeJSON(w, http.StatusOK, map[string]int{"matched": n})
}
