package errors

import (
	"fmt"
	"testing"
)

func TestErrStorageInconsistent(t *testing.T) {
	t.Parallel()

	err := ErrStorageInconsistent(t.Context(), fmt.Errorf("stored plan names Block(x), which has no row"))
	// internal/execute's recover.fetchPlans and Plans.resumeOrExit/Plans.stranded skip a Plan on IsStorageInconsistent.
	if !IsStorageInconsistent(err) {
		t.Errorf("TestErrStorageInconsistent: got IsStorageInconsistent(err) == false, want true")
	}
	// Workstream.Wait (coercion.go) promises callers that an error retrying cannot fix wraps ErrPermanent, and
	// exponential backoff stops retrying on it.
	if !Is(err, ErrPermanent) {
		t.Errorf("TestErrStorageInconsistent: got err not wrapping ErrPermanent, want it wrapped")
	}
}
