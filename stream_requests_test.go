package eventsource

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/launchdarkly/go-test-helpers/httphelpers"
	"github.com/stretchr/testify/assert"
)

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

func TestStreamReconnectWithRequestBodySendsBodyTwice(t *testing.T) {
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
