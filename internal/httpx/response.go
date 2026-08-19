package httpx

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// Response is the management-API envelope: every JSON body carries a numeric
// business code, message, and data. 0 means success. requestId rides along
// for tracing and fieldErrors appears only on validation failures. HTTP
// status codes keep their REST meaning; endpoints with non-JSON contracts
// (subscription artifacts, SSE, health probes, 204 responses) stay outside
// the envelope.
type Response struct {
	Code        Code              `json:"code"`
	Message     string            `json:"message"`
	Data        any               `json:"data"`
	FieldErrors map[string]string `json:"fieldErrors,omitempty"`
	RequestID   string            `json:"requestId"`
}

// JSON writes a raw value without the envelope. It is reserved for
// non-management contracts (health probes); handlers use OK/Created/Error.
func JSON(c *gin.Context, status int, value any) {
	c.JSON(status, value)
}

// OK writes a 200 envelope around data.
func OK(c *gin.Context, data any) {
	c.JSON(http.StatusOK, Response{Code: CodeOK, Message: "success", Data: data, RequestID: RequestID(c)})
}

// Created writes a 201 envelope around data.
func Created(c *gin.Context, data any) {
	c.JSON(http.StatusCreated, Response{Code: CodeOK, Message: "success", Data: data, RequestID: RequestID(c)})
}

// Error writes an envelope with a non-zero business code, a null data slot,
// and optional field-level validation messages.
func Error(c *gin.Context, status int, code Code, message string, fieldErrors map[string]string) {
	c.AbortWithStatusJSON(status, Response{
		Code: code, Message: message, Data: nil, FieldErrors: fieldErrors, RequestID: RequestID(c),
	})
}
