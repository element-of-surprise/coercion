package azblob

import (
	"testing"
	"time"

	"github.com/element-of-surprise/coercion/workflow"
	"github.com/google/uuid"
	"github.com/kylelemons/godebug/pretty"
)

func TestContainerName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		prefix string
		date   time.Time
		want   string
	}{
		{
			name:   "Success: basic container name",
			prefix: "test",
			date:   time.Date(2025, 10, 21, 12, 30, 0, 0, time.UTC),
			want:   "test-2025-10-21",
		},
		{
			name:   "Success: different prefix",
			prefix: "production",
			date:   time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
			want:   "production-2024-01-01",
		},
		{
			name:   "Success: cluster ID prefix",
			prefix: "cluster-abc123",
			date:   time.Date(2025, 12, 31, 23, 59, 59, 0, time.UTC),
			want:   "cluster-abc123-2025-12-31",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := containerName(test.prefix, test.date)
			if got != test.want {
				t.Errorf("TestContainerName(%s): got %q, want %q", test.name, got, test.want)
			}
		})
	}
}

func TestContainerNames(t *testing.T) {
	t.Parallel()

	prefix := "test"
	got := containerNames(prefix)

	// Should return 2 containers: today and yesterday
	if len(got) != 2 {
		t.Errorf("TestContainerNames: got %d containers, want 2", len(got))
	}

	// First should be today
	expectedToday := containerName(prefix, time.Now().UTC())
	if got[0] != expectedToday {
		t.Errorf("TestContainerNames: first container got %q, want %q", got[0], expectedToday)
	}

	// Second should be yesterday
	yesterday := time.Now().UTC().AddDate(0, 0, -1)
	expectedYesterday := containerName(prefix, yesterday)
	if got[1] != expectedYesterday {
		t.Errorf("TestContainerNames: second container got %q, want %q", got[1], expectedYesterday)
	}
}

func TestBlockBlobName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		planID  uuid.UUID
		blockID uuid.UUID
		want    string
	}{
		{
			name:    "Success: valid IDs",
			planID:  uuid.MustParse("123e4567-e89b-12d3-a456-426614174000"),
			blockID: uuid.MustParse("550e8400-e29b-41d4-a716-446655440000"),
			want:    "blocks/123e4567-e89b-12d3-a456-426614174000/550e8400-e29b-41d4-a716-446655440000.json",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := blockBlobName(test.planID, test.blockID)
			if got != test.want {
				t.Errorf("TestBlockBlobName(%s): got %q, want %q", test.name, got, test.want)
			}
		})
	}
}

func TestSequenceBlobName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		planID     uuid.UUID
		sequenceID uuid.UUID
		want       string
	}{
		{
			name:       "Success: valid IDs",
			planID:     uuid.MustParse("123e4567-e89b-12d3-a456-426614174000"),
			sequenceID: uuid.MustParse("550e8400-e29b-41d4-a716-446655440000"),
			want:       "sequences/123e4567-e89b-12d3-a456-426614174000/550e8400-e29b-41d4-a716-446655440000.json",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := sequenceBlobName(test.planID, test.sequenceID)
			if got != test.want {
				t.Errorf("TestSequenceBlobName(%s): got %q, want %q", test.name, got, test.want)
			}
		})
	}
}

func TestChecksBlobName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		planID   uuid.UUID
		checksID uuid.UUID
		want     string
	}{
		{
			name:     "Success: valid IDs",
			planID:   uuid.MustParse("123e4567-e89b-12d3-a456-426614174000"),
			checksID: uuid.MustParse("550e8400-e29b-41d4-a716-446655440000"),
			want:     "checks/123e4567-e89b-12d3-a456-426614174000/550e8400-e29b-41d4-a716-446655440000.json",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := checksBlobName(test.planID, test.checksID)
			if got != test.want {
				t.Errorf("TestChecksBlobName(%s): got %q, want %q", test.name, got, test.want)
			}
		})
	}
}

