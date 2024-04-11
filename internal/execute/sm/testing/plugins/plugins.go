package plugins

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/element-of-surprise/workstream/plugins"
	"github.com/gostdlib/ops/retry/exponential"
)

// Name is the name of the testing plugin.
const Name = "github.com/element-of-surprise/workstream/internal/execute/sm/testing/plugins.Testing"

type Req struct {
	// Arg is a placeholder.
	Arg string
	// Sleep is a duration to sleep before returning.
	Sleep time.Duration
	// FailValidation is a flag to indicate if the request should fail validation.
	FailValidation bool
}

type Resp struct {
	// Arg is a placeholder.
	Arg string
}

var _ plugins.Plugin = &Plugin{}

type Plugin struct {
	// IsCheckPlugin is a flag to indicate if the plugin is a check plugin.
	IsCheckPlugin bool
	// Responses is a list of responses to return.
	Responses []any // This is a list of responses, if *plugins.Error, will be returned as an error
	// AlwaysRespond indicates to ignore Responses and always return a non-error response.
	AlwaysRespond bool

	// at is the current index of the response.
	at atomic.Int64
}

// Name returns the name of the plugin.
func (h *Plugin) Name() string {
	return Name
}

// Execute executes the plugin.
func (h *Plugin) Execute(ctx context.Context, req any) (any, *plugins.Error) {
	at := h.at.Add(1) - 1
	r, ok := req.(Req)
	if !ok {
		panic("invalid request object")
	}

	time.Sleep(r.Sleep)
	if h.AlwaysRespond {
		if r.Arg == "error" {
			return nil, &plugins.Error{Message: "error"}
		}
		return Resp{Arg: "ok"}, nil
	}

	if err, ok := h.Responses[at].(*plugins.Error); ok {
		return nil, err
	}
	return h.Responses[at], nil
}

// ValidateReq validates the request object.
func (h *Plugin) ValidateReq(a any) error {
	if _, ok := a.(Req); !ok {
		return fmt.Errorf("invalid request object(%T)", a)
	}
	if a.(Req).FailValidation {
		return fmt.Errorf("DoNotValidate is true")
	}
	return nil
}

// Request returns an empty request object.
func (h *Plugin) Request() any {
	return Req{}
}

// Response returns an empty response object.
func (h *Plugin) Response() any {
	return Resp{}
}

// IsCheck returns true if the plugin is a check plugin. A check plugin
// can be used as a PreCheck, PostCheck or ContCheck Action. It cannot be used
// in a Sequeunce. A non-check plugin is the opposite.
func (h *Plugin) IsCheck() bool {
	return h.IsCheckPlugin
}

// RetryPolicy returns the retry plan for the plugin so that when an Action wants to
// retry a plugin, it can use the retry plan to determine how to retry the plugin.
// You can build this easily in a few ways:
// 1. Use exponential.Policy for a custom retry timetable.
// 2. Use one of the pre-built retry plans like FastRetryPolicy(), SecondsRetryPolicy(), etc.
func (h *Plugin) RetryPolicy() exponential.Policy {
	return plugins.FastRetryPolicy()
}

// InitCheck is run after the registery is loaded. The plugin should do any necessary checks
// to ensure that it is ready to be used. If the plugin is not ready, it should return an error.
// This is useful for plugins that require local resources like a command line application to
// be installed.
func (h *Plugin) Init() error {
	return nil
}