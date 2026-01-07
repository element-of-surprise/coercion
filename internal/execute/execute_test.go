package execute

import (
	"fmt"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/gostdlib/base/concurrency/sync"
	"github.com/gostdlib/base/context"

	"github.com/element-of-surprise/coercion/internal/execute/sm"
	testplugins "github.com/element-of-surprise/coercion/internal/execute/sm/testing/plugins"
	pluginsLib "github.com/element-of-surprise/coercion/plugins"
	"github.com/element-of-surprise/coercion/plugins/registry"
	"github.com/element-of-surprise/coercion/workflow"
	"github.com/element-of-surprise/coercion/workflow/storage"
	"github.com/element-of-surprise/coercion/workflow/utils/walk"
	"github.com/google/uuid"
	"github.com/gostdlib/base/retry/exponential"
	"github.com/gostdlib/base/statemachine"
	"github.com/kylelemons/godebug/pretty"
)

type badPlugin struct {
	pluginsLib.Plugin
}

func (b badPlugin) Name() string {
	return "bad"
}

func (b badPlugin) Init() error {
	return fmt.Errorf("bad plugin")
}

func (b badPlugin) RetryPolicy() exponential.Policy {
	return pluginsLib.FastRetryPolicy()
}

func (b badPlugin) Request() any {
	return struct{}{}
}

func (b badPlugin) Response() any {
	return struct{}{}
}

type goodPlugin struct {
	name string

	pluginsLib.Plugin
}

func (g goodPlugin) Name() string {
	return g.name
}

func (g goodPlugin) Init() error {
	return nil
}

func (g goodPlugin) RetryPolicy() exponential.Policy {
	return pluginsLib.FastRetryPolicy()
}

func (g goodPlugin) Request() any {
	return struct{}{}
}

func (g goodPlugin) Response() any {
	return struct{}{}
}

func TestInitPlugins(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		plugins []pluginsLib.Plugin
		wantErr bool
	}{
		{
			name: "no plugins",
		},
		{
			name: "good plugins",
			plugins: []pluginsLib.Plugin{
				goodPlugin{name: "good1"},
				goodPlugin{name: "good2"},
			},
		},
		{
			name: "has bad plugin",
			plugins: []pluginsLib.Plugin{
				goodPlugin{name: "good1"},
				badPlugin{},
			},
			wantErr: true,
		},
	}

	for _, test := range tests {
		reg := registry.New()
		for _, p := range test.plugins {
			reg.Register(p)
		}
		p := &Plans{
			registry: reg,
		}
		err := p.initPlugins(context.Background())
		switch {
		case test.wantErr && err == nil:
			t.Errorf("TestInitPlugins(%s): got err == nil, want err != nil", test.name)
		case !test.wantErr && err != nil:
			t.Errorf("TestInitPlugins(%s): got err == %v, want err == nil", test.name, err)
		}
	}

}

type fakeStore struct {
	storage.Vault

	m map[uuid.UUID]*workflow.Plan
}

func (f *fakeStore) Read(ctx context.Context, id uuid.UUID) (*workflow.Plan, error) {
	p, ok := f.m[id]
	if !ok {
		return nil, fmt.Errorf("plan not found")
	}
	return p, nil
}

func (f *fakeStore) UpdatePlan(ctx context.Context, plan *workflow.Plan) error {
	return nil
}

type fakeRunner struct {
	called bool
	req    statemachine.Request[sm.Data]
	ran    chan struct{}
}

func (r *fakeRunner) Run(name string, req statemachine.Request[sm.Data], options ...statemachine.Option) (statemachine.Request[sm.Data], error) {
	defer close(r.ran)
	r.called = true
	r.req = req
	return req, nil
}

func NewV7() uuid.UUID {
	for {
		id, err := uuid.NewV7()
		if err == nil {
			return id
		}
	}
}

