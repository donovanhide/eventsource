package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
)

var supportedCapabilities = []string{
	"headers",
	"last-event-id",
	"post",
	"read-timeout",
	"report",
	"restart",
}
var streams = make(map[string]*streamEntity)
var streamCounter = 0
var lock sync.Mutex

type jsonObject map[string]interface{}

type streamOpts struct {
	StreamURL      string            `json:"streamUrl"`
	CallbackURL    string            `json:"callbackURL"`
	Tag            string            `json:"tag"`
	InitialDelayMS *int              `json:"initialDelayMs"`
	LastEventID    string            `json:"lastEventId"`
	Method         string            `json:"method"`
	Body           string            `json:"body"`
	Headers        map[string]string `json:"headers"`
	ReadTimeoutMS  *int              `json:"readTimeoutMs"`
}

type commandParams struct {
	Command string `json:"command"`
}

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case "GET":
			getServiceStatus(w)
		case "POST":
			postCreateStream(w, r)
		case "DELETE":
			fmt.Println("Test service has told us to exit")
			os.Exit(0)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("/streams/", func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimPrefix(r.URL.Path, "/streams/")
		lock.Lock()
		stream := streams[id]
		lock.Unlock()
		if stream == nil {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		switch r.Method {
		case "POST":
			postStreamCommand(stream, w, r)
		case "DELETE":
			deleteStream(stream, id, w)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})
	server := &http.Server{Handler: mux, Addr: ":8000"}
	_ = server.ListenAndServe()
}

func getServiceStatus(w http.ResponseWriter) {
	resp := jsonObject{
		"capabilities": supportedCapabilities,
	}
	data, _ := json.Marshal(resp)
	w.Header().Add("Content-Type", "application/json")
	w.Header().Add("Content-Length", strconv.Itoa(len(data)))
	w.WriteHeader(200)
	_, _ = w.Write(data)
}

func postCreateStream(w http.ResponseWriter, req *http.Request) {
	var opts streamOpts
	if err := json.NewDecoder(req.Body).Decode(&opts); err != nil {
		sendError(w, err)
		return
	}

	e := newStreamEntity(opts)
	lock.Lock()
	streamCounter++
	streamID := strconv.Itoa(streamCounter)
	streams[streamID] = e
	lock.Unlock()

	w.Header().Add("Location", fmt.Sprintf("/streams/%s", streamID))
	w.WriteHeader(http.StatusCreated)
}

func postStreamCommand(stream *streamEntity, w http.ResponseWriter, req *http.Request) {
	var params commandParams
	if err := json.NewDecoder(req.Body).Decode(&params); err != nil {
		sendError(w, err)
		return
	}

	if !stream.doCommand(params.Command) {
		sendError(w, fmt.Errorf("unrecognized command %q", params.Command))
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

func deleteStream(stream *streamEntity, id string, w http.ResponseWriter) {
	stream.close()
	lock.Lock()
	delete(streams, id)
	lock.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

func sendError(w http.ResponseWriter, err error) {
	w.WriteHeader(400)
	_, _ = w.Write([]byte(err.Error()))
}
