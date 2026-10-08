package storage

import (
	"fmt"
	"testing"

	"github.com/element-of-surprise/coercion/workflow/errors"
)

// TestErrNotFound checks that storage.ErrNotFound and the not-found errors Vaults build with errors.ErrNotFound are one
// kind of error, so callers can recognize a missing Plan from any Vault without matching messages.
func TestErrNotFound(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "Success: storage.ErrNotFound is a not-found error",
			err:  ErrNotFound,
			want: true,
		},
		{
			name: "Success: a not-found Error value compared with ErrNotFound does not panic",
			err:  errors.ErrNotFound(t.Context(), fmt.Errorf("plan(x)")),
			want: true,
		},
		{
			name: "Success: storage.ErrNotFound wrapped with more context is a not-found error",
			err:  fmt.Errorf("plan(x): %w", ErrNotFound),
			want: true,
		},
		{
			name: "Success: a Vault's not-found error matches storage.ErrNotFound",
			err:  errors.ErrNotFound(t.Context(), fmt.Errorf("plan(x) not found")),
			want: true,
		},
		{
			name: "Success: a storage read failure is not a not-found error",
			err:  errors.E(t.Context(), errors.CatInternal, errors.TypeStorageGet, fmt.Errorf("storage busy")),
			want: false,
		},
		{
			name: "Success: a nil error is not a not-found error",
			err:  nil,
			want: false,
		},
	}

	for _, test := range tests {
		// Regression: ErrNotFound was an Error, which holds a slice, so comparing it with == panicked at run time.
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("TestErrNotFound(%s): comparing with ErrNotFound panicked: %v", test.name, r)
				}
			}()
			_ = test.err == ErrNotFound
		}()
		if got := errors.IsNotFound(test.err); got != test.want {
			t.Errorf("TestErrNotFound(%s): got errors.IsNotFound(err) == %v, want %v", test.name, got, test.want)
		}
		if got := errors.Is(test.err, ErrNotFound); got != test.want {
			t.Errorf("TestErrNotFound(%s): got errors.Is(err, ErrNotFound) == %v, want %v", test.name, got, test.want)
		}
	}
}
