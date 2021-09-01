package eventsource

import (
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"time"
)

const (
	streamErrChanLen = 10
	minStreamBackoff = 1 * time.Millisecond
)

type streamConnectFunc func(string) (io.ReadCloser, error)

// Stream handles a connection for receiving Server Sent Events.
// It will try and reconnect if the connection is lost, respecting both
// received retry delays and event id's.
type Stream struct {
	// connectFunc is the function used to connect to the event stream
	connectFunc streamConnectFunc

	lastEventId string
	retry       time.Duration
	// closeChan is used to notify the streamer goroutine that the stream has been closed
	closeChan chan struct{}

	// Events emits the events received by the stream
	Events chan Event
	// Errors emits any errors encountered while reading events from the stream.
	// It's mainly for informative purposes - the client isn't required to take any
	// action when an error is encountered. The stream will always attempt to continue,
	// even if that involves reconnecting to the server.
	Errors chan error
	// Logger is a logger that, when set, will be used for logging debug messages
	Logger *log.Logger
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
func SubscribeWith(lastEventId string, client *http.Client, request *http.Request) (*Stream, error) {
	return subscribe(lastEventId, connectHTTP(lastEventId, client, request))
}

func subscribe(lastEventId string, connectFunc streamConnectFunc) (*Stream, error) {
	stream := &Stream{
		connectFunc: connectFunc,

		lastEventId: lastEventId,
		retry:       3000 * time.Millisecond,
		closeChan:   make(chan struct{}),

		Events: make(chan Event),
		Errors: make(chan error, streamErrChanLen),
	}

	go stream.run()

	return stream, nil
}

// Close will close the stream. It is safe for concurrent access and can be called multiple times.
func (stream *Stream) Close() {
	select {
	case <-stream.closeChan:
	default:
		close(stream.closeChan)
	}
}

// connectHTTP connects to an event stream using the provided http request and client
func connectHTTP(lastEventID string, client *http.Client, request *http.Request) func(string) (io.ReadCloser, error) {
	return func(lastEvtID string) (io.ReadCloser, error) {
		client.CheckRedirect = checkRedirect

		request.Header.Set("Cache-Control", "no-cache")
		request.Header.Set("Accept", "text/event-stream")

		if lastEventID != "" {
			request.Header.Set("Last-Event-ID", lastEventID)
		}

		res, err := client.Do(request)
		if err != nil {
			return nil, err
		}

		if res.StatusCode != 200 {
			message, _ := ioutil.ReadAll(res.Body)
			res.Body.Close()

			return nil, SubscriptionError{
				Code:    res.StatusCode,
				Message: string(message),
			}
		}

		return res.Body, nil
	}
}

func (stream *Stream) run() {
	backoff := minStreamBackoff

runLoop:
	for {
		reader, err := stream.connectFunc(stream.lastEventId)
		if err != nil {
			if stream.Logger != nil {
				stream.Logger.Printf("Reconnecting in %0.4f secs\n", backoff.Seconds())
			}
			time.Sleep(backoff)
			backoff = capStreamBackoff(backoff * 2)
			continue
		}

		// We connected successfully so reset the backoff
		backoff = minStreamBackoff
		stream.stream(reader)

		// Check if we're supposed to close after the stream finishes.  If not
		// just make a new connection and start all over!
		select {
		case <-stream.closeChan:
			break runLoop
		default:
		}
	}

	close(stream.Events)
	close(stream.Errors)
}

func (stream *Stream) stream(reader io.ReadCloser) {
	// If we fail to stream for some reason make sure it gets closed
	// when we're done.
	defer reader.Close()

	dec := NewDecoder(reader)

	for {
		// Check if the stream was closed before every event read just in case
		// we get stuck in a bad decode loop and never reach the point of
		// actually sending an event.
		select {
		case <-stream.closeChan:
			return
		default:
		}

		ev, err := dec.Decode()
		if err != nil {
			stream.writeError(err)
			continue
		}

		pub := ev.(*publication)
		if pub.Retry() > 0 {
			stream.retry = time.Duration(pub.Retry()) * time.Millisecond
		}
		if pub.Id() != "" {
			stream.lastEventId = pub.Id()
		}

		// Send the event but also watch for a close in case nobody
		// reads from the event channel and we get blocked writing.
		select {
		case <-stream.closeChan:
			reader.Close()
			return
		case stream.Events <- ev:
		}
	}

}

func (stream *Stream) writeError(err error) {
	// Start dropping old errors if nobody is reading from the
	// other end so we don't end up blocking on writing an error
	// but still keep a short history of errors.
	if len(stream.Errors) == streamErrChanLen {
		<-stream.Errors
	}
	stream.Errors <- err
}

func capStreamBackoff(backoff time.Duration) time.Duration {
	if backoff > 10*time.Second {
		return 10 * time.Second
	}
	return backoff
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
