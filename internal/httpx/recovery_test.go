package httpx

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestRecoveryMiddlewareConvertsPanicToErrorEnvelope(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(RequestIDMiddleware(), RecoveryMiddleware())
	router.GET("/boom", func(c *gin.Context) { panic("kaboom") })
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/boom", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d", rec.Code)
	}
	var response Response
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Code != CodeInternalError || response.Message != "服务器内部错误" {
		t.Fatalf("response=%+v", response)
	}
	if !validRequestID(response.RequestID) {
		t.Fatalf("request id=%q", response.RequestID)
	}
}
