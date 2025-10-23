package azblob

import (
	"testing"
	"time"

	"github.com/element-of-surprise/coercion/workflow"
	"github.com/google/uuid"
)

func TestPlanToEntry(t *testing.T) {
	t.Parallel()

	planID := workflow.NewV7()
	groupID := workflow.NewV7()
	now := time.Now().UTC()

	tests := []struct {
		name    string
		plan    *workflow.Plan
		wantErr bool
	}{
		{
			name: "Success: plan with minimal fields",
			plan: &workflow.Plan{
				ID:         planID,
				GroupID:    groupID,
				Name:       "Test Plan",
				Descr:      "Test Description",
				SubmitTime: now,
				State: &workflow.State{
					Status: workflow.NotStarted,
					Start:  now,
					End:    time.Time{},
				},
				Blocks: []*workflow.Block{},
			},
			wantErr: false,
		},
		{
			name: "Success: plan with checks",
			plan: &workflow.Plan{
				ID:         planID,
				GroupID:    groupID,
				Name:       "Test Plan",
				Descr:      "Test Description",
				SubmitTime: now,
				PreChecks: &workflow.Checks{
					ID: workflow.NewV7(),
					State: &workflow.State{
						Status: workflow.NotStarted,
					},
					Actions: []*workflow.Action{},
				},
				State: &workflow.State{
					Status: workflow.NotStarted,
				},
				Blocks: []*workflow.Block{},
			},
			wantErr: false,
		},
		{
			name:    "Error: nil plan",
			plan:    nil,
			wantErr: true,
		},
		{
			name: "Error: plan with nil ID",
			plan: &workflow.Plan{
				ID:      uuid.Nil,
				Name:    "Test",
				Descr:   "Test",
				Blocks:  []*workflow.Block{},
				State:   &workflow.State{},
			},
			wantErr: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := planToPlanEntry(test.plan)
			switch {
			case err == nil && test.wantErr:
				t.Errorf("TestPlanToEntry(%s): got err == nil, want err != nil", test.name)
				return
			case err != nil && !test.wantErr:
				t.Errorf("TestPlanToEntry(%s): got err == %s, want err == nil", test.name, err)
				return
			case err != nil:
				return
			}

			// Verify basic fields
			if got.ID != test.plan.ID {
				t.Errorf("TestPlanToEntry(%s): ID got %v, want %v", test.name, got.ID, test.plan.ID)
			}
			if got.Name != test.plan.Name {
				t.Errorf("TestPlanToEntry(%s): Name got %q, want %q", test.name, got.Name, test.plan.Name)
			}
			if got.Type != workflow.OTPlan {
				t.Errorf("TestPlanToEntry(%s): Type got %v, want %v", test.name, got.Type, workflow.OTPlan)
			}
		})
	}
}

func TestEntryToPlan(t *testing.T) {
	t.Skip("entryToPlan function was removed - no longer needed with new architecture")
}

func TestBlockToEntry(t *testing.T) {
	t.Parallel()

	blockID := workflow.NewV7()
	planID := workflow.NewV7()

	tests := []struct {
		name    string
		block   *workflow.Block
		pos     int
		wantErr bool
	}{
		{
			name: "Success: basic block",
			block: func() *workflow.Block {
				b := &workflow.Block{
					ID:    blockID,
					Name:  "Test Block",
					Descr: "Test Description",
					State: &workflow.State{
						Status: workflow.NotStarted,
					},
					Sequences: []*workflow.Sequence{},
				}
				b.SetPlanID(planID)
				return b
			}(),
			pos:     0,
			wantErr: false,
		},
		{
			name:    "Error: nil block",
			block:   nil,
			pos:     0,
			wantErr: true,
		},
		{
			name: "Error: block with nil ID",
			block: &workflow.Block{
				ID:    uuid.Nil,
				Name:  "Test",
				Descr: "Test",
			},
			pos:     0,
			wantErr: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := blockToEntry(test.block, test.pos)
			switch {
			case err == nil && test.wantErr:
				t.Errorf("TestBlockToEntry(%s): got err == nil, want err != nil", test.name)
				return
			case err != nil && !test.wantErr:
				t.Errorf("TestBlockToEntry(%s): got err == %s, want err == nil", test.name, err)
				return
			case err != nil:
				return
			}

			if got.ID != test.block.ID {
				t.Errorf("TestBlockToEntry(%s): ID got %v, want %v", test.name, got.ID, test.block.ID)
			}
			if got.Type != workflow.OTBlock {
				t.Errorf("TestBlockToEntry(%s): Type got %v, want %v", test.name, got.Type, workflow.OTBlock)
			}
			if got.Pos != test.pos {
				t.Errorf("TestBlockToEntry(%s): Pos got %d, want %d", test.name, got.Pos, test.pos)
			}
		})
	}
}

func TestEntryToBlock(t *testing.T) {
	t.Parallel()

	blockID := workflow.NewV7()
	planID := workflow.NewV7()

	tests := []struct {
		name  string
		entry blocksEntry
	}{
		{
			name: "Success: basic entry",
			entry: blocksEntry{
				Type:        workflow.OTBlock,
				ID:          blockID,
				PlanID:      planID,
				Name:        "Test Block",
				Descr:       "Test Description",
				Pos:         0,
				StateStatus: workflow.NotStarted,
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := entryToBlock(test.entry)
			if err != nil {
				t.Errorf("TestEntryToBlock(%s): got err == %s, want err == nil", test.name, err)
				return
			}

			if got.ID != test.entry.ID {
				t.Errorf("TestEntryToBlock(%s): ID got %v, want %v", test.name, got.ID, test.entry.ID)
			}
			if got.GetPlanID() != test.entry.PlanID {
				t.Errorf("TestEntryToBlock(%s): PlanID got %v, want %v", test.name, got.GetPlanID(), test.entry.PlanID)
			}
		})
	}
}

