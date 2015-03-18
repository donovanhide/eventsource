package eventsource

import (
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"sync"
	"time"
)

// Stream handles a connection for receiving Server Sent Events.
// It will try and reconnect if the connection is lost, respecting both
// received retry delays and event id's.
type Stream struct {
	c           http.Client
	url         string
	lastEventId string
	retry       time.Duration
	// Events emits the events received by the stream
	Events chan Event
	// Errors emits any errors encountered while reading events from the stream.
	// It's mainly for informative purposes - the client isn't required to take any
	// action when an error is encountered. The stream will always attempt to continue,
	// even if that involves reconnecting to the server.
	Errors      chan error
	reader      io.ReadCloser
	readerMutex sync.Mutex
	closeChan   chan bool
}

type SubscriptionError struct {
	Code    int
	Message string
}

func (e SubscriptionError) Error() string {
	return fmt.Sprintf("%d: %s", e.Code, e.Message)
}

// Subscribe to the Events emitted from the specified url.
// If lastEventId is non-empty it will be sent to the server in case it can replay missed events.
func Subscribe(url, lastEventId string) (*Stream, error) {
	stream := &Stream{
		url:         url,
		lastEventId: lastEventId,
		retry:       (time.Millisecond * 3000),
		Events:      make(chan Event),
		Errors:      make(chan error),
		closeChan:   make(chan bool),
	}
	r, err := stream.connect()
	if err != nil {
		return nil, err
	}
	go stream.stream(r)
	return stream, nil
}

func (stream *Stream) connect() (r io.ReadCloser, err error) {
	var resp *http.Response
	var req *http.Request
	if req, err = http.NewRequest("GET", stream.url, nil); err != nil {
		return
	}
	req.Header.Set("Cache-Control", "no-cache")
	req.Header.Set("Accept", "text/event-stream")
	if len(stream.lastEventId) > 0 {
		req.Header.Set("Last-Event-ID", stream.lastEventId)
	}
	if resp, err = stream.c.Do(req); err != nil {
		return
	}
	if resp.StatusCode != 200 {
		message, _ := ioutil.ReadAll(resp.Body)
		err = SubscriptionError{
			Code:    resp.StatusCode,
			Message: string(message),
		}
	}
	r = resp.Body
	return
}

func (stream *Stream) stream(r io.ReadCloser) {
	defer r.Close()
	defer func() {
		// close the channels once we're sure we're done writing to them.
		if stream.Closed() {
			close(stream.Events)
			close(stream.Errors)
		}
	}()

	stream.readerMutex.Lock()
	stream.reader = r
	stream.readerMutex.Unlock()
	if stream.Closed() {
		// this is to prevent potential race condition where the stream might have
		// been closed before we set stream.reader, and so we don't call dec.Decode()
		// which might block
		return
	}

	dec := newDecoder(r)
	for {
		ev, err := dec.Decode()
		if stream.Closed() {
			// Ignore any error from dec.Decode() as the stream was explicitly closed
			// by the user, which can cause read errors
			return
		}

		if err != nil {
			stream.Errors <- err
			// respond to all errors by reconnecting and trying again
			break
		}
		pub := ev.(*publication)
		if pub.Retry() > 0 {
			stream.retry = time.Duration(pub.Retry()) * time.Millisecond
		}
		if len(pub.Id()) > 0 {
			stream.lastEventId = pub.Id()
		}
		select {
		case stream.Events <- ev:
		case <-stream.closeChan:
			return
		}
	}
	backoff := stream.retry
	for {
		select {
		case <-time.After(backoff):
		case <-stream.closeChan:
			return
		}
		log.Printf("Reconnecting in %0.4f secs", backoff.Seconds())

		// NOTE: because of the defer we're opening the new connection
		// before closing the old one. Shouldn't be a problem in practice,
		// but something to be aware of.
		next, err := stream.connect()
		if err == nil {
			go stream.stream(next)
			break
		}
		stream.Errors <- err
		backoff *= 2
	}
}

// Close will stop the stream from reading any further events from the server
func (stream *Stream) Close() {
	// The purpose of this function is to unblock stream.stream()
	// and let it know that the stream has been closed
	close(stream.closeChan)
	stream.readerMutex.Lock()
	if stream.reader != nil {
		stream.reader.Close()
	}
	stream.readerMutex.Unlock()
}

// Closed indicates whether Close has been called on the stream
func (stream *Stream) Closed() bool {
	select {
	case <-stream.closeChan:
		return true
	default:
		return false
	}
}
