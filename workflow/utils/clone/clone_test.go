package clone

import (
	"testing"
	"time"

	"github.com/gostdlib/base/context"

	"github.com/element-of-surprise/coercion/plugins"
	"github.com/element-of-surprise/coercion/workflow"
	"github.com/google/uuid"

	"github.com/kylelemons/godebug/pretty"
)

type Req struct {
	Data string `coerce:"secure"`
}

func TestPlan(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	start := time.Now()
	id, err := uuid.NewV7()
	if err != nil {
		panic(err)
	}

	action := &workflow.Action{
		ID:   id,
		Name: "action1",
		Req:  Req{Data: "Hello"},
	}

	block := &workflow.Block{
		ID:    id,
		Name:  "block1",
		Descr: "descr",
	}

	checks := &workflow.Checks{
		ID: id,
		Actions: []*workflow.Action{
			Action(ctx, action, WithKeepState(), WithKeepSecrets()),
		},
	}

	deferredActions := &workflow.DeferredActions{
		ID: id,
		OnFailure: []*workflow.DeferBatch{
			DeferBatch(ctx, &workflow.DeferBatch{
				FailElement: true,
				Sequence: workflow.Sequence{
					Name:  "fail",
					Descr: "fail",
					Actions: []*workflow.Action{
						{Name: "fail_action", Req: Req{Data: "Hello"}},
					},
				},
			}, WithKeepState(), WithKeepSecrets()),
		},
	}

	plan := &workflow.Plan{
		ID:              id,
		Name:            "plan1",
		Descr:           "descr",
		GroupID:         id,
		Meta:            []byte("hello"),
		PreChecks:       Checks(ctx, checks, WithKeepSecrets(), WithKeepState()),
		PostChecks:      Checks(ctx, checks, WithKeepSecrets(), WithKeepState()),
		ContChecks:      Checks(ctx, checks, WithKeepSecrets(), WithKeepState()),
		DeferredActions: DeferredActions(ctx, deferredActions, WithKeepSecrets(), WithKeepState()),
		Blocks: []*workflow.Block{
			Block(ctx, block, WithKeepSecrets(), WithKeepState()),
		},
		Reason:     workflow.FRBlock,
		SubmitTime: start,
	}
	plan.State.Set(workflow.State{
		Status: workflow.Completed,
		Start:  start,
	})

	tests := []struct {
		name    string
		options cloneOptions
		plan    *workflow.Plan
		want    *workflow.Plan
	}{
		{
			name: "nil",
		},
		{
			name: "no options",
			plan: plan,
			want: &workflow.Plan{
				Name:            "plan1",
				Descr:           "descr",
				GroupID:         id,
				Meta:            []byte("hello"),
				PreChecks:       Checks(ctx, checks),
				PostChecks:      Checks(ctx, checks),
				ContChecks:      Checks(ctx, checks),
				DeferredActions: DeferredActions(ctx, deferredActions),
				Blocks:          []*workflow.Block{Block(ctx, plan.Blocks[0])},
			},
		},
		{
			name:    "WithKeepState(), WithKeepSecrets()",
			plan:    plan,
			options: cloneOptions{keepState: true, keepSecrets: true},
			want:    plan,
		},
		{
			name:    "WithKeepState()",
			plan:    plan,
			options: cloneOptions{keepState: true},
			want: func() *workflow.Plan {
				p := &workflow.Plan{
					ID:              id,
					Name:            "plan1",
					Descr:           "descr",
					GroupID:         id,
					Meta:            []byte("hello"),
					PreChecks:       Checks(ctx, checks, WithKeepState()),
					PostChecks:      Checks(ctx, checks, WithKeepState()),
					ContChecks:      Checks(ctx, checks, WithKeepState()),
					DeferredActions: DeferredActions(ctx, deferredActions, WithKeepState()),
					Blocks:          []*workflow.Block{Block(ctx, plan.Blocks[0], WithKeepState())},
					Reason:          workflow.FRBlock,
					SubmitTime:      start,
				}
				p.State.Set(workflow.State{
					Status: workflow.Completed,
					Start:  start,
				})
				return p
			}(),
		},
		{
			name:    "Without WithKeepSecrets(), but callNum > 0",
			plan:    plan,
			options: cloneOptions{keepSecrets: true, callNum: 1},
			want: &workflow.Plan{
				Name:            "plan1",
				Descr:           "descr",
				GroupID:         id,
				Meta:            []byte("hello"),
				PreChecks:       Checks(ctx, checks, WithKeepSecrets()),
				PostChecks:      Checks(ctx, checks, WithKeepSecrets()),
				ContChecks:      Checks(ctx, checks, WithKeepSecrets()),
				DeferredActions: DeferredActions(ctx, deferredActions, WithKeepSecrets()),
				Blocks:          []*workflow.Block{Block(ctx, plan.Blocks[0], WithKeepSecrets())},
			},
		},
	}

	for _, test := range tests {
		got := Plan(context.Background(), test.plan, withOptions(test.options))

		if diff := pretty.Compare(test.want, got); diff != "" {
			t.Errorf("TestPlan(%s): -want/+got:\n%s", test.name, diff)
		}
	}
}