func TestStart(t *testing.T) {
	t.Parallel()

	storedID := NewV7()
	runningID := NewV7()
	completedID := NewV7()
	failedID := NewV7()
	stoppedID := NewV7()

	tests := []struct {
		name           string
		id             uuid.UUID
		plan           *workflow.Plan
		wantErr        bool
		wantRunnerCall bool
	}{
		{
			name:    "Error: no plan could be found",
			id:      NewV7(),
			wantErr: true,
		},
		{
			name: "Success: plan starts execution",
			id:   storedID,
			plan: func() *workflow.Plan {
				p := &workflow.Plan{ID: storedID, SubmitTime: time.Now()}
				p.State.Set(workflow.State{Status: workflow.NotStarted})
				return p
			}(),
			wantRunnerCall: true,
		},
		{
			name: "Success: plan already Running returns nil without starting",
			id:   runningID,
			plan: func() *workflow.Plan {
				p := &workflow.Plan{ID: runningID, SubmitTime: time.Now()}
				p.State.Set(workflow.State{Status: workflow.Running, Start: time.Now()})
				return p
			}(),
			wantRunnerCall: false,
		},
		{
			name: "Success: plan already Completed returns nil without starting",
			id:   completedID,
			plan: func() *workflow.Plan {
				p := &workflow.Plan{ID: completedID, SubmitTime: time.Now().Add(-time.Minute)}
				p.State.Set(workflow.State{Status: workflow.Completed, Start: time.Now().Add(-time.Minute), End: time.Now()})
				return p
			}(),
			wantRunnerCall: false,
		},
		{
			name: "Success: plan already Failed returns nil without starting",
			id:   failedID,
			plan: func() *workflow.Plan {
				p := &workflow.Plan{ID: failedID, SubmitTime: time.Now().Add(-time.Minute)}
				p.State.Set(workflow.State{Status: workflow.Failed, Start: time.Now().Add(-time.Minute), End: time.Now()})
				return p
			}(),
			wantRunnerCall: false,
		},
		{
			name: "Success: plan already Stopped returns nil without starting",
			id:   stoppedID,
			plan: func() *workflow.Plan {
				p := &workflow.Plan{ID: stoppedID, SubmitTime: time.Now().Add(-time.Minute)}
				p.State.Set(workflow.State{Status: workflow.Stopped, Start: time.Now().Add(-time.Minute), End: time.Now()})
				return p
			}(),
			wantRunnerCall: false,
		},
	}

	for _, test := range tests {
		store := &fakeStore{
			m: map[uuid.UUID]*workflow.Plan{},
		}
		if test.plan != nil {
			store.m[test.id] = test.plan
		}

		fr := &fakeRunner{ran: make(chan struct{})}

		p := &Plans{
			store:     store,
			runner:    fr.Run,
			states:    &sm.States{},
			stoppers:  sync.ShardedMap[uuid.UUID, context.CancelFunc]{},
			waiters:   sync.ShardedMap[uuid.UUID, chan struct{}]{},
			maxSubmit: 30 * time.Minute,
		}
		p.addValidators()

		err := p.Start(context.Background(), test.id)
		switch {
		case test.wantErr && err == nil:
			t.Errorf("TestStart(%s): got err == nil, want err != nil", test.name)
			continue
		case !test.wantErr && err != nil:
			t.Errorf("TestStart(%s): got err == %v, want err == nil", test.name, err)
			continue
		case err != nil:
			continue
		}

		if test.wantRunnerCall {
			select {
			case <-time.After(2 * time.Second):
				t.Errorf("TestStart(%s): runner was not called", test.name)
			case <-fr.ran:
			}

			if diff := pretty.Compare(test.plan, fr.req.Data.Plan); diff != "" {
				t.Errorf("TestStart(%s): Plan in Request diff: -want/+got:\n%s", test.name, diff)
			}
			if methodName(fr.req.Next) != methodName(p.states.Start) {
				t.Errorf("TestStart(%s): Next method in Request is not the expected Start method", test.name)
			}

			if p.stoppers.Len() > 0 {
				t.Errorf("TestStart(%s): did not delete the stopper entry", test.name)
			}
		} else {
			// Give a brief moment to ensure runner isn't called
			select {
			case <-time.After(100 * time.Millisecond):
				// Good, runner wasn't called
			case <-fr.ran:
				t.Errorf("TestStart(%s): runner was called but should not have been", test.name)
			}
		}
	}
}

