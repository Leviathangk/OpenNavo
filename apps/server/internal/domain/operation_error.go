package domain

// OperationError adds a code-owned stage without exposing SQL, URLs or payloads.
// Operation must be a static identifier, never user input.
type OperationError struct {
	Operation string
	Cause     error
}

func (e *OperationError) Error() string { return e.Operation + " failed" }
func (e *OperationError) Unwrap() error { return e.Cause }