func TestBlock(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	id, err := uuid.NewV7()
	if err != nil {
		panic(err)
	}

	action := &workflow.Action{
		Name: "action1",
		Req:  Req{Data: "Hello"},
	}

	sequence := &workflow.Sequence{
		Actions: []*workflow.Action{
			Action(ctx, action, WithKeepState(), WithKeepSecrets()),
		},
	}

	actionSecretRemoved := Action(ctx, action, WithKeepState())

	preChecks := &workflow.Checks{
		Actions: []*workflow.Action{
			Action(ctx, action, WithKeepState(), WithKeepSecrets()),
		},
	}
	preChecks.State.Set(workflow.State{})

	postChecks := &workflow.Checks{
		Actions: []*workflow.Action{
			Action(ctx, action, WithKeepState(), WithKeepSecrets()),
		},
	}
	postChecks.State.Set(workflow.State{})

	contChecks := &workflow.Checks{
		Actions: []*workflow.Action{
			Action(ctx, action, WithKeepState(), WithKeepSecrets()),
		},
	}
	contChecks.State.Set(workflow.State{})

	block := &workflow.Block{
		ID:            id,
		Name:          "block1",
		Descr:         "descr",
		EntranceDelay: 1 * time.Second,
		ExitDelay:     1 * time.Second,
		PreChecks:     preChecks,
		PostChecks:    postChecks,
		ContChecks:    contChecks,
		Sequences: []*workflow.Sequence{
			Sequence(ctx, sequence, WithKeepState(), WithKeepSecrets()),
		},
		Concurrency:       1,
		ToleratedFailures: 1,
	}
	block.State.Set(workflow.State{
		Status: workflow.Completed,
	})

	tests := []struct {
		name    string
		options cloneOptions
		block   *workflow.Block
		want    *workflow.Block
	}{
		{
			name: "nil",
		},
		{
			name:  "no options",
			block: block,
			want: &workflow.Block{
				Name:          "block1",
				Descr:         "descr",
				EntranceDelay: 1 * time.Second,
				ExitDelay:     1 * time.Second,
				PreChecks: &workflow.Checks{
					Actions: []*workflow.Action{
						actionSecretRemoved,
					},
				},
				PostChecks: &workflow.Checks{
					Actions: []*workflow.Action{
						actionSecretRemoved,
					},
				},
				ContChecks: &workflow.Checks{
					Actions: []*workflow.Action{
						actionSecretRemoved,
					},
				},
				Sequences: []*workflow.Sequence{
					Sequence(ctx, sequence, WithKeepState()),
				},
				Concurrency:       1,
				ToleratedFailures: 1,
			},
		},
		{
			name:    "WithKeepState(), WithKeepSecrets()",
			block:   block,
			options: cloneOptions{keepState: true, keepSecrets: true},
			want:    block,
		},
		{
			name:    "WithKeepState()",
			block:   block,
			options: cloneOptions{keepState: true},
			want: func() *workflow.Block {
				preChecksWant := &workflow.Checks{
					Actions: []*workflow.Action{
						actionSecretRemoved,
					},
				}
				preChecksWant.State.Set(workflow.State{})
				postChecksWant := &workflow.Checks{
					Actions: []*workflow.Action{
						actionSecretRemoved,
					},
				}
				postChecksWant.State.Set(workflow.State{})
				contChecksWant := &workflow.Checks{
					Actions: []*workflow.Action{
						actionSecretRemoved,
					},
				}
				contChecksWant.State.Set(workflow.State{})
				b := &workflow.Block{
					ID:            id,
					Name:          "block1",
					Descr:         "descr",
					EntranceDelay: 1 * time.Second,
					ExitDelay:     1 * time.Second,
					PreChecks:     preChecksWant,
					PostChecks:    postChecksWant,
					ContChecks:    contChecksWant,
					Sequences: []*workflow.Sequence{
						Sequence(ctx, sequence, WithKeepState()),
					},
					Concurrency:       1,
					ToleratedFailures: 1,
				}
				b.State.Set(workflow.State{
					Status: workflow.Completed,
				})
				return b
			}(),
		},
		{
			name:    "Without WithKeepSecrets(), but callNum > 0",
			block:   block,
			options: cloneOptions{keepSecrets: true, callNum: 1},
			want: &workflow.Block{
				Name:          "block1",
				Descr:         "descr",
				EntranceDelay: 1 * time.Second,
				ExitDelay:     1 * time.Second,
				PreChecks: &workflow.Checks{
					Actions: []*workflow.Action{
						action,
					},
				},
				PostChecks: &workflow.Checks{
					Actions: []*workflow.Action{
						action,
					},
				},
				ContChecks: &workflow.Checks{
					Actions: []*workflow.Action{
						action,
					},
				},
				Sequences: []*workflow.Sequence{
					Sequence(ctx, sequence, WithKeepState(), WithKeepSecrets()),
				},
				Concurrency:       1,
				ToleratedFailures: 1,
			},
		},
	}

	for _, test := range tests {
		got := Block(context.Background(), test.block, withOptions(test.options))

		if diff := pretty.Compare(test.want, got); diff != "" {
			t.Errorf("TestBlock(%s): -want/+got:\n%s", test.name, diff)
		}
	}
}

