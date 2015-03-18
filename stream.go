package eventsource

import (
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"time"
)

type SubscriptionError struct {
	Code    int
	Message string
}

func (e SubscriptionError) Error() string {
	return fmt.Sprintf("%d: %s", e.Code, e.Message)
}

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
	Errors  chan error
	quit    chan bool
	hasQuit chan bool
}

// Subscribe to the Events emitted from the specified url.
// If lastEventId is non-empty it will be sent to the server in case it can replay missed events.
func Subscribe(url, lastEventId string) (*Stream, error) {
	stream := &Stream{
		url:         url,
		lastEventId: lastEventId,
		retry:       (time.Millisecond * 3000),
		Events:      make(chan Event),
		Errors:      make(chan error, 1),
		quit:        make(chan bool),
		hasQuit:     make(chan bool),
	}
	r, err := stream.connect()
	if err != nil {
		return nil, err
	}
	go stream.monitor(r)
	return stream, nil
}

// Close will stop the stream from reading any further events from the server.
// Not safe to be called by more than one goroutine.
func (stream *Stream) Close() {
	stream.quit <- true
	<-stream.hasQuit
}

func (stream *Stream) connect() (io.ReadCloser, error) {
	req, err := http.NewRequest("GET", stream.url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Cache-Control", "no-cache")
	req.Header.Set("Accept", "text/event-stream")
	if len(stream.lastEventId) > 0 {
		req.Header.Set("Last-Event-ID", stream.lastEventId)
	}
	resp, err := stream.c.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != 200 {
		message, _ := ioutil.ReadAll(resp.Body)
		return nil, SubscriptionError{
			Code:    resp.StatusCode,
			Message: string(message),
		}
	}
	return resp.Body, nil
}

func (stream *Stream) monitor(r io.ReadCloser) {
	e := make(chan error)
	go stream.stream(e, r)
	for {
		select {
		case err := <-e:
			r.Close()
			stream.sendError(err)
		case <-stream.quit:
			r.Close()
			<-e
			close(stream.Errors)
			close(stream.Events)
			stream.hasQuit <- true
			return
		}
		for backoff := stream.retry; ; backoff *= 2 {
			log.Printf("Reconnecting in %0.4f secs", backoff.Seconds())
			var err error
			r, err = stream.connect()
			if err == nil {
				go stream.stream(e, r)
				break
			}
			stream.sendError(err)
			time.Sleep(backoff)
		}
	}
}

func (stream *Stream) stream(e chan error, r io.ReadCloser) {
	dec := newDecoder(r)
	ev, err := dec.Decode()
	for ; err == nil; ev, err = dec.Decode() {
		pub := ev.(*publication)
		if pub.Retry() > 0 {
			stream.retry = time.Duration(pub.Retry()) * time.Millisecond
		}
		if len(pub.Id()) > 0 {
			stream.lastEventId = pub.Id()
		}
		stream.Events <- ev
	}
	e <- err
}

func (stream *Stream) sendError(err error) {
	select {
	case stream.Errors <- err:
	default:
		// consumer is apparently ignoring errors
	}
}
