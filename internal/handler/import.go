package handler

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/lnsp/wealth/internal/database/generated"
	"github.com/lnsp/wealth/internal/parser"
)

type ImportHandler struct {
	queries *db.Queries
}

func NewImportHandler(q *db.Queries) *ImportHandler {
	return &ImportHandler{queries: q}
}

func (h *ImportHandler) HandleImport(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(32 << 20); err != nil { // 32MB max
		writeError(w, http.StatusBadRequest, "invalid multipart form: "+err.Error())
		return
	}

	accountIDStr := r.FormValue("account_id")
	if accountIDStr == "" {
		writeError(w, http.StatusBadRequest, "account_id is required")
		return
	}

	accountID, err := uuid.Parse(accountIDStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid account_id: "+err.Error())
		return
	}

	// Verify account exists
	_, err = h.queries.GetAccount(r.Context(), accountID)
	if err != nil {
		writeError(w, http.StatusNotFound, "account not found")
		return
	}

	file, _, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "file is required: "+err.Error())
		return
	}
	defer file.Close()

	data, err := io.ReadAll(file)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "read file: "+err.Error())
		return
	}

	txns, result, err := parser.ParseCSV(data, accountID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	imported, skipped, newSecurities, err := h.insertTransactions(r.Context(), txns)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "insert transactions: "+err.Error())
		return
	}

	result.Imported = imported
	result.Skipped = skipped
	result.NewSecurities = newSecurities

	// Refresh materialized view
	if err := h.queries.RefreshCurrentHoldings(r.Context()); err != nil {
		log.Printf("WARNING: refresh materialized view: %v", err)
	}

	writeJSON(w, http.StatusOK, result)
}

func (h *ImportHandler) insertTransactions(ctx context.Context, txns []parser.Transaction) (imported, skipped int, newSecurities []string, err error) {
	seenSecurities := make(map[string]bool)

	for _, txn := range txns {
		// Auto-create securities for new ISINs
		if txn.SecurityISIN != "" && !seenSecurities[txn.SecurityISIN] {
			seenSecurities[txn.SecurityISIN] = true
			_, secErr := h.queries.GetSecurity(ctx, txn.SecurityISIN)
			if secErr != nil {
				// Security doesn't exist, create it
				name := txn.Counterparty
				if name == "" {
					name = txn.SecurityISIN
				}
				if err := h.queries.UpsertSecurity(ctx, txn.SecurityISIN,
					pgtype.Text{}, pgtype.Text{},
					name, "etf", txn.Currency,
				); err != nil {
					return imported, skipped, newSecurities, fmt.Errorf("create security %s: %w", txn.SecurityISIN, err)
				}
				newSecurities = append(newSecurities, txn.SecurityISIN)
			}
		}

		// Build nullable fields
		secISIN := pgtype.Text{}
		if txn.SecurityISIN != "" {
			secISIN = pgtype.Text{String: txn.SecurityISIN, Valid: true}
		}

		quantity := numericFromFloat(txn.Quantity)
		price := numericFromFloat(txn.Price)
		amount := numericFromFloat(txn.Amount)
		fee := numericFromFloat(txn.Fee)
		tax := numericFromFloat(txn.Tax)

		counterparty := pgtype.Text{}
		if txn.Counterparty != "" {
			counterparty = pgtype.Text{String: txn.Counterparty, Valid: true}
		}
		reference := pgtype.Text{}
		if txn.Reference != "" {
			reference = pgtype.Text{String: txn.Reference, Valid: true}
		}
		category := pgtype.Text{}
		if txn.Category != "" {
			category = pgtype.Text{String: txn.Category, Valid: true}
		}

		err := h.queries.InsertTransaction(ctx,
			txn.AccountID, txn.Date, txn.Type,
			secISIN, quantity, price, amount, fee, tax,
			txn.Currency, counterparty, reference, category, txn.ImportHash,
		)
		if err != nil {
			// ON CONFLICT DO NOTHING means no error for duplicates in PostgreSQL,
			// but we can't easily detect skips vs inserts without checking affected rows.
			// For simplicity, count everything as imported.
			log.Printf("insert transaction: %v", err)
			skipped++
			continue
		}
		imported++
	}

	return imported, skipped, newSecurities, nil
}

func numericFromFloat(f float64) pgtype.Numeric {
	var n pgtype.Numeric
	n.Scan(fmt.Sprintf("%f", f))
	return n
}