func TestActionBlobName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		planID   uuid.UUID
		actionID uuid.UUID
		want     string
	}{
		{
			name:     "Success: valid IDs",
			planID:   uuid.MustParse("123e4567-e89b-12d3-a456-426614174000"),
			actionID: uuid.MustParse("550e8400-e29b-41d4-a716-446655440000"),
			want:     "actions/123e4567-e89b-12d3-a456-426614174000/550e8400-e29b-41d4-a716-446655440000.json",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := actionBlobName(test.planID, test.actionID)
			if got != test.want {
				t.Errorf("TestActionBlobName(%s): got %q, want %q", test.name, got, test.want)
			}
		})
	}
}

func TestBlobNameForObject(t *testing.T) {
	t.Parallel()

	planID := uuid.MustParse("123e4567-e89b-12d3-a456-426614174000")
	objID := uuid.MustParse("550e8400-e29b-41d4-a716-446655440000")

	tests := []struct {
		name string
		obj  workflow.Object
		want string
	}{
		{
			name: "Success: Plan object",
			obj: &workflow.Plan{
				ID: objID,
			},
			want: "plans/550e8400-e29b-41d4-a716-446655440000-object.json",
		},
		{
			name: "Success: Block object",
			obj: func() *workflow.Block {
				b := &workflow.Block{ID: objID}
				b.SetPlanID(planID)
				return b
			}(),
			want: "blocks/123e4567-e89b-12d3-a456-426614174000/550e8400-e29b-41d4-a716-446655440000.json",
		},
		{
			name: "Success: Sequence object",
			obj: func() *workflow.Sequence {
				s := &workflow.Sequence{ID: objID}
				s.SetPlanID(planID)
				return s
			}(),
			want: "sequences/123e4567-e89b-12d3-a456-426614174000/550e8400-e29b-41d4-a716-446655440000.json",
		},
		{
			name: "Success: Checks object",
			obj: func() *workflow.Checks {
				c := &workflow.Checks{ID: objID}
				c.SetPlanID(planID)
				return c
			}(),
			want: "checks/123e4567-e89b-12d3-a456-426614174000/550e8400-e29b-41d4-a716-446655440000.json",
		},
		{
			name: "Success: Action object",
			obj: func() *workflow.Action {
				a := &workflow.Action{ID: objID}
				a.SetPlanID(planID)
				return a
			}(),
			want: "actions/123e4567-e89b-12d3-a456-426614174000/550e8400-e29b-41d4-a716-446655440000.json",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := blobNameForObject(test.obj)
			if got != test.want {
				t.Errorf("TestBlobNameForObject(%s): got %q, want %q", test.name, got, test.want)
			}
		})
	}
}

func TestPlanBlobPrefix(t *testing.T) {
	t.Parallel()

	want := "plans/"
	got := planBlobPrefix()

	if got != want {
		t.Errorf("TestPlanBlobPrefix: got %q, want %q", got, want)
	}
}

func TestObjectBlobPrefix(t *testing.T) {
	t.Parallel()

	planID := uuid.MustParse("123e4567-e89b-12d3-a456-426614174000")
	want := "blocks/123e4567-e89b-12d3-a456-426614174000/"
	got := objectBlobPrefix(planID)

	if got != want {
		t.Errorf("TestObjectBlobPrefix: got %q, want %q", got, want)
	}
}

func TestToPtr(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		value interface{}
	}{
		{
			name:  "Success: string",
			value: "test",
		},
		{
			name:  "Success: int",
			value: 42,
		},
		{
			name:  "Success: bool",
			value: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			switch v := test.value.(type) {
			case string:
				got := toPtr(v)
				if got == nil {
					t.Errorf("TestToPtr(%s): got nil, want non-nil", test.name)
				}
				if *got != v {
					t.Errorf("TestToPtr(%s): got %v, want %v", test.name, *got, v)
				}
			case int:
				got := toPtr(v)
				if got == nil {
					t.Errorf("TestToPtr(%s): got nil, want non-nil", test.name)
				}
				if *got != v {
					t.Errorf("TestToPtr(%s): got %v, want %v", test.name, *got, v)
				}
			case bool:
				got := toPtr(v)
				if got == nil {
					t.Errorf("TestToPtr(%s): got nil, want non-nil", test.name)
				}
				if *got != v {
					t.Errorf("TestToPtr(%s): got %v, want %v", test.name, *got, v)
				}
			}
		})
	}
}

func init() {
	// Configure pretty to show differences more clearly
	pretty.CompareConfig.IncludeUnexported = false
}
