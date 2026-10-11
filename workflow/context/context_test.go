package context

import (
	"log/slog"
	"testing"

	"github.com/google/uuid"
	"github.com/gostdlib/base/concurrency/worker"
	"github.com/kylelemons/godebug/pretty"

	"github.com/element-of-surprise/coercion/workflow/errors"
)

func TestPlanID(t *testing.T) {
	t.Parallel()

	want, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("TestPlanID: uuid.NewV7: %s", err)
	}

	if got := PlanID(SetPlanID(t.Context(), want)); got != want {
		t.Errorf("TestPlanID: got %s, want %s", got, want)
	}
}

func TestActionID(t *testing.T) {
	t.Parallel()

	want, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("TestActionID: uuid.NewV7: %s", err)
	}

	if got := ActionID(SetActionID(t.Context(), want)); got != want {
		t.Errorf("TestActionID: got %s, want %s", got, want)
	}
}

func TestAttrs(t *testing.T) {
	t.Parallel()

	want := []slog.Attr{slog.String("plan", "a"), slog.Int("attempt", 2)}

	got := Attrs(AddAttrs(t.Context(), want...))
	if diff := pretty.Compare(want, got); diff != "" {
		t.Errorf("TestAttrs: -want +got:\n%s", diff)
	}
}

func TestSetPool(t *testing.T) {
	t.Parallel()

	p, err := worker.New(t.Context(), "testSetPool")
	if err != nil {
		t.Fatalf("TestSetPool: worker.New: %s", err)
	}
	defer p.Close(t.Context())

	if got := Pool(SetPool(t.Context(), p)); got != p {
		t.Errorf("TestSetPool: Pool() did not return the pool set by SetPool")
	}
}

func TestEOptions(t *testing.T) {
	t.Parallel()

	ctx := SetEOptions(t.Context(), errors.WithStackTrace(), errors.WithCallNum(2))

	if got := len(EOptions(ctx)); got != 2 {
		t.Errorf("TestEOptions: got %d options, want 2", got)
	}
}
