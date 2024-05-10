package plugins

import (
	"context"
	"fmt"

	"github.com/element-of-surprise/coercion/plugins"
	"github.com/gostdlib/ops/retry/exponential"
)

const HelloPluginName = "github.com/element-of-surprise/coercion/workflow/storage/sqlite/testing/plugins.HelloPlugin"
const CheckPluginName = "github.com/element-of-surprise/coercion/workflow/storage/sqlite/testing/plugins.CheckPlugin"

type HelloReq struct {
	Say string
}

type HelloResp struct {
	Said string
}

var _ plugins.Plugin = &HelloPlugin{}

type HelloPlugin struct{}

// Name returns the name of the plugin.
func (h *HelloPlugin) Name() string {
	return HelloPluginName
}

// Execute executes the plugin.
func (h *HelloPlugin) Execute(ctx context.Context, req any) (any, *plugins.Error) {
	if err := h.ValidateReq(req); err != nil {
		return nil, &plugins.Error{Message: err.Error(), Permanent: true}
	}
	r := req.(*HelloReq)
	return &HelloResp{Said: r.Say}, nil
}

// ValidateReq validates the request object.
func (h *HelloPlugin) ValidateReq(a any) error {
	if _, ok := a.(HelloReq); !ok {
		return fmt.Errorf("invalid request object(%T)", a)
	}
	if a.(HelloReq).Say == "" {
		return fmt.Errorf("Say is empty")
	}
	return nil
}

// Request returns an empty request object.
func (h *HelloPlugin) Request() any {
	return HelloReq{}
}

// Response returns an empty response object.
func (h *HelloPlugin) Response() any {
	return HelloResp{}
}

// IsCheck returns true if the plugin is a check plugin. A check plugin
// can be used as a PreCheck, PostCheck or ContCheck Action. It cannot be used
// in a Sequeunce. A non-check plugin is the opposite.
func (h *HelloPlugin) IsCheck() bool {
	return false
}

// RetryPolicy returns the retry plan for the plugin so that when an Action wants to
// retry a plugin, it can use the retry plan to determine how to retry the plugin.
// You can build this easily in a few ways:
// 1. Use exponential.Policy for a custom retry timetable.
// 2. Use one of the pre-built retry plans like FastRetryPolicy(), SecondsRetryPolicy(), etc.
func (h *HelloPlugin) RetryPolicy() exponential.Policy {
	return plugins.FastRetryPolicy()
}

// InitCheck is run after the registery is loaded. The plugin should do any necessary checks
// to ensure that it is ready to be used. If the plugin is not ready, it should return an error.
// This is useful for plugins that require local resources like a command line application to
// be installed.
func (h *HelloPlugin) Init() error {
	return nil
}

var _ plugins.Plugin = &CheckPlugin{}

type CheckPlugin struct{}

// Name returns the name of the plugin.
func (c *CheckPlugin) Name() string {
	return CheckPluginName
}

func (c *CheckPlugin) Execute(ctx context.Context, req any) (any, *plugins.Error) {
	return nil, nil
}

func (c *CheckPlugin) ValidateReq(a any) error {
	if a != nil {
		return fmt.Errorf("invalid request object(%T)", a)
	}
	return nil
}

func (c *CheckPlugin) Request() any {
	return nil
}

func (c *CheckPlugin) Response() any {
	return nil
}

func (c *CheckPlugin) IsCheck() bool {
	return true
}

func (c *CheckPlugin) RetryPolicy() exponential.Policy {
	return plugins.FastRetryPolicy()
}

func (c *CheckPlugin) Init() error {
	return nil
}
