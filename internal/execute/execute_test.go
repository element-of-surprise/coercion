package execute

import (
	"context"
	"fmt"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/element-of-surprise/coercion/internal/execute/sm"
	testplugins "github.com/element-of-surprise/coercion/internal/execute/sm/testing/plugins"
	pluginsLib "github.com/element-of-surprise/coercion/plugins"
	"github.com/element-of-surprise/coercion/plugins/registry"
	"github.com/element-of-surprise/coercion/workflow"
	"github.com/element-of-surprise/coercion/workflow/storage"
	"github.com/element-of-surprise/coercion/workflow/utils/walk"
	"github.com/google/uuid"
	"github.com/gostdlib/ops/retry/exponential"
	"github.com/gostdlib/ops/statemachine"
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

type fakeRunner struct {
	called bool
	req    statemachine.Request[sm.Data]
	ran    chan struct{}
}

func (r *fakeRunner) Run(name string, req statemachine.Request[sm.Data], options ...statemachine.Option[sm.Data]) (statemachine.Request[sm.Data], error) {
	defer close(r.ran)
	r.called = true
	r.req = req
	return req, nil
}

func TestStart(t *testing.T) {
	t.Parallel()

	storedID := uuid.New()

	tests := []struct {
		name    string
		id      uuid.UUID
		plan    *workflow.Plan
		wantErr bool
	}{
		{
			name:    "no plan could be found",
			id:      uuid.New(),
			wantErr: true,
		},
		{
			name:    "plan is invalid",
			id:      storedID,
			plan:    &workflow.Plan{}, //  plan is invalid, has no ID
			wantErr: true,
		},
		{
			name: "plan starts execution",
			id:   storedID,
			plan: &workflow.Plan{
				ID: storedID,
				State: &workflow.State{
					Status: workflow.NotStarted,
				},
				SubmitTime: time.Now(),
			},
		},
	}

	for _, test := range tests {
		fakeStore := &fakeStore{
			m: map[uuid.UUID]*workflow.Plan{
				storedID: test.plan,
			},
		}
		fr := &fakeRunner{ran: make(chan struct{})}

		p := &Plans{store: fakeStore, runner: fr.Run, states: &sm.States{}, stoppers: map[uuid.UUID]context.CancelFunc{}}
		p.addValidators()

		err := p.Start(context.Background(), test.id)
		switch {
		case test.wantErr && err == nil:
			t.Errorf("TestStart(%s): got err == nil, want err != nil", test.name)
		case !test.wantErr && err != nil:
			t.Errorf("TestStart(%s): got err == %v, want err == nil", test.name, err)
		case err != nil:
			continue
		}

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
		p.mu.Lock()
		stopperLen := len(p.stoppers)
		p.mu.Unlock()
		if stopperLen > 0 {
			t.Errorf("TestStart(%s): did not delete the stopper entry", test.name)
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
			name: "plan is not nil",
			plan: &workflow.Plan{ID: uuid.New()},
		},
	}

	for _, test := range tests {
		p := &Plans{}

		err := p.validateStartState(context.Background(), test.plan)
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
			item: walk.Item{
				Value: &workflow.Action{
					Attempts: []*workflow.Attempt{},
				},
			},
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
	id := uuid.New()

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
			name: "State == nil",
			item: walk.Item{
				Value: &workflow.Plan{},
			},
			wantErr: true,
		},
		{
			name: "Status != NotStarted",
			item: walk.Item{
				Value: &workflow.Plan{
					State: &workflow.State{
						Status: workflow.Running,
					},
				},
			},
			wantErr: true,
		},
		{
			name: "Start != nil",
			item: walk.Item{
				Value: &workflow.Plan{
					State: &workflow.State{
						Start: time.Now(),
					},
				},
			},
			wantErr: true,
		},
		{
			name: "End != nil",
			item: walk.Item{
				Value: &workflow.Plan{
					State: &workflow.State{
						End: time.Now(),
					},
				},
			},
			wantErr: true,
		},
		{
			name: "Success",
			item: walk.Item{
				Value: &workflow.Plan{
					State: &workflow.State{},
				},
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
