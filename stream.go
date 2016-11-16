package eventsource

import (
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"math/rand"
	"net/http"
	"time"
)

// Stream handles a connection for receiving Server Sent Events.
// It will try and reconnect if the connection is lost, respecting both
// received retry delays and event id's.
type Stream struct {
	c           http.Client
	req         *http.Request
	lastEventId string
	retry       time.Duration
	// Events emits the events received by the stream
	Events chan Event
	// Errors emits any errors encountered while reading events from the stream.
	// It's mainly for informative purposes - the client isn't required to take any
	// action when an error is encountered. The stream will always attempt to continue,
	// even if that involves reconnecting to the server.
	Errors chan error
	// Comments emits any comment lines encountered while reading from the stream
	Comments chan string
	// Logger is a logger that, when set, will be used for logging debug messages
	Logger *log.Logger
	// The maximum time to wait between reconnection attempts
	maxReconnectionTime time.Duration
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
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	return SubscribeWithRequest(lastEventId, req)
}

// SubscribeWithRequest will take an http.Request to setup the stream, allowing custom headers
// to be specified, authentication to be configured, etc.
func SubscribeWithRequest(lastEventId string, req *http.Request) (*Stream, error) {
	stream := &Stream{
		req:                 req,
		lastEventId:         lastEventId,
		retry:               (time.Millisecond * 3000),
		Events:              make(chan Event),
		Errors:              make(chan error),
		Comments:            make(chan string),
		maxReconnectionTime: (time.Millisecond * 30000),
	}
	stream.c.CheckRedirect = checkRedirect

	r, err := stream.connect()
	if err != nil {
		return nil, err
	}
	go stream.stream(r)
	return stream, nil
}

// Go's http package doesn't copy headers across when it encounters
// redirects so we need to do that manually.
func checkRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 10 {
		return errors.New("stopped after 10 redirects")
	}
	for k, vv := range via[0].Header {
		for _, v := range vv {
			req.Header.Add(k, v)
		}
	}
	return nil
}

func (stream *Stream) connect() (r io.ReadCloser, err error) {
	var resp *http.Response
	stream.req.Header.Set("Cache-Control", "no-cache")
	stream.req.Header.Set("Accept", "text/event-stream")
	if len(stream.lastEventId) > 0 {
		stream.req.Header.Set("Last-Event-ID", stream.lastEventId)
	}
	if resp, err = stream.c.Do(stream.req); err != nil {
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

func (stream *Stream) backoffWithJitter(attempts int) time.Duration {
	retry := stream.retry.Nanoseconds()
	max := stream.maxReconnectionTime.Nanoseconds()

	exp := pow(2, attempts)

	jitterVal := retry * int64(exp)

	if exp == 0 || jitterVal > max || jitterVal <= 0 {
		jitterVal = max
	}

	return time.Duration(jitterVal/2 + rand.Int63n(jitterVal)/2)
}

// Integer power: compute a**b, from Knuth
func pow(a, b int) int {
	p := 1
	for b > 0 {
		if b&1 != 0 {
			p *= a
		}
		b >>= 1
		a *= a
	}
	return p
}

func (stream *Stream) stream(r io.ReadCloser) {
	reconnectAttempts := 1
	defer r.Close()
	dec := NewDecoder(r)
	for {
		ev, comment, err := dec.Decode()

		if err != nil {
			stream.Errors <- err
			// respond to all errors by reconnecting and trying again
			break
		}

		if comment != nil {
			stream.Comments <- *comment
		}

		if pub, ok := ev.(*publication); ok {
			if pub.Retry() > 0 {
				stream.retry = time.Duration(pub.Retry()) * time.Millisecond
			}
			if len(pub.Id()) > 0 {
				stream.lastEventId = pub.Id()
			}
			stream.Events <- ev
		} else {
			stream.Logger.Printf("Received invalid event")
		}
	}
	for {
		backoff := stream.backoffWithJitter(reconnectAttempts)
		reconnectAttempts++
		time.Sleep(backoff)
		if stream.Logger != nil {
			stream.Logger.Printf("Reconnecting in %0.4f secs\n", backoff.Seconds())
		}

		// NOTE: because of the defer we're opening the new connection
		// before closing the old one. Shouldn't be a problem in practice,
		// but something to be aware of.
		next, err := stream.connect()
		if err == nil {
			go stream.stream(next)
			break
		}
		stream.Errors <- err
	}
}
