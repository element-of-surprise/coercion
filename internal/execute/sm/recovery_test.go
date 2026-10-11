package sm

import (
	"testing"
	"time"

	"github.com/element-of-surprise/coercion/plugins"
	"github.com/element-of-surprise/coercion/plugins/registry"
	"github.com/element-of-surprise/coercion/workflow"
	"github.com/element-of-surprise/coercion/workflow/storage/sqlite"
	"github.com/kylelemons/godebug/pretty"
)

var pConfig = pretty.Config{
	PrintStringers: true,
}

func newActionWithStateAndAttempts(state *workflow.State, attempts []workflow.Attempt) *workflow.Action {
	a := &workflow.Action{}
	// nil attempts leaves Attempts unset, as on an action that never ran.
	if attempts != nil {
		a.Attempts.Set(attempts)
	}
	a.State.Set(*state)
	return a
}

// withRetries sets a's Retries to n and returns a.
func withRetries(a *workflow.Action, n int) *workflow.Action {
	a.Retries = n
	return a
}

func newChecksWithStateAndActionsRecov(state *workflow.State, actions []*workflow.Action) *workflow.Checks {
	c := &workflow.Checks{Actions: actions}
	c.State.Set(*state)
	return c
}

func newSequenceWithStateAndActionsRecov(state *workflow.State, actions []*workflow.Action) *workflow.Sequence {
	s := &workflow.Sequence{Actions: actions}
	s.State.Set(*state)
	return s
}

func newBlockWithStateSeqsChecks(state *workflow.State, seqs []*workflow.Sequence, bypass, pre, cont, post *workflow.Checks) *workflow.Block {
	b := &workflow.Block{Sequences: seqs, BypassChecks: bypass, PreChecks: pre, ContChecks: cont, PostChecks: post}
	b.State.Set(*state)
	return b
}

func newPlanWithStateBlocksChecks(state *workflow.State, blocks []*workflow.Block, bypass, pre, cont, post, deferred *workflow.Checks) *workflow.Plan {
	p := &workflow.Plan{Blocks: blocks, BypassChecks: bypass, PreChecks: pre, ContChecks: cont, PostChecks: post, DeferredChecks: deferred}
	p.State.Set(*state)
	return p
}

func TestFixAction(t *testing.T) {
	t.Parallel()

	now := time.Now()
	tests := []struct {
		name   string
		action *workflow.Action
		want   *workflow.Action
	}{
		{
			name:   "Success: an action that is not running is unchanged",
			action: newActionWithStateAndAttempts(&workflow.State{Status: workflow.Completed}, []workflow.Attempt{{Start: now, End: now.Add(1)}}),
			want:   newActionWithStateAndAttempts(&workflow.State{Status: workflow.Completed}, []workflow.Attempt{{Start: now, End: now.Add(1)}}),
		},
		{
			name:   "Success: a running action with no attempts is reset",
			action: newActionWithStateAndAttempts(&workflow.State{Status: workflow.Running, Start: now, End: now}, []workflow.Attempt{}),
			want:   newActionWithStateAndAttempts(&workflow.State{Status: workflow.NotStarted}, nil),
		},
		{
			name:   "Success: a running action whose last attempt did not finish is reset",
			action: newActionWithStateAndAttempts(&workflow.State{Status: workflow.Running, Start: now}, []workflow.Attempt{{Start: now}}),
			want:   newActionWithStateAndAttempts(&workflow.State{Status: workflow.NotStarted}, nil),
		},
		{
			name:   "Success: a running action whose last attempt passed is completed",
			action: newActionWithStateAndAttempts(&workflow.State{Status: workflow.Running, Start: now}, []workflow.Attempt{{Start: now, End: now.Add(1)}}),
			want:   newActionWithStateAndAttempts(&workflow.State{Status: workflow.Completed, Start: now, End: now.Add(1)}, []workflow.Attempt{{Start: now, End: now.Add(1)}}),
		},
		{
			name:   "Success: a running action whose last attempt failed with retries left stays running to be retried",
			action: withRetries(newActionWithStateAndAttempts(&workflow.State{Status: workflow.Running, Start: now}, []workflow.Attempt{{Err: &plugins.Error{}, Start: now, End: now.Add(1)}}), 1),
			want:   withRetries(newActionWithStateAndAttempts(&workflow.State{Status: workflow.Running, Start: now}, []workflow.Attempt{{Err: &plugins.Error{}, Start: now, End: now.Add(1)}}), 1),
		},
		{
			name:   "Success: a running action whose last attempt failed permanently with retries left is failed",
			action: withRetries(newActionWithStateAndAttempts(&workflow.State{Status: workflow.Running, Start: now}, []workflow.Attempt{{Err: &plugins.Error{Permanent: true}, Start: now, End: now.Add(1)}}), 1),
			want:   withRetries(newActionWithStateAndAttempts(&workflow.State{Status: workflow.Failed, Start: now, End: now.Add(1)}, []workflow.Attempt{{Err: &plugins.Error{Permanent: true}, Start: now, End: now.Add(1)}}), 1),
		},
		{
			name:   "Success: a running action whose last attempt failed is failed",
			action: newActionWithStateAndAttempts(&workflow.State{Status: workflow.Running, Start: now}, []workflow.Attempt{{Err: &plugins.Error{}, Start: now, End: now.Add(1)}}),
			want:   newActionWithStateAndAttempts(&workflow.State{Status: workflow.Failed, Start: now, End: now.Add(1)}, []workflow.Attempt{{Err: &plugins.Error{}, Start: now, End: now.Add(1)}}),
		},
	}

	for _, test := range tests {
		fixAction(test.action)
		if diff := pConfig.Compare(test.want, test.action); diff != "" {
			t.Errorf("TestFixAction(%s): -want/+got:\n%s", test.name, diff)
		}
		// Compare cannot see inside the atomic State and Attempts, so they are compared on their own.
		if diff := pConfig.Compare(test.want.State.Get(), test.action.State.Get()); diff != "" {
			t.Errorf("TestFixAction(%s): State -want/+got:\n%s", test.name, diff)
		}
		if diff := pConfig.Compare(test.want.Attempts.Get(), test.action.Attempts.Get()); diff != "" {
			t.Errorf("TestFixAction(%s): Attempts -want/+got:\n%s", test.name, diff)
		}
		// Compare cannot see whether Attempts is set. A reset action must have it unset, like one that never ran.
		if got, want := test.action.Attempts.IsSet(), test.want.Attempts.IsSet(); got != want {
			t.Errorf("TestFixAction(%s): got Attempts.IsSet() == %v, want %v", test.name, got, want)
		}
	}
}

