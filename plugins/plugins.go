// Package plugins provides the Plugin interface that must be implemented by all plugins.
package plugins

import (
	"context"
	"time"

	"github.com/gostdlib/ops/retry/exponential"
)

// ErrCode is the type for error codes that are returned by plugins.:w
type ErrCode uint

//go:generate stringer -type=ErrCode

// ECUnknown is the error code for an unknown error.
const ECUnknown ErrCode = 0

// Error is an error that is returned by a plugin. This implements the error interface.
// However, it is not used as an error type in plugin methods, as an error interface cannot be encoded
// and decoded by various encoding packages.
type Error struct {
	// Code is the error code that is returned by the plugin, with 0 representing unknown.
	// This is an error code specific to the plugin and all plugins should define their own error codes.
	Code ErrCode
	// Message is the error message that is returned by the plugin.
	Message string
	// Permanent is true if the error is permanent and should not be retried.
	Permanent bool
	// Wrapped is the error that is wrapped by the plugin error.
	Wrapped *Error
}

// Error implements the error interface.
func (e *Error) Error() string {
	return e.Message
}

// Unwrap implements the errors.Wrapper interface.
func (e *Error) Unwrap() error {
	if e.Wrapped == nil {
		return nil
	}
	return e.Wrapped
}

// Plugin is the interface that must be implemented by all plugins.
type Plugin interface {
	// Name returns the name of the plugin. This must be unique in the registry.
	// The name should include the package path to avoid name collisions.
	Name() string
	// Execute executes the plugin.
	Execute(ctx context.Context, req any) (any, *Error)
	// ValidateReq validates the request object. This must check that the request object
	// is the same as the object returned by Request().
	ValidateReq(req any) error
	// Request returns an empty request object.
	Request() any
	// Response returns an empty response object.
	Response() any
	// IsCheck returns true if the plugin is a check plugin. A check plugin
	// can be used as a PreCheck, PostCheck or ContCheck Action. It cannot be used
	// in a Sequeunce. A non-check plugin is the opposite.
	IsCheck() bool
	// RetryPolicy returns the retry plan for the plugin so that when an Action wants to
	// retry a plugin, it can use the retry plan to determine how to retry the plugin.
	// You can build this easily in a few ways:
	// 1. Use exponential.Policy for a custom retry timetable.
	// 2. Use one of the pre-built retry plans like FastRetryPolicy(), SecondsRetryPolicy(), etc.
	RetryPolicy() exponential.Policy
	// InitCheck is run after the registery is loaded. The plugin should do any necessary checks
	// to ensure that it is ready to be used. If the plugin is not ready, it should return an error.
	// This is useful for plugins that require local resources like a command line application to
	// be installed.
	Init() error
}

// FastRetryPolicy returns a retry plan that is fast at first and then slows down.
//
// progression will be:
// 100ms, 200ms, 400ms, 800ms, 1.6s, 3.2s, 6.4s, 12.8s, 25.6s, 51.2s, 60s
// Not counting a randomization factor which will be +/- up to 50% of the interval.
func FastRetryPolicy() exponential.Policy {
	return exponential.Policy{
		InitialInterval:     100 * time.Millisecond,
		Multiplier:          2,
		RandomizationFactor: 0.5,
		MaxInterval:         60 * time.Second,
	}
}

// SecondsRetryPolicy returns a retry plan that  moves in 1 second intervals up to 60 seconds.
//
// progression will be:
// 1s, 2s, 4s, 8s, 16s, 32s, 60s
// Not counting a randomization factor which will be +/- up to 50% of the interval.
func SecondsRetryPolicy() exponential.Policy {
	return exponential.Policy{
		InitialInterval:     1 * time.Second,
		Multiplier:          2,
		RandomizationFactor: 0.5,
		MaxInterval:         60 * time.Second,
	}
}

// ThirtySecondsRetryPolicy returns a retry plan that moves in 30 second intervals up to 5 minutes.
//
// progression will be:
// 30s, 33s, 36s, 40s, 44s, 48s, 53s, 58s, 64s, 70s, 77s, 85s, 94s, 103s, 113s, 124s, 136s, 150s,
// 165s, 181s, 199s, 219s, 241s, 265s, 292s, 300s
// Not counting a randomization factor which will be +/- up to 20% of the interval.
func ThirtySecondsRetryPolicy() exponential.Policy {
	return exponential.Policy{
		InitialInterval:     30 * time.Second,
		Multiplier:          1.1,
		RandomizationFactor: 0.2,
		MaxInterval:         5 * time.Minute,
	}
}
