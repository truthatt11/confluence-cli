package confluence

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Error codes are stable so scripts and AI agents can branch on them.
const (
	CodeBadRequest      = "bad_request"
	CodeAuthFailed      = "auth_failed"
	CodeForbidden       = "forbidden"
	CodeNotFound        = "not_found"
	CodeVersionConflict = "version_conflict"
	CodeRateLimited     = "rate_limited"
	CodeServerError     = "server_error"
	CodeHTTPError       = "http_error"
)

// Error is a failed API call.
type Error struct {
	Code    string `json:"code"`
	Status  int    `json:"status"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return e.Message }

var hints = map[int]struct{ code, text string }{
	400: {CodeBadRequest, "the request was rejected"},
	401: {CodeAuthFailed, "authentication failed: the token is invalid or expired; run `cfl init` to update it"},
	403: {CodeForbidden, "your account does not have permission for this"},
	404: {CodeNotFound, "not found, or your account lacks permission to view it (Confluence answers 404 for both)"},
	409: {CodeVersionConflict, "the page changed after it was read; read it again and reapply your edit"},
	429: {CodeRateLimited, "rate limited by the server; try again later"},
}

// errorFrom builds an Error from a non-2xx response, keeping the server's own
// message (it explains things like invalid storage format).
func errorFrom(resp *http.Response) *Error {
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	return newError(resp.StatusCode, raw)
}

func newError(status int, body []byte) *Error {
	h, ok := hints[status]
	switch {
	case ok:
	case status >= 500:
		h = struct{ code, text string }{CodeServerError, "Confluence server error"}
	default:
		h = struct{ code, text string }{CodeHTTPError, "request failed"}
	}
	msg := fmt.Sprintf("%s (HTTP %d)", h.text, status)
	if detail := serverMessage(body); detail != "" {
		msg += ": " + detail
	}
	return &Error{Code: h.code, Status: status, Message: msg}
}

func serverMessage(raw []byte) string {
	var parsed struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(raw, &parsed) == nil && parsed.Message != "" {
		return truncate(parsed.Message, 300)
	}
	return ""
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
