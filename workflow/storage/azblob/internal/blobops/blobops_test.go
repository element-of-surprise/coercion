package blobops

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/bloberror"
	"github.com/gostdlib/base/concurrency/sync"
	"github.com/gostdlib/base/context"
	"github.com/kylelemons/godebug/pretty"
)

// fakeResp is one scripted HTTP response from fakeServer.
type fakeResp struct {
	status int
	// code is sent as the x-ms-error-code header when set.
	code bloberror.Code
	body string
	// stall never answers: the handler waits until the client gives up on the request.
	stall bool
}

// fakeServer answers requests in order with the scripted responses, repeating the last one once they run out.
type fakeServer struct {
	mu    sync.Mutex
	resps []fakeResp
	calls int
}

func (f *fakeServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	i := min(f.calls, len(f.resps)-1)
	f.calls++
	resp := f.resps[i]
	f.mu.Unlock()

	if resp.stall {
		<-r.Context().Done()
		return
	}
	if resp.code != "" {
		w.Header().Set("x-ms-error-code", string(resp.code))
	}
	if resp.status == http.StatusOK {
		w.Header().Set("Content-Length", fmt.Sprint(len(resp.body)))
	}
	w.WriteHeader(resp.status)
	_, _ = w.Write([]byte(resp.body))
}

// Calls returns how many requests the server received.
func (f *fakeServer) Calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// newTestReal returns a Real talking to srv, with the client built from ClientOptions as production builds it. Only the
// delays between tries and the per-try timeout are scaled down, so the number of tries is the production one and a try
// is bounded exactly when production bounds it.
func newTestReal(t *testing.T, srv *httptest.Server) *Real {
	t.Helper()

	opts := ClientOptions()
	opts.Retry.RetryDelay = time.Millisecond
	opts.Retry.MaxRetryDelay = 5 * time.Millisecond
	opts.Retry.TryTimeout /= 1000
	client, err := azblob.NewClientWithNoCredential(srv.URL+"/account", opts)
	if err != nil {
		t.Fatalf("newTestReal: azblob.NewClientWithNoCredential: %s", err)
	}
	return &Real{Client: client}
}

var (
	respOK       = fakeResp{status: http.StatusOK, body: "data"}
	respBusy     = fakeResp{status: http.StatusServiceUnavailable, code: bloberror.ServerBusy}
	respNotFound = fakeResp{status: http.StatusNotFound, code: bloberror.BlobNotFound}
	respStall    = fakeResp{stall: true}
)

func TestGetBlob(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		resps []fakeResp
		// canceled ends the caller's context before reading.
		canceled     bool
		wantData     string
		wantCalls    int
		wantNotFound bool
		wantErr      bool
	}{
		{
			// Regression: reads retried on a context that could not be cancelled, so a caller with a deadline was held
			// for the whole retry budget while the account was throttled.
			name:      "Error: an ended context stops the read instead of retrying",
			resps:     []fakeResp{respOK},
			canceled:  true,
			wantCalls: 0,
			wantErr:   true,
		},
		{
			name:      "Success: blob is downloaded on the first try",
			resps:     []fakeResp{respOK},
			wantData:  "data",
			wantCalls: 1,
		},
		{
			name:      "Success: server busy is retried instead of panicking, then the blob is downloaded",
			resps:     []fakeResp{respBusy, respBusy, respOK},
			wantData:  "data",
			wantCalls: 3,
		},
		{
			// Regression: Real retried on top of the SDK's own retries, so one request was tried up to 4 times per
			// attempt. Only the client's policy retries now.
			name:      "Error: server busy on every attempt returns an error after the attempt limit",
			resps:     []fakeResp{respBusy},
			wantCalls: maxRetries + 1,
			wantErr:   true,
		},
		{
			// Regression: a try had no time limit, so a server that never answered held the read, and the plan lock
			// above it, for good. Each try is now bounded and the stalled request is retried.
			name:      "Error: a server that never answers times out each try and returns an error after the attempt limit",
			resps:     []fakeResp{respStall},
			wantCalls: maxRetries + 1,
			wantErr:   true,
		},
		{
			name:         "Error: blob not found is not retried and is identifiable as not found",
			resps:        []fakeResp{respNotFound},
			wantCalls:    1,
			wantNotFound: true,
			wantErr:      true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			fs := &fakeServer{resps: test.resps}
			srv := httptest.NewServer(fs)
			t.Cleanup(srv.Close)
			r := newTestReal(t, srv)

			// Bounds the test if a stalled try is never timed out.
			ctx, cancelTest := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancelTest()
			if test.canceled {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			data, err := r.GetBlob(ctx, "container", "plans/blob.json")
			if got := fs.Calls(); got != test.wantCalls {
				t.Errorf("TestGetBlob(%s): got %d calls, want %d", test.name, got, test.wantCalls)
			}
			switch {
			case err == nil && test.wantErr:
				t.Errorf("TestGetBlob(%s): got err == nil, want err != nil", test.name)
				return
			case err != nil && !test.wantErr:
				t.Errorf("TestGetBlob(%s): got err == %s, want err == nil", test.name, err)
				return
			case err != nil:
				// The readers in azblob (reader.go exists, reader_plan.go) treat IsNotFound as a missing plan or blob, so
				// the type is part of the contract.
				if IsNotFound(err) != test.wantNotFound {
					t.Errorf("TestGetBlob(%s): got IsNotFound(err) == %v, want %v", test.name, IsNotFound(err), test.wantNotFound)
				}
				return
			}
			if string(data) != test.wantData {
				t.Errorf("TestGetBlob(%s): got data %q, want %q", test.name, data, test.wantData)
			}
		})
	}
}