func TestFixChecks(t *testing.T) {
	t.Parallel()

	now := time.Now()
	tests := []struct {
		name   string
		checks *workflow.Checks
		want   *workflow.Checks
	}{
		{
			name:   "Success: nil checks do not panic",
			checks: nil,
			want:   nil,
		},
		{
			name:   "Success: checks that are not running are unchanged",
			checks: newChecksWithStateAndActionsRecov(&workflow.State{Status: workflow.Completed}, nil),
			want:   newChecksWithStateAndActionsRecov(&workflow.State{Status: workflow.Completed}, nil),
		},
		{
			name:   "Success: running checks with a completed action are completed",
			checks: newChecksWithStateAndActionsRecov(&workflow.State{Status: workflow.Running, Start: now, End: now}, []*workflow.Action{newActionWithStateAndAttempts(&workflow.State{Status: workflow.Running, Start: now}, []workflow.Attempt{{Start: now, End: now.Add(1)}})}),
			want:   newChecksWithStateAndActionsRecov(&workflow.State{Status: workflow.Completed, Start: now, End: now}, []*workflow.Action{newActionWithStateAndAttempts(&workflow.State{Status: workflow.Completed, Start: now, End: now.Add(1)}, []workflow.Attempt{{Start: now, End: now.Add(1)}})}),
		},
		{
			// Regression: the failed action failed the Checks, but the action between its retries was left Running in
			// Checks that are not run again.
			name: "Success: running checks with a failed action settle an action between its retries Failed",
			checks: newChecksWithStateAndActionsRecov(&workflow.State{Status: workflow.Running, Start: now}, []*workflow.Action{
				newActionWithStateAndAttempts(&workflow.State{Status: workflow.Failed, Start: now, End: now.Add(1)}, []workflow.Attempt{{Err: &plugins.Error{}, Start: now, End: now.Add(1)}}),
				withRetries(newActionWithStateAndAttempts(&workflow.State{Status: workflow.Running, Start: now}, []workflow.Attempt{{Err: &plugins.Error{}, Start: now, End: now.Add(1)}}), 1),
			}),
			want: newChecksWithStateAndActionsRecov(&workflow.State{Status: workflow.Failed, Start: now}, []*workflow.Action{
				newActionWithStateAndAttempts(&workflow.State{Status: workflow.Failed, Start: now, End: now.Add(1)}, []workflow.Attempt{{Err: &plugins.Error{}, Start: now, End: now.Add(1)}}),
				withRetries(newActionWithStateAndAttempts(&workflow.State{Status: workflow.Failed, Start: now, End: now.Add(1)}, []workflow.Attempt{{Err: &plugins.Error{}, Start: now, End: now.Add(1)}}), 1),
			}),
		},
		{
			name:   "Success: running checks with an unfinished action are reset",
			checks: newChecksWithStateAndActionsRecov(&workflow.State{Status: workflow.Running, Start: now, End: now}, []*workflow.Action{newActionWithStateAndAttempts(&workflow.State{Status: workflow.Running, Start: now}, []workflow.Attempt{{Start: now}})}),
			want:   newChecksWithStateAndActionsRecov(&workflow.State{Status: workflow.NotStarted}, []*workflow.Action{newActionWithStateAndAttempts(&workflow.State{Status: workflow.NotStarted}, nil)}),
		},
	}

	for _, test := range tests {
		// Save the original status to detect if fixChecks changed it
		var originalStatus workflow.Status
		if test.checks != nil {
			originalStatus = test.checks.State.Get().Status
		}

		fixChecks(test.checks)

		// For checks that were Running and got marked as Completed by fixChecks, the End time is set to time.Now()
		// We need to exclude it from comparison but verify it was set
		if test.checks != nil && test.want != nil && originalStatus == workflow.Running && test.checks.State.Get().Status == workflow.Completed && test.want.State.Get().Status == workflow.Completed {
			// Save the actual end time
			actualEnd := test.checks.State.Get().End
			// Set both to zero for comparison
			checksState := test.checks.State.Get()
			checksState.End = time.Time{}
			test.checks.State.Set(checksState)
			wantState := test.want.State.Get()
			wantState.End = time.Time{}
			test.want.State.Set(wantState)
			if diff := pConfig.Compare(test.want, test.checks); diff != "" {
				t.Errorf("TestFixChecks(%s): -want/+got):\n%s", test.name, diff)
			}
			// Verify that the End time was actually set (not zero)
			if actualEnd.IsZero() {
				t.Errorf("TestFixChecks(%s): Checks.State.End was not set", test.name)
			}
		} else {
			if diff := pConfig.Compare(test.want, test.checks); diff != "" {
				t.Errorf("TestFixChecks(%s): -want/+got):\n%s", test.name, diff)
			}
		}
		// Compare cannot see inside the atomic State, so the statuses are compared on their own.
		if test.want == nil {
			continue
		}
		if got, want := test.checks.State.Get().Status, test.want.State.Get().Status; got != want {
			t.Errorf("TestFixChecks(%s): got Checks status %v, want %v", test.name, got, want)
		}
		for i, a := range test.checks.Actions {
			if got, want := a.State.Get().Status, test.want.Actions[i].State.Get().Status; got != want {
				t.Errorf("TestFixChecks(%s): got action %d status %v, want %v", test.name, i, got, want)
			}
		}
	}
}

