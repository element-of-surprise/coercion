package workflow

import (
	"context"
	"errors"
	"testing"

	"github.com/element-of-surprise/coercion/plugins"
	"github.com/element-of-surprise/coercion/plugins/registry"
	"github.com/gostdlib/base/retry/exponential"

	"github.com/kylelemons/godebug/pretty"
)

func TestPlanValidate(t *testing.T) {
	t.Parallel()

	goodPlan := func() *Plan {
		p := Plan{
			Name:           "test",
			Descr:          "test",
			BypassChecks:   &Checks{},
			PreChecks:      &Checks{},
			PostChecks:     &Checks{},
			ContChecks:     &Checks{},
			DeferredChecks: &Checks{},
			Blocks:         []*Block{{}},
		}
		x := func(plan Plan) Plan {
			return plan
		}(p)
		return &x
	}
	expectVals := []validator{
		goodPlan().BypassChecks,
		goodPlan().PreChecks,
		goodPlan().PostChecks,
		goodPlan().ContChecks,
		goodPlan().DeferredChecks,
	}
	for _, v := range goodPlan().Blocks {
		expectVals = append(expectVals, v)
	}

	tests := []struct {
		name       string
		plan       func() *Plan
		validators []validator
		err        bool
	}{
		{
			name: "Error: Plan is nil",
			plan: func() *Plan { return nil },
			err:  true,
		},
		{
			name: "Error: Name is empty",
			plan: func() *Plan {
				p := goodPlan()
				p.Name = ""
				return p
			},
			err: true,
		},
		{
			name: "Error: Descr is empty",
			plan: func() *Plan {
				p := goodPlan()
				p.Descr = ""
				return p
			},
			err: true,
		},
		{
			name: "Error: Blocks is nil",
			plan: func() *Plan {
				p := goodPlan()
				p.Blocks = nil
				return p
			},
			err: true,
		},
		{
			name: "Error: Blocks is empty",
			plan: func() *Plan {
				p := goodPlan()
				p.Blocks = []*Block{}
				return p
			},
			err: true,
		},
		{
			name: "Error: State != nil",
			plan: func() *Plan {
				p := goodPlan()
				p.State = &State{}
				return p
			},
			err: true,
		},
		{
			name:       "Success",
			plan:       goodPlan,
			validators: expectVals,
		},
	}

	for _, test := range tests {
		ctx := context.Background()
		m := map[string]bool{}
		ctx = context.WithValue(ctx, keysMap{}, m)

		p := test.plan()
		gotValidators, err := p.validate(ctx)
		switch {
		case test.err && err == nil:
			t.Errorf("TestPlanValidate(%s): got err == nil, want err != nil", test.name)
			continue
		case !test.err && err != nil:
			t.Errorf("TestPlanValidate(%s): got err != %s, want err == nil", test.name, err)
			continue
		case err != nil:
			continue
		}

		if diff := pretty.Compare(test.validators, gotValidators); diff != "" {
			t.Errorf("TestPlanValidate(%s): returned validators: -want/+got:\n%s", test.name, diff)
		}
	}
}

