package repository

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// The dedicated session serializes both automatic policies, including queries
// and state writes; a lost or unavailable lock never permits consumption.
func (r *idempotencyRepository) TryLockOpenAIAutoResetCredit(ctx context.Context, accountID int64) (func(), bool, error) {
	db, ok := r.sql.(interface {
		Conn(context.Context) (*sql.Conn, error)
	})
	if !ok {
		return nil, false, fmt.Errorf("automatic reset lock requires a dedicated database connection")
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, false, err
	}
	key := advisoryLockHash(fmt.Sprintf("openai_auto_reset_credit:%d", accountID))
	var acquired bool
	if err := conn.QueryRowContext(ctx, "SELECT pg_try_advisory_lock($1)", key).Scan(&acquired); err != nil {
		discardSQLConnection(conn)
		return nil, false, err
	}
	if !acquired {
		_ = conn.Close()
		return nil, false, nil
	}
	return func() {
		unlockCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var unlocked bool
		if err := conn.QueryRowContext(unlockCtx, "SELECT pg_advisory_unlock($1)", key).Scan(&unlocked); err != nil || !unlocked {
			discardSQLConnection(conn)
			return
		}
		_ = conn.Close()
	}, true, nil
}