func TestFixSeq(t *testing.T) {
	t.Parallel()

	now := time.Now()
	tests := []struct {
		name string
		seq  *workflow.Sequence
		want *workflow.Sequence
	}{
		{
			name: "Success: a sequence that is not running is unchanged",
			seq:  newSequenceWithStateAndActionsRecov(&workflow.State{Status: workflow.Completed, End: now}, nil),
			want: newSequenceWithStateAndActionsRecov(&workflow.State{Status: workflow.Completed}, nil),
		},
		{
			name: "Success: a running sequence with no completed actions is reset",
			seq: newSequenceWithStateAndActionsRecov(&workflow.State{Status: workflow.Running, Start: now}, []*workflow.Action{
				newActionWithStateAndAttempts(&workflow.State{Status: workflow.Running}, nil),
				newActionWithStateAndAttempts(&workflow.State{Status: workflow.Running}, nil),
			}),
			want: newSequenceWithStateAndActionsRecov(&workflow.State{Status: workflow.NotStarted}, []*workflow.Action{
				newActionWithStateAndAttempts(&workflow.State{Status: workflow.NotStarted}, nil),
				newActionWithStateAndAttempts(&workflow.State{Status: workflow.NotStarted}, nil),
			}),
		},
		{
			name: "Success: a running sequence with a stopped action is stopped",
			seq: newSequenceWithStateAndActionsRecov(&workflow.State{Status: workflow.Running, Start: now}, []*workflow.Action{
				newActionWithStateAndAttempts(&workflow.State{Status: workflow.Completed}, nil),
				newActionWithStateAndAttempts(&workflow.State{Status: workflow.Stopped, End: now}, nil),
				newActionWithStateAndAttempts(&workflow.State{Status: workflow.Running}, nil),
			}),
			want: newSequenceWithStateAndActionsRecov(&workflow.State{Status: workflow.Stopped, Start: now}, []*workflow.Action{
				newActionWithStateAndAttempts(&workflow.State{Status: workflow.Completed}, nil),
				newActionWithStateAndAttempts(&workflow.State{Status: workflow.Stopped}, nil),
				newActionWithStateAndAttempts(&workflow.State{Status: workflow.Stopped}, nil),
			}),
		},
		{
			name: "Success: a running sequence whose actions all completed is completed",
			seq: newSequenceWithStateAndActionsRecov(&workflow.State{Status: workflow.Running, Start: now}, []*workflow.Action{
				newActionWithStateAndAttempts(&workflow.State{Status: workflow.Completed}, nil),
				newActionWithStateAndAttempts(&workflow.State{Status: workflow.Completed}, nil),
			}),
			want: newSequenceWithStateAndActionsRecov(&workflow.State{Status: workflow.Completed, Start: now}, []*workflow.Action{
				newActionWithStateAndAttempts(&workflow.State{Status: workflow.Completed}, nil),
				newActionWithStateAndAttempts(&workflow.State{Status: workflow.Completed}, nil),
			}),
		},
		{
			name: "Success: a running sequence with some completed actions stays running",
			seq: newSequenceWithStateAndActionsRecov(&workflow.State{Status: workflow.Running, Start: now}, []*workflow.Action{
				newActionWithStateAndAttempts(&workflow.State{Status: workflow.Completed}, nil),
				newActionWithStateAndAttempts(&workflow.State{Status: workflow.Running}, nil),
				newActionWithStateAndAttempts(&workflow.State{Status: workflow.Completed}, nil),
			}),
			want: newSequenceWithStateAndActionsRecov(&workflow.State{Status: workflow.Running, Start: now}, []*workflow.Action{
				newActionWithStateAndAttempts(&workflow.State{Status: workflow.Completed}, nil),
				newActionWithStateAndAttempts(&workflow.State{Status: workflow.NotStarted}, nil),
				newActionWithStateAndAttempts(&workflow.State{Status: workflow.Completed}, nil),
			}),
		},
	}

	for _, test := range tests {
		fixSeq(test.seq)
		if test.seq.State.Get().Status == workflow.Stopped {
			if test.seq.State.Get().End.IsZero() {
				t.Errorf("TestFixSeq(%s): got seq.State.End == 0, want non-zero", test.name)
			}
			seqState := test.seq.State.Get()
			seqState.End = time.Time{} // Reset time so we can compare
			test.seq.State.Set(seqState)
			for _, a := range test.seq.Actions {
				if a.State.Get().Status == workflow.Stopped {
					if a.State.Get().End.IsZero() {
						t.Errorf("TestFixSeq(%s): got action.End == 0, want non-zero", test.name)
					}
					aState := a.State.Get()
					aState.End = time.Time{} // Reset time so we can compare
					a.State.Set(aState)
				}
			}
		}
		if test.seq.State.Get().Status == workflow.Completed {
			if test.seq.State.Get().End.IsZero() {
				t.Errorf("TestFixSeq(%s): got seq.State.End == 0, want non-zero", test.name)
			}
			seqState := test.seq.State.Get()
			seqState.End = time.Time{} // Reset time so we can compare
			test.seq.State.Set(seqState)
		}
		if diff := pConfig.Compare(test.want, test.seq); diff != "" {
			t.Errorf("TestFixSeq(%s): -want/+got:\n%s", test.name, diff)
		}
	}
}

