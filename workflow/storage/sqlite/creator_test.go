package sqlite

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gostdlib/base/context"
	"github.com/kylelemons/godebug/pretty"

	"github.com/element-of-surprise/coercion/plugins/registry"
	"github.com/element-of-surprise/coercion/workflow"
	"github.com/element-of-surprise/coercion/workflow/storage/sqlite/testing/plugins"
)

func TestCreate(t *testing.T) {
	t.Parallel()

	reg := registry.New()
	reg.Register(&plugins.CheckPlugin{})
	reg.Register(&plugins.HelloPlugin{})

	v, err := New(t.Context(), "", reg, WithInMemory())
	if err != nil {
		t.Fatalf("TestCreate: New: %s", err)
	}
	defer v.Close(t.Context())

	stored := createTestPlan(t, time.Now().UTC())
	if err := v.Create(t.Context(), stored); err != nil {
		t.Fatalf("TestCreate: Create: %s", err)
	}
	storedRows := planRows(t, v.Pool(), stored.ID)

	// duplicate has the stored Plan's ID but is otherwise a different Plan.
	duplicate := createTestPlan(t, time.Now().UTC())
	duplicate.ID = stored.ID
	duplicate.Name = "duplicate"

	tests := []struct {
		name string
		plan *workflow.Plan
		// cancelled calls Create with a ctx that is already cancelled.
		cancelled bool
		wantErr   bool
	}{
		{
			name: "Success: a Plan with a new ID is created.",
			plan: createTestPlan(t, time.Now().UTC()),
		},
		{
			name:      "Success: a Plan is created even when ctx is cancelled, so a started write always finishes.",
			plan:      createTestPlan(t, time.Now().UTC()),
			cancelled: true,
		},
		{
			name:    "Error: a Plan with a stored Plan's ID is not created.",
			plan:    duplicate,
			wantErr: true,
		},
		{
			name: "Error: a Plan with no ID is not created.",
			plan: func() *workflow.Plan {
				p := createTestPlan(t, time.Now().UTC())
				p.ID = uuid.Nil
				return p
			}(),
			wantErr: true,
		},
	}

	for _, test := range tests {
		ctx := t.Context()
		if test.cancelled {
			var cancel context.CancelFunc
			ctx, cancel = context.WithCancel(ctx)
			cancel()
		}
		err := v.Create(ctx, test.plan)
		switch {
		case err == nil && test.wantErr:
			t.Errorf("TestCreate(%s): got err == nil, want err != nil", test.name)
			continue
		case err != nil && !test.wantErr:
			t.Errorf("TestCreate(%s): got err == %s, want err == nil", test.name, err)
			continue
		case err != nil:
			continue
		}
		if _, err := v.Read(t.Context(), test.plan.ID); err != nil {
			t.Errorf("TestCreate(%s): Read: got err == %s, want err == nil", test.name, err)
		}
	}

	// The rejected duplicate must not have changed or added to the stored Plan.
	got, err := v.Read(t.Context(), stored.ID)
	if err != nil {
		t.Fatalf("TestCreate: Read: %s", err)
	}
	if got.Name != stored.Name {
		t.Errorf("TestCreate: stored Plan's name is %q after a duplicate Create, want %q", got.Name, stored.Name)
	}
	if diff := pretty.Compare(storedRows, planRows(t, v.Pool(), stored.ID)); diff != "" {
		t.Errorf("TestCreate: stored Plan's rows after a duplicate Create -before +after:\n%s", diff)
	}
}
