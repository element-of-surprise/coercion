package sm

import (
	"context"
	"fmt"

	"github.com/element-of-surprise/workstream/workflow"
	"github.com/element-of-surprise/workstream/workflow/storage"
)

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
	blocks  []*workflow.Block
	seqs    []*workflow.Sequence
	actions []*workflow.Action
	checks  []*workflow.Checks
	calls   int

	storage.Vault
}

func (f *fakeUpdater) UpdateBlock(ctx context.Context, block *workflow.Block) error {
	f.calls++

	n := block.Clone()
	n.State = block.State.Clone()
	f.blocks = append(f.blocks, n)
	return nil
}

func (f *fakeUpdater) UpdateChecks(ctx context.Context, checks *workflow.Checks) error {
	f.calls++

	n := checks.Clone()
	n.State = checks.State.Clone()
	f.checks = append(f.checks, n)
	return nil
}

func (f *fakeUpdater) UpdateAction(ctx context.Context, action *workflow.Action) error {
	f.calls++

	n := action.Clone()
	n.State = action.State.Clone()
	f.actions = append(f.actions, n)
	return nil
}

func (f *fakeUpdater) UpdateSequence(ctx context.Context, seq *workflow.Sequence) error {
	f.calls++

	n := seq.Clone()
	n.State = seq.State.Clone()
	f.seqs = append(f.seqs, n)
	return nil
}

func fakeRunChecksOnce(ctx context.Context, checks *workflow.Checks) error {
	if checks.Actions[0].Name == "error" {
		return fmt.Errorf("error")
	}
	return nil
}