// withDeferredChecks sets b's DeferredChecks to c and returns b.
func withDeferredChecks(b *workflow.Block, c *workflow.Checks) *workflow.Block {
	b.DeferredChecks = c
	return b
}

func TestFixBlock(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		b    *workflow.Block
		want *workflow.Block
	}{
		{
			name: "Success: a block that is not running is unchanged",
			b:    newBlockWithStateSeqsChecks(&workflow.State{Status: workflow.Completed}, nil, nil, nil, nil, nil),
			want: newBlockWithStateSeqsChecks(&workflow.State{Status: workflow.Completed}, nil, nil, nil, nil, nil),
		},
		{
			name: "Success: a running block whose PreChecks failed fails",
			b:    newBlockWithStateSeqsChecks(&workflow.State{Status: workflow.Running}, nil, nil, newChecksWithStateAndActionsRecov(&workflow.State{Status: workflow.Failed}, nil), nil, nil),
			want: newBlockWithStateSeqsChecks(&workflow.State{Status: workflow.Failed}, nil, nil, newChecksWithStateAndActionsRecov(&workflow.State{Status: workflow.Failed}, nil), nil, nil),
		},
		{
			name: "Success: a running block whose ContChecks failed fails",
			b:    newBlockWithStateSeqsChecks(&workflow.State{Status: workflow.Running}, nil, nil, newChecksWithStateAndActionsRecov(&workflow.State{Status: workflow.Completed}, nil), newChecksWithStateAndActionsRecov(&workflow.State{Status: workflow.Failed}, nil), nil),
			want: newBlockWithStateSeqsChecks(&workflow.State{Status: workflow.Failed}, nil, nil, newChecksWithStateAndActionsRecov(&workflow.State{Status: workflow.Completed}, nil), newChecksWithStateAndActionsRecov(&workflow.State{Status: workflow.Failed}, nil), nil),
		},
		{
			name: "Success: a block whose PostChecks failed fails",
			b: newBlockWithStateSeqsChecks(&workflow.State{Status: workflow.Failed}, []*workflow.Sequence{
				newSequenceWithStateAndActionsRecov(&workflow.State{Status: workflow.Completed}, nil),
				newSequenceWithStateAndActionsRecov(&workflow.State{Status: workflow.Completed}, nil),
			}, nil, newChecksWithStateAndActionsRecov(&workflow.State{Status: workflow.Completed}, nil), newChecksWithStateAndActionsRecov(&workflow.State{Status: workflow.Completed}, nil), newChecksWithStateAndActionsRecov(&workflow.State{Status: workflow.Failed}, nil)),
			want: newBlockWithStateSeqsChecks(&workflow.State{Status: workflow.Failed}, []*workflow.Sequence{
				newSequenceWithStateAndActionsRecov(&workflow.State{Status: workflow.Completed}, nil),
				newSequenceWithStateAndActionsRecov(&workflow.State{Status: workflow.Completed}, nil),
			}, nil, newChecksWithStateAndActionsRecov(&workflow.State{Status: workflow.Completed}, nil), newChecksWithStateAndActionsRecov(&workflow.State{Status: workflow.Completed}, nil), newChecksWithStateAndActionsRecov(&workflow.State{Status: workflow.Failed}, nil)),
		},
		{
			// Regression: fixBlock asked whether the PostChecks failed before settling them from their actions, so
			// PostChecks still Running over a Failed action left the block Running, the PostChecks were re-run on
			// resume and a pass completed the block.
			name: "Success: a running block whose PostChecks are running over a failed action fails",
			b: newBlockWithStateSeqsChecks(&workflow.State{Status: workflow.Running}, []*workflow.Sequence{
				newSequenceWithStateAndActionsRecov(&workflow.State{Status: workflow.Completed}, nil),
			}, nil, nil, nil, newChecksWithStateAndActionsRecov(&workflow.State{Status: workflow.Running}, []*workflow.Action{newActionWithStateAndAttempts(&workflow.State{Status: workflow.Failed}, nil)})),
			want: newBlockWithStateSeqsChecks(&workflow.State{Status: workflow.Failed}, nil, nil, nil, nil, nil),
		},
		{
			// Regression: as above, for PreChecks.
			name: "Success: a running block whose PreChecks are running over a failed action fails",
			b:    newBlockWithStateSeqsChecks(&workflow.State{Status: workflow.Running}, nil, nil, newChecksWithStateAndActionsRecov(&workflow.State{Status: workflow.Running}, []*workflow.Action{newActionWithStateAndAttempts(&workflow.State{Status: workflow.Failed}, nil)}), nil, nil),
			want: newBlockWithStateSeqsChecks(&workflow.State{Status: workflow.Failed}, nil, nil, nil, nil, nil),
		},
		{
			// Regression: fixBlock never looked at a running block's DeferredChecks. A run that wrote them Failed and
			// crashed before writing the block Failed left the block Running, BlockDeferredChecks reset and re-ran
			// them on resume, and a pass completed the block and the Plan.
			name: "Success: a running block whose DeferredChecks failed fails",
			b: withDeferredChecks(
				newBlockWithStateSeqsChecks(&workflow.State{Status: workflow.Running}, []*workflow.Sequence{
					newSequenceWithStateAndActionsRecov(&workflow.State{Status: workflow.Completed}, nil),
				}, nil, nil, nil, newChecksWithStateAndActionsRecov(&workflow.State{Status: workflow.Completed}, nil)),
				newChecksWithStateAndActionsRecov(&workflow.State{Status: workflow.Failed}, nil),
			),
			want: newBlockWithStateSeqsChecks(&workflow.State{Status: workflow.Failed}, nil, nil, nil, nil, nil),
		},
		{
			// Regression: as above, for a crash after an action was written Failed but before the DeferredChecks were.
			name: "Success: a running block whose DeferredChecks are running over a failed action fails",
			b: withDeferredChecks(
				newBlockWithStateSeqsChecks(&workflow.State{Status: workflow.Running}, []*workflow.Sequence{
					newSequenceWithStateAndActionsRecov(&workflow.State{Status: workflow.Completed}, nil),
				}, nil, nil, nil, newChecksWithStateAndActionsRecov(&workflow.State{Status: workflow.Completed}, nil)),
				newChecksWithStateAndActionsRecov(&workflow.State{Status: workflow.Running}, []*workflow.Action{newActionWithStateAndAttempts(&workflow.State{Status: workflow.Failed}, nil)}),
			),
			want: newBlockWithStateSeqsChecks(&workflow.State{Status: workflow.Failed}, nil, nil, nil, nil, nil),
		},
		{
			name: "Success: a running block whose DeferredChecks completed stays running",
			b: withDeferredChecks(
				newBlockWithStateSeqsChecks(&workflow.State{Status: workflow.Running}, []*workflow.Sequence{
					newSequenceWithStateAndActionsRecov(&workflow.State{Status: workflow.Completed}, nil),
				}, nil, nil, nil, newChecksWithStateAndActionsRecov(&workflow.State{Status: workflow.Completed}, nil)),
				newChecksWithStateAndActionsRecov(&workflow.State{Status: workflow.Completed}, nil),
			),
			want: newBlockWithStateSeqsChecks(&workflow.State{Status: workflow.Running}, nil, nil, nil, nil, nil),
		},
		{
			name: "Success: a running block with completed sequences and no PostChecks stays running",
			b: newBlockWithStateSeqsChecks(&workflow.State{Status: workflow.Running}, []*workflow.Sequence{
				newSequenceWithStateAndActionsRecov(&workflow.State{Status: workflow.Completed}, nil),
				newSequenceWithStateAndActionsRecov(&workflow.State{Status: workflow.Completed}, nil),
			}, nil, nil, nil, nil),
			want: newBlockWithStateSeqsChecks(&workflow.State{Status: workflow.Running}, []*workflow.Sequence{
				newSequenceWithStateAndActionsRecov(&workflow.State{Status: workflow.Completed}, nil),
				newSequenceWithStateAndActionsRecov(&workflow.State{Status: workflow.Completed}, nil),
			}, nil, nil, nil, nil),
		},
		{
			name: "Success: a running block with completed sequences and PostChecks stays running",
			b: newBlockWithStateSeqsChecks(&workflow.State{Status: workflow.Running}, []*workflow.Sequence{
				newSequenceWithStateAndActionsRecov(&workflow.State{Status: workflow.Completed}, nil),
				newSequenceWithStateAndActionsRecov(&workflow.State{Status: workflow.Completed}, nil),
			}, nil, nil, nil, newChecksWithStateAndActionsRecov(&workflow.State{Status: workflow.Completed}, nil)),
			want: newBlockWithStateSeqsChecks(&workflow.State{Status: workflow.Running}, []*workflow.Sequence{
				newSequenceWithStateAndActionsRecov(&workflow.State{Status: workflow.Completed}, nil),
				newSequenceWithStateAndActionsRecov(&workflow.State{Status: workflow.Completed}, nil),
			}, nil, nil, nil, newChecksWithStateAndActionsRecov(&workflow.State{Status: workflow.Completed}, nil)),
		},
		{
			name: "Success: a running block with a stopped sequence stops",
			b: newBlockWithStateSeqsChecks(&workflow.State{Status: workflow.Running}, []*workflow.Sequence{
				newSequenceWithStateAndActionsRecov(&workflow.State{Status: workflow.Completed}, nil),
				newSequenceWithStateAndActionsRecov(&workflow.State{Status: workflow.Stopped}, nil),
				newSequenceWithStateAndActionsRecov(&workflow.State{Status: workflow.Running}, nil),
			}, nil, nil, nil, nil),
			want: newBlockWithStateSeqsChecks(&workflow.State{Status: workflow.Stopped}, []*workflow.Sequence{
				newSequenceWithStateAndActionsRecov(&workflow.State{Status: workflow.Stopped}, nil),
				newSequenceWithStateAndActionsRecov(&workflow.State{Status: workflow.Stopped}, nil),
			}, nil, nil, nil, nil),
		},
		{
			name: "Success: a running block whose BypassChecks completed is completed",
			b: newBlockWithStateSeqsChecks(&workflow.State{Status: workflow.Running}, []*workflow.Sequence{
				newSequenceWithStateAndActionsRecov(&workflow.State{Status: workflow.NotStarted}, nil),
			}, newChecksWithStateAndActionsRecov(&workflow.State{Status: workflow.Completed}, nil), nil, nil, nil),
			want: newBlockWithStateSeqsChecks(&workflow.State{Status: workflow.Completed}, []*workflow.Sequence{
				newSequenceWithStateAndActionsRecov(&workflow.State{Status: workflow.NotStarted}, nil),
			}, newChecksWithStateAndActionsRecov(&workflow.State{Status: workflow.Completed}, nil), nil, nil, nil),
		},
	}

	for _, test := range tests {
		reg := registry.New()
		vault, err := sqlite.New(t.Context(), "", reg, sqlite.WithInMemory())
		if err != nil {
			t.Fatalf("TestFixBlock(%s): failed to create vault: %v", test.name, err)
		}
		(&States{store: vault}).fixBlock(test.b)
		if test.want.State.Get().Status != test.b.State.Get().Status {
			t.Errorf("TestFixBlock(%s): got state %v, want %v", test.name, test.b.State.Get().Status, test.want.State.Get().Status)
		}
	}
}

