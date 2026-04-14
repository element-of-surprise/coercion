package builder

import (
	"testing"

	"github.com/element-of-surprise/coercion/workflow"

	"github.com/kylelemons/godebug/pretty"
)

var pConfig = pretty.Config{
	IncludeUnexported: false,
	PrintStringers:    true,
}

func TestUp(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		bp     *BuildPlan
		expect []any
		err    bool
	}{
		{
			name:   "Error: Nothing to move up from",
			bp:     &BuildPlan{},
			expect: nil,
			err:    true,
		},
		{
			name: "Error: Only a single item",
			bp: &BuildPlan{
				chain: []any{&workflow.Plan{}},
			},
			expect: nil,
			err:    true,
		},
		{
			name: "Success",
			bp: &BuildPlan{
				chain: []any{&workflow.Plan{}, &workflow.Block{}},
			},
			expect: []any{&workflow.Plan{}},
		},
	}

	for _, test := range tests {
		test.bp.Up()
		err := test.bp.Err()
		switch {
		case test.err && err == nil:
			t.Errorf("TestUp(%s): got err == nil, want err != nil", test.name)
			continue
		case !test.err && err != nil:
			t.Errorf("TestUp(%s): got err != %s, want err == nil", test.name, err)
			continue
		case err != nil:
			continue
		}

		if diff := pConfig.Compare(test.expect, test.bp.chain); diff != "" {
			t.Errorf("TestUp(%s): -want/+got:\n%s", test.name, diff)
		}
	}
}

func TestPlan(t *testing.T) {
	t.Parallel()

	bp, err := New("name", "description")
	if err != nil {
		panic(err)
	}

	plan, err := bp.Plan()
	if err != nil {
		t.Fatalf("TestPlan: got err != nil, want err == nil: %s", err)
	}
	if plan == nil {
		t.Fatalf("TestPlan: got nil, want non-nil")
	}

	if !bp.emitted {
		t.Fatalf("TestPlan: .emitted: got false, want true")
	}
}

func TestReset(t *testing.T) {
	t.Parallel()

	emptyName := &workflow.Plan{Name: "", Descr: ""}
	emptyDesc := &workflow.Plan{Name: "name", Descr: ""}
	oldPlan := &workflow.Plan{Name: "old"}
	newPlan := &workflow.Plan{Name: "new", Descr: "new"}

	tests := []struct {
		name     string
		argsName string
		argsDesc string
		bp       *BuildPlan
		want     *BuildPlan
		err      bool
	}{
		{
			name: "Error: name is empty",
			bp:   &BuildPlan{chain: []any{emptyName}},
			err:  true,
		},
		{
			name: "Error: description is empty",
			bp:   &BuildPlan{chain: []any{emptyDesc}},
			err:  true,
		},
		{
			name:     "Success",
			argsName: "new",
			argsDesc: "new",
			bp: &BuildPlan{
				emitted: true,
				chain:   []any{oldPlan},
			},
			want: &BuildPlan{
				chain: []any{newPlan},
			},
		},
	}

	for _, test := range tests {
		err := test.bp.Reset(test.argsName, test.argsDesc)

		switch {
		case test.err && err == nil:
			t.Errorf("TestReset(%s): got err == nil, want err != nil", test.name)
			continue
		case !test.err && err != nil:
			t.Errorf("TestReset(%s): got err != %s, want err == nil", test.name, err)
			continue
		case err != nil:
			continue
		}

		if diff := pConfig.Compare(test.want, test.bp); diff != "" {
			t.Errorf("TestReset(%s): -want/+got:\n%s", test.name, diff)
		}
	}
}

