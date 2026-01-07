package registry

import (
	"errors"
	"testing"
	"time"

	"github.com/element-of-surprise/coercion/plugins"
	"github.com/element-of-surprise/coercion/workflow/context"
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

// Fake plugin request/response types for testing.
type secureReq struct {
	Data     string
	Password string `coerce:"secure"`
}

type secureResp struct {
	Result string
	Token  string `coerce:"secure"`
}

type insecureReq struct {
	Data     string
	Password string // Missing coerce tag - should trigger error
}

type insecureResp struct {
	Result string
	Token  string // Missing coerce tag - should trigger error
}

// fakePlugin implements plugins.Plugin for testing.
type fakePlugin struct {
	name     string
	req      any
	resp     any
	isCheck  bool
	policy   exponential.Policy
	initErr  error
}

func (f *fakePlugin) Name() string                                         { return f.name }
func (f *fakePlugin) Execute(ctx context.Context, req any) (any, *plugins.Error) { return nil, nil }
func (f *fakePlugin) ValidateReq(req any) error                            { return nil }
func (f *fakePlugin) Request() any                                         { return f.req }
func (f *fakePlugin) Response() any                                        { return f.resp }
func (f *fakePlugin) IsCheck() bool                                        { return f.isCheck }
func (f *fakePlugin) RetryPolicy() exponential.Policy                      { return f.policy }
func (f *fakePlugin) Init() error                                          { return f.initErr }

func validPolicy() exponential.Policy {
	return exponential.Policy{
		InitialInterval:     100 * time.Millisecond,
		Multiplier:          2.0,
		RandomizationFactor: 0.5,
		MaxInterval:         60 * time.Second,
	}
}

func TestRegister(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		plugin  plugins.Plugin
		wantErr bool
	}{
		{
			name: "Success: plugin with secure request and response",
			plugin: &fakePlugin{
				name:   "test-plugin-secure",
				req:    secureReq{},
				resp:   secureResp{},
				policy: validPolicy(),
			},
			wantErr: false,
		},
		{
			name: "Error: plugin with insecure request field",
			plugin: &fakePlugin{
				name:   "test-plugin-insecure-req",
				req:    insecureReq{},
				resp:   secureResp{},
				policy: validPolicy(),
			},
			wantErr: true,
		},
		{
			name: "Error: plugin with insecure response field",
			plugin: &fakePlugin{
				name:   "test-plugin-insecure-resp",
				req:    secureReq{},
				resp:   insecureResp{},
				policy: validPolicy(),
			},
			wantErr: true,
		},
		{
			name: "Error: plugin with both insecure request and response",
			plugin: &fakePlugin{
				name:   "test-plugin-insecure-both",
				req:    insecureReq{},
				resp:   insecureResp{},
				policy: validPolicy(),
			},
			wantErr: true,
		},
		{
			name: "Success: plugin with non-struct request and response",
			plugin: &fakePlugin{
				name:   "test-plugin-non-struct",
				req:    "string-request",
				resp:   42,
				policy: validPolicy(),
			},
			wantErr: false,
		},
	}

	for _, test := range tests {
		reg := New()
		err := reg.Register(test.plugin)
		switch {
		case err == nil && test.wantErr:
			t.Errorf("TestRegister(%s): got err == nil, want err != nil", test.name)
		case err != nil && !test.wantErr:
			t.Errorf("TestRegister(%s): got err == %v, want err == nil", test.name, err)
		}
	}
}
