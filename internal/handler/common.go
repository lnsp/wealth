package handler

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"math"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	db "github.com/lnsp/wealth/internal/database/generated"
)

// fmtEUR formats a number with German thousands separator (e.g. 62348 -> "62.348").
// Safely handles NaN, Inf, and large magnitudes without int64 overflow or recursion.
func fmtEUR(v float64) string {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return "0"
	}
	rounded := math.Round(v)
	neg := rounded < 0
	absVal := math.Abs(rounded)

	var s string
	if absVal >= 1e15 {
		s = strconv.FormatFloat(absVal, 'f', 0, 64)
	} else {
		s = strconv.FormatInt(int64(absVal), 10)
	}
	if s == "0" {
		return "0"
	}
	if len(s) > 3 {
		result := make([]byte, 0, len(s)+len(s)/3)
		for i, c := range s {
			if i > 0 && (len(s)-i)%3 == 0 {
				result = append(result, '.')
			}
			result = append(result, byte(c))
		}
		s = string(result)
	}
	if neg {
		return "-" + s
	}
	return s
}

// parseFiniteFloat parses s into a finite float64, rejecting NaN and ±Inf.
func parseFiniteFloat(s string) (float64, error) {
	v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return 0, err
	}
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, errors.New("non-finite float")
	}
	return v, nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	buf, err := json.Marshal(v)
	if err != nil {
		log.Printf("ERROR [500]: marshal JSON response: %v", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"internal server error"}` + "\n"))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(buf)
	_, _ = w.Write([]byte("\n"))
}

func writeError(w http.ResponseWriter, status int, message string) {
	// Log full error server-side, return sanitized message to client
	if status >= 500 {
		log.Printf("ERROR [%d]: %s", status, message)
		writeJSON(w, status, map[string]string{"error": "internal server error"})
		return
	}
	// For 4xx errors, strip internal Go error details after the first colon
	if status >= 400 {
		log.Printf("WARN [%d]: %s", status, message)
		if idx := strings.Index(message, ": "); idx > 0 {
			message = message[:idx]
		}
	}
	writeJSON(w, status, map[string]string{"error": message})
}

type txBeginner interface {
	Begin(ctx context.Context) (pgx.Tx, error)
}

// withTx executes fn inside a database transaction when q.DB() supports Begin.
func withTx(ctx context.Context, q *db.Queries, fn func(qtx *db.Queries) error) error {
	if q == nil {
		return fn(nil)
	}
	b, ok := q.DB().(txBeginner)
	if !ok {
		return fn(q)
	}
	tx, err := b.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := fn(q.WithTx(tx)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// userAccountIDs returns nil under single-user / single-household scope so all
// authenticated logins access the shared household portfolio without per-user filtering.
func userAccountIDs(_ context.Context, _ db.DBTX) []uuid.UUID {
	return nil
}

// isUserAccount returns true under single-user / single-household scope;
// request authentication is enforced upstream by AuthMiddlewareWithQueries.
func isUserAccount(_ context.Context, _ db.DBTX, _ uuid.UUID) bool {
	return true
}
