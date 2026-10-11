package actions

import (
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
	"github.com/element-of-surprise/coercion/workflow/context"
	"github.com/element-of-surprise/coercion/workflow/errors"

	"github.com/gostdlib/base/statemachine"
	"github.com/kylelemons/godebug/pretty"
)

func newActionWithState(state *workflow.State) *workflow.Action {
	a := &workflow.Action{}
	a.State.Set(*state)
	return a
}

type fakeUpdater struct {
	updates []*workflow.Action
	// attempts holds len(action.Attempts) at the time of each update, as updates holds pointers that change later.
	attempts []int
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
	f.attempts = append(f.attempts, len(action.Attempts.Get()))
	return nil
}

func TestStart(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	nower := func() time.Time {
		return now
	}

	action := newActionWithState(&workflow.State{})
	data := Data{
		Action:  action,
		Updater: newFakeUpdater(),
	}

	sm := Runner{nower: nower}
	req := sm.Start(statemachine.Request[Data]{Ctx: t.Context(), Data: data, Next: sm.Start})

	wantAction := newActionWithState(&workflow.State{Start: now, Status: workflow.Running})

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
		wantErr  bool
	}{
		{
			name: "Error: the plugin is not in the registry, so the next state is End",
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
			},
			wantNext: methodName(sm.End),
			wantErr:  true,
		},
		{
			name: "Success: the plugin is in the registry, so it is set and the next state is Execute",
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
		req := sm.GetPlugin(statemachine.Request[Data]{Ctx: t.Context(), Data: test.data, Next: sm.GetPlugin})
		if (req.Data.err != nil) != test.wantErr {
			t.Errorf("TestGetPlugin(%s): got Data.err == %v, want Data.err != nil == %v", test.name, req.Data.err, test.wantErr)
		}
		// Remove the registry and error from the request data for comparison.
		req.Data.Registry = nil
		req.Data.err = nil
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
		wantErr  bool
	}{
		{
			name: "Error: the plugin fails on every attempt, so retries are exhausted",
			data: Data{
				Action: func() *workflow.Action {
					a := &workflow.Action{Plugin: testplugin.Name, Timeout: 1 * time.Second, Retries: 1, Req: testplugin.Req{}}
					a.State.Set(workflow.State{})
					return a
				}(),
				plugin: &testplugin.Plugin{
					Responses: []any{pluginErr, pluginErr},
				},
			},
			wantData: Data{
				Action: func() *workflow.Action {
					a := &workflow.Action{Plugin: testplugin.Name, Timeout: 1 * time.Second, Retries: 1, Req: testplugin.Req{}}
					a.Attempts.Set([]workflow.Attempt{{Err: &plugins.Error{Message: pluginErr.Error()}, Start: now, End: now}, {Err: &plugins.Error{Message: pluginErr.Error()}, Start: now, End: now}})
					a.State.Set(workflow.State{})
					return a
				}(),
			},
			wantErr: true,
		},
		{
			name: "Success: the plugin fails once and then succeeds on the retry",
			data: Data{
				Action: func() *workflow.Action {
					a := &workflow.Action{Plugin: testplugin.Name, Timeout: 1 * time.Second, Retries: 1, Req: testplugin.Req{}}
					a.State.Set(workflow.State{})
					return a
				}(),
				plugin: &testplugin.Plugin{
					Responses: []any{pluginErr, testplugin.Resp{Arg: "ok"}},
				},
			},
			wantData: Data{
				Action: func() *workflow.Action {
					a := &workflow.Action{Plugin: testplugin.Name, Timeout: 1 * time.Second, Retries: 1, Req: testplugin.Req{}}
					a.Attempts.Set([]workflow.Attempt{{Err: &plugins.Error{Message: pluginErr.Error()}, Start: now, End: now}, {Resp: testplugin.Resp{Arg: "ok"}, Start: now, End: now}})
					a.State.Set(workflow.State{})
					return a
				}(),
			},
		},
	}

	sm := Runner{nower: nower}
	for _, test := range tests {
		test.data.Updater = newFakeUpdater()
		req := statemachine.Request[Data]{Ctx: t.Context(), Data: test.data}
		req = sm.Execute(req)
		if (req.Data.err != nil) != test.wantErr {
			t.Errorf("TestExecute(%s): got Data.err == %v, want Data.err != nil == %v", test.name, req.Data.err, test.wantErr)
		}
		// Clear the plugin, updater and error to make the comparison easier.
		req.Data.plugin = nil
		req.Data.Updater = nil
		req.Data.err = nil

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
			name: "Error: Data holds an error, so the action is marked as failed",
			data: Data{
				Action:  newActionWithState(&workflow.State{}),
				Updater: newFakeUpdater(),
				err:     errors.New("fake error"),
			},
			wantDBAction: newActionWithState(&workflow.State{Status: workflow.Failed, End: now}),
			wantErr:      true,
		},
		{
			name: "Success: Data holds no error, so the action is marked as completed",
			data: Data{
				Action:  newActionWithState(&workflow.State{}),
				Updater: newFakeUpdater(),
			},
			wantDBAction: newActionWithState(&workflow.State{Status: workflow.Completed, End: now}),
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
			t.Errorf("TestEnd(%s): got err == %v, want err != nil == %v", test.name, req.Err, test.wantErr)
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
		plugin plugins.Plugin
		action *workflow.Action

		wantAttempts []*workflow.Attempt
		// wantWrites holds len(action.Attempts) for each UpdateAction call exec should make.
		wantWrites   []int
		wantErr      bool
		errPermanent bool
	}{
		{
			name: "Error: attempts already exceed retries, so the plugin is not run and nothing is written",
			plugin: &testplugin.Plugin{
				AlwaysRespond: true,
			},
			action: func() *workflow.Action {
				a := &workflow.Action{Retries: 1}
				a.Attempts.Set([]workflow.Attempt{{Err: &plugins.Error{Message: "first"}}, {Err: &plugins.Error{Message: "last"}}})
				a.State.Set(workflow.State{})
				return a
			}(),
			wantErr:      true,
			errPermanent: true,
			wantAttempts: []*workflow.Attempt{{Err: &plugins.Error{Message: "first"}}, {Err: &plugins.Error{Message: "last"}}},
			wantWrites:   nil,
		},
		{
			name: "Error: plugin times out, so a retryable attempt is recorded and written",
			plugin: &testplugin.Plugin{
				Responses: []any{
					testplugin.Resp{Arg: "ok"},
				},
			},
			action: func() *workflow.Action {
				a := &workflow.Action{Req: testplugin.Req{Sleep: time.Second}, Timeout: 10 * time.Millisecond}
				a.State.Set(workflow.State{})
				return a
			}(),
			wantAttempts: []*workflow.Attempt{
				{
					Err: &plugins.Error{
						Message: pluginTimeoutMsg,
					},
					Start: now,
					End:   now,
				},
			},
			wantWrites: []int{1},
			wantErr:    true,
		},
		{
			name: "Error: the attempt's timeout ends before the plugin is submitted, so a timed-out attempt is recorded and written",
			plugin: &testplugin.Plugin{
				AlwaysRespond: true,
			},
			action: func() *workflow.Action {
				// A negative timeout makes the run ctx done before Submit, so the pool refuses it.
				a := &workflow.Action{Req: testplugin.Req{Arg: "ok"}, Timeout: -1}
				a.State.Set(workflow.State{})
				return a
			}(),
			wantAttempts: []*workflow.Attempt{
				{
					Err: &plugins.Error{
						Message: pluginTimeoutMsg,
					},
					Start: now,
					End:   now,
				},
			},
			wantWrites: []int{1},
			wantErr:    true,
		},
		{
			name: "Error: plugin returns an unexpected response type, so a permanent attempt is recorded and written",
			plugin: &testplugin.Plugin{
				Responses: []any{
					struct{ Hello string }{},
				},
			},
			action: func() *workflow.Action {
				a := &workflow.Action{Req: testplugin.Req{Arg: "ok"}, Timeout: 100 * time.Millisecond}
				a.State.Set(workflow.State{})
				return a
			}(),
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
			wantWrites:   []int{1},
			wantErr:      true,
			errPermanent: true,
		},
		{
			name: "Success: plugin returns a response, so the attempt is recorded and written",
			plugin: &testplugin.Plugin{
				Responses: []any{
					testplugin.Resp{Arg: "ok"},
				},
			},
			action: func() *workflow.Action {
				a := &workflow.Action{Req: testplugin.Req{Arg: "ok"}, Timeout: 100 * time.Millisecond}
				a.State.Set(workflow.State{})
				return a
			}(),
			wantAttempts: []*workflow.Attempt{
				{
					Resp:  &testplugin.Resp{Arg: "ok"},
					Start: now,
					End:   now,
				},
			},
			wantWrites: []int{1},
		},
	}

	sm := Runner{nower: nower}
	for _, test := range tests {
		updater := newFakeUpdater()

		err := sm.exec(t.Context(), test.action, test.plugin, updater)

		switch {
		case err == nil && test.wantErr:
			t.Errorf("TestExec(%s): got err == nil, want error != nil", test.name)
			continue
		case err != nil && !test.wantErr:
			t.Errorf("TestExec(%s): got err == %v, want error == nil", test.name, err)
			continue
		case err != nil:
			// Execute's backoff.Retry branches on this: it stops retrying when errors.Is(err, ErrPermanent).
			if test.errPermanent != errors.Is(err, errors.ErrPermanent) {
				t.Errorf("TestExec(%s): got err permanent == %v, want err permanent == %v", test.name, errors.Is(err, errors.ErrPermanent), test.errPermanent)
			}
		}

		if diff := pretty.Compare(test.wantAttempts, test.action.Attempts.Get()); diff != "" {
			t.Errorf("TestExec(%s): unexpected last attempt: -want/+got:\n%s", test.name, diff)
		}
		// exec must write the action once, after the attempt is appended, so a stored action always carries it.
		if diff := pretty.Compare(test.wantWrites, updater.attempts); diff != "" {
			t.Errorf("TestExec(%s): UpdateAction attempts at each write: -want/+got:\n%s", test.name, diff)
		}
	}
}

