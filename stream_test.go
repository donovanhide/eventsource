package eventsource

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/launchdarkly/go-test-helpers/httphelpers"
	"github.com/stretchr/testify/assert"
)

const (
	eventChannelName   = "Test"
	timeToWaitForEvent = 100 * time.Millisecond
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
		if !reflect.DeepEqual(receivedEvent, publishedEvent) {
			t.Errorf("got event %+v, want %+v", receivedEvent, publishedEvent)
		}
	case <-time.After(timeToWaitForEvent):
		t.Error("Timed out waiting for event")
	}
}

func TestStreamSubscribeErrorsChan(t *testing.T) {
	streamHandler, _, closer := closeableStreamHandler()
	httpServer := httptest.NewServer(streamHandler)
	defer httpServer.Close()
	defer close(closer)

	stream := mustSubscribe(t, httpServer.URL)
	defer stream.Close()

	closer <- struct{}{}

	select {
	case err := <-stream.Errors:
		assert.Equal(t, io.EOF, err)
	case <-time.After(timeToWaitForEvent):
		t.Error("Timed out waiting for error event")
	}
}

func TestStreamCanUseErrorHandlerInsteadOfChannelForErrorOnExistingConnection(t *testing.T) {
	streamHandler1, _, closer1 := closeableStreamHandler()
	streamHandler2, _, closer2 := closeableStreamHandler()
	handler, requestsCh := httphelpers.RecordingHandler(httphelpers.SequentialHandler(streamHandler1, streamHandler2))
	httpServer := httptest.NewServer(handler)
	defer httpServer.Close()
	defer close(closer1)
	defer close(closer2)

	myErrChannel := make(chan error)
	defer close(myErrChannel)

	stream := mustSubscribe(t, httpServer.URL,
		StreamOptionErrorHandler(func(err error) StreamErrorHandlerResult {
			myErrChannel <- err
			return StreamErrorHandlerResult{}
		}),
		StreamOptionInitialRetry(time.Millisecond))
	defer stream.Close()
	assert.Nil(t, stream.Errors)
	<-requestsCh

	closer1 <- struct{}{}

	select {
	case err := <-myErrChannel:
		assert.Equal(t, io.EOF, err)
		// wait for reconnection attempt
		select {
		case <-requestsCh:
			return
		case <-time.After(200 * time.Millisecond):
			t.Error("Timed out waiting for reconnect")
		}
	case <-time.After(timeToWaitForEvent):
		t.Error("Timed out waiting for error event")
	}
}

func TestStreamErrorHandlerCanPreventRetryOnExistingConnection(t *testing.T) {
	streamHandler1, _, closer1 := closeableStreamHandler()
	streamHandler2, _, closer2 := closeableStreamHandler()
	handler, requestsCh := httphelpers.RecordingHandler(httphelpers.SequentialHandler(streamHandler1, streamHandler2))
	httpServer := httptest.NewServer(handler)
	defer httpServer.Close()
	defer close(closer1)
	defer close(closer2)

	myErrChannel := make(chan error)
	defer close(myErrChannel)

	stream := mustSubscribe(t, httpServer.URL,
		StreamOptionErrorHandler(func(err error) StreamErrorHandlerResult {
			myErrChannel <- err
			return StreamErrorHandlerResult{CloseNow: true}
		}),
		StreamOptionInitialRetry(time.Millisecond))
	defer stream.Close()
	assert.Nil(t, stream.Errors)
	<-requestsCh

	closer1 <- struct{}{}

	select {
	case err := <-myErrChannel:
		assert.Equal(t, io.EOF, err)
		// there should *not* be a reconnection attempt
		select {
		case <-requestsCh:
			t.Error("Stream should not have reconnected, but did")
		case <-time.After(200 * time.Millisecond):
			return
		}
	case <-time.After(timeToWaitForEvent):
		t.Error("Timed out waiting for error event")
	}
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

func TestStreamReconnect(t *testing.T) {
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
			if !reflect.DeepEqual(receivedEvent, publishedEvent) {
				t.Errorf("got event %+v, want %+v", receivedEvent, publishedEvent)
			}
			return
		}
	}
}

func TestStreamSendsLastEventID(t *testing.T) {
	streamHandler, _, closer := closeableStreamHandler()
	handler, requestsCh := httphelpers.RecordingHandler(streamHandler)

	httpServer := httptest.NewServer(handler)
	defer httpServer.Close()
	defer close(closer)

	lastID := "xyz"
	stream := mustSubscribe(t, httpServer.URL, StreamOptionLastEventID(lastID))
	defer stream.Close()

	r0 := <-requestsCh
	assert.Equal(t, lastID, r0.Request.Header.Get("Last-Event-ID"))
}

