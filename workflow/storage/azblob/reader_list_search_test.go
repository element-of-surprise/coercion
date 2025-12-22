package azblob

import (
	"testing"
	"time"

	gofrs "github.com/gofrs/uuid/v5"

	"github.com/element-of-surprise/coercion/plugins/registry"
	"github.com/element-of-surprise/coercion/workflow"
	"github.com/element-of-surprise/coercion/workflow/context"
	"github.com/element-of-surprise/coercion/workflow/storage"
	"github.com/element-of-surprise/coercion/workflow/storage/azblob/internal/blobops"
	"github.com/element-of-surprise/coercion/workflow/storage/azblob/internal/planlocks"
	testPlugins "github.com/element-of-surprise/coercion/workflow/storage/sqlite/testing/plugins"
	"github.com/google/uuid"
	"golang.org/x/sync/singleflight"
)

// createPlanIDForDate creates a UUID v7 that appears to have been created at the given time.
func createPlanIDForDate(t *testing.T, when time.Time) uuid.UUID {
	t.Helper()
	gofrsUUID, err := gofrs.NewV7AtTime(when)
	if err != nil {
		t.Fatalf("createPlanIDForDate: failed to create UUID v7: %v", err)
	}
	// Convert gofrs UUID to google UUID
	return uuid.UUID(gofrsUUID)
}

