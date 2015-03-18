package eventsource

import (
	"net/http/httptest"
	"testing"
	"time"
)

func TestStreamClose(t *testing.T) {
	server := NewServer()
	defer server.Close()

	httpServer := httptest.NewServer(server.Handler("TestStreamClose"))
	defer httpServer.Close()

	stream, err := Subscribe(httpServer.URL, "")
	if err != nil {
		t.Fatalf("Failed to subscribe: %s", err)
	}

	stream.Close()

	select {
	case _, ok := <-stream.Events:
		if ok {
			t.Errorf("Expected stream.Events channel to be closed. Is still open.")
		}
	case <-time.After(time.Second):
		t.Errorf("Timed out waiting for stream.Events channel to close")
	}

	select {
	case _, ok := <-stream.Errors:
		if ok {
			t.Errorf("Expected stream.Errors channel to be closed. Is still open.")
		}
	case <-time.After(time.Second):
		t.Errorf("Timed out waiting for stream.Errors channel to close")
	}
}
