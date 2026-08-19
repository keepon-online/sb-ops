package events

import (
	"database/sql"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

func newShutdownTestBus(t *testing.T) *Bus {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(Schema); err != nil {
		t.Fatalf("apply schema: %v", err)
	}
	return NewBus(db)
}

func TestShutdownClosesLiveSubscriptions(t *testing.T) {
	bus := newShutdownTestBus(t)
	channel, _ := bus.Subscribe(func(string) bool { return true })
	bus.Shutdown()
	if _, open := <-channel; open {
		t.Fatal("subscription channel still open after shutdown")
	}
	// Shutdown must be idempotent: Close runs on every teardown path.
	bus.Shutdown()
}

func TestSubscribeAfterShutdownReturnsClosedChannel(t *testing.T) {
	bus := newShutdownTestBus(t)
	bus.Shutdown()
	channel, cancel := bus.Subscribe(func(string) bool { return true })
	defer cancel()
	if _, open := <-channel; open {
		t.Fatal("post-shutdown subscription must be closed immediately")
	}
}
