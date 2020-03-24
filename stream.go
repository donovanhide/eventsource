package eventsource

import (
	"errors"
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
	lastEventID string
	readTimeout time.Duration
	retryDelay  *retryDelayStrategy
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

var (
	// ErrReadTimeout is the error that will be emitted if a stream was closed due to not
	// receiving any data within the configured read timeout interval.
	ErrReadTimeout = errors.New("Read timeout on stream")
)

// SubscriptionError is an error object returned from a stream when there is an HTTP error.
type SubscriptionError struct {
	Code    int
	Message string
}

func (e SubscriptionError) Error() string {
	return fmt.Sprintf("%d: %s", e.Code, e.Message)
}

// Subscribe to the Events emitted from the specified url.
// If lastEventId is non-empty it will be sent to the server in case it can replay missed events.
// Deprecated: use SubscribeWithURL instead.
func Subscribe(url, lastEventID string) (*Stream, error) {
	return SubscribeWithURL(url, StreamOptionLastEventID(lastEventID))
}

// SubscribeWithURL subscribes to the Events emitted from the specified URL. The stream can
// be configured by providing any number of StreamOption values.
func SubscribeWithURL(url string, options ...StreamOption) (*Stream, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	return SubscribeWithRequestAndOptions(req, options...)
}

// SubscribeWithRequest will take an http.Request to set up the stream, allowing custom headers
// to be specified, authentication to be configured, etc.
// Deprecated: use SubscribeWithRequestAndOptions instead.
func SubscribeWithRequest(lastEventID string, request *http.Request) (*Stream, error) {
	return SubscribeWithRequestAndOptions(request, StreamOptionLastEventID(lastEventID))
}

// SubscribeWith takes a HTTP client and request providing customization over both headers and
// control over the HTTP client settings (timeouts, tls, etc)
// If request.Body is set, then request.GetBody should also be set so that we can reissue the request
// Deprecated: use SubscribeWithRequestAndOptions instead.
func SubscribeWith(lastEventID string, client *http.Client, request *http.Request) (*Stream, error) {
	return SubscribeWithRequestAndOptions(request, StreamOptionHTTPClient(client),
		StreamOptionLastEventID(lastEventID))
}

// SubscribeWithRequestAndOptions takes an initial http.Request to set up the stream - allowing
// custom headers, authentication, etc. to be configured - and also takes any number of
// StreamOption values to set other properties of the stream, such as timeouts or a specific
// HTTP client to use.
func SubscribeWithRequestAndOptions(request *http.Request, options ...StreamOption) (*Stream, error) {
	defaultClient := *http.DefaultClient

	configuredOptions := streamOptions{
		httpClient:         &defaultClient,
		initialRetry:       DefaultInitialRetry,
		retryResetInterval: DefaultRetryResetInterval,
	}

	for _, o := range options {
		if err := o.apply(&configuredOptions); err != nil {
			return nil, err
		}
	}

	var backoff backoffStrategy
	var jitter jitterStrategy
	if configuredOptions.backoffMaxDelay > 0 {
		backoff = newDefaultBackoff(configuredOptions.backoffMaxDelay)
	}
	if configuredOptions.jitterRatio > 0 {
		jitter = newDefaultJitter(configuredOptions.jitterRatio, 0)
	}
	retryDelay := newRetryDelayStrategy(
		configuredOptions.initialRetry,
		configuredOptions.retryResetInterval,
		backoff,
		jitter,
	)

	stream := &Stream{
		c:           configuredOptions.httpClient,
		lastEventID: configuredOptions.lastEventID,
		readTimeout: configuredOptions.readTimeout,
		req:         request,
		retryDelay:  retryDelay,
		Events:      make(chan Event),
		Errors:      make(chan error),
		Logger:      configuredOptions.logger,
		closer:      make(chan struct{}),
	}

	// override checkRedirect to include headers before go1.8
	// we'd prefer to skip this because it is not thread-safe and breaks golang race condition checking
	setCheckRedirect(stream.c)

	var initialRetryTimeoutCh <-chan time.Time
	var lastError error
	if configuredOptions.initialRetryTimeout > 0 {
		initialRetryTimeoutCh = time.After(configuredOptions.initialRetryTimeout)
	}
	for {
		r, err := stream.connect()
		if err == nil {
			go stream.stream(r)
			return stream, nil
		}
		lastError = err
		if configuredOptions.initialRetryTimeout == 0 {
			return nil, err
		}
		delay := stream.retryDelay.NextRetryDelay(time.Now())
		if configuredOptions.logger != nil {
			configuredOptions.logger.Printf("Connection failed (%s), retrying in %0.4f secs\n", err, delay.Seconds())
		}
		nextRetryCh := time.After(delay)
		select {
		case <-initialRetryTimeoutCh:
			if lastError == nil {
				lastError = errors.New("timeout elapsed while waiting to connect")
			}
			return nil, lastError
		case <-nextRetryCh:
			continue
		}
	}
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
	if len(stream.lastEventID) > 0 {
		stream.req.Header.Set("Last-Event-ID", stream.lastEventID)
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
		//nolint: gosec
		message, _ := ioutil.ReadAll(resp.Body)
		//nolint: gosec
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

	scheduleRetry := func() {
		logger := stream.getLogger()
		delay := stream.retryDelay.NextRetryDelay(time.Now())
		if logger != nil {
			logger.Printf("Reconnecting in %0.4f secs\n", delay.Seconds())
		}
		time.AfterFunc(delay, func() {
			retryChan <- struct{}{}
		})
	}

NewStream:
	for {
		events := make(chan Event)
		errs := make(chan error)

		if r != nil {
			dec := NewDecoderWithOptions(r, DecoderOptionReadTimeout(stream.readTimeout))
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
				//nolint: gosec
				_ = r.Close()
				r = nil
				scheduleRetry()
				continue NewStream
			case ev := <-events:
				pub := ev.(*publication)
				if pub.Retry() > 0 {
					stream.retryDelay.SetBaseDelay(time.Duration(pub.Retry()) * time.Millisecond)
				}
				if len(pub.Id()) > 0 {
					stream.lastEventID = pub.Id()
				}
				stream.retryDelay.SetGoodSince(time.Now())
				stream.Events <- ev
			case <-stream.closer:
				if r != nil {
					//nolint: gosec
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
					scheduleRetry()
				}
				continue NewStream
			}
		}
	}

	close(stream.Errors)
	close(stream.Events)
}

func (stream *Stream) getRetryDelayStrategy() *retryDelayStrategy { // nolint:megacheck // unused except by tests
	return stream.retryDelay
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
