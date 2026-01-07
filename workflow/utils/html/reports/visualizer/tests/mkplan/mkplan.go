package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/element-of-surprise/coercion/workflow"
	"github.com/go-json-experiment/json"
	"github.com/google/uuid"
)

func NewV7() uuid.UUID {
	for {
		id, err := uuid.NewV7()
		if err == nil {
			return id
		}
	}
}

func main() {
	p := makePlan()

	b, err := json.Marshal(p)
	if err != nil {
		panic(err)
	}

	dir, err := os.Executable()
	if err != nil {
		panic(err)
	}
	dir = filepath.Dir(dir)

	if err := os.WriteFile(dir+"/plan.json", b, 0644); err != nil {
		panic(err)
	}

	fmt.Println("Plan written to plan.json")
}

type Resp struct {
	FieldA string
}

func makePlan() *workflow.Plan {
	// Example Action with no "Output" or "Number", just a simple Status
	actionWithAttempts := func(name string, status workflow.Status, numAttempts int) *workflow.Action {
		attempts := make([]workflow.Attempt, numAttempts)
		for i := range attempts {
			attempts[i] = workflow.Attempt{
				Resp: Resp{
					FieldA: "FieldA",
				},
			}
		}
		action := &workflow.Action{
			ID:   NewV7(),
			Name: name,
		}
		action.Attempts.Set(attempts)
		action.State.Set(workflow.State{Status: status})
		return action
	}

	preChecks := &workflow.Checks{
		ID: NewV7(),
		Actions: []*workflow.Action{
			actionWithAttempts("Verify User Permissions", workflow.Completed, 3),
		},
	}

	contChecks := &workflow.Checks{
		ID: NewV7(),
		Actions: []*workflow.Action{
			actionWithAttempts("Check Site is Reliable", workflow.Completed, 1),
			actionWithAttempts("Check Network Connectivity", workflow.Completed, 1),
		},
	}

	blockPreChecks := &workflow.Checks{
		ID: NewV7(),
		Actions: []*workflow.Action{
			actionWithAttempts("Check Cloud Credentials", workflow.Completed, 2),
		},
	}
	blockPreChecks.State.Set(workflow.State{Status: workflow.Completed})

	seq := &workflow.Sequence{
		ID:   NewV7(),
		Name: "Setup Kubernetes Cluster",
		Actions: []*workflow.Action{
			actionWithAttempts("Setup Kubernetes Cluster", workflow.Running, 2),
		},
	}
	seq.State.Set(workflow.State{Status: workflow.Completed})

	blockPostChecks := &workflow.Checks{
		ID: NewV7(),
		Actions: []*workflow.Action{
			actionWithAttempts("Validate Cluster Configuration", workflow.Completed, 1),
		},
	}
	blockPostChecks.State.Set(workflow.State{Status: workflow.Completed})

	block := &workflow.Block{
		ID:         NewV7(),
		Name:       "Initialize Environment",
		PreChecks:  blockPreChecks,
		Sequences:  []*workflow.Sequence{seq},
		PostChecks: blockPostChecks,
	}
	block.State.Set(workflow.State{Status: workflow.Running})

	postChecks := &workflow.Checks{
		ID:      NewV7(),
		Actions: []*workflow.Action{actionWithAttempts("Cleanup Temporary Files", workflow.Completed, 1)},
	}
	postChecks.State.Set(workflow.State{Status: workflow.Completed})

	// Sample workflow plan generation
	plan := &workflow.Plan{
		ID:         NewV7(),
		Name:       "Complex Deployment Plan",
		Descr:      "This plan deploys multiple microservices in a staged approach.",
		GroupID:    NewV7(),
		SubmitTime: time.Now(),
		PreChecks:  preChecks,
		ContChecks: contChecks,
		Blocks:     []*workflow.Block{block},
		PostChecks: postChecks,
	}
	plan.State.Set(workflow.State{Status: workflow.Running})
	return plan
}
