package actions

import (
	"context"
	"errors"
	"testing"
	"time"

	testplugin "github.com/element-of-surprise/workstream/internal/execute/sm/testing/plugins"
	"github.com/element-of-surprise/workstream/plugins"
	"github.com/element-of-surprise/workstream/plugins/registry"
	"github.com/element-of-surprise/workstream/workflow"
	"github.com/element-of-surprise/workstream/workflow/storage/sqlite"

	"github.com/gostdlib/ops/retry/exponential"
	"github.com/kylelemons/godebug/pretty"
)

func TestExec(t *testing.T) {
	t.Parallel()

	reg := registry.New()
	reg.Register(&testplugin.Plugin{})

	now := time.Now()
	nower := func() time.Time {
		return now
	}

	tests := []struct {
		name   string
		ctx    context.Context
		plugin plugins.Plugin
		action *workflow.Action

		wantAttempts []*workflow.Attempt
		wantErr      bool
		errPermanent bool
	}{
		{
			name: "Attempts exceeds retries",
			ctx:  context.Background(),
			plugin: &testplugin.Plugin{
				AlwaysRespond: true,
			},
			action: &workflow.Action{
				Attempts: []*workflow.Attempt{{}, {}},
				Retries:  1,
				State:    &workflow.State{},
			},
			wantErr:      true,
			errPermanent: true,
			wantAttempts: []*workflow.Attempt{{}, {}},
		},
		{
			name: "Timeout",
			ctx:  context.Background(),
			plugin: &testplugin.Plugin{
				Responses: []any{
					testplugin.Resp{Arg: "ok"},
				},
			},
			action: &workflow.Action{
				Req:     testplugin.Req{Arg: "error", Sleep: time.Second},
				Timeout: 10 * time.Millisecond,
				State:   &workflow.State{},
			},
			wantAttempts: []*workflow.Attempt{
				{
					Err: &plugins.Error{
						Message: pluginTimeoutMsg,
					},
					Start: now,
					End:   now,
				},
			},
			wantErr: true,
		},
	}

	sm := Runner{nower: nower}
	for _, test := range tests {
		rw, err := sqlite.New(context.Background(), "", sqlite.WithInMemory())
		if err != nil {
			t.Fatalf("TestExec(%s): failed to create writer: %v", test.name, err)
		}
		defer rw.Close(context.Background())

		err = sm.exec(test.ctx, test.action, test.plugin, rw)

		switch {
		case err == nil && test.wantErr:
			t.Errorf("TestExec(%s): got err == nil, want error != nil", test.name)
			continue
		case err != nil && !test.wantErr:
			t.Errorf("TestExec(%s): got err == %v, want error == nil", test.name, err)
			continue
		case err != nil:
			if test.errPermanent != errors.Is(err, exponential.ErrPermanent) {
				t.Errorf("TestExec(%s): got err permament == %v, want error permanent == %v", test.name, errors.Is(err, exponential.ErrPermanent), test.errPermanent)
			}
		}

		if diff := pretty.Compare(test.wantAttempts, test.action.Attempts); diff != "" {
			t.Errorf("TestExec(%s): unexpected last attempt: -want/+got:\n%s", test.name, diff)
		}
	}
}

func TestRun(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		req         testplugin.Req
		timeout     time.Duration
		wantResp    testplugin.Resp
		wantErr     bool
		wantTimeout bool
	}{
		{
			name:        "successful execution",
			req:         testplugin.Req{Sleep: 10 * time.Millisecond},
			timeout:     100 * time.Millisecond,
			wantResp:    testplugin.Resp{Arg: "ok"},
			wantErr:     false,
			wantTimeout: false,
		},
		{
			name:        "execution with error",
			req:         testplugin.Req{Sleep: 10 * time.Millisecond, Arg: "error"},
			timeout:     100 * time.Millisecond,
			wantErr:     true,
			wantTimeout: false,
		},
		{
			name:        "context timeout",
			req:         testplugin.Req{Sleep: 200 * time.Millisecond},
			timeout:     50 * time.Millisecond,
			wantErr:     false,
			wantTimeout: true,
		},
	}

	for _, test := range tests {
		ctx, cancel := context.WithTimeout(context.Background(), test.timeout)
		defer cancel()

		resp := run(ctx, &testplugin.Plugin{AlwaysRespond: true}, test.req)
		switch {
		case test.wantErr && resp.Err == nil:
			t.Errorf("TestRun(%s): got err == nil, want error != nil", test.name)
		case !test.wantErr && resp.Err != nil:
			t.Errorf("TestRun(%s): got err == %v, want error == nil", test.name, resp.Err)
		case test.wantTimeout && !resp.timeout:
			t.Errorf("TestRun(%s): got timeout == false, want timeout == true", test.name)
		case !test.wantTimeout && resp.timeout:
			t.Errorf("TestRun(%s): got timeout == true, want timeout == false", test.name)
		case test.wantErr || test.wantTimeout:
			continue
		}

		if diff := pretty.Compare(test.wantResp, resp.Resp); diff != "" {
			t.Errorf("TestRun(%s): unexpected response: -want/+got:\n%s", test.name, diff)
		}
	}
}

func TestIsType(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		a, b any
		want bool
	}{
		{
			name: "Error: different types",
			a:    &workflow.Action{},
			b:    &workflow.Plan{},
			want: false,
		},
		{
			name: "Success",
			a:    &workflow.Action{},
			b:    &workflow.Action{},
			want: true,
		},
	}

	for _, test := range tests {
		got := isType(test.a, test.b)
		if got != test.want {
			t.Errorf("TestIsType(%s): got %v, want %v", test.name, got, test.want)
		}
	}
}
