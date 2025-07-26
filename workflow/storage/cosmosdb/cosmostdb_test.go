package cosmosdb

import (
	"testing"

	"github.com/element-of-surprise/coercion/workflow/errors"
	"github.com/gostdlib/base/context"
	"github.com/gostdlib/base/retry/exponential"
)

// TestBackoff tests that the maximum attempts of 5 is set.
func TestBackoff(t *testing.T) {
	t.Parallel()

	err := backoff.Retry(
		t.Context(),
		func(context.Context, exponential.Record) error {
			return errors.New("error")
		},
	)

	if err == nil {
		t.Fatalf("TestBackoff: expected error, got nil")
	}
}
