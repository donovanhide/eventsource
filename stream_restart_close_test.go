package eventsource

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/launchdarkly/go-test-helpers/v2/httphelpers"
)

func toPublication(e httphelpers.SSEEvent) *publication {
	return &publication{
		id:    e.ID,
		event: e.Event,
		data:  e.Data,
	}
}

func TestStreamRestart(t *testing.T) {
	streamHandler1, streamControl1 := httphelpers.SSEHandler(nil)
	defer streamControl1.Close()
	streamHandler2, streamControl2 := httphelpers.SSEHandler(nil)
	defer streamControl2.Close()
	httpServer := httptest.NewServer(httphelpers.SequentialHandler(streamHandler1, streamHandler2))
	defer httpServer.Close()

	stream := mustSubscribe(t, httpServer.URL,
		StreamOptionInitialRetry(time.Millisecond))
	defer stream.Close()

	eventIn1 := httphelpers.SSEEvent{ID: "123"}
	streamControl1.Enqueue(eventIn1)
	eventOut1 := <-stream.Events
	assert.Equal(t, toPublication(eventIn1), eventOut1)

	stream.Restart()

	eventIn2 := httphelpers.SSEEvent{ID: "456"}
	streamControl2.Enqueue(eventIn2)
	eventOut2 := <-stream.Events // received an event from streamHandler2
	assert.Equal(t, toPublication(eventIn2), eventOut2)

	assert.Equal(t, 0, len(stream.Errors)) // restart is not reported as an error
}

func TestStreamClose(t *testing.T) {
	streamHandler, streamControl := httphelpers.SSEHandler(nil)
	defer streamControl.Close()
	httpServer := httptest.NewServer(streamHandler)
	defer httpServer.Close()

	stream := mustSubscribe(t, httpServer.URL)
	stream.Close()
	// it's safe to Close the stream multiple times
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
	streamHandler, streamControl := httphelpers.SSEHandler(nil)
	defer streamControl.Close()
	httpServer := httptest.NewServer(streamHandler)
	defer httpServer.Close()

	stream := mustSubscribe(t, httpServer.URL, StreamOptionInitialRetry(time.Hour))
	defer stream.Close()

	streamControl.Enqueue(httphelpers.SSEEvent{ID: "123"})

	select {
	case <-stream.Events:
	case <-time.After(timeToWaitForEvent):
		t.Error("Timed out waiting for event")
		return
	}

	streamControl.EndAll()

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
