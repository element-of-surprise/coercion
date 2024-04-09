package sm

import (
	"testing"
	"time"

	"github.com/element-of-surprise/workstream/workflow"
	"github.com/kylelemons/godebug/pretty"
)

func TestResetActions(t *testing.T) {
	t.Parallel()

	action := &workflow.Action{
		Name: "action",
		State: &workflow.State{
			Status: workflow.Running,
			Start:  time.Now(),
			End:    time.Now(),
		},
		Attempts: []*workflow.Attempt{
			{},
		},
	}

	want := &workflow.Action{
		Name: "action",
		State: &workflow.State{
			Status: workflow.NotStarted,
			Start:  time.Time{},
			End:    time.Time{},
		},
		Attempts: nil,
	}

	resetActions([]*workflow.Action{action})

	if diff := pretty.Compare(want, action); diff != "" {
		t.Errorf("TestResetActions: -want +got):\n%s", diff)
	}
}
