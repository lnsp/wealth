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

// SnapshotTrigger allows the import handler to trigger net worth snapshots.
type SnapshotTrigger interface {
	RunNetWorthSnapshot()
	BackfillNetWorthSnapshots()
}

type ImportHandler struct {
	queries   *db.Queries
	snapshot  SnapshotTrigger
	tickerMap map[string]string
}

func NewImportHandler(q *db.Queries, snapshot SnapshotTrigger, tickerMap map[string]string) *ImportHandler {
	return &ImportHandler{queries: q, snapshot: snapshot, tickerMap: tickerMap}
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

	// Verify account exists and belongs to the current user
	account, err := h.queries.GetAccount(r.Context(), accountID)
	if err != nil {
		writeError(w, http.StatusNotFound, "account not found")
		return
	}
	if !isUserAccount(r.Context(), h.queries.DB(), accountID) {
		writeError(w, http.StatusForbidden, "account does not belong to you")
		return
	}

	file, fileHeader, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "file is required: "+err.Error())
		return
	}
	defer file.Close()

	// Validate file size (max 5MB for CSV)
	if fileHeader != nil && fileHeader.Size > 5<<20 {
		writeError(w, http.StatusBadRequest, "file too large (max 5MB)")
		return
	}

	data, err := io.ReadAll(file)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "read file: "+err.Error())
		return
	}

	txns, vests, result, err := parser.ParseCSV(data, accountID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Validate CSV account type matches target account
	if result.AccountType != "" && result.AccountType != account.Type {
		writeError(w, http.StatusBadRequest,
			fmt.Sprintf("CSV contains %s transactions but target account %q is type %q. Select the correct account.",
				result.AccountType, account.Name, account.Type))
		return
	}

	// Resolve ISIN for Morgan Stanley transactions from account's import_security_isin
	if account.ImportSecurityISIN.Valid && account.ImportSecurityISIN.String != "" {
		isin := account.ImportSecurityISIN.String
		for i := range txns {
			if txns[i].SecurityISIN != "" {
				continue
			}
			// Apply to any row representing a share movement on this plan:
			// buy/sell (Withdrawals Report sales) and transfer (in-kind vest grants).
			// Auto-wire withdrawals carry no quantity and stay cash-only.
			if txns[i].Type == "buy" || txns[i].Type == "sell" || txns[i].Type == "transfer" {
				if txns[i].Quantity > 0 {
					txns[i].SecurityISIN = isin
				}
			}
		}
		for i := range vests {
			if vests[i].SecurityISIN == "" {
				vests[i].SecurityISIN = isin
			}
		}
	}

	// Warn (but don't block) if this file was already imported to a different account
	filename := ""
	if fileHeader != nil {
		filename = fileHeader.Filename
	}
	if filename != "" {
		if otherAccount, err := h.queries.CheckFileImportedToOtherAccount(r.Context(), db.CheckFileImportedToOtherAccountParams{AccountID: accountID, Filename: filename}); err == nil {
			result.Warnings = append(result.Warnings,
				fmt.Sprintf("This file was previously imported to account %q — verify you are not double-counting transactions.", otherAccount))
		}
	}

	imported, skipped, adopted, newSecurities, err := h.insertTransactions(r.Context(), txns)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "insert transactions: "+err.Error())
		return
	}
	result.Imported = imported
	result.Skipped = skipped
	result.NewSecurities = newSecurities
	if adopted > 0 {
		result.Warnings = append(result.Warnings,
			fmt.Sprintf("%d transactions matched existing rows imported by an older version (same date, type, quantity and amount) and were deduplicated instead of inserted.", adopted))
	}

	// Insert RSU vests (if any)
	if len(vests) > 0 {
		vestCount := h.insertRSUVests(r.Context(), vests, account.ImportSecurityISIN)
		result.RSUVests = vestCount
	}

	// Log import history
	secNames := newSecurities
	if secNames == nil {
		secNames = []string{}
	}
	if logErr := h.queries.InsertImportHistory(r.Context(), db.InsertImportHistoryParams{
		AccountID: accountID, Institution: result.Institution, Filename: filename,
		TotalRows: int32(len(txns)), Imported: int32(imported), Skipped: int32(skipped),
		NewSecurities: secNames,
	}); logErr != nil {
		log.Printf("WARNING: insert import history: %v", logErr)
	}

	// Refresh materialized view
	if err := h.queries.RefreshCurrentHoldings(r.Context()); err != nil {
		log.Printf("WARNING: refresh materialized view: %v", err)
	}

	// Backfill historical net worth snapshots and create today's snapshot
	if h.snapshot != nil {
		go h.snapshot.BackfillNetWorthSnapshots()
		h.snapshot.RunNetWorthSnapshot()
	}

	writeJSON(w, http.StatusOK, result)
}