func TestChecks(t *testing.T) {
	t.Parallel()

	start := time.Now()
	id, err := uuid.NewV7()
	if err != nil {
		panic(err)
	}

	checks := &workflow.Checks{
		ID:    id,
		Delay: 1 * time.Second,
		Actions: []*workflow.Action{
			{
				Name: "action1",
				Req:  Req{Data: "Hello"},
			},
		},
	}
	checks.State.Set(workflow.State{
		Status: workflow.Completed,
		Start:  start,
	})

	tests := []struct {
		name    string
		options cloneOptions
		// replaceReq lets us replace the Req with something that has the secrets blanked out
		// instead of writing the entire action.
		replaceReq Req
		checks     *workflow.Checks
		want       *workflow.Checks
	}{
		{
			name: "nil",
		},
		{
			name:   "no options",
			checks: checks,
			want: &workflow.Checks{
				Delay: 1 * time.Second,
				Actions: []*workflow.Action{
					{
						Name: "action1",
						Req:  Req{Data: SecureStr},
					},
				},
			},
		},
		{
			name:    "WithKeepState(), WithKeepSecrets()",
			checks:  checks,
			options: cloneOptions{keepState: true, keepSecrets: true},
			want:    checks,
		},
		{
			name:       "WithKeepState()",
			checks:     checks,
			options:    cloneOptions{keepState: true},
			replaceReq: Req{Data: SecureStr},
			want:       checks,
		},
		{
			name:    "Without WithKeepSecrets(), but callNum > 0",
			checks:  checks,
			options: cloneOptions{keepSecrets: false, callNum: 1},
			want: &workflow.Checks{
				Delay: 1 * time.Second,
				Actions: []*workflow.Action{
					{
						Name: "action1",
						Req:  Req{"Hello"},
					},
				},
			},
		},
	}

	for _, test := range tests {
		got := Checks(context.Background(), test.checks, withOptions(test.options))

		var oldReq Req
		if test.want != nil {
			oldReq = test.want.Actions[0].Req.(Req)
			if test.replaceReq.Data != "" {
				test.want.Actions[0].Req = test.replaceReq
			}
		}

		if diff := pretty.Compare(test.want, got); diff != "" {
			t.Errorf("TestChecks(%s): -want/+got:\n%s", test.name, diff)
		}

		if test.want != nil {
			test.want.Actions[0].Req = oldReq
		}
	}

}

