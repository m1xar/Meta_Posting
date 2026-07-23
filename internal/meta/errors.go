package meta

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"
)

// GraphError is Meta's structured Graph API error response.
type GraphError struct {
	Message        string          `json:"message"`
	Type           string          `json:"type,omitempty"`
	Code           int             `json:"code,omitempty"`
	ErrorSubcode   int             `json:"error_subcode,omitempty"`
	IsTransient    bool            `json:"is_transient,omitempty"`
	ErrorUserTitle string          `json:"error_user_title,omitempty"`
	ErrorUserMsg   string          `json:"error_user_msg,omitempty"`
	FBTraceID      string          `json:"fbtrace_id,omitempty"`
	ErrorData      json.RawMessage `json:"error_data,omitempty"`

	HTTPStatus int           `json:"-"`
	RetryAfter time.Duration `json:"-"`
	RequestID  string        `json:"-"`
}

func (e *GraphError) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.ErrorSubcode != 0 {
		return fmt.Sprintf("meta Graph API error %d/%d (%s): %s", e.Code, e.ErrorSubcode, e.Type, e.Message)
	}
	return fmt.Sprintf("meta Graph API error %d (%s): %s", e.Code, e.Type, e.Message)
}

// Retryable recognizes Meta's documented transient/rate-limit families. The
// Retry-After header, when present, controls the actual delay.
func (e *GraphError) Retryable() bool {
	if e == nil {
		return false
	}
	if e.IsTransient || e.HTTPStatus == http.StatusTooManyRequests || e.HTTPStatus >= 500 {
		return true
	}
	switch e.Code {
	case 1, 2, 4, 17, 32, 341, 613:
		return true
	default:
		return false
	}
}

// HTTPError represents a non-Graph error response, such as an upstream proxy
// failure that did not return Meta's JSON error envelope.
type HTTPError struct {
	StatusCode int
	Status     string
	Body       string
	RetryAfter time.Duration
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("meta HTTP error %s: %s", e.Status, e.Body)
}

func (e *HTTPError) Retryable() bool {
	return e != nil && (e.StatusCode == http.StatusTooManyRequests || e.StatusCode >= 500)
}

// TransportError is a sanitized request/response transport failure. The
// original error can contain the full Graph URL (and therefore credentials),
// so only the redacted message crosses the client boundary.
type TransportError struct {
	Message  string
	CanRetry bool
}

func (e *TransportError) Error() string {
	if e == nil {
		return "<nil>"
	}
	return e.Message
}

func (e *TransportError) Retryable() bool {
	return e != nil && e.CanRetry
}

// ResponseError represents an ambiguous response received after Meta accepted
// the HTTP request: the response body could not be read or a successful
// response could not be decoded. It is durable-retryable because a mutation
// may already have succeeded upstream. Non-idempotent requests are never
// replayed inline; their next job attempt reconciles the remote object first.
type ResponseError struct {
	Message string
}

func (e *ResponseError) Error() string {
	if e == nil {
		return "<nil>"
	}
	return e.Message
}

func (e *ResponseError) Retryable() bool {
	return e != nil
}

// IsRetryableError classifies errors that are safe to retry as a durable job.
// Context cancellation is deliberately retryable here even though the HTTP
// client must not retry it inline: the next job attempt first reconciles
// uniquely named remote objects before issuing another non-idempotent create.
func IsRetryableError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	if classified, ok := err.(interface{ Retryable() bool }); ok && classified.Retryable() {
		return true
	}
	switch unwrapped := err.(type) {
	case interface{ Unwrap() []error }:
		for _, child := range unwrapped.Unwrap() {
			if IsRetryableError(child) {
				return true
			}
		}
	case interface{ Unwrap() error }:
		return IsRetryableError(unwrapped.Unwrap())
	}
	return false
}

func decodeGraphError(body []byte, status int, header http.Header) *GraphError {
	var envelope struct {
		Error *GraphError `json:"error"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil || envelope.Error == nil {
		return nil
	}
	envelope.Error.HTTPStatus = status
	envelope.Error.RetryAfter = parseRetryAfter(header.Get("Retry-After"), time.Now())
	envelope.Error.RequestID = header.Get("X-FB-Request-ID")
	return envelope.Error
}
