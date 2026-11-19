package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/lnsp/wealth/internal/analytics"
	db "github.com/lnsp/wealth/internal/database/generated"
)

// planningProfile is the household's planning profile: personal facts, the
// tax and insurance inputs of the payroll calculation, the savings rule and
// the assumptions beyond enacted law.
type planningProfile struct {
	BirthYear           *int     `json:"birth_year"`
	RetirementAge       int      `json:"retirement_age"`
	Children            int      `json:"children"`
	ChurchRate          float64  `json:"church_rate"`
	HealthKind          string   `json:"health_kind"`
	GKVZusatzbeitrag    float64  `json:"gkv_zusatzbeitrag"`
	PKVPremiumMonthly   float64  `json:"pkv_premium_monthly"`
	PKVBasicShare       float64  `json:"pkv_basic_share"`
	SavingsMode         string   `json:"savings_mode"`
	RunningCostsMonthly float64  `json:"running_costs_monthly"`
	SavingsRate         float64  `json:"savings_rate"`
	RSUCountFull        bool     `json:"rsu_count_full"`
	Inflation           float64  `json:"inflation"`
	WageGrowth          float64  `json:"wage_growth"`
	ShareDrift          float64  `json:"share_drift"`
	USDPerEUR           *float64 `json:"usd_per_eur"`
	Saved               bool     `json:"saved"`
}

func defaultPlanningProfile() planningProfile {
	return planningProfile{
		RetirementAge: 67, HealthKind: "gkv", GKVZusatzbeitrag: analytics.KVZusatzbeitrag2026,
		PKVBasicShare: 0.85, SavingsMode: "costs", SavingsRate: 0.4, RSUCountFull: true,
		Inflation: analytics.PlanningDefaultInflationPct / 100, WageGrowth: 0.03,
	}
}

func profileFromRow(r db.PlanningProfile) planningProfile {
	p := planningProfile{
		RetirementAge:       int(r.RetirementAge),
		Children:            int(r.Children),
		ChurchRate:          numericToFloat(r.ChurchRate),
		HealthKind:          r.HealthKind,
		GKVZusatzbeitrag:    numericToFloat(r.GkvZusatzbeitrag),
		PKVPremiumMonthly:   numericToFloat(r.PkvPremiumMonthly),
		PKVBasicShare:       numericToFloat(r.PkvBasicShare),
		SavingsMode:         r.SavingsMode,
		RunningCostsMonthly: numericToFloat(r.RunningCostsMonthly),
		SavingsRate:         numericToFloat(r.SavingsRate),
		RSUCountFull:        r.RsuCountFull,
		Inflation:           numericToFloat(r.Inflation),
		WageGrowth:          numericToFloat(r.WageGrowth),
		ShareDrift:          numericToFloat(r.ShareDrift),
		Saved:               true,
	}
	if r.BirthYear.Valid {
		y := int(r.BirthYear.Int32)
		p.BirthYear = &y
	}
	if r.UsdPerEur.Valid {
		v := numericToFloat(r.UsdPerEur)
		p.USDPerEUR = &v
	}
	return p
}

