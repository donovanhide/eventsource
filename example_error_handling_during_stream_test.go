package eventsource

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func eventsHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")

	flusher := w.(http.Flusher)
	k := 0
	for {
		if k >= 10 {
			break // enable httptestserver to close the current connection
		}
		fmt.Fprint(w, "event: test\n")
		fmt.Fprintf(w, "%s\n\n", "test")
		flusher.Flush()
		k += 1
	}

}

func TestErrorDuringStream(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(eventsHandler))
	req, _ := http.NewRequest("GET", ts.URL, nil)
	stream, _ := SubscribeWithTimeout("", http.DefaultClient, req, time.Second)
	ts.Close()
	closed := false
eventLoop:
	for {
		select {
		case _, ok := <-stream.ErrorsWithTimeout:
			if !ok {
				closed = true
				break eventLoop
			}
		case _ = <-stream.EventsWithTimeout:
		}
	}
	if !closed {
		t.Errorf("The Events are not closed")
	}
}
