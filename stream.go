package eventsource

import (
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"sync"
	"time"
)

// Stream handles a connection for receiving Server Sent Events.
// It will try and reconnect if the connection is lost, respecting both
// received retry delays and event id's.
type Stream struct {
	c           *http.Client
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
	// Logger is a logger that, when set, will be used for logging debug messages
	Logger      Logger // Set with SetLogger if you want your code to be thread-safe
	closer      chan struct{}
	closeOnce   sync.Once
	mu          sync.RWMutex
	connections int
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
func SubscribeWithRequest(lastEventId string, request *http.Request) (*Stream, error) {
	return SubscribeWith(lastEventId, http.DefaultClient, request)
}

// SubscribeWith takes a http client and request providing customization over both headers and
// control over the http client settings (timeouts, tls, etc)
// If request.Body is set, then request.GetBody should also be set so that we can reissue the request
func SubscribeWith(lastEventId string, client *http.Client, request *http.Request) (*Stream, error) {
	// override checkRedirect to include headers before go1.8
	// we'd prefer to skip this because it is not thread-safe and breaks golang race condition checking
	setCheckRedirect(client)

	stream := &Stream{
		c:           client,
		req:         request,
		lastEventId: lastEventId,
		retry:       time.Millisecond * 3000,
		Events:      make(chan Event),
		Errors:      make(chan error),
		closer:      make(chan struct{}),
	}

	r, err := stream.connect()
	if err != nil {
		return nil, err
	}
	go stream.stream(r)
	return stream, nil
}

// Close will close the stream. It is safe for concurrent access and can be called multiple times.
func (stream *Stream) Close() {
	stream.closeOnce.Do(func() {
		close(stream.closer)
	})
}

func (stream *Stream) connect() (io.ReadCloser, error) {
	var err error
	var resp *http.Response
	stream.req.Header.Set("Cache-Control", "no-cache")
	stream.req.Header.Set("Accept", "text/event-stream")
	if len(stream.lastEventId) > 0 {
		stream.req.Header.Set("Last-Event-ID", stream.lastEventId)
	}
	req := *stream.req

	// All but the initial connection will need to regenerate the body
	if stream.connections > 0 && req.GetBody != nil {
		if req.Body, err = req.GetBody(); err != nil {
			return nil, err
		}
	}

	if resp, err = stream.c.Do(&req); err != nil {
		return nil, err
	}
	stream.connections++
	if resp.StatusCode != 200 {
		message, _ := ioutil.ReadAll(resp.Body)
		_ = resp.Body.Close()
		err = SubscriptionError{
			Code:    resp.StatusCode,
			Message: string(message),
		}
		return nil, err
	}
	return resp.Body, nil
}

func (stream *Stream) stream(r io.ReadCloser) {
	retryChan := make(chan struct{}, 1)

	scheduleRetry := func(backoff *time.Duration) {
		logger := stream.getLogger()
		if logger != nil {
			logger.Printf("Reconnecting in %0.4f secs\n", backoff.Seconds())
		}
		time.AfterFunc(*backoff, func() {
			retryChan <- struct{}{}
		})
		*backoff *= 2
	}

NewStream:
	for {
		backoff := stream.getRetry()
		events := make(chan Event)
		errs := make(chan error)

		if r != nil {
			dec := NewDecoder(r)
			go func() {
				for {
					ev, err := dec.Decode()

					if err != nil {
						errs <- err
						close(errs)
						close(events)
						return
					}
					events <- ev
				}
			}()
		}

		for {
			select {
			case err := <-errs:
				stream.Errors <- err
				_ = r.Close()
				r = nil
				scheduleRetry(&backoff)
				continue NewStream
			case ev := <-events:
				pub := ev.(*publication)
				if pub.Retry() > 0 {
					backoff = time.Duration(pub.Retry()) * time.Millisecond
				}
				if len(pub.Id()) > 0 {
					stream.lastEventId = pub.Id()
				}
				stream.Events <- ev
			case <-stream.closer:
				if r != nil {
					_ = r.Close()
					// allow the decoding goroutine to terminate
					for range errs {
					}
					for range events {
					}
				}
				break NewStream
			case <-retryChan:
				var err error
				r, err = stream.connect()
				if err != nil {
					r = nil
					stream.Errors <- err
					scheduleRetry(&backoff)
				}
				continue NewStream
			}
		}
	}

	close(stream.Errors)
	close(stream.Events)
}

func (stream *Stream) setRetry(retry time.Duration) { // nolint:megacheck // unused except by tests
	stream.mu.Lock()
	defer stream.mu.Unlock()
	stream.retry = retry
}

func (stream *Stream) getRetry() time.Duration {
	stream.mu.RLock()
	defer stream.mu.RUnlock()
	return stream.retry
}

// SetLogger sets the Logger field in a thread-safe manner.
func (stream *Stream) SetLogger(logger Logger) {
	stream.mu.Lock()
	defer stream.mu.Unlock()
	stream.Logger = logger
}

func (stream *Stream) getLogger() Logger {
	stream.mu.RLock()
	defer stream.mu.RUnlock()
	return stream.Logger
}
