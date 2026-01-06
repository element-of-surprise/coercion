package azblob

import (
	"testing"
	"time"

	"github.com/element-of-surprise/coercion/plugins"
	"github.com/element-of-surprise/coercion/plugins/registry"
	"github.com/element-of-surprise/coercion/workflow"
	"github.com/element-of-surprise/coercion/workflow/storage"
	testPlugins "github.com/element-of-surprise/coercion/workflow/storage/sqlite/testing/plugins"
	"github.com/go-json-experiment/json"
	"github.com/google/uuid"
	"github.com/kylelemons/godebug/pretty"
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
			plan: func() *workflow.Plan {
				p := &workflow.Plan{
					ID:         planID,
					GroupID:    groupID,
					Name:       "Test Plan",
					Descr:      "Test Description",
					SubmitTime: now,
					Blocks:     []*workflow.Block{},
				}
				p.State.Set(workflow.State{
					Status: workflow.NotStarted,
					Start:  now,
					End:    time.Time{},
				})
				return p
			}(),
			wantErr: false,
		},
		{
			name: "Success: plan with checks",
			plan: func() *workflow.Plan {
				c := &workflow.Checks{
					ID:      workflow.NewV7(),
					Actions: []*workflow.Action{},
				}
				c.State.Set(workflow.State{Status: workflow.NotStarted})
				p := &workflow.Plan{
					ID:         planID,
					GroupID:    groupID,
					Name:       "Test Plan",
					Descr:      "Test Description",
					SubmitTime: now,
					PreChecks:  c,
					Blocks:     []*workflow.Block{},
				}
				p.State.Set(workflow.State{Status: workflow.NotStarted})
				return p
			}(),
			wantErr: false,
		},
		{
			name:    "Error: nil plan",
			plan:    nil,
			wantErr: true,
		},
		{
			name: "Error: plan with nil ID",
			plan: func() *workflow.Plan {
				p := &workflow.Plan{
					ID:     uuid.Nil,
					Name:   "Test",
					Descr:  "Test",
					Blocks: []*workflow.Block{},
				}
				p.State.Set(workflow.State{})
				return p
			}(),
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
					ID:        blockID,
					Name:      "Test Block",
					Descr:     "Test Description",
					Sequences: []*workflow.Sequence{},
				}
				b.State.Set(workflow.State{Status: workflow.NotStarted})
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
					ID:      checksID,
					Actions: []*workflow.Action{},
				}
				c.State.Set(workflow.State{Status: workflow.NotStarted})
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
					ID:      seqID,
					Name:    "Test Sequence",
					Descr:   "Test Description",
					Actions: []*workflow.Action{},
				}
				s.State.Set(workflow.State{Status: workflow.NotStarted})
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
				}
				a.State.Set(workflow.State{Status: workflow.NotStarted})
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

func TestDecodeAttempts(t *testing.T) {
	reg := registry.New()
	reg.Register(&testPlugins.HelloPlugin{})

	attempts := []*workflow.Attempt{
		{
			Resp: &testPlugins.HelloResp{},
			Err: &plugins.Error{
				Message: "msg",
			},
			Start: time.Now().UTC(),
			End:   time.Now().UTC(),
		},
		{
			Resp:  &testPlugins.HelloResp{Said: "world"},
			Start: time.Now().UTC(),
			End:   time.Now().UTC(),
		},
	}

	encoded, err := json.Marshal(attempts)
	if err != nil {
		panic(err)
	}

	decoded, err := decodeAttempts(t.Context(), encoded, reg.Plugin(testPlugins.HelloPluginName))
	if err != nil {
		t.Fatalf("decodeAttempts returned error: %v", err)
	}

	pconfig := pretty.Config{
		PrintStringers:      true,
		PrintTextMarshalers: true,
		SkipZeroFields:      true,
	}

	if diff := pconfig.Compare(attempts, decoded); diff != "" {
		t.Errorf("-want/+got:\n%s", diff)
	}
}

