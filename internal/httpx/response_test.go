package httpx

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestRequestIDPreservesValidHeaderAndGeneratesInvalidHeader(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name       string
		upstream   string
		wantSameID bool
	}{
		{name: "valid", upstream: "request-12345678", wantSameID: true},
		{name: "invalid", upstream: "bad request id", wantSameID: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			router := gin.New()
			router.Use(RequestIDMiddleware())
			router.GET("/", func(c *gin.Context) { JSON(c, http.StatusOK, map[string]string{"requestId": RequestID(c)}) })
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.Header.Set("X-Request-ID", test.upstream)
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)

			got := rec.Header().Get("X-Request-ID")
			if got == "" || !validRequestID(got) {
				t.Fatalf("request id=%q", got)
			}
			if test.wantSameID && got != test.upstream {
				t.Fatalf("request id=%q want=%q", got, test.upstream)
			}
			if !test.wantSameID && got == test.upstream {
				t.Fatalf("invalid upstream request id was preserved")
			}
		})
	}
}

func TestErrorWritesStableResponseWithRequestID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(RequestIDMiddleware())
	router.POST("/", func(c *gin.Context) {
		Error(c, http.StatusBadRequest, CodeValidationFailed, "invalid request", map[string]string{"username": "required"})
	})
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("X-Request-ID", "request-12345678")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d", rec.Code)
	}
	var response Response
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Code != CodeValidationFailed || response.RequestID != "request-12345678" {
		t.Fatalf("response=%+v", response)
	}
	if response.FieldErrors["username"] != "required" {
		t.Fatalf("field errors=%v", response.FieldErrors)
	}
}