func TestSequence(t *testing.T) {
	t.Parallel()

	//start := time.Now()
	id, err := uuid.NewV7()
	if err != nil {
		panic(err)
	}

	sequence := &workflow.Sequence{
		ID:    id,
		Name:  "name",
		Descr: "descr",
		Actions: []*workflow.Action{
			{
				Name: "action1",
				Req:  Req{Data: "Hello"},
			},
		},
	}
	sequence.State.Set(workflow.State{
		Status: workflow.Completed,
	})

	tests := []struct {
		name    string
		options cloneOptions
		// replaceReq lets us replace the Req with something that has the secrets blanked out
		// instead of writing the entire action.
		replaceReq Req
		sequence   *workflow.Sequence
		want       *workflow.Sequence
	}{
		{
			name: "nil",
		},
		{
			name:     "no options",
			sequence: sequence,
			want: &workflow.Sequence{
				Name:  "name",
				Descr: "descr",
				Actions: []*workflow.Action{
					{
						Name: "action1",
						Req:  Req{Data: SecureStr},
					},
				},
			},
		},
		{
			name:     "WithKeepState(), WithKeepSecrets()",
			sequence: sequence,
			options:  cloneOptions{keepState: true, keepSecrets: true},
			want:     sequence,
		},
		{
			name:       "WithKeepState()",
			sequence:   sequence,
			options:    cloneOptions{keepState: true},
			replaceReq: Req{Data: SecureStr},
			want:       sequence,
		},
		{
			name:     "Without WithKeepSecrets(), but callNum > 0",
			sequence: sequence,
			options:  cloneOptions{callNum: 1},
			want: &workflow.Sequence{
				Name:  "name",
				Descr: "descr",
				Actions: []*workflow.Action{
					{
						Name: "action1",
						Req:  Req{"Hello"},
					},
				},
			},
		},
	}

	for _, test := range tests {
		got := Sequence(context.Background(), test.sequence, withOptions(test.options))

		var oldReq Req
		if test.want != nil {
			oldReq = test.want.Actions[0].Req.(Req)
			if test.replaceReq.Data != "" {
				test.want.Actions[0].Req = test.replaceReq
			}
		}

		if diff := pretty.Compare(test.want, got); diff != "" {
			t.Errorf("TestSequnce(%s): -want/+got:\n%s", test.name, diff)
		}

		if test.want != nil {
			test.want.Actions[0].Req = oldReq
		}
	}
}