func TestPreCheckValidate(t *testing.T) {
	t.Parallel()

	key := NewV7()
	goodPreChecks := func() *Checks {
		return &Checks{
			Key:     key,
			Actions: []*Action{{}},
		}
	}

	tests := []struct {
		name     string
		preCheck func() *Checks
		mapKeys  []string
		err      bool
		vals     []validator
	}{
		{
			name:     "Success: PreCheck is nil",
			preCheck: func() *Checks { return nil },
		},
		{
			name: "Error: Actions is nil",
			preCheck: func() *Checks {
				p := goodPreChecks()
				p.Actions = nil
				return p
			},
			err: true,
		},
		{
			name: "Error: Actions is empty",
			preCheck: func() *Checks {
				p := goodPreChecks()
				p.Actions = []*Action{}
				return p
			},
			err: true,
		},
		{
			name: "Error: State != nil",
			preCheck: func() *Checks {
				p := goodPreChecks()
				p.State = &State{}
				return p
			},
			err: true,
		},
		{
			name:     "Error: Duplicate Key",
			preCheck: goodPreChecks,
			mapKeys:  []string{key.String()},
			vals:     []validator{goodPreChecks().Actions[0]},
			err:      true,
		},
		{
			name:     "Success",
			preCheck: goodPreChecks,
			vals:     []validator{goodPreChecks().Actions[0]},
		},
	}

	for _, test := range tests {
		ctx := context.Background()
		m := map[string]bool{}
		for _, k := range test.mapKeys {
			m[k] = true
		}
		ctx = context.WithValue(ctx, keysMap{}, m)
		p := test.preCheck()
		gotValidators, err := p.validate(ctx)
		switch {
		case test.err && err == nil:
			t.Errorf("TestPreCheckValidate(%s): got err == nil, want err != nil", test.name)
			continue
		case !test.err && err != nil:
			t.Errorf("TestPreCheckValidate(%s): got err != %s, want err == nil", test.name, err)
			continue
		case err != nil:
			continue
		}

		if diff := pretty.Compare(test.vals, gotValidators); diff != "" {
			t.Errorf("TestPreCheckValidate(%s): returned validators: -want/+got:\n%s", test.name, diff)
		}
	}
}

func TestPostCheckValidate(t *testing.T) {
	t.Parallel()

	key := NewV7()

	goodPostChecks := func() *Checks {
		return &Checks{
			Key:     key,
			Actions: []*Action{{}},
		}
	}

	tests := []struct {
		name      string
		postCheck func() *Checks
		mapKeys   []string
		err       bool
		vals      []validator
	}{
		{
			name:      "Success: PostChecks is nil",
			postCheck: func() *Checks { return nil },
		},
		{
			name: "Error: Actions is nil",
			postCheck: func() *Checks {
				p := goodPostChecks()
				p.Actions = nil
				return p
			},
			err: true,
		},
		{
			name: "Error: Actions is empty",
			postCheck: func() *Checks {
				p := goodPostChecks()
				p.Actions = []*Action{}
				return p
			},
			err: true,
		},
		{
			name: "Error: State != nil",
			postCheck: func() *Checks {
				p := goodPostChecks()
				p.State = &State{}
				return p
			},
			err: true,
		},
		{
			name:      "Error: Duplicate Key",
			postCheck: goodPostChecks,
			mapKeys:   []string{key.String()},
			vals:      []validator{goodPostChecks().Actions[0]},
			err:       true,
		},
		{
			name:      "Success",
			postCheck: goodPostChecks,
			vals:      []validator{goodPostChecks().Actions[0]},
		},
	}

	for _, test := range tests {
		ctx := context.Background()
		m := map[string]bool{}
		for _, k := range test.mapKeys {
			m[k] = true
		}
		ctx = context.WithValue(ctx, keysMap{}, m)
		p := test.postCheck()
		gotValidators, err := p.validate(ctx)
		switch {
		case test.err && err == nil:
			t.Errorf("TestPostCheckValidate(%s): got err == nil, want err != nil", test.name)
			continue
		case !test.err && err != nil:
			t.Errorf("TestPostCheckValidate(%s): got err != %s, want err == nil", test.name, err)
			continue
		case err != nil:
			continue
		}

		if diff := pretty.Compare(test.vals, gotValidators); diff != "" {
			t.Errorf("TestPostCheckValidate(%s): returned validators: -want/+got:\n%s", test.name, diff)
		}
	}
}