func TestPlanMetadataConversion(t *testing.T) {
	t.Parallel()

	planID := workflow.NewV7()
	groupID := workflow.NewV7()
	now := time.Now().UTC().Round(time.Nanosecond)

	tests := []struct {
		name string
		plan *workflow.Plan
		/* metadataOverride allows us to inject custom metadata for testing mapToPlanMeta. This is useful for:
			1. Testing edge cases - It lets us test mapToPlanMeta with specific metadata that might be hard or impossible to generate from a valid Plan object
			2. Testing error cases - We can inject invalid data like:
			    - Malformed UUIDs: "planid": toPtr("not-a-uuid")
			    - Invalid timestamps: "submittime": toPtr("not-a-time")
			    - Broken JSON: "state": toPtr("{invalid json}")
		3. Testing Azure Blob Storage quirks - The case-insensitive keys test uses metadata with mixed casing ("PlanID", "NAME", "DeSCr") to verify that
		  mapToPlanMeta handles Azure's inconsistent key casing correctly.
		*/
		metadataOverride  map[string]*string
		testMapToPlanMeta bool
		testPlanToMeta    bool
		wantErr           bool
		wantPlanMeta      *planMeta
	}{
		{
			name: "Success: round trip conversion with all fields",
			plan: func() *workflow.Plan {
				p := &workflow.Plan{
					ID:         planID,
					GroupID:    groupID,
					Name:       "Test Plan",
					Descr:      "Test Description",
					SubmitTime: now,
				}
				p.State.Set(workflow.State{
					Status: workflow.Running,
					Start:  now,
				})
				return p
			}(),
			testPlanToMeta:    true,
			testMapToPlanMeta: true,
			wantErr:           false,
		},
		{
			name: "Success: plan without group ID",
			plan: func() *workflow.Plan {
				p := &workflow.Plan{
					ID:         planID,
					GroupID:    uuid.Nil,
					Name:       "Test Plan",
					Descr:      "Test Description",
					SubmitTime: now,
				}
				p.State.Set(workflow.State{Status: workflow.NotStarted})
				return p
			}(),
			testPlanToMeta:    true,
			testMapToPlanMeta: true,
			wantErr:           false,
		},
		{
			name: "Success: case insensitive metadata keys",
			metadataOverride: func() map[string]*string {
				// Note: mapToPlanMeta uses time.RFC3339 for parsing, not RFC3339Nano
				// so we need to truncate to second precision
				truncatedTime := now.Truncate(time.Second)

				// Marshal the state properly
				stateJSON, _ := json.Marshal(&workflow.State{
					Status: workflow.Running,
					Start:  truncatedTime,
				})

				return map[string]*string{
					"PlanID":     toPtr(planID.String()),
					"GroupID":    toPtr(groupID.String()),
					"NAME":       toPtr("Test Plan"),
					"DeSCr":      toPtr("Test Description"),
					"submitTIME": toPtr(truncatedTime.Format(time.RFC3339)),
					"STATE":      toPtr(string(stateJSON)),
					"PlanType":   toPtr("entry"),
				}
			}(),
			testMapToPlanMeta: true,
			wantErr:           false,
			wantPlanMeta: func() *planMeta {
				truncatedTime := now.Truncate(time.Second)
				return &planMeta{
					ListResult: storage.ListResult{
						ID:         planID,
						GroupID:    groupID,
						Name:       "Test Plan",
						Descr:      "Test Description",
						SubmitTime: truncatedTime,
						State: workflow.State{
							Status: workflow.Running,
							Start:  truncatedTime,
						},
					},
					PlanType: "entry",
				}
			}(),
		},
		{
			name: "Success: metadata with nil values",
			metadataOverride: map[string]*string{
				"planid":     toPtr(planID.String()),
				"name":       toPtr("Test Plan"),
				"descr":      toPtr("Test Description"),
				"submittime": toPtr(now.Format(time.RFC3339)),
				"state":      toPtr(`{"status":"notstarted"}`),
				"nullfield":  nil,
			},
			testMapToPlanMeta: true,
			wantErr:           false,
		},
		{
			name: "Error: invalid plan ID in metadata",
			metadataOverride: map[string]*string{
				"planid":     toPtr("not-a-uuid"),
				"name":       toPtr("Test Plan"),
				"descr":      toPtr("Test Description"),
				"submittime": toPtr(now.Format(time.RFC3339)),
				"state":      toPtr(`{"status":"notstarted"}`),
			},
			testMapToPlanMeta: true,
			wantErr:           true,
		},
		{
			name: "Error: invalid group ID in metadata",
			metadataOverride: map[string]*string{
				"planid":     toPtr(planID.String()),
				"groupid":    toPtr("invalid-uuid"),
				"name":       toPtr("Test Plan"),
				"descr":      toPtr("Test Description"),
				"submittime": toPtr(now.Format(time.RFC3339)),
				"state":      toPtr(`{"status":"notstarted"}`),
			},
			testMapToPlanMeta: true,
			wantErr:           true,
		},
		{
			name: "Error: invalid submit time in metadata",
			metadataOverride: map[string]*string{
				"planid":     toPtr(planID.String()),
				"name":       toPtr("Test Plan"),
				"descr":      toPtr("Test Description"),
				"submittime": toPtr("not-a-time"),
				"state":      toPtr(`{"status":"notstarted"}`),
			},
			testMapToPlanMeta: true,
			wantErr:           true,
		},
		{
			name: "Error: invalid state JSON in metadata",
			metadataOverride: map[string]*string{
				"planid":     toPtr(planID.String()),
				"name":       toPtr("Test Plan"),
				"descr":      toPtr("Test Description"),
				"submittime": toPtr(now.Format(time.RFC3339)),
				"state":      toPtr(`{invalid json}`),
			},
			testMapToPlanMeta: true,
			wantErr:           true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Test planToMetadata
			var metadata map[string]*string
			if test.testPlanToMeta {
				var err error
				metadata, err = planToMetadata(t.Context(), test.plan)
				switch {
				case err == nil && test.wantErr:
					t.Errorf("TestPlanMetadataConversion(%s): planToMetadata got err == nil, want err != nil", test.name)
					return
				case err != nil && !test.wantErr:
					t.Errorf("TestPlanMetadataConversion(%s): planToMetadata got err == %s, want err == nil", test.name, err)
					return
				case err != nil:
					return
				}

				// Verify metadata keys
				if metadata[mdKeyPlanID] == nil || *metadata[mdKeyPlanID] != test.plan.ID.String() {
					t.Errorf("TestPlanMetadataConversion(%s): metadata planid mismatch", test.name)
				}
				if metadata[mdKeyName] == nil || *metadata[mdKeyName] != test.plan.Name {
					t.Errorf("TestPlanMetadataConversion(%s): metadata name mismatch", test.name)
				}
				if metadata[mdKeyDescr] == nil || *metadata[mdKeyDescr] != test.plan.Descr {
					t.Errorf("TestPlanMetadataConversion(%s): metadata descr mismatch", test.name)
				}

				// Check GroupID handling
				if test.plan.GroupID != uuid.Nil {
					if metadata[mdKeyGroupID] == nil || *metadata[mdKeyGroupID] != test.plan.GroupID.String() {
						t.Errorf("TestPlanMetadataConversion(%s): metadata groupid mismatch", test.name)
					}
				} else {
					if _, exists := metadata[mdKeyGroupID]; exists {
						t.Errorf("TestPlanMetadataConversion(%s): metadata should not contain groupid for nil GroupID", test.name)
					}
				}
			}

			// Use override metadata if provided, otherwise use the generated metadata
			if test.metadataOverride != nil {
				metadata = test.metadataOverride
			}

			// Test mapToPlanMeta
			if test.testMapToPlanMeta {
				got, err := mapToPlanMeta(metadata)
				switch {
				case err == nil && test.wantErr:
					t.Errorf("TestPlanMetadataConversion(%s): mapToPlanMeta got err == nil, want err != nil", test.name)
					return
				case err != nil && !test.wantErr:
					t.Errorf("TestPlanMetadataConversion(%s): mapToPlanMeta got err == %s, want err == nil", test.name, err)
					return
				case err != nil:
					return
				}

				// If we have expected planMeta, verify it
				if test.wantPlanMeta != nil {
					if got.ID != test.wantPlanMeta.ID {
						t.Errorf("TestPlanMetadataConversion(%s): planMeta ID got %v, want %v", test.name, got.ID, test.wantPlanMeta.ID)
					}
					if got.GroupID != test.wantPlanMeta.GroupID {
						t.Errorf("TestPlanMetadataConversion(%s): planMeta GroupID got %v, want %v", test.name, got.GroupID, test.wantPlanMeta.GroupID)
					}
					if got.Name != test.wantPlanMeta.Name {
						t.Errorf("TestPlanMetadataConversion(%s): planMeta Name got %q, want %q", test.name, got.Name, test.wantPlanMeta.Name)
					}
					if got.Descr != test.wantPlanMeta.Descr {
						t.Errorf("TestPlanMetadataConversion(%s): planMeta Descr got %q, want %q", test.name, got.Descr, test.wantPlanMeta.Descr)
					}
					if got.PlanType != test.wantPlanMeta.PlanType {
						t.Errorf("TestPlanMetadataConversion(%s): planMeta PlanType got %q, want %q", test.name, got.PlanType, test.wantPlanMeta.PlanType)
					}
					if !got.SubmitTime.Equal(test.wantPlanMeta.SubmitTime) {
						t.Errorf("TestPlanMetadataConversion(%s): planMeta SubmitTime got %v, want %v", test.name, got.SubmitTime, test.wantPlanMeta.SubmitTime)
					}
					if got.State != (workflow.State{}) && test.wantPlanMeta.State != (workflow.State{}) {
						if got.State.Status != test.wantPlanMeta.State.Status {
							t.Errorf("TestPlanMetadataConversion(%s): planMeta State.Status got %v, want %v", test.name, got.State.Status, test.wantPlanMeta.State.Status)
						}
					}
				}

				// If we're doing a round trip test, verify the data matches the original plan
				if test.testPlanToMeta && test.plan != nil {
					if got.ID != test.plan.ID {
						t.Errorf("TestPlanMetadataConversion(%s): round trip ID got %v, want %v", test.name, got.ID, test.plan.ID)
					}
					if got.Name != test.plan.Name {
						t.Errorf("TestPlanMetadataConversion(%s): round trip Name got %q, want %q", test.name, got.Name, test.plan.Name)
					}
					if got.Descr != test.plan.Descr {
						t.Errorf("TestPlanMetadataConversion(%s): round trip Descr got %q, want %q", test.name, got.Descr, test.plan.Descr)
					}
					if test.plan.GroupID != uuid.Nil && got.GroupID != test.plan.GroupID {
						t.Errorf("TestPlanMetadataConversion(%s): round trip GroupID got %v, want %v", test.name, got.GroupID, test.plan.GroupID)
					}
					if got.State != (workflow.State{}) && test.plan.State.Get() != (workflow.State{}) {
						if got.State.Status != test.plan.State.Get().Status {
							t.Errorf("TestPlanMetadataConversion(%s): round trip State.Status got %v, want %v", test.name, got.State.Status, test.plan.State.Get().Status)
						}
					}
				}
			}
		})
	}
}