func TestDeferBatch(t *testing.T) {
	t.Parallel()

	id, err := uuid.NewV7()
	if err != nil {
		panic(err)
	}

	batch := &workflow.DeferBatch{
		FailElement: true,
		Sequence: workflow.Sequence{
			ID:    id,
			Name:  "name",
			Descr: "descr",
			Actions: []*workflow.Action{
				{
					Name: "action1",
					Req:  Req{Data: "Hello"},
				},
			},
		},
	}
	batch.State.Set(workflow.State{Status: workflow.Completed})

	tests := []struct {
		name       string
		options    cloneOptions
		replaceReq Req
		batch      *workflow.DeferBatch
		want       *workflow.DeferBatch
	}{
		{
			name: "nil",
		},
		{
			name:  "Success: no options",
			batch: batch,
			want: &workflow.DeferBatch{
				FailElement: true,
				Sequence: workflow.Sequence{
					Name:  "name",
					Descr: "descr",
					Actions: []*workflow.Action{
						{
							Name: "action1",
							Req:  Req{Data: SecureStr},
						},
					},
				},
			},
		},
		{
			name:    "Success: WithKeepState(), WithKeepSecrets()",
			batch:   batch,
			options: cloneOptions{keepState: true, keepSecrets: true},
			want:    batch,
		},
		{
			name:       "Success: WithKeepState()",
			batch:      batch,
			options:    cloneOptions{keepState: true},
			replaceReq: Req{Data: SecureStr},
			want:       batch,
		},
		{
			name:    "Success: Without WithKeepSecrets(), but callNum > 0",
			batch:   batch,
			options: cloneOptions{callNum: 1},
			want: &workflow.DeferBatch{
				FailElement: true,
				Sequence: workflow.Sequence{
					Name:  "name",
					Descr: "descr",
					Actions: []*workflow.Action{
						{
							Name: "action1",
							Req:  Req{Data: "Hello"},
						},
					},
				},
			},
		},
	}

	for _, test := range tests {
		got := DeferBatch(context.Background(), test.batch, withOptions(test.options))

		var oldReq Req
		if test.want != nil {
			oldReq = test.want.Actions[0].Req.(Req)
			if test.replaceReq.Data != "" {
				test.want.Actions[0].Req = test.replaceReq
			}
		}

		if diff := pretty.Compare(test.want, got); diff != "" {
			t.Errorf("TestDeferBatch(%s): -want/+got:\n%s", test.name, diff)
		}

		if test.want != nil {
			test.want.Actions[0].Req = oldReq
		}
	}
}

func TestDeferredActions(t *testing.T) {
	t.Parallel()

	id, err := uuid.NewV7()
	if err != nil {
		panic(err)
	}

	failBatch := &workflow.DeferBatch{
		FailElement: true,
		Sequence: workflow.Sequence{
			Name:  "fail",
			Descr: "fail",
			Actions: []*workflow.Action{
				{Name: "fail_action", Req: Req{Data: "fail_secret"}},
			},
		},
	}
	successBatch := &workflow.DeferBatch{
		Sequence: workflow.Sequence{
			Name:  "success",
			Descr: "success",
			Actions: []*workflow.Action{
				{Name: "success_action", Req: Req{Data: "success_secret"}},
			},
		},
	}

	da := &workflow.DeferredActions{
		ID:        id,
		OnFailure: []*workflow.DeferBatch{failBatch},
		OnSuccess: []*workflow.DeferBatch{successBatch},
	}
	da.State.Set(workflow.State{Status: workflow.Completed})

	tests := []struct {
		name    string
		options cloneOptions
		da      *workflow.DeferredActions
		want    *workflow.DeferredActions
	}{
		{
			name: "nil",
		},
		{
			name: "Success: no options",
			da:   da,
			want: &workflow.DeferredActions{
				OnFailure: []*workflow.DeferBatch{
					{
						FailElement: true,
						Sequence: workflow.Sequence{
							Name:  "fail",
							Descr: "fail",
							Actions: []*workflow.Action{
								{Name: "fail_action", Req: Req{Data: SecureStr}},
							},
						},
					},
				},
				OnSuccess: []*workflow.DeferBatch{
					{
						Sequence: workflow.Sequence{
							Name:  "success",
							Descr: "success",
							Actions: []*workflow.Action{
								{Name: "success_action", Req: Req{Data: SecureStr}},
							},
						},
					},
				},
			},
		},
		{
			name:    "Success: WithKeepState(), WithKeepSecrets()",
			da:      da,
			options: cloneOptions{keepState: true, keepSecrets: true},
			want:    da,
		},
		{
			name:    "Success: Without WithKeepSecrets(), but callNum > 0",
			da:      da,
			options: cloneOptions{callNum: 1},
			want: &workflow.DeferredActions{
				OnFailure: []*workflow.DeferBatch{
					{
						FailElement: true,
						Sequence: workflow.Sequence{
							Name:  "fail",
							Descr: "fail",
							Actions: []*workflow.Action{
								{Name: "fail_action", Req: Req{Data: "fail_secret"}},
							},
						},
					},
				},
				OnSuccess: []*workflow.DeferBatch{
					{
						Sequence: workflow.Sequence{
							Name:  "success",
							Descr: "success",
							Actions: []*workflow.Action{
								{Name: "success_action", Req: Req{Data: "success_secret"}},
							},
						},
					},
				},
			},
		},
	}

	for _, test := range tests {
		got := DeferredActions(context.Background(), test.da, withOptions(test.options))
		if diff := pretty.Compare(test.want, got); diff != "" {
			t.Errorf("TestDeferredActions(%s): -want/+got:\n%s", test.name, diff)
		}
	}
}