func TestWait(t *testing.T) {
	t.Parallel()

	runningID := NewV7()
	completedID := NewV7()
	failedID := NewV7()
	stoppedID := NewV7()
	notStartedID := NewV7()
	bugRunningID := NewV7()
	notFoundID := NewV7()

	tests := []struct {
		name        string
		id          uuid.UUID
		plan        *workflow.Plan
		addWaiter   bool
		closeWaiter bool
		cancelCtx   bool
		wantErr     bool
	}{
		{
			name: "Success: plan is actively running and completes",
			id:   runningID,
			plan: func() *workflow.Plan {
				p := &workflow.Plan{ID: runningID}
				p.State.Set(workflow.State{Status: workflow.Running})
				return p
			}(),
			addWaiter:   true,
			closeWaiter: true,
		},
		{
			name: "Error: plan is actively running and context cancelled",
			id:   runningID,
			plan: func() *workflow.Plan {
				p := &workflow.Plan{ID: runningID}
				p.State.Set(workflow.State{Status: workflow.Running})
				return p
			}(),
			addWaiter: true,
			cancelCtx: true,
			wantErr:   true,
		},
		{
			name: "Success: plan is Completed and not running",
			id:   completedID,
			plan: func() *workflow.Plan {
				p := &workflow.Plan{ID: completedID}
				p.State.Set(workflow.State{Status: workflow.Completed, Start: time.Now().Add(-time.Minute), End: time.Now()})
				return p
			}(),
		},
		{
			name: "Success: plan is Failed and not running",
			id:   failedID,
			plan: func() *workflow.Plan {
				p := &workflow.Plan{ID: failedID}
				p.State.Set(workflow.State{Status: workflow.Failed, Start: time.Now().Add(-time.Minute), End: time.Now()})
				return p
			}(),
		},
		{
			name: "Success: plan is Stopped and not running",
			id:   stoppedID,
			plan: func() *workflow.Plan {
				p := &workflow.Plan{ID: stoppedID}
				p.State.Set(workflow.State{Status: workflow.Stopped, Start: time.Now().Add(-time.Minute), End: time.Now()})
				return p
			}(),
		},
		{
			name: "Error: plan is NotStarted",
			id:   notStartedID,
			plan: func() *workflow.Plan {
				p := &workflow.Plan{ID: notStartedID}
				p.State.Set(workflow.State{Status: workflow.NotStarted})
				return p
			}(),
			wantErr: true,
		},
		{
			name: "Error: plan is Running but not in waiters",
			id:   bugRunningID,
			plan: func() *workflow.Plan {
				p := &workflow.Plan{ID: bugRunningID}
				p.State.Set(workflow.State{Status: workflow.Running})
				return p
			}(),
			wantErr: true,
		},
		{
			name:    "Error: plan does not exist",
			id:      notFoundID,
			wantErr: true,
		},
	}

	for _, test := range tests {
		fakeStore := &fakeStore{
			m: map[uuid.UUID]*workflow.Plan{
				runningID:    tests[0].plan,
				completedID:  tests[2].plan,
				failedID:     tests[3].plan,
				stoppedID:    tests[4].plan,
				notStartedID: tests[5].plan,
				bugRunningID: tests[6].plan,
			},
		}

		p := &Plans{
			store:   fakeStore,
			waiters: sync.ShardedMap[uuid.UUID, chan struct{}]{},
		}

		ctx := context.Background()
		if test.cancelCtx {
			var cancel context.CancelFunc
			ctx, cancel = context.WithCancel(ctx)
			cancel()
		}

		if test.addWaiter {
			waiter := make(chan struct{})
			p.waiters.Set(test.id, waiter)
			if test.closeWaiter {
				close(waiter)
			}
		}

		err := p.Wait(ctx, test.id)
		switch {
		case test.wantErr && err == nil:
			t.Errorf("TestWait(%s): got err == nil, want err != nil", test.name)
		case !test.wantErr && err != nil:
			t.Errorf("TestWait(%s): got err == %v, want err == nil", test.name, err)
		}
	}
}

func TestValidateStartState(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		plan    *workflow.Plan
		wantErr bool
	}{
		{
			name:    "plan is nil",
			wantErr: true,
		},
		{
			name:    "Plan is too old",
			plan:    &workflow.Plan{ID: NewV7(), SubmitTime: time.Now().Add(-time.Hour)},
			wantErr: true,
		},
		{
			name: "Success",
			plan: &workflow.Plan{ID: NewV7(), SubmitTime: time.Now()},
		},
	}

	for _, test := range tests {
		p := &Plans{maxSubmit: 30 * time.Minute}

		err := p.validateStartState(test.plan)
		switch {
		case test.wantErr && err == nil:
			t.Errorf("TestValidateStartState(%s): got err == nil, want err != nil", test.name)
		case !test.wantErr && err != nil:
			t.Errorf("TestValidateStartState(%s): got err == %v, want err == nil", test.name, err)
		}
	}
}

func TestValidatePlan(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		item    walk.Item
		wantErr bool
	}{
		{
			name: "not a plan",
			item: walk.Item{
				Value: &workflow.Action{},
			},
		},
		{
			name: "plan SubmitTime is the zero value",
			item: walk.Item{
				Value: &workflow.Plan{},
			},
			wantErr: true,
		},
		{
			name: "plan Reason is not the zero value",
			item: walk.Item{
				Value: &workflow.Plan{
					SubmitTime: time.Now(),
					Reason:     workflow.FRBlock,
				},
			},
			wantErr: true,
		},
		{
			name: "Success",
			item: walk.Item{
				Value: &workflow.Plan{
					SubmitTime: time.Now(),
				},
			},
		},
	}

	for _, test := range tests {
		p := &Plans{}
		p.addValidators()

		err := p.validatePlan(test.item)
		switch {
		case test.wantErr && err == nil:
			t.Errorf("TestValidatePlan(%s): got err == nil, want err != nil", test.name)
		case !test.wantErr && err != nil:
			t.Errorf("TestValidatePlan(%s): got err == %v, want err == nil", test.name, err)
		}
	}
}