func TestSearchWithRetentionPeriod(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	retentionDays := 14

	tests := []struct {
		name          string
		plansSetup    func(t *testing.T, now time.Time) map[string][]storage.ListResult
		filters       storage.Filters
		wantPlanCount int
	}{
		{
			name: "Success: finds Running plan from today (0 days ago)",
			plansSetup: func(t *testing.T, now time.Time) map[string][]storage.ListResult {
				return map[string][]storage.ListResult{
					containerName("test", now): {
						{
							ID:         createPlanIDForDate(t, now),
							Name:       "today-plan",
							SubmitTime: now,
							State:      &workflow.State{Status: workflow.Running},
						},
					},
				}
			},
			filters:       storage.Filters{ByStatus: []workflow.Status{workflow.Running}},
			wantPlanCount: 1,
		},
		{
			name: "Success: finds Running plan from 1 day ago",
			plansSetup: func(t *testing.T, now time.Time) map[string][]storage.ListResult {
				planDate := now.AddDate(0, 0, -1)
				return map[string][]storage.ListResult{
					containerName("test", planDate): {
						{
							ID:         createPlanIDForDate(t, planDate),
							Name:       "one-day-ago-plan",
							SubmitTime: planDate,
							State:      &workflow.State{Status: workflow.Running},
						},
					},
				}
			},
			filters:       storage.Filters{ByStatus: []workflow.Status{workflow.Running}},
			wantPlanCount: 1,
		},
		{
			name: "Success: finds Running plan from 2 days ago",
			plansSetup: func(t *testing.T, now time.Time) map[string][]storage.ListResult {
				planDate := now.AddDate(0, 0, -2)
				return map[string][]storage.ListResult{
					containerName("test", planDate): {
						{
							ID:         createPlanIDForDate(t, planDate),
							Name:       "two-days-ago-plan",
							SubmitTime: planDate,
							State:      &workflow.State{Status: workflow.Running},
						},
					},
				}
			},
			filters:       storage.Filters{ByStatus: []workflow.Status{workflow.Running}},
			wantPlanCount: 1,
		},
		{
			name: "Success: finds Running plan from 7 days ago",
			plansSetup: func(t *testing.T, now time.Time) map[string][]storage.ListResult {
				planDate := now.AddDate(0, 0, -7)
				return map[string][]storage.ListResult{
					containerName("test", planDate): {
						{
							ID:         createPlanIDForDate(t, planDate),
							Name:       "seven-days-ago-plan",
							SubmitTime: planDate,
							State:      &workflow.State{Status: workflow.Running},
						},
					},
				}
			},
			filters:       storage.Filters{ByStatus: []workflow.Status{workflow.Running}},
			wantPlanCount: 1,
		},
		{
			name: "Success: finds Running plan from 13 days ago",
			plansSetup: func(t *testing.T, now time.Time) map[string][]storage.ListResult {
				planDate := now.AddDate(0, 0, -13)
				return map[string][]storage.ListResult{
					containerName("test", planDate): {
						{
							ID:         createPlanIDForDate(t, planDate),
							Name:       "thirteen-days-ago-plan",
							SubmitTime: planDate,
							State:      &workflow.State{Status: workflow.Running},
						},
					},
				}
			},
			filters:       storage.Filters{ByStatus: []workflow.Status{workflow.Running}},
			wantPlanCount: 1,
		},
		{
			name: "Success: does NOT find Running plan from 14 days ago (at boundary)",
			plansSetup: func(t *testing.T, now time.Time) map[string][]storage.ListResult {
				planDate := now.AddDate(0, 0, -14)
				return map[string][]storage.ListResult{
					containerName("test", planDate): {
						{
							ID:         createPlanIDForDate(t, planDate),
							Name:       "fourteen-days-ago-plan",
							SubmitTime: planDate,
							State:      &workflow.State{Status: workflow.Running},
						},
					},
				}
			},
			filters:       storage.Filters{ByStatus: []workflow.Status{workflow.Running}},
			wantPlanCount: 0, // searchContainerNames generates 0 to retentionDays-1 (0-13)
		},
		{
			name: "Success: does NOT find Running plan from 15 days ago (1 day beyond boundary)",
			plansSetup: func(t *testing.T, now time.Time) map[string][]storage.ListResult {
				planDate := now.AddDate(0, 0, -15)
				return map[string][]storage.ListResult{
					containerName("test", planDate): {
						{
							ID:         createPlanIDForDate(t, planDate),
							Name:       "fifteen-days-ago-plan",
							SubmitTime: planDate,
							State:      &workflow.State{Status: workflow.Running},
						},
					},
				}
			},
			filters:       storage.Filters{ByStatus: []workflow.Status{workflow.Running}},
			wantPlanCount: 0,
		},
		{
			name: "Success: finds multiple Running plans across retention period",
			plansSetup: func(t *testing.T, now time.Time) map[string][]storage.ListResult {
				twoDaysAgo := now.AddDate(0, 0, -2)
				sevenDaysAgo := now.AddDate(0, 0, -7)
				thirteenDaysAgo := now.AddDate(0, 0, -13)

				return map[string][]storage.ListResult{
					containerName("test", now): {
						{
							ID:         createPlanIDForDate(t, now),
							Name:       "today-plan",
							SubmitTime: now,
							State:      &workflow.State{Status: workflow.Running},
						},
					},
					containerName("test", twoDaysAgo): {
						{
							ID:         createPlanIDForDate(t, twoDaysAgo),
							Name:       "two-days-plan",
							SubmitTime: twoDaysAgo,
							State:      &workflow.State{Status: workflow.Running},
						},
					},
					containerName("test", sevenDaysAgo): {
						{
							ID:         createPlanIDForDate(t, sevenDaysAgo),
							Name:       "seven-days-plan",
							SubmitTime: sevenDaysAgo,
							State:      &workflow.State{Status: workflow.Running},
						},
					},
					containerName("test", thirteenDaysAgo): {
						{
							ID:         createPlanIDForDate(t, thirteenDaysAgo),
							Name:       "thirteen-days-plan",
							SubmitTime: thirteenDaysAgo,
							State:      &workflow.State{Status: workflow.Running},
						},
					},
				}
			},
			filters:       storage.Filters{ByStatus: []workflow.Status{workflow.Running}},
			wantPlanCount: 4,
		},
		{
			name: "Success: finds plans within retention, ignores plans beyond retention",
			plansSetup: func(t *testing.T, now time.Time) map[string][]storage.ListResult {
				twoDaysAgo := now.AddDate(0, 0, -2)
				fifteenDaysAgo := now.AddDate(0, 0, -15)
				twentyDaysAgo := now.AddDate(0, 0, -20)

				return map[string][]storage.ListResult{
					containerName("test", twoDaysAgo): {
						{
							ID:         createPlanIDForDate(t, twoDaysAgo),
							Name:       "within-retention",
							SubmitTime: twoDaysAgo,
							State:      &workflow.State{Status: workflow.Running},
						},
					},
					containerName("test", fifteenDaysAgo): {
						{
							ID:         createPlanIDForDate(t, fifteenDaysAgo),
							Name:       "beyond-retention-15",
							SubmitTime: fifteenDaysAgo,
							State:      &workflow.State{Status: workflow.Running},
						},
					},
					containerName("test", twentyDaysAgo): {
						{
							ID:         createPlanIDForDate(t, twentyDaysAgo),
							Name:       "beyond-retention-20",
							SubmitTime: twentyDaysAgo,
							State:      &workflow.State{Status: workflow.Running},
						},
					},
				}
			},
			filters:       storage.Filters{ByStatus: []workflow.Status{workflow.Running}},
			wantPlanCount: 1, // Only the 2-day-old plan should be found
		},
		{
			name: "Success: filters by status correctly",
			plansSetup: func(t *testing.T, now time.Time) map[string][]storage.ListResult {
				return map[string][]storage.ListResult{
					containerName("test", now): {
						{
							ID:         createPlanIDForDate(t, now),
							Name:       "running-plan",
							SubmitTime: now,
							State:      &workflow.State{Status: workflow.Running},
						},
						{
							ID:         createPlanIDForDate(t, now.Add(-time.Hour)),
							Name:       "completed-plan",
							SubmitTime: now.Add(-time.Hour),
							State:      &workflow.State{Status: workflow.Completed},
						},
						{
							ID:         createPlanIDForDate(t, now.Add(-2*time.Hour)),
							Name:       "failed-plan",
							SubmitTime: now.Add(-2 * time.Hour),
							State:      &workflow.State{Status: workflow.Failed},
						},
					},
				}
			},
			filters:       storage.Filters{ByStatus: []workflow.Status{workflow.Running}},
			wantPlanCount: 1, // Only the Running plan
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()

			plansByContainer := test.plansSetup(t, now)

			reg := registry.New()
			reg.Register(&testPlugins.HelloPlugin{})

			fakeClient := blobops.NewFake()
			r := reader{
				mu:            planlocks.New(ctx),
				readFlight:    &singleflight.Group{},
				existsFlight:  &singleflight.Group{},
				prefix:        "test",
				client:        fakeClient,
				reg:           reg,
				retentionDays: retentionDays,
				nowf:          func() time.Time { return now },
				testListPlansInContainer: func(ctx context.Context, cn string) ([]storage.ListResult, error) {
					if plans, ok := plansByContainer[cn]; ok {
						return plans, nil
					}
					return nil, nil
				},
			}

			resultCh, err := r.Search(ctx, test.filters)
			if err != nil {
				t.Errorf("[TestSearchWithRetentionPeriod](%s): got err == %v, want err == nil", test.name, err)
				return
			}

			var results []storage.ListResult
			for res := range resultCh {
				if res.Err != nil {
					t.Errorf("[TestSearchWithRetentionPeriod](%s): got result err == %v", test.name, res.Err)
					continue
				}
				results = append(results, res.Result)
			}

			if len(results) != test.wantPlanCount {
				t.Errorf("[TestSearchWithRetentionPeriod](%s): got %d results, want %d", test.name, len(results), test.wantPlanCount)
			}
		})
	}
}

