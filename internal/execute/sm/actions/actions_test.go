package actions

import (
	"context"
	"errors"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	testplugin "github.com/element-of-surprise/coercion/internal/execute/sm/testing/plugins"
	"github.com/element-of-surprise/coercion/internal/private"
	"github.com/element-of-surprise/coercion/plugins"
	"github.com/element-of-surprise/coercion/plugins/registry"
	"github.com/element-of-surprise/coercion/workflow"
	"github.com/element-of-surprise/coercion/workflow/storage/sqlite"

	"github.com/Azure/retry/exponential"
	"github.com/gostdlib/ops/statemachine"
	"github.com/kylelemons/godebug/pretty"
)

type fakeUpdater struct {
	updates  []*workflow.Action
	index    int
	retErrOn int

	private.Storage
}

func newFakeUpdater() *fakeUpdater {
	return &fakeUpdater{retErrOn: -1}
}

func (f *fakeUpdater) SetRetErrOn(i int) *fakeUpdater {
	f.retErrOn = i
	return f
}

func (f *fakeUpdater) UpdateAction(ctx context.Context, action *workflow.Action) error {
	defer func() {
		f.index++
	}()

	if f.index == f.retErrOn {
		return errors.New("fake error")
	}
	f.updates = append(f.updates, action)
	return nil
}

func TestStart(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	nower := func() time.Time {
		return now
	}

	data := Data{
		Action: &workflow.Action{
			State: &workflow.State{},
		},
		Updater: newFakeUpdater(),
	}

	sm := Runner{nower: nower}
	req := sm.Start(statemachine.Request[Data]{Ctx: context.Background(), Data: data, Next: sm.Start})

	wantAction := &workflow.Action{
		State: &workflow.State{
			Start:  now,
			Status: workflow.Running,
		},
	}

	if diff := pretty.Compare(wantAction, req.Data.Action); diff != "" {
		t.Errorf("TestStart: Action: -want/+got:\n%s", diff)
	}

	if methodName(req.Next) != methodName(sm.GetPlugin) {
		t.Errorf("TestStart: got Request.Next %s, want %s", methodName(req.Next), methodName(sm.GetPlugin))
	}

	if len(data.Updater.(*fakeUpdater).updates) != 1 {
		t.Errorf("TestStart: got %d updates, want 1", len(data.Updater.(*fakeUpdater).updates))
	}
	if diff := pretty.Compare(wantAction, data.Updater.(*fakeUpdater).updates[0]); diff != "" {
		t.Errorf("TestStart: UpdateAction: -want/+got:\n%s", diff)
	}
}

func TestGetPlugin(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	nower := func() time.Time {
		return now
	}

	sm := Runner{nower: nower}

	reg := registry.New()
	reg.Register(&testplugin.Plugin{})

	tests := []struct {
		name     string
		data     Data
		wantData Data
		wantNext string
	}{
		{
			name: "Plugin not found",
			data: Data{
				Action: &workflow.Action{
					Plugin: "notfound",
				},
				Registry: reg,
			},
			wantData: Data{
				Action: &workflow.Action{
					Plugin: "notfound",
				},
				err: pluginNotFoundErr("notfound"),
			},
			wantNext: methodName(sm.End),
		},
		{
			name: "Plugin found",
			data: Data{
				Action: &workflow.Action{
					Plugin: testplugin.Name,
				},
				Registry: reg,
			},
			wantData: Data{
				Action: &workflow.Action{
					Plugin: testplugin.Name,
				},
				plugin: reg.Plugin(testplugin.Name),
			},
			wantNext: methodName(sm.Execute),
		},
	}
	for _, test := range tests {
		req := sm.GetPlugin(statemachine.Request[Data]{Ctx: context.Background(), Data: test.data, Next: sm.GetPlugin})
		// Remove the registry from the request data for comparison.
		req.Data.Registry = nil
		if diff := pretty.Compare(test.wantData, req.Data); diff != "" {
			t.Errorf("TestGetPlugin(%s) -want/+got:\n%s", test.name, diff)
		}
		if methodName(req.Next) != test.wantNext {
			t.Errorf("TestGetPlugin(%s): got Request.Next %s, want Request.Next == %s", test.name, methodName(req.Next), test.wantNext)
		}
	}
}

