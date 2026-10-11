/*
Package walk provides a way to walk a workflow.Plan for all objects under it.

Usage is simple and the Context can be used to cancel the walk early:

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	for item := range walk.Plan(ctx, plan) {
		// Do something with item
	}

The walk.Item type is a wrapper around the workflow.Object interface and provides
methods to get the underlying object. If the object is not the expected type, the
method will panic. So from the above code I can look at the Item.Value.Type() and
call the appropriate method to get the object without using reflection.

For example:

	if item.Type() == workflow.OTPlan {
		plan := item.Plan()
		mutatePlan(plan)
	}
*/
package walk

import (
	"slices"
	"testing"
	"time"

	"github.com/element-of-surprise/coercion/workflow"

	"github.com/kylelemons/godebug/pretty"
)

func TestPlan(t *testing.T) {
	t.Parallel()

	plan := &workflow.Plan{
		Name:  "plan",
		Descr: "plan",
		BypassChecks: &workflow.Checks{
			Actions: []*workflow.Action{
				{Name: "plan_bypass_action"},
			},
		},
		PreChecks: &workflow.Checks{
			Actions: []*workflow.Action{
				{Name: "plan_precheck_action"},
				{Name: "plan_precheck_action_2"},
			},
		},
		ContChecks: &workflow.Checks{
			Actions: []*workflow.Action{
				{Name: "plan_contcheck_action"},
			},
		},
		PostChecks: &workflow.Checks{
			Actions: []*workflow.Action{
				{Name: "plan_postcheck_action"},
			},
		},
		DeferredChecks: &workflow.Checks{
			Actions: []*workflow.Action{
				{Name: "plan_deferred_action"},
			},
		},
		DeferredActions: &workflow.DeferredActions{
			DeferredBatches: []*workflow.DeferBatch{
				{
					When:        workflow.OnFailure,
					FailElement: true,
					Sequence: workflow.Sequence{
						Name:  "plan_defer_fail_batch",
						Descr: "plan_defer_fail_batch",
						Actions: []*workflow.Action{
							{Name: "plan_defer_fail_action"},
							{Name: "plan_defer_fail_action_2"},
						},
					},
				},
				{
					When: workflow.OnSuccess,
					Sequence: workflow.Sequence{
						Name:  "plan_defer_success_batch",
						Descr: "plan_defer_success_batch",
						Actions: []*workflow.Action{
							{Name: "plan_defer_success_action"},
						},
					},
				},
			},
		},
		Blocks: []*workflow.Block{
			{
				Name:  "plan_block",
				Descr: "plan_block",
				BypassChecks: &workflow.Checks{
					Actions: []*workflow.Action{
						{Name: "plan_block_bypass_action"},
					},
				},
				PreChecks: &workflow.Checks{
					Actions: []*workflow.Action{
						{Name: "plan_block_precheck_action"},
						{Name: "plan_block_precheck_action_2"},
					},
				},
				ContChecks: &workflow.Checks{
					Actions: []*workflow.Action{
						{Name: "plan_block_contcheck_action"},
					},
				},
				PostChecks: &workflow.Checks{
					Actions: []*workflow.Action{
						{Name: "plan_block_postcheck_action"},
					},
				},
				DeferredChecks: &workflow.Checks{
					Actions: []*workflow.Action{
						{Name: "plan_block_deferredcheck_action"},
					},
				},
				Sequences: []*workflow.Sequence{
					{
						Name:  "plan_block_sequence",
						Descr: "plan_block_sequence",
						Actions: []*workflow.Action{
							{
								Name:  "plan_block_action",
								Descr: "plan_block_action",
							},
							{Name: "plan_block_action_2"},
						},
					},
					{
						Name: "plan_block_sequence_2",
						Actions: []*workflow.Action{
							{Name: "plan_block_sequence_2_action"},
							{Name: "plan_block_sequence_2_action_2"},
						},
					},
				},
			},
			{
				Name: "plan_block_2",
				Sequences: []*workflow.Sequence{
					{
						Name: "plan_block_2_sequence",
						Actions: []*workflow.Action{
							{Name: "plan_block_2_action"},
							{Name: "plan_block_2_action_2"},
						},
					},
				},
			},
		},
	}

	forward := []Item{
		{Value: plan},
		{Chain: []workflow.Object{plan}, Value: plan.BypassChecks},
		{Chain: []workflow.Object{plan, plan.BypassChecks}, Value: plan.BypassChecks.Actions[0]},
		{Chain: []workflow.Object{plan}, Value: plan.PreChecks},
		{Chain: []workflow.Object{plan, plan.PreChecks}, Value: plan.PreChecks.Actions[0]},
		{Chain: []workflow.Object{plan, plan.PreChecks}, Value: plan.PreChecks.Actions[1]},
		{Chain: []workflow.Object{plan}, Value: plan.ContChecks},
		{Chain: []workflow.Object{plan, plan.ContChecks}, Value: plan.ContChecks.Actions[0]},
		{Chain: []workflow.Object{plan}, Value: plan.Blocks[0]},

		{Chain: []workflow.Object{plan, plan.Blocks[0]}, Value: plan.Blocks[0].BypassChecks},
		{Chain: []workflow.Object{plan, plan.Blocks[0], plan.Blocks[0].BypassChecks}, Value: plan.Blocks[0].BypassChecks.Actions[0]},
		{Chain: []workflow.Object{plan, plan.Blocks[0]}, Value: plan.Blocks[0].PreChecks},
		{Chain: []workflow.Object{plan, plan.Blocks[0], plan.Blocks[0].PreChecks}, Value: plan.Blocks[0].PreChecks.Actions[0]},
		{Chain: []workflow.Object{plan, plan.Blocks[0], plan.Blocks[0].PreChecks}, Value: plan.Blocks[0].PreChecks.Actions[1]},
		{Chain: []workflow.Object{plan, plan.Blocks[0]}, Value: plan.Blocks[0].ContChecks},
		{Chain: []workflow.Object{plan, plan.Blocks[0], plan.Blocks[0].ContChecks}, Value: plan.Blocks[0].ContChecks.Actions[0]},
		{Chain: []workflow.Object{plan, plan.Blocks[0]}, Value: plan.Blocks[0].Sequences[0]},
		{Chain: []workflow.Object{plan, plan.Blocks[0], plan.Blocks[0].Sequences[0]}, Value: plan.Blocks[0].Sequences[0].Actions[0]},
		{Chain: []workflow.Object{plan, plan.Blocks[0], plan.Blocks[0].Sequences[0]}, Value: plan.Blocks[0].Sequences[0].Actions[1]},
		{Chain: []workflow.Object{plan, plan.Blocks[0]}, Value: plan.Blocks[0].Sequences[1]},
		{Chain: []workflow.Object{plan, plan.Blocks[0], plan.Blocks[0].Sequences[1]}, Value: plan.Blocks[0].Sequences[1].Actions[0]},
		{Chain: []workflow.Object{plan, plan.Blocks[0], plan.Blocks[0].Sequences[1]}, Value: plan.Blocks[0].Sequences[1].Actions[1]},
		{Chain: []workflow.Object{plan, plan.Blocks[0]}, Value: plan.Blocks[0].PostChecks},
		{Chain: []workflow.Object{plan, plan.Blocks[0], plan.Blocks[0].PostChecks}, Value: plan.Blocks[0].PostChecks.Actions[0]},
		{Chain: []workflow.Object{plan, plan.Blocks[0]}, Value: plan.Blocks[0].DeferredChecks},
		{Chain: []workflow.Object{plan, plan.Blocks[0], plan.Blocks[0].DeferredChecks}, Value: plan.Blocks[0].DeferredChecks.Actions[0]},
		{Chain: []workflow.Object{plan}, Value: plan.Blocks[1]},
		{Chain: []workflow.Object{plan, plan.Blocks[1]}, Value: plan.Blocks[1].Sequences[0]},
		{Chain: []workflow.Object{plan, plan.Blocks[1], plan.Blocks[1].Sequences[0]}, Value: plan.Blocks[1].Sequences[0].Actions[0]},
		{Chain: []workflow.Object{plan, plan.Blocks[1], plan.Blocks[1].Sequences[0]}, Value: plan.Blocks[1].Sequences[0].Actions[1]},
		{Chain: []workflow.Object{plan}, Value: plan.PostChecks},
		{Chain: []workflow.Object{plan, plan.PostChecks}, Value: plan.PostChecks.Actions[0]},
		{Chain: []workflow.Object{plan}, Value: plan.DeferredActions},
		{Chain: []workflow.Object{plan, plan.DeferredActions}, Value: plan.DeferredActions.DeferredBatches[0]},
		{Chain: []workflow.Object{plan, plan.DeferredActions, plan.DeferredActions.DeferredBatches[0]}, Value: plan.DeferredActions.DeferredBatches[0].Actions[0]},
		{Chain: []workflow.Object{plan, plan.DeferredActions, plan.DeferredActions.DeferredBatches[0]}, Value: plan.DeferredActions.DeferredBatches[0].Actions[1]},
		{Chain: []workflow.Object{plan, plan.DeferredActions}, Value: plan.DeferredActions.DeferredBatches[1]},
		{Chain: []workflow.Object{plan, plan.DeferredActions, plan.DeferredActions.DeferredBatches[1]}, Value: plan.DeferredActions.DeferredBatches[1].Actions[0]},
		{Chain: []workflow.Object{plan}, Value: plan.DeferredChecks},
		{Chain: []workflow.Object{plan, plan.DeferredChecks}, Value: plan.DeferredChecks.Actions[0]},
	}

	reversed := func(items []Item) []Item {
		items = slices.Clone(items)
		slices.Reverse(items)
		return items
	}

	tests := []struct {
		name    string
		options []PlanOption
		want    []Item
	}{
		{
			name: "Success: no options yields the plan then every object under it in call order",
			want: forward,
		},
		{
			name:    "Success: WithSkipPlan yields every object under the plan but not the plan",
			options: []PlanOption{WithSkipPlan()},
			want:    forward[1:],
		},
		{
			name:    "Success: WithReverseOrder yields the forward walk reversed",
			options: []PlanOption{WithReverseOrder()},
			want:    reversed(forward),
		},
		{
			name:    "Success: WithSkipPlan and WithReverseOrder yields the forward walk reversed without the plan",
			options: []PlanOption{WithSkipPlan(), WithReverseOrder()},
			want:    reversed(forward[1:]),
		},
		{
			name:    "Success: WithReverseOrder and WithSkipPlan in the other order gives the same result",
			options: []PlanOption{WithReverseOrder(), WithSkipPlan()},
			want:    reversed(forward[1:]),
		},
	}

	pConfig := pretty.Config{
		IncludeUnexported: false,
		PrintStringers:    true,
	}

	for _, test := range tests {
		got := slices.Collect(Plan(plan, test.options...))
		if diff := pConfig.Compare(test.want, got); diff != "" {
			t.Errorf("TestPlan(%s): -want, +got:\n%s", test.name, diff)
			continue
		}

		// Stopping after every possible item exercises each early return in the walk. A walk that kept yielding
		// after the loop broke would panic.
		for stop := 0; stop <= len(test.want); stop++ {
			got := []Item{}
			for item := range Plan(plan, test.options...) {
				if len(got) == stop {
					break
				}
				got = append(got, item)
			}
			if diff := pConfig.Compare(test.want[:stop], got); diff != "" {
				t.Errorf("TestPlan(%s): stopping after %d items: -want, +got:\n%s", test.name, stop, diff)
			}
		}
	}
}

