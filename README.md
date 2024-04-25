# Workstream - A script workflow framework

[![Go Reference](https://pkg.go.dev/badge/github.com/element-of-surprise/workstream/workstream.svg)](https://pkg.go.dev/github.com/element-of-surprise/workstream)
[![Go Report Card](https://goreportcard.com/badge/github.com/element-of-surprise/workstream)](https://goreportcard.com/report/github.com/element-of-surprise/workstream)

<p align="center">
  <img src="./docs/imgs/gopher_river.jpeg"  width="500">
</p>

## Introduction

Workstream is a programatic workflow framework that allows you to define a complex series of prechecks, postchecks, continuous checks, blocks of actions, a plugin system, etc... with a simple and easy to read methodology.

## Prior Art

This system is based on a distributed Workflow system that I created at Google, that handled the deployment of various configurations and upgrades to the B2 router backbone. At the time of this writing, I believe the system is still in use. It should be noted that at least while I was there, 0 outages were caused by the system. Much of this I believe was due to not only best practices enforced by the system and removal of most coding errors, but also the ability to test the system in a way that was not possible before.

This framework is significatly simpler than that one. It is not designed to be used in the manner that the forementioned system, which was horitzonatally scalable, field upgradable without downtime, had mutli-language support, a policy system, etc...

I have also developed a simliar system to that one that is not open source between employements.

Finally there is a another open source framework similar to this that lacks the testing and utilities I wrote for the book, `Go For DevOps`. It shows how to develope a policy system, which this lacks.

So there is some prior art, and apparently I like writing the same thing over and over again.

## Why?

I created this package because I needed a way to define a complex series of actions in a simple and easy to read way. I also wanted to be able to define a series of actions that could be executed in parallel, but also have a series of actions that could be executed in sequence.

While everyone seems to want to configure systems using YAML, I have found that it generally leads to nasty failures. YAML is not a programming language, and it is not designed to be a programming language. It is a configuration language. And something has to execute that anyways and most of the time the underlying systems aren't great at it, because they take their cues from the YAML. If you see a regex in a YAML file, you know you are in trouble.

Even when the configuration language is tailor made and not some generic thing that wasn't suited for that purpose, it is still a configuration language. Its hard to test a configuration language will do what you want. Usually this means the only tests are in a real environment, which is not ideal (even if it is dev).

I want access to the tools that a programming language provides. I also want to be able to test my workflows in a simple way, which is hard to do with any configuration language.

Scripting languages like `Bash` or `Python` are not much better. Yes, you can use `Bash` to do anything, but it is not a good language for defining complex workflows. `Python` is better, but it is not a good language for defining complex workflows either. You generally know your `Bash` script works when you run it. I want unit tests even if I still need integration tests.

And `Python` still suffers from type safety issues. I want type safety. And I need to have `Python` installed on a system to use it, and I don't want to have to install `Python` on a system to use it. No, I don't want to bundle the interpreter with my script or in my container. I've done that, nothing like shipping hundreds of MiB across the network when 10 MiB would do.

## Advantages over just Go code

As with any framework, you have to ask what the advantages are over just writing Go code.

This code will introduce boiler plate that is harder to write than simple function calls. It will also introduce a learning curve that is not present in just writing Go code for a Go programmer.

Here are a few of the reasons why you might want to use this framework:

- Concurrency and failure control. Concurrency in automation workflow must be carefully considered, which is differnt from concurrency in a standard Go application. This framework provides a way to control concurrency and the number of failure allowed in a workflow.
- Checks. Defines a way to set pre/post/continuous checks for differnt parts of the workflow heirarchy. This allows automatic failures on certain conditions.
- Failure identification. Exact causes of failures are easily identified. In a Go program, its easy to write bad error messages or get error messages on one line that are constructed and filtered up before being dumped to the user. This often leads to questions about where exactly something failed. This framework, by inspecting the resulting `Plan` object after exectuion can tell you exactly where something failed, even if the error message is not helpful.
- Storage of the workflow. This framework provides a way to store the workflow in a way that is easily read and written. This is not a feature of Go code.
- Easy to test. Plugin tests revolve around testing the plugin, not the workflow. This makes it easy to test the plugins in isolation. Your code simply needs to test that it can generate the correct `Plan` object. If your code responds to failures in some way, you then simply need to test that your code responds to the `Plan` object correctly.
- Code reuse. This framework provides a way to reuse code between workflows. While standard Go code can accomplish this, most automation code in Go tends to be single shot code targeted for a purpose. Because everything in the framework is wrapped in plugins, you can reuse the plugins in different workflows.
- Consistency. This framework provides a way to enforce a consistent way of writing workflows.
- Follows Exponential Backoff. Each plugin provides the exponential backoff policy for retries. This enforces a best practice for handling retries.

## The Basics

### Plugins

Plugins are the foundation of the system. In many ways they are similar to an RPC client, where you send a request and get a response.

If a client needs multiple types of requests and responses, you create a top level request object that contains the request type which is used to determine the field in the request to use. The response object is similar.

All `Action`s in the system are calling plugins with different requests.

A plugin implements the following interface:

```go
// Plugin is the interface that must be implemented by all plugins.
type Plugin interface {
	// Name returns the name of the plugin. This must be unique in the registry.
	// The name should include the package path to avoid name collisions.
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
- Init - Validates that the environment that the plugin currently operates in is valid for the plugin. If this fails, the plugin cannot be used. For example, if this leverages an external binary, this can check for the existence of that binary.

A plugin is registered in a plugin registry. The registry is used to look up plugins by name, where all plugin names must be unique within a registry.

You may have multiple registries for multiple workstream objects. This allows you to have different plugins available for different security contexts.

Registering a plugin uses the registry package:

```go
package main

import (
	"github.com/element-of-surprise/plugins/git/github" // Not real, would hold a plugin
	"github.com/element-of-surprise/workstream/plugins/registry"
)

func main() {
	// Create a new registry of plugins.
	reg := registry.New()
	// Register the github plugin.
	githubPlug, err := github.New()
	if err != nil {
		panic(err)
	}
	reg.MustRegister(githubPlug)

	...

	ws, err := workstream.New(storage, reg)
	...
}
```

### Workflow Heirarchy

The workflow is defined in a hierarchy of objects:

- Plan - The top level object.
  - Can have PreChecks, PostChecks and ContChecks that are executed before, after and during the main actions.
- Block - A block of `Sequence` objects. You can have mulitple `Block`s.
  - Can have PreChecks, PostChecks and ContChecks that are executed before, after the main actions.
  - Represents a set of work to be done, usually related.
  - Controls the number of failures that are tolerated. Failures are defined as Sequence failures.
  - Controls the currency of execution. Sequences are then rate limited to this concurrency.
  - Only 1 `Block` can be executed at a time.
  - If a `Block` fails, the `Plan` fails.
- Sequence - A sequence of `Action` objects.
  - Has a set of `Action` objects.
  - Represents a set of work to be done, usually related.
  - Each `Action` is executed in order.
  - If a `Action` fails, the `Sequence` fails.
  - Only one `Action` can be executed at a time.
- Action - A single `Plugin` object.
  - If an `Action` fails, whatever calls it fails.
  - Holds the name of the `Plugin` to execute.
  - Holds the request object for the `Plugin`.
  - Holds the response object for the `Plugin`.

All objects have a field called `State` that holds the internal state of the object. This is used by the system to track the state of the object. It cannot be set by the user.

### Building a Plan

There are two ways to build a `Plan`:

- Simply creating a `Plan` object and adding `Block` objects to it containing all the sub-objects required.
- Using the `builder` package to build a `Plan` object.

#### Simple Plan built by hand

This is an example of how you can build a `Plan` object by hand. In this case we are deploying a server and running it using SCP, SSH and a ping plugin. (These are fictional plugins, just an example).

````go

```go
plan := &workflow.Plan{
	Name: "Deploy Server and Run",
	Blocks: []*workflow.Block{
		{
			Name: "Deploy Server",
			Descr: "Deploys an HTTP Server applicaiton",
			PreChecks: []*workflow.Action{
				{
					Name: "Check Server Exists",
					Descr: "Pings the server to make sure it responds",
					Plugin: "github.com/element-of-surprise/plugins/ping",
					Request: &ping.PingReq{
						Addr: serverAddr,
					},
				},
			},
			Sequences: []*workflow.Sequence{
				{
					Name: "Upload Server and Run",
					Descr: "Uses SCP to copy the server files and then logs in via SSH and runs it",
					Jobs: []workflow.Action{
						{
							Name: "Copy Server Files",
							Plugin: "github.com/element-of-surprise/plugins/scp",
							Request: &scp.CopytToReq{
								Addr: serverAddr,
								Files: []string{"server", "server.conf"},
							},
							Timeout: 5 * time.Minute,
						},
						{
							Name: "Run Server",
							Plugin: "github.com/element-of-surprise/plugins/ssh",
							Request: &ssh.ExecReq{
								Addr: serverAddr,
								Command: "nohup server --port 80",
							},
							Timeout: 10 * time.Second,
						},
					},
				},
			},
			PostChecks: []workflow.Action{
				{
					Name: "Check Server Deployed",
					Plugin: "github.com/element-of-surprise/plugins/healthcheck",
					Request: &healthcheck.Req{
						HostPort: net.JoinHostPort(serverAddr, "80"),
						Protocol: healthcheck.HTTP,
						LookFor: "ok",
					},
				},
			},
		},
	},
}
````

### Builder Package

The `builder` package is used to build a `Plan` using a builder pattern. Use this if you want to build a `Plan` from information gleaned from various sources.

This lets you more easily break up the building of a `Plan` into smaller pieces.

This example is similar to the one above, but will deploy the app to a cluster of machines.

```go

const (
	planName = "Deploy Server to Cluster"
	planDesc = "Deploys a server to a cluster of hosts"
)

build, err := builder.New(planName, planDesc)
if err != nil {
	log.Fatalf("Error creating builder: %v", err)
}

blockArgs := &builder.BlockArgs{
	Name: fmt.Sprintf("Deploy server to cluster %s", cluster),
	Name:  "Deploy Server",
	Descr: "Deploys the server application to a cluster",
	Concurrency: 5,
	ToleratedFailures: 2,
}

build.AddBlock(blockArgs)
for _, machine := range machines {
	build.AddSequence("Deploy to "+machine, "Deploys the server to "+machine)
	build.AddAction(
		&Action{
			Name: "Check Server Exists",
			Descr: "Pings the server to make sure it responds",
			Plugin: "github.com/element-of-surprise/plugins/ping",
			Request: &ping.PingReq{
				Addr: machine.Addr,
			},
		}
	)
	build.AddAction(
		&Action{
			Name: "Copy Server Files",
			Plugin: "github.com/element-of-surprise/plugins/scp",
			Request: &scp.CopytToReq{
				Addr: machine.Addr,
				Files: []string{"server", "server.conf"},
			},
			Timeout: 5 * time.Minute,
		}
	)
	build.AddAction(
		&Action{
			Name: "Run Server",
			Plugin: "github.com/element-of-surprise/plugins/ssh",
			Request: &ssh.ExecReq{
				Addr: machine.Addr,
				Command: "nohup server --port 80",
			},
			Timeout: 10 * time.Second,
		}
	)
	build.Up()
}

plan := build.Plan()
```

### Executing a Plan

Here is a simple execution of a plan, like one of the ones generated above. It runs to completion while printing the status of the workflow every 5 seconds.

If it fails, we print out the Plan. We could do more complex operations here, like retrying the plan, or sending an alert.

```go
... // Build the plan

// Create storage. In this case use sqlite in memory.
// reg is the registry of plugins, like we created above.
store := sqlite.New("", reg, sqlite.WithInMemory())

ws, err := workstream.New(store, reg)
if err != nil {
	log.Fatalf("Error creating workstream: %v", err)
}

id, err := ws.Submit(ctx, plan)
if err != nil {
	log.Fatalf("Error submitting plan: %v", err)
}

if err := ws.Start(ctx, id); err != nil {
	log.Fatalf("Error starting plan: %v", err)
}

// Wait for the plan to finish
var result *workflow.Result[*workflow.Plan]
for result = range ws.Status(ctx, id, 5 * time.Second) {
	fmt.Println("Status: ", result.Data.State.Status)
}

if result.State.Status != workflow.Completed {
	log.Fatalf("Plan failed:\n%s", pretty.Sprint(result))
}
```

## Dealing With Failures

Some workflows can have failures that you tolerate and do not stop the workflow. For example, if you are deploying to a cluster of machines, you may want to continue deploying to the other machines even if one fails.

You can specify the number of tolerated failures in the `Block` struct. If the number of failures exceeds this number, the workflow will stop after existing `Action`s finish (we don't like to stop mid action in some unknown state).

If setting tolerated failures above 0, a workflow can end in a completed state even if there were failures. You must look through the final `Plan` object to see if there were any failures. See the `workflow/utils/walk` package for helpers to walk the `Plan` object.

### Failure Gotcha

Remember that if you are using concurrency, the number of failures you can have can exceed the number of tolerated failures you set.

For example, if you have 0 ToleratedFailures, but a concurrency of 5, you can have up to 5 failures before the workflow stops. That is because when 5 concurrent actions are running, they can all fail. The first one that fails will trigger the workflow to stop, but the other 4 will still run to completion.

### Retries

Actions automatically retry until the timeout on a call is reached. The method of retry is an exponential retry mechansim set by the plugin. This prevents a plugin from overwhelming a system with retries and is an SRE best practice.

The author of the plugin can force retries to fail by returning a permanent error. This will cause the action to fail immediately regardless of the timeout.

Timeouts can be set to infinite, but this is not recommended.

Plugin authors can also take direct control of retries in special circumstances. For example, a plugin might be designed to wait until some file appears and the return. Or it might wait for a socket to open and respond. In these cases, the plugin can loop on a single call while obeying the timeout that is sent via the `Context` object.

### Retrying a Plan

`Plan` objects that are submitted to the system can only be run once. There IDs are unique and they follow a directed acyclic graph (DAG) model. This means that if you want to retry a `Plan`, you must create a new `Plan` object and submit that.

`Plan` objects have a Clone() methd to allow cloning a `Plan` for various purposes. This removes fields such as the ID and State in preparation for a new submission.

In the future, I will add more intelligent cloing methods to do things like remove only failiures and collapse blocks that have no failures.

You can tie `Plan`s together by using the same `GroupID` on a `Plan`.
