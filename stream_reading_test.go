package eventsource

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/launchdarkly/go-test-helpers/httphelpers"
	"github.com/stretchr/testify/assert"
)

func TestStreamSubscribeEventsChan(t *testing.T) {
	streamHandler, events, closer := closeableStreamHandler()
	httpServer := httptest.NewServer(streamHandler)
	defer httpServer.Close()
	defer close(closer)

	stream := mustSubscribe(t, httpServer.URL)
	defer stream.Close()

	publishedEvent := &publication{id: "123"}
	events <- publishedEvent

	select {
	case receivedEvent := <-stream.Events:
		assert.Equal(t, publishedEvent, receivedEvent)
	case <-time.After(timeToWaitForEvent):
		t.Error("Timed out waiting for event")
	}
}

func TestStreamCanChangeRetryDelayBasedOnEvent(t *testing.T) {
	streamHandler, events, closer := closeableStreamHandler()
	httpServer := httptest.NewServer(streamHandler)
	defer httpServer.Close()
	defer close(closer)

	baseDelay := time.Millisecond
	stream := mustSubscribe(t, httpServer.URL, StreamOptionInitialRetry(baseDelay))
	defer stream.Close()

	newRetryMillis := int64(3000)
	event := &publication{event: "event1", data: "a", retry: newRetryMillis}
	events <- event

	<-stream.Events

	retry := stream.getRetryDelayStrategy()
	d := retry.NextRetryDelay(time.Now())
	assert.Equal(t, time.Millisecond*time.Duration(newRetryMillis), d)
}

func TestStreamReadTimeout(t *testing.T) {
	timeout := time.Millisecond * 200

	streamHandler1, events1, closer1 := closeableStreamHandler()
	streamHandler2, events2, closer2 := closeableStreamHandler()
	httpServer := httptest.NewServer(httphelpers.SequentialHandler(streamHandler1, streamHandler2))
	defer httpServer.Close()
	defer close(closer1)
	defer close(closer2)

	stream := mustSubscribe(t, httpServer.URL, StreamOptionReadTimeout(timeout),
		StreamOptionInitialRetry(time.Millisecond))
	defer stream.Close()

	publishedEvent := &publication{id: "123"}
	events1 <- publishedEvent
	events2 <- publishedEvent

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

	streamHandler1, events1, closer1 := closeableStreamHandler()
	streamHandler2, _, closer2 := closeableStreamHandler()
	httpServer := httptest.NewServer(httphelpers.SequentialHandler(streamHandler1, streamHandler2))
	defer httpServer.Close()
	defer close(closer1)
	defer close(closer2)

	stream := mustSubscribe(t, httpServer.URL, StreamOptionReadTimeout(timeout),
		StreamOptionInitialRetry(time.Millisecond))
	defer stream.Close()

	publishedEvent := &publication{id: "123"}
	events1 <- publishedEvent

	var receivedEvents []Event
	var receivedErrors []error

	waitUntil := time.After(timeout + (timeout / 2))

ReadLoop:
	for {
		select {
		case e := <-stream.Events:
			receivedEvents = append(receivedEvents, e)
			time.Sleep(time.Duration(float64(timeout) * 0.75))
			events1 <- nil // nil causes the handler to send a comment
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
