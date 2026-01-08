package reports

import (
	"strings"
	"testing"
	"time"

	"github.com/element-of-surprise/coercion/plugins"
	"github.com/element-of-surprise/coercion/workflow"
	"github.com/google/uuid"
)

func newV7() uuid.UUID {
	for {
		id, err := uuid.NewV7()
		if err == nil {
			return id
		}
	}
}

// testResp is a simple response type for testing.
type testResp struct {
	Message string
}

// testReq is a simple request type for testing.
type testReq struct {
	Input string
}

// makeAction creates an Action with the given name, status, and attempts.
func makeAction(name string, status workflow.Status, numAttempts int, hasError bool) *workflow.Action {
	attempts := make([]workflow.Attempt, numAttempts)
	for i := range attempts {
		if hasError && i == numAttempts-1 {
			attempts[i] = workflow.Attempt{
				Err: &plugins.Error{Message: "test error", Permanent: true},
			}
		} else {
			attempts[i] = workflow.Attempt{
				Resp: testResp{Message: "success"},
			}
		}
	}

	action := &workflow.Action{
		ID:      newV7(),
		Name:    name,
		Descr:   name + " description",
		Plugin:  "testPlugin",
		Timeout: 30 * time.Second,
		Req:     testReq{Input: "test input"},
	}
	action.Attempts.Set(attempts)
	action.State.Set(workflow.State{
		Status: status,
		Start:  time.Now().Add(-1 * time.Hour),
		End:    time.Now(),
	})
	return action
}

// makeChecks creates a Checks with the given actions.
func makeChecks(status workflow.Status, actions ...*workflow.Action) *workflow.Checks {
	checks := &workflow.Checks{
		ID:      newV7(),
		Delay:   5 * time.Second,
		Actions: actions,
	}
	checks.State.Set(workflow.State{
		Status: status,
		Start:  time.Now().Add(-1 * time.Hour),
		End:    time.Now(),
	})
	return checks
}

// makeSequence creates a Sequence with the given name, status, and actions.
func makeSequence(name string, status workflow.Status, actions ...*workflow.Action) *workflow.Sequence {
	seq := &workflow.Sequence{
		ID:      newV7(),
		Name:    name,
		Descr:   name + " description",
		Actions: actions,
	}
	seq.State.Set(workflow.State{
		Status: status,
		Start:  time.Now().Add(-1 * time.Hour),
		End:    time.Now(),
	})
	return seq
}

// makeBlock creates a Block with all check types and sequences.
func makeBlock(name string, status workflow.Status, sequences []*workflow.Sequence) *workflow.Block {
	block := &workflow.Block{
		ID:                newV7(),
		Name:              name,
		Descr:             name + " description",
		EntranceDelay:     1 * time.Second,
		ExitDelay:         1 * time.Second,
		Concurrency:       2,
		ToleratedFailures: 1,
		BypassChecks:      makeChecks(workflow.Completed, makeAction("bypass check", workflow.Completed, 1, false)),
		PreChecks:         makeChecks(workflow.Completed, makeAction("pre check", workflow.Completed, 1, false)),
		ContChecks:        makeChecks(workflow.Completed, makeAction("cont check", workflow.Completed, 1, false)),
		PostChecks:        makeChecks(workflow.Completed, makeAction("post check", workflow.Completed, 1, false)),
		DeferredChecks:    makeChecks(workflow.Completed, makeAction("deferred check", workflow.Completed, 1, false)),
		Sequences:         sequences,
	}
	block.State.Set(workflow.State{
		Status: status,
		Start:  time.Now().Add(-1 * time.Hour),
		End:    time.Now(),
	})
	return block
}

// makePlan creates a Plan with all check types, blocks, sequences, and actions.
func makePlan(status workflow.Status) *workflow.Plan {
	seq := makeSequence("test sequence", workflow.Completed,
		makeAction("action1", workflow.Completed, 2, false),
		makeAction("action2", workflow.Completed, 1, false),
	)

	block := makeBlock("test block", workflow.Completed, []*workflow.Sequence{seq})

	plan := &workflow.Plan{
		ID:             newV7(),
		Name:           "Test Plan",
		Descr:          "A test plan for template rendering",
		GroupID:        newV7(),
		SubmitTime:     time.Now().Add(-2 * time.Hour),
		BypassChecks:   makeChecks(workflow.Completed, makeAction("plan bypass", workflow.Completed, 1, false)),
		PreChecks:      makeChecks(workflow.Completed, makeAction("plan pre", workflow.Completed, 1, false)),
		ContChecks:     makeChecks(workflow.Completed, makeAction("plan cont", workflow.Completed, 1, false)),
		PostChecks:     makeChecks(workflow.Completed, makeAction("plan post", workflow.Completed, 1, false)),
		DeferredChecks: makeChecks(workflow.Completed, makeAction("plan deferred", workflow.Completed, 1, false)),
		Blocks:         []*workflow.Block{block},
	}
	plan.State.Set(workflow.State{
		Status: status,
		Start:  time.Now().Add(-1 * time.Hour),
		End:    time.Now(),
	})
	return plan
}

