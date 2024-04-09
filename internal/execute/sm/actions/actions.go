package actions

import (
	"context"
	"fmt"
	"log"
	"reflect"
	"time"

	"github.com/element-of-surprise/workstream/plugins"
	"github.com/element-of-surprise/workstream/plugins/registry"
	"github.com/element-of-surprise/workstream/workflow"
	"github.com/element-of-surprise/workstream/workflow/storage"

	"github.com/gostdlib/ops/retry/exponential"
	"github.com/gostdlib/ops/statemachine"
)

// Data is the data passed to the state machine.
type Data struct {
	// Action is the action to run.
	Action *workflow.Action
	// Updater is the storage.ActionUpdater to write the action to.
	Updater storage.ActionUpdater
	// Registry is the registry to get the plugin from.
	Registry *registry.Register

	// plugin is the plugin to run. This is set by the GetPlugin state.
	plugin plugins.Plugin
	// err is the error that occurred during the state machine. As all states must
	// call the End state, this is the error that will be returned.
	err error
}

type nower func() time.Time

// Runner is a state machine that runs a workflow.Action.
type Runner struct {
	nower nower
}

// Start stats the statemachine and marks the action as running.
func (r Runner) Start(req statemachine.Request[Data]) statemachine.Request[Data] {
	action := req.Data.Action
	updater := req.Data.Updater

	action.State.Start = r.now()
	action.State.Status = workflow.Running

	if err := updater.UpdateAction(req.Ctx, action); err != nil {
		log.Fatalf("failed to write Action: %v", err)
	}

	req.Next = r.GetPlugin
	return req
}

// pluginNotFoundErr returns an error for when a plugin is not found.
// This allows tests to check for this specific error without worrying
// about the text changing.
func pluginNotFoundErr(name string) error {
	return fmt.Errorf("plugin %s not found", name)
}

func (r Runner) GetPlugin(req statemachine.Request[Data]) statemachine.Request[Data] {
	action := req.Data.Action

	p := req.Data.Registry.Plugin(action.Plugin)
	// This is defense in depth. The plugin should be checked when the Plan is created.
	if p == nil {
		req.Data.err = pluginNotFoundErr(action.Plugin)
		req.Next = r.End
		return req
	}

	req.Data.plugin = p
	req.Next = r.Execute
	return req
}

// Execute runs the action using the plugin and writes the result to the store. This
// function will retry the action based on the plugin's retry policy.
func (r Runner) Execute(req statemachine.Request[Data]) statemachine.Request[Data] {
	action := req.Data.Action
	plugin := req.Data.plugin
	writer := req.Data.Updater

	backoff, err := exponential.New(
		exponential.WithPolicy(req.Data.plugin.RetryPolicy()),
	)
	// This should be protected by upper level code. If it fails, we should panic.
	if err != nil {
		log.Fatalf("failed to create backoff policy: %v", err)
	}

	req.Data.err = backoff.Retry(
		req.Ctx,
		func(ctx context.Context, record exponential.Record) error {
			return r.exec(ctx, action, plugin, writer)
		},
	)
	req.Next = r.End
	return req
}

// End marks the end of the action and handles writing the final state to the store.
// If any error was recorded in the Data object, it will be promoted as the error of the Request.
func (r Runner) End(req statemachine.Request[Data]) statemachine.Request[Data] {
	action := req.Data.Action
	updater := req.Data.Updater

	action.State.Status = workflow.Completed
	if req.Data.err != nil {
		action.State.Status = workflow.Failed
	}

	action.State.End = r.now()

	if err := updater.UpdateAction(req.Ctx, action); err != nil {
		log.Fatalf("failed to write Action: %v", err)
	}
	req.Err = req.Data.err
	return req
}

// pluginTimeoutMsg is the message returned when a plugin times out. Set here
// to syncronize changes with test code.
const pluginTimeoutMsg = "plugin execution timed out"

// unexpectedTypeMsg returns a message for when a plugin returns an unexpected response type.
// This is used to syncronize changes with test code.
func unexpectedTypeMsg(plugin plugins.Plugin, got, want any) string {
	return fmt.Sprintf("plugin(%s) returned a type %T but expected %T", plugin.Name(), got, want)
}

// exec runs the action once using the plugin and writes the result to the store, unless the action
// has exceeded the maximum number of retries. In that case, it returns a permanent error.
func (r Runner) exec(ctx context.Context, action *workflow.Action, plugin plugins.Plugin, updater storage.ActionUpdater) error {
	if len(action.Attempts) > action.Retries {
		return exponential.ErrPermanent
	}

	defer func() {
		if err := updater.UpdateAction(ctx, action); err != nil {
			log.Fatalf("failed to write Action: %v", err)
		}
	}()

	attempt := &workflow.Attempt{
		Start: r.now(),
	}
	defer func() {
		action.Attempts = append(action.Attempts, attempt)
	}()

	runCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), action.Timeout)
	plugResp := run(runCtx, plugin, action.Req)
	cancel()
	attempt.End = r.now()

	if plugResp.timeout {
		attempt.Err = &plugins.Error{
			Message: pluginTimeoutMsg,
			Permanent: false,
		}
		return attempt.Err
	}else{
		attempt.Resp = plugResp.Resp
		attempt.Err = plugResp.Err
	}

	// We make sure the response is the expected type. If not, we return a permanent error.
	// This case means the plugin is not behaving as expected and we should avoid conversion panics
	// by not returning the junk they gave us.
	if attempt.Err == nil {
		expect := plugin.Response()
		if isType(attempt.Resp, expect) {
			return nil
		}
		attempt.Err = &plugins.Error{
			Message: unexpectedTypeMsg(plugin, attempt.Resp, expect),
			Permanent: true,
		}
		attempt.Resp = nil
	}
	if attempt.Err.Permanent {
		return errPermanent(attempt.Err)
	}
	return attempt.Err
}

func errPermanent(err *plugins.Error) error {
	return fmt.Errorf("%w: %w", exponential.ErrPermanent, err)
}

func (r Runner) now() time.Time {
	if r.nower == nil {
		return time.Now()
	}
	return r.nower()
}

type plugResp struct {
	Resp any
	Err *plugins.Error
	timeout bool
}

// run executes the plugin in a goroutine and returns the response or an error if the context is done.
func run(ctx context.Context, plugin plugins.Plugin, req any) plugResp {
	ch := make(chan plugResp, 1)
	go func() {
		defer close(ch)

		plugResp := plugResp{}
		plugResp.Resp, plugResp.Err = plugin.Execute(ctx, req)
		ch <- plugResp
	}()

	select{
	case <-ctx.Done():
		return plugResp{timeout: true}
	case resp := <-ch:
		return resp
	}
}

func isType(a, b interface{}) bool {
    return reflect.TypeOf(a) == reflect.TypeOf(b)
}
