package eventsource

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/launchdarkly/go-test-helpers/v2/httphelpers"
)

func TestStreamReconnectsIfConnectionIsBroken(t *testing.T) {
	streamHandler, streamControl := httphelpers.SSEHandler(nil)
	defer streamControl.Close()
	httpServer := httptest.NewServer(streamHandler)
	defer httpServer.Close()

	stream := mustSubscribe(t, httpServer.URL, StreamOptionInitialRetry(time.Millisecond))
	defer stream.Close()

	event := httphelpers.SSEEvent{ID: "123"}
	streamControl.Enqueue(event)

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

	streamControl.Enqueue(event)

	// Consume errors until we've got another event
	for {
		select {
		case <-stream.Errors:
		case <-time.After(2 * time.Second):
			t.Error("Timed out waiting for event")
			return
		case receivedEvent := <-stream.Events:
			assert.Equal(t, &publication{id: "123"}, receivedEvent)
			return
		}
	}
}

func TestStreamCanUseBackoff(t *testing.T) {
	streamHandler, streamControl := httphelpers.SSEHandler(nil)
	defer streamControl.Close()
	httpServer := httptest.NewServer(streamHandler)
	defer httpServer.Close()

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
	streamHandler, streamControl := httphelpers.SSEHandler(nil)
	defer streamControl.Close()
	httpServer := httptest.NewServer(streamHandler)
	defer httpServer.Close()

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
	streamHandler, streamControl := httphelpers.SSEHandler(nil)
	defer streamControl.Close()
	httpServer := httptest.NewServer(streamHandler)
	defer httpServer.Close()

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
	streamHandler1, streamControl1 := httphelpers.SSEHandler(nil)
	defer streamControl1.Close()
	streamHandler2, streamControl2 := httphelpers.SSEHandler(nil)
	defer streamControl2.Close()
	httpServer := httptest.NewServer(httphelpers.SequentialHandler(streamHandler1, streamHandler2))
	defer httpServer.Close()

	baseDelay := time.Millisecond
	resetInterval := time.Millisecond * 200
	stream := mustSubscribe(t, httpServer.URL,
		StreamOptionInitialRetry(baseDelay),
		StreamOptionUseBackoff(time.Hour),
		StreamOptionRetryResetInterval(resetInterval))
	defer stream.Close()

	retry := stream.getRetryDelayStrategy()

	// The first stream connection sends an event, so the stream state becomes "good".
	event := httphelpers.SSEEvent{ID: "123"}
	streamControl1.Enqueue(event)

	// We ask the retryDelayStrategy to compute the next two delay values; they should show an increase.
	d0 := retry.NextRetryDelay(time.Now())
	d1 := retry.NextRetryDelay(time.Now())
	assert.Equal(t, baseDelay, d0)
	assert.Equal(t, baseDelay*2, d1)

	// The first connection is broken; the state becomes "bad".
	streamControl1.EndAll()

	// After it reconnects, the second connection receives an event and the state becomes "good" again.
	streamControl2.Enqueue(event)
	<-stream.Events

	// Now, ask the retryDelayStrategy what the next delay value would be if the next attempt happened
	// 200 milliseconds from now (assuming the stream remained good). It should go back to baseDelay.
	d2 := retry.NextRetryDelay(time.Now().Add(resetInterval))
	assert.Equal(t, baseDelay, d2)
}
