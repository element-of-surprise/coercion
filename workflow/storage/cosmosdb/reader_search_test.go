package cosmosdb

import (
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"
	"github.com/go-json-experiment/json"
	"github.com/google/uuid"
	"github.com/gostdlib/base/concurrency/sync"
	"github.com/gostdlib/base/context"
	"github.com/gostdlib/base/values/chans"

	"github.com/element-of-surprise/coercion/workflow/storage"
)

// fakeSearchClient is a readerClient whose query pager returns one search entry per page. If pages < 0 the pager
// never ends, so a stream over it can only stop because ctx ended.
type fakeSearchClient struct {
	pages int
	item  []byte
}

func (f fakeSearchClient) ReadItem(ctx context.Context, pk azcosmos.PartitionKey, id string, o *azcosmos.ItemOptions) (azcosmos.ItemResponse, error) {
	return azcosmos.ItemResponse{}, nil
}

func (f fakeSearchClient) NewQueryItemsPager(query string, pk azcosmos.PartitionKey, o *azcosmos.QueryOptions) *runtime.Pager[azcosmos.QueryItemsResponse] {
	fetched := 0
	return runtime.NewPager(runtime.PagingHandler[azcosmos.QueryItemsResponse]{
		More: func(page azcosmos.QueryItemsResponse) bool {
			return f.pages < 0 || fetched < f.pages
		},
		Fetcher: func(ctx context.Context, page *azcosmos.QueryItemsResponse) (azcosmos.QueryItemsResponse, error) {
			fetched++
			return azcosmos.QueryItemsResponse{Items: [][]byte{f.item}}, nil
		},
	})
}

func TestSearchStream(t *testing.T) {
	t.Parallel()

	item, err := json.Marshal(searchEntry{ID: uuid.New(), GroupID: uuid.New(), Name: "plan"})
	if err != nil {
		t.Fatalf("TestSearchStream: could not marshal the search entry: %s", err)
	}

	search := func(ctx context.Context, r reader) (chan storage.Stream[storage.ListResult], error) {
		return r.Search(ctx, storage.Filters{ByIDs: []uuid.UUID{uuid.New()}})
	}
	list := func(ctx context.Context, r reader) (chan storage.Stream[storage.ListResult], error) {
		return r.List(ctx, 0)
	}

	tests := []struct {
		name string
		call func(ctx context.Context, r reader) (chan storage.Stream[storage.ListResult], error)
		// pages is the number of pages the fake returns, < 0 for a pager that never ends.
		pages int
		// cancelBefore cancels ctx before the call, so the producer cannot be submitted.
		cancelBefore bool
		// cancelAfter cancels ctx once this many results have been read. 0 never cancels mid-stream.
		cancelAfter int
		wantResults int
		// wantErr is true if the stream must end with an error result.
		wantErr bool
	}{
		{
			name:        "Success: Search streams every result and ends without an error",
			call:        search,
			pages:       3,
			wantResults: 3,
		},
		{
			name:         "Error: Search with a ctx cancelled before the call closes the stream with an error result",
			call:         search,
			pages:        3,
			cancelBefore: true,
			wantErr:      true,
		},
		{
			name:        "Error: Search with a ctx cancelled mid-stream ends the stream with an error result",
			call:        search,
			pages:       -1,
			cancelAfter: 1,
			wantErr:     true,
		},
		{
			name:        "Success: List streams every result and ends without an error",
			call:        list,
			pages:       3,
			wantResults: 3,
		},
		{
			name:         "Error: List with a ctx cancelled before the call closes the stream with an error result",
			call:         list,
			pages:        3,
			cancelBefore: true,
			wantErr:      true,
		},
		{
			name:        "Error: List with a ctx cancelled mid-stream ends the stream with an error result",
			call:        list,
			pages:       -1,
			cancelAfter: 1,
			wantErr:     true,
		},
	}

	for _, test := range tests {
		// Each row runs in its own func so its contexts are cancelled when the row ends, not when the test ends.
		func() {
			r := reader{mu: &sync.RWMutex{}, client: fakeSearchClient{pages: test.pages, item: item}}

			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if test.cancelBefore {
				cancel()
			}

			results, err := test.call(ctx, r)
			if err != nil {
				t.Errorf("TestSearchStream(%s): got err == %s, want err == nil", test.name, err)
				return
			}

			// The wait is bounded so a stream that is never closed fails the test instead of hanging it.
			waitCtx, waitCancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer waitCancel()

			var got []storage.Stream[storage.ListResult]
			if test.cancelAfter > 0 {
				var closed bool
				got, closed = chans.Collect(waitCtx, results, chans.WithLimit(test.cancelAfter))
				switch {
				case closed:
					t.Errorf("TestSearchStream(%s): the stream closed after %d results, before cancelAfter(%d) results arrived", test.name, len(got), test.cancelAfter)
					return
				case len(got) < test.cancelAfter:
					t.Errorf("TestSearchStream(%s): cancelAfter(%d) results never arrived, got %d", test.name, test.cancelAfter, len(got))
					return
				}
				cancel()
			}
			rest, closed := chans.Collect(waitCtx, results)
			got = append(got, rest...)
			if !closed {
				t.Errorf("TestSearchStream(%s): the stream was not closed", test.name)
				return
			}

			var last storage.Stream[storage.ListResult]
			if len(got) > 0 {
				last = got[len(got)-1]
			}
			switch {
			case last.Err == nil && test.wantErr:
				t.Errorf("TestSearchStream(%s): got a stream that ended without an error result, want one ending with an error result", test.name)
				return
			case last.Err != nil && !test.wantErr:
				t.Errorf("TestSearchStream(%s): got err == %s, want err == nil", test.name, last.Err)
				return
			case last.Err != nil:
				return
			}
			if len(got) != test.wantResults {
				t.Errorf("TestSearchStream(%s): got %d results, want %d", test.name, len(got), test.wantResults)
			}
		}()
	}
}
