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
	objectTypeLabel = "object_type"
	statusLabel     = "status"
)

var (
	// currently running guage for started/running
	runningCount metric.Int64UpDownCounter
	// rename from workflowEventCount to sometheing like objectStateTransition? objectStateChange? objectStateCompletion? just objectState or
	// objectStatus? executionState?
	// is this confusing if something is retried? I guess that would only show up in attempts
	executionStatusCount metric.Int64Counter
)

func metricName(name string) string {
	return fmt.Sprintf("%s_%s", subsystem, name)
}

// Init initializes the readers metrics. This should only be called by the tattler constructor or tests.
func Init(meter api.Meter) error {
	var err error
	runningCount, err = meter.Int64UpDownCounter(metricName("running_total"), api.WithDescription("total number of coercion workflow objects currently running"))
	if err != nil {
		return err
	}
	executionStatusCount, err = meter.Int64Counter(metricName("execution_status_total"), api.WithDescription("total number of coercion workflow objects executed"))
	if err != nil {
		return err
	}
	return nil
}

// StartRunning increases the runningCount metric.
func StartRunning(ctx context.Context, t workflow.ObjectType, s workflow.Status) {
	opt := api.WithAttributes(
		// added, modified, deleted, bookmark, error
		attribute.Key(objectTypeLabel).String(t.String()),
	)
	if runningCount != nil {
		runningCount.Add(ctx, 1, opt)
	}
}

// StopRunning decreases the runningCount metric.
func StopRunning(ctx context.Context, t workflow.ObjectType, s workflow.Status) {
	opt := api.WithAttributes(
		// added, modified, deleted, bookmark, error
		attribute.Key(objectTypeLabel).String(t.String()),
	)
	if runningCount != nil {
		runningCount.Add(ctx, -1, opt)
	}
}

// ExecutionStatus increases the watchEventCount metric
// with event type = (added, modified, deleted, bookmark, error).
// is it confusiong that there isn't a check type?
func ExecutionStatus(ctx context.Context, t workflow.ObjectType, s workflow.Status) {
	opt := api.WithAttributes(
		// added, modified, deleted, bookmark, error
		attribute.Key(objectTypeLabel).String(t.String()),
		attribute.Key(statusLabel).String(s.String()),
	)
	if executionStatusCount != nil {
		executionStatusCount.Add(ctx, 1, opt)
	}
}