// listXML is a List Blobs response holding one blob with plantype=entry metadata.
const listXML = `<?xml version="1.0" encoding="utf-8"?>
<EnumerationResults ServiceEndpoint="http://127.0.0.1/account/" ContainerName="container">
<Prefix>plans/</Prefix>
<Blobs><Blob><Name>plans/a-entry.json</Name><Properties></Properties><Metadata><plantype>entry</plantype></Metadata></Blob></Blobs>
<NextMarker />
</EnumerationResults>`

func TestNextListPage(t *testing.T) {
	t.Parallel()

	respList := fakeResp{status: http.StatusOK, body: listXML}
	respNoContainer := fakeResp{status: http.StatusNotFound, code: bloberror.ContainerNotFound}

	tests := []struct {
		name  string
		resps []fakeResp
		// canceled ends the caller's context before listing.
		canceled     bool
		wantNames    []string
		wantCalls    int
		wantNotFound bool
		wantErr      bool
	}{
		{
			// Regression: listing retried on a context that could not be cancelled, so startup recovery was held for
			// the whole retry budget while the account was throttled.
			name:      "Error: an ended context stops listing instead of retrying",
			resps:     []fakeResp{respList},
			canceled:  true,
			wantCalls: 0,
			wantErr:   true,
		},
		{
			name:      "Success: page is listed on the first try",
			resps:     []fakeResp{respList},
			wantNames: []string{"plans/a-entry.json"},
			wantCalls: 1,
		},
		{
			name:      "Success: server busy is retried and the same page is fetched again",
			resps:     []fakeResp{respBusy, respList},
			wantNames: []string{"plans/a-entry.json"},
			wantCalls: 2,
		},
		{
			// Regression: Real retried on top of the SDK's own retries, so one request was tried up to 4 times per
			// attempt. Only the client's policy retries now.
			name:      "Error: server busy on every attempt returns an error after the attempt limit",
			resps:     []fakeResp{respBusy},
			wantCalls: maxRetries + 1,
			wantErr:   true,
		},
		{
			name:         "Error: missing container is not retried and is identifiable as not found",
			resps:        []fakeResp{respNoContainer},
			wantCalls:    1,
			wantNotFound: true,
			wantErr:      true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			fs := &fakeServer{resps: test.resps}
			srv := httptest.NewServer(fs)
			t.Cleanup(srv.Close)
			r := newTestReal(t, srv)

			ctx := t.Context()
			if test.canceled {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			pager := r.NewListBlobsFlatPager("container", &azblob.ListBlobsFlatOptions{Prefix: toPtr("plans/")})
			page, err := r.NextListPage(ctx, pager)
			if got := fs.Calls(); got != test.wantCalls {
				t.Errorf("TestNextListPage(%s): got %d calls, want %d", test.name, got, test.wantCalls)
			}
			switch {
			case err == nil && test.wantErr:
				t.Errorf("TestNextListPage(%s): got err == nil, want err != nil", test.name)
				return
			case err != nil && !test.wantErr:
				t.Errorf("TestNextListPage(%s): got err == %s, want err == nil", test.name, err)
				return
			case err != nil:
				// scan.go scanPlans skips a container that IsNotFound (nothing written that day), so the type is part of the
				// contract.
				if IsNotFound(err) != test.wantNotFound {
					t.Errorf("TestNextListPage(%s): got IsNotFound(err) == %v, want %v", test.name, IsNotFound(err), test.wantNotFound)
				}
				return
			}

			var names []string
			for _, item := range page.Segment.BlobItems {
				names = append(names, *item.Name)
			}
			if diff := pretty.Compare(test.wantNames, names); diff != "" {
				t.Errorf("TestNextListPage(%s): blob names -want +got:\n%s", test.name, diff)
			}
		})
	}
}
