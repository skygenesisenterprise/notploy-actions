package notploy

import (
	"fmt"
	"net/http"
	"strings"
)

// errorResponse is the public error body Notploy returns for a failed request.
type errorResponse struct {
	Message string `json:"message"`
	Code    string `json:"code"`
	Issues  []struct {
		Message string `json:"message"`
	} `json:"issues"`
}

// APIError is returned when Notploy answers with a non-2xx status code.
//
// It only ever carries data that is safe to print: the operation, the status
// code and the public message from the JSON body. Request headers — including
// the API key — are never part of it.
type APIError struct {
	Operation  string
	StatusCode int
	Code       string
	Message    string
}

func (e *APIError) Error() string {
	message := strings.TrimSpace(e.Message)
	if message == "" {
		message = strings.TrimSpace(http.StatusText(e.StatusCode))
	}
	if message == "" {
		message = "no error message returned"
	}
	if e.Code != "" {
		message = fmt.Sprintf("%s [%s]", message, e.Code)
	}
	return fmt.Sprintf("%s failed with HTTP %d: %s", e.Operation, e.StatusCode, message)
}

// IsUnauthorized reports whether the credential was rejected.
func (e *APIError) IsUnauthorized() bool {
	return e.StatusCode == http.StatusUnauthorized || e.StatusCode == http.StatusForbidden
}

// IsNotFound reports whether the target does not exist.
func (e *APIError) IsNotFound() bool {
	return e.StatusCode == http.StatusNotFound
}

// IsRateLimited reports whether the instance asked the caller to slow down.
func (e *APIError) IsRateLimited() bool {
	return e.StatusCode == http.StatusTooManyRequests
}

// IsRetryable reports whether repeating the request may succeed.
func (e *APIError) IsRetryable() bool {
	return e.IsRateLimited() || e.StatusCode >= http.StatusInternalServerError
}

// Hint returns an actionable explanation for the most common failure modes, or
// "" when there is nothing specific to add.
func (e *APIError) Hint() string {
	switch {
	case e.StatusCode == http.StatusBadRequest:
		return "Notploy rejected the request; check that `application-id` is a valid application id."
	case e.StatusCode == http.StatusUnauthorized:
		return "Notploy rejected the API key; check the `api-key` input (NOTPLOY_API_KEY)."
	case e.StatusCode == http.StatusForbidden:
		return "The API key is valid but not allowed to deploy this application; check its permissions."
	case e.StatusCode == http.StatusNotFound:
		return "Not found; check the `endpoint` input and `application-id`, and that the instance exposes this endpoint."
	case e.StatusCode == http.StatusConflict:
		return "Notploy reported a conflict; the application may already be deploying."
	case e.StatusCode == http.StatusTooManyRequests:
		return "Notploy is rate limiting this API key; retry later or increase `poll-interval`."
	case e.StatusCode >= http.StatusInternalServerError:
		return "Notploy returned a server error; retry, and check the instance logs if it persists."
	default:
		return ""
	}
}