func TestLastUpdate(t *testing.T) {
	t.Parallel()

	now := time.Now()
	past := now.Add(-1 * time.Hour)
	future := now.Add(1 * time.Hour)

	tests := []struct {
		name string
		plan *workflow.Plan
		want time.Time
	}{
		{
			name: "Success: nil plan returns zero time",
			plan: nil,
			want: time.Time{},
		},
		{
			name: "Success: empty plan returns zero time",
			plan: &workflow.Plan{
				Name:  "plan",
				Descr: "plan",
				Blocks: []*workflow.Block{
					{
						Name:  "block",
						Descr: "block",
						Sequences: []*workflow.Sequence{
							{
								Name:  "seq",
								Descr: "seq",
								Actions: []*workflow.Action{
									{Name: "action", Descr: "action", Plugin: "test"},
								},
							},
						},
					},
				},
			},
			want: time.Time{},
		},
		{
			name: "Success: RuntimeUpdate is the latest",
			plan: func() *workflow.Plan {
				p := &workflow.Plan{
					Name:  "plan",
					Descr: "plan",
					Blocks: []*workflow.Block{
						{
							Name:  "block",
							Descr: "block",
							Sequences: []*workflow.Sequence{
								{
									Name:  "seq",
									Descr: "seq",
									Actions: []*workflow.Action{
										{Name: "action", Descr: "action", Plugin: "test"},
									},
								},
							},
						},
					},
				}
				p.RuntimeUpdate.Set(future)
				p.State.Set(workflow.State{Start: past, End: now})
				return p
			}(),
			want: future,
		},
		{
			name: "Success: State.Start is the latest",
			plan: func() *workflow.Plan {
				p := &workflow.Plan{
					Name:  "plan",
					Descr: "plan",
					Blocks: []*workflow.Block{
						{
							Name:  "block",
							Descr: "block",
							Sequences: []*workflow.Sequence{
								{
									Name:  "seq",
									Descr: "seq",
									Actions: []*workflow.Action{
										{Name: "action", Descr: "action", Plugin: "test"},
									},
								},
							},
						},
					},
				}
				p.RuntimeUpdate.Set(past)
				p.State.Set(workflow.State{Start: now})
				p.Blocks[0].Sequences[0].Actions[0].State.Set(workflow.State{Start: future})
				return p
			}(),
			want: future,
		},
		{
			name: "Success: State.End is the latest",
			plan: func() *workflow.Plan {
				p := &workflow.Plan{
					Name:  "plan",
					Descr: "plan",
					Blocks: []*workflow.Block{
						{
							Name:  "block",
							Descr: "block",
							Sequences: []*workflow.Sequence{
								{
									Name:  "seq",
									Descr: "seq",
									Actions: []*workflow.Action{
										{Name: "action", Descr: "action", Plugin: "test"},
									},
								},
							},
						},
					},
				}
				p.RuntimeUpdate.Set(past)
				p.State.Set(workflow.State{Start: past, End: now})
				p.Blocks[0].State.Set(workflow.State{Start: past, End: future})
				return p
			}(),
			want: future,
		},
		{
			name: "Success: nested checks State.End is the latest",
			plan: func() *workflow.Plan {
				p := &workflow.Plan{
					Name:  "plan",
					Descr: "plan",
					PreChecks: &workflow.Checks{
						Actions: []*workflow.Action{
							{Name: "precheck_action", Descr: "precheck_action", Plugin: "test"},
						},
					},
					Blocks: []*workflow.Block{
						{
							Name:  "block",
							Descr: "block",
							Sequences: []*workflow.Sequence{
								{
									Name:  "seq",
									Descr: "seq",
									Actions: []*workflow.Action{
										{Name: "action", Descr: "action", Plugin: "test"},
									},
								},
							},
						},
					},
				}
				p.RuntimeUpdate.Set(past)
				p.State.Set(workflow.State{Start: past, End: now})
				p.PreChecks.Actions[0].State.Set(workflow.State{Start: now, End: future})
				return p
			}(),
			want: future,
		},
	}

	for _, test := range tests {
		got := LastUpdate(t.Context(), test.plan)
		if !got.Equal(test.want) {
			t.Errorf("TestLastUpdate(%s): got %v, want %v", test.name, got, test.want)
		}
	}
}

