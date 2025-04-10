# Execute Package

## Introduction

The `execute` package provides the main entry point for executing workflow.Plan objects. It uses the `sm` package to execute the workflow.Plan as a state machine. There are other sub-statemmachines that handles parts of that execution.

If you have never seen a state machine that returns functions, this [article](https://medium.com/@johnsiilver/go-state-machine-patterns-3b667f345b5e) will give you an introduction and why it is useful.

You will then want to be familar with the [statemachine package](https://github.com/gostdlib/ops/tree/main/statemachine) before viewing the code.

## Structure

```bash
execute/
└── sm
    ├── actions
    └── testing
        └── plugins
```

- `execute` - contains the main entry point for executing workflow.Plan objects.
- `sm` - provides statemachine.State functions for executing the workflow.Plan as a state machine.
- `sm/actions` - provides statemachine.State functions executed as a sub-state machine inside `sm` that handles all the actions in the workflow.Plan.
- `sm/testing/plugins` - provides a `plugins.Plugin` that is used to test the `actions` state machine.

## Things to know about the state machines

Both the `sm` and `actions` state machines do not use the standard statemachine.Request.Err to return an error. Instead they define a `.err` on the `Data` type they define. This way they can always go to an `End` state and then the `error` is promoted to a `Request.Err` after that final catch all state is run.

This allows us to always do a cleanup that needs to be handled regardless of an error or not.

## Errors in the statemachine

We don't use errors.E() in the statemachine. All the errors are recorded as part of the Plan record, so we only do this in upper levels like New() and such where we have errors that are related to bugs and such.