func TestAddChecks(t *testing.T) {
	t.Parallel()

	wantContChecks := &workflow.Checks{Actions: []*workflow.Action{{}}}
	wantBlock := &workflow.Block{
		ContChecks: wantContChecks,
	}

	tests := []struct {
		name   string
		bp     func() *BuildPlan
		checks *workflow.Checks
		want   *BuildPlan
		err    bool
	}{
		{
			name: "Error: already emitted",
			bp: func() *BuildPlan {
				return &BuildPlan{emitted: true, chain: []any{&workflow.Plan{}}}
			},
			err: true,
		},
		{
			name: "Error: checks is nil",
			err:  true,
		},
		{
			name: "Error: current() is not a Plan or Block",
			bp: func() *BuildPlan {
				return &BuildPlan{chain: []any{&workflow.Action{}}}
			},
			checks: &workflow.Checks{Actions: []*workflow.Action{{}}},
			err:    true,
		},
		{
			name: "Success: Plan",
			bp: func() *BuildPlan {
				return &BuildPlan{chain: []any{&workflow.Plan{}}}
			},
			checks: &workflow.Checks{Actions: []*workflow.Action{{}}},
			want: &BuildPlan{
				chain: []any{
					&workflow.Plan{
						ContChecks: wantContChecks,
					},
				},
			},
		},
		{
			name: "Success: Block",
			bp: func() *BuildPlan {
				block := &workflow.Block{}
				return &BuildPlan{
					chain: []any{
						&workflow.Plan{
							Blocks: []*workflow.Block{block},
						},
						block,
					},
				}
			},
			checks: &workflow.Checks{Actions: []*workflow.Action{{}}},
			want: &BuildPlan{
				chain: []any{
					&workflow.Plan{
						Blocks: []*workflow.Block{wantBlock},
					},
					wantBlock,
				},
			},
		},
	}

	for _, test := range tests {
		if test.bp == nil {
			test.bp = func() *BuildPlan {
				return &BuildPlan{
					chain: []any{&workflow.Plan{}},
				}
			}
		}
		bp := test.bp()
		bp.AddChecks(ContChecks, test.checks)
		err := bp.Err()

		switch {
		case test.err && err == nil:
			t.Errorf("TestAddChecks(%s): got err == nil, want err != nil", test.name)
			continue
		case !test.err && err != nil:
			t.Errorf("TestAddChecks(%s): got err != %s, want err == nil", test.name, err)
			continue
		case err != nil:
			continue
		}

		if diff := pConfig.Compare(test.want, bp); diff != "" {
			t.Errorf("TestAddChecks(%s): -want/+got:\n%s", test.name, diff)
		}
	}
}

// TestAddBypassChecks simply tests that AddChecks() adds Bypass checks where they should go.
// TestAddChecks checks Cont checks and all the rest of the logic.
func TestAddBypassChecks(t *testing.T) {
	wantCheck0 := &workflow.Checks{Actions: []*workflow.Action{{Name: "check0"}}}

	builder, err := New("test", "test")
	if err != nil {
		panic(err)
	}

	builder.AddChecks(BypassChecks, wantCheck0).Up()
	builder.AddBlock(BlockArgs{Name: "test", Descr: "test", Concurrency: 1})
	builder.AddChecks(BypassChecks, wantCheck0).Up()

	got, err := builder.Plan()
	if err != nil {
		t.Fatalf("TestAddBypassChecks(builer.Plan()): unexpected error: %v", err)
	}

	if got.BypassChecks.Actions[0].Name != "check0" {
		t.Errorf("TestAddBypassChecks(Plan.BypassChecks): got %s, want check0", got.BypassChecks.Actions[0].Name)
	}
	if got.Blocks[0].BypassChecks.Actions[0].Name != "check0" {
		t.Errorf("TestAddBypassChecks(Block.BypassChecks): got %s, want check0", got.Blocks[0].BypassChecks.Actions[0].Name)
	}
}

