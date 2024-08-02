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
		attempts := make([]*workflow.Attempt, numAttempts)
		for i := range attempts {
			attempts[i] = &workflow.Attempt{
				Resp: Resp{
					FieldA: "FieldA",
				},
			}
		}
		return &workflow.Action{
			ID:   NewV7(),
			Name: name,
			State: &workflow.State{
				Status: status,
			},
			Attempts: attempts,
		}
	}

	// Sample workflow plan generation
	plan := &workflow.Plan{
		ID:         NewV7(),
		Name:       "Complex Deployment Plan",
		Descr:      "This plan deploys multiple microservices in a staged approach.",
		GroupID:    NewV7(),
		SubmitTime: time.Now(),
		State:      &workflow.State{Status: workflow.Running},
		PreChecks: &workflow.Checks{
			ID: NewV7(),
			Actions: []*workflow.Action{
				actionWithAttempts("Verify User Permissions", workflow.Completed, 3),
			},
		},
		ContChecks: &workflow.Checks{
			ID: NewV7(),
			Actions: []*workflow.Action{
				actionWithAttempts("Check Site is Reliable", workflow.Completed, 1),
				actionWithAttempts("Check Network Connectivity", workflow.Completed, 1),
			},
		},
		Blocks: []*workflow.Block{
			{
				ID:    NewV7(),
				Name:  "Initialize Environment",
				State: &workflow.State{Status: workflow.Running},
				PreChecks: &workflow.Checks{
					ID:    NewV7(),
					State: &workflow.State{Status: workflow.Completed},
					Actions: []*workflow.Action{
						actionWithAttempts("Check Cloud Credentials", workflow.Completed, 2),
					},
				},
				Sequences: []*workflow.Sequence{
					{
						ID:    NewV7(),
						Name:  "Setup Kubernetes Cluster",
						State: &workflow.State{Status: workflow.Completed},
						Actions: []*workflow.Action{
							actionWithAttempts("Setup Kubernetes Cluster", workflow.Running, 2),
						},
					},
				},
				PostChecks: &workflow.Checks{
					ID:    NewV7(),
					State: &workflow.State{Status: workflow.Completed},
					Actions: []*workflow.Action{
						actionWithAttempts("Validate Cluster Configuration", workflow.Completed, 1),
					},
				},
			},
		},
		PostChecks: &workflow.Checks{
			ID:      NewV7(),
			State:   &workflow.State{Status: workflow.Completed},
			Actions: []*workflow.Action{actionWithAttempts("Cleanup Temporary Files", workflow.Completed, 1)},
		},
	}
	return plan
}
