# Visualizer

The visualizer is a web application you can run on 127.0.0.1:3000 that allows you to drag
and drop (or use a dialog box) to upload a JSON file representing a workflow.Plan object.

This will cause the visualizer to render the workflow plan into files in the `upload` directory
where the executable is located.

The front page will then be refreshed and show the UUID of the object, that you can click on to see
the rendered workflow plan.  Various plan objects are clickable and will show the details of the
object.

This provides fast diagnostics for customer support or developers to know why something failed.
