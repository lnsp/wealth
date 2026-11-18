package handler

import (
	"context"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/lnsp/wealth/internal/assets"
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
	r.Body = http.MaxBytesReader(w, r.Body, 10<<20) // 10MB max multipart request body
	if err := r.ParseMultipartForm(10 << 20); err != nil {
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
	const maxCSVBytes = 5 << 20
	if fileHeader != nil && fileHeader.Size > maxCSVBytes {
		writeError(w, http.StatusBadRequest, "file too large (max 5MB)")
		return
	}

	data, err := io.ReadAll(io.LimitReader(file, maxCSVBytes+1))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "read file: "+err.Error())
		return
	}
	if len(data) > maxCSVBytes {
		writeError(w, http.StatusBadRequest, "file too large (max 5MB)")
		return
	}

	txns, vests, result, err := parser.ParseCSV(data, accountID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Validate CSV account type matches target account (securities-based CSVs
	// with AccountType == "brokerage", e.g. Delta/PP/TR/IBKR, may also target
	// crypto, pension, or precious_metals accounts).
	if result.AccountType != "" && !compatibleImportAccountType(result.AccountType, account.Type) {
		writeError(w, http.StatusBadRequest,
			fmt.Sprintf("CSV contains %s transactions but target account %q is type %q. Select the correct account.",
				result.AccountType, account.Name, account.Type))
		return
	}

	// Morgan Stanley exports never name the stock: vests and sales only get
	// a security from the account's Stock ISIN. Without it the vests would be
	// stored as ISIN-less transfers, which count as cash withdrawals.
	if result.Institution == "morgan_stanley" && strings.TrimSpace(account.ImportSecurityISIN.String) == "" {
		writeError(w, http.StatusBadRequest,
			fmt.Sprintf("Account %q has no Stock ISIN. Morgan Stanley files don't name the stock, so set the account's Stock ISIN in Settings > Accounts before importing.", account.Name))
		return
	}

	// Resolve ISIN for Morgan Stanley transactions from account's import_security_isin
	if account.ImportSecurityISIN.Valid && strings.TrimSpace(account.ImportSecurityISIN.String) != "" {
		isin := strings.TrimSpace(account.ImportSecurityISIN.String)
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

	// Amounts are stored as magnitudes with the direction in the type. This
	// runs after the parser computed the import hashes, so they don't change.
	for i := range txns {
		normalizeTxnSigns(&txns[i])
	}

	var imported, skipped, updated, adopted, vestCount int
	var newSecurities []string

	if err := withTx(r.Context(), h.queries, func(qtx *db.Queries) error {
		var txErr error
		imported, skipped, updated, adopted, newSecurities, txErr = h.insertTransactions(r.Context(), qtx, result.Institution, txns)
		if txErr != nil {
			return txErr
		}
		if len(vests) > 0 {
			vestCount, txErr = h.insertRSUVests(r.Context(), qtx, vests, account.ImportSecurityISIN)
			if txErr != nil {
				return txErr
			}
		}
		secNames := newSecurities
		if secNames == nil {
			secNames = []string{}
		}
		if logErr := qtx.InsertImportHistory(r.Context(), db.InsertImportHistoryParams{
			AccountID: accountID, Institution: result.Institution, Filename: filename,
			TotalRows: int32(len(txns)), Imported: int32(imported), Skipped: int32(skipped),
			NewSecurities: secNames, Updated: int32(updated), RsuVests: int32(vestCount),
		}); logErr != nil {
			return fmt.Errorf("insert import history: %w", logErr)
		}
		return nil
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "insert transactions: "+err.Error())
		return
	}

	result.Imported = imported
	result.Skipped = skipped
	result.Updated = updated
	result.NewSecurities = newSecurities
	result.RSUVests = vestCount
	if adopted > 0 {
		result.Warnings = append(result.Warnings,
			fmt.Sprintf("%d transactions matched existing rows imported by an older version (same date, type, quantity and amount) and were deduplicated instead of inserted.", adopted))
	}

	// Refresh materialized view (CONCURRENTLY must run outside transaction)
	if err := h.queries.RefreshCurrentHoldings(r.Context()); err != nil {
		log.Printf("WARNING: refresh materialized view: %v", err)
	}

	// Newly imported bank rows may be loan instalments.
	if n, err := assets.MatchAllLoanPayments(r.Context(), h.queries); err != nil {
		log.Printf("WARNING: match loan payments: %v", err)
	} else if n > 0 {
		log.Printf("import: matched %d loan payments", n)
	}

	// Backfill historical net worth snapshots and create today's snapshot sequentially
	if h.snapshot != nil {
		go func() {
			h.snapshot.BackfillNetWorthSnapshots()
			h.snapshot.RunNetWorthSnapshot()
		}()
	}

	writeJSON(w, http.StatusOK, result)
}

func (h *ImportHandler) insertTransactions(ctx context.Context, q *db.Queries, institution string, txns []parser.Transaction) (imported, skipped, updated, adopted int, newSecurities []string, err error) {
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
			_, secErr := q.GetSecurity(ctx, txn.SecurityISIN)
			if secErr != nil {
				// Security doesn't exist, create it
				name := txn.Counterparty
				if name == "" {
					name = txn.SecurityISIN
				}
				if err := q.UpsertSecurity(ctx, db.UpsertSecurityParams{
					ISIN: txn.SecurityISIN, WKN: pgtype.Text{}, Symbol: pgtype.Text{},
					Name: name, AssetClass: parser.ClassifyAsset(txn.SecurityISIN, name), Currency: txn.Currency,
				}); err != nil {
					return imported, skipped, updated, adopted, newSecurities, fmt.Errorf("create security %s: %w", txn.SecurityISIN, err)
				}
				newSecurities = append(newSecurities, txn.SecurityISIN)

				// Apply ticker mapping from seed file if available
				if symbol, ok := h.tickerMap[txn.SecurityISIN]; ok {
					sym := pgtype.Text{String: symbol, Valid: true}
					if err := q.UpdateSecuritySymbol(ctx, db.UpdateSecuritySymbolParams{ISIN: txn.SecurityISIN, Symbol: sym}); err != nil {
						return imported, skipped, updated, adopted, newSecurities, fmt.Errorf("update symbol %s: %w", txn.SecurityISIN, err)
					}
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
		if txn.SecurityISIN == "" {
			// Cash rows have no quantity or price: store NULL, not 0.
			quantity, price = nullIfZero(txn.Quantity), nullIfZero(txn.Price)
		}
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
		// still applies. Count the row as updated when the upsert changes it,
		// otherwise as a skipped duplicate.
		if existing, hashErr := q.GetTransactionImportState(ctx, txn.ImportHash); hashErr == nil {
			if err := q.InsertTransaction(ctx, insertParams); err != nil {
				return imported, skipped, updated, adopted, newSecurities, fmt.Errorf("upsert transaction: %w", err)
			}
			if upsertChanges(existing, insertParams) {
				updated++
			} else {
				skipped++
			}
			continue
		}

		// Hash unknown — the same transaction may still exist under a hash from
		// an older importer version. Adopt the first economically identical row
		// not accounted for by this batch, upgrading its hash to the current
		// scheme so future imports dedup directly.
		// Older parser versions may have stored the row under another type
		// (e.g. a Scalable tax refund as "fee"); those are tried after the
		// row's own type, and an adopted row is then reclassified.
		var adoptedID uuid.UUID
		found, retyped := false, false
		for i, typ := range append([]string{txn.Type}, legacyImportTypes(institution, txn.Type)...) {
			similar, simErr := q.ListSimilarTransactions(ctx, db.ListSimilarTransactionsParams{
				AccountID: txn.AccountID, Date: txn.Date, Type: typ,
				SecurityISIN: secISIN, Quantity: quantity, Amount: amount,
				Fee: fee, Tax: tax, Currency: txn.Currency,
			})
			if simErr != nil {
				return imported, skipped, updated, adopted, newSecurities, fmt.Errorf("list similar transactions: %w", simErr)
			}
			if adoptedID, found = pickAdoptable(similar, batchHashes, claimed); found {
				retyped = i > 0
				break
			}
		}
		if found {
			if updErr := q.UpdateTransactionImportHash(ctx, db.UpdateTransactionImportHashParams{
				ID: adoptedID, ImportHash: txn.ImportHash,
			}); updErr != nil {
				return imported, skipped, updated, adopted, newSecurities, fmt.Errorf("adopt legacy transaction %s: %w", adoptedID, updErr)
			}
			claimed[adoptedID] = true
			adopted++
			if retyped {
				// The row now carries this hash: the upsert reclassifies it.
				if err := q.InsertTransaction(ctx, insertParams); err != nil {
					return imported, skipped, updated, adopted, newSecurities, fmt.Errorf("reclassify adopted transaction: %w", err)
				}
				updated++
			} else {
				skipped++
			}
			continue
		}

		if err := q.InsertTransaction(ctx, insertParams); err != nil {
			return imported, skipped, updated, adopted, newSecurities, fmt.Errorf("insert transaction: %w", err)
		}
		imported++
	}

	return imported, skipped, updated, adopted, newSecurities, nil
}

// legacyImportTypes lists the types earlier parser versions stored for a row
// that is now imported as `typ`, so a row kept under an older import hash is
// still recognised (and reclassified) instead of duplicated. Only the
// sign-based reclassifications of the 2026-11-18 audit are listed; amounts
// were already stored as magnitudes for these parsers, so they still match.
func legacyImportTypes(institution, typ string) []string {
	if institution != "scalable_capital" {
		return nil
	}
	switch typ {
	case "tax":
		return []string{"fee"} // TAX rows were all booked as fees
	case "deposit":
		return []string{"fee", "interest"} // tax refunds; cancelled tax rows (fee reversed)
	case "withdrawal":
		return []string{"deposit"} // unknown cash sub-types defaulted to deposit
	case "fee":
		return []string{"interest", "dividend"} // negative interest or distributions
	}
	return nil
}

// upsertChanges reports whether InsertTransaction's ON CONFLICT clause would
// change the stored row: it overwrites the type, and the security, quantity
// and price when the incoming row carries them.
func upsertChanges(existing db.GetTransactionImportStateRow, in db.InsertTransactionParams) bool {
	if existing.Type != in.Type {
		return true
	}
	if in.SecurityISIN.Valid && (!existing.SecurityISIN.Valid || existing.SecurityISIN.String != in.SecurityISIN.String) {
		return true
	}
	differs := func(stored, incoming pgtype.Numeric) bool {
		v := numericToFloat(incoming)
		if !incoming.Valid || v <= 0 {
			return false // the upsert keeps the stored value
		}
		return !stored.Valid || math.Abs(numericToFloat(stored)-v) > 5e-9
	}
	return differs(existing.Quantity, in.Quantity) || differs(existing.Price, in.Price)
}

// normalizeTxnSigns enforces the stored-amount convention before insert:
// amount, fee and tax are magnitudes (≥ 0) and the type carries the
// direction, as txnSignedCashDelta and the SQL cash queries assume. Parsers
// that pass a signed amount through (e.g. a hand-edited wealth export) would
// otherwise turn a payment into an inflow. It runs after the parser computed
// the import hash, so hashes never change. Migration 032 applies the same
// rules to rows stored before this check existed.
//
//   - interest < 0 → fee (a charge such as overdraft interest)
//   - deposit < 0 → withdrawal; cash_transfer_in < 0 → cash_transfer_out
//   - dividend < 0 → fee. There is no dividend-reversal type, so a reversal
//     is booked as a charge: the cash effect is right, but tax figures keep
//     the original dividend instead of netting the reversal against it.
//   - every other type keeps its type with the magnitude (a negative
//     withdrawal is a withdrawal; buy, sell, fee, tax and transfers alike).
//   - fee and tax always reduce cash, so they are stored as magnitudes.
func normalizeTxnSigns(t *parser.Transaction) {
	if t.Amount < 0 {
		switch t.Type {
		case "interest", "dividend":
			t.Type = "fee"
		case "deposit":
			t.Type = "withdrawal"
		case "cash_transfer_in":
			t.Type = "cash_transfer_out"
		}
		t.Amount = -t.Amount
	}
	t.Fee = math.Abs(t.Fee)
	t.Tax = math.Abs(t.Tax)
}

// nullIfZero stores 0 as NULL: cash rows carry no quantity or price.
func nullIfZero(f float64) pgtype.Numeric {
	if f == 0 || math.IsNaN(f) || math.IsInf(f, 0) {
		return pgtype.Numeric{}
	}
	return numericFromFloat(f)
}

// pickAdoptable returns the first similar existing row that this batch does
// not otherwise account for.
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
	if math.IsNaN(f) || math.IsInf(f, 0) {
		f = 0
	}
	// Format with 8 fractional digits to match the DB columns' NUMERIC(18,8) scale.
	var n pgtype.Numeric
	n.Scan(fmt.Sprintf("%.8f", f))
	return n
}

// insertRSUVests persists RSU vest rows, linking vested rows to their sibling
// transaction via import_hash lookup. For unvested imports it first deletes
// stale unvested rows for the account so the schedule is always fresh.
func (h *ImportHandler) insertRSUVests(ctx context.Context, q *db.Queries, vests []parser.RSUVest, isin pgtype.Text) (int, error) {
	hasUnvested := false
	for _, v := range vests {
		if !v.Vested {
			hasUnvested = true
			break
		}
	}
	if hasUnvested && len(vests) > 0 {
		if err := q.DeleteUnvestedRSUVestsForAccount(ctx, vests[0].AccountID); err != nil {
			return 0, fmt.Errorf("delete stale unvested vests: %w", err)
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
			if id, err := q.GetTransactionIDByHash(ctx, v.LinkTransactionHash); err == nil {
				txnID = &id
			}
		}

		if err := q.InsertRSUVest(ctx, db.InsertRSUVestParams{
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
		}); err != nil {
			return inserted, fmt.Errorf("insert rsu vest: %w", err)
		}
		inserted++
	}
	return inserted, nil
}

// compatibleImportAccountType returns true when a parsed CSV's declared account
// type may be imported into targetType. Securities parsers (Delta, Portfolio
// Performance, Trade Republic, IBKR, etc.) tag exports as "brokerage", which
// are also valid on crypto, pension, and precious_metals accounts.
func compatibleImportAccountType(csvType, targetType string) bool {
	if csvType == "" || csvType == targetType {
		return true
	}
	if csvType == "brokerage" && isSecuritiesOnlyAccountType(targetType) {
		return true
	}
	return false
}

// HandleImportHistory lists past imports, newest first, limited to the
// caller's accounts (userAccountIDs; nil = household scope, every account),
// with the total so the client can page beyond the first page.
// Query: limit (default 50, max 500), offset.
func (h *ImportHandler) HandleImportHistory(w http.ResponseWriter, r *http.Request) {
	limit, offset := int32(50), int32(0)
	if v, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && v > 0 {
		limit = int32(min(v, 500))
	}
	if v, err := strconv.Atoi(r.URL.Query().Get("offset")); err == nil && v > 0 {
		offset = int32(v)
	}
	accountIDs := userAccountIDs(r.Context(), h.queries.DB())

	history, err := h.queries.ListImportHistory(r.Context(), db.ListImportHistoryParams{
		AccountIds: accountIDs, Limit: limit, Offset: offset,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list import history: "+err.Error())
		return
	}
	total, err := h.queries.CountImportHistory(r.Context(), accountIDs)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "count import history: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"history": history,
		"total":   total,
		"limit":   limit,
		"offset":  offset,
	})
}
