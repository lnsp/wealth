package handler

import (
	"net/http"

	db "github.com/lnsp/wealth/internal/database/generated"
	"github.com/lnsp/wealth/internal/analytics"
)

type AnalysisHandler struct {
	queries *db.Queries
}

func NewAnalysisHandler(q *db.Queries) *AnalysisHandler {
	return &AnalysisHandler{queries: q}
}

func (h *AnalysisHandler) HandleSectors(w http.ResponseWriter, r *http.Request) {
	holdings, err := h.queries.ListCurrentHoldings(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list holdings: "+err.Error())
		return
	}

	// Get sector weights for each security
	exposure := make(map[string]float64)
	for _, holding := range holdings {
		sec, err := h.queries.GetSecurity(r.Context(), holding.SecurityISIN)
		if err != nil {
			continue
		}
		qtyFloat := 0.0
		if holding.Quantity.Valid {
			f, _ := holding.Quantity.Float64Value()
			qtyFloat = f.Float64
		}
		price, err := h.queries.GetLatestPrice(r.Context(), holding.SecurityISIN)
		if err != nil {
			continue
		}
		priceFloat := 0.0
		if price.Close.Valid {
			f, _ := price.Close.Float64Value()
			priceFloat = f.Float64
		}
		marketValue := qtyFloat * priceFloat

		sectorWeights := analytics.ParseWeights(sec.SectorWeights)
		for sector, pct := range sectorWeights {
			exposure[sector] += marketValue * pct / 100
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{"sectors": exposure})
}

func (h *AnalysisHandler) HandleCountries(w http.ResponseWriter, r *http.Request) {
	holdings, err := h.queries.ListCurrentHoldings(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list holdings: "+err.Error())
		return
	}

	exposure := make(map[string]float64)
	for _, holding := range holdings {
		sec, err := h.queries.GetSecurity(r.Context(), holding.SecurityISIN)
		if err != nil {
			continue
		}
		qtyFloat := 0.0
		if holding.Quantity.Valid {
			f, _ := holding.Quantity.Float64Value()
			qtyFloat = f.Float64
		}
		price, err := h.queries.GetLatestPrice(r.Context(), holding.SecurityISIN)
		if err != nil {
			continue
		}
		priceFloat := 0.0
		if price.Close.Valid {
			f, _ := price.Close.Float64Value()
			priceFloat = f.Float64
		}
		marketValue := qtyFloat * priceFloat

		countryWeights := analytics.ParseWeights(sec.CountryWeights)
		for country, pct := range countryWeights {
			exposure[country] += marketValue * pct / 100
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{"countries": exposure})
}

func (h *AnalysisHandler) HandleOverlap(w http.ResponseWriter, r *http.Request) {
	holdings, err := h.queries.ListCurrentHoldings(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list holdings: "+err.Error())
		return
	}

	type etfData struct {
		ISIN     string             `json:"isin"`
		Name     string             `json:"name"`
		Holdings map[string]float64 `json:"-"`
	}

	var etfs []etfData
	for _, holding := range holdings {
		sec, err := h.queries.GetSecurity(r.Context(), holding.SecurityISIN)
		if err != nil || sec.AssetClass != "etf" {
			continue
		}
		etfHoldings, err := h.queries.ListETFHoldings(r.Context(), holding.SecurityISIN)
		if err != nil || len(etfHoldings) == 0 {
			continue
		}
		weights := make(map[string]float64)
		for _, eh := range etfHoldings {
			if eh.WeightPct.Valid {
				f, _ := eh.WeightPct.Float64Value()
				weights[eh.HoldingISIN] = f.Float64
			}
		}
		etfs = append(etfs, etfData{ISIN: sec.ISIN, Name: sec.Name, Holdings: weights})
	}

	// Build overlap matrix
	n := len(etfs)
	matrix := make([][]float64, n)
	labels := make([]string, n)
	for i := range etfs {
		labels[i] = etfs[i].Name
		matrix[i] = make([]float64, n)
		matrix[i][i] = 100.0
		for j := i + 1; j < n; j++ {
			o := analytics.ComputeOverlap(etfs[i].Holdings, etfs[j].Holdings)
			matrix[i][j] = o
			matrix[j][i] = o
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"labels": labels,
		"matrix": matrix,
	})
}
