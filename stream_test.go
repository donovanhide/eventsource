package eventsource

import (
	"net"
	"net/http"
	"testing"
)

func TestStreamErrorHandling(t *testing.T) {
	// start mock server
	listener, err := net.Listen("tcp", ":8080")
	if err != nil {
		t.Error("Failed to start fixture server")
	}
	defer listener.Close()
	http.HandleFunc("/stream", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Something wrong.", 500)
	})
	go http.Serve(listener, nil)

	// this is error handling example
	stream, err := Subscribe("http://127.0.0.1:8080/stream", "")

	if err != nil {
		t.Error("failed to subscribe")
	}

	if stream.HttpResponse().StatusCode == 500 {
		// you can handle error as you like based on status code
		return
	}

	t.Error("HTTP respoonse should be 500")
}
