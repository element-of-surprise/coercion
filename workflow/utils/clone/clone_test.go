package clone

import (
	"testing"
	"time"

	"github.com/element-of-surprise/coercion/plugins"
	"github.com/element-of-surprise/coercion/workflow"
	"github.com/element-of-surprise/coercion/workflow/utils/walk"
	"github.com/google/uuid"

	"github.com/kylelemons/godebug/pretty"
)

type Req struct {
	Data string `coerce:"secure"`
}

func TestPlan(t *testing.T) {
	t.Parallel()

	start := time.Now()
	id, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("TestPlan: uuid.NewV7(): %s", err)
	}

	// Keys are user supplied references within a Plan, so every clone must keep them. Each object has its own
	// state and storage ETag, so the state check below sees which object's state a clone dropped.
	var (
		actionKey      = uuid.New()
		blockKey       = uuid.New()
		seqKey         = uuid.New()
		checksKey      = uuid.New()
		batchKey       = uuid.New()
		batchActionKey = uuid.New()
	)

	action := &workflow.Action{
		ID:   id,
		Key:  actionKey,
		Name: "action1",
		Req:  Req{Data: "Hello"},
	}
	action.State.Set(workflow.State{Status: workflow.Completed, Start: start, End: start, ETag: "action"})

	seq := &workflow.Sequence{
		ID:      id,
		Key:     seqKey,
		Name:    "seq1",
		Actions: []*workflow.Action{action},
	}
	seq.State.Set(workflow.State{Status: workflow.Completed, Start: start, End: start, ETag: "seq"})

	block := &workflow.Block{
		ID:        id,
		Key:       blockKey,
		Name:      "block1",
		Descr:     "descr",
		Sequences: []*workflow.Sequence{seq},
	}
	block.State.Set(workflow.State{Status: workflow.Completed, Start: start, End: start, ETag: "block"})

	checks := &workflow.Checks{
		ID:      id,
		Key:     checksKey,
		Actions: []*workflow.Action{action},
	}
	checks.State.Set(workflow.State{Status: workflow.Completed, Start: start, End: start, ETag: "checks"})

	batchAction := &workflow.Action{
		ID:   id,
		Key:  batchActionKey,
		Name: "fail_action",
		Req:  Req{Data: "Hello"},
	}
	batchAction.State.Set(workflow.State{Status: workflow.NotStarted, ETag: "batchAction"})

	batch := &workflow.DeferBatch{
		When:        workflow.OnFailure,
		FailElement: true,
		Sequence: workflow.Sequence{
			ID:      id,
			Key:     batchKey,
			Name:    "fail",
			Descr:   "fail",
			Actions: []*workflow.Action{batchAction},
		},
	}
	batch.State.Set(workflow.State{Status: workflow.NotStarted, ETag: "batch"})

	deferredActions := &workflow.DeferredActions{
		ID:              id,
		DeferredBatches: []*workflow.DeferBatch{batch},
	}
	deferredActions.State.Set(workflow.State{Status: workflow.NotStarted, ETag: "deferredActions"})

	plan := &workflow.Plan{
		ID:              id,
		Name:            "plan1",
		Descr:           "descr",
		GroupID:         id,
		Meta:            []byte("hello"),
		PreChecks:       checks,
		PostChecks:      checks,
		ContChecks:      checks,
		DeferredActions: deferredActions,
		Blocks:          []*workflow.Block{block},
		Reason:          workflow.FRBlock,
		SubmitTime:      start,
	}
	plan.State.Set(workflow.State{Status: workflow.Completed, Start: start, ETag: "plan"})
	plan.RuntimeUpdate.Set(start)

	// wantPlan builds the expected clone from literals, never from the clone functions under test, so a field one
	// of them drops shows up in the diff. IDs are kept only with keepState, and data is what an Action's secure Req
	// field should hold. State is not set here: pretty.Compare does not see inside an AtomicValue, so it is checked
	// apart through wantStates.
	wantPlan := func(keepState bool, data string) *workflow.Plan {
		var wantID uuid.UUID
		if keepState {
			wantID = id
		}
		wantChecks := func() *workflow.Checks {
			return &workflow.Checks{
				ID:      wantID,
				Key:     checksKey,
				Actions: []*workflow.Action{{ID: wantID, Key: actionKey, Name: "action1", Req: Req{Data: data}}},
			}
		}
		p := &workflow.Plan{
			Name:       "plan1",
			Descr:      "descr",
			GroupID:    id,
			Meta:       []byte("hello"),
			PreChecks:  wantChecks(),
			PostChecks: wantChecks(),
			ContChecks: wantChecks(),
			DeferredActions: &workflow.DeferredActions{
				ID: wantID,
				DeferredBatches: []*workflow.DeferBatch{
					{
						When:        workflow.OnFailure,
						FailElement: true,
						Sequence: workflow.Sequence{
							ID:      wantID,
							Key:     batchKey,
							Name:    "fail",
							Descr:   "fail",
							Actions: []*workflow.Action{{ID: wantID, Key: batchActionKey, Name: "fail_action", Req: Req{Data: data}}},
						},
					},
				},
			},
			Blocks: []*workflow.Block{
				{
					ID:    wantID,
					Key:   blockKey,
					Name:  "block1",
					Descr: "descr",
					Sequences: []*workflow.Sequence{
						{
							ID:      wantID,
							Key:     seqKey,
							Name:    "seq1",
							Actions: []*workflow.Action{{ID: wantID, Key: actionKey, Name: "action1", Req: Req{Data: data}}},
						},
					},
				},
			},
		}
		if keepState {
			p.ID = id
			p.Reason = workflow.FRBlock
			p.SubmitTime = start
		}
		return p
	}

	// states returns the state of every object in p in walk order.
	states := func(p *workflow.Plan) []workflow.State {
		var sl []workflow.State
		for item := range walk.Plan(p) {
			sl = append(sl, item.Value.(walk.Stateful).GetState())
		}
		return sl
	}
	// Every state, ETag included, is kept with keepState, and none is without it.
	keptStates := states(plan)
	droppedStates := make([]workflow.State, len(keptStates))

	tests := []struct {
		name    string
		options cloneOptions
		plan    *workflow.Plan
		want    *workflow.Plan
		// wantStates is the state of every object in the clone in walk order.
		wantStates []workflow.State
		// wantRuntimeUpdate is checked apart from want, as pretty.Compare does not see inside an AtomicValue.
		wantRuntimeUpdate time.Time
	}{
		{
			name: "Success: a nil Plan clones to nil",
		},
		{
			name:       "Success: with no options the clone keeps Keys but drops IDs and state and hides secrets",
			plan:       plan,
			want:       wantPlan(false, SecureStr),
			wantStates: droppedStates,
		},
		{
			name:              "Success: WithKeepState() and WithKeepSecrets() keep Keys, IDs, state, ETags, RuntimeUpdate and secrets",
			plan:              plan,
			options:           cloneOptions{keepState: true, keepSecrets: true},
			want:              wantPlan(true, "Hello"),
			wantStates:        keptStates,
			wantRuntimeUpdate: start,
		},
		{
			name:              "Success: WithKeepState() keeps Keys, IDs, state, ETags and RuntimeUpdate but hides secrets",
			plan:              plan,
			options:           cloneOptions{keepState: true},
			want:              wantPlan(true, SecureStr),
			wantStates:        keptStates,
			wantRuntimeUpdate: start,
		},
		{
			name:       "Success: without WithKeepSecrets() a nested clone (callNum > 0) leaves secrets for its caller to hide",
			plan:       plan,
			options:    cloneOptions{callNum: 1},
			want:       wantPlan(false, "Hello"),
			wantStates: droppedStates,
		},
	}

	for _, test := range tests {
		got := Plan(t.Context(), test.plan, withOptions(test.options))

		if diff := pretty.Compare(test.want, got); diff != "" {
			t.Errorf("TestPlan(%s): -want/+got:\n%s", test.name, diff)
		}
		if got == nil {
			continue
		}
		if diff := pretty.Compare(test.wantStates, states(got)); diff != "" {
			t.Errorf("TestPlan(%s): states: -want/+got:\n%s", test.name, diff)
		}
		if got, want := got.RuntimeUpdate.Get(), test.wantRuntimeUpdate; !got.Equal(want) {
			t.Errorf("TestPlan(%s): got RuntimeUpdate %v, want %v", test.name, got, want)
		}
	}
}

