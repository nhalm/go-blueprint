package api

// ListResponse wraps collection responses with pagination metadata.
// @Description Collection response with pagination
type ListResponse struct {
	Data       any    `json:"data"`
	HasMore    bool   `json:"has_more"`
	NextCursor string `json:"next_cursor,omitempty"`
	PrevCursor string `json:"prev_cursor,omitempty"`
}

// ErrorResponse represents all API error responses.
// @Description Standard error response
type ErrorResponse struct {
	Error ErrorDetail `json:"error"`
}

// ErrorDetail contains the specifics of an API error.
// @Description Error details
type ErrorDetail struct {
	Type    string `json:"type"`
	Code    string `json:"code"`
	Message string `json:"message"`
	Param   string `json:"param,omitempty"`
}

func NewListResponse(data any, hasMore bool, nextCursor, prevCursor string) *ListResponse {
	return &ListResponse{
		Data:       data,
		HasMore:    hasMore,
		NextCursor: nextCursor,
		PrevCursor: prevCursor,
	}
}

func NewErrorResponse(httpStatusCode int, err error, message, param string) *ErrorResponse {
	errorType := "api_error"
	if httpStatusCode >= 400 && httpStatusCode < 500 {
		errorType = "invalid_request_error"
	}

	errorCode := "unknown_error"
	if err != nil {
		errorCode = err.Error()
	}

	return &ErrorResponse{
		Error: ErrorDetail{
			Type:    errorType,
			Code:    errorCode,
			Message: message,
			Param:   param,
		},
	}
}
