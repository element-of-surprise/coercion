package sqlite

import (
	"fmt"
	"testing"

	"github.com/gostdlib/base/retry/exponential"
	"zombiezen.com/go/sqlite"

	"github.com/element-of-surprise/coercion/workflow/errors"
)

func TestRetryLocked(t *testing.T) {
	t.Parallel()

	boff := exponential.Must(exponential.New(exponential.WithTesting()))
	locked := errors.E(t.Context(), errors.CatInternal, errors.TypeStorageUpdate, fmt.Errorf("write: %w", sqlite.ResultLockedSharedCache.ToError()))
	busy := fmt.Errorf("commit: %w", sqlite.ResultBusy.ToError())
	other := errors.E(t.Context(), errors.CatInternal, errors.TypeStorageUpdate, fmt.Errorf("write: %w", sqlite.ResultConstraintPrimaryKey.ToError()))

	tests := []struct {
		name string
		// errs are the errors the op returns on each attempt. Attempts past the end succeed.
		errs      []error
		wantCalls int
		wantErr   bool
	}{
		{
			name:      "Success: a write that succeeds is run once.",
			wantCalls: 1,
		},
		{
			name:      "Success: a write that hits lock conflicts is retried until it succeeds.",
			errs:      []error{locked, busy},
			wantCalls: 3,
		},
		{
			name:      "Error: a write that fails for another reason is not retried.",
			errs:      []error{other},
			wantCalls: 1,
			wantErr:   true,
		},
		{
			name:      "Error: a write that keeps hitting lock conflicts stops after lockAttempts.",
			errs:      []error{locked, locked, locked, locked, locked, locked, locked},
			wantCalls: lockAttempts,
			wantErr:   true,
		},
	}

	for _, test := range tests {
		calls := 0
		err := retryLocked(t.Context(), boff, func() error {
			calls++
			if calls <= len(test.errs) {
				return test.errs[calls-1]
			}
			return nil
		})
		if calls != test.wantCalls {
			t.Errorf("TestRetryLocked(%s): got %d calls, want %d", test.name, calls, test.wantCalls)
		}
		switch {
		case err == nil && test.wantErr:
			t.Errorf("TestRetryLocked(%s): got err == nil, want err != nil", test.name)
		case err != nil && !test.wantErr:
			t.Errorf("TestRetryLocked(%s): got err == %s, want err == nil", test.name, err)
		}
	}
}