// TestAddPrePostChecks simply tests that AddChecks() adds Pre and Post checks where they should go.
// TestAddChecks checks Cont checks and all the rest of the logic.
func TestAddPrePostChecks(t *testing.T) {
	wantCheck0 := &workflow.Checks{Actions: []*workflow.Action{{Name: "check0"}}}
	wantCheck1 := &workflow.Checks{Actions: []*workflow.Action{{Name: "check1"}}}

	builder, err := New("test", "test")
	if err != nil {
		panic(err)
	}

	builder.AddChecks(PreChecks, wantCheck0).Up()
	builder.AddChecks(PostChecks, wantCheck1).Up()
	builder.AddBlock(BlockArgs{Name: "test", Descr: "test", Concurrency: 1})
	builder.AddChecks(PreChecks, wantCheck0).Up()
	builder.AddChecks(PostChecks, wantCheck1).Up()

	got, err := builder.Plan()
	if err != nil {
		t.Fatalf("TestAddPrePostChecks(builer.Plan()): unexpected error: %v", err)
	}

	if got.PreChecks.Actions[0].Name != "check0" {
		t.Errorf("TestAddPrePostChecks(Plan.PreChecks): got %s, want check0", got.PreChecks.Actions[0].Name)
	}
	if got.PostChecks.Actions[0].Name != "check1" {
		t.Errorf("TestAddPrePostChecks(Plan.PostChecks): got %s, want check1", got.PostChecks.Actions[0].Name)
	}
	if got.Blocks[0].PreChecks.Actions[0].Name != "check0" {
		t.Errorf("TestAddPrePostChecks(Block.PreChecks): got %s, want check0", got.Blocks[0].PreChecks.Actions[0].Name)
	}
	if got.Blocks[0].PostChecks.Actions[0].Name != "check1" {
		t.Errorf("TestAddPrePostChecks(Block.PostChecks): got %s, want check1", got.Blocks[0].PostChecks.Actions[0].Name)
	}
}

// TestAddDeferredChecks simply tests that AddChecks() adds Deferred checks where they should go.
func TestAddDeferredChecks(t *testing.T) {
	wantCheck0 := &workflow.Checks{Actions: []*workflow.Action{{Name: "check0"}}}

	builder, err := New("test", "test")
	if err != nil {
		panic(err)
	}

	builder.AddChecks(DeferredChecks, wantCheck0).Up()
	builder.AddBlock(BlockArgs{Name: "test", Descr: "test", Concurrency: 1})
	builder.AddChecks(DeferredChecks, wantCheck0).Up()

	got, err := builder.Plan()
	if err != nil {
		t.Fatalf("TestAddDeferredChecks(builer.Plan()): unexpected error: %v", err)
	}

	if got.DeferredChecks.Actions[0].Name != "check0" {
		t.Errorf("TestAddDeferredChecks(Plan.DeferredChecks): got %s, want check0", got.DeferredChecks.Actions[0].Name)
	}
	if got.Blocks[0].DeferredChecks.Actions[0].Name != "check0" {
		t.Errorf("TestAddDeferredChecks(Block.DeferredChecks): got %s, want check0", got.Blocks[0].DeferredChecks.Actions[0].Name)
	}
}