func (p planningProfile) validate() error {
	finite := func(vs ...float64) bool {
		for _, v := range vs {
			if math.IsNaN(v) || math.IsInf(v, 0) {
				return false
			}
		}
		return true
	}
	switch {
	case !finite(p.ChurchRate, p.GKVZusatzbeitrag, p.PKVPremiumMonthly, p.PKVBasicShare, p.RunningCostsMonthly, p.SavingsRate, p.Inflation, p.WageGrowth, p.ShareDrift):
		return errors.New("numbers must be finite")
	case p.BirthYear != nil && (*p.BirthYear < 1900 || *p.BirthYear > 2100):
		return errors.New("birth_year out of range")
	case p.RetirementAge < 40 || p.RetirementAge > 80:
		return errors.New("retirement_age must be between 40 and 80")
	case p.Children < 0 || p.Children > 20:
		return errors.New("children out of range")
	case p.ChurchRate != 0 && p.ChurchRate != 0.08 && p.ChurchRate != 0.09:
		return errors.New("church_rate must be 0, 0.08 or 0.09")
	case p.HealthKind != "gkv" && p.HealthKind != "pkv":
		return errors.New("health_kind must be gkv or pkv")
	case p.GKVZusatzbeitrag < 0 || p.GKVZusatzbeitrag > 0.1:
		return errors.New("gkv_zusatzbeitrag out of range")
	case p.PKVPremiumMonthly < 0 || p.PKVPremiumMonthly > 1e5 || p.PKVBasicShare < 0 || p.PKVBasicShare > 1:
		return errors.New("private insurance inputs out of range")
	case p.SavingsMode != "costs" && p.SavingsMode != "rate":
		return errors.New("savings_mode must be costs or rate")
	case p.RunningCostsMonthly < 0 || p.RunningCostsMonthly > 1e7 || p.SavingsRate < 0 || p.SavingsRate > 1:
		return errors.New("savings inputs out of range")
	case p.Inflation < -0.05 || p.Inflation > 0.2 || p.WageGrowth < -0.05 || p.WageGrowth > 0.2 || p.ShareDrift < -0.5 || p.ShareDrift > 0.5:
		return errors.New("assumptions out of range")
	case p.USDPerEUR != nil && (!finite(*p.USDPerEUR) || *p.USDPerEUR <= 0 || *p.USDPerEUR > 100):
		return errors.New("usd_per_eur out of range")
	}
	return nil
}

func (p planningProfile) payroll() analytics.PayrollProfile {
	return analytics.PayrollProfile{
		Health: p.HealthKind, Zusatzbeitrag: p.GKVZusatzbeitrag,
		PKVPremiumMonthly: p.PKVPremiumMonthly, PKVBasicShare: p.PKVBasicShare,
		ChurchRate: p.ChurchRate, Children: p.Children,
	}
}

func (h *PortfolioHandler) loadPlanningProfile(ctx context.Context) planningProfile {
	row, err := h.queries.GetPlanningProfile(ctx)
	if err != nil {
		return defaultPlanningProfile()
	}
	return profileFromRow(row)
}

func optInt4(v *int) pgtype.Int4 {
	if v == nil {
		return pgtype.Int4{}
	}
	return pgtype.Int4{Int32: int32(*v), Valid: true}
}

func optNumeric(v *float64) pgtype.Numeric {
	if v == nil {
		return pgtype.Numeric{}
	}
	return numericFromFloat(*v)
}

func (h *PortfolioHandler) HandleGetPlanningProfile(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, h.loadPlanningProfile(r.Context()))
}

