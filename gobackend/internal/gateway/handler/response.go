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

// Failure writes a failure response.
func Failure(c *gin.Context, msg string) {
	c.JSON(http.StatusOK, APIResponse{
		Result:  false,
		Code:    "400",
		Data:    []struct{}{},
		Message: msg,
	})
}
