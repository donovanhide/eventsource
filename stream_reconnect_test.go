package eventsource

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/launchdarkly/go-test-helpers/httphelpers"
	"github.com/stretchr/testify/assert"
)

func TestStreamReconnectsIfConnectionIsBroken(t *testing.T) {
	streamHandler, events, closer := closeableStreamHandler()
	httpServer := httptest.NewServer(streamHandler)
	defer httpServer.Close()
	defer close(closer)

	stream := mustSubscribe(t, httpServer.URL, StreamOptionInitialRetry(time.Millisecond))
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

	events <- publishedEvent

	// Consume errors until we've got another event
	for {
		select {
		case <-stream.Errors:
		case <-time.After(2 * time.Second):
			t.Error("Timed out waiting for event")
			return
		case receivedEvent := <-stream.Events:
			assert.Equal(t, publishedEvent, receivedEvent)
			return
		}
	}
}

func TestStreamCanUseBackoff(t *testing.T) {
	streamHandler, _, closer := closeableStreamHandler()
	httpServer := httptest.NewServer(streamHandler)
	defer httpServer.Close()
	defer close(closer)

	baseDelay := time.Millisecond
	stream := mustSubscribe(t, httpServer.URL,
		StreamOptionInitialRetry(baseDelay),
		StreamOptionUseBackoff(time.Minute))
	defer stream.Close()

	retry := stream.getRetryDelayStrategy()
	assert.False(t, retry.hasJitter())
	d0 := retry.NextRetryDelay(time.Now())
	d1 := retry.NextRetryDelay(time.Now())
	d2 := retry.NextRetryDelay(time.Now())
	assert.Equal(t, baseDelay, d0)
	assert.Equal(t, baseDelay*2, d1)
	assert.Equal(t, baseDelay*4, d2)
}

func TestStreamCanUseJitter(t *testing.T) {
	streamHandler, _, closer := closeableStreamHandler()
	httpServer := httptest.NewServer(streamHandler)
	defer httpServer.Close()
	defer close(closer)

	baseDelay := time.Millisecond
	stream := mustSubscribe(t, httpServer.URL,
		StreamOptionInitialRetry(baseDelay),
		StreamOptionUseBackoff(time.Minute),
		StreamOptionUseJitter(0.5))
	defer stream.Close()

	retry := stream.getRetryDelayStrategy()
	assert.True(t, retry.hasJitter())
	d0 := retry.NextRetryDelay(time.Now())
	d1 := retry.NextRetryDelay(time.Now())
	assert.True(t, d0 >= baseDelay/2)
	assert.True(t, d0 <= baseDelay)
	assert.True(t, d1 >= baseDelay)
	assert.True(t, d1 <= baseDelay*2)
}

func TestStreamCanSetMaximumDelayWithBackoff(t *testing.T) {
	streamHandler, _, closer := closeableStreamHandler()
	httpServer := httptest.NewServer(streamHandler)
	defer httpServer.Close()
	defer close(closer)

	baseDelay := time.Millisecond
	max := baseDelay * 3
	stream := mustSubscribe(t, httpServer.URL,
		StreamOptionInitialRetry(baseDelay),
		StreamOptionUseBackoff(max))
	defer stream.Close()

	retry := stream.getRetryDelayStrategy()
	assert.False(t, retry.hasJitter())
	d0 := retry.NextRetryDelay(time.Now())
	d1 := retry.NextRetryDelay(time.Now())
	d2 := retry.NextRetryDelay(time.Now())
	assert.Equal(t, baseDelay, d0)
	assert.Equal(t, baseDelay*2, d1)
	assert.Equal(t, max, d2)
}

func TestStreamBackoffCanUseResetInterval(t *testing.T) {
	// In this test, streamHandler1 sends an event, then breaks the connection too soon for the delay to be
	// reset. We ask the retryDelayStrategy to compute the next delay; it should be higher than the initial
	// value. Then streamHandler2 sends an event, and we verify that the next delay that *would* come from the
	// retryDelayStrategy if the reset interval elapsed would be the initial delay, not a higher value.
	streamHandler1, events1, closer1 := closeableStreamHandler()
	streamHandler2, events2, closer2 := closeableStreamHandler()
	httpServer := httptest.NewServer(httphelpers.SequentialHandler(streamHandler1, streamHandler2))
	defer httpServer.Close()
	defer close(closer1)
	defer close(closer2)

	baseDelay := time.Millisecond
	resetInterval := time.Millisecond * 200
	stream := mustSubscribe(t, httpServer.URL,
		StreamOptionInitialRetry(baseDelay),
		StreamOptionUseBackoff(time.Hour),
		StreamOptionRetryResetInterval(resetInterval))
	defer stream.Close()

	retry := stream.getRetryDelayStrategy()

	// The first stream connection sends an event, so the stream state becomes "good".
	publishedEvent := &publication{id: "123"}
	events1 <- publishedEvent
	<-stream.Events

	// We ask the retryDelayStrategy to compute the next two delay values; they should show an increase.
	d0 := retry.NextRetryDelay(time.Now())
	d1 := retry.NextRetryDelay(time.Now())
	assert.Equal(t, baseDelay, d0)
	assert.Equal(t, baseDelay*2, d1)

	// The first connection is broken; the state becomes "bad".
	closer1 <- struct{}{}
	<-stream.Errors

	// After it reconnects, the second connection receives an event and the state becomes "good" again.
	events2 <- publishedEvent
	<-stream.Events

	// Now, ask the retryDelayStrategy what the next delay value would be if the next attempt happened
	// 200 milliseconds from now (assuming the stream remained good). It should go back to baseDelay.
	d2 := retry.NextRetryDelay(time.Now().Add(resetInterval))
	assert.Equal(t, baseDelay, d2)
}