// TestAddDeferredActions tests that AddDeferredActions and AddDeferBatch attach
// a DeferredActions to the Plan and populate the OnSuccess/OnFailure batches.
func TestAddDeferredActions(t *testing.T) {
	failAction := &workflow.Action{Name: "cleanup", Descr: "cleanup", Plugin: "p"}
	successAction := &workflow.Action{Name: "log", Descr: "log", Plugin: "p"}

	builder, err := New("test", "test")
	if err != nil {
		panic(err)
	}

	builder.AddDeferredActions()
	builder.AddDeferBatch(OnFailure, &workflow.DeferBatch{
		FailElement: true,
		Sequence:    workflow.Sequence{Name: "cleanup", Descr: "rollback state"},
	})
	builder.AddAction(failAction)
	builder.Up()
	builder.AddDeferBatch(OnSuccess, &workflow.DeferBatch{
		Sequence: workflow.Sequence{Name: "log", Descr: "log success"},
	})
	builder.AddAction(successAction)
	builder.Up()
	builder.Up()
	builder.AddBlock(BlockArgs{Name: "b", Descr: "b", Concurrency: 1})
	builder.AddSequence(&workflow.Sequence{Name: "s", Descr: "s"})
	builder.AddAction(&workflow.Action{Name: "a", Descr: "a", Plugin: "p"})

	got, err := builder.Plan()
	if err != nil {
		t.Fatalf("TestAddDeferredActions(builder.Plan()): unexpected error: %v", err)
	}

	if got.DeferredActions == nil {
		t.Fatalf("TestAddDeferredActions: got.DeferredActions == nil, want non-nil")
	}
	if len(got.DeferredActions.OnFailure) != 1 {
		t.Fatalf("TestAddDeferredActions(OnFailure): got len %d, want 1", len(got.DeferredActions.OnFailure))
	}
	if !got.DeferredActions.OnFailure[0].FailElement {
		t.Errorf("TestAddDeferredActions(OnFailure[0].FailElement): got false, want true")
	}
	if got.DeferredActions.OnFailure[0].Name != "cleanup" {
		t.Errorf("TestAddDeferredActions(OnFailure[0].Name): got %s, want cleanup", got.DeferredActions.OnFailure[0].Name)
	}
	if len(got.DeferredActions.OnFailure[0].Actions) != 1 || got.DeferredActions.OnFailure[0].Actions[0].Name != "cleanup" {
		t.Errorf("TestAddDeferredActions(OnFailure[0].Actions): missing or wrong action")
	}
	if len(got.DeferredActions.OnSuccess) != 1 {
		t.Fatalf("TestAddDeferredActions(OnSuccess): got len %d, want 1", len(got.DeferredActions.OnSuccess))
	}
	if got.DeferredActions.OnSuccess[0].Name != "log" {
		t.Errorf("TestAddDeferredActions(OnSuccess[0].Name): got %s, want log", got.DeferredActions.OnSuccess[0].Name)
	}
	if len(got.DeferredActions.OnSuccess[0].Actions) != 1 || got.DeferredActions.OnSuccess[0].Actions[0].Name != "log" {
		t.Errorf("TestAddDeferredActions(OnSuccess[0].Actions): missing or wrong action")
	}
}

// TestAddDeferredActionsErrors covers the error branches of AddDeferredActions
// and AddDeferBatch.
func TestAddDeferredActionsErrors(t *testing.T) {
	goodBatch := func() *workflow.DeferBatch {
		return &workflow.DeferBatch{
			Sequence: workflow.Sequence{Name: "s", Descr: "s"},
		}
	}

	tests := []struct {
		name string
		run  func(*BuildPlan)
	}{
		{
			name: "Error: AddDeferredActions outside Plan scope",
			run: func(bp *BuildPlan) {
				bp.AddBlock(BlockArgs{Name: "b", Descr: "b", Concurrency: 1})
				bp.AddDeferredActions()
			},
		},
		{
			name: "Error: duplicate AddDeferredActions",
			run: func(bp *BuildPlan) {
				bp.AddDeferredActions()
				bp.Up()
				bp.AddDeferredActions()
			},
		},
		{
			name: "Error: AddDeferBatch outside DeferredActions scope",
			run: func(bp *BuildPlan) {
				bp.AddDeferBatch(OnFailure, goodBatch())
			},
		},
		{
			name: "Error: AddDeferBatch with nil batch",
			run: func(bp *BuildPlan) {
				bp.AddDeferredActions()
				bp.AddDeferBatch(OnSuccess, nil)
			},
		},
		{
			name: "Error: AddDeferBatch with unknown condition",
			run: func(bp *BuildPlan) {
				bp.AddDeferredActions()
				bp.AddDeferBatch(DCUnknown, goodBatch())
			},
		},
		{
			name: "Error: AddDeferBatch with empty Name",
			run: func(bp *BuildPlan) {
				bp.AddDeferredActions()
				bp.AddDeferBatch(OnSuccess, &workflow.DeferBatch{
					Sequence: workflow.Sequence{Descr: "s"},
				})
			},
		},
		{
			name: "Error: AddDeferBatch with empty Descr",
			run: func(bp *BuildPlan) {
				bp.AddDeferredActions()
				bp.AddDeferBatch(OnSuccess, &workflow.DeferBatch{
					Sequence: workflow.Sequence{Name: "s"},
				})
			},
		},
	}

	for _, test := range tests {
		bp, err := New("test", "test")
		if err != nil {
			panic(err)
		}
		test.run(bp)
		if bp.Err() == nil {
			t.Errorf("TestAddDeferredActionsErrors(%s): got err == nil, want err != nil", test.name)
		}
	}
}