func TestRun(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		req     testplugin.Req
		timeout time.Duration
		// poolFull runs with a ctx whose pool is a Limited(1) pool with its only token already held, as a nested
		// sequence's pool can be when the parent holds the shared tokens.
		poolFull bool
		// cancelled runs with a ctx that is already done, so the pool refuses the Submit.
		cancelled   bool
		wantResp    testplugin.Resp
		wantErr     bool
		wantTimeout bool
	}{
		{
			name:        "Success: the plugin responds before the timeout",
			req:         testplugin.Req{Sleep: 10 * time.Millisecond},
			timeout:     100 * time.Millisecond,
			wantResp:    testplugin.Resp{Arg: "ok"},
			wantErr:     false,
			wantTimeout: false,
		},
		{
			name:        "Error: the plugin returns an error before the timeout",
			req:         testplugin.Req{Sleep: 10 * time.Millisecond, Arg: "error"},
			timeout:     100 * time.Millisecond,
			wantErr:     true,
			wantTimeout: false,
		},
		{
			name:        "Success: the plugin does not respond before the context times out, which is reported as a timeout",
			req:         testplugin.Req{Sleep: 200 * time.Millisecond},
			timeout:     100 * time.Millisecond,
			wantErr:     false,
			wantTimeout: true,
		},
		{
			name:     "Success: the plugin runs and responds when the ctx's Limited pool has no free token",
			req:      testplugin.Req{Sleep: 10 * time.Millisecond},
			timeout:  time.Second,
			poolFull: true,
			wantResp: testplugin.Resp{Arg: "ok"},
		},
		{
			name:        "Success: the ctx is done before the plugin is submitted, which is reported as a timeout",
			req:         testplugin.Req{Sleep: 10 * time.Millisecond},
			timeout:     time.Second,
			cancelled:   true,
			wantTimeout: true,
		},
	}

	for _, test := range tests {
		ctx, cancel := context.WithTimeout(t.Context(), test.timeout)
		defer cancel()

		if test.poolFull {
			limited := context.Pool(ctx).Limited(ctx, "TestRun", 1)
			release := make(chan struct{})
			t.Cleanup(func() { close(release) })
			if !limited.Submit(ctx, func() { <-release }) {
				t.Fatalf("TestRun(%s): could not take the Limited pool's only token", test.name)
			}
			ctx = context.SetPool(ctx, limited)
		}
		if test.cancelled {
			cancel()
		}

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
			name: "Success: values of different types are reported as not the same type",
			a:    &workflow.Action{},
			b:    &workflow.Plan{},
			want: false,
		},
		{
			name: "Success: values of the same type are reported as the same type",
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
