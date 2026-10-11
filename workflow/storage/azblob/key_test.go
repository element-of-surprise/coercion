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
			t.Parallel()

			got := containerName(test.prefix, test.date)
			if got != test.want {
				t.Errorf("TestContainerName(%s): got %q, want %q", test.name, got, test.want)
			}
		})
	}
}

func TestSearchContainerNames(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		prefix        string
		retentionDays int
		wantLen       int
	}{
		{
			name:          "Success: 14 day retention",
			prefix:        "test",
			retentionDays: 14,
			wantLen:       14,
		},
		{
			name:          "Success: 7 day retention",
			prefix:        "test",
			retentionDays: 7,
			wantLen:       7,
		},
		{
			name:          "Success: 1 day retention",
			prefix:        "test",
			retentionDays: 1,
			wantLen:       1,
		},
		{
			name:          "Success: zero retention returns nil",
			prefix:        "test",
			retentionDays: 0,
			wantLen:       0,
		},
		{
			name:          "Success: negative retention returns nil",
			prefix:        "test",
			retentionDays: -1,
			wantLen:       0,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got := searchContainerNames(test.prefix, test.retentionDays)

			if len(got) != test.wantLen {
				t.Errorf("TestSearchContainerNames(%s): got %d containers, want %d", test.name, len(got), test.wantLen)
				return
			}

			if test.wantLen == 0 {
				return
			}

			// First should be today
			now := time.Now().UTC()
			expectedToday := containerName(test.prefix, now)
			if got[0] != expectedToday {
				t.Errorf("TestSearchContainerNames(%s): first container got %q, want %q", test.name, got[0], expectedToday)
			}

			// Last should be retentionDays-1 days ago
			lastDay := now.AddDate(0, 0, -(test.retentionDays - 1))
			expectedLast := containerName(test.prefix, lastDay)
			if got[len(got)-1] != expectedLast {
				t.Errorf("TestSearchContainerNames(%s): last container got %q, want %q", test.name, got[len(got)-1], expectedLast)
			}
		})
	}
}

// TestBlobName pins every blob name and prefix to the format already in storage, which parsePlanBlobName and the
// listings must keep reading.
func TestBlobName(t *testing.T) {
	t.Parallel()

	planID := uuid.MustParse("123e4567-e89b-12d3-a456-426614174000")
	objID := uuid.MustParse("550e8400-e29b-41d4-a716-446655440000")

	tests := []struct {
		name string
		got  string
		want string
	}{
		{
			name: "Success: planEntryBlobName",
			got:  planEntryBlobName(objID),
			want: "plans/550e8400-e29b-41d4-a716-446655440000-entry.json",
		},
		{
			name: "Success: planObjectBlobName",
			got:  planObjectBlobName(objID),
			want: "plans/550e8400-e29b-41d4-a716-446655440000-object.json",
		},
		{
			name: "Success: blockBlobName",
			got:  blockBlobName(planID, objID),
			want: "blocks/123e4567-e89b-12d3-a456-426614174000/550e8400-e29b-41d4-a716-446655440000.json",
		},
		{
			name: "Success: sequenceBlobName",
			got:  sequenceBlobName(planID, objID),
			want: "sequences/123e4567-e89b-12d3-a456-426614174000/550e8400-e29b-41d4-a716-446655440000.json",
		},
		{
			name: "Success: checksBlobName",
			got:  checksBlobName(planID, objID),
			want: "checks/123e4567-e89b-12d3-a456-426614174000/550e8400-e29b-41d4-a716-446655440000.json",
		},
		{
			name: "Success: actionBlobName",
			got:  actionBlobName(planID, objID),
			want: "actions/123e4567-e89b-12d3-a456-426614174000/550e8400-e29b-41d4-a716-446655440000.json",
		},
		{
			name: "Success: blobNameForObject for a Plan",
			got:  blobNameForObject(&workflow.Plan{ID: objID}),
			want: "plans/550e8400-e29b-41d4-a716-446655440000-object.json",
		},
		{
			name: "Success: blobNameForObject for a Block",
			got: blobNameForObject(func() *workflow.Block {
				b := &workflow.Block{ID: objID}
				b.SetPlanID(planID)
				return b
			}()),
			want: "blocks/123e4567-e89b-12d3-a456-426614174000/550e8400-e29b-41d4-a716-446655440000.json",
		},
		{
			name: "Success: blobNameForObject for a Sequence",
			got: blobNameForObject(func() *workflow.Sequence {
				s := &workflow.Sequence{ID: objID}
				s.SetPlanID(planID)
				return s
			}()),
			want: "sequences/123e4567-e89b-12d3-a456-426614174000/550e8400-e29b-41d4-a716-446655440000.json",
		},
		{
			name: "Success: blobNameForObject for a Checks",
			got: blobNameForObject(func() *workflow.Checks {
				c := &workflow.Checks{ID: objID}
				c.SetPlanID(planID)
				return c
			}()),
			want: "checks/123e4567-e89b-12d3-a456-426614174000/550e8400-e29b-41d4-a716-446655440000.json",
		},
		{
			name: "Success: blobNameForObject for an Action",
			got: blobNameForObject(func() *workflow.Action {
				a := &workflow.Action{ID: objID}
				a.SetPlanID(planID)
				return a
			}()),
			want: "actions/123e4567-e89b-12d3-a456-426614174000/550e8400-e29b-41d4-a716-446655440000.json",
		},
		{
			name: "Success: planBlobPrefix",
			got:  planBlobPrefix(),
			want: "plans/",
		},
		{
			name: "Success: objectBlobPrefix",
			got:  objectBlobPrefix(planID),
			want: "blocks/123e4567-e89b-12d3-a456-426614174000/",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if test.got != test.want {
				t.Errorf("TestBlobName(%s): got %q, want %q", test.name, test.got, test.want)
			}
		})
	}
}