func TestFixPlan(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		plan *workflow.Plan
		want *workflow.Plan
	}{
		{
			name: "Success: a plan that is not running is left unchanged",
			plan: newPlanWithStateBlocksChecks(&workflow.State{Status: workflow.Completed}, nil, nil, nil, nil, nil, nil),
			want: newPlanWithStateBlocksChecks(&workflow.State{Status: workflow.Completed}, nil, nil, nil, nil, nil, nil),
		},
		{
			name: "Success: a running plan whose bypass checks completed completes",
			plan: newPlanWithStateBlocksChecks(&workflow.State{Status: workflow.Running}, nil, newChecksWithStateAndActionsRecov(&workflow.State{Status: workflow.Completed}, nil), nil, nil, nil, nil),
			want: newPlanWithStateBlocksChecks(&workflow.State{Status: workflow.Completed}, nil, newChecksWithStateAndActionsRecov(&workflow.State{Status: workflow.Completed}, nil), nil, nil, nil, nil),
		},
		{
			name: "Success: a running plan whose prechecks failed fails",
			plan: newPlanWithStateBlocksChecks(&workflow.State{Status: workflow.Running}, nil, nil, newChecksWithStateAndActionsRecov(&workflow.State{Status: workflow.Failed}, nil), nil, nil, nil),
			want: newPlanWithStateBlocksChecks(&workflow.State{Status: workflow.Failed}, nil, nil, newChecksWithStateAndActionsRecov(&workflow.State{Status: workflow.Failed}, nil), nil, nil, nil),
		},
		{
			// Regression: fixPlan asked whether the prechecks failed before settling them from their actions, so
			// prechecks still Running over a Failed action let the Plan be recovered as Completed.
			name: "Success: a running plan whose prechecks are running over a failed action fails",
			plan: newPlanWithStateBlocksChecks(&workflow.State{Status: workflow.Running}, nil, nil, newChecksWithStateAndActionsRecov(&workflow.State{Status: workflow.Running}, []*workflow.Action{newActionWithStateAndAttempts(&workflow.State{Status: workflow.Failed}, nil)}), nil, nil, nil),
			want: newPlanWithStateBlocksChecks(&workflow.State{Status: workflow.Failed}, nil, nil, newChecksWithStateAndActionsRecov(&workflow.State{Status: workflow.Failed}, nil), nil, nil, nil),
		},
		{
			name: "Success: a running plan whose blocks and post, continuous and deferred checks completed completes",
			plan: newPlanWithStateBlocksChecks(&workflow.State{Status: workflow.Running}, []*workflow.Block{
				newBlockWithStateSeqsChecks(&workflow.State{Status: workflow.Completed}, nil, nil, nil, nil, nil),
				newBlockWithStateSeqsChecks(&workflow.State{Status: workflow.Completed}, nil, nil, nil, nil, nil),
			}, nil, nil, nil, newChecksWithStateAndActionsRecov(&workflow.State{Status: workflow.Completed}, nil), newChecksWithStateAndActionsRecov(&workflow.State{Status: workflow.Completed}, nil)),
			want: newPlanWithStateBlocksChecks(&workflow.State{Status: workflow.Completed}, []*workflow.Block{
				newBlockWithStateSeqsChecks(&workflow.State{Status: workflow.Completed}, nil, nil, nil, nil, nil),
				newBlockWithStateSeqsChecks(&workflow.State{Status: workflow.Completed}, nil, nil, nil, nil, nil),
			}, nil, nil, newChecksWithStateAndActionsRecov(&workflow.State{Status: workflow.Completed}, nil), newChecksWithStateAndActionsRecov(&workflow.State{Status: workflow.Completed}, nil), newChecksWithStateAndActionsRecov(&workflow.State{Status: workflow.Completed}, nil)),
		},
		{
			// Regression: with every block Completed, a ContChecks pass the crash cut short (reset to NotStarted by
			// fixChecks) was ignored and the Plan was recovered as Completed instead of running that pass again.
			name: "Success: a running plan whose blocks completed but whose continuous checks pass was cut short stays running",
			plan: newPlanWithStateBlocksChecks(&workflow.State{Status: workflow.Running}, []*workflow.Block{
				newBlockWithStateSeqsChecks(&workflow.State{Status: workflow.Completed}, nil, nil, nil, nil, nil),
			}, nil, nil, newChecksWithStateAndActionsRecov(&workflow.State{Status: workflow.Running}, []*workflow.Action{
				newActionWithStateAndAttempts(&workflow.State{Status: workflow.Completed}, nil),
				newActionWithStateAndAttempts(&workflow.State{Status: workflow.NotStarted}, nil),
			}), nil, nil),
			want: newPlanWithStateBlocksChecks(&workflow.State{Status: workflow.Running}, nil, nil, nil, nil, nil, nil),
		},
		{
			name: "Success: a running plan whose blocks and continuous checks completed with no post or deferred work completes",
			plan: newPlanWithStateBlocksChecks(&workflow.State{Status: workflow.Running}, []*workflow.Block{
				newBlockWithStateSeqsChecks(&workflow.State{Status: workflow.Completed}, nil, nil, nil, nil, nil),
			}, nil, nil, newChecksWithStateAndActionsRecov(&workflow.State{Status: workflow.Completed}, []*workflow.Action{
				newActionWithStateAndAttempts(&workflow.State{Status: workflow.Completed}, nil),
			}), nil, nil),
			want: newPlanWithStateBlocksChecks(&workflow.State{Status: workflow.Completed}, nil, nil, nil, nil, nil, nil),
		},
		{
			name: "Success: a running plan with a failed block fails",
			plan: newPlanWithStateBlocksChecks(&workflow.State{Status: workflow.Running}, []*workflow.Block{
				newBlockWithStateSeqsChecks(&workflow.State{Status: workflow.Failed}, nil, nil, nil, nil, nil),
				newBlockWithStateSeqsChecks(&workflow.State{Status: workflow.Completed}, nil, nil, nil, nil, nil),
			}, nil, nil, nil, nil, nil),
			want: newPlanWithStateBlocksChecks(&workflow.State{Status: workflow.Failed}, []*workflow.Block{
				newBlockWithStateSeqsChecks(&workflow.State{Status: workflow.Failed}, nil, nil, nil, nil, nil),
				newBlockWithStateSeqsChecks(&workflow.State{Status: workflow.Completed}, nil, nil, nil, nil, nil),
			}, nil, nil, nil, nil, nil),
		},
	}

	for _, test := range tests {
		reg := registry.New()
		vault, err := sqlite.New(t.Context(), "", reg, sqlite.WithInMemory())
		if err != nil {
			t.Fatalf("TestFixPlan(%s): failed to create vault: %v", test.name, err)
		}
		(&States{store: vault}).fixPlan(test.plan)
		if test.plan.State.Get().Status != test.want.State.Get().Status {
			t.Errorf("TestFixPlan(%s): got status %v, want %v", test.name, test.plan.State.Get().Status, test.want.State.Get().Status)
		}
	}
}

