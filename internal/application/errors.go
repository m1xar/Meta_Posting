package application

import (
	"errors"
	"fmt"
)

var (
	ErrUnauthorized = errors.New("unauthorized")
	ErrConflict     = errors.New("resource conflict")
	// ErrForbidden is authenticated-but-not-allowed, distinct from
	// ErrUnauthorized which means no usable credentials were presented.
	ErrForbidden = errors.New("forbidden")
)

type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string {
	if e.Field == "" {
		return "invalid request: " + e.Message
	}
	return fmt.Sprintf("invalid request field %q: %s", e.Field, e.Message)
}

func invalid(field, message string) error {
	return &ValidationError{Field: field, Message: message}
}

// conflict reports a uniqueness collision without disclosing which field
// collided, so an anonymous caller cannot enumerate registered users.
func conflict(message string) error {
	return fmt.Errorf("%w: %s", ErrConflict, message)
}
