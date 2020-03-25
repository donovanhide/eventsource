package eventsource

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"testing"
	"time"
)

const (
	timeToWaitForEvent = 100 * time.Millisecond
)

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
