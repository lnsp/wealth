package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/http"
	"strings"

	"github.com/google/uuid"
	db "github.com/lnsp/wealth/internal/database/generated"
)

// fmtEUR formats a number with German thousands separator (e.g. 62348 -> "62.348")
func fmtEUR(v float64) string {
	n := int64(math.Round(v))
	if n < 0 {
		return "-" + fmtEUR(-v)
	}
	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 {
		return s
	}
	// Insert dots every 3 digits from the right
	result := make([]byte, 0, len(s)+len(s)/3)
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			result = append(result, '.')
		}
		result = append(result, byte(c))
	}
	return string(result)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
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

// userAccountIDs returns account IDs belonging to the current user.
// Returns nil if no user context (auth disabled / dev mode) — caller should skip filtering.
func userAccountIDs(ctx context.Context, pool db.DBTX) []uuid.UUID {
	userID := UserIDFromContext(ctx)
	if userID == "" {
		return nil // no auth — don't filter
	}
	uid, err := uuid.Parse(userID)
	if err != nil {
		return nil
	}
	rows, err := pool.Query(ctx, "SELECT id FROM accounts WHERE user_id = $1", uid)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err == nil {
			ids = append(ids, id)
		}
	}
	return ids
}

// isUserAccount checks if the given account ID belongs to the current user.
// Returns true if no auth context (dev mode) or if the account belongs to the user.
func isUserAccount(ctx context.Context, pool db.DBTX, accountID uuid.UUID) bool {
	ids := userAccountIDs(ctx, pool)
	if ids == nil {
		return true // no auth — allow all
	}
	for _, id := range ids {
		if id == accountID {
			return true
		}
	}
	return false
}
