package context_test

import (
	"testing"

	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"

	"github.com/element-of-surprise/coercion/workflow/context"
)

// callerPkg is the package MeterName() should derive for meters requested from this file. It is an external test
// package so it differs from the wrapper package being tested.
const callerPkg = "github.com/element-of-surprise/coercion/workflow/context_test"

// fakeProvider is a metric.MeterProvider that records the name of every Meter requested from it.
type fakeProvider struct {
	noop.MeterProvider

	names []string
}

func (f *fakeProvider) Meter(name string, opts ...metric.MeterOption) metric.Meter {
	f.names = append(f.names, name)
	return f.MeterProvider.Meter(name, opts...)
}

func TestMeter(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		meter func(ctx context.Context) metric.Meter
	}{
		{
			name: "Success: Meter() names the meter after the calling package",
			meter: func(ctx context.Context) metric.Meter {
				return context.Meter(ctx)
			},
		},
		{
			name: "Success: MeterWithStackFrame(2) names the meter after the calling package, not the wrapper",
			meter: func(ctx context.Context) metric.Meter {
				return context.MeterWithStackFrame(ctx, 2)
			},
		},
	}

	for _, test := range tests {
		p := &fakeProvider{}
		test.meter(context.SetMeterProvider(t.Context(), p))

		if len(p.names) != 1 {
			t.Errorf("TestMeter(%s): got %d Meter() calls on the provider, want 1", test.name, len(p.names))
			continue
		}
		if p.names[0] != callerPkg {
			t.Errorf("TestMeter(%s): got meter name %q, want %q", test.name, p.names[0], callerPkg)
		}
	}
}

func TestSetMeterProvider(t *testing.T) {
	t.Parallel()

	p := &fakeProvider{}
	if got := context.MeterProvider(context.SetMeterProvider(t.Context(), p)); got != p {
		t.Errorf("TestSetMeterProvider: MeterProvider() did not return the provider set by SetMeterProvider")
	}
}