func TestAction(t *testing.T) {
	t.Parallel()

	start := time.Now()
	id, err := uuid.NewV7()
	if err != nil {
		panic(err)
	}

	action := &workflow.Action{
		ID:     id,
		Name:   "name",
		Descr:  "descr",
		Plugin: "plugin",
		Req: Req{
			Data: "hello",
		},
		Timeout: 10 * time.Second,
		Retries: 2,
		Attempts: func() workflow.AtomicSlice[workflow.Attempt] {
			var s workflow.AtomicSlice[workflow.Attempt]
			s.Set(
				[]workflow.Attempt{
					{Start: start},
				},
			)
			return s
		}(),
	}
	action.State.Set(workflow.State{
		Status: workflow.Completed,
		Start:  start,
	})

	tests := []struct {
		name    string
		options cloneOptions
		action  *workflow.Action
		// replaceReq lets us replace the Req with something that has the secrets blanked out
		// instead of writing the entire action.
		replaceReq Req
		want       *workflow.Action
	}{
		{
			name: "nil",
		},
		{
			name:   "no options",
			action: action,
			want: &workflow.Action{
				Name:   "name",
				Descr:  "descr",
				Plugin: "plugin",
				Req: Req{
					Data: SecureStr,
				},
				Timeout: 10 * time.Second,
				Retries: 2,
			},
		},
		{
			name:    "WithKeepState(), WithKeepSecrets()",
			action:  action,
			options: cloneOptions{keepState: true, keepSecrets: true},
			want:    action,
		},
		{
			name:       "WithKeepState()",
			action:     action,
			options:    cloneOptions{keepState: true},
			replaceReq: Req{Data: SecureStr},
			want: func() *workflow.Action {
				a := &workflow.Action{
					ID:     id,
					Name:   "name",
					Descr:  "descr",
					Plugin: "plugin",
					Req: Req{
						Data: SecureStr,
					},
					Timeout: 10 * time.Second,
					Retries: 2,
					Attempts: func() workflow.AtomicSlice[workflow.Attempt] {
						var s workflow.AtomicSlice[workflow.Attempt]
						s.Set(
							[]workflow.Attempt{
								{Start: start},
							},
						)
						return s
					}(),
				}
				a.State.Set(workflow.State{
					Status: workflow.Completed,
					Start:  start,
				})
				return a
			}(),
		},
		{
			name:    "Without WithKeepSecrets(), but callNum > 0",
			action:  action,
			options: cloneOptions{callNum: 1},
			want: &workflow.Action{
				Name:   "name",
				Descr:  "descr",
				Plugin: "plugin",
				Req: Req{
					Data: "hello",
				},
				Timeout: 10 * time.Second,
				Retries: 2,
			},
		},
	}

	for _, test := range tests {
		got := Action(context.Background(), test.action, withOptions(test.options))

		var oldReq Req
		if test.want != nil {
			oldReq = test.want.Req.(Req)
			if test.replaceReq.Data != "" {
				test.want.Req = test.replaceReq
			}
		}

		if diff := pretty.Compare(test.want, got); diff != "" {
			t.Errorf("TestAction(%s): -want/+got:\n%s", test.name, diff)
		}
		if test.want != nil {
			test.want.Req = oldReq
		}
	}
}