func TestChecksFailed(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		c    *workflow.Checks
		want bool
	}{
		{"Success: nil checks have not failed", nil, false},
		{"Success: failed checks have failed", newChecksWithStateAndActionsRecov(&workflow.State{Status: workflow.Failed}, nil), true},
		{"Success: completed checks have not failed", newChecksWithStateAndActionsRecov(&workflow.State{Status: workflow.Completed}, nil), false},
	}
	for _, test := range tests {
		if got := checksFailed(test.c); got != test.want {
			t.Errorf("TestChecksFailed(%s): got %v, want %v", test.name, got, test.want)
		}
	}
}

func TestChecksCompleted(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		c    *workflow.Checks
		want bool
	}{
		{"Success: nil checks count as completed", nil, true},
		{"Success: completed checks are completed", newChecksWithStateAndActionsRecov(&workflow.State{Status: workflow.Completed}, nil), true},
		{"Success: running checks are not completed", newChecksWithStateAndActionsRecov(&workflow.State{Status: workflow.Running}, nil), false},
	}
	for _, test := range tests {
		if got := checksCompleted(test.c); got != test.want {
			t.Errorf("TestChecksCompleted(%s): got %v, want %v", test.name, got, test.want)
		}
	}
}

func TestSkipBlock(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		b    block
		want bool
	}{
		{"Success: a completed block is skipped", block{block: newBlockWithStateSeqsChecks(&workflow.State{Status: workflow.Completed}, nil, nil, nil, nil, nil)}, true},
		{"Success: a running block is not skipped", block{block: newBlockWithStateSeqsChecks(&workflow.State{Status: workflow.Running}, nil, nil, nil, nil, nil)}, false},
	}
	for _, test := range tests {
		if got := skipBlock(test.b); got != test.want {
			t.Errorf("TestSkipBlock(%s): got %v, want %v", test.name, got, test.want)
		}
	}
}

func TestIsCompleted(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		o    workflow.Object
		want bool
	}{
		{"Success: a completed object is completed", newBlockWithStateSeqsChecks(&workflow.State{Status: workflow.Completed}, nil, nil, nil, nil, nil), true},
		{"Success: a running object is not completed", newPlanWithStateBlocksChecks(&workflow.State{Status: workflow.Running}, nil, nil, nil, nil, nil, nil), false},
		{"Success: a failed object is completed", newSequenceWithStateAndActionsRecov(&workflow.State{Status: workflow.Failed}, nil), true},
		{"Success: a stopped object is completed", newActionWithStateAndAttempts(&workflow.State{Status: workflow.Stopped}, nil), true},
	}
	for _, test := range tests {
		if got := isCompleted(test.o); got != test.want {
			t.Errorf("TestIsCompleted(%s): got %v, want %v", test.name, got, test.want)
		}
	}
}