func TestSearchRecoveryScenario(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	retentionDays := 14

	tests := []struct {
		name               string
		planAges           []int // days ago for each plan
		wantRecoveredCount int
		description        string
	}{
		{
			name:               "Success: recover plan from 2 days ago after service restart",
			planAges:           []int{2},
			wantRecoveredCount: 1,
			description:        "This was the original bug - a 2-day-old plan was not found because Search only looked at today/yesterday",
		},
		{
			name:               "Success: recover plans from multiple days within retention",
			planAges:           []int{0, 1, 2, 5, 10, 13},
			wantRecoveredCount: 6,
			description:        "All plans within retention period should be recovered",
		},
		{
			name:               "Success: recover only plans within retention when some are beyond",
			planAges:           []int{1, 5, 13, 15, 20},
			wantRecoveredCount: 3, // Only 1, 5, and 13 days ago
			description:        "Plans beyond retention (15, 20 days) should not be recovered",
		},
		{
			name:               "Success: no recovery for plans all beyond retention",
			planAges:           []int{15, 20, 30},
			wantRecoveredCount: 0,
			description:        "No plans should be recovered when all are beyond retention",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()

			plansByContainer := make(map[string][]storage.ListResult)
			for _, daysAgo := range test.planAges {
				planDate := now.AddDate(0, 0, -daysAgo)
				cn := containerName("test", planDate)
				plansByContainer[cn] = append(plansByContainer[cn], storage.ListResult{
					ID:         createPlanIDForDate(t, planDate),
					Name:       "recovery-test-plan",
					SubmitTime: planDate,
					State:      &workflow.State{Status: workflow.Running},
				})
			}

			reg := registry.New()
			reg.Register(&testPlugins.HelloPlugin{})

			fakeClient := blobops.NewFake()
			r := reader{
				mu:            planlocks.New(ctx),
				readFlight:    &singleflight.Group{},
				existsFlight:  &singleflight.Group{},
				prefix:        "test",
				client:        fakeClient,
				reg:           reg,
				retentionDays: retentionDays,
				nowf:          func() time.Time { return now },
				testListPlansInContainer: func(ctx context.Context, cn string) ([]storage.ListResult, error) {
					if plans, ok := plansByContainer[cn]; ok {
						return plans, nil
					}
					return nil, nil
				},
			}

			resultCh, err := r.Search(ctx, storage.Filters{ByStatus: []workflow.Status{workflow.Running}})
			if err != nil {
				t.Errorf("[TestSearchRecoveryScenario](%s): got err == %v, want err == nil", test.name, err)
				return
			}

			var results []storage.ListResult
			for res := range resultCh {
				if res.Err != nil {
					t.Errorf("[TestSearchRecoveryScenario](%s): got result err == %v", test.name, res.Err)
					continue
				}
				results = append(results, res.Result)
			}

			if len(results) != test.wantRecoveredCount {
				t.Errorf("[TestSearchRecoveryScenario](%s): got %d recovered plans, want %d. %s", test.name, len(results), test.wantRecoveredCount, test.description)
			}
		})
	}
}

