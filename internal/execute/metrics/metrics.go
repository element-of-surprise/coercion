package readers

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	api "go.opentelemetry.io/otel/metric"

	"github.com/element-of-surprise/coercion/workflow"
)

const (
	subsystem       = "coercion"
	objectTypeLabel  = "object_type"
	statusLabel = "status"
)

var (
	// currently running guage for started/running
	// rename to sometheing like objectStateTransition? objectStateChange? objectStateCompletion? just objectState or
	// objectStatus?
	workflowEventCount metric.Int64Counter
)

func metricName(name string) string {
	return fmt.Sprintf("%s_%s", subsystem, name)
}

// Init initializes the readers metrics. This should only be called by the tattler constructor or tests.
func Init(meter api.Meter) error {
	var err error
	workflowEventCount, err = meter.Int64Counter(metricName("workflow_event_total"), api.WithDescription("total number of watch events handled by tattler"))
	if err != nil {
		return err
	}
	return nil
}

// WorkflowEvent increases the watchEventCount metric
// with event type = (added, modified, deleted, bookmark, error).
func WorkflowEvent(ctx context.Context, t workflow.ObjectType, s workflow.Status) {
	opt := api.WithAttributes(
		// added, modified, deleted, bookmark, error
		attribute.Key(objectTypeLabel).String(t.String()),
		attribute.Key(statusLabel).String(s.String()),
	)
	if workflowEventCount != nil {
		workflowEventCount.Add(ctx, 1, opt)
	}
}