func TestChecksToEntry(t *testing.T) {
	t.Parallel()

	checksID := workflow.NewV7()
	planID := workflow.NewV7()

	tests := []struct {
		name    string
		checks  *workflow.Checks
		wantErr bool
	}{
		{
			name: "Success: basic checks",
			checks: func() *workflow.Checks {
				c := &workflow.Checks{
					ID: checksID,
					State: &workflow.State{
						Status: workflow.NotStarted,
					},
					Actions: []*workflow.Action{},
				}
				c.SetPlanID(planID)
				return c
			}(),
			wantErr: false,
		},
		{
			name:    "Error: nil checks",
			checks:  nil,
			wantErr: true,
		},
		{
			name: "Error: checks with nil ID",
			checks: func() *workflow.Checks {
				c := &workflow.Checks{
					ID: uuid.Nil,
				}
				c.SetPlanID(planID)
				return c
			}(),
			wantErr: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := checksToEntry(test.checks)
			switch {
			case err == nil && test.wantErr:
				t.Errorf("TestChecksToEntry(%s): got err == nil, want err != nil", test.name)
				return
			case err != nil && !test.wantErr:
				t.Errorf("TestChecksToEntry(%s): got err == %s, want err == nil", test.name, err)
				return
			case err != nil:
				return
			}

			if got.ID != test.checks.ID {
				t.Errorf("TestChecksToEntry(%s): ID got %v, want %v", test.name, got.ID, test.checks.ID)
			}
			if got.Type != workflow.OTCheck {
				t.Errorf("TestChecksToEntry(%s): Type got %v, want %v", test.name, got.Type, workflow.OTCheck)
			}
		})
	}
}

func TestSequenceToEntry(t *testing.T) {
	t.Parallel()

	seqID := workflow.NewV7()
	planID := workflow.NewV7()

	tests := []struct {
		name     string
		sequence *workflow.Sequence
		pos      int
		wantErr  bool
	}{
		{
			name: "Success: basic sequence",
			sequence: func() *workflow.Sequence {
				s := &workflow.Sequence{
					ID:    seqID,
					Name:  "Test Sequence",
					Descr: "Test Description",
					State: &workflow.State{
						Status: workflow.NotStarted,
					},
					Actions: []*workflow.Action{},
				}
				s.SetPlanID(planID)
				return s
			}(),
			pos:     0,
			wantErr: false,
		},
		{
			name:     "Error: nil sequence",
			sequence: nil,
			pos:      0,
			wantErr:  true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := sequenceToEntry(test.sequence, test.pos)
			switch {
			case err == nil && test.wantErr:
				t.Errorf("TestSequenceToEntry(%s): got err == nil, want err != nil", test.name)
				return
			case err != nil && !test.wantErr:
				t.Errorf("TestSequenceToEntry(%s): got err == %s, want err == nil", test.name, err)
				return
			case err != nil:
				return
			}

			if got.ID != test.sequence.ID {
				t.Errorf("TestSequenceToEntry(%s): ID got %v, want %v", test.name, got.ID, test.sequence.ID)
			}
			if got.Type != workflow.OTSequence {
				t.Errorf("TestSequenceToEntry(%s): Type got %v, want %v", test.name, got.Type, workflow.OTSequence)
			}
		})
	}
}

func TestActionToEntry(t *testing.T) {
	t.Parallel()

	actionID := workflow.NewV7()
	planID := workflow.NewV7()

	tests := []struct {
		name    string
		action  *workflow.Action
		pos     int
		wantErr bool
	}{
		{
			name: "Success: basic action",
			action: func() *workflow.Action {
				a := &workflow.Action{
					ID:      actionID,
					Name:    "Test Action",
					Descr:   "Test Description",
					Plugin:  "test-plugin",
					Timeout: 30 * time.Second,
					State: &workflow.State{
						Status: workflow.NotStarted,
					},
				}
				a.SetPlanID(planID)
				return a
			}(),
			pos:     0,
			wantErr: false,
		},
		{
			name:    "Error: nil action",
			action:  nil,
			pos:     0,
			wantErr: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := actionToEntry(test.action, test.pos)
			switch {
			case err == nil && test.wantErr:
				t.Errorf("TestActionToEntry(%s): got err == nil, want err != nil", test.name)
				return
			case err != nil && !test.wantErr:
				t.Errorf("TestActionToEntry(%s): got err == %s, want err == nil", test.name, err)
				return
			case err != nil:
				return
			}

			if got.ID != test.action.ID {
				t.Errorf("TestActionToEntry(%s): ID got %v, want %v", test.name, got.ID, test.action.ID)
			}
			if got.Type != workflow.OTAction {
				t.Errorf("TestActionToEntry(%s): Type got %v, want %v", test.name, got.Type, workflow.OTAction)
			}
		})
	}
}

func TestRoundTripConversion(t *testing.T) {
	t.Skip("Round-trip test skipped - entryToPlan function was removed with new architecture")
}