func TestRunningObjects(t *testing.T) {
	t.Parallel()

	running := workflow.State{Status: workflow.Running}
	done := workflow.State{Status: workflow.Completed}

	newAction := func(state workflow.State) *workflow.Action {
		a := &workflow.Action{ID: workflow.NewV7()}
		a.State.Set(state)
		return a
	}

	runningAction := newAction(running)
	seq := &workflow.Sequence{ID: workflow.NewV7(), Actions: []*workflow.Action{newAction(done), runningAction}}
	seq.State.Set(running)
	finishedSeq := &workflow.Sequence{ID: workflow.NewV7(), Actions: []*workflow.Action{newAction(done)}}
	finishedSeq.State.Set(done)
	block := &workflow.Block{ID: workflow.NewV7(), Sequences: []*workflow.Sequence{seq, finishedSeq}}
	block.State.Set(running)
	plan := &workflow.Plan{ID: workflow.NewV7(), Blocks: []*workflow.Block{block}}
	plan.State.Set(running)

	idle := &workflow.Plan{ID: workflow.NewV7(), Blocks: []*workflow.Block{}}
	idle.State.Set(running)

	tests := []struct {
		name string
		plan *workflow.Plan
		want []workflow.Object
	}{
		{
			name: "Success: the running objects below the plan are yielded in walk order, the plan itself is not",
			plan: plan,
			want: []workflow.Object{block, seq, runningAction},
		},
		{
			name: "Success: a running plan with nothing below it yields nothing",
			plan: idle,
			want: nil,
		},
	}

	for _, test := range tests {
		var got []workflow.Object
		for item := range RunningObjects(test.plan) {
			got = append(got, item.Value)
		}
		if len(got) != len(test.want) {
			t.Errorf("TestRunningObjects(%s): got %d objects, want %d", test.name, len(got), len(test.want))
			continue
		}
		for i := range got {
			if got[i] != test.want[i] {
				t.Errorf("TestRunningObjects(%s): object %d: got %v %p, want %v %p", test.name, i, got[i].Type(), got[i], test.want[i].Type(), test.want[i])
			}
		}
		for stop := 0; stop <= len(test.want); stop++ {
			var got []workflow.Object
			for item := range RunningObjects(test.plan) {
				if len(got) == stop {
					break
				}
				got = append(got, item.Value)
			}
			if !slices.Equal(got, test.want[:stop]) {
				t.Errorf("TestRunningObjects(%s): stopping after %d items: got %v, want %v", test.name, stop, got, test.want[:stop])
			}
		}
	}
}