func TestDeferredCheckValidate(t *testing.T) {
	t.Parallel()

	key := NewV7()
	goodDeferChecks := func() *Checks {
		return &Checks{
			Key:     key,
			Actions: []*Action{{}},
		}
	}

	tests := []struct {
		name       string
		deferCheck func() *Checks
		mapKeys    []string
		err        bool
		vals       []validator
	}{
		{
			name:       "Success: PostChecks is nil",
			deferCheck: func() *Checks { return nil },
		},
		{
			name: "Error: Actions is nil",
			deferCheck: func() *Checks {
				p := goodDeferChecks()
				p.Actions = nil
				return p
			},
			err: true,
		},
		{
			name: "Error: Actions is empty",
			deferCheck: func() *Checks {
				p := goodDeferChecks()
				p.Actions = []*Action{}
				return p
			},
			err: true,
		},
		{
			name: "Error: State != nil",
			deferCheck: func() *Checks {
				p := goodDeferChecks()
				p.State = &State{}
				return p
			},
			err: true,
		},
		{
			name:       "Error: Duplicate Key",
			deferCheck: goodDeferChecks,
			mapKeys:    []string{key.String()},
			vals:       []validator{goodDeferChecks().Actions[0]},
			err:        true,
		},
		{
			name:       "Success",
			deferCheck: goodDeferChecks,
			vals:       []validator{goodDeferChecks().Actions[0]},
		},
	}

	for _, test := range tests {
		ctx := context.Background()
		m := map[string]bool{}
		for _, k := range test.mapKeys {
			m[k] = true
		}
		ctx = context.WithValue(ctx, keysMap{}, m)
		p := test.deferCheck()
		gotValidators, err := p.validate(ctx)
		switch {
		case test.err && err == nil:
			t.Errorf("TestDeferCheckValidate(%s): got err == nil, want err != nil", test.name)
			continue
		case !test.err && err != nil:
			t.Errorf("TestDeferCheckValidate(%s): got err != %s, want err == nil", test.name, err)
			continue
		case err != nil:
			continue
		}

		if diff := pretty.Compare(test.vals, gotValidators); diff != "" {
			t.Errorf("TestDeferCheckValidate(%s): returned validators: -want/+got:\n%s", test.name, diff)
		}
	}
}

func TestContCheckValidate(t *testing.T) {
	t.Parallel()

	key := NewV7()
	goodContChecks := func() *Checks {
		return &Checks{
			Key:     key,
			Actions: []*Action{{}},
		}
	}

	tests := []struct {
		name      string
		contCheck func() *Checks
		mapKeys   []string
		err       bool
		vals      []validator
	}{
		{
			name:      "Success: ContChecks is nil",
			contCheck: func() *Checks { return nil },
		},
		{
			name: "Error: Actions is nil",
			contCheck: func() *Checks {
				p := goodContChecks()
				p.Actions = nil
				return p
			},
			err: true,
		},
		{
			name: "Error: Actions is empty",
			contCheck: func() *Checks {
				p := goodContChecks()
				p.Actions = []*Action{}
				return p
			},
			err: true,
		},
		{
			name: "Error: State != nil",
			contCheck: func() *Checks {
				p := goodContChecks()
				p.State = &State{}
				return p
			},
			err: true,
		},
		{
			name:      "Error: Duplicate Key",
			contCheck: goodContChecks,
			mapKeys:   []string{key.String()},
			vals:      []validator{goodContChecks().Actions[0]},
			err:       true,
		},
		{
			name:      "Success",
			contCheck: goodContChecks,
			vals:      []validator{goodContChecks().Actions[0]},
		},
	}

	for _, test := range tests {
		ctx := context.Background()
		m := map[string]bool{}
		for _, k := range test.mapKeys {
			m[k] = true
		}
		ctx = context.WithValue(ctx, keysMap{}, m)
		p := test.contCheck()
		gotValidators, err := p.validate(ctx)
		switch {
		case test.err && err == nil:
			t.Errorf("TestContCheckValidate(%s): got err == nil, want err != nil", test.name)
			continue
		case !test.err && err != nil:
			t.Errorf("TestContCheckValidate(%s): got err != %s, want err == nil", test.name, err)
			continue
		case err != nil:
			continue
		}

		if diff := pretty.Compare(test.vals, gotValidators); diff != "" {
			t.Errorf("TestContCheckValidate(%s): returned validators: -want/+got:\n%s", test.name, diff)
		}
	}
}

