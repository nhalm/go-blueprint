package apperrors

import "fmt"

type NotFoundError struct {
	Resource string
	ID       string
}

func (e *NotFoundError) Error() string {
	if e.ID != "" {
		return fmt.Sprintf("%s not found: %s", e.Resource, e.ID)
	}
	return fmt.Sprintf("%s not found", e.Resource)
}

func NewNotFoundError(resource, id string) *NotFoundError {
	return &NotFoundError{Resource: resource, ID: id}
}

type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string {
	if e.Field != "" {
		return fmt.Sprintf("%s: %s", e.Field, e.Message)
	}
	return e.Message
}

func NewValidationError(field, message string) *ValidationError {
	return &ValidationError{Field: field, Message: message}
}

type ConflictError struct {
	Resource string
	Reason   string
}

func (e *ConflictError) Error() string {
	if e.Reason != "" {
		return fmt.Sprintf("%s conflict: %s", e.Resource, e.Reason)
	}
	return fmt.Sprintf("%s conflict", e.Resource)
}

func NewConflictError(resource, reason string) *ConflictError {
	return &ConflictError{Resource: resource, Reason: reason}
}

type OptimisticLockError struct {
	Resource string
	ID       string
}

func (e *OptimisticLockError) Error() string {
	return fmt.Sprintf("%s has been modified: %s", e.Resource, e.ID)
}

func NewOptimisticLockError(resource, id string) *OptimisticLockError {
	return &OptimisticLockError{Resource: resource, ID: id}
}

type ServiceUnavailableError struct {
	Message string
}

func (e *ServiceUnavailableError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("service unavailable: %s", e.Message)
	}
	return "service unavailable"
}

func NewServiceUnavailableError(message string) *ServiceUnavailableError {
	return &ServiceUnavailableError{Message: message}
}

type TimeoutError struct {
	Operation string
}

func (e *TimeoutError) Error() string {
	if e.Operation != "" {
		return fmt.Sprintf("operation timed out: %s", e.Operation)
	}
	return "operation timed out"
}

func NewTimeoutError(operation string) *TimeoutError {
	return &TimeoutError{Operation: operation}
}