func TestSettleRunning(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	existingEnd := start.Add(time.Minute)
	end := start.Add(2 * time.Minute)

	for _, status := range []workflow.Status{workflow.Failed, workflow.Completed} {
		t.Run(status.String(), func(t *testing.T) {
			t.Parallel()

			plan := &workflow.Plan{
				PreChecks: &workflow.Checks{Actions: []*workflow.Action{{}}},
				Blocks: []*workflow.Block{
					{
						Sequences: []*workflow.Sequence{
							{Actions: []*workflow.Action{{}, {}, {}}},
							{Actions: []*workflow.Action{{}}},
						},
					},
				},
				DeferredActions: &workflow.DeferredActions{
					DeferredBatches: []*workflow.DeferBatch{
						{Sequence: workflow.Sequence{Actions: []*workflow.Action{{}}}},
					},
				},
			}
			block := plan.Blocks[0]
			seq := block.Sequences[0]
			batch := plan.DeferredActions.DeferredBatches[0]
			running := workflow.State{Status: workflow.Running, Start: start, ETag: "unchanged"}
			for item := range Plan(plan) {
				item.Value.(Stateful).SetState(running)
			}
			withEnd := running
			withEnd.End = existingEnd
			seq.Actions[0].SetState(withEnd)
			seq.Actions[1].SetState(workflow.State{Status: workflow.Completed, Start: start, End: existingEnd, ETag: "completed"})
			seq.Actions[2].SetState(workflow.State{Status: workflow.NotStarted, ETag: "idle"})
			block.Sequences[1].SetState(workflow.State{Status: workflow.Completed, Start: start, End: existingEnd})

			before := map[workflow.Object]workflow.State{}
			for item := range Plan(plan) {
				before[item.Value] = item.Value.(Stateful).GetState()
			}

			want := []Item{
				{Chain: []workflow.Object{plan, plan.DeferredActions, batch}, Value: batch.Actions[0]},
				{Chain: []workflow.Object{plan, plan.DeferredActions}, Value: batch},
				{Chain: []workflow.Object{plan}, Value: plan.DeferredActions},
				{Chain: []workflow.Object{plan, block, block.Sequences[1]}, Value: block.Sequences[1].Actions[0]},
				{Chain: []workflow.Object{plan, block, seq}, Value: seq.Actions[0]},
				{Chain: []workflow.Object{plan, block}, Value: seq},
				{Chain: []workflow.Object{plan}, Value: block},
				{Chain: []workflow.Object{plan, plan.PreChecks}, Value: plan.PreChecks.Actions[0]},
				{Chain: []workflow.Object{plan}, Value: plan.PreChecks},
			}
			got := SettleRunning(plan, status, end)
			if len(got) != len(want) {
				t.Errorf("SettleRunning: got %d objects, want %d", len(got), len(want))
			} else {
				for i := range got {
					if got[i].Value != want[i].Value || !slices.Equal(got[i].Chain, want[i].Chain) {
						t.Errorf("SettleRunning: item %d: got %v, want %v", i, got[i], want[i])
					}
				}
			}
			for obj, state := range before {
				want := state
				if obj != plan && state.Status == workflow.Running {
					want.Status = status
					if want.End.IsZero() {
						want.End = end
					}
				}
				if got := obj.(Stateful).GetState(); got != want {
					t.Errorf("SettleRunning: %v %p: got state %v, want %v", obj.Type(), obj, got, want)
				}
			}
			if got := SettleRunning(plan, status, end.Add(time.Minute)); len(got) != 0 {
				t.Errorf("SettleRunning: repeated call returned %d objects, want none", len(got))
			}
		})
	}
}
