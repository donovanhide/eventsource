package eventsource

import (
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"time"
)

// Stream handles a connection for receiving Server Sent Events.
// It will try and reconnect if the connection is lost, respecting both
// received retry delays and event id's.
type Stream struct {
	transport     string
	socketAddress string
	c             http.Client
	tlsConfig     *tls.Config
	url           string
	lastEventId   string
	retry         time.Duration
	// Events emits the events received by the stream
	Events chan Event
	// Errors emits any errors encountered while reading events from the stream.
	// It's mainly for informative purposes - the client isn't required to take any
	// action when an error is encountered. The stream will always attempt to continue,
	// even if that involves reconnecting to the server.
	Errors chan error
}

type SubscriptionError struct {
	Code    int
	Message string
}

func (e SubscriptionError) Error() string {
	return fmt.Sprintf("%d: %s", e.Code, e.Message)
}

func SubscribeSocket(socketAddress string, url string, lastEventId string, tlsConfig *tls.Config) (*Stream, error) {
	stream := &Stream{
		url:           url,
		transport:     "unix",
		socketAddress: socketAddress,
		lastEventId:   lastEventId,
		tlsConfig:     tlsConfig,
		retry:         (time.Millisecond * 1000),
		Events:        make(chan Event),
		Errors:        make(chan error),
	}
	r, err := stream.connect()
	if err != nil {
		return nil, err
	}
	go stream.stream(r)
	return stream, nil
}

// Subscribe to the Events emitted from the specified url.
// If lastEventId is non-empty it will be sent to the server in case it can replay missed events.
func Subscribe(url string, lastEventId string, tlsConfig *tls.Config) (*Stream, error) {
	stream := &Stream{
		url:         url,
		transport:   "tcp",
		lastEventId: lastEventId,
		tlsConfig:   tlsConfig,
		retry:       (time.Millisecond * 1000),
		Events:      make(chan Event),
		Errors:      make(chan error),
	}
	r, err := stream.connect()
	if err != nil {
		return nil, err
	}
	go stream.stream(r)
	return stream, nil
}

func (stream *Stream) connect() (r io.ReadCloser, err error) {
	if stream.transport == "tcp" {
		return stream.connectTcp()
	} else if stream.transport == "unix" {
		return stream.connectUnix()
	}

	return nil, errors.New("Unsupported transport")
}

func (stream *Stream) connectUnix() (r io.ReadCloser, err error) {
	tr := &http.Transport{
		Dial: func(network, addr string) (net.Conn, error) {
			return net.Dial("unix", stream.socketAddress)
		},
		TLSClientConfig: stream.tlsConfig,
	}
	stream.c = http.Client{Transport: tr}

	var resp *http.Response
	var req *http.Request
	if req, err = http.NewRequest("GET", stream.url, nil); err != nil {
		return
	}
	req.URL.Scheme = "http"
	req.URL.Host = stream.socketAddress
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
func (stream *Stream) connectTcp() (r io.ReadCloser, err error) {
	if stream.tlsConfig != nil {
		tr := &http.Transport{
			TLSClientConfig:    stream.tlsConfig,
			DisableCompression: true,
		}
		stream.c = http.Client{Transport: tr}
	}

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
	dec := newDecoder(r)
	for {
		ev, err := dec.Decode()

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
		stream.Events <- ev
	}
	backoff := stream.retry
	for {
		log.Printf("Reconnecting in %0.4f secs", backoff.Seconds())
		time.Sleep(backoff)

		// NOTE: because of the defer we're opening the new connection
		// before closing the old one. Shouldn't be a problem in practice,
		// but something to be aware of.
		next, err := stream.connect()
		if err == nil {
			go stream.stream(next)
			break
		}
		stream.Errors <- err

		// don't let the exponential backoff go to over 64 seconds...
		if backoff < (time.Millisecond * 64000) {
			backoff *= 2
		}
	}
}