func TestToPtr(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// check calls toPtr with a value of one type and checks the result.
		check func(t *testing.T, name string)
	}{
		{
			name:  "Success: string",
			check: func(t *testing.T, name string) { checkToPtr(t, name, "test") },
		},
		{
			name:  "Success: int",
			check: func(t *testing.T, name string) { checkToPtr(t, name, 42) },
		},
		{
			name:  "Success: bool",
			check: func(t *testing.T, name string) { checkToPtr(t, name, true) },
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			test.check(t, test.name)
		})
	}
}

// checkToPtr checks that toPtr(v) points at a copy of v.
func checkToPtr[T comparable](t *testing.T, name string, v T) {
	t.Helper()

	got := toPtr(v)
	if got == nil {
		t.Fatalf("TestToPtr(%s): got nil, want non-nil", name)
	}
	if *got != v {
		t.Errorf("TestToPtr(%s): got %v, want %v", name, *got, v)
	}
}

func init() {
	// Configure pretty to show differences more clearly
	pretty.CompareConfig.IncludeUnexported = false
}

func TestParsePlanBlobName(t *testing.T) {
	t.Parallel()

	id := workflow.NewV7()
	storedID := uuid.MustParse("550e8400-e29b-41d4-a716-446655440000")

	tests := []struct {
		name     string
		blobName string
		wantID   uuid.UUID
		wantKind blobKind
		wantOK   bool
	}{
		// The literal names are the format already in storage. They keep the parser reading existing blobs even if
		// the name builders and the parser are changed together.
		{
			name:     "Success: a stored entry blob name gives its plan ID and kind",
			blobName: "plans/550e8400-e29b-41d4-a716-446655440000-entry.json",
			wantID:   storedID,
			wantKind: entryBlob,
			wantOK:   true,
		},
		{
			name:     "Success: a stored object blob name gives its plan ID and kind",
			blobName: "plans/550e8400-e29b-41d4-a716-446655440000-object.json",
			wantID:   storedID,
			wantKind: objectBlob,
			wantOK:   true,
		},
		{
			name:     "Success: an entry blob name gives its plan ID and kind",
			blobName: planEntryBlobName(id),
			wantID:   id,
			wantKind: entryBlob,
			wantOK:   true,
		},
		{
			name:     "Success: an object blob name gives its plan ID and kind",
			blobName: planObjectBlobName(id),
			wantID:   id,
			wantKind: objectBlob,
			wantOK:   true,
		},
		{
			name:     "Success: a name outside plans/ is not a plan blob",
			blobName: "blocks/" + id.String() + "-entry.json",
		},
		{
			name:     "Success: a name with an unknown suffix is not a plan blob",
			blobName: planBlobPrefix() + id.String() + "-other.json",
		},
		{
			name:     "Success: a name whose ID is not a UUID is not a plan blob",
			blobName: planBlobPrefix() + "not-a-uuid-entry.json",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			gotID, gotKind, gotOK := parsePlanBlobName(test.blobName)
			if gotOK != test.wantOK || gotID != test.wantID || gotKind != test.wantKind {
				t.Errorf("TestParsePlanBlobName(%s): got (%s, %v, %v), want (%s, %v, %v)", test.name, gotID, gotKind, gotOK, test.wantID, test.wantKind, test.wantOK)
			}
		})
	}
}
