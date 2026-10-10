package cosmosdb

import (
	"net"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"
	"github.com/go-json-experiment/json"
	"github.com/google/uuid"
	"github.com/gostdlib/base/concurrency/sync"
	"github.com/gostdlib/base/context"
	"github.com/gostdlib/base/values/chans"

	"github.com/element-of-surprise/coercion/workflow"
)

// fakeReadClient is a readerClient that counts ReadItem calls. It returns err for every call if err is set, plan for
// planID, and a 404 for any other id.
type fakeReadClient struct {
	planID string
	plan   []byte
	err    error
	calls  *atomic.Int64
	// called, if not nil, receives a value (without blocking) on each ReadItem call.
	called chan struct{}
}

func (f fakeReadClient) ReadItem(ctx context.Context, pk azcosmos.PartitionKey, id string, o *azcosmos.ItemOptions) (azcosmos.ItemResponse, error) {
	f.calls.Add(1)
	if f.called != nil {
		chans.TryPut(f.called, struct{}{})
	}
	if f.err != nil {
		return azcosmos.ItemResponse{}, f.err
	}
	if id == f.planID {
		return azcosmos.ItemResponse{Value: f.plan}, nil
	}
	return azcosmos.ItemResponse{}, runtime.NewResponseError(&http.Response{StatusCode: http.StatusNotFound})
}

func (f fakeReadClient) NewQueryItemsPager(query string, pk azcosmos.PartitionKey, o *azcosmos.QueryOptions) *runtime.Pager[azcosmos.QueryItemsResponse] {
	return nil
}

func TestReadRetry(t *testing.T) {
	t.Parallel()

	id := uuid.New()
	plan, err := json.Marshal(plansEntry{ID: id, PlanID: id, Name: "plan", StateStatus: workflow.Running})
	if err != nil {
		t.Fatalf("TestReadRetry: could not marshal the plan entry: %s", err)
	}
	withSubDoc, err := json.Marshal(plansEntry{ID: id, PlanID: id, Name: "plan", PreChecks: uuid.New()})
	if err != nil {
		t.Fatalf("TestReadRetry: could not marshal the plan entry: %s", err)
	}

	tests := []struct {
		name string
		plan []byte
		err  error
		// wantCalls is the number of ReadItem calls Read must make. A permanent error must not be retried.
		wantCalls int64
		wantErr   bool
	}{
		{
			name:      "Success: a plan document that decodes is read with a single attempt",
			plan:      plan,
			wantCalls: 1,
		},
		{
			// Regression: the decode error text contained "unexpected EOF", so it was retried 5 times though it can
			// never succeed.
			name:      "Error: a truncated plan document is a permanent error and is read only once",
			plan:      plan[:len(plan)/2],
			wantCalls: 1,
			wantErr:   true,
		},
		{
			name:      "Error: an unauthorized response is a permanent error and is read only once",
			plan:      plan,
			err:       runtime.NewResponseError(&http.Response{StatusCode: http.StatusUnauthorized}),
			wantCalls: 1,
			wantErr:   true,
		},
		{
			// A plan document plus its one missing sub-document is two calls for one attempt.
			name:      "Error: a plan with a missing sub-document is a permanent error and is read only once",
			plan:      withSubDoc,
			wantCalls: 2,
			wantErr:   true,
		},
	}

	for _, test := range tests {
		calls := &atomic.Int64{}
		r := reader{
			mu:     &sync.RWMutex{},
			client: fakeReadClient{planID: id.String(), plan: test.plan, err: test.err, calls: calls},
			reg:    testReg,
		}

		_, err := r.Read(t.Context(), id)
		if got := calls.Load(); got != test.wantCalls {
			t.Errorf("TestReadRetry(%s): got %d ReadItem calls, want %d", test.name, got, test.wantCalls)
		}
		switch {
		case err == nil && test.wantErr:
			t.Errorf("TestReadRetry(%s): got err == nil, want err != nil", test.name)
			continue
		case err != nil && !test.wantErr:
			t.Errorf("TestReadRetry(%s): got err == %s, want err == nil", test.name, err)
			continue
		}
	}
}

// TestReadRetryWait is a regression test: Read held the store-wide read lock across its backoff waits and ignored the
// caller's ctx, so a failing Read blocked every writer, and then every reader, for the whole retry window.
func TestReadRetryWait(t *testing.T) {
	t.Parallel()

	const bound = 2 * time.Second

	called := make(chan struct{}, 1)
	r := reader{
		mu:     &sync.RWMutex{},
		client: fakeReadClient{err: &net.DNSError{IsTemporary: true}, calls: &atomic.Int64{}, called: called},
		reg:    testReg,
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	readErr := make(chan error, 1)
	g := context.Pool(t.Context()).Group()
	g.Go(
		t.Context(),
		func(context.Context) error {
			_, err := r.Read(ctx, uuid.New())
			readErr <- err
			return nil
		},
	)
	defer g.Wait(t.Context())

	waitCtx, waitCancel := context.WithTimeout(t.Context(), bound)
	defer waitCancel()
	if _, res := chans.Get(waitCtx, called); !res.OK() {
		t.Fatalf("TestReadRetryWait: Read never called ReadItem")
	}

	// A writer must get the lock while Read waits to retry.
	locked := false
	for deadline := time.Now().Add(bound); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if r.mu.TryLock() {
			locked = true
			r.mu.Unlock()
			break
		}
	}
	if !locked {
		t.Errorf("TestReadRetryWait: a writer could not take the lock within %v while Read was retrying", bound)
	}

	// Cancelling the caller's ctx must end Read promptly.
	cancel()
	endCtx, endCancel := context.WithTimeout(t.Context(), bound)
	defer endCancel()
	err, res := chans.Get(endCtx, readErr)
	switch {
	case !res.OK():
		t.Errorf("TestReadRetryWait: Read did not return within %v of its ctx being cancelled", bound)
	case err == nil:
		t.Errorf("TestReadRetryWait: got err == nil, want err != nil")
	}
}