func TestAddBlock(t *testing.T) {
	t.Parallel()

	goodArgs := BlockArgs{
		Name:  "test",
		Descr: "test",
	}

	wantBlock := &workflow.Block{Name: "test", Descr: "test"}

	tests := []struct {
		name string
		args BlockArgs
		bp   func() *BuildPlan
		want *BuildPlan
		err  bool
	}{
		{
			name: "Error: already emitted",
			args: goodArgs,
			bp: func() *BuildPlan {
				return &BuildPlan{emitted: true, chain: []any{&workflow.Plan{}}}
			},
			err: true,
		},
		{
			name: "Error: current() is not a Plan",
			args: goodArgs,
			bp: func() *BuildPlan {
				return &BuildPlan{chain: []any{&workflow.Action{}}}
			},
			err: true,
		},
		{
			name: "Error: no name",
			args: BlockArgs{Descr: "test"},
			err:  true,
		},
		{
			name: "Error: no description",
			args: BlockArgs{Name: "test"},
			err:  true,
		},
		{
			name: "Success",
			args: goodArgs,
			bp: func() *BuildPlan {
				return &BuildPlan{chain: []any{&workflow.Plan{}}}
			},
			want: &BuildPlan{
				chain: []any{
					&workflow.Plan{
						Blocks: []*workflow.Block{wantBlock},
					},
					wantBlock,
				},
			},
		},
	}

	for _, test := range tests {
		if test.bp == nil {
			test.bp = func() *BuildPlan {
				return &BuildPlan{
					chain: []any{&workflow.Plan{}},
				}
			}
		}
		bp := test.bp()
		bp.AddBlock(test.args)
		err := bp.Err()

		switch {
		case test.err && err == nil:
			t.Errorf("TestAddBlock(%s): got err == nil, want err != nil", test.name)
			continue
		case !test.err && err != nil:
			t.Errorf("TestAddBlock(%s): got err != %s, want err == nil", test.name, err)
			continue
		case err != nil:
			continue
		}

		if diff := pConfig.Compare(test.want, bp); diff != "" {
			t.Errorf("TestAddBlock(%s): -want/+got:\n%s", test.name, diff)
		}
	}
}

func TestAddSequence(t *testing.T) {
	t.Parallel()

	newPlan := func() *workflow.Plan {
		block := &workflow.Block{}
		return &workflow.Plan{Blocks: []*workflow.Block{block}}
	}

	tests := []struct {
		name    string
		argName string
		descr   string
		bp      func() *BuildPlan
		want    func() *BuildPlan
		err     bool
	}{
		{
			name:    "Error: already emitted",
			argName: "test",
			descr:   "test",
			bp: func() *BuildPlan {
				p := newPlan()
				return &BuildPlan{emitted: true, chain: []any{p, p.Blocks[0]}}
			},
			err: true,
		},
		{
			name:  "Error: name is empty",
			descr: "test",
			bp: func() *BuildPlan {
				p := newPlan()
				return &BuildPlan{chain: []any{p, p.Blocks[0]}}
			},
			err: true,
		},
		{
			name:    "Error: description is empty",
			argName: "test",
			bp: func() *BuildPlan {
				p := newPlan()
				return &BuildPlan{chain: []any{p, p.Blocks[0]}}
			},
			err: true,
		},
		{
			name:    "Error: current() is not a Block",
			argName: "test",
			descr:   "test",
			bp: func() *BuildPlan {
				return &BuildPlan{chain: []any{&workflow.Action{}}}
			},
			err: true,
		},
		{
			name:    "Success",
			argName: "test",
			descr:   "test",
			bp: func() *BuildPlan {
				p := newPlan()
				return &BuildPlan{chain: []any{p, p.Blocks[0]}}
			},
			want: func() *BuildPlan {
				p := &workflow.Plan{}
				seq := &workflow.Sequence{Name: "test", Descr: "test"}
				block := &workflow.Block{}
				block.Sequences = append(block.Sequences, seq)
				p.Blocks = append(p.Blocks, block)
				return &BuildPlan{chain: []any{p, block, seq}}
			},
		},
	}

	for _, test := range tests {
		bp := test.bp()
		bp.AddSequence(&workflow.Sequence{Name: test.argName, Descr: test.descr})
		err := bp.Err()

		switch {
		case test.err && err == nil:
			t.Errorf("TestAddSequence(%s): got err == nil, want err != nil", test.name)
			continue
		case !test.err && err != nil:
			t.Errorf("TestAddSequence(%s): got err != %s, want err == nil", test.name, err)
			continue
		case err != nil:
			continue
		}

		if diff := pConfig.Compare(test.want(), bp); diff != "" {
			t.Errorf("TestAddSequence(%s): -want/+got:\n%s", test.name, diff)
		}
	}
}