func TestCloneStateAtomic(t *testing.T) {
	t.Parallel()

	start := time.Now()
	end := time.Now()

	tests := []struct {
		name  string
		state workflow.State
		want  workflow.State
	}{
		{
			name: "nil",
		},
		{
			name: "Success",
			state: workflow.State{
				Status: workflow.Completed,
				Start:  start,
				End:    end,
			},
			want: workflow.State{
				Status: workflow.Completed,
				Start:  start,
				End:    end,
			},
		},
	}

	for _, test := range tests {
		var src, dst workflow.AtomicValue[workflow.State]
		src.Set(test.state)
		cloneStateAtomic(&dst, &src)
		got := dst.Get()

		if diff := pretty.Compare(test.want, got); diff != "" {
			t.Errorf("TestCloneStateAtomic(%s): -want/+got:\n%s", test.name, diff)
		}
	}
}

func TestCloneAttempts(t *testing.T) {
	t.Parallel()

	type Resp struct {
		M map[string]any
		m map[string]any
	}

	start := time.Now()
	end := time.Now()

	tests := []struct {
		name     string
		attempts []workflow.Attempt
		want     []workflow.Attempt
	}{
		{
			name: "nil",
		},
		{
			name:     "len(0)",
			attempts: []workflow.Attempt{},
		},
		{
			name: "success",
			attempts: []workflow.Attempt{
				{
					Resp: Resp{
						M: map[string]any{
							"hello": 1,
							"world": 2,
						},
						m: map[string]any{
							"hello": &Resp{
								M: map[string]any{
									"hello": 2,
								},
							},
						},
					},
					Err: &plugins.Error{
						Code:      plugins.ErrCode(1),
						Message:   "not found",
						Permanent: true,
					},
					Start: start,
					End:   end,
				},
			},
			want: []workflow.Attempt{
				{
					Resp: Resp{
						M: map[string]any{
							"hello": 1,
							"world": 2,
						},
						m: map[string]any{
							"hello": &Resp{
								M: map[string]any{
									"hello": 2,
								},
							},
						},
					},
					Err: &plugins.Error{
						Code:      plugins.ErrCode(1),
						Message:   "not found",
						Permanent: true,
					},
					Start: start,
					End:   end,
				},
			},
		},
	}

	for _, test := range tests {
		got := cloneAttempts(test.attempts)

		if diff := pretty.Compare(test.want, got); diff != "" {
			t.Errorf("TestCloneAttempts(%s): -want/+got:\n%s", test.name, diff)
		}
	}
}

func TestCloneErr(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		e    *plugins.Error
		want *plugins.Error
	}{
		{
			name: "nil",
		},
		{
			name: "non-nested",
			e: &plugins.Error{
				Code:      plugins.ErrCode(1),
				Message:   "not found",
				Permanent: true,
			},
			want: &plugins.Error{
				Code:      plugins.ErrCode(1),
				Message:   "not found",
				Permanent: true,
			},
		},
		{
			name: "nested",
			e: &plugins.Error{
				Code:      plugins.ErrCode(1),
				Message:   "not found",
				Permanent: true,
				Wrapped: &plugins.Error{
					Code:      plugins.ErrCode(2),
					Message:   "not found 2",
					Permanent: false,
				},
			},
			want: &plugins.Error{
				Code:      plugins.ErrCode(1),
				Message:   "not found",
				Permanent: true,
				Wrapped: &plugins.Error{
					Code:      plugins.ErrCode(2),
					Message:   "not found 2",
					Permanent: false,
				},
			},
		},
	}

	for _, test := range tests {
		got := cloneErr(test.e)
		if diff := pretty.Compare(test.want, got); diff != "" {
			t.Errorf("TestCloneErr(%s): -want/+got:\n%s", test.name, diff)
		}
	}
}
