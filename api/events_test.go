package api

import (
	"context"
	"database/sql"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	controlevents "github.com/keepon-online/sb-ops/internal/events"

	_ "github.com/mattn/go-sqlite3"
)

// Regression: http.Server.Shutdown does not cancel request contexts of active
// connections, so an open /events SSE stream must be ended by closing the
// event bus — otherwise the unbounded drain waits on the stream forever.
func TestStreamEventsEndsOnBusShutdown(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(controlevents.Schema); err != nil {
		t.Fatalf("apply schema: %v", err)
	}
	bus := controlevents.NewBus(db)
	router := gin.New()
	h := &handler{Events: bus}
	router.GET("/events", h.streamEvents)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	server := &http.Server{Handler: router}
	go func() { _ = server.Serve(listener) }()

	resp, err := http.Get("http://" + listener.Addr().String() + "/events")
	if err != nil {
		t.Fatalf("open SSE stream: %v", err)
	}
	defer resp.Body.Close()
	bodyDone := make(chan struct{})
	go func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		close(bodyDone)
	}()
	// Give the handler a moment to reach Subscribe after flushing headers.
	time.Sleep(100 * time.Millisecond)

	bus.Shutdown()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		t.Fatalf("http shutdown blocked or failed: %v", err)
	}
	select {
	case <-bodyDone:
	case <-time.After(3 * time.Second):
		t.Fatal("SSE stream did not end after bus shutdown")
	}
}
