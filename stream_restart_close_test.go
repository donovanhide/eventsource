package eventsource

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/launchdarkly/go-test-helpers/httphelpers"
	"github.com/stretchr/testify/assert"
)

func TestStreamRestart(t *testing.T) {
	streamHandler1, events1, closer1 := closeableStreamHandler()
	streamHandler2, events2, closer2 := closeableStreamHandler()
	httpServer := httptest.NewServer(httphelpers.SequentialHandler(streamHandler1, streamHandler2))
	defer httpServer.Close()
	defer close(closer1)
	defer close(closer2)

	stream := mustSubscribe(t, httpServer.URL,
		StreamOptionInitialRetry(time.Millisecond))
	defer stream.Close()

	eventIn1 := &publication{id: "123"}
	events1 <- eventIn1
	eventOut1 := <-stream.Events
	assert.Equal(t, eventIn1, eventOut1)

	stream.Restart()

	eventIn2 := &publication{id: "123"}
	events2 <- eventIn2
	eventOut2 := <-stream.Events // received an event from streamHandler2
	assert.Equal(t, eventIn2, eventOut2)

	assert.Equal(t, 0, len(stream.Errors)) // restart is not reported as an error
}

func TestStreamClose(t *testing.T) {
	streamHandler, _, closer := closeableStreamHandler()
	httpServer := httptest.NewServer(streamHandler)
	defer httpServer.Close()
	defer close(closer)

	stream := mustSubscribe(t, httpServer.URL)
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

func TestStreamCloseWhileReconnecting(t *testing.T) {
	streamHandler, events, closer := closeableStreamHandler()
	httpServer := httptest.NewServer(streamHandler)
	defer httpServer.Close()
	defer close(closer)

	stream := mustSubscribe(t, httpServer.URL, StreamOptionInitialRetry(time.Hour))
	defer stream.Close()

	publishedEvent := &publication{id: "123"}
	events <- publishedEvent

	select {
	case <-stream.Events:
	case <-time.After(timeToWaitForEvent):
		t.Error("Timed out waiting for event")
		return
	}

	closer <- struct{}{}

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
