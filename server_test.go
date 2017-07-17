package eventsource

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestNewServerHandlerRespondsAfterClose(t *testing.T) {
	server := NewServer()
	httpServer := httptest.NewServer(server.Handler("test"))
	defer httpServer.Close()

	server.Close()
	responses := make(chan *http.Response)

	go func() {
		resp, err := http.Get(httpServer.URL)
		if err != nil {
			t.Fatalf("Unexpected error %s", err)
		}
		responses <- resp
	}()

	select {
	case resp := <-responses:
		if resp.StatusCode != 200 {
			t.Errorf("Received StatusCode %d, want 200", resp.StatusCode)
		}
	case <-time.After(250 * time.Millisecond):
		t.Errorf("Did not receive response in time")
	}
}

func TestSubscriptionChanGetsClosedWhenBufferIsFull(t *testing.T) {
	// This is a regression test to make sure that when the receiving channel is full, we don't have a race condition that can write to a closed channel, causing panic.
	for i := 0; i < 1000; i++ {
		srv := NewServer()
		outChan := make(chan Event, 1)
		testChannelName := "foo"
		sub := &subscription{
			channel: testChannelName,
			out:     outChan,
		}
		srv.subs <- sub
		wg := sync.WaitGroup{}
		wg.Add(1)
		// simulates filling up of buffer
		for i := 0; i < 100; i++ {
			go func() {
				wg.Wait()
				srv.pub <- &outbound{
					channels: []string{testChannelName},
				}
			}()
		}
		wg.Done()
	}
}

func TestIdempotentUnregister(t *testing.T) {
	srv := NewServer()
	outChan := make(chan Event, 1)
	testChannelName := "foo"
	sub := &subscription{
		channel: testChannelName,
		out:     outChan,
	}
	srv.subs <- sub
	// overflow the buffers, so we close the subscription channel
	for i := 0; i < 2; i++ {
		srv.pub <- &outbound{
			channels: []string{testChannelName},
		}
	}
	//simulate client disconnecting
	srv.unregister <- sub
}
