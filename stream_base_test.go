package eventsource

import (
	"log"
	"net/http"
	"os"
	"testing"
	"time"
)

// Test implementation note: to test our client logic, we deliberately do *not* use the SSE
// server implementation in this package, but rather the simpler one from httphelpers, which
// is validated with its own tests. That way if there's something wrong with our server
// implementation, it will show up in just the server tests and not the client tests.

const (
	timeToWaitForEvent = 100 * time.Millisecond
)

func mustSubscribe(t *testing.T, url string, options ...StreamOption) *Stream {
	logger := log.New(os.Stderr, "", log.LstdFlags)
	allOpts := append(options, StreamOptionLogger(logger))
	resultCh := make(chan *Stream, 1)
	errorCh := make(chan error, 1)
	go func() {
		stream, err := SubscribeWithURL(url, allOpts...)
		if err != nil {
			errorCh <- err
		} else {
			resultCh <- stream
		}
	}()
	select {
	case stream := <-resultCh:
		return stream
	case err := <-errorCh:
		t.Fatalf("Failed to subscribe: %s", err)
	case <-time.After(time.Second * 2):
		t.Fatalf("Timed out trying to subscribe to stream")
	}
	return nil
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