func TestExecute(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	nower := func() time.Time {
		return now
	}

	pluginErr := &plugins.Error{
		Message: "plugin error",
	}

	tests := []struct {
		name     string
		data     Data
		wantData Data
	}{
		{
			name: "Failed after a retry",
			data: Data{
				Action: &workflow.Action{
					State:   &workflow.State{},
					Plugin:  testplugin.Name,
					Timeout: 1 * time.Second,
					Retries: 1,
					Req:     testplugin.Req{},
				},
				plugin: &testplugin.Plugin{
					Responses: []any{pluginErr, pluginErr},
				},
			},
			wantData: Data{
				Action: &workflow.Action{
					State:   &workflow.State{},
					Plugin:  testplugin.Name,
					Timeout: 1 * time.Second,
					Retries: 1,
					Req:     testplugin.Req{},
					Attempts: []*workflow.Attempt{
						{
							Err:   &plugins.Error{Message: pluginErr.Error()},
							Start: now,
							End:   now,
						},
						{
							Err:   &plugins.Error{Message: pluginErr.Error()},
							Start: now,
							End:   now,
						},
					},
				},
				err: exponential.ErrPermanent,
			},
		},
		{
			name: "Success after retry",
			data: Data{
				Action: &workflow.Action{
					State:   &workflow.State{},
					Plugin:  testplugin.Name,
					Timeout: 1 * time.Second,
					Retries: 1,
					Req:     testplugin.Req{},
				},
				plugin: &testplugin.Plugin{
					Responses: []any{pluginErr, testplugin.Resp{Arg: "ok"}},
				},
			},
			wantData: Data{
				Action: &workflow.Action{
					State:   &workflow.State{},
					Plugin:  testplugin.Name,
					Timeout: 1 * time.Second,
					Retries: 1,
					Req:     testplugin.Req{},
					Attempts: []*workflow.Attempt{
						{
							Err:   &plugins.Error{Message: pluginErr.Error()},
							Start: now,
							End:   now,
						},
						{
							Resp:  testplugin.Resp{Arg: "ok"},
							Start: now,
							End:   now,
						},
					},
				},
			},
		},
	}

	sm := Runner{nower: nower}
	for _, test := range tests {
		test.data.Updater = newFakeUpdater()
		req := statemachine.Request[Data]{Ctx: context.Background(), Data: test.data}
		req = sm.Execute(req)
		// Clear the plugin and updater to make the comparison easier.
		req.Data.plugin = nil
		req.Data.Updater = nil

		if diff := pretty.Compare(test.wantData, req.Data); diff != "" {
			t.Errorf("TestExecute(%s): -want +got):\n%s", test.name, diff)
		}
		if methodName(req.Next) != methodName(sm.End) {
			t.Errorf("TestExecute(%s): -want +got):\n%s", test.name, "Next method is not End state")
		}
		if req.Err != nil {
			t.Errorf("TestExecute(%s): got unexpected req.Err: %s", test.name, req.Err)
		}
	}
}

func TestEnd(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	nower := func() time.Time {
		return now
	}

	reg := registry.New()
	reg.Register(&testplugin.Plugin{})

	tests := []struct {
		name         string
		data         Data
		wantDBAction *workflow.Action
		wantErr      bool
	}{
		{
			name: "Data had error, so action should be marked as failed",
			data: Data{
				Action: &workflow.Action{
					State: &workflow.State{},
				},
				Updater: newFakeUpdater(),
				err:     errors.New("fake error"),
			},
			wantDBAction: &workflow.Action{
				State: &workflow.State{
					Status: workflow.Failed,
					End:    now,
				},
			},
			wantErr: true,
		},
		{
			name: "Data had no error, so action should be marked as completed",
			data: Data{
				Action: &workflow.Action{
					State: &workflow.State{},
				},
				Updater: newFakeUpdater(),
			},
			wantDBAction: &workflow.Action{
				State: &workflow.State{
					Status: workflow.Completed,
					End:    now,
				},
			},
		},
	}

	sm := Runner{nower: nower}
	for _, test := range tests {
		req := statemachine.Request[Data]{Data: test.data}
		req = sm.End(req)

		if diff := pretty.Compare(test.wantDBAction, test.data.Action); diff != "" {
			t.Errorf("TestEnd(%s): -want +got):\n%s", test.name, diff)
		}
		if test.wantErr != (req.Err != nil) {
			t.Errorf("TestEnd(%s): gotErr=%v, wantErr=%v", test.name, test.wantErr, req.Err)
		}
	}
}

func TestExec(t *testing.T) {
	t.Parallel()

	reg := registry.New()
	reg.Register(&testplugin.Plugin{})

	now := time.Now().UTC()
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
		{
			name: "Unexpected response type",
			ctx:  context.Background(),
			plugin: &testplugin.Plugin{
				Responses: []any{
					struct{ Hello string }{},
				},
			},
			action: &workflow.Action{
				Req:     testplugin.Req{Arg: "ok"},
				Timeout: 100 * time.Millisecond,
				State:   &workflow.State{},
			},
			wantAttempts: []*workflow.Attempt{
				{
					Err: &plugins.Error{
						Message:   unexpectedTypeMsg(reg.Plugin(testplugin.Name), struct{ Hello string }{}, reg.Plugin(testplugin.Name).Response()),
						Permanent: true,
					},
					Start: now,
					End:   now,
				},
			},
			wantErr:      true,
			errPermanent: true,
		},
		{
			name: "Success",
			ctx:  context.Background(),
			plugin: &testplugin.Plugin{
				Responses: []any{
					testplugin.Resp{Arg: "ok"},
				},
			},
			action: &workflow.Action{
				Req:     testplugin.Req{Arg: "ok"},
				Timeout: 100 * time.Millisecond,
				State:   &workflow.State{},
			},
			wantAttempts: []*workflow.Attempt{
				{
					Resp:  &testplugin.Resp{Arg: "ok"},
					Start: now,
					End:   now,
				},
			},
		},
	}

	sm := Runner{nower: nower}
	for _, test := range tests {
		rw, err := sqlite.New(context.Background(), "", reg, sqlite.WithInMemory())
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

// methodName returns the name of the method of the given value.
func methodName(method any) string {
	if method == nil {
		return "<nil>"
	}
	valueOf := reflect.ValueOf(method)
	switch valueOf.Kind() {
	case reflect.Func:
		return strings.TrimSuffix(strings.TrimSuffix(runtime.FuncForPC(valueOf.Pointer()).Name(), "-fm"), "[...]")
	default:
		return "<not a function>"
	}
}
