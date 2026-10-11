package cosmosdb

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"syscall"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/go-json-experiment/json"
	"github.com/gostdlib/base/context"

	"github.com/element-of-surprise/coercion/workflow/errors"
)

func TestIsNotFound(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "nil",
			want: false,
		},
		{
			name: "unknown error",
			err:  fmt.Errorf("test error"),
			want: false,
		},
		{
			name: "not found",
			err:  runtime.NewResponseError(&http.Response{StatusCode: http.StatusNotFound}),
			want: true,
		},
	}

	for _, test := range tests {
		notFound := isNotFound(test.err)
		if test.want != notFound {
			t.Errorf("TestNotFound(%s): got %t, want %t", test.name, notFound, test.want)
			continue
		}
	}
}

func TestIsConflict(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "nil",
			want: false,
		},
		{
			name: "unknown error",
			err:  fmt.Errorf("test error"),
			want: false,
		},
		{
			name: "conflict",
			err:  runtime.NewResponseError(&http.Response{StatusCode: http.StatusConflict}),
			want: true,
		},
	}

	for _, test := range tests {
		notFound := isConflict(test.err)
		if test.want != notFound {
			t.Errorf("TestConflict(%s): got %t, want %t", test.name, notFound, test.want)
			continue
		}
	}
}

// fakeNetErr is a net.Error that reports a timeout.
type fakeNetErr struct{}

func (fakeNetErr) Error() string   { return "fake timeout" }
func (fakeNetErr) Timeout() bool   { return true }
func (fakeNetErr) Temporary() bool { return true }

func TestIsRetriableError(t *testing.T) {
	t.Parallel()

	var decodeErr error
	if err := json.Unmarshal([]byte(`{"name": "pla`), &searchEntry{}); err != nil {
		decodeErr = fmt.Errorf("couldn't decode plan: %w", err)
	}
	if decodeErr == nil {
		t.Fatalf("TestIsRetriableError: decoding a truncated document did not fail")
	}
	opErr := func(err error) error {
		return &url.Error{Op: "Get", URL: "https://cosmos", Err: &net.OpError{Op: "dial", Net: "tcp", Err: &os.SyscallError{Syscall: "connect", Err: err}}}
	}

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "Success: an untyped error is not retriable",
			err:  fmt.Errorf("test error"),
			want: false,
		},
		{
			name: "Success: an untyped error whose text looks like a connection reset is not retriable",
			err:  fmt.Errorf("connection reset by peer"),
			want: false,
		},
		{
			name: "Success: an unauthorized response is not retriable",
			err:  runtime.NewResponseError(&http.Response{StatusCode: http.StatusUnauthorized}),
			want: false,
		},
		{
			name: "Success: a DNS error is retriable",
			err:  &net.DNSError{IsTemporary: true},
			want: true,
		},
		{
			name: "Success: a per-try deadline is retriable",
			err:  fmt.Errorf("try: %w", context.DeadlineExceeded),
			want: true,
		},
		{
			name: "Success: a cancelled ctx is not retriable",
			err:  fmt.Errorf("try: %w", context.Canceled),
			want: false,
		},
		{
			name: "Success: a refused connection is retriable",
			err:  opErr(syscall.ECONNREFUSED),
			want: true,
		},
		{
			name: "Success: a reset connection is retriable",
			err:  opErr(syscall.ECONNRESET),
			want: true,
		},
		{
			name: "Success: a timed out connection is retriable",
			err:  opErr(syscall.ETIMEDOUT),
			want: true,
		},
		{
			name: "Success: a net.Error timeout is retriable",
			err:  &url.Error{Op: "Get", URL: "https://cosmos", Err: fakeNetErr{}},
			want: true,
		},
		{
			name: "Success: a transport EOF is retriable",
			err:  &url.Error{Op: "Get", URL: "https://cosmos", Err: io.EOF},
			want: true,
		},
		{
			name: "Success: a transport unexpected EOF is retriable",
			err:  &url.Error{Op: "Get", URL: "https://cosmos", Err: io.ErrUnexpectedEOF},
			want: true,
		},
		{
			// Regression: the decode error text contains "unexpected EOF", so it was retried though it can never succeed.
			name: "Success: a decode error of a truncated document is not retriable",
			err:  decodeErr,
			want: false,
		},
		{
			name: "Success: a retriable error marked permanent is not retriable",
			err:  fmt.Errorf("%w: %w", opErr(syscall.ECONNRESET), errors.ErrPermanent),
			want: false,
		},
	}

	for _, test := range tests {
		shouldRetry := isRetriableError(test.err)
		if test.want != shouldRetry {
			t.Errorf("TestIsRetriableError(%s): got %t, want %t", test.name, shouldRetry, test.want)
			continue
		}
	}
}