func TestListWithRetentionPeriod(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	retentionDays := 14

	tests := []struct {
		name          string
		plansSetup    func(t *testing.T) map[string][]storage.ListResult
		limit         int
		wantPlanCount int
	}{
		{
			name: "Success: lists plans from multiple days within retention",
			plansSetup: func(t *testing.T) map[string][]storage.ListResult {
				twoDaysAgo := now.AddDate(0, 0, -2)
				sevenDaysAgo := now.AddDate(0, 0, -7)

				return map[string][]storage.ListResult{
					containerName("test", now): {
						{
							ID:         createPlanIDForDate(t, now),
							Name:       "today-plan",
							SubmitTime: now,
							State:      &workflow.State{Status: workflow.Running},
						},
					},
					containerName("test", twoDaysAgo): {
						{
							ID:         createPlanIDForDate(t, twoDaysAgo),
							Name:       "two-days-plan",
							SubmitTime: twoDaysAgo,
							State:      &workflow.State{Status: workflow.Completed},
						},
					},
					containerName("test", sevenDaysAgo): {
						{
							ID:         createPlanIDForDate(t, sevenDaysAgo),
							Name:       "seven-days-plan",
							SubmitTime: sevenDaysAgo,
							State:      &workflow.State{Status: workflow.Failed},
						},
					},
				}
			},
			limit:         0,
			wantPlanCount: 3,
		},
		{
			name: "Success: does NOT list plans beyond retention",
			plansSetup: func(t *testing.T) map[string][]storage.ListResult {
				fifteenDaysAgo := now.AddDate(0, 0, -15)

				return map[string][]storage.ListResult{
					containerName("test", now): {
						{
							ID:         createPlanIDForDate(t, now),
							Name:       "today-plan",
							SubmitTime: now,
							State:      &workflow.State{Status: workflow.Running},
						},
					},
					containerName("test", fifteenDaysAgo): {
						{
							ID:         createPlanIDForDate(t, fifteenDaysAgo),
							Name:       "beyond-retention",
							SubmitTime: fifteenDaysAgo,
							State:      &workflow.State{Status: workflow.Running},
						},
					},
				}
			},
			limit:         0,
			wantPlanCount: 1, // Only today's plan
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()

			plansByContainer := test.plansSetup(t)

			reg := registry.New()
			reg.Register(&testPlugins.HelloPlugin{})

			fakeClient := blobops.NewFake()
			r := reader{
				mu:            planlocks.New(ctx),
				readFlight:    &singleflight.Group{},
				existsFlight:  &singleflight.Group{},
				prefix:        "test",
				client:        fakeClient,
				reg:           reg,
				retentionDays: retentionDays,
				nowf:          func() time.Time { return now },
				testListPlansInContainer: func(ctx context.Context, cn string) ([]storage.ListResult, error) {
					if plans, ok := plansByContainer[cn]; ok {
						return plans, nil
					}
					return nil, nil
				},
			}

			resultCh, err := r.List(ctx, test.limit)
			if err != nil {
				t.Errorf("[TestListWithRetentionPeriod](%s): got err == %v, want err == nil", test.name, err)
				return
			}

			var results []storage.ListResult
			for res := range resultCh {
				if res.Err != nil {
					t.Errorf("[TestListWithRetentionPeriod](%s): got result err == %v", test.name, res.Err)
					continue
				}
				results = append(results, res.Result)
			}

			if len(results) != test.wantPlanCount {
				t.Errorf("[TestListWithRetentionPeriod](%s): got %d results, want %d", test.name, len(results), test.wantPlanCount)
			}
		})
	}
}
