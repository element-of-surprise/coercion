package cosmosdb

import (
	"errors"
	"testing"

	"github.com/gostdlib/base/context"

	"github.com/element-of-surprise/coercion/workflow"
	"github.com/element-of-surprise/coercion/workflow/storage"
	"github.com/google/uuid"
	"github.com/kylelemons/godebug/pretty"
)

var pConfig = &pretty.Config{
	PrintStringers: true,
}

type fakeSearch struct {
	results                       []storage.ListResult
	readResult                    *workflow.Plan
	searchErr, readErr, updateErr bool

	storage.Reader
}

func (f *fakeSearch) Read(ctx context.Context, id uuid.UUID) (*workflow.Plan, error) {
	if f.readErr {
		return nil, errors.New("error")
	}
	return f.readResult, nil
}

func (f *fakeSearch) Search(ctx context.Context, filters storage.Filters) (chan storage.Stream[storage.ListResult], error) {
	if f.searchErr {
		return nil, errors.New("error")
	}

	ch := make(chan storage.Stream[storage.ListResult], 1)
	go func() {
		defer close(ch)
		for _, result := range f.results {
			ch <- storage.Stream[storage.ListResult]{Result: result}
		}
	}()

	return ch, nil
}

type fakeUpdatePlan struct {
	err       bool
	updateWas *workflow.Plan
	storage.Updater
}

func (f *fakeUpdatePlan) UpdatePlan(ctx context.Context, plan *workflow.Plan) error {
	if f.err {
		return errors.New("error")
	}
	f.updateWas = plan
	return nil
}

func TestRecovery(t *testing.T) {
	t.Parallel()

	searchResult := []storage.ListResult{{ID: uuid.New()}}
	readResult := &workflow.Plan{ID: searchResult[0].ID}

	tests := []struct {
		name                          string
		results                       []storage.ListResult
		readResult                    *workflow.Plan
		searchErr, readErr, updateErr bool
		err                           bool
	}{
		{
			name: "No results",
		},
		{
			name:      "Error: search",
			searchErr: true,
			err:       true,
		},
		{
			name:    "Error: read plan",
			results: searchResult,
			readErr: true,
			err:     true,
		},
		{
			name:       "Error: update plan",
			results:    searchResult,
			readResult: readResult,
			updateErr:  true,
			err:        true,
		},
		{
			name:       "Success",
			results:    searchResult,
			readResult: readResult,
		},
	}

	for _, test := range tests {
		r := recovery{
			reader: &fakeSearch{
				results:    test.results,
				readResult: test.readResult,
				searchErr:  test.searchErr,
				readErr:    test.readErr,
			},
			updater: &fakeUpdatePlan{err: test.updateErr},
		}
		ctx := context.Background()

		err := r.Recovery(ctx)
		switch {
		case test.err && err == nil:
			t.Errorf("TestRecovery(%s): got err == nil, want err != nil", test.name)
			continue
		case !test.err && err != nil:
			t.Errorf("TestRecovery(%s): got err == %v, want err == nil", test.name, err)
			continue
		case err != nil:
			continue
		}
		// No results were found.
		if r.updater.(*fakeUpdatePlan).updateWas == nil {
			continue
		}

		if diff := pConfig.Compare(r.updater.(*fakeUpdatePlan).updateWas, readResult); diff != "" {
			t.Errorf("TestRecovery(%s): -want/+got:\n%s", test.name, diff)
		}
	}
}
