package eventsource

import (
	"bufio"
	"io"
	"strconv"
	"strings"
	"time"
)

type publication struct {
	id, event, data, lastEventID string
	retry                        int64
}

//nolint:golint,stylecheck // should be ID; retained for backward compatibility
func (s *publication) Id() string    { return s.id }
func (s *publication) Event() string { return s.event }
func (s *publication) Data() string  { return s.data }
func (s *publication) Retry() int64  { return s.retry }

// LastEventID is from a separate interface, EventWithLastID
func (s *publication) LastEventID() string { return s.lastEventID }

// A Decoder is capable of reading Events from a stream.
type Decoder struct {
	linesCh     <-chan string
	errorCh     <-chan error
	readTimeout time.Duration
	lastEventID string
}

// DecoderOption is a common interface for optional configuration parameters that can be
// used in creating a Decoder.
type DecoderOption interface {
	apply(s *Decoder)
}

type readTimeoutDecoderOption time.Duration

func (o readTimeoutDecoderOption) apply(d *Decoder) {
	d.readTimeout = time.Duration(o)
}

type lastEventIDDecoderOption string

func (o lastEventIDDecoderOption) apply(d *Decoder) {
	d.lastEventID = string(o)
}

// DecoderOptionReadTimeout returns an option that sets the read timeout interval for a
// Decoder when the Decoder is created. If the Decoder does not receive new data within this
// length of time, it will return an error. By default, there is no read timeout.
func DecoderOptionReadTimeout(timeout time.Duration) DecoderOption {
	return readTimeoutDecoderOption(timeout)
}

// DecoderOptionLastEventID returns an option that sets the last event ID property for a
// Decoder when the Decoder is created. This allows the last ID to be included in new
// events if they do not override it.
func DecoderOptionLastEventID(lastEventID string) DecoderOption {
	return lastEventIDDecoderOption(lastEventID)
}

// NewDecoder returns a new Decoder instance that reads events with the given io.Reader.
func NewDecoder(r io.Reader) *Decoder {
	bufReader := bufio.NewReader(newNormaliser(r))
	linesCh, errorCh := newLineStreamChannel(bufReader)
	return &Decoder{
		linesCh: linesCh,
		errorCh: errorCh,
	}
}

// NewDecoderWithOptions returns a new Decoder instance that reads events with the given
// io.Reader, with optional configuration parameters.
func NewDecoderWithOptions(r io.Reader, options ...DecoderOption) *Decoder {
	d := NewDecoder(r)
	for _, o := range options {
		o.apply(d)
	}
	return d
}

// Decode reads the next Event from a stream (and will block until one
// comes in).
// Graceful disconnects (between events) are indicated by an io.EOF error.
// Any error occurring mid-event is considered non-graceful and will
// show up as some other error (most likely io.ErrUnexpectedEOF).
func (dec *Decoder) Decode() (Event, error) {
	pub := new(publication)
	inDecoding := false
	var timeoutTimer *time.Timer
	var timeoutCh <-chan time.Time
	if dec.readTimeout > 0 {
		timeoutTimer = time.NewTimer(dec.readTimeout)
		defer timeoutTimer.Stop()
		timeoutCh = timeoutTimer.C
	}
ReadLoop:
	for {
		select {
		case line := <-dec.linesCh:
			if timeoutTimer != nil {
				if !timeoutTimer.Stop() {
					<-timeoutCh
				}
				timeoutTimer.Reset(dec.readTimeout)
			}
			if line == "\n" && inDecoding {
				// the empty line signals the end of an event
				break ReadLoop
			} else if line == "\n" && !inDecoding {
				// only a newline was sent, so we don't want to publish an empty event but try to read again
				continue ReadLoop
			}
			line = strings.TrimSuffix(line, "\n")
			if strings.HasPrefix(line, ":") {
				continue ReadLoop
			}
			sections := strings.SplitN(line, ":", 2)
			field, value := sections[0], ""
			if len(sections) == 2 {
				value = strings.TrimPrefix(sections[1], " ")
			}
			inDecoding = true
			switch field {
			case "event":
				pub.event = value
			case "data":
				pub.data += value + "\n"
			case "id":
				if !strings.ContainsRune(value, 0) {
					pub.id = value
					dec.lastEventID = value
				}
			case "retry":
				pub.retry, _ = strconv.ParseInt(value, 10, 64)
			}
		case err := <-dec.errorCh:
			if err == io.ErrUnexpectedEOF && !inDecoding {
				// if we're not in the middle of an event then just return EOF
				err = io.EOF
			} else if err == io.EOF && inDecoding {
				// if we are in the middle of an event then EOF is unexpected
				err = io.ErrUnexpectedEOF
			}
			return nil, err
		case <-timeoutCh:
			return nil, ErrReadTimeout
		}
	}
	pub.data = strings.TrimSuffix(pub.data, "\n")
	pub.lastEventID = dec.lastEventID
	return pub, nil
}

/**
 * Returns a channel that will receive lines of text as they are read. On any error
 * from the underlying reader, it stops and posts the error to a second channel.
 */
func newLineStreamChannel(r *bufio.Reader) (<-chan string, <-chan error) {
	linesCh := make(chan string)
	errorCh := make(chan error)
	go func() {
		defer close(linesCh)
		defer close(errorCh)
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				errorCh <- err
				return
			}
			linesCh <- line
		}
	}()
	return linesCh, errorCh
}
