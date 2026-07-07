package analytics

import (
	"math"
	"sort"
	"time"

	"github.com/google/uuid"
)

// InKindTransfer is the minimal shape the pair-matcher needs from a
// transaction. Keep this decoupled from the generated DB type so the
// analytics package stays import-free.
type InKindTransfer struct {
	ID       uuid.UUID
	Date     time.Time
	Type     string  // "transfer" (in-kind grant or in-kind receive) or "transfer_out"
	ISIN     string  // empty if it's a cash transfer (no ISIN)
	Quantity float64 // share count for in-kind moves
}

// MatchInKindTransferPairs identifies broker-to-broker in-kind moves and
// returns the set of transaction IDs to exclude from attribution. The match
// rule is conservative: an outgoing `transfer_out` is paired with an
// incoming `transfer` only if they share the same ISIN, the same quantity
// (within 0.0001 shares), and settle within ±5 calendar days. Each row
// participates in at most one pair; unmatched rows are left to the classifier
// (so a true RSU vest with no offsetting transfer_out remains a
// Contribution).
//
// Partial-quantity matches (e.g. transfer 100 shares, transfer_out 50) are
// intentionally NOT supported — partial moves are rare, and silently
// matching the wrong half would corrupt attribution. Add proper qty-split
// logic if a real-world fixture demands it.
func MatchInKindTransferPairs(txns []InKindTransfer) map[uuid.UUID]struct{} {
	const qtyTol = 1e-4
	const dateWindow = 5 * 24 * time.Hour

	excluded := make(map[uuid.UUID]struct{})

	// Bucket by ISIN; only in-kind rows participate.
	byISIN := make(map[string][]InKindTransfer)
	for _, t := range txns {
		if t.ISIN == "" {
			continue
		}
		if t.Type != "transfer" && t.Type != "transfer_out" {
			continue
		}
		byISIN[t.ISIN] = append(byISIN[t.ISIN], t)
	}

	for _, group := range byISIN {
		// Stable order: oldest first, so the greedy match is deterministic.
		sort.SliceStable(group, func(i, j int) bool {
			return group[i].Date.Before(group[j].Date)
		})

		used := make(map[uuid.UUID]bool)
		for i, out := range group {
			if out.Type != "transfer_out" || used[out.ID] {
				continue
			}
			// Find the closest-in-time `transfer` of the same qty within the
			// window. Scan both directions because brokers can credit the
			// receiving account before or after the sending one.
			bestIdx := -1
			bestGap := time.Duration(math.MaxInt64)
			for j, in := range group {
				if i == j || used[in.ID] || in.Type != "transfer" {
					continue
				}
				if math.Abs(in.Quantity-out.Quantity) > qtyTol {
					continue
				}
				gap := in.Date.Sub(out.Date)
				if gap < 0 {
					gap = -gap
				}
				if gap > dateWindow {
					continue
				}
				if gap < bestGap {
					bestGap = gap
					bestIdx = j
				}
			}
			if bestIdx >= 0 {
				used[out.ID] = true
				used[group[bestIdx].ID] = true
				excluded[out.ID] = struct{}{}
				excluded[group[bestIdx].ID] = struct{}{}
			}
		}
	}

	return excluded
}
