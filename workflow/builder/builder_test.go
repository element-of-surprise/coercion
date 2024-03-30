package builder

import (
	"testing"

	"github.com/element-of-surprise/workstream/workflow"

	"github.com/kylelemons/godebug/pretty"
)

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
		err := test.bp.Up()
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

		if diff := pretty.Compare(test.expect, test.bp.chain); diff != "" {
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

	plan := bp.Plan()
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

		if diff := pretty.Compare(test.want, test.bp); diff != "" {
			t.Errorf("TestReset(%s): -want/+got:\n%s", test.name, diff)
		}
	}
}

func TestAddPreChecks(t *testing.T) {
	t.Parallel()

	wantPreCheck := &workflow.PreChecks{Actions: []*workflow.Action{{}}}
	wantBlock := &workflow.Block{
		PreChecks: wantPreCheck,
	}

	tests := []struct {
		name    string
		bp      func() *BuildPlan
		actions []*workflow.Action
		want    *BuildPlan
		err     bool
	}{
		{
			name: "Error: already emitted",
			bp: func() *BuildPlan {
				return &BuildPlan{emitted: true, chain: []any{&workflow.Plan{}}}
			},
			err: true,
		},
		{
			name: "Error: no actions",
			err:  true,
		},
		{
			name:    "Error: an Action was nil",
			actions: []*workflow.Action{{}, nil},
			err:     true,
		},
		{
			name: "Error: current() is not a Plan or Block",
			bp: func() *BuildPlan {
				return &BuildPlan{chain: []any{&workflow.Action{}}}
			},
			actions: []*workflow.Action{{}},
			err:     true,
		},
		{
			name: "Success: Plan",
			bp: func() *BuildPlan {
				return &BuildPlan{chain: []any{&workflow.Plan{}}}
			},
			actions: []*workflow.Action{{}},
			want: &BuildPlan{
				chain: []any{
					&workflow.Plan{
						PreChecks: wantPreCheck,
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
			actions: []*workflow.Action{{}},
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
		err := bp.AddPreChecks(test.actions...)

		switch {
		case test.err && err == nil:
			t.Errorf("TestAddPreChecks(%s): got err == nil, want err != nil", test.name)
			continue
		case !test.err && err != nil:
			t.Errorf("TestAddPreChecks(%s): got err != %s, want err == nil", test.name, err)
			continue
		case err != nil:
			continue
		}

		if diff := pretty.Compare(test.want, bp); diff != "" {
			t.Errorf("TestAddPreChecks(%s): -want/+got:\n%s", test.name, diff)
		}
	}
}

func TestAddPostChecks(t *testing.T) {
	t.Parallel()

	wantPostChecks := &workflow.PostChecks{Actions: []*workflow.Action{{}}}
	wantBlock := &workflow.Block{
		PostChecks: wantPostChecks,
	}

	tests := []struct {
		name    string
		bp      func() *BuildPlan
		actions []*workflow.Action
		want    *BuildPlan
		err     bool
	}{
		{
			name: "Error: already emitted",
			bp: func() *BuildPlan {
				return &BuildPlan{emitted: true, chain: []any{&workflow.Plan{}}}
			},
			err: true,
		},
		{
			name: "Error: no actions",
			err:  true,
		},
		{
			name:    "Error: an Action was nil",
			actions: []*workflow.Action{{}, nil},
			err:     true,
		},
		{
			name: "Error: current() is not a Plan or Block",
			bp: func() *BuildPlan {
				return &BuildPlan{chain: []any{&workflow.Action{}}}
			},
			actions: []*workflow.Action{{}},
			err:     true,
		},
		{
			name: "Success: Plan",
			bp: func() *BuildPlan {
				return &BuildPlan{chain: []any{&workflow.Plan{}}}
			},
			actions: []*workflow.Action{{}},
			want: &BuildPlan{
				chain: []any{
					&workflow.Plan{
						PostChecks: wantPostChecks,
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
			actions: []*workflow.Action{{}},
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
		err := bp.AddPostChecks(test.actions...)

		switch {
		case test.err && err == nil:
			t.Errorf("TestAddPostChecks(%s): got err == nil, want err != nil", test.name)
			continue
		case !test.err && err != nil:
			t.Errorf("TestAddPostChecks(%s): got err != %s, want err == nil", test.name, err)
			continue
		case err != nil:
			continue
		}

		if diff := pretty.Compare(test.want, bp); diff != "" {
			t.Errorf("TestAddPostChecks(%s): -want/+got:\n%s", test.name, diff)
		}
	}
}

func TestAddContChecks(t *testing.T) {
	t.Parallel()

	wantContChecks := &workflow.ContChecks{Actions: []*workflow.Action{{}}}
	wantBlock := &workflow.Block{
		ContChecks: wantContChecks,
	}

	tests := []struct {
		name    string
		bp      func() *BuildPlan
		actions []*workflow.Action
		want    *BuildPlan
		err     bool
	}{
		{
			name: "Error: already emitted",
			bp: func() *BuildPlan {
				return &BuildPlan{emitted: true, chain: []any{&workflow.Plan{}}}
			},
			err: true,
		},
		{
			name: "Error: no actions",
			err:  true,
		},
		{
			name:    "Error: an Action was nil",
			actions: []*workflow.Action{{}, nil},
			err:     true,
		},
		{
			name: "Error: current() is not a Plan or Block",
			bp: func() *BuildPlan {
				return &BuildPlan{chain: []any{&workflow.Action{}}}
			},
			actions: []*workflow.Action{{}},
			err:     true,
		},
		{
			name: "Success: Plan",
			bp: func() *BuildPlan {
				return &BuildPlan{chain: []any{&workflow.Plan{}}}
			},
			actions: []*workflow.Action{{}},
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
			actions: []*workflow.Action{{}},
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
		err := bp.AddContChecks(0, test.actions...)

		switch {
		case test.err && err == nil:
			t.Errorf("TestAddContChecks(%s): got err == nil, want err != nil", test.name)
			continue
		case !test.err && err != nil:
			t.Errorf("TestAddContChecks(%s): got err != %s, want err == nil", test.name, err)
			continue
		case err != nil:
			continue
		}

		if diff := pretty.Compare(test.want, bp); diff != "" {
			t.Errorf("TestAddContChecks(%s): -want/+got:\n%s", test.name, diff)
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
		err := bp.AddBlock(test.args)

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

		if diff := pretty.Compare(test.want, bp); diff != "" {
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
		err := bp.AddSequence(test.argName, test.descr)

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

		if diff := pretty.Compare(test.want(), bp); diff != "" {
			t.Errorf("TestAddSequence(%s): -want/+got:\n%s", test.name, diff)
		}
	}
}

func TestAddSequenceDirect(t *testing.T) {
	t.Parallel()

	newPlan := func() *workflow.Plan {
		block := &workflow.Block{}
		return &workflow.Plan{Blocks: []*workflow.Block{block}}
	}

	tests := []struct {
		name string
		seq  *workflow.Sequence
		bp   func() *BuildPlan
		want func() *BuildPlan
		err  bool
	}{
		{
			name: "Error: already emitted",
			seq:  &workflow.Sequence{Name: "test", Descr: "test"},
			bp: func() *BuildPlan {
				p := newPlan()
				return &BuildPlan{emitted: true, chain: []any{p, p.Blocks[0]}}
			},
			err: true,
		},
		{
			name: "Error: name is empty",
			seq:  &workflow.Sequence{Descr: "test"},
			bp: func() *BuildPlan {
				p := newPlan()
				return &BuildPlan{chain: []any{p, p.Blocks[0]}}
			},
			err: true,
		},
		{
			name: "Error: description is empty",
			seq:  &workflow.Sequence{Name: "test"},
			bp: func() *BuildPlan {
				p := newPlan()
				return &BuildPlan{chain: []any{p, p.Blocks[0]}}
			},
			err: true,
		},
		{
			name: "Error: current() is not a Block",
			seq:  &workflow.Sequence{Name: "test", Descr: "test"},
			bp: func() *BuildPlan {
				return &BuildPlan{chain: []any{&workflow.Action{}}}
			},
			err: true,
		},
		{
			name: "Success",
			seq:  &workflow.Sequence{Name: "test", Descr: "test", Jobs: []*workflow.Job{{}}},
			bp: func() *BuildPlan {
				p := newPlan()
				return &BuildPlan{chain: []any{p, p.Blocks[0]}}
			},
			want: func() *BuildPlan {
				p := &workflow.Plan{}
				seq := &workflow.Sequence{Name: "test", Descr: "test", Jobs: []*workflow.Job{{}}}
				block := &workflow.Block{}
				block.Sequences = append(block.Sequences, seq)
				p.Blocks = append(p.Blocks, block)
				return &BuildPlan{chain: []any{p, block}}
			},
		},
	}

	for _, test := range tests {
		bp := test.bp()
		err := bp.AddSequenceDirect(test.seq)

		switch {
		case test.err && err == nil:
			t.Errorf("TestAddSequenceDirect(%s): got err == nil, want err != nil", test.name)
			continue
		case !test.err && err != nil:
			t.Errorf("TestAddSequenceDirect(%s): got err != %s, want err == nil", test.name, err)
			continue
		case err != nil:
			continue
		}

		if diff := pretty.Compare(test.want(), bp); diff != "" {
			t.Errorf("TestAddSequenceDirect(%s): -want/+got:\n%s", test.name, diff)
		}
	}
}

func TestAddJob(t *testing.T) {
	t.Parallel()

	newPlan := func() *workflow.Plan {
		seq := &workflow.Sequence{}
		block := &workflow.Block{Sequences: []*workflow.Sequence{seq}}
		return &workflow.Plan{Blocks: []*workflow.Block{block}}
	}

	tests := []struct {
		name string
		job  *workflow.Job
		bp   func() *BuildPlan
		want func() *BuildPlan
		err  bool
	}{
		{
			name: "Error: already emitted",
			job:  &workflow.Job{Name: "test", Descr: "test", Action: &workflow.Action{}},
			bp: func() *BuildPlan {
				p := newPlan()
				return &BuildPlan{emitted: true, chain: []any{p, p.Blocks[0], p.Blocks[0].Sequences[0]}}
			},
			err: true,
		},
		{
			name: "Error: name is empty",
			job:  &workflow.Job{Descr: "test", Action: &workflow.Action{}},
			bp: func() *BuildPlan {
				p := newPlan()
				return &BuildPlan{chain: []any{p, p.Blocks[0], p.Blocks[0].Sequences[0]}}
			},
			err: true,
		},
		{
			name: "Error: description is empty",
			job:  &workflow.Job{Name: "test", Action: &workflow.Action{}},
			bp: func() *BuildPlan {
				p := newPlan()
				return &BuildPlan{chain: []any{p, p.Blocks[0], p.Blocks[0].Sequences[0]}}
			},
			err: true,
		},
		{
			name: "Error: current() is not a Sequence",
			job:  &workflow.Job{Name: "test", Descr: "test", Action: &workflow.Action{}},
			bp: func() *BuildPlan {
				return &BuildPlan{chain: []any{&workflow.Action{}}}
			},
			err: true,
		},
		{
			name: "Success",
			job:  &workflow.Job{Name: "test", Descr: "test", Action: &workflow.Action{}},
			bp: func() *BuildPlan {
				p := newPlan()
				return &BuildPlan{chain: []any{p, p.Blocks[0], p.Blocks[0].Sequences[0]}}
			},
			want: func() *BuildPlan {
				p := newPlan()
				job := &workflow.Job{Name: "test", Descr: "test", Action: &workflow.Action{}}
				p.Blocks[0].Sequences[0].Jobs = append(p.Blocks[0].Sequences[0].Jobs, job)
				return &BuildPlan{chain: []any{p, p.Blocks[0], p.Blocks[0].Sequences[0]}}
			},
		},
	}

	for _, test := range tests {
		bp := test.bp()
		err := bp.AddJob(test.job)

		switch {
		case test.err && err == nil:
			t.Errorf("TestAddJob(%s): got err == nil, want err != nil", test.name)
			continue
		case !test.err && err != nil:
			t.Errorf("TestAddJob(%s): got err != %s, want err == nil", test.name, err)
			continue
		case err != nil:
			continue
		}

		if diff := pretty.Compare(test.want(), bp); diff != "" {
			t.Errorf("TestAddJob(%s): -want/+got:\n%s", test.name, diff)
		}
	}
}
