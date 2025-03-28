package context

import (
	"testing"

	"github.com/gostdlib/base/context"

	"github.com/google/uuid"
)

func TestPlanID(t *testing.T) {
	ctx := context.Background()
	want, err := uuid.NewV7()
	if err != nil {
		t.Fatal(err)
	}

	ctx = SetPlanID(ctx, want)

	got := PlanID(ctx)
	if want != got {
		t.Fatalf("TestPlanID: got %s, want %s", got, want)
	}
}

func TestActionID(t *testing.T) {
	ctx := context.Background()
	want, err := uuid.NewV7()
	if err != nil {
		t.Fatal(err)
	}

	ctx = SetActionID(ctx, want)

	got := ActionID(ctx)
	if want != got {
		t.Fatalf("TestActionID: got %s, want %s", got, want)
	}
}