func TestRender(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		plan           func() *workflow.Plan
		wantErr        bool
		wantContains   []string
		wantNotContain []string
	}{
		{
			name: "Success: renders plan with completed status",
			plan: func() *workflow.Plan {
				return makePlan(workflow.Completed)
			},
			wantContains: []string{
				"Test Plan",
				"A test plan for template rendering",
				"Plan Summary",
				"test block",
				"Completed",
			},
		},
		{
			name: "Success: renders plan with running status",
			plan: func() *workflow.Plan {
				plan := makePlan(workflow.Running)
				plan.State.Set(workflow.State{
					Status: workflow.Running,
					Start:  time.Now().Add(-1 * time.Hour),
				})
				return plan
			},
			wantContains: []string{
				"Test Plan",
				"Running",
			},
		},
		{
			name: "Success: renders plan with failed status",
			plan: func() *workflow.Plan {
				plan := makePlan(workflow.Failed)
				plan.State.Set(workflow.State{
					Status: workflow.Failed,
					Start:  time.Now().Add(-1 * time.Hour),
					End:    time.Now(),
				})
				return plan
			},
			wantContains: []string{
				"Test Plan",
				"Failed",
			},
		},
		{
			name: "Success: renders plan with multiple blocks",
			plan: func() *workflow.Plan {
				plan := makePlan(workflow.Completed)
				seq2 := makeSequence("second sequence", workflow.Completed,
					makeAction("another action", workflow.Completed, 1, false),
				)
				block2 := makeBlock("second block", workflow.Completed, []*workflow.Sequence{seq2})
				plan.Blocks = append(plan.Blocks, block2)
				return plan
			},
			wantContains: []string{
				"test block",
				"second block",
				"second sequence",
			},
		},
		{
			name: "Success: renders plan with actions having errors in attempts",
			plan: func() *workflow.Plan {
				action := makeAction("failing action", workflow.Failed, 3, true)
				seq := makeSequence("error sequence", workflow.Failed, action)
				block := makeBlock("error block", workflow.Failed, []*workflow.Sequence{seq})
				plan := makePlan(workflow.Failed)
				plan.Blocks = []*workflow.Block{block}
				return plan
			},
			wantContains: []string{
				"error sequence",
				"error block",
				"Failed",
			},
		},
		{
			name: "Success: renders plan without optional checks",
			plan: func() *workflow.Plan {
				seq := makeSequence("minimal sequence", workflow.Completed,
					makeAction("minimal action", workflow.Completed, 1, false),
				)
				block := &workflow.Block{
					ID:        newV7(),
					Name:      "minimal block",
					Descr:     "minimal block description",
					Sequences: []*workflow.Sequence{seq},
				}
				block.State.Set(workflow.State{Status: workflow.Completed})

				plan := &workflow.Plan{
					ID:         newV7(),
					Name:       "Minimal Plan",
					Descr:      "A minimal plan without optional checks",
					GroupID:    newV7(),
					SubmitTime: time.Now(),
					Blocks:     []*workflow.Block{block},
				}
				plan.State.Set(workflow.State{Status: workflow.Completed})
				return plan
			},
			wantContains: []string{
				"Minimal Plan",
				"minimal block",
				"minimal sequence",
			},
		},
		{
			name: "Success: renders plan with zero time values",
			plan: func() *workflow.Plan {
				plan := makePlan(workflow.NotStarted)
				plan.State.Set(workflow.State{Status: workflow.NotStarted})
				return plan
			},
			wantContains: []string{
				"Test Plan",
				"NotStarted",
			},
		},
	}

	for _, test := range tests {
		ctx := t.Context()
		plan := test.plan()

		fs, err := Render(ctx, plan)
		switch {
		case err == nil && test.wantErr:
			t.Errorf("[TestRender(%s)]: got err == nil, want err != nil", test.name)
			continue
		case err != nil && !test.wantErr:
			t.Errorf("[TestRender(%s)]: got err == %s, want err == nil", test.name, err)
			continue
		case err != nil:
			continue
		}

		planHTML, err := fs.ReadFile("plan.html")
		if err != nil {
			t.Errorf("[TestRender(%s)]: failed to read plan.html: %s", test.name, err)
			continue
		}

		htmlContent := string(planHTML)
		for _, want := range test.wantContains {
			if !strings.Contains(htmlContent, want) {
				t.Errorf("[TestRender(%s)]: plan.html does not contain %q", test.name, want)
			}
		}

		for _, notWant := range test.wantNotContain {
			if strings.Contains(htmlContent, notWant) {
				t.Errorf("[TestRender(%s)]: plan.html should not contain %q", test.name, notWant)
			}
		}
	}
}

