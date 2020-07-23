package eventsource

import (
	"context"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type testServerRepository struct{}

func (r *testServerRepository) Replay(channel, id string) chan Event {
	out := make(chan Event, 1)
	var fakeID string
	if id == "" {
		fakeID = "replayed-from-start"
	} else {
		fakeID = "replayed-from-" + id
	}
	out <- &publication{id: fakeID, data: "example"}
	close(out)
	return out
}

func TestNewServerHandlerRespondsAfterClose(t *testing.T) {
	server := NewServer()
	httpServer := httptest.NewServer(server.Handler("test"))
	defer httpServer.Close()

	server.Close()
	responses := make(chan *http.Response)

	go func() {
		resp, err := http.Get(httpServer.URL)
		if err != nil {
			t.Errorf("Unexpected error %s", err)
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

func TestServerHandlerReceivesPublishedEvents(t *testing.T) {
	channel := "test"
	server := NewServer()
	httpServer := httptest.NewServer(server.Handler(channel))
	defer httpServer.Close()

	resp1, err := http.Get(httpServer.URL)
	require.NoError(t, err)
	defer resp1.Body.Close()
	resp2, err := http.Get(httpServer.URL)
	require.NoError(t, err)
	defer resp2.Body.Close()

	event := &publication{data: "my-event"}
	ackCh := server.PublishWithAcknowledgment([]string{channel}, event)
	<-ackCh
	server.Close()

	expected := "data: my-event\n\n"
	body1, err := ioutil.ReadAll(resp1.Body)
	require.NoError(t, err)
	body2, err := ioutil.ReadAll(resp2.Body)
	require.NoError(t, err)
	assert.Equal(t, expected, string(body1))
	assert.Equal(t, expected, string(body2))
}

func TestServerHandlerReceivesPublishedComments(t *testing.T) {
	channel := "test"
	server := NewServer()
	httpServer := httptest.NewServer(server.Handler(channel))
	defer httpServer.Close()

	resp1, err := http.Get(httpServer.URL)
	require.NoError(t, err)
	defer resp1.Body.Close()
	resp2, err := http.Get(httpServer.URL)
	require.NoError(t, err)
	defer resp2.Body.Close()

	server.PublishComment([]string{channel}, "my comment")
	event := &publication{data: "my-event"}
	ackCh := server.PublishWithAcknowledgment([]string{channel}, event)
	<-ackCh
	server.Close()

	expected := ":my comment\ndata: my-event\n\n"
	body1, err := ioutil.ReadAll(resp1.Body)
	require.NoError(t, err)
	body2, err := ioutil.ReadAll(resp2.Body)
	require.NoError(t, err)
	assert.Equal(t, expected, string(body1))
	assert.Equal(t, expected, string(body2))
}

func TestServerHandlerCanReceiveEventsFromRepository(t *testing.T) {
	channel := "test"
	repo := &testServerRepository{}

	t.Run("events are not replayed if ReplayAll is false and Last-Event-Id is not specified", func(t *testing.T) {
		server := NewServer()
		server.Register(channel, repo)

		httpServer := httptest.NewServer(server.Handler(channel))
		defer httpServer.Close()

		resp, err := http.Get(httpServer.URL)
		require.NoError(t, err)
		defer resp.Body.Close()

		server.Close()

		body, err := ioutil.ReadAll(resp.Body)
		require.NoError(t, err)
		assert.Len(t, body, 0)
	})

	t.Run("all events are replayed if ReplayAll is true", func(t *testing.T) {
		server := NewServer()
		server.ReplayAll = true
		server.Register(channel, repo)

		httpServer := httptest.NewServer(server.Handler(channel))
		defer httpServer.Close()

		resp, err := http.Get(httpServer.URL)
		require.NoError(t, err)
		defer resp.Body.Close()

		server.Close()

		body, err := ioutil.ReadAll(resp.Body)
		require.NoError(t, err)
		assert.Equal(t, "id: replayed-from-start\ndata: example\n\n", string(body))
	})

	t.Run("events are replayed selectively if ReplayAll is false and Last-Event-Id is specified", func(t *testing.T) {
		server := NewServer()
		server.Register(channel, repo)

		httpServer := httptest.NewServer(server.Handler(channel))
		defer httpServer.Close()

		req, err := http.NewRequest("GET", httpServer.URL, nil)
		require.NoError(t, err)
		req.Header.Set("Last-Event-Id", "some-id")
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		server.Close()

		body, err := ioutil.ReadAll(resp.Body)
		require.NoError(t, err)
		assert.Equal(t, "id: replayed-from-some-id\ndata: example\n\n", string(body))
	})

	t.Run("repository is no longer used after being unregistered", func(t *testing.T) {
		server := NewServer()
		server.ReplayAll = true
		server.Register(channel, repo)
		server.Unregister(channel, false)

		httpServer := httptest.NewServer(server.Handler(channel))
		defer httpServer.Close()

		resp, err := http.Get(httpServer.URL)
		require.NoError(t, err)
		defer resp.Body.Close()

		server.Close()

		body, err := ioutil.ReadAll(resp.Body)
		require.NoError(t, err)
		assert.Len(t, body, 0)
	})
}

func TestServerCanDisconnectClientsWhenUnregisteringRepository(t *testing.T) {
	channel := "test"
	repo := &testServerRepository{}
	server := NewServer()
	server.Register(channel, repo)

	httpServer := httptest.NewServer(server.Handler(channel))
	defer httpServer.Close()

	resp1, err := http.Get(httpServer.URL)
	require.NoError(t, err)
	defer resp1.Body.Close()
	resp2, err := http.Get(httpServer.URL)
	require.NoError(t, err)
	defer resp2.Body.Close()

	event1 := &publication{data: "my-event1"}
	ackCh := server.PublishWithAcknowledgment([]string{channel}, event1)
	<-ackCh
	server.Unregister(channel, true)

	event2 := &publication{data: "my-event2"}
	ackCh = server.PublishWithAcknowledgment([]string{channel}, event2)
	<-ackCh

	server.Close()

	expected := "data: my-event1\n\n"
	body1, err := ioutil.ReadAll(resp1.Body)
	require.NoError(t, err)
	body2, err := ioutil.ReadAll(resp2.Body)
	require.NoError(t, err)
	assert.Equal(t, expected, string(body1))
	assert.Equal(t, expected, string(body2))
}

func TestServerHandlerHasNoMaxConnectionTimeByDefault(t *testing.T) {
	server := NewServer()
	defer server.Close()
	httpServer := httptest.NewServer(server.Handler("test"))
	defer httpServer.Close()

	resp, err := http.Get(httpServer.URL)
	require.NoError(t, err)
	defer resp.Body.Close()

	readCh := make(chan []byte)
	go func() {
		bytes, _ := ioutil.ReadAll(resp.Body)
		readCh <- bytes
	}()
	select {
	case <-readCh:
		assert.Fail(t, "Unexpectedly got end of response")
	case <-time.After(time.Millisecond * 400):
		break
	}
}

func TestServerHandlerCanEnforceMaxConnectionTime(t *testing.T) {
	server := NewServer()
	server.MaxConnTime = time.Millisecond * 200
	defer server.Close()
	httpServer := httptest.NewServer(server.Handler("test"))
	defer httpServer.Close()

	startTime := time.Now()
	resp, err := http.Get(httpServer.URL)
	require.NoError(t, err)
	defer resp.Body.Close()

	readCh := make(chan []byte)
	go func() {
		bytes, _ := ioutil.ReadAll(resp.Body)
		readCh <- bytes
	}()
	select {
	case <-readCh:
		assert.GreaterOrEqual(t, time.Now().Sub(startTime).Milliseconds(), int64(200))
	case <-time.After(time.Millisecond * 400):
		assert.Fail(t, "Timed out without response being closed")
	}
}

func TestServerHandlerExitsIfClientClosesConnection(t *testing.T) {
	server := NewServer()
	defer server.Close()

	serverHandler := server.Handler("test")
	startedCh := make(chan struct{}, 1)
	endedCh := make(chan struct{}, 1)
	testHandler := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		startedCh <- struct{}{}
		serverHandler.ServeHTTP(w, req)
		endedCh <- struct{}{}
	})

	httpServer := httptest.NewServer(testHandler)
	defer httpServer.Close()

	req, err := http.NewRequest("GET", httpServer.URL, nil)
	require.NoError(t, err)
	ctx, canceller := context.WithCancel(context.Background())
	resp, err := http.DefaultClient.Do(req.WithContext(ctx))
	require.NoError(t, err)
	defer resp.Body.Close()

	select {
	case <-startedCh: // handler has received the request and is blocking for events
		break
	case <-time.After(time.Second):
		require.Fail(t, "timed out waiting for handler to start")
	}

	canceller() // causes the socket to be closed

	select {
	case <-endedCh:
		break
	case <-time.After(time.Second):
		require.Fail(t, "timed out waiting for handler to end")
	}
}
