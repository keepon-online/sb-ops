package web

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/keepon-online/sb-ops/internal/observability"
	_ "github.com/mattn/go-sqlite3"
)

func TestHealthRoutesReportLivenessAndReadiness(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`CREATE TABLE schema_versions(version INTEGER PRIMARY KEY)`); err != nil {
		t.Fatalf("create schema versions: %v", err)
	}
	router := gin.New()
	health := observability.NewHealthState(nil)
	health.MarkBackgroundRunning(true)
	health.RecordAggregation(nil)
	health.RecordTrafficCollection(nil)
	health.RecordAlertEvaluation(nil)
	RegisterHealthRoutes(router, db, health, 0)

	for path, body := range map[string]string{"/health/live": `"status":"ok"`, "/health/ready": `"status":"ready"`} {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), body) {
			t.Fatalf("%s code=%d body=%s", path, recorder.Code, recorder.Body.String())
		}
	}
}