func (h *ImportHandler) insertTransactions(ctx context.Context, txns []parser.Transaction) (imported, skipped, adopted int, newSecurities []string, err error) {
	seenSecurities := make(map[string]bool)

	// Hashes of the whole batch: an existing row whose hash appears here is
	// (or will be) accounted for by its own incoming row, so a different
	// incoming row must not adopt it as a legacy duplicate.
	batchHashes := make(map[string]bool, len(txns))
	for _, txn := range txns {
		batchHashes[txn.ImportHash] = true
	}
	claimed := make(map[uuid.UUID]bool)

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
				if err := h.queries.UpsertSecurity(ctx, db.UpsertSecurityParams{
					ISIN: txn.SecurityISIN, WKN: pgtype.Text{}, Symbol: pgtype.Text{},
					Name: name, AssetClass: parser.ClassifyAsset(txn.SecurityISIN, name), Currency: txn.Currency,
				}); err != nil {
					return imported, skipped, adopted, newSecurities, fmt.Errorf("create security %s: %w", txn.SecurityISIN, err)
				}
				newSecurities = append(newSecurities, txn.SecurityISIN)

				// Apply ticker mapping from seed file if available
				if symbol, ok := h.tickerMap[txn.SecurityISIN]; ok {
					sym := pgtype.Text{String: symbol, Valid: true}
					h.queries.UpdateSecuritySymbol(ctx, db.UpdateSecuritySymbolParams{ISIN: txn.SecurityISIN, Symbol: sym})
				}
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

		insertParams := db.InsertTransactionParams{
			AccountID: txn.AccountID, Date: txn.Date, Type: txn.Type,
			SecurityISIN: secISIN, Quantity: quantity, Price: price, Amount: amount,
			Fee: fee, Tax: tax, Currency: txn.Currency,
			Counterparty: counterparty, Reference: reference, Category: category,
			ImportHash: txn.ImportHash,
		}

		// Exact hash already present: re-run the upsert so type reclassification
		// still applies, but count the row as skipped.
		if _, hashErr := h.queries.GetTransactionIDByHash(ctx, txn.ImportHash); hashErr == nil {
			if err := h.queries.InsertTransaction(ctx, insertParams); err != nil {
				log.Printf("upsert transaction: %v", err)
			}
			skipped++
			continue
		}

		// Hash unknown — the same transaction may still exist under a hash from
		// an older importer version. Adopt the first economically identical row
		// not accounted for by this batch, upgrading its hash to the current
		// scheme so future imports dedup directly.
		similar, simErr := h.queries.ListSimilarTransactions(ctx, db.ListSimilarTransactionsParams{
			AccountID: txn.AccountID, Date: txn.Date, Type: txn.Type,
			SecurityISIN: secISIN, Quantity: quantity, Amount: amount,
			Fee: fee, Tax: tax, Currency: txn.Currency,
		})
		if simErr != nil {
			log.Printf("WARNING: list similar transactions: %v", simErr)
		}
		if id, ok := pickAdoptable(similar, batchHashes, claimed); ok {
			if updErr := h.queries.UpdateTransactionImportHash(ctx, db.UpdateTransactionImportHashParams{
				ID: id, ImportHash: txn.ImportHash,
			}); updErr != nil {
				log.Printf("WARNING: adopt legacy transaction %s: %v", id, updErr)
			} else {
				claimed[id] = true
				adopted++
				skipped++
				continue
			}
		}

		if err := h.queries.InsertTransaction(ctx, insertParams); err != nil {
			log.Printf("insert transaction: %v", err)
			skipped++
			continue
		}
		imported++
	}

	return imported, skipped, adopted, newSecurities, nil
}

