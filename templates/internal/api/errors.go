package api

import (
	"errors"
	"net/http"

	"github.com/yourorg/myapp/internal/apperrors"
)

func handleServiceError(w http.ResponseWriter, r *http.Request, err error) {
	var notFoundErr *apperrors.NotFoundError
	if errors.As(err, &notFoundErr) {
		NotFound(w, r, err, err.Error())
		return
	}

	var validationErr *apperrors.ValidationError
	if errors.As(err, &validationErr) {
		BadRequest(w, r, err, err.Error(), validationErr.Field)
		return
	}

	var conflictErr *apperrors.ConflictError
	if errors.As(err, &conflictErr) {
		ConflictError(w, r, err, err.Error())
		return
	}

	var optimisticLockErr *apperrors.OptimisticLockError
	if errors.As(err, &optimisticLockErr) {
		ConflictError(w, r, err, "resource has been modified, please refresh and try again")
		return
	}

	InternalError(w, r, err, "internal server error")
}
