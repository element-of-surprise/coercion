package azblob

import (
	"errors"
	"net/http"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/bloberror"
)

func TestIsNotFound(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "Success: nil error",
			err:  nil,
			want: false,
		},
		{
			name: "Success: blob not found",
			err:  &azcore.ResponseError{ErrorCode: string(bloberror.BlobNotFound)},
			want: true,
		},
		{
			name: "Success: container not found",
			err:  &azcore.ResponseError{ErrorCode: string(bloberror.ContainerNotFound)},
			want: true,
		},
		{
			name: "Success: other error",
			err:  &azcore.ResponseError{ErrorCode: string(bloberror.ServerBusy)},
			want: false,
		},
		{
			name: "Success: generic error",
			err:  errors.New("some error"),
			want: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := isNotFound(test.err)
			if got != test.want {
				t.Errorf("TestIsNotFound(%s): got %v, want %v", test.name, got, test.want)
			}
		})
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
			name: "Success: nil error",
			err:  nil,
			want: false,
		},
		{
			name: "Success: container already exists",
			err:  &azcore.ResponseError{ErrorCode: string(bloberror.ContainerAlreadyExists)},
			want: true,
		},
		{
			name: "Success: blob already exists",
			err:  &azcore.ResponseError{ErrorCode: string(bloberror.BlobAlreadyExists)},
			want: true,
		},
		{
			name: "Success: other error",
			err:  &azcore.ResponseError{ErrorCode: string(bloberror.ServerBusy)},
			want: false,
		},
		{
			name: "Success: generic error",
			err:  errors.New("some error"),
			want: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := isConflict(test.err)
			if got != test.want {
				t.Errorf("TestIsConflict(%s): got %v, want %v", test.name, got, test.want)
			}
		})
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
			name: "Success: nil error",
			err:  nil,
			want: false,
		},
		{
			name: "Success: server busy",
			err:  &azcore.ResponseError{ErrorCode: string(bloberror.ServerBusy)},
			want: true,
		},
		{
			name: "Success: internal error",
			err:  &azcore.ResponseError{ErrorCode: string(bloberror.InternalError)},
			want: true,
		},
		{
			name: "Success: operation timed out",
			err:  &azcore.ResponseError{ErrorCode: string(bloberror.OperationTimedOut)},
			want: true,
		},
		{
			name: "Success: HTTP 408 Request Timeout",
			err:  &azcore.ResponseError{StatusCode: http.StatusRequestTimeout},
			want: true,
		},
		{
			name: "Success: HTTP 429 Too Many Requests",
			err:  &azcore.ResponseError{StatusCode: http.StatusTooManyRequests},
			want: true,
		},
		{
			name: "Success: HTTP 500 Internal Server Error",
			err:  &azcore.ResponseError{StatusCode: http.StatusInternalServerError},
			want: true,
		},
		{
			name: "Success: HTTP 502 Bad Gateway",
			err:  &azcore.ResponseError{StatusCode: http.StatusBadGateway},
			want: true,
		},
		{
			name: "Success: HTTP 503 Service Unavailable",
			err:  &azcore.ResponseError{StatusCode: http.StatusServiceUnavailable},
			want: true,
		},
		{
			name: "Success: HTTP 504 Gateway Timeout",
			err:  &azcore.ResponseError{StatusCode: http.StatusGatewayTimeout},
			want: true,
		},
		{
			name: "Success: HTTP 404 Not Found (not retriable)",
			err:  &azcore.ResponseError{StatusCode: http.StatusNotFound},
			want: false,
		},
		{
			name: "Success: blob not found (not retriable)",
			err:  &azcore.ResponseError{ErrorCode: string(bloberror.BlobNotFound)},
			want: false,
		},
		{
			name: "Success: generic error (not retriable)",
			err:  errors.New("some error"),
			want: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := isRetriableError(test.err)
			if got != test.want {
				t.Errorf("TestIsRetriableError(%s): got %v, want %v", test.name, got, test.want)
			}
		})
	}
}
