package cosmosdb

import (
	"errors"
	"net"
	"net/http"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
)

// IsNotFound checks if the error that Azure returned is 404.
func IsNotFound(err error) bool {
	var resErr *azcore.ResponseError
	return errors.As(err, &resErr) && resErr.StatusCode == http.StatusNotFound
}

// IsConflict checks if the error indicates there is a resource conflict.
func IsConflict(err error) bool {
	var resErr *azcore.ResponseError
	return errors.As(err, &resErr) && resErr.StatusCode == http.StatusConflict
}

// IsUnauthorized checks if the error that Azure returned is 401.
func IsUnauthorized(err error) bool {
	var resErr *azcore.ResponseError
	return errors.As(err, &resErr) && resErr.StatusCode == http.StatusUnauthorized
}

// isDNSError checks if the error is related to failure to connect / resolve DNS.
// Based on github.io/Azure/azure-sdk-for-go/sdk/data/azcosmos/cosmos_client_retry_policy.go.
func isDNSError(err error) bool {
	var dnserror *net.DNSError
	return errors.As(err, &dnserror)
}

func isRetriableError(err error) bool {
	if IsUnauthorized(err) {
		return false
	}

	if isDNSError(err) {
		return true
	}

	var connectivityErrors = []string{
		"context deadline exceeded",
		"connection refused",
		"connection reset by peer",
		"connection timed out",
		"TLS handshake timeout",
		"i/o timeout",
		"no such host",
		"EOF",
		"context canceled",
	}

	for _, e := range connectivityErrors {
		if strings.Contains(err.Error(), e) {
			return true
		}
	}

	return false
}
