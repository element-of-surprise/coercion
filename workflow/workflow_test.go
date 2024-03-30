package workflow

import (
	"errors"
	"testing"
	"time"

	"github.com/element-of-surprise/workstream/plugins"

	"github.com/kylelemons/godebug/pretty"
)

func TestPlanValidate(t *testing.T) {
	t.Parallel()

	goodPlan := func() *Plan {
		return &Plan{
			Name:       "test",
			Descr:      "test",
			PreChecks:  &PreChecks{},
			PostChecks: &PostChecks{},
			ContChecks: &ContChecks{},
			Blocks:     []*Block{{}},
		}
	}
	expectVals := []validator{
		goodPlan().PreChecks,
		goodPlan().PostChecks,
		goodPlan().ContChecks,
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
			name: "Error: Internal != nil",
			plan: func() *Plan {
				p := goodPlan()
				p.Internal = &Internal{}
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
		p := test.plan()
		gotValidators, err := p.validate()
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

	goodPreChecks := func() *PreChecks {
		return &PreChecks{
			Actions: []*Action{{}},
		}
	}

	tests := []struct {
		name     string
		preCheck func() *PreChecks
		err      bool
		vals     []validator
	}{
		{
			name:     "Success: PreCheck is nil",
			preCheck: func() *PreChecks { return nil },
		},
		{
			name: "Error: Actions is nil",
			preCheck: func() *PreChecks {
				p := goodPreChecks()
				p.Actions = nil
				return p
			},
			err: true,
		},
		{
			name: "Error: Actions is empty",
			preCheck: func() *PreChecks {
				p := goodPreChecks()
				p.Actions = []*Action{}
				return p
			},
			err: true,
		},
		{
			name: "Error: Internal != nil",
			preCheck: func() *PreChecks {
				p := goodPreChecks()
				p.Internal = &Internal{}
				return p
			},
			err: true,
		},
		{
			name:     "Success",
			preCheck: goodPreChecks,
			vals:     []validator{goodPreChecks().Actions[0]},
		},
	}

	for _, test := range tests {
		p := test.preCheck()
		gotValidators, err := p.validate()
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

	goodPostChecks := func() *PostChecks {
		return &PostChecks{
			Actions: []*Action{{}},
		}
	}

	tests := []struct {
		name      string
		postCheck func() *PostChecks
		err       bool
		vals      []validator
	}{
		{
			name:      "Success: PostChecks is nil",
			postCheck: func() *PostChecks { return nil },
		},
		{
			name: "Error: Actions is nil",
			postCheck: func() *PostChecks {
				p := goodPostChecks()
				p.Actions = nil
				return p
			},
			err: true,
		},
		{
			name: "Error: Actions is empty",
			postCheck: func() *PostChecks {
				p := goodPostChecks()
				p.Actions = []*Action{}
				return p
			},
			err: true,
		},
		{
			name: "Error: Internal != nil",
			postCheck: func() *PostChecks {
				p := goodPostChecks()
				p.Internal = &Internal{}
				return p
			},
			err: true,
		},
		{
			name:      "Success",
			postCheck: goodPostChecks,
			vals:      []validator{goodPostChecks().Actions[0]},
		},
	}

	for _, test := range tests {
		p := test.postCheck()
		gotValidators, err := p.validate()
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

func TestContCheckValidate(t *testing.T) {
	t.Parallel()

	goodContChecks := func() *ContChecks {
		return &ContChecks{
			Actions: []*Action{{}},
		}
	}

	tests := []struct {
		name      string
		contCheck func() *ContChecks
		err       bool
		vals      []validator
	}{
		{
			name:      "Success: ContChecks is nil",
			contCheck: func() *ContChecks { return nil },
		},
		{
			name: "Error: Actions is nil",
			contCheck: func() *ContChecks {
				p := goodContChecks()
				p.Actions = nil
				return p
			},
			err: true,
		},
		{
			name: "Error: Actions is empty",
			contCheck: func() *ContChecks {
				p := goodContChecks()
				p.Actions = []*Action{}
				return p
			},
			err: true,
		},
		{
			name: "Error: Internal != nil",
			contCheck: func() *ContChecks {
				p := goodContChecks()
				p.Internal = &Internal{}
				return p
			},
			err: true,
		},
		{
			name:      "Success",
			contCheck: goodContChecks,
			vals:      []validator{goodContChecks().Actions[0]},
		},
	}

	for _, test := range tests {
		p := test.contCheck()
		gotValidators, err := p.validate()
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

	goodBlock := func() *Block {
		b := &Block{
			Name:       "block",
			Descr:      "block description",
			PreChecks:  &PreChecks{},
			PostChecks: &PostChecks{},
			ContChecks: &ContChecks{},
			Sequences:  []*Sequence{{}},
		}
		return b.defaults()
	}

	tests := []struct {
		name  string
		block func() *Block
		err   bool
		vals  []validator
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
			name: "Error: Internal is non-nil",
			block: func() *Block {
				b := goodBlock()
				b.Internal = &Internal{}
				return b
			},
			err: true,
		},
		{
			name:  "Success",
			block: goodBlock,
			vals: []validator{
				goodBlock().PreChecks,
				goodBlock().PostChecks,
				goodBlock().ContChecks,
				goodBlock().Sequences[0],
			},
		},
	}

	for _, test := range tests {
		b := test.block()
		gotValidators, err := b.validate()
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

	goodSequence := func() *Sequence {
		return &Sequence{
			Name:  "sequence",
			Descr: "sequence description",
			Jobs:  []*Job{{}},
		}
	}

	tests := []struct {
		name     string
		sequence func() *Sequence
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
			name: "Error: Jobs is nil",
			sequence: func() *Sequence {
				s := goodSequence()
				s.Jobs = nil
				return s
			},
			err: true,
		},
		{
			name: "Error: Jobs is empty",
			sequence: func() *Sequence {
				s := goodSequence()
				s.Jobs = []*Job{}
				return s
			},
			err: true,
		},
		{
			name: "Error: Internal is non-nil",
			sequence: func() *Sequence {
				s := goodSequence()
				s.Internal = &Internal{}
				return s
			},
			err: true,
		},
		{
			name:     "Success",
			sequence: goodSequence,
			vals:     []validator{goodSequence().Jobs[0]},
		},
	}

	for _, test := range tests {
		s := test.sequence()
		gotValidators, err := s.validate()
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

type checkPlugin struct {
	plugins.Plugin
	isCheck bool
}

func (c checkPlugin) IsCheck() bool {
	return c.isCheck
}

func TestJobValidate(t *testing.T) {
	t.Parallel()

	goodJob := func() *Job {
		return (&Job{
			Name:  "job",
			Descr: "job description",
			Action: &Action{
				plugin: checkPlugin{},
			},
		}).defaults()
	}

	tests := []struct {
		name string
		job  func() *Job
		err  bool
		vals []validator
	}{
		{
			name: "Error: Job is nil",
			job:  func() *Job { return nil },
			err:  true,
		},
		{
			name: "Error: Name is empty",
			job: func() *Job {
				j := goodJob()
				j.Name = ""
				return j
			},
			err: true,
		},
		{
			name: "Error: Descr is empty",
			job: func() *Job {
				j := goodJob()
				j.Descr = ""
				return j
			},
			err: true,
		},
		{
			name: "Error: Action is nil",
			job: func() *Job {
				j := goodJob()
				j.Action = nil
				return j
			},
			err: true,
		},
		{
			name: "Error: Timeout is < 5 seconds",
			job: func() *Job {
				j := goodJob()
				j.Timeout = 4 * time.Second
				return j
			},
			err: true,
		},
		{
			name: "Error: Internal is non-nil",
			job: func() *Job {
				j := goodJob()
				j.Internal = &Internal{}
				return j
			},
			err: true,
		},
		{
			name: "Error: Action is nil",
			job: func() *Job {
				j := goodJob()
				j.Action = nil
				return j
			},
			err: true,
		},
		{
			name: "Error: Action is a Check Action",
			job: func() *Job {
				j := goodJob()
				j.Action = &Action{
					plugin: checkPlugin{isCheck: true},
				}
				return j
			},
			err: true,
		},
		{
			name: "Success",
			job:  goodJob,
			vals: []validator{goodJob().Action},
		},
	}

	for _, test := range tests {
		j := test.job()
		gotValidators, err := j.validate()
		switch {
		case test.err && err == nil:
			t.Errorf("TestJobValidate(%s): got err == nil, want err != nil", test.name)
			continue
		case !test.err && err != nil:
			t.Errorf("TestJobValidate(%s): got err != %s, want err == nil", test.name, err)
			continue
		case err != nil:
			continue
		}

		if diff := pretty.Compare(test.vals, gotValidators); diff != "" {
			t.Errorf("TestJobValidate(%s): returned validators: -want/+got:\n%s", test.name, diff)
		}
	}
}

type falseRegister struct {
	m map[string]plugins.Plugin
}

func (f falseRegister) Get(name string) plugins.Plugin {
	if f.m == nil {
		return nil
	}
	return f.m[name]
}

type validatePlugin struct {
	plugins.Plugin
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

	reg := falseRegister{
		m: map[string]plugins.Plugin{
			"myPlugin": validatePlugin{},
		},
	}

	goodAction := func() *Action {
		return &Action{
			Name:     "goodAction",
			Descr:    "goodAction",
			Plugin:   "myPlugin",
			Req:      "goodAction",
			register: reg,
		}
	}

	tests := []struct {
		name   string
		action func() *Action
		err    bool
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
			name: "Error: Internal is not nil",
			action: func() *Action {
				a := goodAction()
				a.Internal = &Internal{}
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
			name:   "Success",
			action: goodAction,
		},
	}

	for _, test := range tests {
		a := test.action()
		gotValidator, err := a.validate()
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