func TestBlockValidate(t *testing.T) {
	t.Parallel()

	key := NewV7()
	goodBlock := func() *Block {
		b := Block{
			Key:            key,
			Name:           "block",
			Descr:          "block description",
			BypassChecks:   &Checks{},
			PreChecks:      &Checks{},
			PostChecks:     &Checks{},
			ContChecks:     &Checks{},
			DeferredChecks: &Checks{},
			Sequences:      []*Sequence{{}},
		}
		x := func(block Block) Block {
			return block
		}(b)
		return &x
	}

	tests := []struct {
		name    string
		block   func() *Block
		mapKeys []string
		err     bool
		vals    []validator
	}{
		{
			name:  "Error: Block is nil",
			block: func() *Block { return nil },
			err:   true,
		},
		{
			name: "Error: Name is empty",
			block: func() *Block {
				b := goodBlock()
				b.Name = ""
				return b
			},
			err: true,
		},
		{
			name: "Error: Descr is empty",
			block: func() *Block {
				b := goodBlock()
				b.Descr = ""
				return b
			},
			err: true,
		},
		{
			name: "Error: Sequences is nil",
			block: func() *Block {
				b := goodBlock()
				b.Sequences = nil
				return b
			},
			err: true,
		},
		{
			name: "Error: Sequences is empty",
			block: func() *Block {
				b := goodBlock()
				b.Sequences = []*Sequence{}
				return b
			},
			err: true,
		},
		{
			name: "Error: State is non-nil",
			block: func() *Block {
				b := goodBlock()
				b.State = &State{}
				return b
			},
			err: true,
		},
		{
			name:    "Error: Duplicate Key",
			block:   goodBlock,
			mapKeys: []string{key.String()},
			vals: []validator{
				goodBlock().BypassChecks,
				goodBlock().PreChecks,
				goodBlock().PostChecks,
				goodBlock().ContChecks,
				goodBlock().DeferredChecks,
				goodBlock().Sequences[0],
			},
			err: true,
		},
		{
			name:  "Success",
			block: goodBlock,
			vals: []validator{
				goodBlock().BypassChecks,
				goodBlock().PreChecks,
				goodBlock().PostChecks,
				goodBlock().ContChecks,
				goodBlock().DeferredChecks,
				goodBlock().Sequences[0],
			},
		},
	}

	for _, test := range tests {
		ctx := context.Background()
		m := map[string]bool{}
		for _, k := range test.mapKeys {
			m[k] = true
		}
		ctx = context.WithValue(ctx, keysMap{}, m)
		b := test.block()
		gotValidators, err := b.validate(ctx)
		switch {
		case test.err && err == nil:
			t.Errorf("TestBlockValidate(%s): got err == nil, want err != nil", test.name)
			continue
		case !test.err && err != nil:
			t.Errorf("TestBlockValidate(%s): got err != %s, want err == nil", test.name, err)
			continue
		case err != nil:
			continue
		}

		if diff := pretty.Compare(test.vals, gotValidators); diff != "" {
			t.Errorf("TestBlockValidate(%s): returned validators: -want/+got:\n%s", test.name, diff)
		}
	}
}

func TestSequenceValidate(t *testing.T) {
	t.Parallel()

	key := NewV7()
	goodSequence := func() *Sequence {
		return &Sequence{
			Key:     key,
			Name:    "sequence",
			Descr:   "sequence description",
			Actions: []*Action{{}},
		}
	}

	tests := []struct {
		name     string
		sequence func() *Sequence
		mapKeys  []string
		err      bool
		vals     []validator
	}{
		{
			name:     "Error: Sequence is nil",
			sequence: func() *Sequence { return nil },
			err:      true,
		},
		{
			name: "Error: Name is empty",
			sequence: func() *Sequence {
				s := goodSequence()
				s.Name = ""
				return s
			},
			err: true,
		},
		{
			name: "Error: Descr is empty",
			sequence: func() *Sequence {
				s := goodSequence()
				s.Descr = ""
				return s
			},
			err: true,
		},
		{
			name: "Error: Actions is nil",
			sequence: func() *Sequence {
				s := goodSequence()
				s.Actions = nil
				return s
			},
			err: true,
		},
		{
			name: "Error: Jobs is empty",
			sequence: func() *Sequence {
				s := goodSequence()
				s.Actions = []*Action{}
				return s
			},
			err: true,
		},
		{
			name: "Error: State is non-nil",
			sequence: func() *Sequence {
				s := goodSequence()
				s.State = &State{}
				return s
			},
			err: true,
		},
		{
			name:     "Error: Duplicate Key",
			sequence: goodSequence,
			mapKeys:  []string{key.String()},
			vals:     []validator{goodSequence().Actions[0]},
			err:      true,
		},
		{
			name:     "Success",
			sequence: goodSequence,
			vals:     []validator{goodSequence().Actions[0]},
		},
	}

	for _, test := range tests {
		ctx := context.Background()
		m := map[string]bool{}
		for _, k := range test.mapKeys {
			m[k] = true
		}
		ctx = context.WithValue(ctx, keysMap{}, m)
		s := test.sequence()
		gotValidators, err := s.validate(ctx)
		switch {
		case test.err && err == nil:
			t.Errorf("TestSequenceValidate(%s): got err == nil, want err != nil", test.name)
			continue
		case !test.err && err != nil:
			t.Errorf("TestSequenceValidate(%s): got err != %s, want err == nil", test.name, err)
			continue
		case err != nil:
			continue
		}

		if diff := pretty.Compare(test.vals, gotValidators); diff != "" {
			t.Errorf("TestSequenceValidate(%s): returned validators: -want/+got:\n%s", test.name, diff)
		}
	}
}

