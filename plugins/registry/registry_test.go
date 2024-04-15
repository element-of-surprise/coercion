package registry

import (
	"errors"
	"testing"
	"time"

	"github.com/gostdlib/ops/retry/exponential"
	"github.com/kylelemons/godebug/pretty"
)

// TODO(element-of-surprise): Remove this once expontential.Policy.Validate() is made public.
// This is a local copy.
func TestValidatePolicy(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		policy exponential.Policy
		want   error
	}{
		{
			name: "valid policy",
			policy: exponential.Policy{
				InitialInterval:     100 * time.Millisecond,
				Multiplier:          2.0,
				RandomizationFactor: 0.5,
				MaxInterval:         60 * time.Second,
			},
			want: nil,
		},
		{
			name: "Err: initial interval zero",
			policy: exponential.Policy{
				InitialInterval:     0,
				Multiplier:          2.0,
				RandomizationFactor: 0.5,
				MaxInterval:         60 * time.Second,
			},
			want: errors.New("Policy.InitialInterval must be greater than 0"),
		},
		{
			name: "Err: multiplier not greater than 1",
			policy: exponential.Policy{
				InitialInterval:     100 * time.Millisecond,
				Multiplier:          1.0,
				RandomizationFactor: 0.5,
				MaxInterval:         60 * time.Second,
			},
			want: errors.New("Policy.Multiplier must be greater than 1"),
		},
		{
			name: "Err: randomization factor out of range",
			policy: exponential.Policy{
				InitialInterval:     100 * time.Millisecond,
				Multiplier:          2.0,
				RandomizationFactor: 1.1,
				MaxInterval:         60 * time.Second,
			},
			want: errors.New("Policy.RandomizationFactor must be between 0 and 1"),
		},
		{
			name: "Err: max interval zero",
			policy: exponential.Policy{
				InitialInterval:     100 * time.Millisecond,
				Multiplier:          2.0,
				RandomizationFactor: 0.5,
				MaxInterval:         0,
			},
			want: errors.New("Policy.MaxInterval must be greater than 0"),
		},
		{
			name: "Err: initial interval greater than max interval",
			policy: exponential.Policy{
				InitialInterval:     2 * time.Minute,
				Multiplier:          2.0,
				RandomizationFactor: 0.5,
				MaxInterval:         1 * time.Minute,
			},
			want: errors.New("Policy.InitialInterval must be less than or equal to Policy.MaxInterval"),
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got := validatePolicy(test.policy)
			if diff := pretty.Compare(got, test.want); diff != "" {
				t.Errorf("Validate(): -got +want: %v", diff)
			}
		})
	}
}
