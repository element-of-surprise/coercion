/*
package registry provides a registry of plugins. This is used to register plugins
that will be used by a workstream plan.

Usage:

	package main

	import (
		"github.com/element-of-surprise/plugins/github" // Doesn't really exist, example name
		"github.com/element-of-surprise/workstream/registry"
	)

	func main() {
		reg := registry.New()
		if err := reg.Register(github.New()); err != nil {
			// handle error
		}
		...
	}
*/
package registry

import (
	"errors"
	"fmt"
	"strings"

	"github.com/element-of-surprise/workstream/plugins"
	"github.com/gostdlib/ops/retry/exponential"
)

// Register provides a Register for plugins. This should not be used directly by the user,
// but instead via the Registry variable. Use of this type directly is not supported.
type Register struct {
	m map[string]plugins.Plugin
}

// New creates a new Register. Not for use by the user.
func New() *Register {
	return &Register{
		m: map[string]plugins.Plugin{},
	}
}

// Register registers a plugin by name. It panics if the name is empty, the plugin is nil,
// or a plugin is already registered with the same name. This can only be called during
// init, otherwise the behavior is undefined. Not safe for concurrent use.
func (r *Register) Register(p plugins.Plugin) error {
	if p == nil {
		return fmt.Errorf("plugin is nil")
	}

	if strings.TrimSpace(p.Name()) == "" {
		return fmt.Errorf("name is empty")
	}

	if r.m == nil {
		return fmt.Errorf("bug: Registry not initialized")
	}

	if _, ok := r.m[p.Name()]; ok {
		return fmt.Errorf("plugin(%s) already registered", p.Name())
	}

	if err := validatePolicy(p.RetryPolicy()); err != nil {
		return fmt.Errorf("plugin(%s) has invalid retry plan: %v", p.Name(), err)
	}

	r.m[p.Name()] = p
	return nil
}

// MustRegister registers a plugin by name. It panics if their is an error
// registering the plugin.
func (r *Register) MustRegister(p plugins.Plugin) {
	if err := r.Register(p); err != nil {
		panic(err)
	}
}

// Plugins returns a channel of all the plugins in the registry.
func (r *Register) Plugins() chan plugins.Plugin {
	ch := make(chan plugins.Plugin, 1)
	go func() {
		for _, p := range r.m {
			ch <- p
		}
		close(ch)
	}()
	return ch
}

// Plugin returns a plugin by name. It returns nil if the plugin is not found.
func (r *Register) Plugin(name string) plugins.Plugin {
	if r == nil || r.m == nil {
		return nil
	}
	return r.m[name]
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