func (h *PortfolioHandler) HandlePutPlanningProfile(w http.ResponseWriter, r *http.Request) {
	p := defaultPlanningProfile()
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if err := p.validate(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	row, err := h.queries.UpsertPlanningProfile(r.Context(), db.UpsertPlanningProfileParams{
		BirthYear:           optInt4(p.BirthYear),
		RetirementAge:       int32(p.RetirementAge),
		Children:            int32(p.Children),
		ChurchRate:          numericFromFloat(p.ChurchRate),
		HealthKind:          p.HealthKind,
		GkvZusatzbeitrag:    numericFromFloat(p.GKVZusatzbeitrag),
		PkvPremiumMonthly:   numericFromFloat(p.PKVPremiumMonthly),
		PkvBasicShare:       numericFromFloat(p.PKVBasicShare),
		SavingsMode:         p.SavingsMode,
		RunningCostsMonthly: numericFromFloat(p.RunningCostsMonthly),
		SavingsRate:         numericFromFloat(p.SavingsRate),
		RsuCountFull:        p.RSUCountFull,
		Inflation:           numericFromFloat(p.Inflation),
		WageGrowth:          numericFromFloat(p.WageGrowth),
		ShareDrift:          numericFromFloat(p.ShareDrift),
		UsdPerEur:           optNumeric(p.USDPerEUR),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "save profile: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, profileFromRow(row))
}

// --- Income streams ---

type incomeStepJSON struct {
	Date   string  `json:"date"`
	Amount float64 `json:"amount"`
}

type incomeStreamJSON struct {
	ID           string           `json:"id,omitempty"`
	Kind         string           `json:"kind"`
	Name         string           `json:"name"`
	Currency     string           `json:"currency"`
	Amount       float64          `json:"amount"`
	Growth       float64          `json:"growth"`
	Month        *int             `json:"month"`
	PayoutFactor *float64         `json:"payout_factor"`
	VestMonths   *int             `json:"vest_months"`
	VestEvery    *int             `json:"vest_every"`
	CliffMonths  int              `json:"cliff_months"`
	AccountID    *string          `json:"account_id"`
	FirstYear    *int             `json:"first_year"`
	VestWeights  []float64        `json:"vest_weights"`
	StartDate    string           `json:"start_date"`
	EndDate      *string          `json:"end_date"`
	Steps        []incomeStepJSON `json:"steps"`
	CreatedAt    string           `json:"created_at,omitempty"`
}

func intPtr(v pgtype.Int4) *int {
	if !v.Valid {
		return nil
	}
	i := int(v.Int32)
	return &i
}

func floatPtr(v pgtype.Numeric) *float64 {
	if !v.Valid {
		return nil
	}
	f := numericToFloat(v)
	return &f
}

func streamFromRow(s db.IncomeStream, steps []db.IncomeStreamStep) incomeStreamJSON {
	out := incomeStreamJSON{
		ID: s.ID.String(), Kind: s.Kind, Name: s.Name, Currency: s.Currency,
		Amount: numericToFloat(s.Amount), Growth: numericToFloat(s.Growth),
		Month: intPtr(s.Month), PayoutFactor: floatPtr(s.PayoutFactor),
		VestMonths: intPtr(s.VestMonths), VestEvery: intPtr(s.VestEvery),
		CliffMonths: int(s.CliffMonths), FirstYear: intPtr(s.FirstYear), VestWeights: s.VestWeights,
		StartDate: s.StartDate.Format("2006-01-02"), Steps: []incomeStepJSON{},
		CreatedAt: s.CreatedAt.UTC().Format(time.RFC3339),
	}
	if s.AccountID != nil {
		id := s.AccountID.String()
		out.AccountID = &id
	}
	if s.EndDate.Valid {
		d := s.EndDate.Time.Format("2006-01-02")
		out.EndDate = &d
	}
	for _, st := range steps {
		if st.StreamID == s.ID {
			out.Steps = append(out.Steps, incomeStepJSON{Date: st.EffectiveDate.Format("2006-01-02"), Amount: numericToFloat(st.Amount)})
		}
	}
	return out
}

type parsedStream struct {
	params db.CreateIncomeStreamParams
	steps  []db.InsertIncomeStreamStepParams
}

func (s incomeStreamJSON) parse() (parsedStream, error) {
	var out parsedStream
	s.Name = strings.TrimSpace(s.Name)
	s.Currency = strings.ToUpper(strings.TrimSpace(s.Currency))
	if s.Currency == "" {
		s.Currency = "EUR"
	}
	finite := func(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }
	switch {
	case s.Kind != analytics.IncomeSalary && s.Kind != analytics.IncomeBonus && s.Kind != analytics.IncomeRSURefresh && s.Kind != analytics.IncomeOneOff:
		return out, errors.New("kind must be salary, bonus, rsu_refresh or one_off")
	case s.Name == "" || len(s.Name) > 100:
		return out, errors.New("name is required")
	case s.Currency != "EUR" && s.Currency != "USD":
		return out, errors.New("currency must be EUR or USD")
	case !finite(s.Amount) || s.Amount < 0 || s.Amount > 1e8 || !finite(s.Growth) || s.Growth < -0.5 || s.Growth > 1:
		return out, errors.New("amount or growth out of range")
	case s.Month != nil && (*s.Month < 1 || *s.Month > 12):
		return out, errors.New("month must be 1–12")
	case s.CliffMonths < 0 || s.CliffMonths > 120:
		return out, errors.New("cliff_months out of range")
	}
	start, err := time.Parse("2006-01-02", s.StartDate)
	if err != nil {
		return out, errors.New("start_date must be YYYY-MM-DD")
	}
	end := pgtype.Date{}
	if s.EndDate != nil && *s.EndDate != "" {
		e, err := time.Parse("2006-01-02", *s.EndDate)
		if err != nil || e.Before(start) {
			return out, errors.New("end_date must be YYYY-MM-DD and not before start_date")
		}
		end = pgtype.Date{Time: e, Valid: true}
	}
	if s.Kind != analytics.IncomeRSURefresh && len(s.VestWeights) > 0 {
		return out, errors.New("only an RSU refresh has a vesting shape")
	}
	switch s.Kind {
	case analytics.IncomeBonus:
		if s.Month == nil || s.PayoutFactor == nil || !finite(*s.PayoutFactor) || *s.PayoutFactor < 0 || *s.PayoutFactor > 5 || s.Amount > 5 {
			return out, errors.New("a bonus needs a payout month, a payout factor and a target share of base salary")
		}
	case analytics.IncomeRSURefresh:
		if s.Month == nil || s.VestMonths == nil || *s.VestMonths <= 0 || *s.VestMonths > 120 || s.VestEvery == nil {
			return out, errors.New("an RSU refresh needs a grant month and a vesting schedule")
		}
		if e := *s.VestEvery; e != 1 && e != 3 && e != 6 && e != 12 {
			return out, errors.New("vest_every must be 1, 3, 6 or 12")
		}
		if len(s.VestWeights) > 0 {
			weights, err := normalizeVestWeights(s.VestWeights, *s.VestMonths)
			if err != nil {
				return out, err
			}
			s.VestWeights = weights
		}
	}
	var account *uuid.UUID
	if s.AccountID != nil && *s.AccountID != "" {
		id, err := uuid.Parse(*s.AccountID)
		if err != nil {
			return out, errors.New("invalid account_id")
		}
		account = &id
	}
	growth := s.Growth
	out.params = db.CreateIncomeStreamParams{
		Kind: s.Kind, Name: s.Name, Currency: s.Currency,
		Amount: numericFromFloat(s.Amount), Growth: numericFromFloat(growth),
		Month: optInt4(s.Month), PayoutFactor: optNumeric(s.PayoutFactor),
		VestMonths: optInt4(s.VestMonths), VestEvery: optInt4(s.VestEvery),
		CliffMonths: int32(s.CliffMonths), AccountID: account, FirstYear: optInt4(s.FirstYear),
		StartDate: start, EndDate: end, VestWeights: s.VestWeights,
	}
	if s.Kind != analytics.IncomeSalary && len(s.Steps) > 0 {
		return out, errors.New("only a salary has step changes")
	}
	seen := map[string]bool{}
	for _, st := range s.Steps {
		d, err := time.Parse("2006-01-02", st.Date)
		if err != nil || !finite(st.Amount) || st.Amount < 0 || st.Amount > 1e8 || seen[st.Date] {
			return out, errors.New("each step needs a unique date and an amount")
		}
		seen[st.Date] = true
		out.steps = append(out.steps, db.InsertIncomeStreamStepParams{EffectiveDate: d, Amount: numericFromFloat(st.Amount)})
	}
	return out, nil
}

func (h *PortfolioHandler) listStreamsJSON(ctx context.Context) ([]incomeStreamJSON, error) {
	rows, err := h.queries.ListIncomeStreams(ctx)
	if err != nil {
		return nil, err
	}
	steps, err := h.queries.ListIncomeStreamSteps(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]incomeStreamJSON, 0, len(rows))
	for _, s := range rows {
		out = append(out, streamFromRow(s, steps))
	}
	return out, nil
}

func (h *PortfolioHandler) HandleListIncomeStreams(w http.ResponseWriter, r *http.Request) {
	out, err := h.listStreamsJSON(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list income streams: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"streams": out})
}

func (h *PortfolioHandler) saveStream(ctx context.Context, id *uuid.UUID, ps parsedStream) (uuid.UUID, error) {
	var saved uuid.UUID
	err := withTx(ctx, h.queries, func(q *db.Queries) error {
		if id == nil {
			row, err := q.CreateIncomeStream(ctx, ps.params)
			if err != nil {
				return err
			}
			saved = row.ID
		} else {
			p := ps.params
			row, err := q.UpdateIncomeStream(ctx, db.UpdateIncomeStreamParams{
				ID: *id, Name: p.Name, Currency: p.Currency, Amount: p.Amount, Growth: p.Growth,
				Month: p.Month, PayoutFactor: p.PayoutFactor, VestMonths: p.VestMonths,
				VestEvery: p.VestEvery, CliffMonths: p.CliffMonths, AccountID: p.AccountID,
				FirstYear: p.FirstYear, StartDate: p.StartDate, EndDate: p.EndDate,
				VestWeights: p.VestWeights,
			})
			if err != nil {
				return err
			}
			if row.Kind != p.Kind {
				return errKindChange
			}
			saved = row.ID
			if err := q.DeleteIncomeStreamSteps(ctx, saved); err != nil {
				return err
			}
		}
		for _, st := range ps.steps {
			st.StreamID = saved
			if err := q.InsertIncomeStreamStep(ctx, st); err != nil {
				return err
			}
		}
		return nil
	})
	return saved, err
}

var errKindChange = errors.New("a stream's kind can't change")

// normalizeVestWeights checks one share per vesting year that adds up to 100 %
// and rescales it to sum exactly to 1.
func normalizeVestWeights(weights []float64, vestMonths int) ([]float64, error) {
	if vestMonths%12 != 0 || len(weights) != vestMonths/12 {
		return nil, fmt.Errorf("a vesting shape needs one share per year (%d months)", vestMonths)
	}
	var total float64
	for _, w := range weights {
		if math.IsNaN(w) || math.IsInf(w, 0) || w < 0 || w > 1 {
			return nil, errors.New("each year's share must be between 0 and 100 %")
		}
		total += w
	}
	if math.Abs(total-1) > 0.005 {
		return nil, errors.New("the yearly shares must add up to 100 %")
	}
	out := make([]float64, len(weights))
	for i, w := range weights {
		out[i] = w / total
	}
	return out, nil
}

func (h *PortfolioHandler) writeStream(w http.ResponseWriter, ctx context.Context, id uuid.UUID, status int) {
	all, err := h.listStreamsJSON(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "load income stream: "+err.Error())
		return
	}
	for _, s := range all {
		if s.ID == id.String() {
			writeJSON(w, status, s)
			return
		}
	}
	writeError(w, http.StatusNotFound, "income stream not found")
}

