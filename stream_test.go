package eventsource

import (
	"bytes"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"testing"
	"time"
)

const (
	eventChannelName   = "Test"
	timeToWaitForEvent = 100 * time.Millisecond
)

type eventReplayer struct {
	events []Event
}

func (r eventReplayer) Replay(channel string, lastEventID string) chan Event {
	ch := make(chan Event, len(r.events))
	for _, e := range r.events {
		ch <- e
	}
	return ch
}

func TestStreamSubscribeEventsChan(t *testing.T) {
	server := NewServer()
	httpServer := httptest.NewServer(server.Handler(eventChannelName))
	// The server has to be closed before the httpServer is closed.
	// Otherwise the httpServer has still an open connection and it can not close.
	defer httpServer.Close()
	defer server.Close()

	stream := mustSubscribe(t, httpServer.URL, "", StreamConfig{})

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

	stream := mustSubscribe(t, httpServer.URL, "", StreamConfig{})
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

	stream := mustSubscribe(t, httpServer.URL, "", StreamConfig{})
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

func TestStreamReconnect(t *testing.T) {
	server := NewServer()
	httpServer := httptest.NewServer(server.Handler(eventChannelName))
	// The server has to be closed before the httpServer is closed.
	// Otherwise the httpServer has still an open connection and it can not close.
	defer httpServer.Close()
	defer server.Close()

	stream := mustSubscribe(t, httpServer.URL, "", StreamConfig{})
	stream.setRetry(time.Millisecond)
	publishedEvent := &publication{id: "123"}
	server.Publish([]string{eventChannelName}, publishedEvent)

	select {
	case <-stream.Events:
	case <-time.After(timeToWaitForEvent):
		t.Error("Timed out waiting for event")
		return
	}

	httpServer.CloseClientConnections()

	// Expect at least one error
	select {
	case <-stream.Errors:
	case <-time.After(timeToWaitForEvent):
		t.Error("Timed out waiting for event")
		return
	}

	go func() {
		// Publish again after we've reconnected
		time.Sleep(time.Second)
		server.Publish([]string{eventChannelName}, publishedEvent)
	}()

	// Consume errors until we've got another event
	for {
		select {
		case <-stream.Errors:
		case <-time.After(2 * time.Second):
			t.Error("Timed out waiting for event")
			return
		case receivedEvent := <-stream.Events:
			if !reflect.DeepEqual(receivedEvent, publishedEvent) {
				t.Errorf("got event %+v, want %+v", receivedEvent, publishedEvent)
			}
			return
		}
	}
}

func TestStreamReconnectWithReportSendsBodyTwice(t *testing.T) {
	connections := make(chan struct{}, 2)
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := ioutil.ReadAll(r.Body)
		if string(body) != "my-body" {
			t.Error("didn't get expected body")
		}
		connections <- struct{}{}
		ticker := time.NewTicker(time.Millisecond)
		defer ticker.Stop()
		for range ticker.C {
			// Just send comments
			if _, err := w.Write([]byte(":\n")); err != nil {
				return
			}
		}
	}))

	defer httpServer.Close()

	req, _ := http.NewRequest("REPORT", httpServer.URL, bytes.NewBufferString("my-body"))
	if req.GetBody == nil {
		t.Fatalf("Expected get body to be set")
	}
	stream, err := SubscribeWithRequest("", req)
	if err != nil {
		t.Fatalf("Failed to subscribe: %s", err)
		return
	}

	// Acknowledge initial connection
	<-connections

	stream.setRetry(time.Millisecond)

	// Kick everyone off
	httpServer.CloseClientConnections()

	// Accept the error to unblock the retry handler
	<-stream.Errors

	// Wait for a second connection attempt
WaitForSecondConnection:
	for {
		select {
		case <-connections:
			break WaitForSecondConnection
		case <-time.After(2 * time.Second):
			t.Fatalf("Timed out waiting for second connection")
			return
		}
	}
	stream.Close()

	// Speed up the close by eliminating current connections
	httpServer.CloseClientConnections()
}

func TestStreamCloseWhileReconnecting(t *testing.T) {
	server := NewServer()
	httpServer := httptest.NewServer(server.Handler(eventChannelName))

	stream := mustSubscribe(t, httpServer.URL, "", StreamConfig{})
	stream.setRetry(time.Hour)
	publishedEvent := &publication{id: "123"}
	server.Publish([]string{eventChannelName}, publishedEvent)

	select {
	case <-stream.Events:
	case <-time.After(timeToWaitForEvent):
		t.Error("Timed out waiting for event")
		return
	}

	server.Close()
	httpServer.Close()

	// Expect at least one error
	select {
	case <-stream.Errors:
	case <-time.After(timeToWaitForEvent):
		t.Error("Timed out waiting for event")
		return
	}

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

func TestStreamReadTimeout(t *testing.T) {
	timeout := time.Millisecond * 200
	config := StreamConfig{
		ReadTimeout:  timeout,
		InitialRetry: time.Millisecond,
	}

	server := NewServer()
	server.ReplayAll = true
	publishedEvent := &publication{data: "123"}
	server.Register(eventChannelName, eventReplayer{[]Event{publishedEvent}})
	// this makes it send exactly one event for each connection
	httpServer := httptest.NewServer(server.Handler(eventChannelName))
	defer httpServer.Close()
	defer server.Close()

	stream := mustSubscribe(t, httpServer.URL, "", config)

	var receivedEvents []Event
	var receivedErrors []error

	waitUntil := time.After(timeout + (timeout / 2))
ReadLoop:
	for {
		select {
		case e := <-stream.Events:
			receivedEvents = append(receivedEvents, e)
		case err := <-stream.Errors:
			receivedErrors = append(receivedErrors, err)
		case <-waitUntil:
			break ReadLoop
		}
	}

	httpServer.CloseClientConnections()

	if len(receivedEvents) != 2 {
		t.Errorf("Expected 2 events, received %d", len(receivedEvents))
	}
	if len(receivedErrors) != 1 {
		t.Errorf("Expected 1 error, received %d", len(receivedErrors))
	} else {
		if receivedErrors[0] != ErrReadTimeout {
			t.Errorf("Expected %s, received %s", ErrReadTimeout, receivedErrors[0])
		}
	}
}

func mustSubscribe(t *testing.T, url, lastEventId string, config StreamConfig) *Stream {
	stream, err := SubscribeWithConfig(url, lastEventId, config)
	if err != nil {
		t.Fatalf("Failed to subscribe: %s", err)
	}
	stream.SetLogger(log.New(os.Stderr, "", log.LstdFlags))
	return stream
}
