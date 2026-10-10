package cosmosdb

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"syscall"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/go-json-experiment/json"
	"github.com/go-json-experiment/json/jsontext"
	"github.com/gostdlib/base/context"

	"github.com/element-of-surprise/coercion/workflow/errors"
)

// isNotFound checks if the error that Azure returned is 404.
func isNotFound(err error) bool {
	if err == nil {
		return false
	}
	var resErr *azcore.ResponseError
	return errors.As(err, &resErr) && resErr.StatusCode == http.StatusNotFound
}

// IsConflict checks if the error indicates there is a resource conflict.
func isConflict(err error) bool {
	if err == nil {
		return false
	}
	var resErr *azcore.ResponseError
	return errors.As(err, &resErr) && resErr.StatusCode == http.StatusConflict
}

// IsUnauthorized checks if the error that Azure returned is 401.
func isUnauthorized(err error) bool {
	var resErr *azcore.ResponseError
	return errors.As(err, &resErr) && resErr.StatusCode == http.StatusUnauthorized
}

// isDNSError checks if the error is related to failure to connect / resolve DNS.
// Based on github.io/Azure/azure-sdk-for-go/sdk/data/azcosmos/cosmos_client_retry_policy.go.
func isDNSError(err error) bool {
	var dnserror *net.DNSError
	return errors.As(err, &dnserror)
}

// isDecodeError reports whether err came from decoding a JSON document. A stored document that does not decode will
// not decode on a retry either, even when the cause is a truncation reported as io.ErrUnexpectedEOF.
func isDecodeError(err error) bool {
	var synErr *jsontext.SyntacticError
	var semErr *json.SemanticError
	return errors.As(err, &synErr) || errors.As(err, &semErr)
}

// isNetTimeout reports whether err is a net.Error that timed out, such as an i/o or TLS handshake timeout.
func isNetTimeout(err error) bool {
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}

// isRetriableError reports whether err is a transient connectivity failure worth retrying. Anything not known to be
// transient is not retried.
func isRetriableError(err error) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, errors.ErrPermanent), isUnauthorized(err), isDecodeError(err):
		return false
	// The caller's ctx ended, so a retry cannot run. A per-try deadline below the caller's ctx is retriable.
	case errors.Is(err, context.Canceled):
		return false
	case errors.Is(err, context.DeadlineExceeded):
		return true
	case isDNSError(err), isNetTimeout(err):
		return true
	case errors.Is(err, syscall.ECONNREFUSED), errors.Is(err, syscall.ECONNRESET), errors.Is(err, syscall.ETIMEDOUT):
		return true
	// Decode errors were excluded above, so an EOF here is the transport losing the connection mid-response.
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		return true
	}
	return false
}

// unmarshalDoc decodes a stored document into v. A decode failure is marked permanent: retrying a read returns the
// same bytes, so it can never succeed.
func unmarshalDoc(data []byte, v any) error {
	if err := json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("couldn't decode document: %w: %w", err, errors.ErrPermanent)
	}
	return nil
}