func (h *PortfolioHandler) HandleCreateIncomeStream(w http.ResponseWriter, r *http.Request) {
	var req incomeStreamJSON
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	ps, err := req.parse()
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	id, err := h.saveStream(r.Context(), nil, ps)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "create income stream: "+err.Error())
		return
	}
	h.writeStream(w, r.Context(), id, http.StatusCreated)
}

func (h *PortfolioHandler) HandleUpdateIncomeStream(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var req incomeStreamJSON
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	ps, err := req.parse()
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if _, err := h.saveStream(r.Context(), &id, ps); err != nil {
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			writeError(w, http.StatusNotFound, "income stream not found")
		case errors.Is(err, errKindChange):
			writeError(w, http.StatusBadRequest, err.Error())
		default:
			writeError(w, http.StatusInternalServerError, "update income stream: "+err.Error())
		}
		return
	}
	h.writeStream(w, r.Context(), id, http.StatusOK)
}

func (h *PortfolioHandler) HandleDeleteIncomeStream(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	if err := h.queries.DeleteIncomeStream(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, "delete income stream: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// --- The schedule ---

type heldSummary struct {
	Currency     string  `json:"currency"`
	ToVest       float64 `json:"to_vest"`
	LastVest     string  `json:"last_vest,omitempty"`
	ImportedOn   string  `json:"imported_on,omitempty"`
	LatestGrant  string  `json:"latest_grant_first_vest,omitempty"`
	GrantsListed int     `json:"grants_listed"`
}

// incomeInputs is everything the schedule needs, loaded once per request.
type incomeInputs struct {
	profile planningProfile
	streams []analytics.IncomeStream
	held    []analytics.HeldVest
	grants  []analytics.SeenGrant
	fx      map[string]float64
	summary heldSummary
}

func (h *PortfolioHandler) loadIncomeInputs(ctx context.Context, now time.Time) (incomeInputs, error) {
	in := incomeInputs{profile: h.loadPlanningProfile(ctx), fx: loadFXRatesMap(ctx, h.queries)}
	if in.profile.USDPerEUR != nil {
		in.fx["USD"] = *in.profile.USDPerEUR
	}
	rows, err := h.queries.ListIncomeStreams(ctx)
	if err != nil {
		return in, fmt.Errorf("list income streams: %w", err)
	}
	steps, err := h.queries.ListIncomeStreamSteps(ctx)
	if err != nil {
		return in, fmt.Errorf("list income steps: %w", err)
	}
	for _, s := range rows {
		st := analytics.IncomeStream{
			Kind: s.Kind, Currency: s.Currency, Amount: numericToFloat(s.Amount), Growth: numericToFloat(s.Growth),
			CliffMonths: int(s.CliffMonths), Start: s.StartDate, Created: s.CreatedAt,
		}
		if s.Month.Valid {
			st.Month = int(s.Month.Int32)
		}
		if s.PayoutFactor.Valid {
			st.PayoutFactor = numericToFloat(s.PayoutFactor)
		}
		if s.VestMonths.Valid {
			st.VestMonths = int(s.VestMonths.Int32)
		}
		if s.VestEvery.Valid {
			st.VestEvery = int(s.VestEvery.Int32)
		}
		if s.FirstYear.Valid {
			st.FirstYear = int(s.FirstYear.Int32)
		}
		st.VestWeights = s.VestWeights
		if s.EndDate.Valid {
			st.End = s.EndDate.Time
		}
		for _, x := range steps {
			if x.StreamID == s.ID {
				st.Steps = append(st.Steps, analytics.IncomeStep{Date: x.EffectiveDate, Amount: numericToFloat(x.Amount)})
			}
		}
		in.streams = append(in.streams, st)
	}

	accounts, err := h.queries.ListAccounts(ctx)
	if err != nil {
		return in, fmt.Errorf("list accounts: %w", err)
	}
	var lastVest time.Time
	for _, acc := range accounts {
		if acc.Institution != "morgan_stanley" {
			continue
		}
		vests, err := h.queries.ListUnvestedRSUVests(ctx, acc.ID)
		if err != nil {
			continue
		}
		prices := map[string]struct {
			close    float64
			currency string
		}{}
		for _, v := range vests {
			if vestIsPast(v.VestDate, now) || !v.SecurityISIN.Valid {
				continue
			}
			isin := v.SecurityISIN.String
			p, ok := prices[isin]
			if !ok {
				if lp, err := h.queries.GetLatestPrice(ctx, isin); err == nil {
					p.close, p.currency = numericToFloat(lp.Close), lp.Currency
				}
				prices[isin] = p
			}
			if p.close <= 0 {
				continue
			}
			currency := p.currency
			if currency == "" {
				currency = v.Currency
			}
			amount := numericToFloat(v.GrossQuantity) * p.close
			in.held = append(in.held, analytics.HeldVest{Date: v.VestDate, Amount: amount, Currency: currency})
			in.summary.Currency = currency
			in.summary.ToVest += amount
			if v.VestDate.After(lastVest) {
				lastVest = v.VestDate
			}
		}
	}
	if !lastVest.IsZero() {
		in.summary.LastVest = lastVest.Format("2006-01-02")
	}
	grants, err := h.queries.ListRSUGrants(ctx)
	if err != nil {
		return in, fmt.Errorf("list rsu grants: %w", err)
	}
	var imported, latest time.Time
	for _, g := range grants {
		in.grants = append(in.grants, analytics.SeenGrant{FirstSeen: g.FirstSeenOn, FirstVest: g.FirstVestDate})
		if g.LastSeenOn.After(imported) {
			imported = g.LastSeenOn
		}
		if g.FirstVestDate.After(latest) {
			latest = g.FirstVestDate
		}
	}
	in.summary.GrantsListed = len(grants)
	if !imported.IsZero() {
		in.summary.ImportedOn = imported.Format("2006-01-02")
	}
	if !latest.IsZero() {
		in.summary.LatestGrant = latest.Format("2006-01-02")
	}
	return in, nil
}

// retirementStart is the first month without employment income: January of
// the year the household turns the retirement age, or 40 years out.
func (p planningProfile) retirementStart(now time.Time) time.Time {
	if p.BirthYear != nil {
		return time.Date(*p.BirthYear+p.RetirementAge, 1, 1, 0, 0, 0, 0, time.UTC)
	}
	return time.Date(now.Year()+40, 1, 1, 0, 0, 0, 0, time.UTC)
}

const maxScheduleMonths = 600

func (in incomeInputs) schedule(now time.Time) analytics.Schedule {
	start := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	stop := in.profile.retirementStart(now)
	months := (stop.Year()-start.Year())*12 + int(stop.Month()) - int(start.Month())
	months = min(max(months, 12), maxScheduleMonths)
	p := in.profile
	return analytics.BuildSchedule(analytics.ScheduleInput{
		Start: start, Months: months, Stop: stop,
		Streams: in.streams, Held: in.held, Grants: in.grants,
		Profile: p.payroll(),
		Assume:  analytics.ScheduleAssumptions{Inflation: p.Inflation, WageGrowth: p.WageGrowth, ShareDrift: p.ShareDrift, FX: in.fx},
		Savings: analytics.SavingsRule{Mode: p.SavingsMode, RunningCostsMonthly: p.RunningCostsMonthly, Rate: p.SavingsRate, RSUFull: p.RSUCountFull},
	})
}

// hasIncome reports whether the household described any income stream.
func (in incomeInputs) hasIncome() bool { return len(in.streams) > 0 }

// fireExpensesToday is the yearly spending the FIRE Calculator starts from,
// in today's money: running costs, or what isn't saved of salary and bonus
// over the next 12 months.
func (in incomeInputs) fireExpensesToday(s analytics.Schedule) float64 {
	if in.profile.SavingsMode != "rate" {
		return in.profile.RunningCostsMonthly * 12
	}
	var v float64
	for _, m := range s.Months[:min(12, len(s.Months))] {
		v += m.BaseNet + m.BonusNet + m.OneOffNet - m.CashSavings
	}
	return v
}

func (h *PortfolioHandler) HandleIncome(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	now := time.Now()
	in, err := h.loadIncomeInputs(ctx, now)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s := in.schedule(now)

	type yearOut struct {
		analytics.ScheduleYear
		TariffEnacted   bool    `json:"tariff_enacted"`
		CeilingsEnacted bool    `json:"ceilings_enacted"`
		RVCap           float64 `json:"rv_cap"`
		KVCap           float64 `json:"kv_cap"`
		TariffIndex     float64 `json:"tariff_index"`
	}
	years := make([]yearOut, 0, len(s.Years))
	for _, y := range s.Years {
		law := y.Payroll.Law
		years = append(years, yearOut{ScheduleYear: y, TariffEnacted: law.TariffEnacted, CeilingsEnacted: law.CeilingsEnacted, RVCap: law.RVCap, KVCap: law.KVCap, TariffIndex: law.TariffIndex})
	}

	next := s.Months[:min(12, len(s.Months))]
	var gross12, lumps12, savings12 float64
	cash := make([]float64, 0, len(next))
	for _, m := range next {
		gross12 += m.Gross()
		lumps12 += m.BonusNet + m.OneOffNet + m.HeldNet + m.RefreshNet
		savings12 += m.Savings
		cash = append(cash, m.CashSavings)
	}
	sort.Float64s(cash)
	median := 0.0
	if n := len(cash); n > 0 {
		median = (cash[(n-1)/2] + cash[n/2]) / 2
	}
	marginal := 0.0
	for _, y := range s.Years {
		if y.Months == 12 || y.Year == now.Year()+1 {
			marginal = y.MarginalTax
			break
		}
	}

	streams, err := h.listStreamsJSON(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list income streams: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"has_income": in.hasIncome(),
		"profile":    in.profile,
		"streams":    streams,
		"held":       in.summary,
		"usd_per_eur": func() float64 {
			if v := in.fx["USD"]; v > 0 {
				return v
			}
			return 0
		}(),
		"years":  years,
		"months": s.Months,
		"summary": map[string]any{
			"gross_next_12":             round2(gross12),
			"savings_next_12":           round2(savings12),
			"bonus_and_vests_next_12":   round2(lumps12),
			"cash_savings_normal_month": round2(median),
			"marginal_tax":              marginal,
			"fire_expenses_today":       round2(in.fireExpensesToday(s)),
		},
	})
}

// projectionPlan picks what a projection pays in: the income schedule when
// streams exist (unless the caller asks for the trailing or a custom amount),
// a custom flat amount, or the trailing 12-month contribution.
func (h *PortfolioHandler) projectionPlan(ctx context.Context, now time.Time, source string, custom bool, flat, trailing, growth float64) (contributionPlan, string, *incomeInputs) {
	var inp *incomeInputs
	if in, err := h.loadIncomeInputs(ctx, now); err == nil {
		inp = &in
	}
	has := inp != nil && inp.hasIncome()
	switch {
	case has && (source == "income" || (source == "" && !custom)):
		return contributionPlan{Schedule: inp.schedule(now).Contributions()}, "income", inp
	case custom && source != "trailing":
		return contributionPlan{Monthly: flat, Growth: growth}, "custom", inp
	default:
		return contributionPlan{Monthly: trailing, Growth: growth}, "trailing", inp
	}
}

// postFireHealth estimates the yearly health and care insurance after FIRE in
// today's money. Voluntary statutory insurance is charged on the gain part of
// the withdrawals, so a first run without it finds that share.
func (in incomeInputs) postFireHealth(fp fireParams, now time.Time) float64 {
	first := simulateFIRE(fp, now)
	capital := fp.Expenses * 0.5
	if first.Reached && first.FirstYearGross > 0 {
		capital = first.FirstYearGross * first.GainShare
	}
	law := analytics.PayrollLawFor(now.Year(), analytics.LawAssumptions{Inflation: in.profile.Inflation, WageGrowth: in.profile.WageGrowth})
	return analytics.PostFireHealth(capital, law, in.profile.payroll())
}