func TestStreamReconnectWithReportSendsBodyTwice(t *testing.T) {
	body := []byte("my-body")

	streamHandler, _, closer := closeableStreamHandler()
	handler, requestsCh := httphelpers.RecordingHandler(streamHandler)

	httpServer := httptest.NewServer(handler)
	defer httpServer.Close()
	defer close(closer)

	req, _ := http.NewRequest("REPORT", httpServer.URL, bytes.NewBuffer(body))
	if req.GetBody == nil {
		t.Fatalf("Expected get body to be set")
	}
	stream, err := SubscribeWithRequestAndOptions(req, StreamOptionInitialRetry(time.Millisecond))
	if err != nil {
		t.Fatalf("Failed to subscribe: %s", err)
		return
	}
	defer stream.Close()

	// Wait for the first request
	r0 := <-requestsCh

	// Allow the stream to reconnect once; get the second request
	closer <- struct{}{}
	<-stream.Errors // Accept the error to unblock the retry handler
	r1 := <-requestsCh

	stream.Close()

	assert.Equal(t, body, r0.Body)
	assert.Equal(t, body, r1.Body)
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

func TestStreamCanUseCustomClient(t *testing.T) {
	streamHandler, _, closer := closeableStreamHandler()
	handler, requestsCh := httphelpers.RecordingHandler(streamHandler)
	httpServer := httptest.NewServer(handler)
	defer httpServer.Close()
	defer close(closer)

	client := *http.DefaultClient
	client.Transport = urlSuffixingRoundTripper{http.DefaultTransport, "path"}

	stream := mustSubscribe(t, httpServer.URL, StreamOptionHTTPClient(&client))
	defer stream.Close()

	r := <-requestsCh
	assert.Equal(t, "/path", r.Request.URL.Path)
}

func TestStreamDoesNotRetryInitialConnectionByDefault(t *testing.T) {
	connectionFailureHandler := httphelpers.PanicHandler(errors.New("sorry"))
	streamHandler, _, closer := closeableStreamHandler()
	handler, requestsCh := httphelpers.RecordingHandler(httphelpers.SequentialHandler(connectionFailureHandler, streamHandler))
	httpServer := httptest.NewServer(handler)
	defer httpServer.Close()
	defer close(closer)

	stream, err := SubscribeWithURL(httpServer.URL)
	defer func() {
		if stream != nil {
			stream.Close()
		}
	}()
	assert.Error(t, err)

	assert.Equal(t, 1, len(requestsCh))
}

func TestStreamCanRetryInitialConnection(t *testing.T) {
	connectionFailureHandler := httphelpers.PanicHandler(errors.New("sorry"))
	streamHandler, _, closer := closeableStreamHandler()
	handler, requestsCh := httphelpers.RecordingHandler(httphelpers.SequentialHandler(
		connectionFailureHandler,
		connectionFailureHandler,
		streamHandler))
	httpServer := httptest.NewServer(handler)
	defer httpServer.Close()
	defer close(closer)

	stream, err := SubscribeWithURL(httpServer.URL,
		StreamOptionInitialRetry(time.Millisecond),
		StreamOptionCanRetryFirstConnection(time.Second*2))
	defer func() {
		if stream != nil {
			stream.Close()
		}
	}()
	assert.NoError(t, err)

	assert.Equal(t, 3, len(requestsCh))
}

func TestStreamCanRetryInitialConnectionWithIndefiniteTimeout(t *testing.T) {
	connectionFailureHandler := httphelpers.PanicHandler(errors.New("sorry"))
	streamHandler, _, closer := closeableStreamHandler()
	handler, requestsCh := httphelpers.RecordingHandler(httphelpers.SequentialHandler(
		connectionFailureHandler,
		connectionFailureHandler,
		streamHandler))
	httpServer := httptest.NewServer(handler)
	defer httpServer.Close()
	defer close(closer)

	stream, err := SubscribeWithURL(httpServer.URL,
		StreamOptionInitialRetry(time.Millisecond),
		StreamOptionCanRetryFirstConnection(-1))
	defer func() {
		if stream != nil {
			stream.Close()
		}
	}()
	assert.NoError(t, err)

	assert.Equal(t, 3, len(requestsCh))
}

func TestStreamCanRetryInitialConnectionUntilFiniteTimeout(t *testing.T) {
	connectionFailureHandler := httphelpers.PanicHandler(errors.New("sorry"))
	streamHandler, _, closer := closeableStreamHandler()
	handler, requestsCh := httphelpers.RecordingHandler(httphelpers.SequentialHandler(
		connectionFailureHandler,
		connectionFailureHandler,
		streamHandler))
	httpServer := httptest.NewServer(handler)
	defer httpServer.Close()
	defer close(closer)

	stream, err := SubscribeWithURL(httpServer.URL,
		StreamOptionInitialRetry(100*time.Millisecond),
		StreamOptionCanRetryFirstConnection(150*time.Millisecond))
	defer func() {
		if stream != nil {
			stream.Close()
		}
	}()
	assert.Error(t, err)

	assert.Equal(t, 2, len(requestsCh))
}

func TestStreamErrorHandlerCanAllowRetryOfInitialConnectionAfterNetworkError(t *testing.T) {
	testStreamErrorHandlerCanAllowRetryOfInitialConnection(t, httphelpers.PanicHandler(errors.New("sorry")),
		func(err error) {
			if !strings.HasSuffix(err.Error(), "EOF") {
				t.Errorf("expected EOF error, got %v", err)
			}
		})
}

func TestStreamErrorHandlerCanAllowRetryOfInitialConnectionAfterHTTPError(t *testing.T) {
	testStreamErrorHandlerCanAllowRetryOfInitialConnection(t, httphelpers.HandlerWithStatus(401),
		func(err error) {
			assert.Equal(t, "error 401", err.Error())
		})
}

func testStreamErrorHandlerCanAllowRetryOfInitialConnection(t *testing.T, errorHandler http.Handler, checkError func(error)) {
	streamHandler, _, closer := closeableStreamHandler()
	handler, requestsCh := httphelpers.RecordingHandler(httphelpers.SequentialHandler(
		errorHandler,
		streamHandler))
	httpServer := httptest.NewServer(handler)
	defer httpServer.Close()
	defer close(closer)

	myErrChannel := make(chan error, 1)

	stream, err := SubscribeWithURL(httpServer.URL,
		StreamOptionInitialRetry(100*time.Millisecond),
		StreamOptionCanRetryFirstConnection(150*time.Millisecond),
		StreamOptionErrorHandler(func(err error) StreamErrorHandlerResult {
			myErrChannel <- err
			return StreamErrorHandlerResult{}
		}))
	defer func() {
		if stream != nil {
			stream.Close()
		}
	}()
	assert.NoError(t, err)

	assert.Equal(t, 1, len(myErrChannel))
	e := <-myErrChannel
	checkError(e)

	assert.Equal(t, 2, len(requestsCh))
}

func TestStreamErrorHandlerCanPreventRetryOfInitialConnection(t *testing.T) {
	connectionFailureHandler := httphelpers.PanicHandler(errors.New("sorry"))
	streamHandler, _, closer := closeableStreamHandler()
	handler, requestsCh := httphelpers.RecordingHandler(httphelpers.SequentialHandler(
		connectionFailureHandler,
		streamHandler))
	httpServer := httptest.NewServer(handler)
	defer httpServer.Close()
	defer close(closer)

	stream, err := SubscribeWithURL(httpServer.URL,
		StreamOptionInitialRetry(100*time.Millisecond),
		StreamOptionCanRetryFirstConnection(150*time.Millisecond),
		StreamOptionErrorHandler(func(err error) StreamErrorHandlerResult {
			return StreamErrorHandlerResult{CloseNow: true}
		}))
	defer func() {
		if stream != nil {
			stream.Close()
		}
	}()
	assert.Error(t, err)

	assert.Equal(t, 1, len(requestsCh))
}

func mustSubscribe(t *testing.T, url string, options ...StreamOption) *Stream {
	logger := log.New(os.Stderr, "", log.LstdFlags)
	allOpts := append(options, StreamOptionLogger(logger))
	stream, err := SubscribeWithURL(url, allOpts...)
	if err != nil {
		t.Fatalf("Failed to subscribe: %s", err)
	}
	return stream
}

func closeableStreamHandler() (http.Handler, chan<- Event, chan<- struct{}) {
	eventsCh := make(chan Event, 10)
	closerCh := make(chan struct{}, 10)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Content-Type", "text/event-stream")
		w.Header().Add("Transfer-Encoding", "chunked")
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		enc := NewEncoder(w, false)
		for {
			select {
			case e := <-eventsCh:
				if e == nil {
					enc.Encode(comment{""})
				} else {
					if p, ok := e.(*publication); ok {
						if p.Retry() > 0 { // Encoder doesn't support the retry: attribute
							w.Write([]byte(fmt.Sprintf("retry:%d\n", p.Retry())))
						}
					}
					enc.Encode(e)
				}
				w.(http.Flusher).Flush()
			case <-closerCh:
				return
			}
		}
	})
	return handler, eventsCh, closerCh
}

type urlSuffixingRoundTripper struct {
	transport http.RoundTripper
	suffix    string
}

func (u urlSuffixingRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	url1, _ := req.URL.Parse(u.suffix)
	req.URL = url1
	return u.transport.RoundTrip(req)
}
