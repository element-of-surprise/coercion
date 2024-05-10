package main

import (
	"bytes"
	"context"
	"flag"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/element-of-surprise/coercion/workflow"
	html "github.com/element-of-surprise/coercion/workflow/utils/html/reports"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/filesystem"
	"github.com/google/uuid"
)

var port = flag.String("port", ":8080", "port to listen on")
var download = flag.Bool("download", false, "if true, will download the report instead of serving it.")

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
			ID:   uuid.New(),
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
			ID: uuid.New(),
			Actions: []*workflow.Action{
				actionWithAttempts("Verify User Permissions", workflow.Completed, 3),
			},
		},
		ContChecks: &workflow.Checks{
			ID: uuid.New(),
			Actions: []*workflow.Action{
				actionWithAttempts("Check Site is Reliable", workflow.Completed, 1),
				actionWithAttempts("Check Network Connectivity", workflow.Completed, 1),
			},
		},
		Blocks: []*workflow.Block{
			{
				ID:    uuid.New(),
				Name:  "Initialize Environment",
				State: &workflow.State{Status: workflow.Running},
				PreChecks: &workflow.Checks{
					ID:    uuid.New(),
					State: &workflow.State{Status: workflow.Completed},
					Actions: []*workflow.Action{
						actionWithAttempts("Check Cloud Credentials", workflow.Completed, 2),
					},
				},
				Sequences: []*workflow.Sequence{
					{
						ID:    uuid.New(),
						Name:  "Setup Kubernetes Cluster",
						State: &workflow.State{Status: workflow.Completed},
						Actions: []*workflow.Action{
							actionWithAttempts("Setup Kubernetes Cluster", workflow.Running, 2),
						},
					},
				},
				PostChecks: &workflow.Checks{
					ID:    uuid.New(),
					State: &workflow.State{Status: workflow.Completed},
					Actions: []*workflow.Action{
						actionWithAttempts("Validate Cluster Configuration", workflow.Completed, 1),
					},
				},
			},
		},
		PostChecks: &workflow.Checks{
			ID:      uuid.New(),
			State:   &workflow.State{Status: workflow.Completed},
			Actions: []*workflow.Action{actionWithAttempts("Cleanup Temporary Files", workflow.Completed, 1)},
		},
	}
	return plan
}

func main() {
	flag.Parse()

	if *download {
		os.Remove(("report.tar.gz"))

		f, err := os.Create("report.tar.gz")
		if err != nil {
			panic(err)
		}
		defer f.Close()

		b, err := html.Download(context.Background(), makePlan())
		if err != nil {
			panic(err)
		}
		if _, err = io.Copy(f, bytes.NewReader(b)); err != nil {
			panic(err)
		}
		return
	}

	fs, err := html.Render(context.Background(), makePlan())
	if err != nil {
		panic(err)
	}

	app := fiber.New()
	app.Use("/", filesystem.New(filesystem.Config{
		Root:  http.FS(fs),
		Index: "plan.html",
	}))
	app.Listen(*port)

}
