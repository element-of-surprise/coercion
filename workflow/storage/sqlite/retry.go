package sqlite

import (
	"github.com/gostdlib/base/context"
	"github.com/gostdlib/base/retry/exponential"
	"zombiezen.com/go/sqlite"
)

// lockAttempts is how many times a write is tried before a lock conflict is returned to the caller.
const lockAttempts = 5

// lockBackoff paces the retries of a write that failed on a lock conflict: 100ms, 200ms, 400ms, 800ms.
var lockBackoff = exponential.Must(exponential.New(exponential.WithPolicy(exponential.FastRetryPolicy())))

// retryLocked runs op, and runs it again while it fails because another connection holds a lock that SQLite would
// not wait for. op must do the whole write, taking the Vault's lock and a connection itself, so a failed attempt
// releases both and whatever blocked it can finish. Any other error is returned at once, and the error from the last
// attempt is returned unchanged.
func retryLocked(ctx context.Context, boff *exponential.Backoff, op func() error) error {
	var opErr error
	retryErr := boff.Retry(
		ctx,
		func(context.Context, exponential.Record) error {
			opErr = op()
			if opErr == nil || isLockConflict(opErr) {
				return opErr
			}
			return exponential.ErrPermanent
		},
		exponential.WithMaxAttempts(lockAttempts),
	)
	if retryErr == nil {
		return nil
	}
	if opErr == nil {
		// The context ended before the first attempt.
		return retryErr
	}
	return opErr
}

// isLockConflict reports whether err is SQLite failing a statement because another connection holds a lock: BUSY
// when SQLite gives up waiting to break a lock cycle, LOCKED when a shared-cache table lock would deadlock.
func isLockConflict(err error) bool {
	switch sqlite.ErrCode(err).ToPrimary() {
	case sqlite.ResultBusy, sqlite.ResultLocked:
		return true
	}
	return false
}
