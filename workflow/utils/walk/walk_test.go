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
	"testing"
	"time"

	"github.com/element-of-surprise/coercion/workflow"

	"github.com/kylelemons/godebug/pretty"
)

func TestPlan(t *testing.T) {
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
						},
					},
				},
			},
		},
	}

	got := []Item{}
	for item := range Plan(plan) {
		got = append(got, item)
	}

	want := []Item{
		{Value: plan},
		{Chain: []workflow.Object{plan}, Value: plan.BypassChecks},
		{Chain: []workflow.Object{plan, plan.BypassChecks}, Value: plan.BypassChecks.Actions[0]},
		{Chain: []workflow.Object{plan}, Value: plan.PreChecks},
		{Chain: []workflow.Object{plan, plan.PreChecks}, Value: plan.PreChecks.Actions[0]},
		{Chain: []workflow.Object{plan}, Value: plan.ContChecks},
		{Chain: []workflow.Object{plan, plan.ContChecks}, Value: plan.ContChecks.Actions[0]},
		{Chain: []workflow.Object{plan}, Value: plan.Blocks[0]},

		{Chain: []workflow.Object{plan, plan.Blocks[0]}, Value: plan.Blocks[0].BypassChecks},
		{Chain: []workflow.Object{plan, plan.Blocks[0], plan.Blocks[0].BypassChecks}, Value: plan.Blocks[0].BypassChecks.Actions[0]},
		{Chain: []workflow.Object{plan, plan.Blocks[0]}, Value: plan.Blocks[0].PreChecks},
		{Chain: []workflow.Object{plan, plan.Blocks[0], plan.Blocks[0].PreChecks}, Value: plan.Blocks[0].PreChecks.Actions[0]},
		{Chain: []workflow.Object{plan, plan.Blocks[0]}, Value: plan.Blocks[0].ContChecks},
		{Chain: []workflow.Object{plan, plan.Blocks[0], plan.Blocks[0].ContChecks}, Value: plan.Blocks[0].ContChecks.Actions[0]},
		{Chain: []workflow.Object{plan, plan.Blocks[0]}, Value: plan.Blocks[0].Sequences[0]},
		{Chain: []workflow.Object{plan, plan.Blocks[0], plan.Blocks[0].Sequences[0]}, Value: plan.Blocks[0].Sequences[0].Actions[0]},
		{Chain: []workflow.Object{plan, plan.Blocks[0]}, Value: plan.Blocks[0].PostChecks},
		{Chain: []workflow.Object{plan, plan.Blocks[0], plan.Blocks[0].PostChecks}, Value: plan.Blocks[0].PostChecks.Actions[0]},
		{Chain: []workflow.Object{plan, plan.Blocks[0]}, Value: plan.Blocks[0].DeferredChecks},
		{Chain: []workflow.Object{plan, plan.Blocks[0], plan.Blocks[0].DeferredChecks}, Value: plan.Blocks[0].DeferredChecks.Actions[0]},
		{Chain: []workflow.Object{plan}, Value: plan.PostChecks},
		{Chain: []workflow.Object{plan, plan.PostChecks}, Value: plan.PostChecks.Actions[0]},
		{Chain: []workflow.Object{plan}, Value: plan.DeferredChecks},
		{Chain: []workflow.Object{plan, plan.DeferredChecks}, Value: plan.DeferredChecks.Actions[0]},
	}

	pConfig := pretty.Config{
		IncludeUnexported: false,
		PrintStringers:    true,
	}

	if diff := pConfig.Compare(want, got); diff != "" {
		t.Errorf("TestPlan: -want, +got:\n%s", diff)
	}
}

func TestLastUpdate(t *testing.T) {
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
