package eventsource

import (
	"net/http"
	"strings"
	"sync"
	"time"
)

type subscription struct {
	channel     string
	lastEventID string
	out         chan<- eventOrComment
}

type eventOrComment interface{}

type outbound struct {
	channels       []string
	eventOrComment eventOrComment
	ackCh          chan<- struct{}
}

type registration struct {
	channel    string
	repository Repository
}

type unregistration struct {
	channel         string
	forceDisconnect bool
}

type comment struct {
	value string
}

type eventBatch struct {
	events <-chan Event
}

// Server manages any number of event-publishing channels and allows subscribers to consume them.
// To use it within an HTTP server, create a handler for each channel with Handler().
type Server struct {
	AllowCORS       bool          // Enable all handlers to be accessible from any origin
	ReplayAll       bool          // Replay repository even if there's no Last-Event-Id specified
	BufferSize      int           // How many messages do we let the client get behind before disconnecting
	Gzip            bool          // Enable compression if client can accept it
	MaxConnTime     time.Duration // If non-zero, HTTP connections will be automatically closed after this time
	Logger          Logger        // Logger is a logger that, when set, will be used for logging debug messages
	registrations   chan *registration
	unregistrations chan *unregistration
	pub             chan *outbound
	subs            chan *subscription
	unsubs          chan *subscription
	quit            chan bool
	isClosed        bool
	isClosedMutex   sync.RWMutex
}

// NewServer creates a new Server instance.
func NewServer() *Server {
	srv := &Server{
		registrations:   make(chan *registration),
		unregistrations: make(chan *unregistration),
		pub:             make(chan *outbound),
		subs:            make(chan *subscription),
		unsubs:          make(chan *subscription, 2),
		quit:            make(chan bool),
		BufferSize:      128,
	}
	go srv.run()
	return srv
}

// Close permanently shuts down the Server. It will no longer allow new subscriptions.
func (srv *Server) Close() {
	srv.quit <- true
	srv.markServerClosed()
}

// Handler creates a new HTTP handler for serving a specified channel.
//
// The channel does not have to have been previously registered with Register, but if it has been, the
// handler may replay events from the registered Repository depending on the setting of server.ReplayAll
// and the Last-Event-Id header of the request.
func (srv *Server) Handler(channel string) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		h := w.Header()
		h.Set("Content-Type", "text/event-stream; charset=utf-8")
		h.Set("Cache-Control", "no-cache, no-store, must-revalidate")
		h.Set("Connection", "keep-alive")
		if srv.AllowCORS {
			h.Set("Access-Control-Allow-Origin", "*")
		}
		useGzip := srv.Gzip && strings.Contains(req.Header.Get("Accept-Encoding"), "gzip")
		if useGzip {
			h.Set("Content-Encoding", "gzip")
		}
		w.WriteHeader(http.StatusOK)

		// If the Handler is still active even though the server is closed, stop here.
		// Otherwise the Handler will block while publishing to srv.subs indefinitely.
		if srv.isServerClosed() {
			return
		}

		var maxConnTimeCh <-chan time.Time
		if srv.MaxConnTime > 0 {
			t := time.NewTimer(srv.MaxConnTime)
			defer t.Stop()
			maxConnTimeCh = t.C
		}

		eventCh := make(chan eventOrComment, srv.BufferSize)
		sub := &subscription{
			channel:     channel,
			lastEventID: req.Header.Get("Last-Event-ID"),
			out:         eventCh,
		}
		srv.subs <- sub
		flusher := w.(http.Flusher)
		flusher.Flush()
		enc := NewEncoder(w, useGzip)

		writeEventOrComment := func(ec eventOrComment) bool {
			if err := enc.Encode(ec); err != nil {
				srv.unsubs <- sub
				if srv.Logger != nil {
					srv.Logger.Println(err)
				}
				return false // if this happens, we'll end the handler early because something's clearly broken
			}
			flusher.Flush()
			return true
		}

		// The logic below works as follows:
		// - Normally, the handler is reading from eventCh. Server.run() accesses this channel through sub.out
		//   and sends published events to it.
		// - However, if a Repository is being used, the Server might get a whole batch of events that the
		//   Repository provides through its Replay method. The Repository provides these in the form of a
		//   channel that it writes to. Since we don't know how many events there will be or how long it will
		//   take to write them, we do not want to block Server.run() for this.
		// - Previous implementations of sending events from Replay used a separate goroutine. That was unsafe,
		//   due to a race condition where Server.run() might close the channel while the Replay goroutine is
		//   still writing to it.
		// - So, instead, Server.run() now takes the channel from Replay and wraps it in an eventBatch. When
		//   the handler sees an eventBatch, it switches over to reading events from that channel until the
		//   channel is closed. Then it switches back to reading events from the regular channel.
		// - The Server can close eventCh at any time to indicate that the stream is done. The handler exits.
		// - If the client closes the connection, or if MaxConnTime elapses, the handler exits after telling
		//   the Server to stop publishing events to it.

		var readMainCh <-chan eventOrComment = eventCh
		var readBatchCh <-chan Event
		closedNormally := false
		closeNotify := req.Context().Done()

	ReadLoop:
		for {
			select {
			case <-closeNotify:
				break ReadLoop
			case <-maxConnTimeCh: // if MaxConnTime was not set, this is a nil channel and has no effect on the select
				break ReadLoop
			case ev, ok := <-readMainCh:
				if !ok {
					closedNormally = true
					break ReadLoop
				}
				if batch, ok := ev.(eventBatch); ok {
					readBatchCh = batch.events
					readMainCh = nil
				} else if !writeEventOrComment(ev) {
					break ReadLoop
				}
			case ev, ok := <-readBatchCh:
				if !ok { // end of batch
					readBatchCh = nil
					readMainCh = eventCh
				} else if !writeEventOrComment(ev) {
					break ReadLoop
				}
			}
		}
		if !closedNormally {
			srv.unsubs <- sub // the server didn't tell us to close, so we must tell it that we're closing
		}
	}
}

