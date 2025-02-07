package cosmosdb

import (
	"fmt"
	"net"
	"net/http"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
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

func TestIsRetriableError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "unknown error",
			err:  fmt.Errorf("test error"),
			want: false,
		},
		{
			name: "unauthorized error",
			err:  runtime.NewResponseError(&http.Response{StatusCode: http.StatusUnauthorized}),
			want: false,
		},
		{
			name: "temporary dns error",
			err:  &net.DNSError{IsTemporary: true},
			want: true,
		},
		{
			name: "connection error",
			err:  fmt.Errorf("context deadline exceeded"),
			want: true,
		},
		{
			name: "wrapped connection error",
			err:  runtime.NewResponseError(&http.Response{Status: "context deadline exceeded"}),
			want: true,
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
