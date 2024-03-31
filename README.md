# Workstream - A script workflow framework

## Introduction

Workstream is a progromatic workflow framework that allows you to define a complex series of prechecks, postchecks, continuous checks, blocks of actions, a plugin system, etc... with a simple and easy to read methodology.

This system is based on a distributed Workflow system that I created at Google, that handled the deployment of various configurations and upgrades to the B2 router backbone. At the time of this writing, I believe the system is still in use. It should be noted that at least while I was there, 0 outages were caused by the system.

This framework is significatly simpler, for use in CLI applications or to accomplish simple tasks. It is not designed to be used in the manner that the forementioned system was. That was a much more robust system that was horitzonatally scalable, field upgradable without downtime, mutli-language support, support for policy systems, etc...

I developed a simliar system to the forementioned system that is not open source between employers.

I also have an open source framework similar to this that lacks the testing and utilities of this version for the book, `Go For DevOps`. It shows how to develope a policy system, which this lacks.

So there is some prior art, and apparently I like writing the same thing over and over again.

## Why?

I created this package because I needed a way to define a complex series of actions in a simple and easy to read way. I also wanted to be able to define a series of actions that could be executed in parallel, but also have a series of actions that could be executed in sequence.

While everyone seems to want to configure systems using YAML, I have found that it generally leads to nasty failures. YAML is not a programming language, and it is not designed to be a programming language. It is a configuration language. And something has to execute that anyways and most of the time the underlying systems aren't great at it, because they take their cues from the YAML. If you see a regex in a YAML file, you know you are in trouble.

Even when the configuration language is tailor made and not some generic thing that wasn't suited for that purpose, it is still a configuration language. Its hard to test a configuration language will do what you want. Usually this means the only tests are in a real environment, which is not ideal (even if it is dev).

I want access to the tools that a programming language provides. I also want to be able to test my workflows in a simple way, which is hard to do with any configuration language.

Scripting languages like `Bash` or `Python` are not much better. Yes, you can use `Bash` to do anything, but it is not a good language for defining complex workflows. `Python` is better, but it is not a good language for defining complex workflows either. You generally know your `Bash` script works when you run it. I want unit tests even if I still need integration tests.

And `Python` still suffers from type safety issues. I want type safety. And I need to have `Python` installed on a system to use it, and I don't want to have to install `Python` on a system to use it. No, I don't want to bundle the interpreter with my script or in my container. I've done that, nothing like shipping hundreds of MiB across the network when 10 MiB would do.

## The Basics

### Plugins

Plugins are the foundation of the system. These plugins are linked in via your `main.go` file via side effect imports.

A plugin implements the following interface:

```go
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
	// 1. Use exponential.Policy.TimeTable() for a custom retry timetable.
	// 2. Use one of the pre-built retry plans like FastRetryPlan(), SecondsRetryPlan(), etc.
	RetryPlan(retries int) exponential.TimeTable
	// InitCheck is run after the registery is loaded. The plugin should do any necessary checks
	// to ensure that it is ready to be used. If the plugin is not ready, it should return an error.
	// This is useful for plugins that require local resources like a command line application to
	// be installed.
	Init() error
}
```

- Name - The name of the plugin, only a single plugin may have a specific name. To avoid name collisions, the plugin name should include the package path.
- Execute - The main function of the plugin, this is where the work is done.
- ValidateReq - Validates the request object, since they are passed in as `any`.
- Request - Returns an empty request object.
- Response - Returns an empty response object. If a plugin returns a response that isn't the same as this, the plugin is considered to have failed.
- IsCheck - Returns true if the plugin is a check plugin. A check plugin should not have side effects and can only be used in one of the check actions. A check plugin cannot be used in a Job.
- RetryPlan - Returns the retry plan for the plugin. This is the plan for how the plugin should be retried. The number of retries is set in the `Job` object. This RetryPlan uses exponential backoff that you define for SRE best practices.

### Workflow Heirarchy

The workflow is defined in a hierarchy of objects:

- Plan - The top level object.
  - Can have PreChecks, PostChecks and ContChecks that are executed before, after and during the main actions.
- Block - A block of `Sequence` objects. You can have mulitple `Block`s.
  - Can have PreChecks, PostChecks and ContChecks that are executed before, after the main actions.
  - Represents a set of work to be done, usually related.
  - Controls the number of failures that are tolerated.
  - Controls the currency of execution.
  - Only 1 `Block` can be executed at a time.
  - If a `Block` fails, the `Plan` fails.
- Sequence - A sequence of `Job` objects.
  - Has a set of `Job` objects.
  - Represents a set of work to be done, usually related.
  - Each `Job` is executed in order.
  - If a `Job` fails, the `Sequence` fails.
  - Only one `Job` can be executed at a time.
- Job - A single `Action` object.
  - Represents a single unit of work.
  - If a `Job` fails, the `Sequence` fails.
  - Only one `Job` can be executed at a time.
  - Sets the number of retries for the `Action`.
  - Sets the timeout for an `Action`.
- Action - A single `Plugin` object.
  - If an `Action` fails, the `Job` fails.
  - Holds the name of the `Plugin` to execute.
  - Holds the request object for the `Plugin`.
  - Holds the response object for the `Plugin`.

All objects have a field called `Internal` that holds the internal state of the object. This is used by the system to track the state of the object. It cannot be set by the user.

### Builder Package

The `builder` package is used to build a `Plan` using a builder pattern. Use this if you want to build a `Plan` from information gleaned from various sources.

See documentation in that package for more information.
