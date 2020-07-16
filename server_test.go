package eventsource

import (
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
