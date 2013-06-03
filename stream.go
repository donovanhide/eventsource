package eventsource

import (
	"io"
	"log"
	"net/http"
	"time"
)

// A stream will manage connections to Server Sent Events. It will try and reconnect if the connection is lost, respecting both received retry delays and event id's.
type Stream struct {
	c           http.Client
	url         string
	lastEventId string
	retry       time.Duration
	Events      chan Event
	Errors      chan error
}

// Subscribe to the Events emitted from the specified url.
// A non-empty lastEventId will be sent to the server in case it can replay missed events.
func Subscribe(url, lastEventId string) (*Stream, error) {
	stream := &Stream{
		url:         url,
		lastEventId: lastEventId,
		retry:       (time.Millisecond * 3000),
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
	r = resp.Body
	return
}

func (stream *Stream) stream(r io.ReadCloser) {
	defer r.Close()
	dec := newDecoder(r)
	for {
		ev, err := dec.Decode()
		if err == io.EOF {
			stream.Errors <- err
			break
		}
		if err != nil {
			stream.Errors <- err
			continue
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
		time.Sleep(backoff)
		log.Printf("Reconnecting in %0.4f secs", backoff.Seconds())
		next, err := stream.connect()
		if err == nil {
			go stream.stream(next)
			break
		}
		stream.Errors <- err
		backoff *= 2
	}
}