func TestAddAction(t *testing.T) {
	t.Parallel()

	newPlan := func() *workflow.Plan {
		seq := &workflow.Sequence{}
		block := &workflow.Block{Sequences: []*workflow.Sequence{seq}}
		return &workflow.Plan{Blocks: []*workflow.Block{block}}
	}

	tests := []struct {
		name   string
		action *workflow.Action
		bp     func() *BuildPlan
		want   func() *BuildPlan
		err    bool
	}{
		{
			name:   "Error: already emitted",
			action: &workflow.Action{Name: "test", Descr: "test", Plugin: "plugin"},
			bp: func() *BuildPlan {
				p := newPlan()
				return &BuildPlan{emitted: true, chain: []any{p, p.Blocks[0], p.Blocks[0].Sequences[0]}}
			},
			err: true,
		},
		{
			name:   "Error: name is empty",
			action: &workflow.Action{Descr: "test", Plugin: "plugin"},
			bp: func() *BuildPlan {
				p := newPlan()
				return &BuildPlan{chain: []any{p, p.Blocks[0], p.Blocks[0].Sequences[0]}}
			},
			err: true,
		},
		{
			name:   "Error: description is empty",
			action: &workflow.Action{Name: "test", Plugin: "plugin"},
			bp: func() *BuildPlan {
				p := newPlan()
				return &BuildPlan{chain: []any{p, p.Blocks[0], p.Blocks[0].Sequences[0]}}
			},
			err: true,
		},
		{
			name:   "Error: plugin is empty",
			action: &workflow.Action{Name: "test", Descr: "test"},
			bp: func() *BuildPlan {
				p := newPlan()
				return &BuildPlan{chain: []any{p, p.Blocks[0], p.Blocks[0].Sequences[0]}}
			},
			err: true,
		},
		{
			name:   "Error: current() is not a Sequence",
			action: &workflow.Action{Name: "test", Descr: "test", Plugin: "plugin"},
			bp: func() *BuildPlan {
				return &BuildPlan{chain: []any{&workflow.Action{}}}
			},
			err: true,
		},
		{
			name:   "Success",
			action: &workflow.Action{Name: "test", Descr: "test", Plugin: "plugin"},
			bp: func() *BuildPlan {
				p := newPlan()
				return &BuildPlan{chain: []any{p, p.Blocks[0], p.Blocks[0].Sequences[0]}}
			},
			want: func() *BuildPlan {
				p := newPlan()
				action := &workflow.Action{Name: "test", Descr: "test"}
				p.Blocks[0].Sequences[0].Actions = append(p.Blocks[0].Sequences[0].Actions, action)
				return &BuildPlan{chain: []any{p, p.Blocks[0], p.Blocks[0].Sequences[0]}}
			},
		},
	}

	for _, test := range tests {
		bp := test.bp()
		bp.AddAction(test.action)
		err := bp.Err()

		switch {
		case test.err && err == nil:
			t.Errorf("TestAddAction(%s): got err == nil, want err != nil", test.name)
			continue
		case !test.err && err != nil:
			t.Errorf("TestAddAction(%s): got err != %s, want err == nil", test.name, err)
			continue
		case err != nil:
			continue
		}

		if diff := pConfig.Compare(test.want(), bp); diff != "" {
			t.Errorf("TestAddJob(%s): -want/+got:\n%s", test.name, diff)
		}
	}
}
