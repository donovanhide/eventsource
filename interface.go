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

// Repository is an interface to be used with Server.Register() allowing clients to replay previous events
// through the server, if history is required.
type Repository interface {
	// Gets the Events which should follow on from the specified channel and event id. This method may be called
	// from different goroutines, so it must be safe for concurrent access.
	Replay(channel, id string) chan Event
}

// Logger is the interface for a custom logging implementation that can handle log output for a Stream.
type Logger interface {
	Println(...interface{})
	Printf(string, ...interface{})
}