func TestRenderSequenceTemplates(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	plan := makePlan(workflow.Completed)

	fs, err := Render(ctx, plan)
	if err != nil {
		t.Fatalf("[TestRenderSequenceTemplates]: Render failed: %s", err)
	}

	for _, block := range plan.Blocks {
		for _, seq := range block.Sequences {
			path := "sequences/" + seq.ID.String() + ".html"
			content, err := fs.ReadFile(path)
			if err != nil {
				t.Errorf("[TestRenderSequenceTemplates]: failed to read %s: %s", path, err)
				continue
			}

			htmlContent := string(content)
			if !strings.Contains(htmlContent, seq.Name) {
				t.Errorf("[TestRenderSequenceTemplates]: sequence html does not contain sequence name %q", seq.Name)
			}
			if !strings.Contains(htmlContent, "Sequence Details") {
				t.Errorf("[TestRenderSequenceTemplates]: sequence html does not contain 'Sequence Details'")
			}
		}
	}
}

func TestRenderActionTemplates(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	plan := makePlan(workflow.Completed)

	fs, err := Render(ctx, plan)
	if err != nil {
		t.Fatalf("[TestRenderActionTemplates]: Render failed: %s", err)
	}

	for _, block := range plan.Blocks {
		for _, seq := range block.Sequences {
			for _, action := range seq.Actions {
				path := "actions/" + action.ID.String() + ".html"
				content, err := fs.ReadFile(path)
				if err != nil {
					t.Errorf("[TestRenderActionTemplates]: failed to read %s: %s", path, err)
					continue
				}

				htmlContent := string(content)
				if !strings.Contains(htmlContent, action.Name) {
					t.Errorf("[TestRenderActionTemplates]: action html does not contain action name %q", action.Name)
				}
				if !strings.Contains(htmlContent, "Action Details") {
					t.Errorf("[TestRenderActionTemplates]: action html does not contain 'Action Details'")
				}
				if !strings.Contains(htmlContent, "Attempts") {
					t.Errorf("[TestRenderActionTemplates]: action html does not contain 'Attempts'")
				}
			}
		}
	}
}

func TestRenderAllStatuses(t *testing.T) {
	t.Parallel()

	statuses := []workflow.Status{
		workflow.NotStarted,
		workflow.Running,
		workflow.Completed,
		workflow.Failed,
	}

	for _, status := range statuses {
		ctx := t.Context()

		action := makeAction("status test action", status, 1, status == workflow.Failed)
		seq := makeSequence("status test sequence", status, action)
		block := makeBlock("status test block", status, []*workflow.Sequence{seq})

		plan := &workflow.Plan{
			ID:         newV7(),
			Name:       "Status Test Plan",
			Descr:      "Testing status: " + status.String(),
			GroupID:    newV7(),
			SubmitTime: time.Now(),
			Blocks:     []*workflow.Block{block},
		}
		plan.State.Set(workflow.State{Status: status})

		fs, err := Render(ctx, plan)
		if err != nil {
			t.Errorf("[TestRenderAllStatuses(%s)]: Render failed: %s", status, err)
			continue
		}

		planHTML, err := fs.ReadFile("plan.html")
		if err != nil {
			t.Errorf("[TestRenderAllStatuses(%s)]: failed to read plan.html: %s", status, err)
			continue
		}

		if !strings.Contains(string(planHTML), status.String()) {
			t.Errorf("[TestRenderAllStatuses(%s)]: plan.html does not contain status %q", status, status.String())
		}
	}
}
