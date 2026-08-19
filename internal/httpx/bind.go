package httpx

import (
	"errors"
	"net/http"
	"reflect"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
	"github.com/go-playground/locales/zh"
	ut "github.com/go-playground/universal-translator"
	"github.com/go-playground/validator/v10"
)

// bindingTranslator renders binding-tag violations as Chinese messages. The
// field name is the fieldErrors key, so templates never repeat it.
var bindingTranslator ut.Translator

func init() {
	chinese := zh.New()
	bindingTranslator = ut.New(chinese, chinese).GetFallback()
	if bindingTranslator == nil {
		return
	}
	for tag, text := range map[string]string{
		"required": "不能为空",
		"email":    "邮箱格式无效",
		"url":      "URL 格式无效",
	} {
		_ = bindingTranslator.Add(tag, text, false)
	}
	if engine, ok := binding.Validator.Engine().(*validator.Validate); ok {
		// Violations key by JSON tag so clients receive wire-format names
		// instead of Go field names.
		engine.RegisterTagNameFunc(func(field reflect.StructField) string {
			name := strings.SplitN(field.Tag.Get("json"), ",", 2)[0]
			if name == "" || name == "-" {
				return field.Name
			}
			return name
		})
	}
}

// maxRequestBodyBytes caps every JSON body before decoding. Unauthenticated
// endpoints (login/setup) must not be able to drive unbounded allocation in
// the single process that also hosts the proxy kernel. 4 MiB comfortably
// covers the largest legitimate body (certificate PEM uploads).
const maxRequestBodyBytes = 4 << 20

// BindJSON decodes the JSON body into target. On failure it writes a
// VALIDATION_FAILED response — carrying per-field Chinese messages in
// fieldErrors when the failure comes from binding tags — and returns false,
// so handlers only need to return.
func BindJSON(c *gin.Context, target any, message string) bool {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxRequestBodyBytes)
	err := c.ShouldBindJSON(target)
	if err == nil {
		return true
	}
	var maxErr *http.MaxBytesError
	if errors.As(err, &maxErr) {
		Error(c, http.StatusRequestEntityTooLarge, CodeValidationFailed, "请求体过大", nil)
		return false
	}
	Error(c, http.StatusBadRequest, CodeValidationFailed, message, bindingFieldErrors(err))
	return false
}

func bindingFieldErrors(err error) map[string]string {
	var violations validator.ValidationErrors
	if !errors.As(err, &violations) || bindingTranslator == nil {
		// Malformed or empty bodies have no field-level information.
		return nil
	}
	messages := make(map[string]string, len(violations))
	for _, violation := range violations {
		key := violation.Field()
		if _, exists := messages[key]; exists {
			continue
		}
		messages[key] = bindingMessage(violation)
	}
	return messages
}

func bindingMessage(violation validator.FieldError) string {
	switch violation.Tag() {
	case "oneof":
		return "取值必须是 " + strings.Join(strings.Split(violation.Param(), " "), "/") + " 之一"
	case "min":
		return "长度或数值不能小于 " + violation.Param()
	case "max":
		return "长度或数值不能超过 " + violation.Param()
	}
	if text, err := bindingTranslator.T(violation.Tag()); err == nil && text != "" {
		return text
	}
	return "格式无效"
}
