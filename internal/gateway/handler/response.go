package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// APIResponse is the standard JSON envelope matching the Python backend format.
// Python: {"result": True, "code": "200", "data": ..., "message": "success"}
type APIResponse struct {
	Result  bool        `json:"result"`
	Code    string      `json:"code"`
	Data    interface{} `json:"data"`
	Message string      `json:"message"`
}

// Success writes a success response. data defaults to empty slice [] if nil.
func Success(c *gin.Context, msg string, data interface{}) {
	if data == nil {
		data = []struct{}{}
	}
	c.JSON(http.StatusOK, APIResponse{
		Result:  true,
		Code:    "200",
		Data:    data,
		Message: msg,
	})
}

// SuccessData is a shortcut for Success with default msg="success".
func SuccessData(c *gin.Context, data interface{}) {
	Success(c, "success", data)
}

// Failure writes a failure response. PRESERVES the legacy 200-OK-with-
// JSON-envelope shape so every regular /api/* consumer (axios interceptor,
// JS fetch wrappers, etc.) keeps working: those callers branch on
// `data.result === false`, NOT on HTTP status. New code that needs the
// HTTP layer (notably the `<audio>` element consuming /api/stream/) MUST
// use FailureStatus instead — see comment there.
func Failure(c *gin.Context, msg string) {
	// Explicit UTF-8 charset: some browsers/devtools decode bare
	// application/json as Latin-1, turning Chinese message into mojibake.
	c.Header("Content-Type", "application/json; charset=utf-8")
	c.JSON(http.StatusOK, APIResponse{
		Result:  false,
		Code:    "400",
		Data:    []struct{}{},
		Message: msg,
	})
}

// FailureStatus writes the same envelope as Failure() but with a caller-
// chosen HTTP status code. Use this from endpoints whose response is
// consumed by something that needs the HTTP-layer signal — most
// importantly the `<audio>` element on /api/stream/.
//
// Background: the `<audio>` HTMLMediaElement silently drops a 200-OK
// response with a JSON body when that body doesn't parse as audio. Most
// browsers (Chrome, Firefox, Safari) therefore never fire an `error`
// event on the audio element, which means useNoticeStore.push() is
// never called and the user sees "nothing happens". Switching to 4xx/5xx
// makes every browser fire `error`, which the audio onerror handler in
// PlayerBar already listens for (driving the toast). The envelope shape
// is unchanged so any post-processing on `data.result === false` keeps
// working.
//
// `code` (string) is INTENTIONALLY kept at the legacy "400" rather
// than matching the HTTP status. The frontend's axios-envelope
// convention has historically relied on `data.code === "400"` to
// detect any failure — making it match the HTTP status would silently
// miss stream failures that return 502/503/etc. The HTTP status layer
// is the new machine-readable signal for browser consumers; the JSON
// `code` field is the legacy signal for axios-style consumers.
func FailureStatus(c *gin.Context, status int, msg string) {
	// Explicit UTF-8 charset (see Failure).
	c.Header("Content-Type", "application/json; charset=utf-8")
	c.JSON(status, APIResponse{
		Result:  false,
		Code:    "400",
		Data:    []struct{}{},
		Message: msg,
	})
}
