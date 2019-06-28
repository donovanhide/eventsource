package eventsource

import (
	"io"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"
)

const (
	eventChannelName   = "Test"
	timeToWaitForEvent = 100 * time.Millisecond
)

func TestStreamSubscribeEventsChan(t *testing.T) {
	server := NewServer()
	httpServer := httptest.NewServer(server.Handler(eventChannelName))
	// The server has to be closed before the httpServer is closed.
	// Otherwise the httpServer has still an open connection and it can not close.
	defer httpServer.Close()
	defer server.Close()

	stream := mustSubscribe(t, httpServer.URL, "")

	publishedEvent := &publication{id: "123"}
	server.Publish([]string{eventChannelName}, publishedEvent)

	select {
	case receivedEvent := <-stream.Events:
		if !reflect.DeepEqual(receivedEvent, publishedEvent) {
			t.Errorf("got event %+v, want %+v", receivedEvent, publishedEvent)
		}
	case <-time.After(timeToWaitForEvent):
		t.Error("Timed out waiting for event")
	}
}

func TestStreamSubscribeErrorsChan(t *testing.T) {
	server := NewServer()
	httpServer := httptest.NewServer(server.Handler(eventChannelName))

	defer httpServer.Close()

	stream := mustSubscribe(t, httpServer.URL, "")
	server.Close()

	select {
	case err := <-stream.Errors:
		if err != io.EOF {
			t.Errorf("got error %+v, want %+v", err, io.EOF)
		}
	case <-time.After(timeToWaitForEvent):
		t.Error("Timed out waiting for error event")
	}
}

func TestStreamClose(t *testing.T) {
	server := NewServer()
	httpServer := httptest.NewServer(server.Handler(eventChannelName))
	// The server has to be closed before the httpServer is closed.
	// Otherwise the httpServer has still an open connection and it can not close.
	defer httpServer.Close()
	defer server.Close()

	stream := mustSubscribe(t, httpServer.URL, "")
	stream.Close()
	// its safe to Close the stream multiple times
	stream.Close()

	select {
	case _, ok := <-stream.Events:
		if ok {
			t.Error("Expected stream.Events channel to be closed. Is still open.")
		}
	case <-time.After(timeToWaitForEvent):
		t.Error("Timed out waiting for stream.Events channel to close")
	}

	select {
	case _, ok := <-stream.Errors:
		if ok {
			t.Error("Expected stream.Errors channel to be closed. Is still open.")
		}
	case <-time.After(timeToWaitForEvent):
		t.Error("Timed out waiting for stream.Errors channel to close")
	}
}

func TestStreamCloseWithEvents(t *testing.T) {
	server := NewServer()
	httpServer := httptest.NewServer(server.Handler(eventChannelName))
	// The server has to be closed before the httpServer is closed.
	// Otherwise the httpServer has still an open connection and it can not close.
	defer httpServer.Close()
	defer server.Close()

	stream := mustSubscribe(t, httpServer.URL, "")

	publishedEvent := &publication{id: "123"}
	server.Publish([]string{eventChannelName}, publishedEvent)

	time.Sleep(100 * time.Millisecond)

	eventsC := drainEventChannel(stream.Events)

	stream.Close()

	select {
	case receivedEvents := <-eventsC:
		if len(receivedEvents) != 1 {
			t.Fatalf("got %d events after close, want %d", len(receivedEvents), 1)
		}

		if !reflect.DeepEqual(receivedEvents[0], publishedEvent) {
			t.Errorf("got event %+v, want %+v", receivedEvents[0], publishedEvent)
		}
	case <-time.After(timeToWaitForEvent):
		t.Fatalf("Timed out waiting for stream.Events channel to close")
	}
}

func drainEventChannel(c <-chan Event) <-chan []Event {
	eventsC := make(chan []Event, 1)

	go func() {
		defer close(eventsC)

		events := []Event{}
		for event := range c {
			events = append(events, event)
		}

		eventsC <- events
	}()

	return eventsC
}

func TestStreamCloseIsImmediate(t *testing.T) {
	server := NewServer()
	httpServer := httptest.NewServer(server.Handler(eventChannelName))
	// The server has to be closed before the httpServer is closed.
	// Otherwise the httpServer has still an open connection and it can not close.
	defer httpServer.Close()
	defer server.Close()

	stream := mustSubscribe(t, httpServer.URL, "")

	done := make(chan struct{})
	go func() {
		stream.Close()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Error("Timed out waiting for Close")
	}
}

func mustSubscribe(t *testing.T, url, lastEventId string) *Stream {
	stream, err := Subscribe(url, lastEventId)
	if err != nil {
		t.Fatalf("Failed to subscribe: %s", err)
	}
	return stream
}
