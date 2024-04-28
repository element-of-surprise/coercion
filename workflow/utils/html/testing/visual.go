package main

import (
	"net/http"
	"time"

	"github.com/element-of-surprise/workstream/workflow"
	html "github.com/element-of-surprise/workstream/workflow/utils/html"
	"github.com/google/uuid"
)

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
			Name: name,
			State: &workflow.State{
				Status: status,
			},
			Attempts: attempts,
		}
	}

	// Sample workflow plan generation
	plan := &workflow.Plan{
		ID:         uuid.New(),
		Name:       "Complex Deployment Plan",
		Descr:      "This plan deploys multiple microservices in a staged approach.",
		GroupID:    uuid.New(),
		SubmitTime: time.Now(),
		State:      &workflow.State{Status: workflow.Running},
		PreChecks: &workflow.Checks{
			Actions: []*workflow.Action{
				actionWithAttempts("Verify User Permissions", workflow.Completed, 3),
			},
		},
		ContChecks: &workflow.Checks{
			Actions: []*workflow.Action{
				actionWithAttempts("Check Site is Reliable", workflow.Completed, 1),
				actionWithAttempts("Check Network Connectivity", workflow.Completed, 1),
			},
		},
		Blocks: []*workflow.Block{
			{
				Name:  "Initialize Environment",
				State: &workflow.State{Status: workflow.Running},
				PreChecks: &workflow.Checks{
					State: &workflow.State{Status: workflow.Completed},
					Actions: []*workflow.Action{
						actionWithAttempts("Check Cloud Credentials", workflow.Completed, 2),
					},
				},
				Sequences: []*workflow.Sequence{
					{
						Name:  "Setup Kubernetes Cluster",
						State: &workflow.State{Status: workflow.Completed},
						Actions: []*workflow.Action{
							actionWithAttempts("Setup Kubernetes Cluster", workflow.Running, 2),
						},
					},
				},
				PostChecks: &workflow.Checks{
					State: &workflow.State{Status: workflow.Completed},
					Actions: []*workflow.Action{
						actionWithAttempts("Validate Cluster Configuration", workflow.Completed, 1),
					},
				},
			},
		},
		PostChecks: &workflow.Checks{
			State:   &workflow.State{Status: workflow.Completed},
			Actions: []*workflow.Action{actionWithAttempts("Cleanup Temporary Files", workflow.Completed, 1)},
		},
	}
	return plan
}

func main() {
	b, err := html.Render(makePlan())
	if err != nil {
		panic(err)
	}

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write(b.Bytes())
	})

	http.ListenAndServe(":8080", nil)
}
