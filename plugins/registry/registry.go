/*
package registry provides the global registry of plugins. This is used to register plugins
during plugin initialization.

Usage should happen in a plugin during init():

	package myplugin

	import (
		"github.com/element-of-surprise/workstream/plugins"
		"github.com/element-of-surprise/workstream/registry"
		...
	)

	func init() {
		registry.Plugins.Register(&myPlugin{})
	}

	var _ plugins.Plugin = &myPlugin{}

	type myPlugin struct {
		...
	}
	...
*/
package registry

import (
	"errors"
	"fmt"
	"strings"

	"github.com/element-of-surprise/workstream/plugins"
	"github.com/gostdlib/ops/retry/exponential"
)

// Plugins is the global registry of plugins. It is only intended to be used
// during initialization, any other use can result in undefined behavior.
var Plugins = &Register{
	m: map[string]plugins.Plugin{},
}

// Register provides a Register for plugins. This should not be used directly by the user,
// but instead via the Registry variable. Use of this type directly is not supported.
type Register struct {
	m map[string]plugins.Plugin
}

// Register registers a plugin by name. It panics if the name is empty, the plugin is nil,
// or a plugin is already registered with the same name. This can only be called during
// init, otherwise the behavior is undefined. Not safe for concurrent use.
func (r *Register) Register(p plugins.Plugin) {
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

	if err := validatePolicy(p.RetryPolicy()); err != nil {
		panic(fmt.Sprintf("plugin(%s) has invalid retry plan: %v", p.Name(), err))
	}

	r.m[p.Name()] = p
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
