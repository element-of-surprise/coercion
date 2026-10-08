package blobops

import (
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/bloberror"

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
			t.Parallel()

			got := IsNotFound(test.err)
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
			t.Parallel()

			got := IsConflict(test.err)
			if got != test.want {
				t.Errorf("TestIsConflict(%s): got %v, want %v", test.name, got, test.want)
			}
		})
	}
}