func TestBlock(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

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
			name: "Success: a nil Block clones to nil",
		},
		{
			name:  "Success: a Block is cloned with no options",
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
			name:    "Success: a Block is cloned with WithKeepState() and WithKeepSecrets()",
			block:   block,
			options: cloneOptions{keepState: true, keepSecrets: true},
			want:    block,
		},
		{
			name:    "Success: a Block is cloned with WithKeepState()",
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
			name:    "Success: a Block is cloned without WithKeepSecrets() from a nested call (callNum > 0)",
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
		got := Block(t.Context(), test.block, withOptions(test.options))

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
			name: "Success: a nil Checks clones to nil",
		},
		{
			name:   "Success: a Checks is cloned with no options",
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
			name:    "Success: a Checks is cloned with WithKeepState() and WithKeepSecrets()",
			checks:  checks,
			options: cloneOptions{keepState: true, keepSecrets: true},
			want:    checks,
		},
		{
			name:       "Success: a Checks is cloned with WithKeepState()",
			checks:     checks,
			options:    cloneOptions{keepState: true},
			replaceReq: Req{Data: SecureStr},
			want:       checks,
		},
		{
			name:    "Success: a Checks is cloned without WithKeepSecrets() from a nested call (callNum > 0)",
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
		got := Checks(t.Context(), test.checks, withOptions(test.options))

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
			name: "Success: a nil Sequence clones to nil",
		},
		{
			name:     "Success: a Sequence is cloned with no options",
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
			name:     "Success: a Sequence is cloned with WithKeepState() and WithKeepSecrets()",
			sequence: sequence,
			options:  cloneOptions{keepState: true, keepSecrets: true},
			want:     sequence,
		},
		{
			name:       "Success: a Sequence is cloned with WithKeepState()",
			sequence:   sequence,
			options:    cloneOptions{keepState: true},
			replaceReq: Req{Data: SecureStr},
			want:       sequence,
		},
		{
			name:     "Success: a Sequence is cloned without WithKeepSecrets() from a nested call (callNum > 0)",
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
		got := Sequence(t.Context(), test.sequence, withOptions(test.options))

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
			name: "Success: a nil DeferBatch clones to nil",
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
		got := DeferBatch(t.Context(), test.batch, withOptions(test.options))

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
		When:        workflow.OnFailure,
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
		When: workflow.OnSuccess,
		Sequence: workflow.Sequence{
			Name:  "success",
			Descr: "success",
			Actions: []*workflow.Action{
				{Name: "success_action", Req: Req{Data: "success_secret"}},
			},
		},
	}

	da := &workflow.DeferredActions{
		ID:              id,
		DeferredBatches: []*workflow.DeferBatch{failBatch, successBatch},
	}
	da.State.Set(workflow.State{Status: workflow.Completed})

	tests := []struct {
		name    string
		options cloneOptions
		da      *workflow.DeferredActions
		want    *workflow.DeferredActions
	}{
		{
			name: "Success: a nil DeferredActions clones to nil",
		},
		{
			name: "Success: no options",
			da:   da,
			want: &workflow.DeferredActions{
				DeferredBatches: []*workflow.DeferBatch{
					{
						When:        workflow.OnFailure,
						FailElement: true,
						Sequence: workflow.Sequence{
							Name:  "fail",
							Descr: "fail",
							Actions: []*workflow.Action{
								{Name: "fail_action", Req: Req{Data: SecureStr}},
							},
						},
					},
					{
						When: workflow.OnSuccess,
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
				DeferredBatches: []*workflow.DeferBatch{
					{
						When:        workflow.OnFailure,
						FailElement: true,
						Sequence: workflow.Sequence{
							Name:  "fail",
							Descr: "fail",
							Actions: []*workflow.Action{
								{Name: "fail_action", Req: Req{Data: "fail_secret"}},
							},
						},
					},
					{
						When: workflow.OnSuccess,
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
		got := DeferredActions(t.Context(), test.da, withOptions(test.options))
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
		// wantAttemptsSet is whether the clone's Attempts must be set. Compare cannot see this.
		wantAttemptsSet bool
	}{
		{
			name: "Success: a nil Action clones to nil",
		},
		{
			name:   "Success: a Action is cloned with no options",
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
			name:            "Success: a Action is cloned with WithKeepState() and WithKeepSecrets()",
			action:          action,
			options:         cloneOptions{keepState: true, keepSecrets: true},
			want:            action,
			wantAttemptsSet: true,
		},
		{
			name:    "Success: WithKeepState() leaves unset Attempts unset",
			action:  &workflow.Action{ID: id, Name: "name", Req: Req{Data: "hello"}},
			options: cloneOptions{keepState: true, keepSecrets: true},
			want:    &workflow.Action{ID: id, Name: "name", Req: Req{Data: "hello"}},
		},
		{
			name: "Success: WithKeepState() keeps Attempts set to an empty slice set",
			action: func() *workflow.Action {
				a := &workflow.Action{ID: id, Name: "name", Req: Req{Data: "hello"}}
				a.Attempts.Set([]workflow.Attempt{})
				return a
			}(),
			options:         cloneOptions{keepState: true, keepSecrets: true},
			want:            &workflow.Action{ID: id, Name: "name", Req: Req{Data: "hello"}},
			wantAttemptsSet: true,
		},
		{
			name:            "Success: a Action is cloned with WithKeepState()",
			action:          action,
			options:         cloneOptions{keepState: true},
			replaceReq:      Req{Data: SecureStr},
			wantAttemptsSet: true,
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
			name:    "Success: a Action is cloned without WithKeepSecrets() from a nested call (callNum > 0)",
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
		got := Action(t.Context(), test.action, withOptions(test.options))

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
		if got != nil && got.Attempts.IsSet() != test.wantAttemptsSet {
			t.Errorf("TestAction(%s): got Attempts.IsSet() == %v, want %v", test.name, got.Attempts.IsSet(), test.wantAttemptsSet)
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
		name string
		// unset leaves the source unset instead of setting it to state.
		unset   bool
		state   workflow.State
		want    workflow.State
		wantSet bool
	}{
		{
			name:  "Success: an unset state stays unset",
			unset: true,
		},
		{
			name:    "Success: a state set to the zero value stays set",
			wantSet: true,
		},
		{
			name:    "Success: a set state is copied",
			wantSet: true,
			state: workflow.State{
				Status: workflow.Completed,
				Start:  start,
				End:    end,
				ETag:   "etag",
			},
			want: workflow.State{
				Status: workflow.Completed,
				Start:  start,
				End:    end,
				ETag:   "etag",
			},
		},
	}

	for _, test := range tests {
		var src, dst workflow.AtomicValue[workflow.State]
		if !test.unset {
			src.Set(test.state)
		}
		cloneStateAtomic(&dst, &src)
		got := dst.Get()

		if diff := pretty.Compare(test.want, got); diff != "" {
			t.Errorf("TestCloneStateAtomic(%s): -want/+got:\n%s", test.name, diff)
		}
		if dst.IsSet() != test.wantSet {
			t.Errorf("TestCloneStateAtomic(%s): got IsSet() == %v, want %v", test.name, dst.IsSet(), test.wantSet)
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
			name: "Success: nil attempts clone to nil",
		},
		{
			name:     "Success: empty attempts are cloned",
			attempts: []workflow.Attempt{},
		},
		{
			name: "Success: attempts with nested responses are deep copied",
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
			name: "Success: a nil error clones to nil",
		},
		{
			name: "Success: an error is cloned",
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
			name: "Success: an error is cloned with the error it wraps",
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
