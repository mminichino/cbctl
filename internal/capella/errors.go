package capella

import "fmt"

// CapellaAPIError is returned for Capella Management API HTTP failures.
type CapellaAPIError struct {
	Code    int
	Body    string
	Message string
	Cause   error
}

func (e *CapellaAPIError) Error() string {
	if e == nil {
		return "capella api error"
	}
	if e.Body != "" {
		return fmt.Sprintf("%s (HTTP %d): %s", e.Message, e.Code, e.Body)
	}
	if e.Code != 0 {
		return fmt.Sprintf("%s (HTTP %d)", e.Message, e.Code)
	}
	return e.Message
}

func (e *CapellaAPIError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// CapellaNotFoundError indicates a Capella resource was not found.
type CapellaNotFoundError struct {
	Message string
}

func (e *CapellaNotFoundError) Error() string {
	if e == nil || e.Message == "" {
		return "capella resource not found"
	}
	return e.Message
}

// UserNotConfiguredError indicates Capella user email/id was not set.
type UserNotConfiguredError struct {
	Message string
}

func (e *UserNotConfiguredError) Error() string {
	if e == nil || e.Message == "" {
		return "Capella user not configured"
	}
	return e.Message
}

func apiError(code int, body, message string, cause error) error {
	return &CapellaAPIError{Code: code, Body: body, Message: message, Cause: cause}
}

func notFound(message string) error {
	return &CapellaNotFoundError{Message: message}
}