type validatePlugin struct {
	plugins.Plugin
}

func (validatePlugin) Request() any {
	return struct{}{}
}

func (validatePlugin) Response() any {
	return struct{}{}
}

func (validatePlugin) Name() string {
	return "validatePlugin"
}

func (validatePlugin) RetryPolicy() exponential.Policy {
	return plugins.FastRetryPolicy()
}

func (v validatePlugin) ValidateReq(req any) error {
	if req == nil {
		return errors.New("req is nil")
	}
	if _, ok := req.(string); !ok {
		return errors.New("req is not a string")
	}
	return nil
}

func TestActionValidate(t *testing.T) {
	t.Parallel()

	reg := registry.New()
	reg.Register(validatePlugin{})

	key := NewV7()
	goodAction := func() *Action {
		return &Action{
			Key:      key,
			Name:     "goodAction",
			Descr:    "goodAction",
			Plugin:   "validatePlugin",
			Req:      "goodAction",
			register: reg,
		}
	}

	tests := []struct {
		name    string
		action  func() *Action
		mapKeys []string
		err     bool
	}{
		{
			name: "Error: Action is nil",
			action: func() *Action {
				return nil
			},
			err: true,
		},
		{
			name: "Error: Name is empty",
			action: func() *Action {
				a := goodAction()
				a.Name = ""
				return a
			},
			err: true,
		},
		{
			name: "Error: Descr is empty",
			action: func() *Action {
				a := goodAction()
				a.Descr = ""
				return a
			},
			err: true,
		},
		{
			name: "Error: Plugin is empty",
			action: func() *Action {
				a := goodAction()
				a.Plugin = ""
				return a
			},
			err: true,
		},
		{
			name: "Error: State is not nil",
			action: func() *Action {
				a := goodAction()
				a.State = &State{}
				return a
			},
			err: true,
		},
		{
			name: "Error: Plugin not found",
			action: func() *Action {
				a := goodAction()
				a.Plugin = "notFound"
				return a
			},
			err: true,
		},
		{
			name: "Error: Req doesn't validate",
			action: func() *Action {
				a := goodAction()
				a.Req = 123
				return a
			},
			err: true,
		},
		{
			name:    "Error: Duplicate Key",
			action:  goodAction,
			mapKeys: []string{key.String()},
			err:     true,
		},
		{
			name:   "Success",
			action: goodAction,
		},
	}

	for _, test := range tests {
		ctx := context.Background()
		m := map[string]bool{}
		for _, k := range test.mapKeys {
			m[k] = true
		}
		ctx = context.WithValue(ctx, keysMap{}, m)
		a := test.action()
		gotValidator, err := a.validate(ctx)
		switch {
		case test.err && err == nil:
			t.Errorf("TestActionValidate(%s): got err == nil, want err != nil", test.name)
			continue
		case !test.err && err != nil:
			t.Errorf("TestActionValidate(%s): got err == %v, want err == nil", test.name, err)
			continue
		case err != nil:
			continue
		}

		if len(gotValidator) != 0 {
			t.Errorf("TestActionValidate(%s): got validator != nil, want validator == nil", test.name)
		}
	}
}
