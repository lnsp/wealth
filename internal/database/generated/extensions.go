package generated

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

// DB returns the underlying database connection for raw queries.
func (q *Queries) DB() DBTX {
	return q.db
}

// AssignOrphanedDataToUser assigns rows with NULL user_id to the given user
// across multiple tables. This uses dynamic SQL and cannot be expressed as a
// single sqlc query.
func (q *Queries) AssignOrphanedDataToUser(ctx context.Context, userID uuid.UUID) error {
	tables := []string{"accounts", "financial_goals", "price_alerts", "wealth_reports"}
	for _, t := range tables {
		if _, err := q.db.Exec(ctx, fmt.Sprintf(`UPDATE %s SET user_id = $1 WHERE user_id IS NULL`, t), userID); err != nil {
			return err
		}
	}
	return nil
}