// pickAdoptable returns the first similar existing row that this batch does
// not otherwise account for: rows whose import_hash matches any incoming row
// belong to that row (they will dedup by hash), and rows already claimed by an
// earlier incoming transaction of identical content stay claimed. This keeps
// the multiset semantics right when a file legitimately contains several
// identical transactions (e.g. two grants vesting the same quantity on the
// same day).
func pickAdoptable(candidates []db.ListSimilarTransactionsRow, batchHashes map[string]bool, claimed map[uuid.UUID]bool) (uuid.UUID, bool) {
	for _, c := range candidates {
		if batchHashes[c.ImportHash] || claimed[c.ID] {
			continue
		}
		return c.ID, true
	}
	return uuid.UUID{}, false
}

func numericFromFloat(f float64) pgtype.Numeric {
	// Format with 8 fractional digits to match the DB columns' NUMERIC(18,8)
	// scale. The previous "%f" default rounded to 6 places, which silently
	// dropped precision on crypto quantities (e.g. 0.67420698 → 0.674207).
	var n pgtype.Numeric
	n.Scan(fmt.Sprintf("%.8f", f))
	return n
}

// insertRSUVests persists RSU vest rows, linking vested rows to their sibling
// transaction via import_hash lookup. For unvested imports it first deletes
// stale unvested rows for the account so the schedule is always fresh.
func (h *ImportHandler) insertRSUVests(ctx context.Context, vests []parser.RSUVest, isin pgtype.Text) int {
	// If any unvested rows, clear stale ones first
	hasUnvested := false
	for _, v := range vests {
		if !v.Vested {
			hasUnvested = true
			break
		}
	}
	if hasUnvested && len(vests) > 0 {
		if err := h.queries.DeleteUnvestedRSUVestsForAccount(ctx, vests[0].AccountID); err != nil {
			log.Printf("WARNING: delete stale unvested vests: %v", err)
		}
	}

	inserted := 0
	for _, v := range vests {
		secISIN := pgtype.Text{}
		if v.SecurityISIN != "" {
			secISIN = pgtype.Text{String: v.SecurityISIN, Valid: true}
		} else if isin.Valid {
			secISIN = isin
		}

		grantNumber := pgtype.Text{}
		if v.GrantNumber != "" {
			grantNumber = pgtype.Text{String: v.GrantNumber, Valid: true}
		}

		// Resolve transaction_id from the linked import_hash
		var txnID *uuid.UUID
		if v.LinkTransactionHash != "" {
			if id, err := h.queries.GetTransactionIDByHash(ctx, v.LinkTransactionHash); err == nil {
				txnID = &id
			}
		}

		err := h.queries.InsertRSUVest(ctx, db.InsertRSUVestParams{
			AccountID:     v.AccountID,
			SecurityISIN:  secISIN,
			VestDate:      v.VestDate,
			GrantNumber:   grantNumber,
			GrossQuantity: numericFromFloat(v.GrossQuantity),
			NetQuantity:   numericFromFloat(v.NetQuantity),
			Price:         numericFromFloat(v.Price),
			Currency:      v.Currency,
			Vested:        v.Vested,
			TransactionID: txnID,
			ImportHash:    v.ImportHash,
		})
		if err != nil {
			log.Printf("WARNING: insert rsu vest: %v", err)
			continue
		}
		inserted++
	}
	return inserted
}
