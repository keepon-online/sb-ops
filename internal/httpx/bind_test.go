package httpx

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

type bindTarget struct {
	Name    string `json:"name" binding:"required"`
	Enabled *bool  `json:"enabled" binding:"required"`
}

func newBindContext(t *testing.T, body string) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	context.Request.Header.Set("Content-Type", "application/json")
	return context, recorder
}

func TestBindJSONReportsChineseFieldErrors(t *testing.T) {
	context, recorder := newBindContext(t, `{}`)
	if BindJSON(context, &bindTarget{}, "用户信息无效") {
		t.Fatal("BindJSON = true, want false")
	}
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadRequest)
	}
	var response Response
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response %q: %v", recorder.Body.String(), err)
	}
	if response.Code != CodeValidationFailed || response.Message != "用户信息无效" {
		t.Fatalf("code/message = %d/%s", response.Code, response.Message)
	}
	want := map[string]string{"name": "不能为空", "enabled": "不能为空"}
	if len(response.FieldErrors) != len(want) {
		t.Fatalf("fieldErrors = %#v, want %#v", response.FieldErrors, want)
	}
	for field, message := range want {
		if response.FieldErrors[field] != message {
			t.Fatalf("fieldErrors[%s] = %q, want %q", field, response.FieldErrors[field], message)
		}
	}
}

func TestBindJSONKeepsMessageForMalformedBody(t *testing.T) {
	for _, body := range []string{"", `{`} {
		context, recorder := newBindContext(t, body)
		if BindJSON(context, &bindTarget{}, "用户信息无效") {
			t.Fatalf("BindJSON(%q) = true, want false", body)
		}
		var response Response
		if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
			t.Fatalf("decode response %q: %v", recorder.Body.String(), err)
		}
		if response.Code != CodeValidationFailed || response.Message != "用户信息无效" || response.FieldErrors != nil {
			t.Fatalf("response = %#v, want message-only failure", response)
		}
	}
}

func TestBindJSONPassesValidBody(t *testing.T) {
	context, recorder := newBindContext(t, `{"name":"alice","enabled":true}`)
	var target bindTarget
	if !BindJSON(context, &target, "用户信息无效") {
		t.Fatalf("response = %s", recorder.Body.String())
	}
	if target.Name != "alice" || target.Enabled == nil || !*target.Enabled {
		t.Fatalf("target = %#v", target)
	}
	if recorder.Body.Len() != 0 || recorder.Code != http.StatusOK { // untouched recorder stays 200/empty
		t.Fatalf("response written on success: code=%d body=%q", recorder.Code, recorder.Body.String())
	}
}
