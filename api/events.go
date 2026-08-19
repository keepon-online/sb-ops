package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	controlevents "github.com/keepon-online/sb-ops/internal/events"
)

func (h *handler) streamEvents(c *gin.Context) {
	after, _ := strconv.ParseInt(c.GetHeader("Last-Event-ID"), 10, 64)
	allowAll := func(string) bool { return true }
	backlog, reset, err := h.events.Since(c.Request.Context(), after, allowAll)
	if err != nil {
		c.Status(http.StatusInternalServerError)
		return
	}
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("X-Accel-Buffering", "no")
	c.Status(http.StatusOK)
	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		return
	}
	if reset {
		writeSSE(c, controlevents.Event{Type: "reset", Payload: json.RawMessage(`{"refetch":true}`)})
		flusher.Flush()
		return
	}
	for _, event := range backlog {
		writeSSE(c, event)
	}
	flusher.Flush()
	channel, cancel := h.events.Subscribe(allowAll)
	defer cancel()
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-c.Request.Context().Done():
			return
		case event, open := <-channel:
			if !open {
				writeSSE(c, controlevents.Event{Type: "reset", Payload: json.RawMessage(`{"refetch":true}`)})
				flusher.Flush()
				return
			}
			writeSSE(c, event)
			flusher.Flush()
		case <-heartbeat.C:
			token, _ := c.Get(tokenKey)
			if _, err := h.identity.Authenticate(c.Request.Context(), token.(string)); err != nil {
				return
			}
			_, _ = fmt.Fprint(c.Writer, ": heartbeat\n\n")
			flusher.Flush()
		}
	}
}

func writeSSE(c *gin.Context, event controlevents.Event) {
	if event.ID > 0 {
		_, _ = fmt.Fprintf(c.Writer, "id: %d\n", event.ID)
	}
	_, _ = fmt.Fprintf(c.Writer, "event: %s\n", event.Type)
	payload, _ := json.Marshal(event)
	_, _ = fmt.Fprintf(c.Writer, "data: %s\n\n", payload)
}
