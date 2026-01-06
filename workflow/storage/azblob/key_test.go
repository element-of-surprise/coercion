package azblob

import (
	"fmt"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/bloberror"
	"github.com/element-of-surprise/coercion/workflow"
	"github.com/element-of-surprise/coercion/workflow/context"
	"github.com/element-of-surprise/coercion/workflow/storage"
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

func TestRecoveryContainerNames(t *testing.T) {
	t.Parallel()

	notFoundErr := &azcore.ResponseError{ErrorCode: string(bloberror.ContainerNotFound)}

	tests := []struct {
		name           string
		listResults    func(containerName string) ([]storage.ListResult, error)
		wantContainers []string
		wantErr        bool
	}{
		{
			name: "Success: single container with running plan",
			listResults: func(cn string) ([]storage.ListResult, error) {
				today := containerName("test", time.Now().UTC())
				if cn == today {
					return []storage.ListResult{
						{
							ID:    uuid.New(),
							State: workflow.State{Status: workflow.Running},
						},
					}, nil
				}
				return nil, notFoundErr
			},
			wantContainers: []string{containerName("test", time.Now().UTC())},
			wantErr:        false,
		},
		{
			name: "Success: single container with NotStarted plan within 2 days",
			listResults: func(cn string) ([]storage.ListResult, error) {
				today := containerName("test", time.Now().UTC())
				if cn == today {
					return []storage.ListResult{
						{
							ID: uuid.New(),
							State: workflow.State{
								Status: workflow.NotStarted,
							},
						},
					}, nil
				}
				return nil, notFoundErr
			},
			wantContainers: []string{containerName("test", time.Now().UTC())},
			wantErr:        false,
		},
		{
			name: "Success: no containers found after 10 not found errors",
			listResults: func(cn string) ([]storage.ListResult, error) {
				return nil, notFoundErr
			},
			wantContainers: []string{},
			wantErr:        false,
		},
		{
			name: "Success: stops after 5 containers with no uncompleted plans",
			listResults: func(cn string) ([]storage.ListResult, error) {
				return []storage.ListResult{
					{
						ID:    uuid.New(),
						State: workflow.State{Status: workflow.Completed},
					},
				}, nil
			},
			wantContainers: []string{},
			wantErr:        false,
		},
		{
			name: "Success: multiple containers with running plans",
			listResults: func(cn string) ([]storage.ListResult, error) {
				now := time.Now().UTC()
				today := containerName("test", now)
				yesterday := containerName("test", now.AddDate(0, 0, -1))

				switch cn {
				case today:
					return []storage.ListResult{
						{
							ID:    uuid.New(),
							State: workflow.State{Status: workflow.Running},
						},
					}, nil
				case yesterday:
					return []storage.ListResult{
						{
							ID:    uuid.New(),
							State: workflow.State{Status: workflow.Running},
						},
					}, nil
				default:
					return nil, notFoundErr
				}
			},
			wantContainers: []string{
				containerName("test", time.Now().UTC()),
				containerName("test", time.Now().UTC().AddDate(0, 0, -1)),
			},
			wantErr: false,
		},
		{
			name: "Success: mix of completed and running containers",
			listResults: func(cn string) ([]storage.ListResult, error) {
				now := time.Now().UTC()
				today := containerName("test", now)
				yesterday := containerName("test", now.AddDate(0, 0, -1))
				twoDaysAgo := containerName("test", now.AddDate(0, 0, -2))

				switch cn {
				case today:
					return []storage.ListResult{
						{
							ID:    uuid.New(),
							State: workflow.State{Status: workflow.Completed},
						},
					}, nil
				case yesterday:
					return []storage.ListResult{
						{
							ID:    uuid.New(),
							State: workflow.State{Status: workflow.Running},
						},
					}, nil
				case twoDaysAgo:
					return []storage.ListResult{
						{
							ID:    uuid.New(),
							State: workflow.State{Status: workflow.Completed},
						},
					}, nil
				default:
					return nil, notFoundErr
				}
			},
			wantContainers: []string{
				containerName("test", time.Now().UTC().AddDate(0, 0, -1)),
			},
			wantErr: false,
		},
		{
			name: "Error: non-NotFound error returns error",
			listResults: func(cn string) ([]storage.ListResult, error) {
				return nil, fmt.Errorf("some other error")
			},
			wantContainers: nil,
			wantErr:        true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			r := reader{
				prefix: "test",
				testListPlansInContainer: func(ctx context.Context, cn string) ([]storage.ListResult, error) {
					return test.listResults(cn)
				},
			}

			got, err := recoveryContainerNames(t.Context(), "test", r, 10)

			switch {
			case err == nil && test.wantErr:
				t.Errorf("[TestRecoveryContainerNames](%s): got err == nil, want err != nil", test.name)
				return
			case err != nil && !test.wantErr:
				t.Errorf("[TestRecoveryContainerNames](%s): got err == %s, want err == nil", test.name, err)
				return
			case err != nil:
				return
			}

			if diff := pretty.Compare(test.wantContainers, got); diff != "" {
				t.Errorf("[TestRecoveryContainerNames](%s): -want +got:\n%s", test.name, diff)
			}
		})
	}
}
