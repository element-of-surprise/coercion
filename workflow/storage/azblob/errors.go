package azblob

import (
	"errors"
	"net/http"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/bloberror"
)

// isNotFound returns true if the error is a not found error.
func isNotFound(err error) bool {
	if err == nil {
		return false
	}
	return bloberror.HasCode(err, bloberror.BlobNotFound, bloberror.ContainerNotFound)
}

// isConflict returns true if the error is a conflict error (already exists).
func isConflict(err error) bool {
	if err == nil {
		return false
	}
	return bloberror.HasCode(err, bloberror.ContainerAlreadyExists, bloberror.BlobAlreadyExists)
}

// isRetriableError returns true if the error is a retriable error.
// Retriable errors are typically transient network or service errors.
func isRetriableError(err error) bool {
	if err == nil {
		return false
	}

	// Check for specific blob error codes
	if bloberror.HasCode(err,
		bloberror.ServerBusy,
		bloberror.InternalError,
		bloberror.OperationTimedOut) {
		return true
	}

	// Check for HTTP status codes that indicate transient errors
	var respErr *azcore.ResponseError
	if errors.As(err, &respErr) {
		switch respErr.StatusCode {
		case http.StatusRequestTimeout,      // 408
			http.StatusTooManyRequests,      // 429
			http.StatusInternalServerError,  // 500
			http.StatusBadGateway,           // 502
			http.StatusServiceUnavailable,   // 503
			http.StatusGatewayTimeout:       // 504
			return true
		}
	}

	return false
}
