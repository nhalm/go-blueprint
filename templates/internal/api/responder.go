package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/nhalm/canonlog"
)

func renderJSON(w http.ResponseWriter, statusCode int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(data)
}

func renderError(w http.ResponseWriter, r *http.Request, statusCode int, err error, message, param string) {
	canonlog.AddRequestError(r.Context(), err)
	sanitizedMessage := sanitizeErrorMessage(message, statusCode)
	renderJSON(w, statusCode, NewErrorResponse(statusCode, err, sanitizedMessage, param))
}

func sanitizeErrorMessage(message string, statusCode int) string {
	lowerMsg := strings.ToLower(message)

	if strings.Contains(lowerMsg, "sql") ||
		strings.Contains(lowerMsg, "database") ||
		strings.Contains(lowerMsg, "postgres") {
		if statusCode >= 500 {
			return "An internal error occurred"
		}
		return "Invalid request"
	}

	if statusCode >= 500 {
		return "An internal error occurred"
	}

	return message
}

func Success(w http.ResponseWriter, data any) {
	renderJSON(w, http.StatusOK, data)
}

func Created(w http.ResponseWriter, data any) {
	renderJSON(w, http.StatusCreated, data)
}

func List(w http.ResponseWriter, data any, hasMore bool, nextCursor, prevCursor string) {
	renderJSON(w, http.StatusOK, NewListResponse(data, hasMore, nextCursor, prevCursor))
}

func BadRequest(w http.ResponseWriter, r *http.Request, err error, message, param string) {
	renderError(w, r, http.StatusBadRequest, err, message, param)
}

func NotFound(w http.ResponseWriter, r *http.Request, err error, message string) {
	renderError(w, r, http.StatusNotFound, err, message, "")
}

func InternalError(w http.ResponseWriter, r *http.Request, err error, message string) {
	renderError(w, r, http.StatusInternalServerError, err, message, "")
}

func ConflictError(w http.ResponseWriter, r *http.Request, err error, message string) {
	renderError(w, r, http.StatusConflict, err, message, "")
}
