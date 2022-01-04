// Package eventsource implements a client and server to allow streaming data one-way over a HTTP connection
// using the Server-Sent Events API http://dev.w3.org/html5/eventsource/
//
// The client and server respect the Last-Event-ID header.
// If the Repository interface is implemented on the server, events can be replayed in case of a network disconnection.
package eventsource

// Event is the interface for any event received by the client or sent by the server.
type Event interface {
	// Id is an identifier that can be used to allow a client to replay
	// missed Events by returning the Last-Event-Id header.
	// Return empty string if not required.
	Id() string
	// The name of the event. Return empty string if not required.
	Event() string
	// The payload of the event.
	Data() string
}

// EventWithLastID is an additional interface for an event received by the client,
// allowing access to the LastEventID method.
//
// This is defined as a separate interface for backward compatibility, since this
// feature was added after the Event interface had been defined and adding a method
// to Event would break existing implementations. All events returned by Stream do
// implement this interface, and in a future major version the Event type will be
// changed to always include this field.
type EventWithLastID interface {
	// LastEventID is the value of the `id:` field that was most recently seen in an event
	// from this stream, if any. This differs from Event.Id() in that it retains the same
	// value in subsequent events if they do not provide their own `id:` field.
	LastEventID() string
}

// Repository is an interface to be used with Server.Register() allowing clients to replay previous events
// through the server, if history is required.
type Repository interface {
	// Gets the Events which should follow on from the specified channel and event id. This method may be called
	// from different goroutines, so it must be safe for concurrent access.
	//
	// It is important for the Repository to close the channel after all the necessary events have been
	// written to it. The stream will not be able to proceed to any new events until it has finished consuming
	// the channel that was returned by Replay.
	//
	// Replay may return nil if there are no events to be sent.
	Replay(channel, id string) chan Event
}

// Logger is the interface for a custom logging implementation that can handle log output for a Stream.
type Logger interface {
	Println(...interface{})
	Printf(string, ...interface{})
}

// StreamErrorHandlerResult contains values returned by StreamErrorHandler.
type StreamErrorHandlerResult struct {
	// CloseNow can be set to true to tell the Stream to immediately stop and not retry, as if Close had
	// been called.
	//
	// If CloseNow is false, the Stream will proceed as usual after an error: if there is an existing
	// connection it will retry the connection, and if the Stream is still being initialized then the
	// retry behavior is configurable (see StreamOptionCanRetryFirstConnection).
	CloseNow bool
}

// StreamErrorHandler is a function type used with StreamOptionErrorHandler.
//
// This function will be called whenever Stream encounters either a network error or an HTTP error response
// status. The returned value determines whether Stream should retry as usual, or immediately stop.
//
// The error may be any I/O error returned by Go's networking types, or it may be the eventsource type
// SubscriptionError representing an HTTP error response status.
//
// For errors during initialization of the Stream, this function will be called on the same goroutine that
// called the Subscribe method; for errors on an existing connection, it will be called on a worker
// goroutine. It should return promptly and not block the goroutine.
//
// In this example, the error handler always logs the error with log.Printf, and it forces the stream to
// close permanently if there was an HTTP 401 error:
//
//     func handleError(err error) eventsource.StreamErrorHandlerResult {
//         log.Printf("stream error: %s", err)
//         if se, ok := err.(eventsource.SubscriptionError); ok && se.Code == 401 {
//             return eventsource.StreamErrorHandlerResult{CloseNow: true}
//         }
//         return eventsource.StreamErrorHandlerResult{}
//     }
type StreamErrorHandler func(error) StreamErrorHandlerResult
