// Package plugins provides a plugin registry and definition for Workflows.
package plugins

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gostdlib/ops/retry/exponential"
)

// Plugin is the interface that must be implemented by all plugins.
type Plugin interface {
	// Name returns the name of the plugin.
	Name() string
	// Execute executes the plugin.
	Execute(ctx context.Context, req any) (any, error)
	// ValidateReq validates the request object.
	ValidateReq(any) error
	// Request returns an empty request object.
	Request() any
	// Response returns an empty response object.
	Response() any
	// IsCheck returns true if the plugin is a check plugin. A check plugin
	// can be used as a PreCheck, PostCheck or ContCheck Action. It cannot be used
	// in a Sequeunce. A non-check plugin is the opposite.
	IsCheck() bool
	// RetryPlan returns the retry plan for the plugin so that when an Action wants to
	// retry a plugin, it can use the retry plan to determine how to retry the plugin.
	// You can build this easily in a few ways:
	// 1. Use exponential.Policy for a custom retry timetable.
	// 2. Use one of the pre-built retry plans like FastRetryPlan(), SecondsRetryPlan(), etc.
	RetryPlan(retries int) exponential.Policy
	// InitCheck is run after the registery is loaded. The plugin should do any necessary checks
	// to ensure that it is ready to be used. If the plugin is not ready, it should return an error.
	// This is useful for plugins that require local resources like a command line application to
	// be installed.
	Init() error
}

// FastRetryPlan returns a retry plan that is fast at first and then slows down.
//
// progression will be:
// 100ms, 200ms, 400ms, 800ms, 1.6s, 3.2s, 6.4s, 12.8s, 25.6s, 51.2s, 60s
// Not counting a randomization factor which will be +/- up to 50% of the interval.
func FastRetryPlan() exponential.Policy {
	return exponential.Policy{
		InitialInterval:     100 * time.Millisecond,
		Multiplier:          2,
		RandomizationFactor: 0.5,
		MaxInterval:         60 * time.Second,
	}
}

// SecondsRetryPlan returns a retry plan that  moves in 1 second intervals up to 60 seconds.
//
// progression will be:
// 1s, 2s, 4s, 8s, 16s, 32s, 60s
// Not counting a randomization factor which will be +/- up to 50% of the interval.
func SecondsRetryPlan() exponential.Policy {
	return exponential.Policy{
		InitialInterval:     1 * time.Second,
		Multiplier:          2,
		RandomizationFactor: 0.5,
		MaxInterval:         60 * time.Second,
	}
}

// ThirtySecondsRetryPlan returns a retry plan that moves in 30 second intervals up to 5 minutes.
//
// progression will be:
// 30s, 33s, 36s, 40s, 44s, 48s, 53s, 58s, 64s, 70s, 77s, 85s, 94s, 103s, 113s, 124s, 136s, 150s,
// 165s, 181s, 199s, 219s, 241s, 265s, 292s, 300s
// Not counting a randomization factor which will be +/- up to 20% of the interval.
func ThirtySecondsRetryPlan() exponential.Policy {
	return exponential.Policy{
		InitialInterval:     30 * time.Second,
		Multiplier:          1.1,
		RandomizationFactor: 0.2,
		MaxInterval:         5 * time.Minute,
	}
}

// validatePolicy validates the exponential policy. This is a copy of the exponential.Policy.validate method.
// TODO(element-of-surprise): Remove this when the exponential package is updated to export the validate method.
func validatePolicy(p exponential.Policy) error {
	if p.InitialInterval <= 0 {
		return errors.New("Policy.InitialInterval must be greater than 0")
	}
	if p.Multiplier <= 1 {
		return errors.New("Policy.Multiplier must be greater than 1")
	}
	if p.RandomizationFactor < 0 || p.RandomizationFactor > 1 {
		return errors.New("Policy.RandomizationFactor must be between 0 and 1")
	}
	if p.MaxInterval <= 0 {
		return errors.New("Policy.MaxInterval must be greater than 0")
	}
	if p.InitialInterval > p.MaxInterval {
		return errors.New("Policy.InitialInterval must be less than or equal to Policy.MaxInterval")
	}
	return nil
}

// Registry is the global registry of plugins. It is only intended to be used
// during initialization, any other use can result in undefined behavior.
var Registry = registry{
	m: map[string]Plugin{},
}

type registry struct {
	m map[string]Plugin
}

// Register registers a plugin by name. It panics if the name is empty, the plugin is nil,
// or a plugin is already registered with the same name. This can only be called during
// init, otherwise the behavior is undefined. Not safe for concurrent use.
func (r *registry) Register(p Plugin) {
	if p == nil {
		panic("plugin is nil")
	}

	if strings.TrimSpace(p.Name()) == "" {
		panic("name is empty")
	}

	if r.m == nil {
		panic("bug: Registry not initialized")
	}

	if _, ok := r.m[p.Name()]; ok {
		panic(fmt.Sprintf("plugin(%s) already registered", p.Name()))
	}

	r.m[p.Name()] = p
}

// Get returns a plugin by name. It returns nil if the plugin is not found.
func (r *registry) Get(name string) Plugin {
	if r == nil || r.m == nil {
		return nil
	}
	return r.m[name]
}
