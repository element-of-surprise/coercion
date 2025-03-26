package execute

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
	// submittedCount is a guage for plans that have been submitted but not started.
	submittedCount metric.Int64UpDownCounter
	// startLatency is the time from when the plan is submitted to when it is started.
	// The client calls Submit and Start, so this high latency might say something about their call pattern, or be a
	// result of recovery.
	startPlanLatency metric.Int64Histogram
	// runningCount is a gauge for currently running coercion workflow objects.
	runningCount metric.Int64UpDownCounter
	// rename from workflowEventCount to sometheing like objectStateTransition? objectStateChange? objectStateCompletion? just objectState or
	// objectStatus? executionState?
	// is this confusing if something is retried? I guess that would only show up in attempts
	executionStatusCount metric.Int64Counter
	// latency from start to end of a workflow?
	// latency from submit time to start time?
	// number of retries/attempts?
)

func metricName(name string) string {
	return fmt.Sprintf("%s_%s", subsystem, name)
}

// Init initializes the readers metrics. This should only be called by the tattler constructor or tests.
func Init(meter api.Meter) error {
	var err error
	submittedCount, err = meter.Int64UpDownCounter(metricName("plan_submitted_total"), api.WithDescription("total number of coercion workflow objects submitted but not started"))
	if err != nil {
		return err
	}
	runningCount, err = meter.Int64UpDownCounter(metricName("running_total"), api.WithDescription("total number of coercion workflow objects currently running"))
	if err != nil {
		return err
	}
	executionStatusCount, err = meter.Int64Counter(metricName("execution_status_total"), api.WithDescription("total number of coercion workflow objects executed"))
	if err != nil {
		return err
	}
	startPlanLatency, err = meter.Int64Histogram(metricName("plan_start_ms"), api.WithDescription("total number of coercion workflow objects executed"))
	// api.WithExplicitBucketBoundaries(50, 100, 200, 400, 600, 800, 1000, 1250, 1500, 2000, 3000, 4000, 5000, 10000),
	if err != nil {
		return err
	}
	return nil
}

// NotStarted increases the submittedCount metric.
func NotStarted(ctx context.Context, plan *workflow.Plan) {
	opt := api.WithAttributes(
	// attribute.Key(objectTypeLabel).String(t.String()),
	)
	if submittedCount != nil {
		submittedCount.Add(ctx, 1, opt)
	}
}

// Started decreases the submittedCount and records the startPlanLatency metric.
func Started(ctx context.Context, plan *workflow.Plan) {
	opt := api.WithAttributes(
	// attribute.Key(objectTypeLabel).String(t.String()),
	)
	if submittedCount != nil {
		submittedCount.Add(ctx, -1, opt)
	}
	if startPlanLatency != nil {
		startPlanLatency.Record(ctx, plan.State.Start.Sub(plan.SubmitTime).Milliseconds(), opt)
	}
}

// Start increases the runningCount metric.
func Start(ctx context.Context, t workflow.ObjectType) {
	opt := api.WithAttributes(
		attribute.Key(objectTypeLabel).String(t.String()),
	)
	if runningCount != nil {
		runningCount.Add(ctx, 1, opt)
	}
}

// End decreases the runningCount metric.
func End(ctx context.Context, t workflow.ObjectType) {
	opt := api.WithAttributes(
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
		attribute.Key(objectTypeLabel).String(t.String()),
		attribute.Key(statusLabel).String(s.String()),
	)
	if executionStatusCount != nil {
		executionStatusCount.Add(ctx, 1, opt)
	}
}
