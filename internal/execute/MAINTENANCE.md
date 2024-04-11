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
- `actions` - provides statemachine.State functions executed as a sub-state machine inside `sm` that handles all the actions in the workflow.Plan.
- `testing/plugins` - provides a `plugins.Plugin` that is used to test the `actions` state machine.

## Things to know about the state machines

Both the `sm` and `actions` state machines do not use the standard statemachine.Request.Err to return an error. Instead they define a `.err` on the `Data` type they define. This way they can always go to an `End` state and then the `error` is promoted to a `Request.Err`. This allows us to always do a cleanup that needs to be handled regardless of an error or not.
