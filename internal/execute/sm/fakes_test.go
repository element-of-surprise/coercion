package sm

import (
	"context"
	"fmt"

	"github.com/element-of-surprise/coercion/workflow"
	"github.com/element-of-surprise/coercion/workflow/storage"
	"github.com/element-of-surprise/coercion/workflow/utils/clone"
)

var cloneOpts = []clone.Option{clone.WithKeepSecrets(), clone.WithKeepState()}

func fakeActionRunner(ctx context.Context, action *workflow.Action, updater storage.ActionUpdater) error {
	if action.Name == "error" {
		return fmt.Errorf("error")
	}
	return nil
}

func fakeParallelActionRunner(ctx context.Context, actions []*workflow.Action) error {
	for _, action := range actions {
		if action.Name == "error" {
			return fmt.Errorf("error")
		}
	}
	return nil
}

type fakeUpdater struct {
	create  []*workflow.Plan
	plans   []*workflow.Plan
	blocks  []*workflow.Block
	seqs    []*workflow.Sequence
	actions []*workflow.Action
	checks  []*workflow.Checks
	calls   int

	storage.Vault
}

func (f *fakeUpdater) Create(ctx context.Context, plan *workflow.Plan) error {
	f.calls++

	n := clone.Plan(ctx, plan, cloneOpts...)
	f.create = append(f.create, n)
	return nil
}

func (f *fakeUpdater) UpdatePlan(ctx context.Context, plan *workflow.Plan) error {
	f.calls++

	n := clone.Plan(ctx, plan, cloneOpts...)
	f.plans = append(f.plans, n)
	return nil
}

func (f *fakeUpdater) UpdateBlock(ctx context.Context, block *workflow.Block) error {
	f.calls++

	n := clone.Block(ctx, block, cloneOpts...)
	f.blocks = append(f.blocks, n)
	return nil
}

func (f *fakeUpdater) UpdateChecks(ctx context.Context, checks *workflow.Checks) error {
	f.calls++

	n := clone.Checks(ctx, checks, cloneOpts...)
	f.checks = append(f.checks, n)
	return nil
}

func (f *fakeUpdater) UpdateAction(ctx context.Context, action *workflow.Action) error {
	f.calls++

	n := clone.Action(ctx, action, cloneOpts...)
	f.actions = append(f.actions, n)
	return nil
}

func (f *fakeUpdater) UpdateSequence(ctx context.Context, seq *workflow.Sequence) error {
	f.calls++

	n := clone.Sequence(ctx, seq, cloneOpts...)
	f.seqs = append(f.seqs, n)
	return nil
}

func fakeRunChecksOnce(ctx context.Context, checks *workflow.Checks) error {
	if checks.Actions[0].Name == "error" {
		return fmt.Errorf("error")
	}
	return nil
}