// Register registers a Repository to be used for the specified channel. The Repository will be used to
// determine whether new subscribers should receive data that was generated before they subscribed.
//
// Channels do not have to be registered unless you want to specify a Repository. An unregistered channel can
// still be subscribed to with Handler, and published to with Publish.
func (srv *Server) Register(channel string, repo Repository) {
	srv.registrations <- &registration{
		channel:    channel,
		repository: repo,
	}
}

// Unregister removes a channel registration that was created by Register. If forceDisconnect is true, it also
// causes all currently active handlers for that channel to close their connections. If forceDisconnect is false,
// those connections will remain open until closed by their clients but will not receive any more events.
//
// This will not prevent creating new channel subscriptions for the same channel with Handler, or publishing
// events to that channel with Publish. It is the caller's responsibility to avoid using channels that are no
// longer supposed to be used.
func (srv *Server) Unregister(channel string, forceDisconnect bool) {
	srv.unregistrations <- &unregistration{
		channel:         channel,
		forceDisconnect: forceDisconnect,
	}
}

// Publish publishes an event to one or more channels.
func (srv *Server) Publish(channels []string, ev Event) {
	srv.pub <- &outbound{
		channels:       channels,
		eventOrComment: ev,
	}
}

// PublishWithAcknowledgment publishes an event to one or more channels, returning a channel that will receive
// a value after the event has been processed by the server.
//
// This can be used to ensure a well-defined ordering of operations. Since each Server method is handled
// asynchronously via a separate channel, if you call server.Publish and then immediately call server.Close,
// there is no guarantee that the server execute the Close operation only after the event has been published.
// If you instead call PublishWithAcknowledgement, and then read from the returned channel before calling
// Close, you can be sure that the event was published before the server was closed.
func (srv *Server) PublishWithAcknowledgment(channels []string, ev Event) <-chan struct{} {
	ackCh := make(chan struct{}, 1)
	srv.pub <- &outbound{
		channels:       channels,
		eventOrComment: ev,
		ackCh:          ackCh,
	}
	return ackCh
}

// PublishComment publishes a comment to one or more channels.
func (srv *Server) PublishComment(channels []string, text string) {
	srv.pub <- &outbound{
		channels:       channels,
		eventOrComment: comment{value: text},
	}
}

func (srv *Server) run() {
	// All access to the subs and repos maps is done from the same goroutine, so modifications are safe.
	subs := make(map[string]map[*subscription]struct{})
	repos := make(map[string]Repository)
	trySend := func(sub *subscription, ec eventOrComment) {
		if !sub.send(ec) {
			sub.close()
			delete(subs[sub.channel], sub)
		}
	}
	for {
		select {
		case reg := <-srv.registrations:
			repos[reg.channel] = reg.repository
		case unreg := <-srv.unregistrations:
			delete(repos, unreg.channel)
			previousSubs := subs[unreg.channel]
			delete(subs, unreg.channel)
			if unreg.forceDisconnect {
				for s := range previousSubs {
					s.close()
				}
			}
		case sub := <-srv.unsubs:
			delete(subs[sub.channel], sub)
		case pub := <-srv.pub:
			for _, c := range pub.channels {
				for s := range subs[c] {
					trySend(s, pub.eventOrComment)
				}
			}
			if pub.ackCh != nil {
				select {
				// It shouldn't be possible for this channel to block since it is created for a single use, but
				// we'll do a non-blocking push just to be safe
				case pub.ackCh <- struct{}{}:
				default:
				}
			}
		case sub := <-srv.subs:
			if _, ok := subs[sub.channel]; !ok {
				subs[sub.channel] = make(map[*subscription]struct{})
			}
			subs[sub.channel][sub] = struct{}{}
			if srv.ReplayAll || len(sub.lastEventID) > 0 {
				repo, ok := repos[sub.channel]
				if ok {
					batchCh := repo.Replay(sub.channel, sub.lastEventID)
					if batchCh != nil {
						trySend(sub, eventBatch{events: batchCh})
					}
				}
			}
		case <-srv.quit:
			for _, sub := range subs {
				for s := range sub {
					s.close()
				}
			}
			return
		}
	}
}

func (srv *Server) isServerClosed() bool {
	srv.isClosedMutex.RLock()
	defer srv.isClosedMutex.RUnlock()
	return srv.isClosed
}

func (srv *Server) markServerClosed() {
	srv.isClosedMutex.Lock()
	defer srv.isClosedMutex.Unlock()
	srv.isClosed = true
}

// Attempts to send an event or comment to the subscription's channel.
//
// We do not want to block the main Server goroutine, so this is a non-blocking send. If it fails,
// we return false to tell the Server that the subscriber has fallen behind and should be removed;
// we also immediately close the channel in that case. If the send succeeds-- or if we didn't need
// to attempt a send, because the channel was already closed-- we return true.
//
// This should be called only from the Server.run() goroutine.
func (s *subscription) send(e eventOrComment) bool {
	if s.out == nil {
		return true
	}
	select {
	case s.out <- e:
		return true
	default:
		s.close()
		return false
	}
}

// Closes a subscription's channel and sets it to nil.
//
// This should be called only from the Server.run() goroutine.
func (s *subscription) close() {
	close(s.out)
	s.out = nil
}
