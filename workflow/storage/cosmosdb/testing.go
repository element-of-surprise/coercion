package cosmosdb

import (
	"context"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"
	"github.com/go-json-experiment/json"
	"github.com/google/uuid"
	"github.com/kylelemons/godebug/pretty"

	pluglib "github.com/element-of-surprise/coercion/plugins"
	"github.com/element-of-surprise/coercion/plugins/registry"
	"github.com/element-of-surprise/coercion/workflow"
	"github.com/element-of-surprise/coercion/workflow/builder"
	"github.com/element-of-surprise/coercion/workflow/storage/sqlite/testing/plugins"
	"github.com/element-of-surprise/coercion/workflow/utils/clone"
	"github.com/element-of-surprise/coercion/workflow/utils/walk"
)

//+gocover:ignore:file No need to test fake store.

var testReg = registry.New()

func init() {
	testReg.MustRegister(&plugins.CheckPlugin{})
	testReg.MustRegister(&plugins.HelloPlugin{})
}

var prettyConfig = pretty.Config{
	PrintStringers:      true,
	PrintTextMarshalers: true,
	SkipZeroFields:      true,
}

type setters interface {
	// SetID is a setter for the ID field.
	SetID(uuid.UUID)
	// SetState is a setter for the State settings.
	SetState(*workflow.State)
}

type setPlanIDer interface {
	SetPlanID(uuid.UUID)
}

func NewTestPlan() *workflow.Plan {
	var plan *workflow.Plan
	ctx := context.Background()

	build, err := builder.New("test", "test", builder.WithGroupID(mustUUID()))
	if err != nil {
		panic(err)
	}

	checkAction1 := &workflow.Action{Name: "action1", Descr: "preCheckAction", Plugin: plugins.CheckPluginName, Req: nil}
	checkAction2 := &workflow.Action{Name: "action2", Descr: "contCheckAction", Plugin: plugins.CheckPluginName, Req: nil}
	checkAction3 := &workflow.Action{Name: "action3", Descr: "postCheckAction", Plugin: plugins.CheckPluginName, Req: nil}
	checkAction4 := &workflow.Action{Name: "action4", Descr: "deferredCheckAction", Plugin: plugins.CheckPluginName, Req: nil}
	checkAction5 := &workflow.Action{Name: "action5", Descr: "bypassCheckAction", Plugin: plugins.CheckPluginName, Req: nil}
	seqAction1 := &workflow.Action{
		Name:   "action",
		Descr:  "action",
		Plugin: plugins.HelloPluginName,
		Req:    plugins.HelloReq{Say: "hello"},
		Attempts: []*workflow.Attempt{
			{
				Err:   &pluglib.Error{Message: "internal error"},
				Start: time.Now().Add(-1 * time.Minute).UTC(),
				End:   time.Now().UTC(),
			},
			{
				Resp:  plugins.HelloResp{Said: "hello"},
				Start: time.Now().Add(-1 * time.Second).UTC(),
				End:   time.Now().UTC(),
			},
		},
	}

	build.AddChecks(builder.PreChecks, &workflow.Checks{})
	build.AddAction(clone.Action(ctx, checkAction1))
	build.Up()

	build.AddChecks(builder.ContChecks, &workflow.Checks{Delay: 32 * time.Second})
	build.AddAction(clone.Action(ctx, checkAction2))
	build.Up()

	build.AddChecks(builder.PostChecks, &workflow.Checks{})
	build.AddAction(clone.Action(ctx, checkAction3))
	build.Up()

	build.AddChecks(builder.DeferredChecks, &workflow.Checks{})
	build.AddAction(clone.Action(ctx, checkAction4))
	build.Up()

	build.AddChecks(builder.BypassChecks, &workflow.Checks{})
	build.AddAction(clone.Action(ctx, checkAction5))
	build.Up()

	build.AddBlock(builder.BlockArgs{
		Name:              "block",
		Descr:             "block",
		EntranceDelay:     1 * time.Second,
		ExitDelay:         1 * time.Second,
		ToleratedFailures: 1,
		Concurrency:       1,
	})

	build.AddChecks(builder.PreChecks, &workflow.Checks{})
	build.AddAction(checkAction1)
	build.Up()

	build.AddChecks(builder.ContChecks, &workflow.Checks{Delay: 1 * time.Minute})
	build.AddAction(checkAction2)
	build.Up()

	build.AddChecks(builder.PostChecks, &workflow.Checks{})
	build.AddAction(checkAction3)
	build.Up()

	build.AddChecks(builder.DeferredChecks, &workflow.Checks{})
	build.AddAction(checkAction4)
	build.Up()

	build.AddChecks(builder.BypassChecks, &workflow.Checks{})
	build.AddAction(checkAction5)
	build.Up()

	build.AddSequence(&workflow.Sequence{Name: "sequence", Descr: "sequence"})
	build.AddAction(seqAction1)
	build.Up()

	plan, err = build.Plan()
	if err != nil {
		panic(err)
	}

	plan.SubmitTime = time.Now().UTC()
	for item := range walk.Plan(context.Background(), plan) {
		setter := item.Value.(setters)
		setter.SetID(mustUUID())
		if item.Value.Type() != workflow.OTPlan {
			item.Value.(setPlanIDer).SetPlanID(plan.ID)
		}
		setter.SetState(
			&workflow.State{
				Status: workflow.Running,
				Start:  time.Now().UTC(),
				End:    time.Now().UTC(),
			},
		)
	}

	return plan
}

func mustUUID() uuid.UUID {
	id, err := uuid.NewV7()
	if err != nil {
		panic(err)
	}
	return id
}

func getIDsFromQueryParameters(params []azcosmos.QueryParameter) map[uuid.UUID]struct{} {
	for _, param := range params {
		if param.Name != "@ids" {
			continue
		}
		ids := map[uuid.UUID]struct{}{}
		for _, id := range param.Value.([]uuid.UUID) {
			ids[id] = struct{}{}
		}
		return ids
	}
	return nil
}

type commonFields struct {
	ID          uuid.UUID           `json:"id"`
	PlanID      uuid.UUID           `json:"planID"`  // not set on a Type plan
	GroupID     uuid.UUID           `json:"groupID"` // not set except on Type plan
	Name        string              `json:"name"`
	Descr       string              `json:"descr"`
	Type        workflow.ObjectType `json:"type"`
	StateStatus workflow.Status     `json:"stateStatus"`
	SubmitTime  time.Time           `json:"submitTime"` // not set except on Type plan
	StateStart  time.Time           `json:"stateStart"`
	StateEnd    time.Time           `json:"stateEnd"`
}

// getCommonFields gets fields that are common to all Coercion workflow "entry"objects.
// entry objects are what we store this in within cosmosDB (planEntry, checksEntry, ...).
func getCommonFields(data []byte) (commonFields, error) {
	var c commonFields
	if err := json.Unmarshal(data, &c); err != nil {
		return c, err
	}
	return c, nil
}