func TestValidateAction(t *testing.T) {
	t.Parallel()

	checkPlugin := "checkPlugin"

	reg := registry.New()
	reg.Register(&testplugins.Plugin{})
	reg.Register(&testplugins.Plugin{PlugName: checkPlugin, IsCheckPlugin: true})

	tests := []struct {
		name    string
		item    walk.Item
		wantErr bool
	}{
		{
			name: "not an action",
			item: walk.Item{
				Value: &workflow.Plan{},
			},
		},
		{
			name: "action.Attempts != nil",
			item: func() walk.Item {
				a := &workflow.Action{}
				a.Attempts.Set([]workflow.Attempt{{}})
				return walk.Item{Value: a}
			}(),
			wantErr: true,
		},
		{
			name: "plugin is not defined",
			item: walk.Item{
				Value: &workflow.Action{
					Plugin: "not here",
				},
			},
			wantErr: true,
		},
		{
			name: "Parent is Checks object, but plugin is not a check plugin",
			item: walk.Item{
				Chain: []workflow.Object{&workflow.Checks{}},
				Value: &workflow.Action{
					Plugin: testplugins.Name,
				},
			},
			wantErr: true,
		},
		{
			name: "Success with parent == Sequence",
			item: walk.Item{
				Chain: []workflow.Object{&workflow.Sequence{}},
				Value: &workflow.Action{
					Plugin: testplugins.Name,
				},
			},
		},
		{
			name: "Success with parent == Checks",
			item: walk.Item{
				Chain: []workflow.Object{&workflow.Checks{}},
				Value: &workflow.Action{
					Plugin: checkPlugin,
				},
			},
		},
	}

	for _, test := range tests {
		p := &Plans{registry: reg}
		p.addValidators()

		err := p.validateAction(test.item)
		switch {
		case test.wantErr && err == nil:
			t.Errorf("TestValidateAction(%s): got err == nil, want err != nil", test.name)
		case !test.wantErr && err != nil:
			t.Errorf("TestValidateAction(%s): got err == %v, want err == nil", test.name, err)
		}
	}
}

func TestValidateID(t *testing.T) {
	id := NewV7()

	// This adds compile level checking that all the object types implement the ider interface.
	iders := []ider{
		&workflow.Plan{ID: id},
		&workflow.Checks{ID: id},
		&workflow.Block{ID: id},
		&workflow.Sequence{ID: id},
		&workflow.Action{ID: id},
	}

	for _, tIDer := range iders {
		p := &Plans{}
		p.addValidators()

		item := walk.Item{
			Value: tIDer.(workflow.Object), // Compile check that each implements workflow.Object.
		}

		err := p.validateID(item)
		if err != nil {
			t.Errorf("TestValidateID(%T): got err == %v, want err == nil", tIDer, err)
		}
	}
}

func TestValidateState(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		item    walk.Item
		wantErr bool
	}{
		{
			name: "Status != NotStarted",
			item: walk.Item{
				Value: func() *workflow.Plan {
					p := &workflow.Plan{}
					p.State.Set(workflow.State{Status: workflow.Running})
					return p
				}(),
			},
			wantErr: true,
		},
		{
			name: "Start != nil",
			item: walk.Item{
				Value: func() *workflow.Plan {
					p := &workflow.Plan{}
					p.State.Set(workflow.State{Start: time.Now()})
					return p
				}(),
			},
			wantErr: true,
		},
		{
			name: "End != nil",
			item: walk.Item{
				Value: func() *workflow.Plan {
					p := &workflow.Plan{}
					p.State.Set(workflow.State{End: time.Now()})
					return p
				}(),
			},
			wantErr: true,
		},
		{
			name: "Success",
			item: walk.Item{
				Value: func() *workflow.Plan {
					p := &workflow.Plan{}
					p.State.Set(workflow.State{})
					return p
				}(),
			},
		},
	}
	for _, test := range tests {
		p := &Plans{}
		p.addValidators()

		err := p.validateState(test.item)
		switch {
		case test.wantErr && err == nil:
			t.Errorf("TestValidateState(%s): got err == nil, want err != nil", test.name)
		case !test.wantErr && err != nil:
			t.Errorf("TestValidateState(%s): got err == %v, want err == nil", test.name, err)
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
