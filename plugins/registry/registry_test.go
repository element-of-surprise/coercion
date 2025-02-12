package registry

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/gostdlib/base/retry/exponential"
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

// Example structs for testing
type User struct {
	Username string
	Password string // should trigger the secret regex
	Email    string
}

type User2 struct {
	Username string
	Password string `coerce:"ignore"`
}

type Config struct {
	APIKey   string // should trigger the secret regex
	Endpoint string
}

type NestedConfig struct {
	Detail struct {
		SigningKey string // should trigger the secret regex
	}
}

type NestedConfig2 struct {
	Detail struct {
		SigningKey string `coerce:"secure"`
	}
}

type NoSecrets struct {
	Detail struct {
		NothingHere string
	}
}

// TestFindSecrets function
func TestFindSecrets(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		value any
		err   bool
	}{
		{
			name:  "No secrets",
			value: &NoSecrets{},
		},
		{
			name:  "Secrets field in User struct called Password, but it is not set",
			value: &User{Username: "john_doe", Email: "john@example.com"},
			err:   true,
		},
		{
			name:  "Secrets field in User struct called Password, but field annotated with ignore",
			value: &User2{Username: "john_doe", Password: "supersecret"},
		},
		{
			name:  "WithSecretPassword",
			value: &User{Password: "supersecret"},
			err:   true,
		},
		{
			name:  "ConfigWithAPIKey",
			value: Config{APIKey: "12345"},
			err:   true,
		},
		{
			name:  "NestedConfigWithSigningKey",
			value: NestedConfig{Detail: struct{ SigningKey string }{SigningKey: "key123"}},
			err:   true,
		},
		{
			name: "NestedConfigWithSecureSigningKey, but field annotated with secure",
			value: &NestedConfig2{Detail: struct {
				SigningKey string `coerce:"secure"`
			}{SigningKey: "key123"}},
		},
	}

	for _, test := range tests {
		err := findSecrets(test.value, "")
		switch {
		case err == nil && test.err:
			t.Errorf("TestFindSecrets(%v): got err == nil, want err != nil", test.name)
		case err != nil && !test.err:
			t.Errorf("TestFindSecrets(%v): got err != %v, want err == nil", test.name, err)
		}
	}
}

type tagsStruct struct {
	FieldA string `coerce:"secure"`
	FieldB string `coerce:"secure,ignored"`
	FieldC string `coerce:" "`
}

func TestGetTags(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		f    reflect.StructField
		want tags
	}{
		{
			name: "Success: FieldA",
			f:    reflect.TypeOf(tagsStruct{}).Field(0),
			want: tags{"secure": true},
		},
		{
			name: "Success: FieldB",
			f:    reflect.TypeOf(tagsStruct{}).Field(1),
			want: tags{"secure": true, "ignored": true},
		},
		{
			name: "Success: FieldC",
			f:    reflect.TypeOf(tagsStruct{}).Field(2),
			want: nil,
		},
	}

	for _, test := range tests {
		got := getTags(test.f)
		if diff := pretty.Compare(test.want, got); diff != "" {
			t.Errorf("TestGetTags(%s): -want/+got:\n%s", test.name, diff)
		}
	}
}
