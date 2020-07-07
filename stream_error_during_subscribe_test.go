package eventsource

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/launchdarkly/go-test-helpers/v2/httphelpers"
)

func handlerCausingNetworkError() http.Handler {
	return httphelpers.BrokenConnectionHandler()
}

func handlerCausingHTTPError(status int) http.Handler {
	return httphelpers.HandlerWithStatus(status)
}

func shouldBeNetworkError(t *testing.T) func(error) {
	return func(err error) {
		if !strings.HasSuffix(err.Error(), "EOF") {
			t.Errorf("expected EOF error, got %v", err)
		}
	}
}

func shouldBeHTTPError(t *testing.T, status int) func(error) {
	return func(err error) {
		assert.Equal(t, SubscriptionError{Code: status}, err)
	}
}

func TestStreamDoesNotRetryInitialConnectionByDefaultAfterNetworkError(t *testing.T) {
	testStreamDoesNotRetryInitialConnectionByDefault(t, handlerCausingNetworkError(), shouldBeNetworkError(t))
}

func TestStreamDoesNotRetryInitialConnectionByDefaultAfterHTTPError(t *testing.T) {
	testStreamDoesNotRetryInitialConnectionByDefault(t, handlerCausingHTTPError(401), shouldBeHTTPError(t, 401))
}

func testStreamDoesNotRetryInitialConnectionByDefault(t *testing.T, errorHandler http.Handler, checkError func(error)) {
	streamHandler, streamControl := httphelpers.SSEHandler(nil)
	defer streamControl.Close()
	handler, requestsCh := httphelpers.RecordingHandler(httphelpers.SequentialHandler(errorHandler, streamHandler))
	httpServer := httptest.NewServer(handler)
	defer httpServer.Close()

	stream, err := SubscribeWithURL(httpServer.URL)
	defer func() {
		if stream != nil {
			stream.Close()
		}
	}()
	assert.Error(t, err)
	assert.Nil(t, stream)

	assert.Equal(t, 1, len(requestsCh))
}

func TestStreamCanRetryInitialConnection(t *testing.T) {
	streamHandler, streamControl := httphelpers.SSEHandler(nil)
	defer streamControl.Close()
	handler, requestsCh := httphelpers.RecordingHandler(httphelpers.SequentialHandler(
		handlerCausingNetworkError(),
		handlerCausingNetworkError(),
		streamHandler))
	httpServer := httptest.NewServer(handler)
	defer httpServer.Close()

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
	streamHandler, streamControl := httphelpers.SSEHandler(nil)
	defer streamControl.Close()
	handler, requestsCh := httphelpers.RecordingHandler(httphelpers.SequentialHandler(
		handlerCausingNetworkError(),
		handlerCausingNetworkError(),
		streamHandler))
	httpServer := httptest.NewServer(handler)
	defer httpServer.Close()

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
	streamHandler, streamControl := httphelpers.SSEHandler(nil)
	defer streamControl.Close()
	handler, requestsCh := httphelpers.RecordingHandler(httphelpers.SequentialHandler(
		handlerCausingNetworkError(),
		handlerCausingNetworkError(),
		streamHandler))
	httpServer := httptest.NewServer(handler)
	defer httpServer.Close()

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
	testStreamErrorHandlerCanAllowRetryOfInitialConnection(t, handlerCausingNetworkError(), shouldBeNetworkError(t))
}

func TestStreamErrorHandlerCanAllowRetryOfInitialConnectionAfterHTTPError(t *testing.T) {
	testStreamErrorHandlerCanAllowRetryOfInitialConnection(t, handlerCausingHTTPError(401), shouldBeHTTPError(t, 401))
}

func testStreamErrorHandlerCanAllowRetryOfInitialConnection(t *testing.T, errorHandler http.Handler, checkError func(error)) {
	streamHandler, streamControl := httphelpers.SSEHandler(nil)
	defer streamControl.Close()
	handler, requestsCh := httphelpers.RecordingHandler(httphelpers.SequentialHandler(
		errorHandler,
		streamHandler))
	httpServer := httptest.NewServer(handler)
	defer httpServer.Close()

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
	streamHandler, streamControl := httphelpers.SSEHandler(nil)
	defer streamControl.Close()
	handler, requestsCh := httphelpers.RecordingHandler(httphelpers.SequentialHandler(
		handlerCausingNetworkError(),
		streamHandler))
	httpServer := httptest.NewServer(handler)
	defer httpServer.Close()

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
