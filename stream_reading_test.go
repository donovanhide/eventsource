package eventsource

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/launchdarkly/go-test-helpers/v2/httphelpers"
)

func TestStreamSubscribeEventsChan(t *testing.T) {
	streamHandler, streamControl := httphelpers.SSEHandler(nil)
	defer streamControl.Close()
	httpServer := httptest.NewServer(streamHandler)
	defer httpServer.Close()

	stream := mustSubscribe(t, httpServer.URL)
	defer stream.Close()

	streamControl.Send(httphelpers.SSEEvent{ID: "123"})

	select {
	case receivedEvent := <-stream.Events:
		assert.Equal(t, &publication{id: "123"}, receivedEvent)
	case <-time.After(timeToWaitForEvent):
		t.Error("Timed out waiting for event")
	}
}

func TestStreamCanChangeRetryDelayBasedOnEvent(t *testing.T) {
	streamHandler, streamControl := httphelpers.SSEHandler(nil)
	defer streamControl.Close()
	httpServer := httptest.NewServer(streamHandler)
	defer httpServer.Close()

	baseDelay := time.Millisecond
	stream := mustSubscribe(t, httpServer.URL, StreamOptionInitialRetry(baseDelay))
	defer stream.Close()

	newRetryMillis := 3000
	streamControl.Send(httphelpers.SSEEvent{Event: "event1", Data: "a", RetryMillis: newRetryMillis})

	<-stream.Events

	retry := stream.getRetryDelayStrategy()
	d := retry.NextRetryDelay(time.Now())
	assert.Equal(t, time.Millisecond*time.Duration(newRetryMillis), d)
}

func TestStreamReadTimeout(t *testing.T) {
	timeout := time.Millisecond * 200

	streamHandler1, streamControl1 := httphelpers.SSEHandler(nil)
	defer streamControl1.Close()
	streamHandler2, streamControl2 := httphelpers.SSEHandler(nil)
	defer streamControl2.Close()
	httpServer := httptest.NewServer(httphelpers.SequentialHandler(streamHandler1, streamHandler2))
	defer httpServer.Close()

	stream := mustSubscribe(t, httpServer.URL, StreamOptionReadTimeout(timeout),
		StreamOptionInitialRetry(time.Millisecond))
	defer stream.Close()

	event := httphelpers.SSEEvent{ID: "123"}
	streamControl1.Enqueue(event)
	streamControl2.Enqueue(event)

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
		t.Errorf("Expected 1 error, received %d (%+v)", len(receivedErrors), receivedErrors)
	} else {
		if receivedErrors[0] != ErrReadTimeout {
			t.Errorf("Expected %s, received %s", ErrReadTimeout, receivedErrors[0])
		}
	}
}

func TestStreamReadTimeoutIsPreventedByComment(t *testing.T) {
	timeout := time.Millisecond * 200

	streamHandler1, streamControl1 := httphelpers.SSEHandler(nil)
	defer streamControl1.Close()
	streamHandler2, streamControl2 := httphelpers.SSEHandler(nil)
	defer streamControl2.Close()
	httpServer := httptest.NewServer(httphelpers.SequentialHandler(streamHandler1, streamHandler2))
	defer httpServer.Close()

	stream := mustSubscribe(t, httpServer.URL, StreamOptionReadTimeout(timeout),
		StreamOptionInitialRetry(time.Millisecond))
	defer stream.Close()

	event := httphelpers.SSEEvent{ID: "123"}
	streamControl1.Enqueue(event)

	var receivedEvents []Event
	var receivedErrors []error

	waitUntil := time.After(timeout + (timeout / 2))

ReadLoop:
	for {
		select {
		case e := <-stream.Events:
			receivedEvents = append(receivedEvents, e)
			time.Sleep(time.Duration(float64(timeout) * 0.75))
			streamControl1.SendComment("")
		case err := <-stream.Errors:
			receivedErrors = append(receivedErrors, err)
		case <-waitUntil:
			break ReadLoop
		}
	}

	httpServer.CloseClientConnections()

	if len(receivedEvents) != 1 {
		t.Errorf("Expected 1 event, received %d", len(receivedEvents))
	}
	if len(receivedErrors) != 0 {
		t.Errorf("Expected 0 errors, received %d (%+v)", len(receivedErrors), receivedErrors)
	}
}
