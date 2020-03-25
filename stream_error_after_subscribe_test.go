package eventsource

import (
	"io"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/launchdarkly/go-test-helpers/httphelpers"
	"github.com/stretchr/testify/assert"
)

func TestStreamErrorsAreSentToErrorsChannel(t *testing.T) {
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
